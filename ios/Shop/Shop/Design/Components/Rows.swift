import SwiftUI

// Flat rows, hairline dividers, no card backgrounds (DESIGN-RULES "Density").

// MARK: - Section headers

/// "Today ……… €38,12" — 13pt Medium ink2.
struct DaySectionHeader: View {
    let title: String
    var trailing: String? = nil

    var body: some View {
        HStack {
            Text(title)
            Spacer()
            if let trailing { Text(trailing) }
        }
        .textStyle(.label)
        .foregroundStyle(Palette.ink2)
        .padding(.top, Space.ml)
        .padding(.bottom, Space.xs)
        .accessibilityAddTraits(.isHeader)
    }
}

/// Section label with an optional trailing action ("Lines", "Receipt names  +3").
struct SectionLabel<Trailing: View>: View {
    let title: String
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            TypeStyle.label.text(title).foregroundStyle(Palette.ink2)
                .accessibilityAddTraits(.isHeader)
            Spacer()
            trailing()
        }
        .padding(.bottom, Space.xs)
    }
}

extension SectionLabel where Trailing == EmptyView {
    init(_ title: String) {
        self.title = title
        self.trailing = { EmptyView() }
    }
}

// MARK: - Receipt row

/// Inbox row: small shop mark, shop name, status sub-line, amount. 54pt.
struct ReceiptRow: View {
    let shopName: String
    let subtitle: String
    var subtitleColor: Color = Palette.ink2
    let amount: String
    var amountColor: Color = Palette.ink
    var showsSpinner: Bool = false
    var trailingIcon: Icon? = nil
    /// Receipt has no shop yet ("Unassigned"): dashed "?" mark instead of a monogram.
    var isUnknownShop: Bool = false
    /// Amber dot on the shop mark: the receipt is accepted but still needs something (items to link).
    var needsAttention: Bool = false

    var body: some View {
        HStack(spacing: Space.l) {
            Group {
                if isUnknownShop { UnknownShopMark() } else { ShopMark(name: shopName) }
            }
            .overlay(alignment: .topTrailing) {
                if needsAttention {
                    Circle().fill(Accent.amber)
                        .frame(width: 9, height: 9)
                        .overlay(Circle().strokeBorder(Palette.bg, lineWidth: 2))
                        .offset(x: 3, y: -3)
                }
            }
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.text(shopName).foregroundStyle(Palette.ink)
                    .lineLimit(1)
                HStack(spacing: 5) {
                    if showsSpinner {
                        ProgressView().controlSize(.mini).tint(subtitleColor)
                    }
                    TypeStyle.rowSub.text(subtitle).foregroundStyle(subtitleColor)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: Space.m)
            if let trailingIcon {
                IconView(trailingIcon, size: 18).foregroundStyle(Palette.ink3)
            } else {
                TypeStyle.rowTitle.text(amount).foregroundStyle(amountColor)
            }
        }
        .frame(minHeight: Size.rowReceipt)
        .hairline()
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Receipt line row

/// Review line: category dot, name (or raw mono text), sub-line, amount. ≥46pt.
struct LineRow: View {
    let title: String
    var isRaw: Bool = false
    var subtitle: String? = nil
    var subtitleColor: Color = Palette.ink2
    let amount: String
    var titleColor: Color = Palette.ink
    var amountColor: Color = Palette.ink
    var dot: Color = Palette.neutral
    var indented: Bool = false

    var body: some View {
        HStack(spacing: Space.ml) {
            CategoryDot(color: dot)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .textStyle(isRaw ? TypeStyle.mono : .rowTitle)
                    .foregroundStyle(titleColor)
                    .lineLimit(1)
                    .truncationMode(.tail)
                if let subtitle {
                    TypeStyle.caption.text(subtitle).foregroundStyle(subtitleColor)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: Space.m)
            TypeStyle.rowTitle.sized(14.5).text(amount).foregroundStyle(amountColor)
        }
        .padding(.vertical, 6)
        .padding(.leading, indented ? 18 : 0)
        .frame(minHeight: Size.rowLine)
        .hairline()
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Product row

/// Catalog row: tile, name, meta ("1,5 l · Dairy"), best per-unit price with the shop mark.
struct ProductRow: View {
    let name: String
    var meta: String? = nil
    var category: String? = nil
    /// The category's stored colour token; wins over the name.
    var categoryToken: String? = nil
    var imageURL: URL? = nil
    var price: String? = nil
    var priceShop: String? = nil
    var showsChevron: Bool = false

    var body: some View {
        HStack(spacing: Space.l) {
            ProductTile(name: name, category: category, categoryToken: categoryToken, imageURL: imageURL)
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.text(name).foregroundStyle(Palette.ink).lineLimit(1)
                if let meta {
                    TypeStyle.rowSub.text(meta).foregroundStyle(Palette.ink2).lineLimit(1)
                }
            }
            Spacer(minLength: Space.m)
            if let price {
                HStack(spacing: Space.m) {
                    TypeStyle.rowTitle.sized(14.5).text(price).foregroundStyle(Palette.ink)
                    if let priceShop { ShopMark(name: priceShop, size: 20) }
                }
            }
            if showsChevron {
                IconView(.forward, size: 14, weight: .bold).foregroundStyle(Palette.ink3)
            }
        }
        .frame(minHeight: Size.rowProduct)
        .hairline()
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Check circle + list item row

/// 24pt circle with a 2pt ink3 ring that fills green with a spring.
struct CheckCircle: View {
    var isChecked: Bool

    var body: some View {
        ZStack {
            Circle().strokeBorder(Palette.ink3, lineWidth: 2).opacity(isChecked ? 0 : 1)
            Circle().fill(Accent.green).scaleEffect(isChecked ? 1 : 0.4).opacity(isChecked ? 1 : 0)
            Image(icon: .check)
                .font(.system(size: 11, weight: .heavy))
                .foregroundStyle(Accent.white)
                .opacity(isChecked ? 1 : 0)
        }
        .frame(width: 24, height: 24)
    }
}

/// Shopping-list row. Checking: circle fills (200ms spring), text strikes and fades to ink3
/// after 160ms, then `onCollapse` runs so the parent can remove the row (240ms collapse).
struct ListItemRow: View {
    let title: String
    var hint: String? = nil
    var hintShop: String? = nil
    var quantity: String? = nil
    var isChecked: Bool
    let onToggle: (Bool) -> Void
    var onTap: (() -> Void)? = nil

    @State private var struck = false
    /// Optimistic checked state while a check-off plays out, before the parent moves the row.
    @State private var pendingCheck = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    private var shownChecked: Bool { isChecked || pendingCheck }

    var body: some View {
        HStack(spacing: Space.l) {
            Button {
                toggle()
            } label: {
                CheckCircle(isChecked: shownChecked)
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
            .padding(.horizontal, -10)
            .accessibilityLabel(shownChecked ? "Uncheck \(title)" : "Check off \(title)")

            VStack(alignment: .leading, spacing: 2) {
                TypeStyle.rowTitle.text(title)
                    .strikethrough(struck || (isChecked && reduce), color: Palette.ink3)
                    .foregroundStyle(struck || shownChecked ? Palette.ink3 : Palette.ink)
                    .lineLimit(1)
                if let hint {
                    HStack(spacing: 5) {
                        if let hintShop { ShopMark(name: hintShop, size: 14) }
                        TypeStyle.caption.text(hint).foregroundStyle(Palette.ink2).lineLimit(1)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .onTapGesture {
                guard let onTap else { return }
                Haptics.play(.selection)
                onTap()
            }

            if let quantity {
                TypeStyle.body.sized(13.5).text(quantity).foregroundStyle(Palette.ink2)
            }
        }
        .frame(minHeight: Size.rowList)
        .hairline()
        .onAppear { struck = isChecked }
        .onChange(of: isChecked) { _, now in
            if now { pendingCheck = false } else { struck = false }
        }
    }

    /// Check-off: the circle fills and `onToggle` fires right away, text strikes at 160ms. The
    /// parent may apply the change later (the list screen waits ~420ms before moving the row),
    /// so the circle stays filled optimistically until `isChecked` catches up (1s safety cap).
    /// Without that the circle stayed empty while the text was already struck.
    private func toggle() {
        guard !pendingCheck else { return }
        let next = !isChecked
        Haptics.play(.soft)
        if next && !reduce {
            withAnimation(Motion.pop) { pendingCheck = true }
            Task {
                try? await Task.sleep(for: .milliseconds(160))
                withAnimation(Motion.fade) { struck = true }
                try? await Task.sleep(for: .milliseconds(840))
                pendingCheck = false
            }
        } else {
            struck = next
        }
        withAnimation(reduce ? Motion.reducedFade : Motion.pop) { onToggle(next) }
    }
}

// MARK: - Settings row

/// S rows: coloured 32pt icon tile, label, value, chevron. 52pt.
struct SettingsRow: View {
    let icon: Icon
    var color: Color
    let title: String
    var value: String? = nil
    var showsChevron: Bool = true

    var body: some View {
        HStack(spacing: Space.lx) {
            IconTile(icon: icon, color: color)
            TypeStyle.rowTitle.sized(15.5).text(title).foregroundStyle(Palette.ink)
            Spacer(minLength: Space.m)
            if let value {
                TypeStyle.body.text(value).foregroundStyle(Palette.ink2).lineLimit(1)
            }
            if showsChevron {
                IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
            }
        }
        .frame(minHeight: Size.rowProduct)
        .hairline()
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Key / value row

/// "Database ……… ● Healthy" (O.2 checks, S.1 health). 42pt, hairline on top.
struct KeyValueRow: View {
    let key: String
    let value: String
    var dot: Color? = nil
    var valueColor: Color = Palette.ink
    var mono: Bool = false

    var body: some View {
        HStack {
            Text(key).foregroundStyle(Palette.ink2)
            Spacer(minLength: Space.m)
            HStack(spacing: 7) {
                if let dot { StatusDot(color: dot) }
                Text(value)
                    .textStyle(mono ? TypeStyle.mono.sized(13) : TypeStyle.label.sized(14))
                    .foregroundStyle(valueColor)
                    .lineLimit(1)
            }
        }
        .textStyle(.body)
        .frame(minHeight: 42)
        .hairline(.top)
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Generic navigation row

struct NavRow: View {
    let title: String
    var subtitle: String? = nil
    var value: String? = nil
    var leading: AnyView? = nil

    var body: some View {
        HStack(spacing: Space.l) {
            if let leading { leading }
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.text(title).foregroundStyle(Palette.ink)
                if let subtitle { TypeStyle.rowSub.text(subtitle).foregroundStyle(Palette.ink2) }
            }
            Spacer(minLength: Space.m)
            if let value { TypeStyle.body.text(value).foregroundStyle(Palette.ink2) }
            IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
        }
        .frame(minHeight: Size.rowProduct)
        .hairline()
        .contentShape(Rectangle())
    }
}

#Preview("Rows") {
    @Previewable @State var checked = false
    ScrollView {
        VStack(spacing: 0) {
            DaySectionHeader(title: "Today", trailing: "€38,12")
            ReceiptRow(shopName: "Lidl", subtitle: "Reading receipt…", subtitleColor: Palette.blueText, amount: "—", amountColor: Palette.ink3, showsSpinner: true)
            ReceiptRow(shopName: "Albert Heijn", subtitle: "Total doesn’t match · 18:42", subtitleColor: Palette.amberText, amount: "€38,12")
            ReceiptRow(shopName: "Dirk", subtitle: "12:08 · 9 items", amount: "€12,40")
            SectionLabel("Lines").padding(.top, 16)
            LineRow(title: "Halfvolle melk", subtitle: "AH HALFV MELK 1,5L · Dairy", amount: "1,79", dot: Accent.blue)
            LineRow(title: "Bonus", subtitle: "on Halfvolle melk", amount: "−0,45", titleColor: Palette.greenText, amountColor: Palette.greenText, dot: Accent.blue, indented: true)
            LineRow(title: "AH GRIEKS YOGH 0%", isRaw: true, subtitle: "Link to Griekse yoghurt 0%? 92% match", subtitleColor: Palette.amberText, amount: "2,19", dot: Accent.amber)
            ProductRow(name: "Halfvolle melk", meta: "1,5 l · Dairy", category: "Dairy", price: "€0,85/l", priceShop: "Lidl")
            ListItemRow(title: "Halfvolle melk", hint: "Cheapest at Lidl · €0,85/l", hintShop: "Lidl", quantity: "× 2", isChecked: checked) { checked = $0 }
            SettingsRow(icon: .upload, color: Accent.blue, title: "Upload queue", value: "Empty")
            KeyValueRow(key: "Database", value: "Healthy", dot: Accent.green)
        }
        .padding(.horizontal, 20)
    }
    .background(Palette.bg)
}
