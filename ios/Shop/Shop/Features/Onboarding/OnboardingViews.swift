import AVFoundation
import SwiftUI

struct OnboardingFlow: View {
    @Environment(AppModel.self) private var app
    @State private var model: OnboardingModel?
    @State private var forward = true

    var body: some View {
        Group {
            if let model {
                content(model)
            } else {
                Color.clear
            }
        }
        .onAppear {
            if model == nil { model = OnboardingModel(app: app) }
        }
    }

    @ViewBuilder
    private func content(_ model: OnboardingModel) -> some View {
        ZStack {
            switch model.step {
            case .welcome:
                WelcomeScreen { go(model, to: .server) }
                    .transition(stepTransition)
            case .server:
                ServerScreen(model: model, onBack: { go(model, to: .welcome) }, onContinue: { go(model, to: .password) })
                    .transition(stepTransition)
            case .password:
                PasswordScreen(model: model, onBack: { go(model, to: .server) })
                    .transition(stepTransition)
            case .ready:
                ReadyScreen(model: model)
                    .transition(stepTransition)
            }
        }
        .animation(Motion.ease, value: model.step)
    }

    private var stepTransition: AnyTransition {
        .asymmetric(
            insertion: .offset(x: forward ? 24 : -24).combined(with: .opacity),
            removal: .offset(x: forward ? -24 : 24).combined(with: .opacity)
        )
    }

    private func go(_ model: OnboardingModel, to step: OnboardingModel.Step) {
        forward = step > model.step
        model.step = step
    }
}

// MARK: - O.1 Welcome

struct WelcomeScreen: View {
    let onConnect: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            WelcomeIllustration()
                .frame(height: 400)
            VStack(alignment: .leading, spacing: Space.l) {
                TypeStyle.display.text("Every receipt, itemised.")
                    .foregroundStyle(Palette.ink)
                    .fixedSize(horizontal: false, vertical: true)
                TypeStyle.input.text("Scan paper receipts. Shop reads them on your own server and tracks what you spend and what things cost, shop by shop.")
                    .foregroundStyle(Palette.ink2)
                    .lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, Space.xxxl)
            Spacer(minLength: Space.xxl)
            VStack(spacing: Space.lx) {
                Button("Connect your server", action: onConnect)
                    .buttonStyle(.shop(size: .large))
                TypeStyle.body.sized(13).text("Nothing leaves your home network")
                    .foregroundStyle(Palette.ink2)
            }
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, Space.xxl)
        }
        .background(Palette.bg)
    }
}

/// Floating tilted cards from O.1.
private struct WelcomeIllustration: View {
    @State private var settled = false
    @Environment(\.accessibilityReduceMotion) private var reduce

    var body: some View {
        ZStack {
            card(title: "Dairy", amount: "€78,40", color: Accent.pink, width: 170, height: 112)
                .rotationEffect(.degrees(-12))
                .offset(x: -70, y: -24)
            card(title: "Produce", amount: "€71,20", color: Accent.green, width: 160, height: 106)
                .rotationEffect(.degrees(9))
                .offset(x: 70, y: -50)
            VStack(alignment: .leading) {
                HStack {
                    Text(Dates.month(.now))
                    Spacer()
                    Text("14 receipts").opacity(0.8)
                }
                .textStyle(.label)
                Spacer()
                HeroAmount(cents: 41238, size: 52, color: Accent.white, minorColor: Accent.white.opacity(0.7))
            }
            .foregroundStyle(Accent.white)
            .padding(18)
            .frame(width: 250, height: 176)
            .background(Accent.blue, in: RoundedRectangle.continuous(Radius.card))
            .shadow(color: Accent.blue.opacity(0.3), radius: 20, y: 20)
            .rotationEffect(.degrees(-2))
            .offset(x: 0, y: 82)
            TypeStyle.label.sized(13.5).text("Melk €0,85/l at Lidl")
                .foregroundStyle(Accent.onLight)
                .padding(.horizontal, 14)
                .frame(height: 40)
                .background(Accent.yellow, in: Capsule())
                .shadow(color: .black.opacity(0.12), radius: 10, y: 10)
                .rotationEffect(.degrees(6))
                .offset(x: 72, y: 166)
        }
        .scaleEffect(settled || reduce ? 1 : 0.94)
        .opacity(settled || reduce ? 1 : 0)
        .onAppear { withAnimation(Motion.spring.delay(0.05)) { settled = true } }
        .accessibilityHidden(true)
    }

    private func card(title: String, amount: String, color: Color, width: CGFloat, height: CGFloat) -> some View {
        VStack(alignment: .leading) {
            TypeStyle.label.text(title)
            Spacer()
            TypeStyle.emptyTitle.text(amount)
        }
        .foregroundStyle(Accent.white)
        .padding(16)
        .frame(width: width, height: height, alignment: .leading)
        .background(color, in: RoundedRectangle.continuous(Radius.art))
    }
}

// MARK: - O.2 Server

struct ServerScreen: View {
    @Bindable var model: OnboardingModel
    let onBack: () -> Void
    let onContinue: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            StepHeader(step: "Step 1 of 2", onBack: onBack)
            ScrollView {
                VStack(alignment: .leading, spacing: Space.xxl) {
                    HeadlineBlock(title: "Where is your server?", message: "The address of the Mac mini running Shop. A Tailscale address keeps it working away from home.")
                        .padding(.horizontal, Space.xs)
                    VStack(alignment: .leading, spacing: Space.ml) {
                        FieldTile(label: "Server URL", text: $model.urlText, placeholder: "http://shop.local:8080", mono: true, large: true) {
                            Task { await model.testConnection() }
                        }
                        .keyboardType(.URL)
                        .textContentType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.go)
                        .onChange(of: model.urlText) { _, _ in model.urlEdited() }

                        HStack(spacing: Space.s) {
                            ForEach(model.suggestions, id: \.self) { s in
                                Button {
                                    Haptics.play(.selection)
                                    model.urlText = "http://" + s
                                } label: {
                                    TypeStyle.mono.sized(12.5).withFace(.monoRegular).text(s)
                                        .foregroundStyle(Palette.ink2)
                                        .padding(.horizontal, Space.l)
                                        .frame(height: Size.chip)
                                        .background(Palette.sunk, in: Capsule())
                                }
                                .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
                            }
                        }
                    }
                    checkResult
                }
                .padding(.horizontal, Space.edge)
                .padding(.top, 26)
            }
            .scrollDismissesKeyboard(.interactively)
            Button {
                if model.canContinueFromServer {
                    onContinue()
                } else {
                    Task { await model.testConnection() }
                }
            } label: {
                LoadingLabel(title: model.canContinueFromServer ? "Continue" : "Test connection", isLoading: model.serverCheck == .testing)
                    .labelMorph()
            }
            .buttonStyle(.shop(size: .large))
            .disabled(model.urlText.trimmingCharacters(in: .whitespaces).isEmpty || model.serverCheck == .testing)
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, Space.xxl)
            .padding(.top, Space.m)
        }
        .background(Palette.bg)
        .animation(Motion.ease, value: model.serverCheck)
    }

    @ViewBuilder
    private var checkResult: some View {
        switch model.serverCheck {
        case .idle:
            EmptyView()
        case .testing:
            // The spinner lives inside the button; this line just says what is happening.
            TypeStyle.body.text("Looking for the server…").foregroundStyle(Palette.ink2)
                .padding(.top, Space.m)
        case .found(let version, let ms, let checks):
            VStack(spacing: 0) {
                HStack(spacing: Space.l) {
                    IconTile(icon: .check, color: Accent.green, size: 36)
                    VStack(alignment: .leading, spacing: 1) {
                        TypeStyle.rowTitle.text("Shop server found").foregroundStyle(Palette.ink)
                        TypeStyle.rowSub.text("Version \(version) · answered in \(ms) ms").foregroundStyle(Palette.ink2)
                    }
                    Spacer()
                }
                .padding(.bottom, Space.l)
                ForEach(checks) { row in
                    KeyValueRow(key: row.key, value: row.value, dot: row.ok ? Accent.green : Accent.red)
                }
            }
            .transition(.opacity.combined(with: .offset(y: 8)))
        case .failed(let message):
            Banner(tone: .red, message: message)
                .transition(.opacity.combined(with: .offset(y: 8)))
        }
    }
}

// MARK: - O.3 Password

struct PasswordScreen: View {
    @Bindable var model: OnboardingModel
    var step: String = "Step 2 of 2"
    var title: String = "Enter the server password"
    var message: String = "The pairing password from setting up the Mac mini. This iPhone gets its own key; the password isn't stored."
    let onBack: (() -> Void)?

    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            StepHeader(step: step, onBack: onBack)
            ScrollView {
                VStack(alignment: .leading, spacing: Space.xxl) {
                    HeadlineBlock(title: title, message: message)
                        .padding(.horizontal, Space.xs)
                    VStack(alignment: .leading, spacing: Space.ml) {
                        FieldTile(label: "Password", text: $model.password, large: true, isSecure: true, error: isError) {
                            Task { await model.submitPassword() }
                        }
                        .textContentType(.password)
                        .submitLabel(.go)
                        .shake(model.wrongAttempts)
                        .onChange(of: model.password) { _, _ in model.passwordEdited() }

                        statusLine
                    }
                    hintCard
                }
                .padding(.horizontal, Space.edge)
                .padding(.top, 26)
            }
            .scrollDismissesKeyboard(.interactively)
            Button {
                Task { await model.submitPassword() }
            } label: {
                LoadingLabel(title: "Connect", isLoading: model.signIn == .working)
            }
            .buttonStyle(.shop(size: .large))
            .disabled(model.password.isEmpty || model.signIn == .working)
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, Space.xxl)
            .padding(.top, Space.m)
        }
        .background(Palette.bg)
        .animation(Motion.ease, value: model.signIn)
    }

    private var isError: Bool {
        switch model.signIn {
        case .wrong, .locked, .failed: true
        default: false
        }
    }

    @ViewBuilder
    private var statusLine: some View {
        switch model.signIn {
        case .wrong:
            TypeStyle.label.text("Wrong password. Check it and try again.")
                .foregroundStyle(Palette.redText)
                .transition(.opacity)
        case .locked(let message), .failed(let message):
            Banner(tone: .red, message: message)
                .transition(.opacity)
        default:
            EmptyView()
        }
    }

    /// Dark terminal card: where the password comes from.
    private var hintCard: some View {
        VStack(alignment: .leading, spacing: Space.ml) {
            HStack(spacing: Space.m) {
                Text("?")
                    .font(.custom(ShopFont.bold, size: 11))
                    .foregroundStyle(Accent.onLight)
                    .frame(width: 20, height: 20)
                    .background(Accent.yellow, in: RoundedRectangle.continuous(Radius.badge))
                TypeStyle.label.withFace(.regular).text("Lost it? On the Mac mini, run")
                    .foregroundStyle(Palette.darkCardInk2)
            }
            HStack(spacing: Space.ml) {
                (Text("$ ").foregroundStyle(Palette.scanGreen) + Text("shop password generate"))
                    .textStyle(TypeStyle.mono.sized(14).withFace(.monoRegular))
                    .foregroundStyle(Palette.darkCardInk)
                Spacer()
                Button {
                    UIPasteboard.general.string = "docker compose -f deploy/docker-compose.yml exec api shop password generate"
                    Haptics.play(.success)
                } label: {
                    IconView(.link, size: 15)
                        .foregroundStyle(Palette.darkCardInk)
                        .frame(width: 34, height: 34)
                        .background(Palette.darkCardField, in: Circle())
                }
                .buttonStyle(.press(scale: Motion.pillScale, haptic: .selection))
                .accessibilityLabel("Copy command")
            }
            TypeStyle.mono.sized(12).withFace(.monoRegular).text("or: docker compose exec api shop password generate")
                .foregroundStyle(Palette.darkCardInk3)
        }
        .padding(Space.xl)
        .background(Palette.darkCard, in: RoundedRectangle.continuous(Radius.hero))
    }
}

// MARK: - O.4 Ready

struct ReadyScreen: View {
    @Bindable var model: OnboardingModel
    @State private var popped = false

    private let rows: [(String, Color)] = [
        ("Shops", Accent.orange), ("Categories", Accent.pink), ("Shopping list", Accent.green), ("Products", Accent.blue),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 18) {
                IconTile(icon: .check, color: Accent.green, size: 60)
                    .shadow(color: Accent.green.opacity(0.35), radius: 14, y: 12)
                    .scaleEffect(popped ? 1 : 0.6)
                    .opacity(popped ? 1 : 0)
                HeadlineBlock(title: "You're connected", message: "Copying your catalog to this iPhone so the list and products work without signal.")
            }
            .padding(.horizontal, Space.xxxl)
            .padding(.top, 60)

            VStack(spacing: 0) {
                ForEach(rows.indices, id: \.self) { i in
                    syncRow(name: rows[i].0, color: rows[i].1)
                }
                cameraRow
                    .padding(.top, 18)
            }
            .padding(.horizontal, Space.gutter)
            .padding(.top, Space.xxxl)

            Spacer(minLength: Space.xxl)
            VStack(spacing: Space.m) {
                Button("Scan your first receipt") { model.finish(scanFirst: true) }
                    .buttonStyle(.shop(size: .large))
                Button("Go to Home") { model.finish(scanFirst: false) }
                    .buttonStyle(.press(haptic: .light))
                    .textStyle(TypeStyle.rowTitle)
                    .foregroundStyle(Palette.ink2)
                    .frame(height: 48)
            }
            .padding(.horizontal, Space.gutter)
            .padding(.bottom, Space.xxl)
        }
        .background(Palette.bg)
        .onAppear { withAnimation(Motion.spring) { popped = true } }
    }

    private func syncRow(name: String, color: Color) -> some View {
        let progress = model.syncProgress[name]
        let done = progress.map { $0.1 != nil } ?? false
        let countText: String = {
            guard let progress else { return "…" }
            if let total = progress.1 { return "\(total)" }
            return progress.0 > 0 ? "\(progress.0)…" : "…"
        }()
        return HStack(spacing: Space.l) {
            RoundedRectangle.continuous(Radius.tile).fill(color).frame(width: 32, height: 32)
            VStack(spacing: 5) {
                HStack {
                    TypeStyle.rowTitle.text(name).foregroundStyle(Palette.ink)
                    Spacer()
                    TypeStyle.input.sized(15).text(countText).foregroundStyle(Palette.ink2)
                }
                ShareBar(fraction: done ? 1 : (progress == nil ? 0.08 : 0.6), color: color)
            }
        }
        .frame(height: 54)
        .hairline()
        .animation(Motion.ease, value: done)
    }

    private var cameraRow: some View {
        HStack(spacing: Space.l) {
            IconView(.camera, size: 18)
                .foregroundStyle(Palette.ink)
                .frame(width: 36, height: 36)
                .background(Palette.bg, in: RoundedRectangle.continuous(Radius.ml))
            VStack(alignment: .leading, spacing: 1) {
                TypeStyle.rowTitle.text("Camera").foregroundStyle(Palette.ink)
                TypeStyle.rowSub.text("For receipts and barcodes").foregroundStyle(Palette.ink2)
            }
            Spacer()
            switch model.cameraStatus {
            case .authorized:
                GlyphBadge(glyph: .check, color: Accent.green, size: 22)
            case .denied, .restricted:
                Button("Settings") {
                    if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
                }
                .buttonStyle(.shop(.secondary, size: .small, fullWidth: false))
            default:
                Button("Allow") { Task { await model.requestCamera() } }
                    .buttonStyle(.shop(.solid(Accent.blue), size: .small, fullWidth: false))
            }
        }
        .padding(.vertical, Space.l)
        .padding(.horizontal, Space.lx)
        .background(Palette.sunk, in: RoundedRectangle.continuous(Radius.input))
    }
}

// MARK: - Token rejected

/// The server stopped accepting this iPhone's token. Local scans and list are kept.
struct TokenRejectedView: View {
    @Environment(AppModel.self) private var app
    @State private var model: OnboardingModel?

    var body: some View {
        Group {
            if let model {
                PasswordScreen(
                    model: model,
                    step: app.serverURL?.host() ?? "Server",
                    title: "Sign in again",
                    message: "The server no longer accepts this iPhone's key. Enter the server password to get a new one. Your scans and list are still on this iPhone.",
                    onBack: nil
                )
                .overlay(alignment: .topTrailing) {
                    Button("Other server") { Task { await app.signOut() } }
                        .textStyle(.label)
                        .foregroundStyle(Palette.blueText)
                        .padding(.top, Space.xxl)
                        .padding(.trailing, Space.gutter)
                }
                .onChange(of: model.step) { _, step in
                    if step == .ready { app.enterApp() }
                }
            }
        }
        .onAppear {
            if model == nil {
                let m = OnboardingModel(app: app, presetURL: app.serverURL)
                m.step = .password
                model = m
            }
        }
    }
}

#Preview("Welcome") {
    WelcomeScreen {}
}

#Preview("Server") {
    let app = AppModel(mock: true)
    let model = OnboardingModel(app: app)
    return ServerScreen(model: model, onBack: {}, onContinue: {})
        .environment(app)
}

#Preview("Password") {
    let app = AppModel(mock: true)
    let model = OnboardingModel(app: app)
    return PasswordScreen(model: model, onBack: {})
        .environment(app)
}

#Preview("Ready · dark") {
    let app = AppModel(mock: true)
    let model = OnboardingModel(app: app)
    return ReadyScreen(model: model)
        .environment(app)
        .preferredColorScheme(.dark)
}
