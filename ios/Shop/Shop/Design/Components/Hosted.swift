import AuthenticationServices
import SwiftUI

// Hosted (paid) version. Spec: notes/design-hosted.md. Copy is plain and factual; prices and dates are
// always passed in by the caller (the price comes from StoreKit, never typed here).

// MARK: - Welcome: the two doors

/// The system "Continue with Apple" button (App Review wants Apple's own button, not a lookalike).
/// Black on light, white on dark, 54 pt, fully rounded.
struct ContinueWithAppleButton: View {
    var isWorking = false
    let onRequest: (ASAuthorizationAppleIDRequest) -> Void
    let onCompletion: (Result<ASAuthorization, Error>) -> Void

    @Environment(\.colorScheme) private var scheme

    var body: some View {
        SignInWithAppleButton(.continue, onRequest: onRequest, onCompletion: onCompletion)
            .signInWithAppleButtonStyle(scheme == .dark ? .white : .black)
            .frame(height: Size.buttonLarge)
            .clipShape(Capsule())
            .opacity(isWorking ? 0.45 : 1)
            .overlay { if isWorking { ProgressView().tint(scheme == .dark ? Palette.btnInk : Accent.white) } }
            .allowsHitTesting(!isWorking)
            .animation(Motion.fade, value: isWorking)
            .accessibilityLabel(isWorking ? "Signing in" : "Continue with Apple")
    }
}

/// "I have my own server": a clear secondary door, never hidden. Two lines and a chevron, on a `sunk` card.
struct SecondaryDoorRow: View {
    var title = "I have my own server"
    var subtitle = "Free, with no limits. Your server, your data."
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Space.l) {
                IconView(.server, size: 18, weight: .semibold)
                    .frame(width: 36, height: 36)
                    .background(Palette.bg, in: RoundedRectangle.continuous(Radius.tile))
                VStack(alignment: .leading, spacing: 1) {
                    TypeStyle.rowTitle.sized(15.5).text(title).foregroundStyle(Palette.ink)
                    TypeStyle.rowSub.text(subtitle).foregroundStyle(Palette.ink2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                IconView(.forward, size: 14, weight: .bold).foregroundStyle(Palette.ink3)
            }
            .padding(.horizontal, Space.lx)
            .padding(.vertical, Space.l)
            .frame(minHeight: 64)
            .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.l))
            .contentShape(RoundedRectangle.continuous(Radius.l))
        }
        .buttonStyle(.press(haptic: .light))
    }
}

// MARK: - Trial and paywall

/// The plan on offer. `price` is the StoreKit display price ("€4,99"); nil while it loads (a skeleton shows).
/// `trialDays` is nil when the Apple ID is not eligible for the trial any more.
struct PlanOfferCard: View {
    var price: String?
    var period = "a month"
    var trialDays: Int? = 14
    var loadFailed = false
    var retry: (() -> Void)? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            if let trialDays {
                TypeStyle.label.text("\(trialDays) days free")
                    .foregroundStyle(Palette.greenText)
                    .padding(.horizontal, 8)
                    .frame(height: Size.chipSmall)
                    .background(Palette.greenTint, in: RoundedRectangle.continuous(Radius.s))
            }
            if let price {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    TypeStyle.hero.sized(44).text(price).foregroundStyle(Palette.ink)
                    TypeStyle.body.sized(16).text(period).foregroundStyle(Palette.ink2)
                }
                .accessibilityElement(children: .combine)
            } else if loadFailed {
                VStack(alignment: .leading, spacing: Space.m) {
                    TypeStyle.body.text("Couldn't load the price.").foregroundStyle(Palette.ink2)
                    if let retry {
                        Button("Try again", action: retry).buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
                    }
                }
            } else {
                SkeletonBlock(width: 160, height: 44, radius: 10)
                    .accessibilityLabel("Loading the price")
            }
            if let trialDays, price != nil {
                TypeStyle.body.sized(14).text("Free for \(trialDays) days, then \(price ?? "") \(period).")
                    .foregroundStyle(Palette.ink2)
            }
        }
        .padding(Space.xxl)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.feature))
    }
}

/// "✓ Share receipts and the list with up to 4 people": one plain fact per line.
struct CheckLine: View {
    let text: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: Space.l) {
            IconView(.check, size: 13, weight: .heavy)
                .foregroundStyle(Accent.white)
                .frame(width: 22, height: 22)
                .background(Accent.green, in: Circle())
                .alignmentGuide(.firstTextBaseline) { $0[.bottom] - 4 }
            TypeStyle.input.sized(15.5).text(text).foregroundStyle(Palette.ink)
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Small print under a purchase button (renewal terms): 11.5 pt ink2, left aligned, wraps.
struct FinePrint: View {
    let text: String

    var body: some View {
        TypeStyle.caption.sized(11.5).text(text)
            .foregroundStyle(Palette.ink2)
            .lineSpacing(2)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// "Restore purchases · Terms of Use · Privacy Policy": the links App Review looks for. 44 pt targets.
struct PaywallLinks: View {
    var showsRestore = true
    let onRestore: () -> Void
    let onTerms: () -> Void
    let onPrivacy: () -> Void

    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        // Side by side normally; stacked from the accessibility sizes so no label wraps mid-word.
        let layout = typeSize.isAccessibilitySize ? AnyLayout(VStackLayout(spacing: 0)) : AnyLayout(HStackLayout(spacing: 0))
        layout {
            if showsRestore { link("Restore purchases", onRestore) }
            link("Terms of Use", onTerms)
            link("Privacy Policy", onPrivacy)
        }
    }

    private func link(_ title: String, _ run: @escaping () -> Void) -> some View {
        Button(action: run) {
            TypeStyle.label.sized(12.5).text(title)
                .foregroundStyle(Palette.blueText)
                .multilineTextAlignment(.center)
                .frame(maxWidth: .infinity, minHeight: 44)
                .contentShape(Rectangle())
        }
        .buttonStyle(.press(haptic: .light))
    }
}

// MARK: - Plan status

/// Where the plan stands. UI-only: the app maps the server's account/plan fields to this.
enum HostedPlanStatus: Equatable {
    case trial(endsOn: String)
    case active(renewsOn: String)
    case cancelled(endsOn: String)
    case freeAccess(until: String)
    case grace(daysLeft: Int)
    case billingRetry
    case lapsed(daysLeft: Int)
    case pendingDeletion(daysLeft: Int)
    /// The owner asked to delete the account: 7 day cooling-off.
    case deletionRequested(on: String)

    var tone: Tone {
        switch self {
        case .trial, .active, .freeAccess: .green
        case .cancelled: .amber
        case .grace, .billingRetry: .amber
        case .lapsed, .pendingDeletion, .deletionRequested: .red
        }
    }

    var chip: String {
        switch self {
        case .trial: "Free trial"
        case .active: "Active"
        case .cancelled: "Ends soon"
        case .freeAccess: "Free access"
        case .grace: "Payment issue"
        case .billingRetry: "Payment issue"
        case .lapsed: "Read-only"
        case .pendingDeletion: "Deleting soon"
        case .deletionRequested: "Deletion scheduled"
        }
    }

    var headline: String {
        switch self {
        case .trial(let d): "Free trial until \(d)"
        case .active(let d): "Renews on \(d)"
        case .cancelled(let d): "Ends on \(d)"
        case .freeAccess(let d): "Free access until \(d)"
        case .grace(let n): "Payment didn't go through. \(Self.days(n)) left to fix it."
        case .billingRetry: "Apple is retrying your payment"
        case .lapsed(let n): "Read-only. \(Self.days(n)) left to export or resubscribe."
        case .pendingDeletion(let n): "Your data will be deleted in \(Self.days(n))"
        case .deletionRequested(let d): "Deletion scheduled for \(d)"
        }
    }

    static func days(_ n: Int) -> String { n == 1 ? "1 day" : "\(n) days" }
}

/// Account screen header card: plan name, a status chip, one honest sentence.
struct PlanStatusCard: View {
    let status: HostedPlanStatus
    var planName = "Shop"
    var detail: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            HStack {
                TypeStyle.rowTitle.sized(17).text(planName).foregroundStyle(Palette.ink)
                Spacer()
                TypeStyle.captionMedium.text(status.chip)
                    .foregroundStyle(status.tone.text)
                    .padding(.horizontal, 8)
                    .frame(height: Size.chipSmall)
                    .background(status.tone.tint, in: RoundedRectangle.continuous(Radius.s))
            }
            TypeStyle.body.sized(15).text(status.headline)
                .foregroundStyle(Palette.ink)
                .fixedSize(horizontal: false, vertical: true)
            if let detail {
                TypeStyle.rowSub.text(detail).foregroundStyle(Palette.ink2)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(Space.xxl)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.feature))
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Notices (banners)

/// Something about the plan or limits that deserves a banner on the main screens. One component, one
/// copy table (see the state table in notes/design-hosted.md).
enum HostedNotice: Equatable {
    case trialEnding(daysLeft: Int)
    case freeAccessEnding(daysLeft: Int)
    case grace(daysLeft: Int)
    case billingRetry
    case lapsedReadOnly(daysLeft: Int)
    case pendingDeletion(daysLeft: Int)
    /// Deletion the owner asked for; owners can cancel, members cannot.
    case deletionRequested(on: String)
    case queueFull(limit: Int)
    case removedFromHousehold
    case receiptLimit(used: Int, limit: Int, resetsOn: String)
    case storageNearFull(percent: Int)
    case storageFull
    case deleted

    var tone: Tone {
        switch self {
        case .trialEnding, .freeAccessEnding, .grace, .billingRetry, .storageNearFull: .amber
        case .lapsedReadOnly, .pendingDeletion, .deletionRequested, .receiptLimit, .storageFull: .red
        case .queueFull: .amber
        case .deleted, .removedFromHousehold: .neutral
        }
    }

    var lead: String {
        switch self {
        case .trialEnding(let n): n == 1 ? "Your free trial ends tomorrow." : "Your free trial ends in \(n) days."
        case .freeAccessEnding(let n): "Your free access ends in \(HostedPlanStatus.days(n))."
        case .grace(let n): "Your payment didn't go through."
        case .billingRetry: "Apple couldn't charge your payment method."
        case .lapsedReadOnly: "Shop is read-only."
        case .pendingDeletion(let n): "Your data will be deleted in \(HostedPlanStatus.days(n))."
        case .deletionRequested(let d): "Deletion is scheduled for \(d)."
        case .queueFull: "Too many receipts are waiting."
        case .removedFromHousehold: "You were removed from the household."
        case .receiptLimit: "You've reached this month's receipt limit."
        case .storageNearFull(let p): "Storage is \(p)% full."
        case .storageFull: "Storage is full."
        case .deleted: "This account was deleted."
        }
    }

    var message: String { message(isOwner: true) }

    func message(isOwner: Bool) -> String {
        switch self {
        case .trialEnding: "Subscribe to keep adding receipts."
        case .freeAccessEnding: "Subscribe to keep adding receipts after that."
        case .grace(let n): "You have \(HostedPlanStatus.days(n)) to update it. Everything still works."
        case .billingRetry: "Apple will try again. Update your payment method to avoid a pause."
        case .lapsedReadOnly(let n): isOwner
            ? "You can look at everything and export your data. \(HostedPlanStatus.days(n)) left before it's deleted."
            : "You can look at everything and export your data. Ask the owner to renew. \(HostedPlanStatus.days(n)) left before it's deleted."
        case .pendingDeletion: isOwner ? "Export your data now, or resubscribe to keep it." : "Export your data now. Ask the owner to renew to keep it."
        case .deletionRequested: isOwner ? "Export your data now. You can cancel until then." : "The owner asked to delete this household. Export your data now."
        case .queueFull(let n): "\(n) are waiting. New ones can be added once some are read."
        case .removedFromHousehold: "Subscribe to keep using Shop, or ask for a new invite."
        case .receiptLimit(let used, let limit, let on): "\(used) of \(limit) used. You can add more from \(on)."
        case .storageNearFull: "Delete old receipt photos or export your data to free space."
        case .storageFull: "New photos can't be saved until you free some space."
        case .deleted: "Your receipts and list were removed. You can start again with a new account."
        }
    }

    /// Short label for the action button, nil when the banner has none.
    var actionTitle: String? {
        switch self {
        case .trialEnding, .freeAccessEnding, .removedFromHousehold: "Subscribe"
        case .grace, .billingRetry: "Fix payment"
        case .lapsedReadOnly: "Resubscribe"
        case .pendingDeletion: "Export or resubscribe"
        case .deletionRequested: "Cancel deletion"
        case .storageNearFull, .storageFull: "Manage storage"
        case .receiptLimit, .queueFull, .deleted: nil
        }
    }

    var glyph: GlyphBadge.Glyph { (self == .deleted || self == .removedFromHousehold) ? .info : .alert }
}

struct HostedNoticeBanner: View {
    let notice: HostedNotice
    /// Members never get purchase or payment actions: only the owner's wording and buttons.
    var isOwner = true
    var action: (() -> Void)? = nil

    var body: some View {
        Banner(
            tone: notice.tone,
            glyph: notice.glyph,
            lead: notice.lead,
            message: notice.message(isOwner: isOwner),
            action: isOwner ? notice.actionTitle.flatMap { title in action.map { (title: title, run: $0) } } : nil
        )
    }
}

// MARK: - Read-only: one lock for every write control

private struct WritesLocked: ViewModifier {
    let locked: Bool
    let onBlocked: () -> Void

    func body(content: Content) -> some View {
        content
            .opacity(locked ? 0.45 : 1)
            .allowsHitTesting(!locked)
            .overlay {
                if locked {
                    Color.clear.contentShape(Rectangle())
                        .onTapGesture {
                            Haptics.play(.warning)
                            onBlocked()
                        }
                }
            }
            .accessibilityHint(locked ? "Shop is read-only" : "")
            .animation(Motion.fade, value: locked)
    }
}

extension View {
    /// Dims a write control (add, edit, delete, check off) while the household is read-only. A tap
    /// plays the warning haptic and calls `onBlocked` (the app shows the "Shop is read-only" tray). Reads and export stay live.
    func writesLocked(_ locked: Bool, onBlocked: @escaping () -> Void) -> some View {
        modifier(WritesLocked(locked: locked, onBlocked: onBlocked))
    }
}

// MARK: - Pending deletion countdown

/// Big "27 days" in a ring that empties over the 30 day window. Not alarming until the last week.
struct DeletionCountdown: View {
    let daysLeft: Int
    var windowDays = 30

    var body: some View {
        let fraction = max(0, min(1, Double(daysLeft) / Double(windowDays)))
        let urgent = daysLeft <= 7
        ZStack {
            Circle().stroke(Palette.sunk, lineWidth: 10)
            Circle().trim(from: 0, to: fraction)
                .stroke(urgent ? Accent.red : Accent.amber, style: StrokeStyle(lineWidth: 10, lineCap: .round))
                .rotationEffect(.degrees(-90))
                .animation(Motion.spring, value: fraction)
            VStack(spacing: 0) {
                TypeStyle.hero.sized(44).text("\(daysLeft)").foregroundStyle(Palette.ink)
                TypeStyle.body.sized(14).text(daysLeft == 1 ? "day left" : "days left").foregroundStyle(Palette.ink2)
            }
        }
        .frame(width: 148, height: 148)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(HostedPlanStatus.days(daysLeft)) until your data is deleted")
    }
}

// MARK: - Household

/// "3 of 4 people": four dots, filled for each member.
struct SeatMeter: View {
    let used: Int
    /// Open invites also hold a seat (the server counts members plus open invites against the cap): ring dots.
    var pending = 0
    var total = 4

    var body: some View {
        HStack(spacing: Space.l) {
            HStack(spacing: 5) {
                ForEach(0..<total, id: \.self) { i in
                    Circle()
                        .fill(i < used ? Accent.blue : Palette.sunk)
                        .overlay(Circle().strokeBorder(Accent.blue, lineWidth: 1.5).opacity(i >= used && i < used + pending ? 1 : 0))
                        .frame(width: 10, height: 10)
                }
            }
            TypeStyle.body.sized(14).text(pending > 0 ? "\(used) of \(total) people · \(pending) \(pending == 1 ? "invite" : "invites") open" : "\(used) of \(total) people").foregroundStyle(Palette.ink2)
            Spacer(minLength: 0)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(pending > 0 ? "\(used) of \(total) people in this household, \(pending) invites open" : "\(used) of \(total) people in this household")
    }
}

/// One person in the household: initial tile, name, email or "You", a role chip, trailing menu.
struct MemberRow<Trailing: View>: View {
    let name: String
    var detail: String? = nil
    var isOwner = false
    var isYou = false
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(spacing: Space.l) {
            Text(String(name.first ?? "?").uppercased())
                .textStyle(TypeStyle.rowTitle)
                .foregroundStyle(Palette.ink)
                .frame(width: Size.tile, height: Size.tile)
                .background(Palette.sunk, in: Circle())
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    TypeStyle.rowTitle.text(name).foregroundStyle(Palette.ink).lineLimit(1)
                    if isYou { TypeStyle.rowSub.text("You").foregroundStyle(Palette.ink3) }
                }
                if let detail {
                    TypeStyle.rowSub.text(detail).foregroundStyle(Palette.ink2).lineLimit(1)
                }
            }
            Spacer(minLength: Space.m)
            if isOwner {
                TypeStyle.captionMedium.text("Owner")
                    .foregroundStyle(Palette.blueText)
                    .padding(.horizontal, 8)
                    .frame(height: Size.chipSmall)
                    .background(Palette.blueTint, in: RoundedRectangle.continuous(Radius.s))
            }
            trailing()
        }
        .frame(minHeight: Size.rowProduct)
        .hairline()
        .accessibilityElement(children: .contain)
    }
}

extension MemberRow where Trailing == EmptyView {
    init(name: String, detail: String? = nil, isOwner: Bool = false, isYou: Bool = false) {
        self.init(name: name, detail: detail, isOwner: isOwner, isYou: isYou, trailing: { EmptyView() })
    }
}

/// The invite link, ready to share: link in mono on a `sunk` capsule, who can use it and for how long.
struct InviteLinkCard: View {
    let link: String
    /// "Valid for 7 days. Works once."
    let terms: String
    let onShare: () -> Void
    let onCopy: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Space.l) {
            Button(action: onCopy) {
                HStack {
                    TypeStyle.mono.sized(13).text(link)
                        .foregroundStyle(Palette.ink)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer(minLength: Space.m)
                    IconView(.link, size: 15, weight: .semibold).foregroundStyle(Palette.ink2)
                }
                .padding(.horizontal, Space.xl)
                .frame(height: 46)
                .background(Palette.sunk, in: Capsule())
            }
            .buttonStyle(.press(haptic: .success))
            .accessibilityLabel("Invite link. Tap to copy.")
            TypeStyle.rowSub.text(terms).foregroundStyle(Palette.ink2)
            Button("Share invite link", action: onShare)
                .buttonStyle(.shop(size: .regular))
        }
    }
}
