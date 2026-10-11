import SwiftUI

// MARK: - Progress meter

/// 6pt track on sunk, fill coloured by state.
struct ProgressMeter: View {
    let fraction: Double
    var color: Color = Accent.green
    var height: CGFloat = 6

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Palette.sunk)
                Capsule()
                    .fill(color)
                    .frame(width: geo.size.width * CGFloat(min(1, max(0, fraction))))
            }
        }
        .frame(height: height)
        .animation(Motion.numberRoll, value: fraction)
        .accessibilityHidden(true)
    }
}

// MARK: - Confirm bar

/// Floating bar at the bottom of review (H.1): "Lines €37,87 · Receipt €38,12", meter,
/// delta, and Confirm (always enabled, medium haptic). Numbers roll as lines change.
struct ConfirmBar: View {
    /// Sum of counted lines.
    let linesCents: Int64
    /// Printed total, if read.
    let totalCents: Int64?
    var buttonTitle: String = "Confirm"
    var isWorking: Bool = false
    /// True while only the receipt summary is on screen and its lines are still loading:
    /// shows the printed total with a neutral meter, disables Confirm and skips the
    /// number roll, so "€38,13 off" doesn't flash amber before the lines arrive.
    var isLoading: Bool = false
    let onConfirm: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduce

    private var delta: Int64? { totalCents.map { linesCents - $0 } }
    private var matches: Bool { delta.map { abs($0) <= 1 } ?? false }

    var body: some View {
        HStack(spacing: Space.l) {
            VStack(alignment: .leading, spacing: 3) {
                TypeStyle.label.withFace(.regular).text(caption)
                    .foregroundStyle(Palette.ink2)
                    .contentTransition(reduce ? .opacity : .numericText(value: Double(linesCents)))
                    .lineLimit(1)
                    .minimumScaleFactor(0.8)
                    .id(isLoading) // loading -> loaded cross-fades; a numeric roll between the two strings garbles
                    .transition(.asymmetric(insertion: .opacity.animation(Motion.fade.delay(0.06)), removal: .opacity.animation(.easeOut(duration: 0.06))))
                HStack(spacing: Space.m) {
                    ProgressMeter(fraction: isLoading ? 0 : fraction, color: matches ? Accent.green : Accent.amber)
                    TypeStyle.label.text(deltaText)
                        .foregroundStyle(isLoading ? Palette.ink3 : (matches ? Palette.greenText : Palette.amberText))
                        .contentTransition(reduce ? .opacity : .numericText(value: Double(delta ?? 0)))
                        .lineLimit(1)
                        .fixedSize()
                        .id(isLoading)
                        .transition(.asymmetric(insertion: .opacity.animation(Motion.fade.delay(0.06)), removal: .opacity.animation(.easeOut(duration: 0.06))))
                }
            }
            Button(action: onConfirm) {
                LoadingLabel(title: buttonTitle, isLoading: isWorking)
                    .labelMorph()
            }
            .buttonStyle(.shop(size: .confirm, fullWidth: false, haptic: .medium))
            .disabled(isWorking || isLoading)
        }
        .padding(.vertical, Space.xl)
        .padding(.leading, 22)
        .padding(.trailing, Space.xl)
        .background(Palette.tray, in: RoundedRectangle.continuous(Radius.bar))
        .elevationRing(RoundedRectangle.continuous(Radius.bar))
        .elevation(.tray)
        .animation(isLoading ? nil : (reduce ? Motion.reducedFade : Motion.numberRoll), value: linesCents)
        .animation(Motion.fade, value: isLoading)
        .accessibilityElement(children: .contain)
    }

    private var caption: String {
        if isLoading, let totalCents { return "Receipt \(Money.format(totalCents))" }
        if let totalCents {
            return "Lines \(Money.format(linesCents)) · Receipt \(Money.format(totalCents))"
        }
        return "Lines \(Money.format(linesCents)) · no printed total"
    }

    private var deltaText: String {
        if isLoading { return "Reading lines…" }
        guard let delta else { return "Add the total" }
        if abs(delta) <= 1 { return "Adds up" }
        return "\(Money.format(Int64(delta.magnitude))) off"
    }

    private var fraction: Double {
        guard let totalCents, totalCents > 0 else { return 0 }
        let a = Double(linesCents), b = Double(totalCents)
        return min(a, b) / max(a, b)
    }
}

#Preview("Confirm bar") {
    @Previewable @State var lines: Int64 = 3787
    VStack(spacing: 20) {
        Spacer()
        Button("+ €0,25") { lines += 25 }.buttonStyle(.shop(.secondary))
        ConfirmBar(linesCents: lines, totalCents: 3812) {}
        ConfirmBar(linesCents: 3812, totalCents: 3812) {}
    }
    .padding(8)
    .background(Palette.bg)
}
