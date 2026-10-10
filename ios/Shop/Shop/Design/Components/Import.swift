import SwiftUI
import UIKit

// Components for the receipt import flow (docs/DESIGN-UPLOAD-FLOW.md): picking photos and files,
// grouping them into receipts, upload progress, and the "unknown shop" suggestion.

// MARK: - Unknown shop mark

/// Stand-in for a ShopMark while a receipt has no shop yet: same square, dashed ring, "?".
struct UnknownShopMark: View {
    var size: CGFloat = Size.shopMark
    private var side: CGFloat { min(size, 32) }

    var body: some View {
        let shape = RoundedRectangle.continuous(side * 6 / 22)
        Text("?")
            .font(.custom(ShopFont.medium, size: side * 11 / 22))
            .foregroundStyle(Palette.ink3)
            .frame(width: side, height: side)
            .background(Palette.bg, in: shape)
            .overlay(shape.strokeBorder(Palette.ink3, style: StrokeStyle(lineWidth: 1, dash: [3, 2.5])))
            .accessibilityHidden(true)
    }
}

// MARK: - Page strip

/// Up to three overlapping page thumbnails, like a small fanned stack, with a "+n" tail.
struct PageStrip: View {
    let pages: [UIImage]
    var height: CGFloat = 56

    var body: some View {
        let shown = Array(pages.prefix(3))
        let width = height * 0.72
        ZStack(alignment: .leading) {
            ForEach(Array(shown.enumerated()), id: \.offset) { index, image in
                Image(uiImage: image)
                    .resizable()
                    .scaledToFill()
                    .frame(width: width, height: height)
                    .clipShape(RoundedRectangle.continuous(6))
                    .overlay(RoundedRectangle.continuous(6).strokeBorder(Palette.line, lineWidth: 1))
                    .shadow(color: .black.opacity(0.08), radius: 3, y: 1)
                    .offset(x: CGFloat(index) * 9)
                    .zIndex(Double(shown.count - index))
            }
        }
        .frame(width: width + CGFloat(max(0, shown.count - 1)) * 9, height: height, alignment: .leading)
        .accessibilityHidden(true)
    }
}

// MARK: - Straighten state

/// What the import did to a picked photo (docs/DESIGN-UPLOAD-FLOW.md §5a).
enum StraightenState: Equatable {
    /// Edges found and corrected; the chip toggles to the original.
    case straightened
    /// The user went back to the original photo.
    case original
    /// No acceptable document outline: shows "Edges not found · Adjust".
    case notFound
}

// MARK: - Import receipt row

/// One receipt in the import list: page strip, "Receipt 2 · 3 pages", shop and date chips,
/// remove. Flat row with a hairline like every list; the shop chip is amber until a shop is set.
struct ImportReceiptRow: View {
    let pages: [UIImage]
    let title: String
    var shopName: String? = nil
    /// No shop picked and none needed: the server reads it from the receipt. Shows a neutral
    /// "Shop will find out" chip. With `shopName == nil` and this false the chip is amber
    /// "Pick a shop" (the user really has to choose).
    var shopWillFindOut: Bool = false
    /// "Read from receipt" unless the user set a date or a photo date was accepted.
    var dateText: String = "Date from receipt"
    /// Offered only when the photo carries a capture date: "Photo from 3 Oct".
    var photoDateHint: String? = nil
    var straighten: StraightenState? = nil
    var onToggleStraighten: () -> Void = {}
    var onAdjust: () -> Void = {}
    var onShop: () -> Void
    var onDetails: () -> Void
    var onRemove: () -> Void

    var body: some View {
        HStack(alignment: .center, spacing: Space.l) {
            PageStrip(pages: pages)
            VStack(alignment: .leading, spacing: 6) {
                Text(title).textStyle(.rowTitle).foregroundStyle(Palette.ink).lineLimit(1)
                // Shop chip first and never truncated; if the date chip doesn't fit beside it,
                // it wraps onto its own line instead of squeezing "Shop will find out".
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 6) {
                        shopChip.fixedSize()
                        dateChip
                    }
                    VStack(alignment: .leading, spacing: 6) {
                        shopChip.fixedSize()
                        dateChip
                    }
                }
                if let straighten, straighten != .notFound {
                    Button(action: onToggleStraighten) {
                        HStack(spacing: 4) {
                            IconView(.scan, size: 10, weight: .bold)
                            Text(straighten == .straightened ? "Straightened" : "Original").textStyle(.captionMedium)
                        }
                        .foregroundStyle(straighten == .straightened ? Palette.ink : Palette.ink2)
                        .padding(.horizontal, 7)
                        .frame(height: 20)
                        .background(Palette.sunk, in: RoundedRectangle.continuous(6))
                    }
                    .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
                    .accessibilityLabel(straighten == .straightened ? "Straightened, double tap to use the original" : "Original, double tap to straighten")
                }
                if let photoDateHint {
                    Text(photoDateHint).textStyle(.caption).foregroundStyle(Palette.ink3).lineLimit(1)
                }
                if straighten == .notFound {
                    HStack(spacing: 4) {
                        Text("Edges not found").textStyle(.caption).foregroundStyle(Palette.ink3)
                        Button("Adjust", action: onAdjust)
                            .textStyle(.captionMedium)
                            .foregroundStyle(Palette.blueText)
                            .buttonStyle(.press(haptic: .selection))
                    }
                }
            }
            .layoutPriority(1)
            Spacer(minLength: Space.s)
            IconButton(icon: .close, label: "Remove \(title)", size: 30, glyph: 12, foreground: Palette.ink2, background: Palette.sunk, action: onRemove)
        }
        .padding(.vertical, Space.l)
        .hairline()
        .accessibilityElement(children: .contain)
    }

    private var dateChip: some View {
        Button(action: onDetails) {
            HStack(spacing: 4) {
                IconView(.calendar, size: 11, weight: .semibold)
                Text(dateText).textStyle(.caption).lineLimit(1)
            }
            .foregroundStyle(Palette.ink2)
            .padding(.horizontal, 8)
            .frame(height: Size.chipSmall)
            .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.s))
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
    }

    private var shopChip: some View {
        let needsPick = shopName == nil && !shopWillFindOut
        return Button(action: onShop) {
            HStack(spacing: 5) {
                if let shopName { ShopMark(name: shopName, size: 16) } else { UnknownShopMark(size: 16) }
                Text(shopName ?? (shopWillFindOut ? "Shop will find out" : "Pick a shop")).textStyle(.captionMedium).lineLimit(1)
            }
            .foregroundStyle(needsPick ? Palette.amberText : (shopName == nil ? Palette.ink2 : Palette.ink))
            .padding(.leading, 5)
            .padding(.trailing, 8)
            .frame(height: Size.chipSmall)
            .background(needsPick ? Palette.amberTint : Palette.sunk, in: RoundedRectangle.continuous(Radius.s))
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
    }
}

// MARK: - Join pill

/// Sits on the hairline between two import rows: "Same receipt" joins the photo above and below
/// into one multi-page receipt (max 5 pages). Joined state is blue with a closed link.
struct JoinPill: View {
    var isJoined: Bool
    var isEnabled: Bool = true
    let action: () -> Void

    var body: some View {
        Button {
            action()
        } label: {
            HStack(spacing: 5) {
                IconView(isJoined ? .link : .unlink, size: 12, weight: .semibold)
                Text(isJoined ? "Same receipt" : "Join pages").textStyle(.captionMedium)
            }
            .foregroundStyle(isJoined ? Palette.blueText : Palette.ink2)
            .padding(.horizontal, 10)
            .frame(height: 26)
            .background(isJoined ? Palette.blueTint : Palette.sunk, in: Capsule())
            .overlay(Capsule().strokeBorder(Palette.bg, lineWidth: 3))
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
        .opacity(isEnabled ? 1 : 0.4)
        .disabled(!isEnabled)
        .animation(Motion.pop, value: isJoined)
        .accessibilityLabel(isJoined ? "Pages above and below are one receipt" : "Join this photo with the next one")
    }
}

// MARK: - Upload progress card

/// Home, above the receipt list, while imports are preparing, uploading or need attention.
/// One card for the whole batch; tap opens the upload queue.
struct UploadProgressCard: View {
    enum State: Equatable {
        case preparing(done: Int, total: Int)
        case uploading(done: Int, total: Int)
        case offline(waiting: Int)
        case partial(uploaded: Int, failed: Int)
        case done(total: Int)
    }

    let state: State
    var onOpen: () -> Void = {}
    var onRetry: () -> Void = {}

    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        Button(action: onOpen) {
            HStack(spacing: Space.ml) {
                lead
                VStack(alignment: .leading, spacing: 6) {
                    Text(title)
                        .textStyle(.rowTitle)
                        .foregroundStyle(tone.text)
                        .contentTransition(reduce ? .opacity : .numericText())
                    if let fraction {
                        ProgressMeter(fraction: fraction, color: tone.solid, height: 4)
                    } else {
                        Text(caption).textStyle(.rowSub).foregroundStyle(Palette.ink2).lineLimit(1)
                    }
                }
                Spacer(minLength: Space.s)
                if case .partial = state {
                    Button("Retry", action: onRetry)
                        .textStyle(.label)
                        .foregroundStyle(tone.text)
                        .padding(.horizontal, 12)
                        .frame(height: 30)
                        .background(Palette.bg.opacity(0.7), in: Capsule())
                        .buttonStyle(.press(scale: Motion.pillScale, haptic: .light))
                } else {
                    IconView(.forward, size: 14, weight: .bold).foregroundStyle(Palette.ink3)
                }
            }
            .padding(.horizontal, Space.lx)
            .frame(minHeight: Size.attention + 8)
            .background(tone.tint, in: RoundedRectangle.continuous(Radius.l))
        }
        .buttonStyle(.press(haptic: nil))
        .animation(reduce ? Motion.reducedFade : Motion.numberRoll, value: state)
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder private var lead: some View {
        switch state {
        case .preparing, .uploading:
            IconView(.upload, size: 18, weight: .semibold).foregroundStyle(tone.text)
        case .offline:
            IconView(.wifiSlash, size: 16, weight: .semibold).foregroundStyle(tone.text)
        case .partial:
            GlyphBadge(glyph: .alert, color: tone.solid, size: 22)
        case .done:
            GlyphBadge(glyph: .check, color: tone.solid, size: 22)
        }
    }

    private var tone: Tone {
        switch state {
        case .preparing, .uploading: .blue
        case .offline: .neutral
        case .partial: .amber
        case .done: .green
        }
    }

    private var title: String {
        switch state {
        case .preparing(let done, let total): "Preparing \(done) of \(total)"
        case .uploading(let done, let total): "Uploading \(done) of \(total)"
        case .offline(let waiting): waiting == 1 ? "1 receipt waiting" : "\(waiting) receipts waiting"
        case .partial(let uploaded, let failed): "\(uploaded) uploaded · \(failed) didn't"
        case .done(let total): total == 1 ? "Receipt uploaded" : "\(total) receipts uploaded"
        }
    }

    private var caption: String {
        switch state {
        case .offline: "They upload when you're back online"
        case .partial: "Tap to see which ones"
        case .done: "Reading them now"
        default: ""
        }
    }

    private var fraction: Double? {
        switch state {
        case .preparing(let done, let total), .uploading(let done, let total):
            total > 0 ? Double(done) / Double(total) : 0
        default: nil
        }
    }
}

// MARK: - Shop suggestion card

/// A receipt arrived with no known shop and the server read one from its header.
/// Shown at the top of the receipt and (compact) on Home. Accepting adds the shop and links every
/// receipt waiting on it in one go.
struct ShopSuggestionCard: View {
    /// `new`: the shop isn't in the user's shops yet (button adds it). `existing`: the receipt looks
    /// like a shop the user already has (button moves the receipt there; nothing is created).
    enum Kind { case new, existing }

    var kind: Kind = .new
    let name: String
    var detail: String? = nil
    /// Receipts waiting on this shop, including the one on screen.
    var waiting: Int = 1
    var isWorking: Bool = false
    var onAccept: () -> Void
    var onEdit: () -> Void
    var onNotNow: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            HStack(spacing: Space.ml) {
                ShopMark(name: name, size: Size.shopMarkHeader)
                VStack(alignment: .leading, spacing: 2) {
                    Text("This looks like \(name)").textStyle(.rowTitle).foregroundStyle(Palette.ink)
                    Text(subtitle).textStyle(.rowSub).foregroundStyle(Palette.ink2).lineLimit(2)
                }
                Spacer(minLength: 0)
            }
            HStack(spacing: Space.m) {
                Button(action: onAccept) {
                    LoadingLabel(title: acceptTitle, isLoading: isWorking)
                }
                .buttonStyle(.shop(.solid(Accent.blue), size: .small, fullWidth: true, haptic: .medium))
                .disabled(isWorking)
                Button(kind == .new ? "Edit" : "Pick another", action: onEdit)
                    .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    .disabled(isWorking)
            }
            Button("Not now", action: onNotNow)
                .textStyle(.label)
                .foregroundStyle(Palette.ink2)
                .buttonStyle(.press(haptic: .selection))
                .disabled(isWorking)
                .frame(maxWidth: .infinity)
        }
        .padding(Space.lx)
        .background(Palette.blueTint, in: RoundedRectangle.continuous(Radius.xl))
        .accessibilityElement(children: .contain)
    }

    private var acceptTitle: String {
        switch kind {
        case .new: waiting > 1 ? "Add \(name) · \(waiting) receipts" : "Add \(name)"
        case .existing: waiting > 1 ? "Move \(waiting) receipts to \(name)" : "Move to \(name)"
        }
    }

    private var subtitle: String {
        var parts: [String] = []
        if let detail { parts.append(detail) }
        switch kind {
        case .new: parts.append(waiting > 1 ? "\(waiting) receipts are waiting for it" : "Not in your shops yet")
        case .existing: parts.append(waiting > 1 ? "One of your shops · \(waiting) receipts look like it" : "One of your shops")
        }
        return parts.joined(separator: " · ")
    }
}

// MARK: - Previews

#Preview("Import components") {
    let page = UIGraphicsImageRenderer(size: CGSize(width: 90, height: 130)).image { ctx in
        UIColor(white: 0.93, alpha: 1).setFill()
        ctx.fill(CGRect(x: 0, y: 0, width: 90, height: 130))
    }
    return ScrollView {
        VStack(alignment: .leading, spacing: 16) {
            ImportReceiptRow(pages: [page, page], title: "Receipt 1 · 2 pages", shopName: "Albert Heijn", dateText: "Date from receipt", onShop: {}, onDetails: {}, onRemove: {})
            JoinPill(isJoined: false) {}
            ImportReceiptRow(pages: [page], title: "Receipt 2", shopName: nil, dateText: "Date from receipt", photoDateHint: "Photo from 3 Oct", onShop: {}, onDetails: {}, onRemove: {})
            JoinPill(isJoined: true) {}
            UploadProgressCard(state: .uploading(done: 3, total: 6))
            UploadProgressCard(state: .offline(waiting: 6))
            UploadProgressCard(state: .partial(uploaded: 4, failed: 2))
            UploadProgressCard(state: .done(total: 6))
            ShopSuggestionCard(name: "Aldi", detail: "Utrecht · aldi.nl", waiting: 3, onAccept: {}, onEdit: {}, onNotNow: {})
            ShopSuggestionCard(kind: .existing, name: "Albert Heijn", detail: "Oudegracht", waiting: 1, onAccept: {}, onEdit: {}, onNotNow: {})
            ImportReceiptRow(pages: [page], title: "Receipt 3", shopWillFindOut: true, onShop: {}, onDetails: {}, onRemove: {})
        }
        .padding(20)
    }
    .background(Palette.bg)
}
