import SwiftUI

// Product auto-population UI (docs/DESIGN-PRODUCT-AUTOPOPULATE.md): the "New products" review queue
// card, its skeleton, and the scanner's per-barcode rows and tally.

// MARK: - Review reasons

/// Why a product is waiting for a look. Raw values are the API's `review_reason`.
enum ReviewReasonKind: String, CaseIterable, Sendable {
    case nearMatch = "near_match"
    case barcodeNameMatch = "barcode_name_match"
    case oddName = "odd_name"
    case noCategory = "no_category"
    case barcodeMinimal = "barcode_minimal"
    case barcodeOffUnavailable = "barcode_off_unavailable"

    /// Section header and card lead line.
    var headline: String {
        switch self {
        case .nearMatch: "Might be the same product"
        case .barcodeNameMatch: "Matches a product from your receipts"
        case .oddName: "The name looks garbled"
        case .noCategory: "No category yet"
        case .barcodeMinimal: "Not on Open Food Facts"
        case .barcodeOffUnavailable: "Details will fill in later"
        }
    }

    /// One sentence under the headline; `suggestion` is the name of the suggested merge target.
    func explanation(suggestion: String?) -> String {
        switch self {
        case .nearMatch: suggestion.map { "Looks like \($0). Same product, or a different one?" } ?? "Looks like a product you already have."
        case .barcodeNameMatch: suggestion.map { "This barcode looks like \($0) from your receipts." } ?? "This barcode looks like a product from your receipts."
        case .oddName: "The receipt text was hard to read. Check the name."
        case .noCategory: "Pick a category so it counts in your analytics."
        case .barcodeMinimal: "Give it a name so you can find it later."
        case .barcodeOffUnavailable: "Open Food Facts couldn't be reached. The server tries again soon."
        }
    }

    var icon: Icon {
        switch self {
        case .nearMatch, .barcodeNameMatch: .link
        case .oddName: .edit
        case .noCategory: .grid
        case .barcodeMinimal: .barcode
        case .barcodeOffUnavailable: .clock
        }
    }

    /// Amber = needs a look. "Fills in later" is information, so blue.
    var tone: Tone { self == .barcodeOffUnavailable ? .blue : .amber }

    /// Label of the main button. `suggestion` is the merge target's name.
    func primaryTitle(suggestion: String?) -> String {
        switch self {
        case .nearMatch, .barcodeNameMatch: suggestion.map { "Same as \($0)" } ?? "Same product"
        case .oddName: "Looks right"
        case .noCategory, .barcodeOffUnavailable: "Accept"
        case .barcodeMinimal: "Name it"
        }
    }

    var secondaryTitle: String {
        switch self {
        case .nearMatch, .barcodeNameMatch: "Keep separate"
        case .oddName, .barcodeOffUnavailable: "Edit name"
        case .noCategory: "Pick category"
        case .barcodeMinimal: "Keep as is"
        }
    }
}

// MARK: - Review card

/// One product in the "New products" queue. Product, why it is here, the receipt line it came
/// from (or the scanned barcode), two buttons chosen by the reason, and a ⋯ for the rest
/// (edit name, pick category, discard).
struct ReviewCard: View {
    struct Source {
        var shopName: String
        /// "Sun 30 Aug"
        var dateText: String
        var rawText: String
        var amountText: String?
    }

    let name: String
    var detail: String? = nil
    var categoryName: String? = nil
    /// The category's stored colour token; wins over the name.
    var categoryToken: String? = nil
    var imageURL: URL? = nil
    let reason: ReviewReasonKind
    var suggestionName: String? = nil
    var barcode: String? = nil
    var source: Source? = nil
    var isWorking = false
    /// Offline: the card stays readable, actions are disabled with a caption.
    var isOffline = false
    var onPrimary: () -> Void = {}
    var onSecondary: () -> Void = {}
    var onCategory: () -> Void = {}
    var onMore: () -> Void = {}

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            HStack(alignment: .top, spacing: Space.l) {
                ProductTile(name: name, category: categoryName, categoryToken: categoryToken, imageURL: imageURL, size: 44)
                VStack(alignment: .leading, spacing: 3) {
                    TypeStyle.rowTitle.text(name).foregroundStyle(Palette.ink).lineLimit(2)
                    HStack(spacing: 6) {
                        if let detail { TypeStyle.rowSub.text(detail).foregroundStyle(Palette.ink2).lineLimit(1) }
                        categoryChip
                    }
                }
                Spacer(minLength: Space.s)
                IconButton(icon: .more, label: "More for \(name)", size: 32, glyph: 15, foreground: Palette.ink2, background: Palette.bg, action: onMore)
                    .disabled(isWorking || isOffline)
            }

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    IconView(reason.icon, size: 12, weight: .semibold)
                    TypeStyle.captionMedium.text(reason.headline)
                }
                .foregroundStyle(reason.tone.text)
                TypeStyle.rowSub.text(reason.explanation(suggestion: suggestionName))
                    .foregroundStyle(Palette.ink2)
                    .fixedSize(horizontal: false, vertical: true)
            }

            if let source {
                RawRowTile(
                    caption: "On \(source.shopName) · \(source.dateText)",
                    raw: source.rawText,
                    amount: source.amountText,
                    background: Palette.bg
                )
            } else if let barcode {
                RawRowTile(caption: "Scanned barcode", raw: barcode, amount: nil, background: Palette.bg)
            }

            ViewThatFits(in: .horizontal) {
                HStack(spacing: Space.m) {
                    primaryButton(fullWidth: true)
                    secondaryButton(fullWidth: false)
                }
                VStack(spacing: Space.m) {
                    primaryButton(fullWidth: true)
                    secondaryButton(fullWidth: true)
                }
            }
            .disabled(isWorking || isOffline)

            if isOffline {
                TypeStyle.caption.text("Needs a connection").foregroundStyle(Palette.ink3)
            }
        }
        .padding(Space.lx)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.card))
        .opacity(isWorking ? 0.7 : 1)
        .animation(Motion.fade, value: isWorking)
        .accessibilityElement(children: .contain)
    }

    private func primaryButton(fullWidth: Bool) -> some View {
        Button(action: onPrimary) {
            LoadingLabel(title: reason.primaryTitle(suggestion: suggestionName), isLoading: isWorking)
                .lineLimit(1)
        }
        .buttonStyle(.shop(size: .small, fullWidth: fullWidth, haptic: .medium))
    }

    private func secondaryButton(fullWidth: Bool) -> some View {
        Button(reason.secondaryTitle, action: onSecondary)
            .lineLimit(1)
            .buttonStyle(.shop(.tinted(text: Palette.ink, tint: Palette.bg), size: .small, fullWidth: fullWidth, haptic: .selection))
    }

    @ViewBuilder private var categoryChip: some View {
        if let categoryName {
            Button(action: onCategory) {
                HStack(spacing: 4) {
                    CategoryDot(color: CategoryColor.swatch(token: categoryToken, name: categoryName).color)
                    TypeStyle.caption.text(categoryName)
                }
                .foregroundStyle(Palette.ink2)
            }
            .buttonStyle(.press(haptic: .selection))
        } else {
            Button(action: onCategory) {
                TypeStyle.captionMedium.text("No category").foregroundStyle(Palette.amberText)
                    .padding(.horizontal, 7)
                    .frame(height: 20)
                    .background(Palette.amberTint, in: RoundedRectangle.continuous(6))
            }
            .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
        }
    }
}

/// Same footprint as `ReviewCard` so the real cards replace it in place.
struct ReviewCardSkeleton: View {
    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            HStack(spacing: Space.l) {
                RoundedRectangle.continuous(14).fill(Palette.bg).frame(width: 44, height: 44)
                VStack(alignment: .leading, spacing: 8) {
                    bar(width: 150, height: 14)
                    bar(width: 90, height: 10)
                }
                Spacer()
            }
            VStack(alignment: .leading, spacing: 8) {
                bar(width: 190, height: 10)
                bar(width: 250, height: 10)
            }
            RoundedRectangle.continuous(Radius.l).fill(Palette.bg).frame(height: 58)
            HStack(spacing: Space.m) {
                Capsule().fill(Palette.bg).frame(height: 36)
                Capsule().fill(Palette.bg).frame(height: 36)
            }
        }
        .padding(Space.lx)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.card))
        .accessibilityHidden(true)
    }

    private func bar(width: CGFloat, height: CGFloat) -> some View {
        RoundedRectangle.continuous(height / 2).fill(Palette.bg).frame(width: width, height: height)
    }
}

// MARK: - Scanner rows (dark tray)

private enum ScanPalette {
    static let ink = Palette.scanInk
    static let ink2 = Palette.scanInk2
    static let line = Palette.scanField
    static let green = Palette.scanGreen
    static let amber = Palette.scanAmber
    static let red = Palette.scanRed
    static let blue = Palette.scanBlue
}

/// What a scanned barcode turned into. One row per code in the Unpack session tray.
enum ScanOutcome: Equatable {
    case looking
    case known(name: String, meta: String?)
    /// Created from Open Food Facts data.
    case created(name: String, category: String?)
    /// Created with only the barcode: the user should name it.
    case createdMinimal(name: String)
    /// Created, Open Food Facts could not be reached: details arrive later.
    case offUnavailable(name: String)
    /// Scanned offline: the code waits on the phone.
    case queued
    case failed(message: String)
}

/// One barcode in the dark session tray, with an optional inline merge prompt and a receipt-lines chip.
struct ScanEntryRow: View {
    let code: String
    let outcome: ScanOutcome
    /// "Same as AH BIO MELK?" shown under the row while a merge suggestion is open.
    var mergeSuggestion: String? = nil
    /// Unmatched receipt lines (last 14 days) the user can link, shown as a chip.
    var receiptLines: Int = 0
    var onName: () -> Void = {}
    var onRetry: () -> Void = {}
    var onLinkLines: () -> Void = {}
    var onMerge: () -> Void = {}
    var onDismissMerge: () -> Void = {}

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: Space.l) {
                lead
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).textStyle(outcomeIsQueued ? TypeStyle.mono.sized(14) : .rowTitle).foregroundStyle(ScanPalette.ink).lineLimit(1)
                    TypeStyle.rowSub.text(subtitle).foregroundStyle(subtitleColor).lineLimit(1)
                }
                Spacer(minLength: Space.m)
                trailing
            }
            .padding(.horizontal, Space.xs)
            .frame(height: 50)

            if let mergeSuggestion {
                HStack(spacing: Space.m) {
                    TypeStyle.label.text("Same as \(mergeSuggestion)?")
                        .foregroundStyle(ScanPalette.blue)
                        .lineLimit(1)
                    Spacer(minLength: Space.s)
                    Button("No", action: onDismissMerge)
                        .buttonStyle(.shop(.tinted(text: ScanPalette.ink, tint: ScanPalette.line), size: .small, fullWidth: false, haptic: .selection))
                    Button("Merge", action: onMerge)
                        .buttonStyle(.shop(.tinted(text: Color(hex: 0x0C0C0B), tint: ScanPalette.ink), size: .small, fullWidth: false, haptic: .medium))
                }
                .padding(.horizontal, Space.l)
                .padding(.vertical, Space.m)
                .background(ScanPalette.blue.opacity(0.14), in: RoundedRectangle.continuous(Radius.l))
                .padding(.bottom, Space.m)
                .transition(.opacity.combined(with: .offset(y: -6)))
            }
        }
        .overlay(alignment: .bottom) { ScanPalette.line.frame(height: 1) }
        .animation(Motion.heightSpring, value: mergeSuggestion)
        .accessibilityElement(children: .contain)
    }

    private var outcomeIsQueued: Bool { outcome == .queued }

    // MARK: Pieces

    @ViewBuilder private var lead: some View {
        let (icon, fg, tint): (Icon, Color, Color) = {
            switch outcome {
            case .looking: (.search, ScanPalette.ink2, ScanPalette.line)
            case .known: (.check, ScanPalette.green, ScanPalette.green.opacity(0.16))
            case .created: (.plus, ScanPalette.green, ScanPalette.green.opacity(0.16))
            case .createdMinimal: (.plus, ScanPalette.amber, ScanPalette.amber.opacity(0.18))
            case .offUnavailable: (.clock, ScanPalette.blue, ScanPalette.blue.opacity(0.16))
            case .queued: (.wifiSlash, ScanPalette.ink2, ScanPalette.line)
            case .failed: (.retry, ScanPalette.red, ScanPalette.red.opacity(0.18))
            }
        }()
        Group {
            if outcome == .looking {
                ProgressView().controlSize(.small).tint(ScanPalette.ink2)
            } else {
                IconView(icon, size: 16, weight: .bold)
            }
        }
        .foregroundStyle(fg)
        .frame(width: 32, height: 32)
        .background(tint, in: RoundedRectangle.continuous(10))
    }

    @ViewBuilder private var trailing: some View {
        switch outcome {
        case .createdMinimal, .offUnavailable:
            Button(outcomeNeedsName ? "Name it" : "Add name", action: onName)
                .buttonStyle(.shop(.tinted(text: outcomeNeedsName ? Color(hex: 0x0C0C0B) : ScanPalette.ink, tint: outcomeNeedsName ? ScanPalette.ink : ScanPalette.line), size: .small, fullWidth: false, haptic: .light))
        case .failed:
            Button("Retry", action: onRetry)
                .buttonStyle(.shop(.tinted(text: ScanPalette.ink, tint: ScanPalette.line), size: .small, fullWidth: false, haptic: .light))
        case .created, .known:
            if receiptLines > 0 {
                Button(action: onLinkLines) {
                    TypeStyle.captionMedium.text("\(receiptLines) on receipts")
                }
                .foregroundStyle(ScanPalette.blue)
                .buttonStyle(.press(scale: Motion.pillScale, haptic: .light))
            }
        default:
            EmptyView()
        }
    }

    private var outcomeNeedsName: Bool {
        if case .createdMinimal = outcome { return true }
        return false
    }

    private var title: String {
        switch outcome {
        case .looking: "Looking up…"
        case .known(let name, _), .created(let name, _), .createdMinimal(let name), .offUnavailable(let name): name
        case .queued: code
        case .failed: "Couldn't look up"
        }
    }

    private var subtitle: String {
        switch outcome {
        case .looking: code
        case .known(_, let meta): ["Known", meta].compactMap { $0 }.joined(separator: " · ")
        case .created(_, let category): ["Added", category ?? "Open Food Facts"].joined(separator: " · ")
        case .createdMinimal: "New · add a name"
        case .offUnavailable: "Added · details fill in later"
        case .queued: "Waiting for a connection"
        case .failed(let message): message
        }
    }

    private var subtitleColor: Color {
        switch outcome {
        case .createdMinimal: ScanPalette.amber
        case .failed: ScanPalette.red
        default: ScanPalette.ink2
        }
    }
}

/// Under "This session": 9 known · 4 added · 1 to name. Numbers roll as scans land.
struct ScanTally: View {
    var known: Int
    var added: Int
    var toName: Int

    var body: some View {
        HStack(spacing: Space.lx) {
            item(known, "known", ScanPalette.green)
            item(added, "added", ScanPalette.blue)
            if toName > 0 { item(toName, "to name", ScanPalette.amber) }
            Spacer(minLength: 0)
        }
        .animation(Motion.numberRoll, value: known)
        .animation(Motion.numberRoll, value: added)
        .animation(Motion.numberRoll, value: toName)
        .accessibilityElement(children: .combine)
    }

    private func item(_ count: Int, _ label: String, _ color: Color) -> some View {
        HStack(spacing: 5) {
            StatusDot(color: color, size: 7)
            TypeStyle.label.text("\(count)").foregroundStyle(ScanPalette.ink).contentTransition(.numericText(value: Double(count)))
            TypeStyle.rowSub.text(label).foregroundStyle(ScanPalette.ink2)
        }
    }
}

// MARK: - Previews

#Preview("Review cards") {
    ScrollView {
        VStack(spacing: 12) {
            ReviewCard(
                name: "Arla bio halfvolle melk", detail: "1 l", categoryName: "Dairy", reason: .nearMatch, suggestionName: "ARLA MELK",
                source: .init(shopName: "Albert Heijn", dateText: "Sun 30 Aug", rawText: "ARLA BIO HALFV. MELK", amountText: "1,89")
            )
            ReviewCard(name: "Hummus naturel", reason: .noCategory, source: .init(shopName: "Lidl", dateText: "Thu 8 Oct", rawText: "HUMMUS NATUREL", amountText: "1,49"))
            ReviewCard(name: "Unknown product 2345", reason: .barcodeMinimal, barcode: "8710400012345")
            ReviewCard(name: "Griekse yoghurt", reason: .barcodeNameMatch, suggestionName: "AH GRIEKS YOGH", barcode: "8718452390010", isOffline: true)
            ReviewCardSkeleton()
        }
        .padding(20)
    }
    .background(Palette.bg)
}
