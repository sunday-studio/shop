import Foundation

/// Dates are stored UTC and shown in Europe/Amsterdam (PLAN §3). Copy is English with
/// Dutch number formatting, matching the wireframes ("Thursday 8 Oct", "18:42").
nonisolated enum Dates {
    static let timeZone = TimeZone(identifier: "Europe/Amsterdam") ?? .current

    static let calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = timeZone
        c.locale = Locale(identifier: "en_GB")
        c.firstWeekday = 2
        return c
    }()

    nonisolated private static func formatter(_ format: String) -> DateFormatter {
        let f = DateFormatter()
        f.calendar = calendar
        f.timeZone = timeZone
        f.locale = Locale(identifier: "en_GB")
        f.dateFormat = format
        return f
    }

    nonisolated(unsafe) private static let timeF = formatter("HH:mm")
    nonisolated(unsafe) private static let dayLongF = formatter("EEEE d MMM")
    nonisolated(unsafe) private static let dayShortF = formatter("EEE d MMM")
    nonisolated(unsafe) private static let dayYearF = formatter("d MMM yyyy")
    nonisolated(unsafe) private static let monthF = formatter("MMMM")
    nonisolated(unsafe) private static let monthShortF = formatter("MMM")
    nonisolated(unsafe) private static let monthYearF = formatter("MMMM yyyy")
    nonisolated(unsafe) private static let isoDayF = formatter("yyyy-MM-dd")

    /// "18:42"
    static func time(_ date: Date) -> String { timeF.string(from: date) }

    /// "Today", "Yesterday", "Thursday 8 Oct", older than this year: "8 Oct 2025".
    static func dayLabel(_ date: Date, now: Date = .now) -> String {
        if calendar.isDate(date, inSameDayAs: now) { return "Today" }
        if let y = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: y) { return "Yesterday" }
        if calendar.component(.year, from: date) != calendar.component(.year, from: now) { return dayYearF.string(from: date) }
        return dayLongF.string(from: date)
    }

    /// "Sat 10 Oct"
    static func shortDay(_ date: Date) -> String { dayShortF.string(from: date) }
    /// "8 Oct 2025"
    static func dayWithYear(_ date: Date) -> String { dayYearF.string(from: date) }
    /// "October"
    static func month(_ date: Date) -> String { monthF.string(from: date) }
    /// "Oct"
    static func monthShort(_ date: Date) -> String { monthShortF.string(from: date) }
    /// "October 2026"
    static func monthYear(_ date: Date) -> String { monthYearF.string(from: date) }
    /// "2026-10-10" (local), for API `from`/`to` query params.
    static func isoDay(_ date: Date) -> String { isoDayF.string(from: date) }
    static func parseIsoDay(_ s: String) -> Date? { isoDayF.date(from: s) }

    /// "today 18:42", "yesterday 17:20", "Thu 8 Oct 18:10"
    static func relativeStamp(_ date: Date, now: Date = .now) -> String {
        let day = dayLabel(date, now: now)
        if day == "Today" || day == "Yesterday" { return day.lowercased() + " " + time(date) }
        return shortDay(date) + " " + time(date)
    }

    /// "2 min ago", "3 h ago", "2 days ago"
    static func ago(_ date: Date, now: Date = .now) -> String {
        let s = max(0, now.timeIntervalSince(date))
        if s < 60 { return "just now" }
        if s < 3600 { return "\(Int(s / 60)) min ago" }
        if s < 86400 { return "\(Int(s / 3600)) h ago" }
        let d = Int(s / 86400)
        return d == 1 ? "1 day ago" : "\(d) days ago"
    }

    static func startOfDay(_ date: Date) -> Date { calendar.startOfDay(for: date) }

    static func startOfMonth(_ date: Date) -> Date {
        calendar.date(from: calendar.dateComponents([.year, .month], from: date)) ?? date
    }

    static func adding(_ component: Calendar.Component, _ value: Int, to date: Date) -> Date {
        calendar.date(byAdding: component, value: value, to: date) ?? date
    }

    // MARK: RFC3339

    nonisolated(unsafe) private static let isoFractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()

    nonisolated(unsafe) private static let isoPlain: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    static func parseRFC3339(_ s: String) -> Date? {
        fastRFC3339(s) ?? isoFractional.date(from: s) ?? isoPlain.date(from: s)
    }

    /// "2026-10-11T03:12:09Z", with optional fraction and "+02:00" offsets. About 100x faster than
    /// ISO8601DateFormatter, which matters when decoding a few thousand cached products at launch.
    /// Returns nil for anything unusual so the formatter can have a go.
    static func fastRFC3339(_ s: String) -> Date? {
        var s = s
        return s.withUTF8 { b -> Date? in
            let n = b.count
            guard n >= 20, b[4] == 45, b[7] == 45, b[10] == 84 || b[10] == 32, b[13] == 58, b[16] == 58 else { return nil }
            func num(_ from: Int, _ len: Int) -> Int? {
                var v = 0
                for i in from..<(from + len) {
                    let c = Int(b[i]) - 48
                    guard c >= 0, c <= 9 else { return nil }
                    v = v * 10 + c
                }
                return v
            }
            guard let y = num(0, 4), let mo = num(5, 2), let d = num(8, 2), let h = num(11, 2), let mi = num(14, 2), let sec = num(17, 2),
                  (1...12).contains(mo), (1...31).contains(d), h < 24, mi < 60, sec < 61 else { return nil }
            var i = 19
            var fraction = 0.0
            if i < n, b[i] == 46 {
                i += 1
                var scale = 0.1
                while i < n, b[i] >= 48, b[i] <= 57 {
                    fraction += Double(b[i] - 48) * scale
                    scale /= 10
                    i += 1
                }
            }
            var offset = 0
            if i < n, b[i] == 90 || b[i] == 122 {
                i += 1
            } else if i + 6 <= n, b[i] == 43 || b[i] == 45, b[i + 3] == 58, let oh = num(i + 1, 2), let om = num(i + 4, 2) {
                offset = (oh * 3600 + om * 60) * (b[i] == 45 ? -1 : 1)
                i += 6
            } else {
                return nil
            }
            guard i == n else { return nil }
            // Days from 1970-01-01 (proleptic Gregorian civil date).
            let yy = mo <= 2 ? y - 1 : y
            let era = (yy >= 0 ? yy : yy - 399) / 400
            let yoe = yy - era * 400
            let doy = (153 * (mo + (mo > 2 ? -3 : 9)) + 2) / 5 + d - 1
            let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
            let days = era * 146097 + doe - 719468
            let seconds = Double(days * 86400 + h * 3600 + mi * 60 + sec - offset) + fraction
            return Date(timeIntervalSince1970: seconds)
        }
    }

    static func rfc3339(_ date: Date) -> String {
        isoPlain.string(from: date)
    }
}
