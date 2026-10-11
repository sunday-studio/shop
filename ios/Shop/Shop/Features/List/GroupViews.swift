import SwiftUI

/// Checklists inside the shopping list (a group is a section of the same scroll): header with
/// progress, its menu, and the trays for creating, renaming, moving, deleting and templates.

// MARK: - Header

struct GroupHeaderRow: View {
    let group: ListGroup
    let progress: (checked: Int, total: Int)
    let collapsed: Bool
    let onToggle: () -> Void
    let onAdd: () -> Void
    let onRename: () -> Void
    let onSaveTemplate: () -> Void
    let onArchive: () -> Void
    let onDelete: () -> Void

    private var done: Bool { progress.total > 0 && progress.checked == progress.total }
    private var subtitle: String {
        if progress.total == 0 { return "No items yet" }
        if done { return "Done" }
        if progress.checked == 0 { return "Nothing checked yet" }
        return "\(progress.checked) of \(progress.total)"
    }

    var body: some View {
        VStack(spacing: Space.s) {
            HStack(spacing: Space.m) {
                Button(action: onToggle) {
                    HStack(spacing: Space.m) {
                        IconView(done ? .check : .forward, size: 14, weight: .bold)
                            .foregroundStyle(done ? Accent.green : Palette.ink2)
                            .rotationEffect(.degrees(done || collapsed ? 0 : 90))
                            .frame(width: 18)
                        VStack(alignment: .leading, spacing: 1) {
                            HStack(spacing: Space.s) {
                                TypeStyle.rowTitle.sized(17).text(group.name).foregroundStyle(Palette.ink).lineLimit(1)
                                if group.isAssistant {
                                    TypeStyle.captionMedium.sized(11.5).text("Assistant")
                                        .foregroundStyle(Palette.blueText)
                                        .padding(.horizontal, 7).padding(.vertical, 2)
                                        .background(Palette.blueTint, in: Capsule())
                                }
                            }
                            TypeStyle.rowSub.text(subtitle).foregroundStyle(Palette.ink2)
                        }
                        Spacer(minLength: 0)
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: .selection))
                .accessibilityLabel("\(group.name), \(subtitle)")
                .accessibilityHint(collapsed ? "Expands the group" : "Collapses the group")

                IconButton(icon: .plus, label: "Add to \(group.name)", size: 32, glyph: 14, action: onAdd)
                Menu {
                    Button("Rename", systemImage: "pencil", action: onRename)
                    Button("Save as template", systemImage: "square.on.square", action: onSaveTemplate)
                    Button("Archive", systemImage: "archivebox", action: onArchive)
                    Button("Delete", systemImage: "trash", role: .destructive, action: onDelete)
                } label: {
                    IconView(.more, size: 14, weight: .bold)
                        .foregroundStyle(Palette.ink)
                        .frame(width: 32, height: 32)
                        .background(Palette.sunk, in: Circle())
                }
                .accessibilityLabel("\(group.name) options")
            }
            if progress.total > 0 {
                GeometryReader { proxy in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Palette.sunk)
                        Capsule().fill(Accent.green)
                            .frame(width: proxy.size.width * CGFloat(progress.checked) / CGFloat(max(1, progress.total)))
                    }
                }
                .frame(height: 4)
                .animation(Motion.spring, value: progress.checked)
                .accessibilityHidden(true)
            }
        }
        .padding(.top, Space.l)
        .padding(.bottom, Space.s)
        .frame(minHeight: 56)
    }
}

// MARK: - New group

struct NewGroupTray: View {
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var lines = ""

    /// Lines of the pasted text: blank lines and bullets / numbers in front are dropped.
    private var parsed: [String] {
        lines.split(whereSeparator: \.isNewline).compactMap { raw in
            var text = raw.trimmingCharacters(in: .whitespaces)
            while let first = text.first, "-•*·".contains(first) { text.removeFirst(); text = text.trimmingCharacters(in: .whitespaces) }
            if let dot = text.firstIndex(where: { $0 == "." || $0 == ")" }), text[text.startIndex..<dot].allSatisfy(\.isNumber), !text[text.startIndex..<dot].isEmpty {
                text = String(text[text.index(after: dot)...]).trimmingCharacters(in: .whitespaces)
            }
            return text.isEmpty ? nil : String(text.prefix(100))
        }.prefix(60).map { $0 }
    }

    private var canCreate: Bool { !name.trimmingCharacters(in: .whitespaces).isEmpty && !parsed.isEmpty }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "New group")
            InlineFieldGroup {
                InlineField(label: "Name", text: $name, placeholder: "BBQ, jerk chicken…", divider: false, autofocus: true)
            }
            VStack(alignment: .leading, spacing: Space.s) {
                ZStack(alignment: .topLeading) {
                    if lines.isEmpty {
                        TypeStyle.input.sized(16).text("One per line. Paste a recipe if you like.")
                            .foregroundStyle(Palette.ink3)
                            .padding(.horizontal, 5).padding(.vertical, 8)
                    }
                    TextEditor(text: $lines)
                        .scrollContentBackground(.hidden)
                        .font(TypeStyle.input.sized(16).font)
                        .frame(height: 120)
                }
                .padding(.horizontal, Space.m)
                .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.input))
                TypeStyle.rowSub.text(parsed.isEmpty ? " " : (parsed.count == 1 ? "1 item" : "\(parsed.count) items"))
                    .foregroundStyle(Palette.ink2)
                    .padding(.horizontal, Space.xs)
            }
            Button {
                trays.dismiss()
                let name = name, lines = parsed
                Task { @MainActor in
                    try? await Task.sleep(for: .milliseconds(350))
                    withAnimation(Motion.collapse) { app.list.createGroup(name: name, lines: lines) }
                    Haptics.play(.success)
                    await app.list.sync()
                }
            } label: {
                TypeStyle.label.sized(16).text("Create group")
            }
            .buttonStyle(.shop(haptic: .success))
            .disabled(!canCreate)
        }
    }
}

// MARK: - Rename

struct RenameGroupTray: View {
    let group: ListGroup
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Rename group")
            InlineFieldGroup {
                InlineField(label: "Name", text: $name, divider: false, autofocus: true) { save() }
            }
            Button("Save") { save() }
                .buttonStyle(.shop(haptic: .medium))
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || name == group.name)
        }
        .onAppear { if name.isEmpty { name = group.name } }
    }

    private func save() {
        guard !name.trimmingCharacters(in: .whitespaces).isEmpty else { return }
        app.list.renameGroup(group.id, to: name)
        trays.dismiss()
        Task { await app.list.sync() }
    }
}

// MARK: - Delete

struct DeleteGroupTray: View {
    let group: ListGroup
    let itemCount: Int
    let onDelete: (_ keepItems: Bool) -> Void
    @Environment(TrayCoordinator.self) private var trays

    var body: some View {
        VStack(alignment: .leading, spacing: Space.xl) {
            TrayHeader(title: "Delete “\(group.name)”?")
            if itemCount > 0 {
                TypeStyle.body.sized(14.5).text("It has \(itemCount) item\(itemCount == 1 ? "" : "s"). You can undo this for a few minutes.")
                    .foregroundStyle(Palette.ink2)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, Space.xs)
            }
            VStack(spacing: Space.m) {
                if itemCount > 0 {
                    Button("Keep \(itemCount) item\(itemCount == 1 ? "" : "s") on the main list") { trays.dismiss(); onDelete(true) }
                        .buttonStyle(.shop(.secondary))
                }
                Button(itemCount > 0 ? "Delete group and items" : "Delete group") { trays.dismiss(); onDelete(false) }
                    .buttonStyle(.shop(.solid(Accent.red), haptic: .medium))
                Button("Cancel") { trays.dismiss() }
                    .textStyle(.label).foregroundStyle(Palette.ink2)
                    .frame(maxWidth: .infinity)
                    .buttonStyle(.press(haptic: .selection))
            }
        }
    }
}

// MARK: - Move item

struct MoveItemTray: View {
    let item: ListItem
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Move “\(item.title)”")
            VStack(spacing: 6) {
                ChoiceRow(title: "Main list", selected: item.groupId == nil) { move(nil) }
                ForEach(app.list.checklists) { group in
                    ChoiceRow(title: group.name, selected: item.groupId == group.id) { move(group.id) }
                }
            }
        }
    }

    private func move(_ groupId: Int64?) {
        trays.dismiss()
        guard groupId != item.groupId else { return }
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(350))
            withAnimation(Motion.collapse) { app.list.moveItem(item, to: groupId) }
            await app.list.sync()
        }
    }
}

// MARK: - Archived

struct ArchivedGroupsTray: View {
    @Environment(AppModel.self) private var app

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Archived", subtitle: "\(app.list.archivedGroups.count) group\(app.list.archivedGroups.count == 1 ? "" : "s")")
            if app.list.archivedGroups.isEmpty {
                TypeStyle.body.text("Nothing archived.").foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
            }
            ForEach(app.list.archivedGroups) { group in
                HStack {
                    VStack(alignment: .leading, spacing: 1) {
                        TypeStyle.rowTitle.text(group.name).foregroundStyle(Palette.ink)
                        let p = app.list.progress(group.id)
                        TypeStyle.rowSub.text("\(p.checked) of \(p.total)").foregroundStyle(Palette.ink2)
                    }
                    Spacer()
                    Button("Restore") {
                        withAnimation(Motion.collapse) { app.list.archiveGroup(group.id, false) }
                        Task { await app.list.sync() }
                    }
                    .textStyle(.label).foregroundStyle(Palette.blueText)
                    .buttonStyle(.press(haptic: .light))
                }
                .padding(.horizontal, Space.lx)
                .frame(minHeight: 56)
                .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            }
        }
    }
}

// MARK: - Templates (need a connection)

struct NewFromTemplateTray: View {
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var templates: [ListGroup]?
    @State private var failed = false
    @State private var picked: ListGroup?
    @State private var name = ""
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: picked == nil ? "New from template" : "Start list")
            if let picked {
                InlineFieldGroup {
                    InlineField(label: "Name", text: $name, divider: false, autofocus: true)
                }
                Button { Task { await start(picked) } } label: { LoadingLabel(title: "Start list", isLoading: working) }
                    .buttonStyle(.shop(haptic: .success))
                    .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || working)
            } else if let templates {
                if templates.isEmpty {
                    TypeStyle.body.text("No templates yet. Open a group's menu and choose Save as template.")
                        .foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
                }
                VStack(spacing: 6) {
                    ForEach(templates) { template in
                        Button {
                            Haptics.play(.selection)
                            name = template.name
                            picked = template
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 1) {
                                    TypeStyle.rowTitle.text(template.name).foregroundStyle(Palette.ink)
                                    TypeStyle.rowSub.text("\(template.itemCount ?? 0) items").foregroundStyle(Palette.ink2)
                                }
                                Spacer()
                                IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                            }
                            .padding(.horizontal, Space.lx)
                            .frame(minHeight: 56)
                            .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
                        }
                        .buttonStyle(.press(haptic: nil))
                    }
                }
            } else if failed {
                TypeStyle.body.text(app.network.isOnline ? "Couldn't load your templates." : "Templates need a connection.")
                    .foregroundStyle(Palette.ink2).padding(.horizontal, Space.xs)
            } else {
                SkeletonRows(count: 3) { SkeletonSimpleRow() }
            }
        }
        .task {
            do { templates = try await app.list.templates() } catch { failed = true }
        }
    }

    private func start(_ template: ListGroup) async {
        working = true
        defer { working = false }
        do {
            _ = try await app.list.startList(from: template, name: name.trimmingCharacters(in: .whitespaces))
            trays.dismiss()
            Haptics.play(.success)
        } catch {
            Haptics.play(.error)
            app.toasts.error("Couldn't start it. Try again.")
        }
    }
}

struct SaveTemplateTray: View {
    let group: ListGroup
    @Environment(AppModel.self) private var app
    @Environment(TrayCoordinator.self) private var trays
    @State private var name = ""
    @State private var working = false

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Save as template", subtitle: "Items start unchecked.")
            InlineFieldGroup {
                InlineField(label: "Name", text: $name, divider: false, autofocus: true)
            }
            Button { Task { await save() } } label: { LoadingLabel(title: "Save template", isLoading: working) }
                .buttonStyle(.shop(haptic: .success))
                .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || working)
        }
        .onAppear { if name.isEmpty { name = group.name } }
    }

    private func save() async {
        working = true
        defer { working = false }
        do {
            try await app.list.saveAsTemplate(group, name: name.trimmingCharacters(in: .whitespaces))
            trays.dismiss()
            app.toasts.show("Saved as template", tone: .green)
        } catch {
            Haptics.play(.error)
            app.toasts.error(app.network.isOnline ? "Couldn't save it. Try again." : "Templates need a connection.")
        }
    }
}
