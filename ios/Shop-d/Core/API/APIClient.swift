import Foundation
import Observation

// MARK: - Errors

nonisolated enum APIError: Error, Sendable, Equatable {
    case notConfigured
    case invalidURL
    /// No network at all (airplane mode, no Wi-Fi).
    case offline
    /// Network is up but the server didn't answer.
    case unreachable
    /// Token rejected (or wrong password on login).
    case unauthorized
    /// Too many attempts (login lockout).
    case rateLimited(String?)
    /// 409: duplicate receipt, barcode in use, protected reparse…
    case conflict(Problem)
    case notFound
    case problem(Problem)
    case http(Int)
    case decoding(String)

    var message: String {
        switch self {
        case .notConfigured: "Connect to your server first."
        case .invalidURL: "That doesn't look like a server address."
        case .offline: "You're offline."
        case .unreachable: "The server didn't answer."
        case .unauthorized: "This iPhone isn't signed in any more."
        case .rateLimited(let d): d ?? "Too many attempts. Wait a few minutes and try again."
        case .conflict(let p): p.detail ?? p.title
        case .notFound: "Not found."
        case .problem(let p): p.detail ?? p.title
        case .http(let code): "The server answered with an error (\(code))."
        case .decoding: "The server sent something this app doesn't understand."
        }
    }

    /// Network-level failures that a retry later may fix.
    var isConnectivity: Bool { self == .offline || self == .unreachable }
}

// MARK: - Transport

/// The HTTP layer. `URLSessionTransport` in the app; `MockServer` for previews and -mock.
@MainActor
protocol HTTPTransport: AnyObject {
    func send(_ request: URLRequest) async throws -> (Data, HTTPURLResponse)
}

@MainActor
final class URLSessionTransport: HTTPTransport {
    private let session: URLSession

    init() {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 20
        config.waitsForConnectivity = false
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        session = URLSession(configuration: config)
    }

    func send(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw APIError.http(0) }
        return (data, http)
    }
}

// MARK: - Coders

nonisolated enum Coders {
    static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let s = try c.decode(String.self)
            if let date = Dates.parseRFC3339(s) ?? Dates.parseIsoDay(s) { return date }
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "Bad date \(s)")
        }
        return d
    }

    static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        e.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(Dates.rfc3339(date))
        }
        return e
    }
}

// MARK: - Client

/// Talks to the Shop server. Holds the base URL and bearer token, maps errors, and tracks
/// whether the server is reachable (for the sync pill).
@MainActor
@Observable
final class APIClient {
    enum Reachability: Equatable {
        case unknown, online, offline, unreachable
    }

    private(set) var baseURL: URL?
    private(set) var token: String?
    private(set) var reachability: Reachability = .unknown
    private(set) var lastSuccess: Date?
    /// Called once when the server rejects the token (401 outside login).
    @ObservationIgnored var onUnauthorized: (() -> Void)?

    @ObservationIgnored let transport: HTTPTransport
    @ObservationIgnored private let decoder = Coders.decoder()
    @ObservationIgnored private let encoder = Coders.encoder()

    var isMock: Bool { transport is MockServer }

    init(transport: HTTPTransport = URLSessionTransport(), baseURL: URL? = nil, token: String? = nil) {
        self.transport = transport
        self.baseURL = baseURL.map(Self.normalized)
        self.token = token
        configureImageAuth()
    }

    /// Points the client at a server (and optionally a token).
    func configure(baseURL: URL?, token: String?) {
        self.baseURL = baseURL.map(Self.normalized)
        self.token = token
        if baseURL == nil { reachability = .unknown }
        configureImageAuth()
    }

    func setToken(_ token: String?) {
        self.token = token
        configureImageAuth()
    }

    /// Drops a trailing slash so paths join cleanly.
    static func normalized(_ url: URL) -> URL {
        var s = url.absoluteString
        while s.hasSuffix("/") { s.removeLast() }
        return URL(string: s) ?? url
    }

    // MARK: Requests

    func get<T: Decodable>(_ path: String, query: [String: String?] = [:], as type: T.Type = T.self) async throws -> T {
        let data = try await raw("GET", path, query: query)
        return try decode(data)
    }

    func send<T: Decodable, B: Encodable>(_ method: String, _ path: String, body: B, query: [String: String?] = [:], as type: T.Type = T.self) async throws -> T {
        let data = try await raw(method, path, query: query, body: try encoder.encode(body), contentType: "application/json")
        return try decode(data)
    }

    func send<T: Decodable>(_ method: String, _ path: String, query: [String: String?] = [:], as type: T.Type = T.self) async throws -> T {
        let data = try await raw(method, path, query: query)
        return try decode(data)
    }

    /// For endpoints that answer 204.
    func perform(_ method: String, _ path: String, query: [String: String?] = [:]) async throws {
        _ = try await raw(method, path, query: query)
    }

    func perform<B: Encodable>(_ method: String, _ path: String, body: B) async throws {
        _ = try await raw(method, path, body: try encoder.encode(body), contentType: "application/json")
    }

    func upload<T: Decodable>(_ method: String, _ path: String, multipart: Multipart, as type: T.Type = T.self) async throws -> T {
        let data = try await raw(method, path, body: multipart.data, contentType: multipart.contentType, timeout: 120)
        return try decode(data)
    }

    /// Full URL for a path (images, logos).
    func url(_ path: String, query: [String: String?] = [:]) -> URL? {
        guard let baseURL else { return nil }
        let trimmed = path.hasPrefix("/") ? String(path.dropFirst()) : path
        var components = URLComponents(string: baseURL.absoluteString + "/" + trimmed)
        let items = query.compactMap { key, value in value.map { URLQueryItem(name: key, value: $0) } }
        if !items.isEmpty { components?.queryItems = items.sorted { $0.name < $1.name } }
        return components?.url
    }

    /// Builds an authorised request (also used by the background upload queue).
    func request(_ method: String, _ path: String, query: [String: String?] = [:], contentType: String? = nil, timeout: TimeInterval = 20) throws -> URLRequest {
        guard baseURL != nil else { throw APIError.notConfigured }
        guard let url = url(path, query: query) else { throw APIError.invalidURL }
        var req = URLRequest(url: url, timeoutInterval: timeout)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let contentType { req.setValue(contentType, forHTTPHeaderField: "Content-Type") }
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        return req
    }

    @discardableResult
    func raw(
        _ method: String,
        _ path: String,
        query: [String: String?] = [:],
        body: Data? = nil,
        contentType: String? = nil,
        timeout: TimeInterval = 20,
        acceptStatus: Set<Int> = []
    ) async throws -> Data {
        var req = try request(method, path, query: query, contentType: contentType, timeout: timeout)
        req.httpBody = body

        let result: (Data, HTTPURLResponse)
        do {
            result = try await transport.send(req)
        } catch let error as URLError {
            let mapped = Self.map(error)
            reachability = mapped == .offline ? .offline : .unreachable
            throw mapped
        } catch let error as APIError {
            throw error
        } catch {
            reachability = .unreachable
            throw APIError.unreachable
        }

        let data = result.0
        let status = result.1.statusCode
        reachability = .online
        lastSuccess = .now
        if (200..<300).contains(status) || acceptStatus.contains(status) { return data }

        let problem = (try? decoder.decode(Problem.self, from: data))
            ?? Problem(type: nil, title: HTTPURLResponse.localizedString(forStatusCode: status), status: status, detail: nil)
        switch status {
        case 401:
            if !path.hasPrefix("/v1/auth/pair") { onUnauthorized?() }
            throw APIError.unauthorized
        case 404: throw APIError.notFound
        case 409: throw APIError.conflict(problem)
        case 429: throw APIError.rateLimited(problem.detail)
        default:
            throw status >= 500 && problem.detail == nil ? APIError.http(status) : APIError.problem(problem)
        }
    }

    func decode<T: Decodable>(_ data: Data) throws -> T {
        if T.self == EmptyResponse.self, let empty = EmptyResponse() as? T { return empty }
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }

    func markOffline() { reachability = .offline }

    private static func map(_ error: URLError) -> APIError {
        switch error.code {
        case .notConnectedToInternet, .networkConnectionLost, .dataNotAllowed, .internationalRoamingOff:
            return .offline
        case .badURL, .unsupportedURL:
            return .invalidURL
        default:
            return .unreachable
        }
    }

    private func configureImageAuth() {
        let token = self.token
        let host = baseURL?.host()
        ImageCache.shared.authorize = { request in
            var r = request
            if let token, let host, r.url?.host() == host {
                r.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
            }
            return r
        }
    }
}

/// Decodes from an empty body.
nonisolated struct EmptyResponse: Decodable, Sendable {
    init() {}
    init(from decoder: Decoder) throws {}
}

// MARK: - Multipart

nonisolated struct Multipart: Sendable {
    let boundary = "shop-" + UUID().uuidString
    private(set) var data = Data()

    var contentType: String { "multipart/form-data; boundary=\(boundary)" }

    mutating func add(field name: String, value: String) {
        data.append("--\(boundary)\r\n".data(using: .utf8)!)
        data.append("Content-Disposition: form-data; name=\"\(name)\"\r\n\r\n".data(using: .utf8)!)
        data.append("\(value)\r\n".data(using: .utf8)!)
    }

    mutating func add(file name: String, filename: String, mimeType: String, data fileData: Data) {
        data.append("--\(boundary)\r\n".data(using: .utf8)!)
        data.append("Content-Disposition: form-data; name=\"\(name)\"; filename=\"\(filename)\"\r\n".data(using: .utf8)!)
        data.append("Content-Type: \(mimeType)\r\n\r\n".data(using: .utf8)!)
        data.append(fileData)
        data.append("\r\n".data(using: .utf8)!)
    }

    mutating func finish() {
        data.append("--\(boundary)--\r\n".data(using: .utf8)!)
    }
}
