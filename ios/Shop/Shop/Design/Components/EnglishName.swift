import SwiftUI

// MARK: - Needs English (spec: notes/design-english-names.md)

/// One word the dictionary did not know. Mirrors the API `Span`: `start`/`end` are UTF-16 offsets
/// (end exclusive) into the string that is displayed.
struct UnknownWord: Hashable {
    let word: String
    let key: String
    let start: Int
    let end: Int
}

/// "Needs English": amber pill next to a product or receipt line whose English is partial or missing.
struct NeedsEnglishBadge: View {
    var body: some View {
        TypeStyle.captionMedium.text("Needs English")
            .foregroundStyle(Palette.amberText)
            .padding(.horizontal, 6)
            .frame(height: 20)
            .background(Palette.amberTint, in: RoundedRectangle.continuous(Radius.s))
            .accessibilityLabel("Needs English")
    }
}

/// Where an English name came from, as a quiet 12 pt hint: "You", "Open Food Facts", "Auto".
/// Nothing for `partial` or `missing` (the badge says it).
struct EnglishSourceHint: View {
    enum Source {
        case you, openFoodFacts, auto

        var title: String {
            switch self {
            case .you: "You"
            case .openFoodFacts: "Open Food Facts"
            case .auto: "Auto"
            }
        }
    }

    let source: Source

    var body: some View {
        TypeStyle.caption.text(source.title).foregroundStyle(Palette.ink3)
    }
}

/// A product or line name with its unknown words highlighted (amber wash, amber ink). Tapping a
/// highlighted word calls `onTapWord` (the app opens "Teach a word"). Without a handler the words are
/// highlighted but inert. Spans that fall outside the string are ignored, so a stale response can never crash.
struct EnglishNameText: View {
    let text: String
    var unknown: [UnknownWord] = []
    var style: TypeStyle = .rowTitle
    var lineLimit: Int = 2
    var onTapWord: ((UnknownWord) -> Void)? = nil

    var body: some View {
        Text(attributed)
            .lineLimit(lineLimit)
            .environment(\.openURL, OpenURLAction { url in
                guard let onTapWord, let index = Self.index(of: url), unknown.indices.contains(index) else {
                    return .discarded
                }
                Haptics.play(.light)
                onTapWord(unknown[index])
                return .handled
            })
    }

    static let scheme = "shop-unknown-word"

    private static func index(of url: URL) -> Int? {
        guard url.scheme == scheme, let host = url.host else { return nil }
        return Int(host)
    }

    private var attributed: AttributedString {
        var out = AttributedString(text)
        out.font = style.font
        out.tracking = style.tracking
        for (i, word) in unknown.enumerated() {
            guard word.start >= 0, word.end > word.start, word.end <= text.utf16.count,
                  let range = Range(NSRange(location: word.start, length: word.end - word.start), in: out)
            else { continue }
            out[range].backgroundColor = Palette.highlight
            out[range].foregroundColor = Palette.amberText
            if onTapWord != nil, let url = URL(string: "\(Self.scheme)://\(i)") {
                out[range].link = url
            }
        }
        return out
    }
}
