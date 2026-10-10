import Foundation

/// Money is always integer cents (PLAN §3). Formatting follows the wireframes:
/// `€38,12`, `€2.614,90`, line amounts without symbol `1,79`, discounts `−0,45`,
/// per unit `€0,85/l`. The minus sign is U+2212, as in the designs.
nonisolated enum Money {
    static let minus = "\u{2212}"

    /// "2.614,90" — grouped euros, comma decimals, no symbol, no sign.
    static func digits(_ cents: Int64) -> String {
        let a = cents.magnitude
        let euros = a / 100
        let rest = a % 100
        return grouped(euros) + "," + (rest < 10 ? "0\(rest)" : "\(rest)")
    }

    /// "€38,12" (negative: "−€0,45").
    static func format(_ cents: Int64) -> String {
        (cents < 0 ? minus : "") + "€" + digits(cents)
    }

    /// Receipt-line style: "1,79", discounts "−0,45".
    static func plain(_ cents: Int64) -> String {
        (cents < 0 ? minus : "") + digits(cents)
    }

    /// Always signed: "+€1,20" / "−€0,45".
    static func signed(_ cents: Int64) -> String {
        (cents < 0 ? minus : "+") + "€" + digits(cents)
    }

    /// Rounded whole euros: "€78", "€2.615".
    static func whole(_ cents: Int64) -> String {
        let euros = (cents.magnitude + 50) / 100
        return (cents < 0 ? minus : "") + "€" + grouped(euros)
    }

    /// Hero split: ("€", "412", ",38").
    static func heroParts(_ cents: Int64) -> (symbol: String, whole: String, fraction: String) {
        let a = cents.magnitude
        let rest = a % 100
        return ((cents < 0 ? minus : "") + "€", grouped(a / 100), "," + (rest < 10 ? "0\(rest)" : "\(rest)"))
    }

    /// Per base unit: "€0,85/l", "€11,49/kg", "€0,33/pc".
    static func perUnit(_ cents: Int64, unit: BaseUnit) -> String {
        format(cents) + "/" + unit.rawValue
    }

    /// Parses user input like "1,29", "€ 1.29", "-0,45", "0,45-", "1.234,56" into cents.
    static func parse(_ text: String) -> Int64? {
        var s = text.trimmingCharacters(in: .whitespaces)
            .replacingOccurrences(of: "€", with: "")
            .replacingOccurrences(of: " ", with: "")
            .replacingOccurrences(of: minus, with: "-")
        guard !s.isEmpty else { return nil }
        var negative = false
        if s.hasPrefix("-") { negative = true; s.removeFirst() }
        if s.hasSuffix("-") { negative = true; s.removeLast() }
        // Decide the decimal separator: the last of "," or "." followed by 1–2 digits.
        var euros = s
        var fraction = ""
        if let idx = s.lastIndex(where: { $0 == "," || $0 == "." }) {
            let after = s[s.index(after: idx)...]
            if after.count <= 2 {
                euros = String(s[..<idx])
                fraction = String(after)
            }
        }
        euros = euros.replacingOccurrences(of: ".", with: "").replacingOccurrences(of: ",", with: "")
        if euros.isEmpty { euros = "0" }
        while fraction.count < 2 { fraction += "0" }
        guard let e = Int64(euros), let f = Int64(fraction), euros.allSatisfy(\.isNumber), fraction.allSatisfy(\.isNumber) else { return nil }
        let value = e * 100 + f
        return negative ? -value : value
    }

    /// Editable text for a field: "1,29" (no symbol).
    static func editable(_ cents: Int64?) -> String {
        guard let cents else { return "" }
        return plain(cents).replacingOccurrences(of: minus, with: "-")
    }

    private static func grouped(_ value: UInt64) -> String {
        let s = String(value)
        guard s.count > 3 else { return s }
        var out = ""
        for (i, ch) in s.enumerated() {
            if i > 0 && (s.count - i) % 3 == 0 { out.append(".") }
            out.append(ch)
        }
        return out
    }
}

/// Base unit for per-unit prices (PricePoint/ShopPrice `base_unit`).
nonisolated enum BaseUnit: String, Codable, Sendable, CaseIterable {
    case kg, l, pc
}

/// Quantity formatting: "0,842 kg", "1,5 l", "× 2".
nonisolated enum Quantity {
    static func decimal(_ value: Double, maxFraction: Int = 3) -> String {
        let f = NumberFormatter()
        f.locale = Locale(identifier: "nl_NL")
        f.minimumFractionDigits = 0
        f.maximumFractionDigits = maxFraction
        f.usesGroupingSeparator = false
        return f.string(from: NSNumber(value: value)) ?? String(value)
    }

    /// Parses "0,842" or "0.842".
    static func parse(_ text: String) -> Double? {
        Double(text.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: ",", with: "."))
    }

    /// Package size: "1,5 l", "500 g", "10 pc".
    static func netSize(_ quantity: Double?, unit: String?) -> String? {
        guard let quantity, let unit else { return nil }
        if unit == "ml" && quantity >= 1000 { return decimal(quantity / 1000) + " l" }
        if unit == "g" && quantity >= 1000 { return decimal(quantity / 1000) + " kg" }
        return decimal(quantity) + " " + unit
    }
}
