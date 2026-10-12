import SwiftUI

// MARK: - Pill action

/// Small `sunk` pill with a leading glyph: "Edit English name" under a product title (canvas P1). 30pt.
struct PillAction: View {
    let title: String
    var icon: Icon = .edit
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 6) {
                IconView(icon, size: 13, weight: .semibold)
                TypeStyle.label.text(title)
            }
            .foregroundStyle(Palette.ink)
            .padding(.leading, 10)
            .padding(.trailing, 12)
            .frame(minHeight: 30)
            .background(Palette.sunk, in: Capsule())
            .contentShape(Capsule())
        }
        .buttonStyle(.press(scale: Motion.pillScale, haptic: .light))
    }
}

// MARK: - Action row

/// A tappable row at the bottom of a detail screen ("Merge into another product", "Delete product").
/// Looks like a row, not a heading: 15.5pt Medium, glyph tile on the left, chevron on the right, hairline below.
struct ActionRow: View {
    enum Tone { case normal, destructive }

    let title: String
    var icon: Icon
    var tone: Tone = .normal
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.l) {
                IconView(icon, size: 16, weight: .semibold)
                    .frame(width: 32, height: 32)
                    .background(tone == .destructive ? Palette.redTint : Palette.sunk, in: RoundedRectangle.continuous(Radius.tile))
                TypeStyle.rowTitle.sized(15.5).text(title)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if tone == .normal {
                    IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                }
            }
            .foregroundStyle(tone == .destructive ? Palette.redText : Palette.ink)
            .frame(minHeight: 52)
            .contentShape(Rectangle())
            .hairline()
        }
        .buttonStyle(.press(haptic: .selection))
    }
}

// MARK: - Camera off

/// "Camera is off" with a way out (canvas C-CameraDenied): shown in place of a scanner when access was denied.
struct CameraOffState: View {
    var message: String = "Shop needs the camera to read receipts and barcodes. You can also add receipts from your photos."
    let onOpenSettings: () -> Void
    var onChoosePhoto: (() -> Void)? = nil

    var body: some View {
        EmptyState(
            title: "Camera is off",
            message: message,
            action: (title: "Open Settings", run: onOpenSettings)
        ) {
            GlyphIllustration(icon: .camera, color: Accent.amber)
        }
        .overlay(alignment: .bottom) {
            if let onChoosePhoto {
                Button("Choose a photo instead", action: onChoosePhoto)
                    .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    .padding(.bottom, Space.xl)
            }
        }
    }
}
