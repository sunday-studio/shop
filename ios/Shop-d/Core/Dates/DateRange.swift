import Foundation

/// An inclusive local-date range for analytics (A.4 presets + custom).
nonisolated struct DateRange: Hashable, Sendable {
    enum Preset: String, CaseIterable, Sendable {
        case thisMonth, lastMonth, last3Months, last6Months, thisYear, allTime, custom

        var title: String {
            switch self {
            case .thisMonth: "This month"
            case .lastMonth: "Last month"
            case .last3Months: "Last 3 months"
            case .last6Months: "Last 6 months"
            case .thisYear: "This year"
            case .allTime: "All time"
            case .custom: "Custom"
            }
        }
    }

    var preset: Preset
    /// Start of the first day (local).
    var from: Date
    /// Start of the last included day (local).
    var to: Date
    /// "Bars per" choice from A.4; nil picks by length.
    var bars: SpendGrouping? = nil

    /// Short preset label for the A.4 grid.
    static func gridTitle(_ preset: Preset) -> String {
        switch preset {
        case .last3Months: "3 months"
        case .last6Months: "6 months"
        default: preset.title
        }
    }

    static func preset(_ preset: Preset, now: Date = .now) -> DateRange {
        let cal = Dates.calendar
        let today = cal.startOfDay(for: now)
        let monthStart = Dates.startOfMonth(now)
        switch preset {
        case .thisMonth:
            return DateRange(preset: preset, from: monthStart, to: today)
        case .lastMonth:
            let start = Dates.adding(.month, -1, to: monthStart)
            return DateRange(preset: preset, from: start, to: Dates.adding(.day, -1, to: monthStart))
        case .last3Months:
            return DateRange(preset: preset, from: Dates.adding(.month, -2, to: monthStart), to: today)
        case .last6Months:
            return DateRange(preset: preset, from: Dates.adding(.month, -5, to: monthStart), to: today)
        case .thisYear:
            let start = cal.date(from: cal.dateComponents([.year], from: now)) ?? monthStart
            return DateRange(preset: preset, from: start, to: today)
        case .allTime:
            return DateRange(preset: preset, from: Dates.adding(.month, -35, to: monthStart), to: today)
        case .custom:
            return DateRange(preset: .custom, from: Dates.adding(.day, -29, to: today), to: today)
        }
    }

    static func custom(from: Date, to: Date) -> DateRange {
        let a = Dates.startOfDay(min(from, to))
        let b = Dates.startOfDay(max(from, to))
        return DateRange(preset: .custom, from: a, to: b)
    }

    static var defaultAnalytics: DateRange { .preset(.last6Months) }

    var days: Int {
        (Dates.calendar.dateComponents([.day], from: from, to: to).day ?? 0) + 1
    }

    /// Month bars for long ranges, weeks for a month or two, days for short ranges.
    var grouping: SpendGrouping {
        if let bars { return bars }
        if days > 70 { return .month }
        if days > 21 { return .week }
        return .day
    }

    /// Pill title: "Last 6 months" or "3 Aug – 9 Oct".
    var title: String {
        preset == .custom ? short : preset.title
    }

    /// "May – October 2026"
    var longLabel: String {
        let cal = Dates.calendar
        let sameYear = cal.component(.year, from: from) == cal.component(.year, from: to)
        let sameMonth = sameYear && cal.component(.month, from: from) == cal.component(.month, from: to)
        if sameMonth { return Dates.monthYear(from) }
        if grouping == .month {
            return sameYear
                ? "\(Dates.month(from)) – \(Dates.monthYear(to))"
                : "\(Dates.monthYear(from)) – \(Dates.monthYear(to))"
        }
        return "\(Dates.shortDay(from)) – \(Dates.shortDay(to))"
    }

    /// "May – Oct" / "3 Aug – 9 Oct"
    var short: String {
        if grouping == .month { return "\(Dates.monthShort(from)) – \(Dates.monthShort(to))" }
        return "\(Self.dayMonth(from)) – \(Self.dayMonth(to))"
    }

    /// Number of months touched (for "a month" averages).
    var monthCount: Int {
        let cal = Dates.calendar
        let a = cal.dateComponents([.year, .month], from: from)
        let b = cal.dateComponents([.year, .month], from: to)
        return max(1, ((b.year ?? 0) - (a.year ?? 0)) * 12 + (b.month ?? 0) - (a.month ?? 0) + 1)
    }

    private static func dayMonth(_ date: Date) -> String {
        let d = Dates.calendar.component(.day, from: date)
        return "\(d) \(Dates.monthShort(date))"
    }
}
