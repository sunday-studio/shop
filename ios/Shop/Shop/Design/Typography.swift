import CoreText
import SwiftUI
import UIKit

// GT America registration
// -----------------------
// Bundled files (ios/Shop/Resources/Fonts/), add all five to the app target:
//   GTAmerica-Regular.otf       -> GTAmericaTrial-Rg
//   GTAmerica-Medium.otf        -> GTAmericaTrial-Md
//   GTAmerica-Bold.otf          -> GTAmericaTrial-Bd
//   GTAmericaMono-Regular.otf   -> GTAmericaMonoTrial-Rg
//   GTAmericaMono-Medium.otf    -> GTAmericaMonoTrial-Md
//
// The project generates its Info.plist, which can't carry UIAppFonts, so `ShopFont.register()`
// registers the bundled files with Core Text at launch (AppModel.init), before any view draws.
// The names used in code are the internal PostScript names above, not the file names.
//
// Rules (design/DESIGN-RULES.md): Bold only for screen titles and the hero. Rows, labels,
// buttons, chips and tabs use Regular or Medium. Numbers are tabular everywhere.

enum ShopFont {
    static let regular = "GTAmericaTrial-Rg"
    static let medium = "GTAmericaTrial-Md"
    static let bold = "GTAmericaTrial-Bd"
    static let monoRegular = "GTAmericaMonoTrial-Rg"
    static let monoMedium = "GTAmericaMonoTrial-Md"

    static let files = [
        "GTAmerica-Regular", "GTAmerica-Medium", "GTAmerica-Bold",
        "GTAmericaMono-Regular", "GTAmericaMono-Medium",
    ]

    static func register() {
        for file in files {
            guard let url = Bundle.main.url(forResource: file, withExtension: "otf")
                ?? Bundle.main.url(forResource: file, withExtension: "otf", subdirectory: "Fonts")
            else { continue }
            CTFontManagerRegisterFontsForURL(url as CFURL, .process, nil)
        }
        #if DEBUG
        for name in [regular, medium, bold, monoRegular, monoMedium] where UIFont(name: name, size: 12) == nil {
            print("⚠️ Font \(name) is not registered. Check the .otf files are in the app target.")
        }
        #endif
    }
}

/// One entry of the type scale (docs/DESIGN-SYSTEM.md §2).
struct TypeStyle: Sendable {
    enum Face: Sendable { case regular, medium, bold, monoRegular, monoMedium }

    let size: CGFloat
    let face: Face
    /// Letter-spacing in em (multiplied by size).
    let trackingEm: CGFloat
    let relativeTo: Font.TextStyle

    var fontName: String {
        switch face {
        case .regular: ShopFont.regular
        case .medium: ShopFont.medium
        case .bold: ShopFont.bold
        case .monoRegular: ShopFont.monoRegular
        case .monoMedium: ShopFont.monoMedium
        }
    }

    var font: Font { .custom(fontName, size: size, relativeTo: relativeTo) }
    var tracking: CGFloat { trackingEm * size }

    func sized(_ newSize: CGFloat) -> TypeStyle {
        TypeStyle(size: newSize, face: face, trackingEm: trackingEm, relativeTo: relativeTo)
    }

    func withFace(_ newFace: Face) -> TypeStyle {
        TypeStyle(size: size, face: newFace, trackingEm: trackingEm, relativeTo: relativeTo)
    }
}

extension TypeStyle {
    /// Hero number (Home, Analytics), 60. Currency and cents use `heroMinor`.
    static let hero = TypeStyle(size: 60, face: .regular, trackingEm: -0.05, relativeTo: .largeTitle)
    static let heroMinor = TypeStyle(size: 40, face: .regular, trackingEm: -0.05, relativeTo: .largeTitle)
    /// Onboarding headline, 38 (O.1) / 32 (O.2–O.4).
    static let display = TypeStyle(size: 38, face: .regular, trackingEm: -0.045, relativeTo: .largeTitle)
    static let screenTitle = TypeStyle(size: 28, face: .bold, trackingEm: -0.04, relativeTo: .title)
    static let emptyTitle = TypeStyle(size: 22, face: .regular, trackingEm: -0.03, relativeTo: .title2)
    static let trayTitle = TypeStyle(size: 19, face: .bold, trackingEm: -0.02, relativeTo: .title3)
    static let navTitle = TypeStyle(size: 16, face: .medium, trackingEm: 0, relativeTo: .headline)
    static let button = TypeStyle(size: 16, face: .medium, trackingEm: 0, relativeTo: .body)
    static let input = TypeStyle(size: 16, face: .regular, trackingEm: 0, relativeTo: .body)
    static let rowTitle = TypeStyle(size: 15, face: .medium, trackingEm: 0, relativeTo: .body)
    static let body = TypeStyle(size: 14, face: .regular, trackingEm: 0, relativeTo: .callout)
    static let bodyLarge = TypeStyle(size: 15.5, face: .regular, trackingEm: 0, relativeTo: .body)
    static let label = TypeStyle(size: 13, face: .medium, trackingEm: 0, relativeTo: .subheadline)
    static let rowSub = TypeStyle(size: 12.5, face: .regular, trackingEm: 0, relativeTo: .footnote)
    static let caption = TypeStyle(size: 12, face: .regular, trackingEm: 0, relativeTo: .caption)
    static let captionMedium = TypeStyle(size: 12, face: .medium, trackingEm: 0, relativeTo: .caption)
    static let micro = TypeStyle(size: 10.5, face: .regular, trackingEm: 0, relativeTo: .caption2)
    static let tab = TypeStyle(size: 10.5, face: .medium, trackingEm: 0, relativeTo: .caption2)
    /// Raw receipt text, URLs, barcodes, CLI commands.
    static let mono = TypeStyle(size: 13.5, face: .monoMedium, trackingEm: 0, relativeTo: .body)
}

extension TypeStyle {
    /// GT America's digits are proportional and the font has no tabular feature, so
    /// `.monospacedDigit()` does nothing. The design canvas gets tabular figures by taking the digits
    /// 0 to 9 from GT America Mono; this does the same: digit runs use the mono face at the same size.
    var digitFont: Font {
        let name: String
        switch face {
        case .regular: name = ShopFont.monoRegular
        case .medium, .bold, .monoMedium: name = ShopFont.monoMedium
        case .monoRegular: name = ShopFont.monoRegular
        }
        return .custom(name, size: size, relativeTo: relativeTo)
    }

    /// Text in this style with tabular digits. Use it for anything that can contain a number:
    /// amounts, prices, counts, dates, times, quantities.
    func text(_ string: String) -> Text {
        var out = AttributedString()
        var run = ""
        var runIsDigit = false
        func flush() {
            guard !run.isEmpty else { return }
            var part = AttributedString(run)
            part.font = runIsDigit ? digitFont : font
            part.tracking = tracking
            out.append(part)
            run = ""
        }
        for ch in string {
            let isDigit = ch.isASCII && ch.isNumber
            if isDigit != runIsDigit { flush(); runIsDigit = isDigit }
            run.append(ch)
        }
        flush()
        return Text(out)
    }
}

extension View {
    /// Applies a type style: font and tracking. For text that can contain digits use
    /// `style.text(string)` instead (tabular figures); this modifier cannot change the digits.
    func textStyle(_ style: TypeStyle) -> some View {
        font(style.font)
            .tracking(style.tracking)
            .monospacedDigit()
    }
}

/// Wrap the app root once so every descendant Text gets the body face and tabular digits.
struct ShopTypography: ViewModifier {
    func body(content: Content) -> some View {
        content
            .font(TypeStyle.body.font)
            .monospacedDigit()
            .foregroundStyle(Palette.ink)
    }
}

extension View {
    func shopTypography() -> some View { modifier(ShopTypography()) }
}
