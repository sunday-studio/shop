import SwiftUI

// "Add products from this receipt" (one receipt) and "Load products from receipts" (all of them).
// Both show a dry run first, then do it. The server decides: nothing here guesses what is a product.

private func plural(_ n: Int, _ noun: String) -> String { "\(n) \(noun)\(n == 1 ? "" : "s")" }

/// Message for a refused load: the server's own words for a 409 (still being read, failed, automatic products off).
private func message(for error: APIError) -> String {
    switch error {
    case .conflict(let p), .problem(let p): p.detail ?? p.title
    default: error.message
    }
}

// MARK: - One receipt

struct LoadReceiptProductsTray: View {
    let model: ReceiptModel

    enum Phase: Equatable {
        case loading
        case preview(ReceiptProductLoad)
        case applying(ReceiptProductLoad)
        case failed(String)
    }

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var phase = Phase.loading

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Add products from this receipt")
            switch phase {
            case .loading:
                SkeletonRows(count: 3) { SkeletonSimpleRow() }
            case .preview(let plan), .applying(let plan):
                summary(plan)
                if plan.created + plan.linked == 0 {
                    Button("Done") { trays.dismiss() }.buttonStyle(.shop(.secondary))
                } else {
                    Button { Task { await apply(plan) } } label: {
                        LoadingLabel(title: app.network.isOnline ? actionTitle(plan) : "Needs a connection", isLoading: phase != .preview(plan))
                    }
                    .buttonStyle(.shop(haptic: .medium))
                    .disabled(!app.network.isOnline || phase != .preview(plan))
                }
            case .failed(let text):
                TypeStyle.body.sized(14.5).text(text).foregroundStyle(Palette.ink2).fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, Space.xs)
                Button("Try again") { Task { await preview() } }.buttonStyle(.shop(.secondary))
            }
        }
        .task { await preview() }
    }

    private func actionTitle(_ plan: ReceiptProductLoad) -> String {
        plan.created > 0 ? "Add \(plural(plan.created, "product"))" : "Link \(plural(plan.linked, "line"))"
    }

    @ViewBuilder
    private func summary(_ plan: ReceiptProductLoad) -> some View {
        if plan.created + plan.linked == 0 {
            TypeStyle.body.sized(14.5).text("Every item on this receipt already has a product.").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
        } else {
            VStack(alignment: .leading, spacing: Space.s) {
                TypeStyle.body.sized(14.5)
                    .text([plan.created > 0 ? "\(plural(plan.created, "new product")) will be created" : nil,
                           plan.linked > 0 ? "\(plural(plan.linked, "line")) will link to products you have" : nil].compactMap { $0 }.joined(separator: " and ") + ".")
                    .foregroundStyle(Palette.ink)
                if plan.needsReview > 0 {
                    TypeStyle.rowSub.text("New ones wait in the review queue so you can check names and categories.").foregroundStyle(Palette.ink2)
                }
            }
            .padding(.horizontal, Space.xs)
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(plan.lines.filter { $0.action != "skipped" }.prefix(12)) { line in
                        HStack(spacing: Space.m) {
                            TypeStyle.mono.sized(13).text(line.rawText).foregroundStyle(Palette.ink2).lineLimit(1)
                            Spacer(minLength: Space.s)
                            TypeStyle.rowSub.text(line.action == "created" ? "New: \(line.productName ?? "")" : (line.productName ?? "")).foregroundStyle(Palette.ink).lineLimit(1)
                        }
                        .frame(minHeight: 34)
                    }
                }
            }
            .frame(maxHeight: 220)
            .scrollBounceBehavior(.basedOnSize)
        }
    }

    private func preview() async {
        phase = .loading
        guard app.network.isOnline else { phase = .failed("Needs a connection."); return }
        do {
            phase = .preview(try await app.api.loadReceiptProducts(model.receiptId, dryRun: true))
        } catch let e as APIError {
            phase = .failed(message(for: e))
        } catch { phase = .failed("Couldn't check this receipt. Try again.") }
    }

    private func apply(_ plan: ReceiptProductLoad) async {
        phase = .applying(plan)
        do {
            let done = try await app.api.loadReceiptProducts(model.receiptId, dryRun: false)
            Haptics.play(.success)
            trays.dismiss()
            let parts = [done.created > 0 ? "Added \(plural(done.created, "product"))" : nil, done.linked > 0 ? "linked \(plural(done.linked, "line"))" : nil]
            app.toasts.show(parts.compactMap { $0 }.joined(separator: ", ").capitalizedFirst, tone: .green)
            await model.load()
            if done.needsReview > 0 { app.productReview.bump(by: done.needsReview) }
            Task { await app.productReview.refreshCount() }
            Task { try? await app.catalog.refreshProducts() }
        } catch let e as APIError {
            Haptics.play(.error)
            phase = .failed(message(for: e))
        } catch { phase = .failed("Couldn't add the products. Try again.") }
    }
}

private extension String {
    var capitalizedFirst: String { prefix(1).uppercased() + dropFirst() }
}

// MARK: - All receipts

@MainActor
@Observable
final class BackfillModel {
    enum Phase: Equatable {
        case loading
        case preview(BackfillState)
        case running(BackfillState)
        case done(BackfillState)
        case failed(String)
    }

    private(set) var phase = Phase.loading
    @ObservationIgnored private let app: AppModel

    init(app: AppModel) { self.app = app }

    func preview() async {
        phase = .loading
        do {
            try await app.api.startBackfill(dryRun: true)
            phase = .preview(try await wait(dryRun: true))
        } catch let e as APIError { phase = .failed(message(for: e)) } catch { phase = .failed("Couldn't check your receipts. Try again.") }
    }

    func run() async {
        guard case .preview(let plan) = phase else { return }
        phase = .running(plan)
        do {
            try await app.api.startBackfill(dryRun: false)
            let result = try await wait(dryRun: false)
            Haptics.play(.success)
            phase = .done(result)
            if result.needsReview > 0 { app.productReview.bump(by: result.needsReview) }
            Task { await app.productReview.refreshCount() }
            Task { try? await app.catalog.refreshProducts() }
            await app.refreshBackfill()
        } catch let e as APIError { Haptics.play(.error); phase = .failed(message(for: e)) } catch { phase = .failed("Couldn't load the products. Try again.") }
    }

    /// Polls until the job finished; shows progress while it runs.
    private func wait(dryRun: Bool) async throws -> BackfillState {
        for _ in 0..<600 {
            let state = try await app.api.backfillState()
            if state.state == "failed" { throw APIError.problem(Problem(type: nil, title: "Failed", status: 500, detail: state.error ?? "The server couldn't finish.")) }
            if state.state == "done", state.dryRun == dryRun { return state }
            if state.isRunning, !dryRun { phase = .running(state) }
            try await Task.sleep(for: .milliseconds(state.isRunning ? 700 : 300))
        }
        throw APIError.unreachable
    }
}

struct BackfillTray: View {
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var model: BackfillModel?

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Load products from receipts")
            if let model {
                content(model)
            } else {
                SkeletonRows(count: 3) { SkeletonSimpleRow() }
            }
        }
        .task {
            let m = BackfillModel(app: app)
            model = m
            await m.preview()
        }
    }

    @ViewBuilder
    private func content(_ model: BackfillModel) -> some View {
        switch model.phase {
        case .loading:
            SkeletonRows(count: 3) { SkeletonSimpleRow() }
        case .preview(let s):
            if s.created + s.linked == 0 {
                TypeStyle.body.sized(14.5).text("Every item on your receipts already has a product.").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
                Button("Done") { trays.dismiss() }.buttonStyle(.shop(.secondary))
            } else {
                TypeStyle.body.sized(14.5)
                    .text("\(plural(s.receiptsTotal, "receipt")) have items without a product. \(plural(s.created, "new product")) would be created and \(plural(s.linked, "line")) linked to products you have.")
                    .foregroundStyle(Palette.ink).fixedSize(horizontal: false, vertical: true).padding(.horizontal, Space.xs)
                if let names = s.createdNames, !names.isEmpty {
                    TypeStyle.rowSub.text("New: " + names.prefix(6).joined(separator: ", ") + (names.count > 6 || s.namesTruncated == true ? "…" : ""))
                        .foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
                }
                TypeStyle.rowSub.text("New products wait in the review queue. Nothing is merged or deleted.").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
                Button { Task { await model.run() } } label: {
                    LoadingLabel(title: app.network.isOnline ? "Load \(plural(s.created, "product"))" : "Needs a connection", isLoading: false)
                }
                .buttonStyle(.shop(haptic: .medium))
                .disabled(!app.network.isOnline)
            }
        case .running(let s):
            VStack(alignment: .leading, spacing: Space.m) {
                TypeStyle.body.sized(14.5).text("Reading receipt \(min(s.receiptsDone + 1, max(s.receiptsTotal, 1))) of \(max(s.receiptsTotal, 1))…").foregroundStyle(Palette.ink)
                ProgressMeter(fraction: s.receiptsTotal == 0 ? 0 : Double(s.receiptsDone) / Double(s.receiptsTotal), color: Accent.blue)
            }
            .padding(.horizontal, Space.xs)
        case .done(let s):
            TypeStyle.body.sized(14.5).text("Done. \(plural(s.created, "product")) created, \(plural(s.linked, "line")) linked.").foregroundStyle(Palette.ink).padding(.horizontal, Space.xs)
            if s.needsReview > 0 {
                Button("Review \(s.needsReview) new") { trays.dismiss(); app.push(.productReview) }.buttonStyle(.shop())
            }
            Button("Close") { trays.dismiss() }.buttonStyle(.shop(.secondary))
        case .failed(let text):
            TypeStyle.body.sized(14.5).text(text).foregroundStyle(Palette.ink2).fixedSize(horizontal: false, vertical: true).padding(.horizontal, Space.xs)
            Button("Try again") { Task { await model.preview() } }.buttonStyle(.shop(.secondary))
        }
    }
}
