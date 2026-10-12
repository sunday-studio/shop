import Foundation
import XCTest
@testable import Shop

/// A transport that answers from a closure and records what was asked, so sync code runs for real
/// without a server. Return a status and a JSON body, or throw URLError to play "offline".
@MainActor
final class ScriptedTransport: HTTPTransport {
    struct Call: Equatable { let method: String; let path: String; let query: String; let body: String }
    private(set) var calls: [Call] = []
    var handler: (Call) throws -> (Int, String)

    init(_ handler: @escaping (Call) throws -> (Int, String)) { self.handler = handler }

    func send(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let url = request.url!
        let call = Call(method: request.httpMethod ?? "GET", path: url.path, query: url.query ?? "", body: request.httpBody.flatMap { String(data: $0, encoding: .utf8) } ?? "")
        calls.append(call)
        let (status, body) = try handler(call)
        let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: ["Content-Type": "application/json"])!
        return (Data(body.utf8), response)
    }

    func paths(_ method: String? = nil) -> [String] { calls.filter { method == nil || $0.method == method }.map { "\($0.method) \($0.path)" } }
}

enum JSON {
    static func item(_ id: Int, _ text: String, _ group: Int?) -> String {
        "{\"id\":\(id),\"text\":\"\(text)\",\"created_at\":\"2026-10-11T10:00:00Z\"" + (group.map { ",\"group_id\":\($0)" } ?? "") + "}"
    }
    static func changes(items: [String] = [], groups: [String] = [], deleted: [Int] = [], deletedGroups: [Int] = [], rev: Int, full: Bool = false) -> String {
        "{\"items\":[\(items.joined(separator: ","))],\"deleted_ids\":\(deleted),\"groups\":[\(groups.joined(separator: ","))],\"deleted_group_ids\":\(deletedGroups),\"rev\":\(rev),\"server_time\":\"2026-10-11T10:00:00Z\",\"full_resync\":\(full),\"suggestions\":[]}"
    }
    static func group(_ id: Int, _ name: String) -> String {
        "{\"id\":\(id),\"name\":\"\(name)\",\"kind\":\"checklist\",\"source\":\"user\",\"sort\":1,\"created_at\":\"2026-10-11T10:00:00Z\",\"updated_at\":\"2026-10-11T10:00:00Z\",\"rev\":1,\"item_count\":0,\"checked_count\":0}"
    }
}

/// A fresh list store on a scripted server. Clears what earlier runs left on disk.
@MainActor
func makeListStore(_ handler: @escaping (ScriptedTransport.Call) throws -> (Int, String)) -> (ShoppingListStore, ScriptedTransport) {
    let transport = ScriptedTransport(handler)
    let api = APIClient(transport: transport, baseURL: URL(string: "http://test.local"), token: "t")
    let store = ShoppingListStore(api: api, catalog: Catalog(api: api))
    store.reset()
    return (store, transport)
}
