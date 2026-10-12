import SwiftUI

// Teach a word, Edit English name and Your glossary (notes/design-english-names.md sections 3 to 5).

private func plural(_ n: Int, _ noun: String) -> String { "\(n) \(noun)\(n == 1 ? "" : "s")" }

// MARK: - Teach a word

struct TeachWordTray: View {
    let key: String
    let word: String
    var existing: GlossaryTerm? = nil
    var fromQueue = false
    var onSaved: (TeachResult) -> Void = { _ in }

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var english = ""
    @State private var keep = false
    @State private var preview: GlossaryPreview?
    @State private var previewing = false
    @State private var previewFailed = false
    @State private var saving = false
    @State private var failed = false
    @State private var loaded = false

    private var offline: Bool { !app.network.isOnline }
    private var trimmed: String { english.trimmingCharacters(in: .whitespaces) }
    private var canSave: Bool { !offline && !saving && (keep || !trimmed.isEmpty) }
    private var affected: Int? { preview?.productsAffected }

    private var buttonTitle: String {
        if offline { return "Needs a connection" }
        guard let n = affected, n > 0 else { return "Save" }
        return "Save and update \(plural(n, "product"))"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Teach a word", subtitle: subtitle)

            InlineFieldGroup {
                InlineField(label: "English", text: $english, placeholder: keep ? word : "chocolate sprinkles", error: failed, divider: false, autofocus: true) {
                    if canSave { Task { await save() } }
                }
                .disabled(keep)
                .opacity(keep ? 0.5 : 1)
            }

            Toggle(isOn: $keep) {
                TypeStyle.rowTitle.sized(15).text("Keep as is (brand)").foregroundStyle(Palette.ink)
            }
            .padding(.horizontal, Space.xs)
            .onChange(of: keep) { _, on in Haptics.play(.selection); if on { english = "" } }

            if failed {
                TypeStyle.caption.text("Couldn't save. Try again.").foregroundStyle(Palette.redText).padding(.horizontal, Space.xs)
            }

            willBecome

            Button { Task { await save() } } label: {
                LoadingLabel(title: buttonTitle, isLoading: saving)
            }
            .buttonStyle(.shop(haptic: .medium))
            .disabled(!canSave)

            TypeStyle.caption.sized(12.5).text("Names you typed are never changed.")
                .foregroundStyle(Palette.ink3).frame(maxWidth: .infinity)
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            if let existing { english = existing.english ?? ""; keep = existing.keepAsIs }
        }
        .task(id: "\(english)|\(keep)") { await refreshPreview() }
    }

    private var subtitle: String {
        var parts = [word]
        if let n = affected { parts.append("used in \(plural(n, "product"))") }
        parts.append("Dutch")
        return parts.joined(separator: " · ")
    }

    @ViewBuilder
    private var willBecome: some View {
        if previewing && preview == nil {
            SkeletonBar(width: 220, height: 14).padding(.horizontal, Space.xs)
        } else if let preview, !previewFailed {
            if preview.productsAffected == 0 {
                TypeStyle.rowSub.text("No products use this word right now").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
            } else if !preview.examples.isEmpty {
                VStack(alignment: .leading, spacing: Space.s) {
                    TypeStyle.caption.text("Will become").foregroundStyle(Palette.ink2)
                    ForEach(preview.examples.prefix(3)) { ex in
                        HStack(spacing: Space.s) {
                            TypeStyle.rowSub.text(ex.nameEnBefore ?? ex.name).foregroundStyle(Palette.ink3).lineLimit(1)
                            IconView(.forward, size: 10, weight: .bold).foregroundStyle(Palette.ink3)
                            TypeStyle.rowSub.text(ex.nameEnAfter ?? ex.name).foregroundStyle(Palette.ink).lineLimit(1)
                        }
                    }
                }
                .padding(.horizontal, Space.xs)
            }
        }
    }

    private func refreshPreview() async {
        let en = trimmed
        guard keep || !en.isEmpty else { preview = nil; previewFailed = false; return }
        try? await Task.sleep(for: .milliseconds(350))
        guard !Task.isCancelled else { return }
        previewing = true
        defer { previewing = false }
        do {
            preview = try await app.api.previewGlossary(GlossaryPreviewBody(dutch: word, english: keep ? nil : en, keepAsIs: keep))
            previewFailed = false
        } catch {
            if !(error is CancellationError) { previewFailed = true }
        }
    }

    private func save() async {
        saving = true
        failed = false
        defer { saving = false }
        let input = GlossaryTermInput(english: keep ? nil : trimmed, keepAsIs: keep)
        do {
            let result = try await app.api.teachWord(key, input)
            Haptics.play(.success)
            trays.dismiss()
            let shown = keep ? "kept as is" : "“\(trimmed)”"
            let detail = "\(word) is now \(shown)"
            app.toasts.show("Updated \(plural(result.productsUpdated, "product")) · \(detail)", duration: .seconds(6),
                            action: (title: "Undo", run: { Task { await undo(result) } }))
            await app.refreshCatalogNames()
            onSaved(result)
        } catch {
            failed = true
            Haptics.play(.error)
        }
    }

    private func undo(_ result: TeachResult) async {
        do {
            if let previous = result.previous { _ = try await app.api.teachWord(key, previous) } else { _ = try await app.api.forgetWord(key) }
            app.toasts.show("Restored")
            await app.refreshCatalogNames()
            await app.needsEnglish.load()
        } catch let e as APIError { app.toasts.error(e.message) } catch {}
    }
}

// MARK: - Edit English name

struct EditEnglishTray: View {
    let productId: Int64

    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var english = ""
    @State private var original = ""
    @State private var manual = false
    @State private var live: TranslatePreview?
    @State private var saving = false
    @State private var loaded = false

    private var product: Product? { app.catalog.product(productId) }
    private var trimmed: String { english.trimmingCharacters(in: .whitespaces) }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "English name")

            InlineFieldGroup {
                HStack(spacing: 12) {
                    TypeStyle.caption.text("Dutch").foregroundStyle(Palette.ink2).frame(width: 70, alignment: .leading)
                    TypeStyle.input.sized(16).text(product?.name ?? "").foregroundStyle(Palette.ink2).lineLimit(1)
                    Spacer()
                    IconView(.eyeSlash, size: 13, weight: .regular).foregroundStyle(Palette.ink3)
                }
                .padding(.horizontal, 16).frame(height: 50)
                .overlay(alignment: .bottom) { Rectangle().fill(Palette.line).frame(height: 1) }
                InlineField(label: "English", text: $english, placeholder: "Chocolate sprinkles", divider: false, autofocus: true) { Task { await save() } }
            }

            statusLine

            if let live, !live.unknown.isEmpty, let first = live.unknown.first {
                EnglishNameText(text: live.english ?? "", unknown: live.unknown.map { UnknownWord(word: $0.word, key: $0.key, start: $0.start, end: $0.end) }, onTapWord: teach)
                Button { teach(UnknownWord(word: first.word, key: first.key, start: first.start, end: first.end)) } label: {
                    TypeStyle.label.sized(14).text("Teach “\(first.word)” for every product").foregroundStyle(Palette.blueText)
                }
            }

            HStack(spacing: Space.m) {
                Button("Reset to automatic") { Task { await save(reset: true) } }
                    .buttonStyle(.shop(.secondary))
                    .disabled(!manual || saving)
                Button { Task { await save() } } label: { LoadingLabel(title: "Save", isLoading: saving) }
                    .buttonStyle(.shop(haptic: .medium))
                    .disabled(trimmed.isEmpty || trimmed == original || saving)
            }
            TypeStyle.caption.sized(12.5).text("Won't be changed automatically").foregroundStyle(Palette.ink3).frame(maxWidth: .infinity)
        }
        .onAppear {
            guard !loaded, let p = product else { return }
            loaded = true
            english = p.nameEn ?? ""
            original = english
            manual = p.nameEnStatus == "manual"
        }
        .task(id: english) { await refreshLive() }
    }

    @ViewBuilder
    private var statusLine: some View {
        if trimmed != original || !manual {
            if let live, trimmed == original || trimmed.isEmpty {
                let n = live.unknown.count
                TypeStyle.caption.text(n > 0 ? "Auto-translated · \(plural(n, "word")) unknown" : "Auto-translated").foregroundStyle(n > 0 ? Palette.amberText : Palette.ink2).padding(.horizontal, Space.xs)
            }
        }
        if manual && trimmed == original {
            TypeStyle.caption.text("Yours · Won't be changed automatically").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
        }
    }

    private func refreshLive() async {
        guard let dutch = product?.name else { return }
        try? await Task.sleep(for: .milliseconds(350))
        guard !Task.isCancelled else { return }
        live = try? await app.api.previewTranslation(trimmed.isEmpty || manual ? dutch : trimmed)
    }

    private func teach(_ word: UnknownWord) {
        app.trays.present("teach-word") { TeachWordTray(key: word.key, word: word.word) }
    }

    private func save(reset: Bool = false) async {
        saving = true
        defer { saving = false }
        do {
            let updated = try await app.api.updateProduct(productId, ProductInput(name: product?.name ?? "", nameEn: reset ? "" : trimmed))
            app.catalog.upsert(updated)
            Haptics.play(.success)
            trays.dismiss()
            await app.needsEnglish.load()
        } catch let e as APIError { app.toasts.error(e.message) } catch {}
    }
}

// MARK: - Your glossary

struct GlossaryView: View {
    @Environment(AppModel.self) private var app
    @State private var terms: [GlossaryTerm]?
    @State private var failed: APIError?

    var body: some View {
        VStack(spacing: 0) {
            NavHeader()
            List {
                LargeTitleHeader(title: "Your glossary", detail: terms.map { "\($0.count)" }).shopListRow()
                if let terms {
                    if terms.isEmpty {
                        EmptyState(title: "No words taught yet", message: "Words you teach are kept here and win over the built-in dictionary.") {
                            GlyphIllustration(icon: .edit, color: Accent.teal)
                        }
                        .padding(.top, 60).shopListRow()
                    }
                    ForEach(terms) { term in
                        SwipeActionRow(trailing: SwipeAction(icon: "trash", title: "Remove", tint: Accent.red, removesRow: true, haptic: .soft) { Task { await remove(term) } }) {
                            Button { edit(term) } label: {
                                HStack(spacing: Space.l) {
                                    VStack(alignment: .leading, spacing: 2) {
                                        TypeStyle.rowTitle.sized(15).text(term.dutch).foregroundStyle(Palette.ink)
                                        TypeStyle.rowSub.text("\(plural(term.productsCount, "product")) · taught \(term.updatedAt.formatted(.dateTime.day().month(.abbreviated)))").foregroundStyle(Palette.ink2)
                                    }
                                    Spacer()
                                    TypeStyle.body.text(term.keepAsIs ? "Keep as is" : (term.english ?? "")).foregroundStyle(Palette.ink2).lineLimit(1)
                                }
                                .frame(minHeight: 60).hairline().contentShape(Rectangle())
                            }
                            .buttonStyle(.press(haptic: .selection))
                            .padding(.horizontal, Space.gutter)
                        }
                        .shopListRow()
                    }
                } else if let failed {
                    ErrorState(message: failed.message, offline: failed.isConnectivity) { Task { await load() } }.shopListRow()
                } else {
                    SkeletonRows { SkeletonSimpleRow() }.padding(.horizontal, Space.gutter).shopListRow()
                }
            }
            .shopListStyle()
            .refreshable { await load() }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .task { await load() }
    }

    private func load() async {
        do { terms = try await app.api.glossary(); failed = nil } catch let e as APIError { if terms == nil { failed = e } } catch {}
    }

    private func edit(_ term: GlossaryTerm) {
        app.trays.present("teach-word") {
            TeachWordTray(key: term.dutch, word: term.dutch, existing: term) { _ in Task { await load() } }
        }
    }

    private func remove(_ term: GlossaryTerm) async {
        do {
            let result = try await app.api.forgetWord(term.dutch)
            withAnimation(Motion.collapse) { terms?.removeAll { $0.id == term.id } }
            await app.refreshCatalogNames()
            app.toasts.show("Removed. \(plural(result.productsUpdated, "product")) changed back", duration: .seconds(6), action: (title: "Undo", run: {
                Task {
                    _ = try? await app.api.teachWord(term.dutch, GlossaryTermInput(english: term.english, keepAsIs: term.keepAsIs))
                    await load()
                    await app.refreshCatalogNames()
                }
            }))
        } catch let e as APIError { app.toasts.error(e.message) } catch {}
    }
}
