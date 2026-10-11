import SwiftUI

/// A · Analytics: spend over a range, stacked bars, breakdown by category or shop, price changes.
@MainActor
@Observable
final class AnalyticsModel {
    enum Breakdown: String, CaseIterable, Hashable {
        case category = "By category"
        case shop = "By shop"
    }

    private(set) var range: DateRange
    var breakdown: Breakdown = .category
    private(set) var byTime: Spend?
    private(set) var byCategory: Spend?
    private(set) var byShop: Spend?
    private(set) var priceChanges: [PriceChange] = []
    private(set) var error: APIError?
    private(set) var isLoading = false

    @ObservationIgnored private let app: AppModel
    @ObservationIgnored private static let rangeKey = "analytics.range"

    init(app: AppModel) {
        self.app = app
        let stored = UserDefaults.standard.string(forKey: Self.rangeKey).flatMap(DateRange.Preset.init(rawValue:))
        range = .preset(stored.flatMap { $0 == .custom ? nil : $0 } ?? .last6Months)
    }

    func setRange(_ new: DateRange) {
        range = new
        if new.preset != .custom { UserDefaults.standard.set(new.preset.rawValue, forKey: Self.rangeKey) }
        Task { await load() }
    }

    func load() async {
        isLoading = true
        defer { isLoading = false }
        let r = range
        do {
            async let t = app.api.spend(from: r.from, to: r.to, groupBy: r.grouping, splitBy: .category)
            async let c = app.api.spend(from: r.from, to: r.to, groupBy: .category)
            async let s = app.api.spend(from: r.from, to: r.to, groupBy: .shop)
            let (time, cat, shop) = try await (t, c, s)
            guard r == range else { return }
            byTime = time
            byCategory = cat
            byShop = shop
            error = nil
        } catch let e as APIError {
            error = e
        } catch {}
        priceChanges = (try? await app.api.priceChanges(since: Dates.adding(.day, -90, to: .now))) ?? priceChanges
    }

    /// "2026-W40" → Monday of that ISO week.
    static func weekStart(_ key: String) -> Date? {
        let parts = key.split(separator: "-")
        guard parts.count == 2, let year = Int(parts[0]), parts[1].hasPrefix("W"), let week = Int(parts[1].dropFirst()) else { return nil }
        var c = DateComponents()
        c.yearForWeekOfYear = year
        c.weekOfYear = week
        c.weekday = 2
        var cal = Calendar(identifier: .iso8601)
        cal.timeZone = Dates.timeZone
        return cal.date(from: c)
    }

    // MARK: Presentation

    /// The five biggest categories get the palette; everything else is grey.
    var categoryColors: [String: Color] {
        let palette = [Accent.blue, Accent.green, Accent.red, Accent.yellow, Accent.orange]
        let top = (byCategory?.buckets ?? []).filter { $0.key != "none" && $0.cents > 0 }.sorted { $0.cents > $1.cents }.prefix(5)
        var map: [String: Color] = [:]
        for (i, b) in top.enumerated() { map[b.key] = palette[i] }
        return map
    }

    var bars: [SpendBar] {
        guard let time = byTime else { return [] }
        let colors = categoryColors
        let last = time.buckets.last?.key
        return time.buckets.map { b in
            let label = Self.barLabel(b, grouping: time.groupBy)
            if let split = b.split, !split.isEmpty {
                var named: [SpendBar.Segment] = []
                var rest: Int64 = 0
                for s in split {
                    if let c = colors[s.key] { named.append(.init(id: s.key, cents: s.cents, color: c)) } else { rest += s.cents }
                }
                named.sort { a, b in
                    let ranks = (byCategory?.buckets ?? []).sorted { $0.cents > $1.cents }.map(\.key)
                    return (ranks.firstIndex(of: a.id) ?? 99) < (ranks.firstIndex(of: b.id) ?? 99)
                }
                if rest > 0 { named.append(.init(id: "rest", cents: rest, color: Palette.neutral)) }
                return SpendBar(id: b.key, label: label, segments: named, isCurrent: b.key == last)
            }
            return SpendBar(id: b.key, label: label, cents: b.cents, color: b.key == last ? Palette.ink : Palette.ink3.opacity(0.5), isCurrent: b.key == last)
        }
    }

    static func barLabel(_ bucket: SpendBucket, grouping: SpendGrouping) -> String {
        switch grouping {
        case .month:
            if let d = Dates.parseIsoDay(bucket.key + (bucket.key.count == 7 ? "-01" : "")) { return Dates.monthShort(d) }
        case .week:
            if let d = weekStart(bucket.key) ?? Dates.parseIsoDay(bucket.key) { return "\(Dates.calendar.component(.day, from: d))" }
        case .day:
            if let d = Dates.parseIsoDay(bucket.key) { return "\(Dates.calendar.component(.day, from: d))" }
        default: break
        }
        return bucket.label
    }

    /// "€435,82 a month · October €42,72 under September so far"
    var caption: String {
        guard let time = byTime, time.totalCents > 0 else { return "No spending in this range yet." }
        var parts: [String] = []
        if time.groupBy == .month {
            parts.append("\(Money.format(time.totalCents / Int64(max(1, range.monthCount)))) a month")
            let nonEmpty = time.buckets
            if nonEmpty.count >= 2 {
                let cur = nonEmpty[nonEmpty.count - 1], prev = nonEmpty[nonEmpty.count - 2]
                let diff = cur.cents - prev.cents
                let curName = Self.monthName(cur), prevName = Self.monthName(prev)
                parts.append("\(curName) \(Money.format(Int64(diff.magnitude))) \(diff <= 0 ? "under" : "over") \(prevName)\(range.to >= Dates.startOfDay(.now) ? " so far" : "")")
            }
        } else {
            parts.append("\(time.receipts) receipts")
            let days = max(1, range.days)
            parts.append("\(Money.format(time.totalCents / Int64(days))) a day")
        }
        return parts.joined(separator: " · ")
    }

    private static func monthName(_ b: SpendBucket) -> String {
        Dates.parseIsoDay(b.key + (b.key.count == 7 ? "-01" : "")).map(Dates.month) ?? b.label
    }

    var priceChangeSummary: String {
        let up = priceChanges.filter { $0.pct > 0 }
        let down = priceChanges.filter { $0.pct < 0 }
        guard !priceChanges.isEmpty else { return "No big changes in the last 90 days" }
        var s = "\(up.count) up, \(down.count) down in 90 days"
        if let top = up.max(by: { $0.pct < $1.pct }) { s += " · \(top.productName) +\(Int(top.pct.rounded()))%" }
        return s
    }
}

struct AnalyticsView: View {
    @Environment(AppModel.self) private var app
    @State private var model: AnalyticsModel?

    var body: some View {
        Group {
            if let model { content(model) } else { Palette.bg }
        }
        .background(Palette.bg)
        .toolbar(.hidden, for: .navigationBar)
        .onAppear { if model == nil { model = AnalyticsModel(app: app) } }
        .task(id: model == nil) { await model?.load() }
    }

    @ViewBuilder
    private func content(_ model: AnalyticsModel) -> some View {
        @Bindable var model = model
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                LargeTitleHeader(title: "Analytics") {
                    PillMenuButton(title: model.range.title) {
                        app.trays.present("range") { RangeTray(range: model.range) { model.setRange($0) } }
                    }
                }

                if model.byTime == nil {
                    StatefulContent(phase: model.error.map { .failed($0.message) } ?? .loading, retry: { Task { await model.load() } }) {
                        VStack(alignment: .leading, spacing: Space.xl) {
                            VStack(alignment: .leading, spacing: Space.s) {
                                SkeletonBar(width: 120, height: 13)
                                SkeletonBar(width: 190, height: 44)
                            }
                            SkeletonChart()
                            SkeletonRows(count: 5) { SkeletonSimpleRow() }
                        }
                        .padding(.top, 18)
                    } empty: {
                        EmptyView()
                    } content: {
                        EmptyView()
                    }
                    .padding(.horizontal, Space.gutter)
                } else {
                    if let error = model.error {
                        StaleBanner(what: "spending", offline: error.isConnectivity) { Task { await model.load() } }
                            .padding(.horizontal, Space.edge)
                            .padding(.top, Space.l)
                    }
                    VStack(alignment: .leading, spacing: Space.xs) {
                        TypeStyle.rowTitle.sized(14).text(model.range.longLabel).foregroundStyle(Palette.ink2)
                        HeroAmount(cents: model.byTime?.totalCents ?? 0, size: 54)
                        TypeStyle.body.sized(13.5).text(model.caption).foregroundStyle(Palette.ink2)
                    }
                    .padding(.horizontal, Space.gutter)
                    .padding(.top, 18)

                    SpendBars(bars: model.bars)
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, 22)

                    SegmentedControl(options: AnalyticsModel.Breakdown.allCases, title: { $0.rawValue }, selection: $model.breakdown)
                        .padding(.horizontal, Space.edge)
                        .padding(.top, Space.lx)

                    breakdown(model)
                        .padding(.horizontal, Space.gutter)
                        .padding(.top, 6)

                }

                Button { app.push(.priceChanges) } label: {
                    HStack(spacing: Space.l) {
                        IconView(.trendUp, size: 16, weight: .bold)
                            .foregroundStyle(Palette.redText)
                            .frame(width: 32, height: 32)
                            .background(Palette.redTint, in: RoundedRectangle.continuous(10))
                        VStack(alignment: .leading, spacing: 1) {
                            TypeStyle.rowTitle.text("Price changes").foregroundStyle(Palette.ink)
                            TypeStyle.rowSub.text(model.priceChangeSummary).foregroundStyle(Palette.ink2).lineLimit(1)
                        }
                        Spacer()
                        IconView(.forward, size: 13, weight: .bold).foregroundStyle(Palette.ink3)
                    }
                    .frame(height: Size.rowReceipt)
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: nil))
                .padding(.horizontal, Space.gutter)
            }
            .padding(.bottom, Space.tabBarClearance)
        }
        .scrollIndicators(.hidden)
        .refreshable { await model.load() }
    }

    @ViewBuilder
    private func breakdown(_ model: AnalyticsModel) -> some View {
        let spend = model.breakdown == .category ? model.byCategory : model.byShop
        let total = max(1, spend?.totalCents ?? 1)
        let buckets = (spend?.buckets ?? []).filter { $0.cents > 0 }.sorted { $0.cents > $1.cents }
        let colors = model.categoryColors
        VStack(spacing: 0) {
            if buckets.isEmpty && spend != nil {
                TypeStyle.body.text("Nothing in this range.").foregroundStyle(Palette.ink2).padding(.vertical, Space.xxl)
            }
            ForEach(buckets) { b in
                let fraction = Double(b.cents) / Double(total)
                let color = model.breakdown == .category
                    ? (colors[b.key] ?? (b.key == "none" ? Palette.neutral : CategoryColor.swatch(for: Int64(b.key).flatMap { app.catalog.category($0) }).color))
                    : ShopBrand.chartColor(for: b.label)
                Button {
                    if model.breakdown == .category {
                        app.push(.categorySpend(b.label, Int64(b.key), model.range))
                    } else if let id = Int64(b.key) {
                        app.push(.shopSpend(id, model.range))
                    }
                } label: {
                    HStack(spacing: Space.l) {
                        if model.breakdown == .shop && b.key == "none" {
                            UnknownShopMark()
                        } else if model.breakdown == .shop {
                            ShopMark(name: b.label)
                        } else {
                            CategoryDot(color: color, size: 12)
                        }
                        VStack(spacing: 5) {
                            HStack {
                                TypeStyle.rowTitle.text(b.key == "none" ? (model.breakdown == .shop ? "Unassigned" : "Uncategorised") : b.label).foregroundStyle(Palette.ink)
                                Spacer()
                                TypeStyle.rowTitle.text(Money.format(b.cents)).foregroundStyle(Palette.ink)
                            }
                            HStack(spacing: Space.m) {
                                ShareBar(fraction: fraction, color: color)
                                TypeStyle.caption.sized(11.5).text("\(Int((fraction * 100).rounded()))%")
                                    .foregroundStyle(Palette.ink2)
                                    .frame(width: 32, alignment: .trailing)
                            }
                        }
                    }
                    .frame(height: 50)
                    .hairline()
                    .contentShape(Rectangle())
                }
                .buttonStyle(.press(haptic: nil))
                .disabled(b.key == "none")
            }
        }
    }
}

// MARK: - A.4 Date range

struct RangeTray: View {
    let onApply: (DateRange) -> Void
    @State private var preset: DateRange.Preset
    @State private var from: Date
    @State private var to: Date
    @State private var bars: SpendGrouping

    @Environment(TrayCoordinator.self) private var trays

    private let presets: [DateRange.Preset] = [.thisMonth, .lastMonth, .last3Months, .last6Months, .thisYear, .allTime]

    init(range: DateRange, onApply: @escaping (DateRange) -> Void) {
        self.onApply = onApply
        _preset = State(initialValue: range.preset)
        _from = State(initialValue: range.from)
        _to = State(initialValue: range.to)
        _bars = State(initialValue: range.grouping)
    }

    private var current: DateRange {
        var r = preset == .custom ? DateRange.custom(from: from, to: to) : DateRange.preset(preset)
        r.bars = bars
        return r
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: "Date range")

            // An eager Grid, not a LazyVGrid: a lazy grid reports its height late, so the tray
            // surface was measured too short and the buttons showed before the background.
            Grid(horizontalSpacing: 6, verticalSpacing: 6) {
                ForEach(Array(stride(from: 0, to: presets.count, by: 3)), id: \.self) { start in
                    GridRow {
                        ForEach(presets[start..<min(start + 3, presets.count)], id: \.self) { p in
                            Button {
                                Haptics.play(.selection)
                                preset = p
                                let r = DateRange.preset(p)
                                from = r.from
                                to = r.to
                                bars = r.grouping
                            } label: {
                                TypeStyle.label.sized(14).text(DateRange.gridTitle(p))
                                    .foregroundStyle(preset == p ? Accent.white : Palette.ink)
                                    .frame(maxWidth: .infinity)
                                    .frame(height: 46)
                                    .background(preset == p ? Accent.blue : Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
                            }
                            .buttonStyle(.press(haptic: nil))
                        }
                    }
                }
            }

            HStack(spacing: 6) {
                dateTile("From", selection: $from)
                dateTile("To", selection: $to)
            }

            HStack {
                TypeStyle.input.sized(15).text("Bars per").foregroundStyle(Palette.ink)
                Spacer()
                SegmentedControl(options: [SpendGrouping.day, .week, .month], title: { $0.rawValue.capitalized }, selection: $bars, track: Palette.trayField)
                    .frame(width: 210)
            }
            .padding(.horizontal, Space.xs)

            Button("Show \(preset == .custom ? current.title : (current.title.lowercased().hasPrefix("last ") ? String(current.title.dropFirst(5)) : current.title.lowercased()))") {
                onApply(current)
                trays.dismiss()
            }
            .buttonStyle(.shop())
        }
    }

    /// A plain tile (no system popover inside the tray): tapping swaps this tray for a calendar
    /// tray, and "Done" swaps back with the date set.
    private func dateTile(_ label: String, selection: Binding<Date>) -> some View {
        Button {
            Haptics.play(.selection)
            let isFrom = label == "From"
            let bars = bars
            let other = isFrom ? to : from
            let onApply = onApply
            trays.present("range-date") {
                RangeDateTray(title: label, date: selection.wrappedValue, limit: isFrom ? Date.distantPast...min(other, .now) : other...Date.now) { picked in
                    let range = DateRange.custom(from: isFrom ? picked : other, to: isFrom ? other : picked)
                    var r = range
                    r.bars = bars
                    trays.present("range") { RangeTray(range: r, onApply: onApply) }
                }
            }
        } label: {
            VStack(alignment: .leading, spacing: 2) {
                TypeStyle.caption.text(label).foregroundStyle(Palette.ink2)
                TypeStyle.input.sized(16).text(selection.wrappedValue.formatted(Date.FormatStyle(date: .abbreviated, time: .omitted, timeZone: Dates.timeZone)))
                    .foregroundStyle(Palette.ink)
            }
            .padding(.vertical, Space.ml)
            .padding(.horizontal, Space.lx)
            .frame(maxWidth: .infinity, minHeight: 64, alignment: .leading)
            .background(Palette.trayField, in: RoundedRectangle.continuous(Radius.l))
            .contentShape(RoundedRectangle.continuous(Radius.l))
        }
        .buttonStyle(.press(haptic: nil))
    }
}

/// Calendar for one end of the range. Replaces the range tray, then returns to it.
struct RangeDateTray: View {
    let title: String
    let limit: ClosedRange<Date>
    let onDone: (Date) -> Void
    @State private var date: Date
    @Environment(TrayCoordinator.self) private var trays

    init(title: String, date: Date, limit: ClosedRange<Date>, onDone: @escaping (Date) -> Void) {
        self.title = title
        self.limit = limit
        self.onDone = onDone
        _date = State(initialValue: min(max(date, limit.lowerBound), limit.upperBound))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Space.lx) {
            TrayHeader(title: title == "From" ? "Start date" : "End date")
            DatePicker("", selection: $date, in: limit, displayedComponents: .date)
                .datePickerStyle(.graphical)
                .labelsHidden()
                .tint(Accent.blue)
                .environment(\.timeZone, Dates.timeZone)
            Button("Done") { onDone(date) }
                .buttonStyle(.shop(haptic: .medium))
        }
    }
}

#Preview("Analytics") {
    let app = AppModel(mock: true)
    return NavigationStack { AnalyticsView() }
        .environment(app)
        .trayHost(app.trays)
}
