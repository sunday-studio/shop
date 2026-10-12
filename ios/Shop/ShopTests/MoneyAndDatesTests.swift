import XCTest
@testable import Shop

@MainActor
final class MoneyTests: XCTestCase {
    func testFormatsDutchStyle() {
        XCTAssertEqual(Money.format(3812), "€38,12")
        XCTAssertEqual(Money.format(261490), "€2.614,90")
        XCTAssertEqual(Money.format(5), "€0,05")
        XCTAssertEqual(Money.format(-45), "\u{2212}€0,45")
    }

    func testPlainSignedWholeAndPerUnit() {
        XCTAssertEqual(Money.plain(179), "1,79")
        XCTAssertEqual(Money.plain(-45), "\u{2212}0,45")
        XCTAssertEqual(Money.signed(120), "+€1,20")
        XCTAssertEqual(Money.signed(-45), "\u{2212}€0,45")
        XCTAssertEqual(Money.whole(7849), "€78")
        XCTAssertEqual(Money.whole(7850), "€79")
        XCTAssertEqual(Money.perUnit(85, unit: .l), "€0,85/l")
        XCTAssertEqual(Money.perUnit(1149, unit: .kg), "€11,49/kg")
    }

    func testParseAcceptsEverydayInput() {
        XCTAssertEqual(Money.parse("1,29"), 129)
        XCTAssertEqual(Money.parse("€ 1.29"), 129)
        XCTAssertEqual(Money.parse("1.234,56"), 123456)
        XCTAssertEqual(Money.parse("-0,45"), -45)
        XCTAssertEqual(Money.parse("0,45-"), -45)
        XCTAssertEqual(Money.parse("\u{2212}0,45"), -45)
        XCTAssertEqual(Money.parse("12"), 1200)
        XCTAssertEqual(Money.parse("0,5"), 50)
    }

    func testParseRejectsGarbage() {
        XCTAssertNil(Money.parse(""))
        XCTAssertNil(Money.parse("abc"))
        XCTAssertNil(Money.parse("1,2x"))
    }

    func testRoundTripsThroughEditableText() {
        for cents: Int64 in [0, 5, 99, 100, 12345, 261490] {
            XCTAssertEqual(Money.parse(Money.editable(cents)), cents)
        }
    }

    func testHeroPartsSplitsAmount() {
        let parts = Money.heroParts(41238)
        XCTAssertEqual(parts.symbol, "€")
        XCTAssertEqual(parts.whole, "412")
        XCTAssertEqual(parts.fraction, ",38")
    }
}

@MainActor
final class DatesTests: XCTestCase {
    func testFastRFC3339MatchesTheSystemFormatter() {
        let samples = ["2026-10-11T03:12:09Z", "2026-10-11T03:12:09.123Z", "2026-10-11T05:12:09+02:00", "2024-02-29T23:59:59.5-05:30", "1999-12-31T00:00:00Z", "2000-03-01T00:00:00Z"]
        let fractional = ISO8601DateFormatter(); fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter(); plain.formatOptions = [.withInternetDateTime]
        for sample in samples {
            let expected = fractional.date(from: sample) ?? plain.date(from: sample)
            XCTAssertEqual(Dates.fastRFC3339(sample), expected, sample)
            XCTAssertEqual(Dates.parseRFC3339(sample), expected, sample)
        }
    }

    func testFastParserLeavesOddInputToTheFormatter() {
        XCTAssertNil(Dates.fastRFC3339("2026-10-11"))
        XCTAssertNil(Dates.fastRFC3339("not a date"))
        XCTAssertNil(Dates.fastRFC3339("2026-13-11T03:12:09Z"))
        XCTAssertNil(Dates.fastRFC3339("2026-10-11T03:12:09Zjunk"))
    }

    func testRFC3339RoundTrip() {
        let date = Date(timeIntervalSince1970: 1_791_688_329)
        XCTAssertEqual(Dates.parseRFC3339(Dates.rfc3339(date)), date)
    }

    func testIsoDayRoundTrip() {
        let day = Dates.parseIsoDay("2026-10-11")
        XCTAssertNotNil(day)
        XCTAssertEqual(Dates.isoDay(day!), "2026-10-11")
    }

    func testDayLabels() {
        let now = Dates.parseRFC3339("2026-10-11T12:00:00Z")!
        XCTAssertEqual(Dates.dayLabel(now, now: now), "Today")
        XCTAssertEqual(Dates.dayLabel(now.addingTimeInterval(-86_400), now: now), "Yesterday")
    }

    func testStartOfMonth() {
        let date = Dates.parseRFC3339("2026-10-11T12:00:00Z")!
        XCTAssertEqual(Dates.isoDay(Dates.startOfMonth(date)), "2026-10-01")
    }
}
