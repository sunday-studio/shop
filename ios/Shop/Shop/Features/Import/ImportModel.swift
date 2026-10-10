import CryptoKit
import Foundation
import Observation
import PDFKit
import PhotosUI
import SwiftUI
import UIKit

/// Where an import's pictures come from.
enum ImportSource: String, Identifiable {
    case photos, files
    var id: String { rawValue }
}

/// Switches from `docs/DESIGN-UPLOAD-FLOW.md` that depend on the backend.
enum ImportFlags {
    /// "Let Shop find out": upload without a shop. Off until the server accepts that (§13.1).
    static let letShopFindOut = true
    /// Send `photo_taken_at` with an upload. Off until the server has the field (§13.3).
    static let sendPhotoTakenAt = true
}

/// State of the Import screen (S3): the picked pictures, how they are grouped into receipts,
/// and the shop and date for each. Nothing here touches the network.
@MainActor
@Observable
final class ImportModel: Identifiable {
    nonisolated static let maxPages = 30
    nonisolated static let maxPagesPerReceipt = 5

    enum DateChoice: Equatable {
        case fromReceipt, photo
        case picked(Date)
    }

    enum Grouping: Hashable {
        case separate, oneLong
    }

    /// What straightening found for a picked image.
    enum Straighten: Equatable {
        case notApplicable        // PDF pages, or the setting is off
        case straightened         // outline found, corrected in the preview
        case square               // already square: untouched
        case notFound             // "Edges not found · Adjust"
        case adjusted             // set by hand
        case original             // "Use original"
    }

    struct Page: Identifiable {
        let id = UUID()
        var data: Data
        var fileExtension: String
        var thumbnail: UIImage
        var takenAt: Date?
        /// Pages of one PDF stay together when the grouping is "Separate receipts".
        var groupKey: UUID
        var isPDFPage: Bool
        var straighten: Straighten
        var quad: StoredQuad?
        var rotation = 0
    }

    struct Receipt: Identifiable {
        let id = UUID()
        var pages: [Page]
        var shopId: Int64?
        var date: DateChoice = .fromReceipt
    }

    struct Unreadable: Identifiable {
        let id = UUID()
        var name: String
    }

    private(set) var receipts: [Receipt] = []
    private(set) var unreadable: [Unreadable] = []
    private(set) var isLoading = true
    private(set) var cappedAtLimit = false
    private(set) var skippedDuplicate = false
    var grouping: Grouping = .separate
    var source: UploadItem.Source = .photos
    var batchShopId: Int64?
    /// True once the user changed grouping, shop or date: closing then asks first.
    private(set) var touched = false

    @ObservationIgnored private var seen: Set<String> = []
    @ObservationIgnored private var all: [Page] = []

    var pageCount: Int { receipts.reduce(0) { $0 + $1.pages.count } }
    var canJoinAll: Bool { pageCount <= Self.maxPagesPerReceipt }
    var isEmpty: Bool { receipts.isEmpty && unreadable.isEmpty && !isLoading }

    /// A shop is required for every receipt until the server can find it out.
    func missingShops(_ defaultShopId: Int64?) -> Int {
        receipts.filter { ($0.shopId ?? batchShopId ?? defaultShopId) == nil }.count
    }

    // MARK: Loading

    private struct Loaded: @unchecked Sendable {
        var data: Data
        var ext: String
        var thumbnail: UIImage?
        var takenAt: Date?
        var quad: StoredQuad?
        var status: Straighten
        var pdfPages: [Loaded] = []
        var name: String
    }

    func load(photos: [PhotosPickerItem]) async {
        var datas: [(Data, String)] = []
        for item in photos {
            if let data = try? await item.loadTransferable(type: Data.self) {
                let ext = item.supportedContentTypes.first?.preferredFilenameExtension ?? "jpg"
                datas.append((data, ext))
            } else {
                unreadable.append(Unreadable(name: "Photo"))
            }
        }
        await ingest(datas.map { ($0.0, $0.1, "Photo") })
    }

    func load(files urls: [URL]) async {
        var datas: [(Data, String, String)] = []
        for url in urls {
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            if let data = try? Data(contentsOf: url) {
                datas.append((data, url.pathExtension.lowercased(), url.lastPathComponent))
            } else {
                unreadable.append(Unreadable(name: url.lastPathComponent))
            }
        }
        await ingest(datas)
    }

    /// Decodes in the background, three at a time, and adds rows in picked order.
    private func ingest(_ inputs: [(Data, String, String)]) async {
        let straighten = Straightener.isEnabled
        var index = 0
        while index < inputs.count {
            let window = Array(inputs[index..<min(index + 3, inputs.count)])
            index += 3
            let results: [Loaded?] = await withTaskGroup(of: (Int, Loaded?).self) { group in
                for (offset, input) in window.enumerated() {
                    group.addTask { (offset, Self.decode(input.0, ext: input.1, name: input.2, straighten: straighten)) }
                }
                var out = [Loaded?](repeating: nil, count: window.count)
                for await (offset, loaded) in group { out[offset] = loaded }
                return out
            }
            for (offset, result) in results.enumerated() {
                guard let result else {
                    unreadable.append(Unreadable(name: window[offset].2))
                    continue
                }
                add(result)
            }
            rebuild(keepManual: !receipts.isEmpty && index > 3)
        }
        isLoading = false
        if receipts.count == 1 && batchShopId == nil { batchShopId = nil }
    }

    private func add(_ loaded: Loaded) {
        let pages = loaded.pdfPages.isEmpty ? [loaded] : loaded.pdfPages
        let key = UUID()
        for item in pages {
            guard pageCountWithPending < Self.maxPages else { cappedAtLimit = true; return }
            let hash = SHA256.hash(data: item.data).map { String(format: "%02x", $0) }.joined()
            guard !seen.contains(hash) else { skippedDuplicate = true; continue }
            seen.insert(hash)
            guard let thumb = item.thumbnail else { continue }
            all.append(Page(data: item.data, fileExtension: item.ext, thumbnail: thumb, takenAt: item.takenAt,
                            groupKey: loaded.pdfPages.isEmpty ? UUID() : key, isPDFPage: !loaded.pdfPages.isEmpty,
                            straighten: item.status, quad: item.quad))
        }
    }

    private var pageCountWithPending: Int { all.count }

    nonisolated private static func decode(_ data: Data, ext: String, name: String, straighten: Bool) -> Loaded? {
        if ext == "pdf" || data.prefix(4) == Data("%PDF".utf8) {
            guard let document = PDFDocument(data: data), document.pageCount > 0 else { return nil }
            var pages: [Loaded] = []
            for i in 0..<min(document.pageCount, maxPages) {
                guard let page = document.page(at: i), let jpeg = render(page),
                      let cg = Straightener.upright(jpeg, maxSide: 420) else { continue }
                pages.append(Loaded(data: jpeg, ext: "jpg", thumbnail: UIImage(cgImage: cg), takenAt: nil, quad: nil, status: .notApplicable, name: name))
            }
            guard !pages.isEmpty else { return nil }
            return Loaded(data: data, ext: "pdf", thumbnail: pages[0].thumbnail, takenAt: nil, quad: nil, status: .notApplicable, pdfPages: pages, name: name)
        }
        guard let big = Straightener.upright(data, maxSide: 1024) else { return nil }
        var status: Straighten = .notApplicable
        var quad: StoredQuad?
        var preview = big
        if straighten {
            switch Straightener.detect(in: big) {
            case .quad(let q):
                if let corrected = Straightener.apply(q, to: big, maxSide: 1024) { preview = corrected; quad = q; status = .straightened }
                else { status = .notFound }
            case .square: status = .square
            case .notFound: status = .notFound
            }
        }
        guard let thumb = downscale(preview, maxSide: 420) else { return nil }
        let normalized = ext.isEmpty ? "jpg" : ext
        return Loaded(data: data, ext: normalized, thumbnail: UIImage(cgImage: thumb), takenAt: Straightener.captureDate(data),
                      quad: quad, status: status, name: name)
    }

    nonisolated private static func downscale(_ image: CGImage, maxSide: CGFloat) -> CGImage? {
        let long = CGFloat(max(image.width, image.height))
        guard long > maxSide else { return image }
        let scale = maxSide / long
        let size = CGSize(width: CGFloat(image.width) * scale, height: CGFloat(image.height) * scale)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        return UIGraphicsImageRenderer(size: size, format: format).image { _ in
            UIImage(cgImage: image).draw(in: CGRect(origin: .zero, size: size))
        }.cgImage
    }

    /// A PDF page as a white-background JPEG at 200 dpi.
    nonisolated private static func render(_ page: PDFPage) -> Data? {
        let bounds = page.bounds(for: .mediaBox)
        guard bounds.width > 0, bounds.height > 0 else { return nil }
        let scale = 200.0 / 72.0
        let size = CGSize(width: bounds.width * scale, height: bounds.height * scale)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        return UIGraphicsImageRenderer(size: size, format: format).jpegData(withCompressionQuality: 0.9) { ctx in
            UIColor.white.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
            ctx.cgContext.translateBy(x: 0, y: size.height)
            ctx.cgContext.scaleBy(x: scale, y: -scale)
            page.draw(with: .mediaBox, to: ctx.cgContext)
        }
    }

    // MARK: Grouping

    /// Lays `all` out as receipts for the current grouping. While more photos are still loading,
    /// receipts the user already shaped by hand are kept.
    private func rebuild(keepManual: Bool = false) {
        let loadedIds = Set(receipts.flatMap { $0.pages.map(\.id) })
        if keepManual {
            let fresh = all.filter { !loadedIds.contains($0.id) }
            receipts += layout(fresh)
        } else {
            receipts = layout(all)
        }
    }

    private func layout(_ pages: [Page]) -> [Receipt] {
        switch grouping {
        case .separate:
            var out: [Receipt] = []
            var lastKey: UUID?
            for page in pages {
                if page.isPDFPage, page.groupKey == lastKey, let i = out.indices.last, out[i].pages.count < Self.maxPagesPerReceipt {
                    out[i].pages.append(page)
                } else {
                    out.append(Receipt(pages: [page]))
                }
                lastKey = page.isPDFPage ? page.groupKey : nil
            }
            return out
        case .oneLong:
            return stride(from: 0, to: pages.count, by: Self.maxPagesPerReceipt).map {
                Receipt(pages: Array(pages[$0..<min($0 + Self.maxPagesPerReceipt, pages.count)]))
            }
        }
    }

    func setGrouping(_ value: Grouping) {
        guard value != grouping else { return }
        if value == .oneLong && !canJoinAll { return }
        grouping = value
        touched = true
        let flat = receipts.flatMap(\.pages)
        withAnimation(Motion.spring) { receipts = layout(flat) }
    }

    func canJoin(after index: Int) -> Bool {
        guard receipts.indices.contains(index), receipts.indices.contains(index + 1) else { return false }
        return receipts[index].pages.count + receipts[index + 1].pages.count <= Self.maxPagesPerReceipt
    }

    func join(after index: Int) {
        guard canJoin(after: index) else { return }
        touched = true
        withAnimation(Motion.spring) {
            receipts[index].pages += receipts[index + 1].pages
            receipts.remove(at: index + 1)
        }
    }

    func remove(_ id: UUID) {
        touched = true
        withAnimation(Motion.collapse) { receipts.removeAll { $0.id == id } }
    }

    func removePage(_ pageId: UUID, from receiptId: UUID) {
        guard let r = receipts.firstIndex(where: { $0.id == receiptId }) else { return }
        touched = true
        withAnimation(Motion.spring) {
            receipts[r].pages.removeAll { $0.id == pageId }
            if receipts[r].pages.isEmpty { receipts.remove(at: r) }
        }
    }

    func removeUnreadable(_ id: UUID) {
        withAnimation(Motion.collapse) { unreadable.removeAll { $0.id == id } }
    }

    func movePage(in receiptId: UUID, from source: IndexSet, to destination: Int) {
        guard let r = receipts.firstIndex(where: { $0.id == receiptId }) else { return }
        receipts[r].pages.move(fromOffsets: source, toOffset: destination)
    }

    func setShop(_ shopId: Int64?, for receiptId: UUID) {
        guard let r = receipts.firstIndex(where: { $0.id == receiptId }) else { return }
        touched = true
        receipts[r].shopId = shopId
    }

    func setBatchShop(_ shopId: Int64?) {
        touched = true
        batchShopId = shopId
        for i in receipts.indices { receipts[i].shopId = nil }
    }

    func setDate(_ choice: DateChoice, for receiptId: UUID) {
        guard let r = receipts.firstIndex(where: { $0.id == receiptId }) else { return }
        touched = true
        receipts[r].date = choice
    }

    func setDateForAll(_ choice: DateChoice) {
        touched = true
        for i in receipts.indices { receipts[i].date = choice }
    }

    func updatePage(_ pageId: UUID, in receiptId: UUID, _ change: (inout Page) -> Void) {
        guard let r = receipts.firstIndex(where: { $0.id == receiptId }),
              let p = receipts[r].pages.firstIndex(where: { $0.id == pageId }) else { return }
        touched = true
        change(&receipts[r].pages[p])
    }

    func receipt(_ id: UUID) -> Receipt? { receipts.first { $0.id == id } }

    // MARK: Display helpers

    func title(at index: Int) -> String {
        let receipt = receipts[index]
        return receipt.pages.count > 1 ? "Receipt \(index + 1) · \(receipt.pages.count) pages" : "Receipt \(index + 1)"
    }

    func photoHint(for receipt: Receipt) -> String? {
        guard let taken = receipt.pages.first?.takenAt else { return nil }
        return "Photo from \(Dates.shortDay(taken))"
    }

    func effectiveShopId(_ receipt: Receipt, fallback: Int64?) -> Int64? {
        receipt.shopId ?? batchShopId ?? fallback
    }

    func dateText(_ receipt: Receipt) -> String {
        switch receipt.date {
        case .fromReceipt: return "Date from receipt"
        case .photo: return receipt.pages.first?.takenAt.map { "Photo · \(Dates.shortDay($0))" } ?? "Date from receipt"
        case .picked(let date): return Dates.shortDay(date)
        }
    }

    // MARK: Commit

    /// Drafts for the upload queue, one per receipt.
    func drafts(catalog: Catalog) -> [UploadQueue.ImportDraft] {
        let fallback = ImportFlags.letShopFindOut || receipts.count != 1 ? nil : catalog.lastShop?.id
        return receipts.compactMap { receipt in
            let shopId = effectiveShopId(receipt, fallback: fallback)
            guard shopId != nil || ImportFlags.letShopFindOut else { return nil }
            var files: [(name: String, data: Data)] = []
            var edits: [PageEdit] = []
            for (n, page) in receipt.pages.enumerated() {
                let name = "page-\(n).\(page.fileExtension)"
                files.append((name, page.data))
                var edit = PageEdit(file: name, rotation: page.rotation)
                switch page.straighten {
                case .straightened: edit.quad = page.quad
                case .adjusted: edit.quad = page.quad; edit.adjusted = true
                case .notFound, .square, .original, .notApplicable: break
                }
                files[n].name = name
                edits.append(edit)
            }
            let purchasedAt: Date?
            switch receipt.date {
            case .fromReceipt: purchasedAt = nil
            case .photo: purchasedAt = receipt.pages.first?.takenAt
            case .picked(let date): purchasedAt = date
            }
            return UploadQueue.ImportDraft(
                files: files, edits: edits, shopId: shopId,
                shopName: catalog.shop(shopId)?.displayName ?? "Unknown shop",
                purchasedAt: purchasedAt, photoTakenAt: ImportFlags.sendPhotoTakenAt ? receipt.pages.first?.takenAt : nil
            )
        }
    }
}
