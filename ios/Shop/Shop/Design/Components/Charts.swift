import Charts
import SwiftUI

// MARK: - Spend bars (A, A.2)

struct SpendBar: Identifiable {
    struct Segment: Identifiable {
        let id: String
        let cents: Int64
        let color: Color
    }

    let id: String
    let label: String
    let segments: [Segment]
    var isCurrent: Bool = false

    var total: Int64 { segments.reduce(0) { $0 + max(0, $1.cents) } }

    /// Single-colour bar.
    init(id: String, label: String, cents: Int64, color: Color, isCurrent: Bool = false) {
        self.id = id
        self.label = label
        self.segments = [Segment(id: "all", cents: cents, color: color)]
        self.isCurrent = isCurrent
    }

    init(id: String, label: String, segments: [Segment], isCurrent: Bool = false) {
        self.id = id
        self.label = label
        self.segments = segments
        self.isCurrent = isCurrent
    }
}

/// Monthly stacked bars with dashed gridlines and euro labels on the right (A).
struct SpendBars: View {
    let bars: [SpendBar]
    var plotHeight: CGFloat = 120
    var onSelect: ((SpendBar) -> Void)? = nil

    var body: some View {
        let top = Self.niceCeiling(bars.map(\.total).max() ?? 0)
        VStack(spacing: Space.ml) {
            ZStack(alignment: .topLeading) {
                gridlines(top: top)
                HStack(alignment: .bottom, spacing: Space.ml) {
                    ForEach(bars) { bar in
                        barView(bar, top: top)
                            .onTapGesture {
                                guard let onSelect else { return }
                                Haptics.play(.selection)
                                onSelect(bar)
                            }
                    }
                }
                .frame(height: plotHeight, alignment: .bottom)
                .padding(.trailing, 44)
            }
            .frame(height: plotHeight)

            HStack(spacing: Space.ml) {
                ForEach(bars) { bar in
                    TypeStyle.captionMedium.sized(11.5).text(bar.label)
                        .foregroundStyle(bar.isCurrent ? Palette.ink : Palette.ink3)
                        .frame(maxWidth: .infinity)
                }
            }
            .padding(.trailing, 44)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(bars.map { "\($0.label) \(Money.whole($0.total))" }.joined(separator: ", "))
    }

    private func barView(_ bar: SpendBar, top: Int64) -> some View {
        let h = top > 0 ? plotHeight * CGFloat(bar.total) / CGFloat(top) : 0
        return VStack(spacing: 2) {
            ForEach(bar.segments.reversed()) { seg in
                Rectangle()
                    .fill(seg.color)
                    .frame(height: bar.total > 0 ? max(0, (h - CGFloat(bar.segments.count - 1) * 2) * CGFloat(max(0, seg.cents)) / CGFloat(bar.total)) : 0)
            }
        }
        .frame(maxWidth: .infinity)
        .frame(height: max(h, 2), alignment: .bottom)
        .clipShape(RoundedRectangle.continuous(Radius.s))
        .contentShape(Rectangle())
    }

    private func gridlines(top: Int64) -> some View {
        let half = top / 2
        return ZStack(alignment: .topLeading) {
            gridLine(y: 0, label: Money.whole(top), dashed: true)
            gridLine(y: plotHeight / 2, label: Money.whole(half), dashed: true)
            gridLine(y: plotHeight, label: "€0", dashed: false)
        }
        .frame(height: plotHeight, alignment: .top)
    }

    private func gridLine(y: CGFloat, label: String, dashed: Bool) -> some View {
        HStack(spacing: 6) {
            Line()
                .stroke(Palette.line, style: StrokeStyle(lineWidth: 1, dash: dashed ? [3, 3] : []))
                .frame(height: 1)
            TypeStyle.micro.text(label)
                .foregroundStyle(Palette.ink3)
                .frame(width: 38, alignment: .trailing)
        }
        .offset(y: y - 0.5)
        .frame(maxHeight: .infinity, alignment: .top)
    }

    /// Rounds up to a "nice" ceiling for axis labels: €500, €250, €2.000…
    static func niceCeiling(_ cents: Int64) -> Int64 {
        guard cents > 0 else { return 10_000 }
        let euros = Double(cents) / 100
        let magnitude = pow(10, floor(log10(euros)))
        let steps: [Double] = [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]
        let step = steps.first { $0 * magnitude >= euros } ?? 10
        return Int64(step * magnitude * 100)
    }
}

/// A horizontal line shape (dashable).
nonisolated struct Line: Shape {
    func path(in rect: CGRect) -> Path {
        var p = Path()
        p.move(to: CGPoint(x: rect.minX, y: rect.midY))
        p.addLine(to: CGPoint(x: rect.maxX, y: rect.midY))
        return p
    }
}

// MARK: - Mini bars (white bars on a coloured card, A.1)

struct MiniBars: View {
    let values: [(label: String, cents: Int64)]
    var color: Color = Accent.white
    var height: CGFloat = 40

    var body: some View {
        let top = max(1, values.map { $0.cents }.max() ?? 1)
        HStack(alignment: .bottom, spacing: Space.m) {
            ForEach(Array(values.enumerated()), id: \.offset) { index, value in
                VStack(spacing: Space.xs) {
                    RoundedRectangle.continuous(6)
                        .fill(color.opacity(index == values.count - 1 ? 1 : 0.4))
                        .frame(height: max(3, height * CGFloat(value.cents) / CGFloat(top)))
                    TypeStyle.micro.text(value.label)
                        .opacity(0.8)
                }
                .frame(maxWidth: .infinity)
            }
        }
        .frame(height: height + 18, alignment: .bottom)
        .accessibilityHidden(true)
    }
}

// MARK: - Price chart (P.1)

struct PriceSeries: Identifiable {
    struct Point: Identifiable {
        let id: Int64
        let date: Date
        let unitCents: Int64
    }

    let id: Int64
    let shopName: String
    let color: Color
    let points: [Point]
    var isCheapest: Bool = false
}

/// Price per base unit over time, one line per shop, last point dotted.
struct PriceChart: View {
    let series: [PriceSeries]
    var height: CGFloat = 150

    var body: some View {
        Chart {
            ForEach(series) { s in
                ForEach(s.points) { p in
                    LineMark(
                        x: .value("Date", p.date),
                        y: .value("Price", Double(p.unitCents)),
                        series: .value("Shop", s.shopName)
                    )
                    .interpolationMethod(.stepEnd)
                    .foregroundStyle(s.color)
                    .lineStyle(StrokeStyle(lineWidth: s.isCheapest ? 3 : 2.6, lineCap: .round, lineJoin: .round))
                }
                if let last = s.points.last {
                    PointMark(x: .value("Date", last.date), y: .value("Price", Double(last.unitCents)))
                        .foregroundStyle(s.color)
                        .symbolSize(s.isCheapest ? 90 : 70)
                }
            }
        }
        .chartLegend(.hidden)
        .chartYScale(domain: .automatic(includesZero: false))
        .chartYAxis {
            AxisMarks(position: .trailing, values: .automatic(desiredCount: 3)) { value in
                AxisGridLine(stroke: StrokeStyle(lineWidth: 1, dash: [3, 3]))
                    .foregroundStyle(Palette.line)
                AxisValueLabel {
                    if let cents = value.as(Double.self) {
                        Text(Money.plain(Int64(cents.rounded())))
                            .textStyle(.micro)
                            .foregroundStyle(Palette.ink3)
                    }
                }
            }
        }
        .chartXAxis {
            AxisMarks(values: .stride(by: .month)) { value in
                AxisValueLabel {
                    if let date = value.as(Date.self) {
                        TypeStyle.micro.text(Dates.monthShort(date))
                            .foregroundStyle(Palette.ink3)
                    }
                }
            }
        }
        .frame(height: height)
        .accessibilityLabel(accessibilitySummary)
    }

    private var accessibilitySummary: String {
        let parts = series.compactMap { s -> String? in
            guard let last = s.points.last else { return nil }
            return "\(s.shopName) \(Money.format(last.unitCents))"
        }
        return "Price per unit by shop. Latest: " + parts.joined(separator: ", ")
    }
}

#Preview("Charts") {
    let months = ["May", "Jun", "Jul", "Aug", "Sep", "Oct"]
    let totals: [Int64] = [44820, 47105, 40260, 42532, 45535, 41238]
    let shares: [(Double, Color)] = [(0.19, Accent.blue), (0.17, Accent.green), (0.16, Accent.red), (0.14, Accent.yellow), (0.10, Accent.orange), (0.24, Palette.neutral)]
    let bars = zip(months, totals).enumerated().map { i, pair in
        SpendBar(id: pair.0, label: pair.0, segments: shares.enumerated().map { j, s in
            .init(id: "\(j)", cents: Int64(Double(pair.1) * s.0), color: s.1)
        }, isCurrent: i == months.count - 1)
    }
    let now = Date()
    func series(_ id: Int64, _ name: String, _ color: Color, _ cents: [Int64], cheapest: Bool = false) -> PriceSeries {
        PriceSeries(id: id, shopName: name, color: color, points: cents.enumerated().map { i, c in
            .init(id: Int64(i), date: Dates.adding(.month, i - cents.count + 1, to: now), unitCents: c)
        }, isCheapest: cheapest)
    }
    return VStack(spacing: 30) {
        SpendBars(bars: bars)
        PriceChart(series: [
            series(1, "Jumbo", Accent.orange, [95, 95, 93, 95, 93, 93]),
            series(2, "Albert Heijn", Accent.blue, [92, 92, 89, 92, 89, 89]),
            series(3, "Lidl", Color(hex: 0xE5B000), [87, 85, 85, 87, 85, 85], cheapest: true),
        ])
        MiniBars(values: [("May", 19010), ("Jun", 20540), ("Jul", 16085), ("Aug", 17825), ("Sep", 21460), ("Oct", 15510)])
            .foregroundStyle(.white)
            .padding()
            .background(Accent.blue, in: RoundedRectangle.continuous(28))
    }
    .padding(20)
    .background(Palette.bg)
}
