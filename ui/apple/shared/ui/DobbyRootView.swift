import SwiftUI
#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

extension Color {
    static let paper = Color(DobbyPalette.paper)
    static let ink = Color(DobbyPalette.ink)
    static let surface = Color(DobbyPalette.surface)
    static let muted = Color(DobbyPalette.muted)
    static let rule = Color(DobbyPalette.rule)
    static let tray = Color(DobbyPalette.tray)
}

#if os(macOS)
// The mockup is drawn for phones; desktop text and controls are smaller.
private let textScale: CGFloat = 0.85
private let sizeScale: CGFloat = 0.75
#else
private let textScale: CGFloat = 1
private let sizeScale: CGFloat = 1
#endif

/// A mockup length in points, scaled for the platform.
private func pt(_ value: CGFloat) -> CGFloat { value * sizeScale }

/// Mockup font sizes that still follow the system text size on iOS.
private struct ScaledFont: ViewModifier {
    @ScaledMetric private var size: CGFloat
    private let weight: Font.Weight
    private let design: Font.Design

    init(size: CGFloat, weight: Font.Weight, design: Font.Design) {
        _size = ScaledMetric(wrappedValue: size * textScale)
        self.weight = weight
        self.design = design
    }

    func body(content: Content) -> some View {
        content.font(.system(size: size, weight: weight, design: design))
    }
}

extension View {
    func dobbyFont(_ size: CGFloat, _ weight: Font.Weight = .regular, design: Font.Design = .default) -> some View {
        modifier(ScaledFont(size: size, weight: weight, design: design))
    }
}

private let buttonShape = RoundedRectangle(cornerRadius: pt(10))

public struct DobbyRootView: View {
    @ObservedObject private var model: DobbySessionViewModel
    @Environment(\.scenePhase) private var scenePhase
    @FocusState private var sourceFocused: Bool
    @State private var canPaste = false
    @State private var followingLogs = true
    @State private var exportedLogsURL: URL?
    @State private var showingExport = false
    @State private var rowHeight: CGFloat = 0

    public init(model: DobbySessionViewModel) {
        self.model = model
    }

    public var body: some View {
        GeometryReader { geometry in
            VStack(spacing: 0) {
                VStack(spacing: pt(8)) {
                    subscriptionField
                    mainButton
                    connections
                        .frame(height: trayHeight(geometry.size.height))
                        .padding(.top, pt(8))
                }
                .padding(.horizontal, pt(12))
                .padding(.top, pt(8))
                .padding(.bottom, pt(12))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("Connection controls")
                logs
            }
            .frame(maxWidth: 480)
            .frame(maxWidth: .infinity)
        }
        .background(Color.paper.ignoresSafeArea().onTapGesture { sourceFocused = false })
        .ignoresSafeArea(.keyboard)
        .overlay { errorPopup }
        .preferredColorScheme(.light)
#if os(macOS)
        .frame(minWidth: 320, minHeight: 460)
        .navigationTitle("doBBYVPN")
#endif
        .onAppear { model.setLogsVisible(true); refreshClipboard() }
        .onDisappear { model.setLogsVisible(false) }
        .onChange(of: model.status) { announceStatus($0) }
        .onChange(of: scenePhase) { phase in model.setLogsVisible(phase == .active); refreshClipboard() }
#if os(macOS)
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            refreshClipboard()
        }
#endif
        .onOpenURL { model.importLink($0) }
        .sheet(isPresented: $showingExport, onDismiss: removeExport) {
            if let url = exportedLogsURL { DobbyShareSheet(url: url) }
        }
    }

    /// Four connections when there is room; on short screens the logs keep a quarter of the height.
    private func trayHeight(_ height: CGFloat) -> CGFloat {
        let fourRows = 4 * max(rowHeight, pt(56)) + 5 * pt(8)
        return min(max(height - pt(180) - height / 4, pt(72)), fourRows)
    }

    private var subscriptionField: some View {
        let showPaste = model.sourceText.isEmpty && !model.loading
        return HStack(spacing: pt(8)) {
            ZStack(alignment: .leading) {
                if model.sourceText.isEmpty {
                    Text("Subscription URL")
                        .dobbyFont(15)
                        .foregroundColor(.muted)
                        .lineLimit(1)
                        .accessibilityHidden(true)
                }
                TextField("", text: sourceBinding)
                    .textFieldStyle(.plain)
                    .dobbyFont(15)
                    .foregroundColor(.ink)
                    .focused($sourceFocused)
                    .accessibilityIdentifier("Connection configuration")
                    .accessibilityLabel("Subscription URL")
#if os(iOS)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.done)
#endif
            }
            if model.loading {
                Text("Loading…").dobbyFont(13).foregroundColor(.muted)
            } else if showPaste {
                pasteButton
            }
        }
        .padding(.leading, pt(12))
        .padding(.trailing, showPaste ? pt(5) : pt(12))
        .frame(minHeight: pt(44))
        .background(Color.surface, in: RoundedRectangle(cornerRadius: pt(8)))
        .overlay(RoundedRectangle(cornerRadius: pt(8)).stroke(Color.ink, lineWidth: 1.5))
    }

    @ViewBuilder
    private var pasteButton: some View {
#if os(iOS)
        if #available(iOS 16.0, *) {
            // The system button pastes without the "Allow Paste" prompt.
            PasteButton(payloadType: String.self) { values in
                model.pasteCallback(values)
            }
            .labelStyle(.titleOnly)
            .buttonBorderShape(.roundedRectangle(radius: pt(6)))
            .tint(.ink)
            .controlSize(.small)
            .font(.system(size: 14, weight: .semibold))
            // The system control draws past its frame at accessibility sizes, so it stops growing at Large.
            .dynamicTypeSize(...DynamicTypeSize.large)
            .accessibilityIdentifier("subscription.paste")
        } else if canPaste {
            PlainPasteButton { if let value = UIPasteboard.general.string { model.pasteCallback([value]) } }
                .accessibilityIdentifier("subscription.paste")
        }
#elseif os(macOS)
        if canPaste {
            PlainPasteButton { if let value = NSPasteboard.general.string(forType: .string) { model.paste(value) } }
        }
#endif
    }

    private func refreshClipboard() {
#if os(iOS)
        canPaste = UIPasteboard.general.hasStrings
#elseif os(macOS)
        canPaste = NSPasteboard.general.availableType(from: [.string]) != nil
#endif
    }

    private var sourceBinding: Binding<String> {
        Binding(get: { model.sourceText }, set: { model.sourceChanged($0) })
    }

    private var mainButton: some View {
        let title = model.mainActionTitle
        let enabled = model.mainActionEnabled
        let idle = title == "Auto connect"
        let filled = enabled && idle
        let outlined = enabled || !idle
        return Button {
            sourceFocused = false
            model.mainAction()
        } label: {
            Text(title)
                .dobbyFont(20, .semibold)
                .foregroundColor(filled ? .surface : outlined ? .ink : .muted)
                .frame(maxWidth: .infinity, minHeight: pt(60))
                .background(filled ? Color.ink : .clear, in: buttonShape)
                .overlay(buttonShape.stroke(outlined ? Color.ink : .rule, lineWidth: filled ? 0 : outlined ? 2 : 1.5))
                .contentShape(buttonShape)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .accessibilityIdentifier("VPN connection action")
        .accessibilityValue(model.status)
        .keyboardShortcut(.return, modifiers: .command)
    }

    private var connections: some View {
        let snapshot = model.snapshot
        let orphan = model.orphanProfile
        return ZStack {
            Color.tray.onTapGesture { sourceFocused = false }
            if snapshot.profiles.isEmpty && orphan == nil && !model.loadError.isEmpty {
                VStack(spacing: pt(10)) {
                    SockIcon(holey: true).frame(width: pt(40), height: pt(50))
                    Text("Couldn't load the subscription")
                        .dobbyFont(15, .medium)
                        .foregroundColor(.ink)
                    Button { model.retryLoad() } label: {
                        Text("Retry")
                            .dobbyFont(15, .semibold)
                            .foregroundColor(.surface)
                            .padding(.horizontal, pt(22))
                            .frame(minHeight: pt(40))
                            .background(Color.ink, in: RoundedRectangle(cornerRadius: pt(8)))
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("tray.retry")
                }
            } else if snapshot.profiles.isEmpty && orphan == nil {
                SockIcon(holey: false)
                    .frame(width: pt(40), height: pt(50))
                    .accessibilityElement()
                    .accessibilityLabel("No subscription yet")
            } else {
                ScrollView(showsIndicators: false) {
                    LazyVStack(spacing: pt(8)) {
                        if let orphan {
                            ConnectionButton(
                                name: orphan.name,
                                details: [protocolName(orphan.protocolName), model.sessionStatus?.rawValue, "not in subscription"],
                                on: isOn(model.sessionStatus),
                                action: model.mainActionTitle
                            ) { sourceFocused = false; model.mainAction() }
                            .disabled(!model.mainActionEnabled)
                            .accessibilityIdentifier("profile.active")
                        }
                        ForEach(snapshot.profiles) { profile in
                            let status = model.profileStatus(profile.index)
                            ConnectionButton(
                                name: profile.name,
                                details: [protocolName(profile.protocolName), status?.rawValue],
                                on: isOn(status),
                                action: model.profileActionTitle(profile.index)
                            ) { sourceFocused = false; model.performPrimaryAction(profile.index) }
                            .disabled(!model.canAct(profile.index))
                            .accessibilityIdentifier("profile.\(snapshot.digest).\(profile.index).action")
                        }
                    }
                    .padding(pt(8))
                }
                .accessibilityIdentifier("Profile list viewport")
                .onPreferenceChange(RowHeight.self) { rowHeight = $0 }
            }
        }
        .clipShape(RoundedRectangle(cornerRadius: pt(14)))
    }

    private func isOn(_ status: DobbyProfileStatus?) -> Bool {
        status == .connected || status == .reconnecting
    }

    private func protocolName(_ value: String) -> String? {
        switch value {
        case "": return nil
        case "OUTLINE": return "Outline"
        case "XRAY": return "Xray"
        case "TRUST_TUNNEL": return "TrustTunnel"
        default: return value
        }
    }

    private var logs: some View {
        VStack(spacing: 0) {
            Rectangle().fill(Color.ink).frame(height: 1.5)
#if os(iOS)
            DobbyLogView(entries: model.logEntries, clear: model.clearRevision, onSendLogs: sendLogs, onClear: model.clearLogs)
                .accessibilityIdentifier("Connection logs")
#else
            DobbyLogView(
                entries: model.logEntries, following: $followingLogs, clear: model.clearRevision,
                interactive: !showingError, onSendLogs: sendLogs, onClear: { followingLogs = true; model.clearLogs() }
            )
            .accessibilityIdentifier("Connection logs")
#endif
        }
        .background(Color.surface.ignoresSafeArea(edges: .bottom))
    }

    private func sendLogs() {
        model.prepareLogsExport { url in
            exportedLogsURL = url
            showingExport = true
        }
    }

    private func removeExport() {
        guard let url = exportedLogsURL else { return }
        do { try FileManager.default.removeItem(at: url) } catch { model.reportLogsError(error.localizedDescription) }
        exportedLogsURL = nil
    }

    private var showingError: Bool { !model.visibleFailure.isEmpty }

    @ViewBuilder
    private var errorPopup: some View {
        let detail = model.visibleFailure
        if !detail.isEmpty {
            let loadFailed = detail == model.loadError
            ZStack {
                Color.black.opacity(0.32).ignoresSafeArea().onTapGesture(perform: model.closeFailure)
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: pt(10)) {
                        SockIcon(holey: true).frame(width: pt(26), height: pt(32.5))
                        Text(loadFailed ? "Couldn't load the subscription" : "Something went wrong")
                            .dobbyFont(18, .semibold)
                            .foregroundColor(.ink)
                    }
                    if loadFailed {
                        Text("Check the link and whether another VPN is blocking it.")
                            .dobbyFont(14)
                            .foregroundColor(.muted)
                            .padding(.top, pt(8))
                    }
                    HStack(spacing: pt(8)) {
                        Text(detail)
                            .dobbyFont(11.5, design: .monospaced)
                            .foregroundColor(.ink)
                            .lineLimit(4)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .accessibilityIdentifier("error.detail")
                        Button { copy(detail) } label: {
                            Text("Copy").underline().dobbyFont(12, .semibold).foregroundColor(.ink)
                        }
                        .buttonStyle(.plain)
                    }
                    .padding(.horizontal, pt(10))
                    .padding(.vertical, pt(8))
                    .overlay(RoundedRectangle(cornerRadius: pt(6)).stroke(Color.rule, lineWidth: 1))
                    .padding(.top, pt(10))
                    HStack(spacing: pt(8)) {
                        DialogButton(title: "Close", filled: false, action: model.closeFailure)
                        if loadFailed {
                            DialogButton(title: "Retry", filled: true) { model.retryLoad() }
                                .accessibilityIdentifier("subscription.retry")
                        }
                    }
                    .padding(.top, pt(14))
                }
                .padding(EdgeInsets(top: pt(16), leading: pt(16), bottom: pt(12), trailing: pt(16)))
                .background(Color.surface, in: buttonShape)
                .overlay(buttonShape.stroke(Color.ink, lineWidth: 1.5))
                .frame(maxWidth: 420)
                .padding(.horizontal, pt(24))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("error.dialog")
                .accessibilityValue(model.status)
                .accessibilityAddTraits(.isModal)
            }
        }
    }

    private func copy(_ text: String) {
#if os(iOS)
        UIPasteboard.general.string = text
#elseif os(macOS)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
#endif
    }

    private func announceStatus(_ status: String) {
        guard scenePhase == .active else { return }
#if os(iOS)
        UIAccessibility.post(notification: .announcement, argument: status)
#elseif os(macOS)
        if let window = NSApp.mainWindow {
            NSAccessibility.post(element: window, notification: .announcementRequested, userInfo: [
                .announcement: status, .priority: NSAccessibilityPriorityLevel.medium.rawValue,
            ])
        }
#endif
    }
}

private struct PlainPasteButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text("Paste")
                .dobbyFont(14, .semibold)
                .foregroundColor(.surface)
                .padding(.horizontal, pt(14))
                .frame(minHeight: pt(34))
                .background(Color.ink, in: RoundedRectangle(cornerRadius: pt(6)))
        }
        .buttonStyle(.plain)
    }
}

private struct ConnectionButton: View {
    let name: String
    let details: [String?]
    let on: Bool
    let action: String
    let perform: () -> Void

    var body: some View {
        Button(action: perform) {
            HStack(spacing: pt(12)) {
                VStack(alignment: .leading, spacing: 0) {
                    Text(name)
                        .dobbyFont(17, .medium)
                        .foregroundColor(.ink)
                    Text(details.compactMap { $0 }.joined(separator: " · "))
                        .dobbyFont(13, on ? .semibold : .regular)
                        .foregroundColor(on ? .ink : .muted)
                }
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
                Text(action)
                    .dobbyFont(15, .semibold)
                    .foregroundColor(.ink)
            }
            .padding(.horizontal, pt(14))
            .padding(.vertical, pt(8))
            .frame(minHeight: pt(56))
            .background(Color.surface, in: buttonShape)
            .overlay(buttonShape.stroke(Color.ink, lineWidth: on ? 3 : 1.5))
            .contentShape(buttonShape)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(action)
        .accessibilityValue(([name] + details.compactMap { $0 }).joined(separator: " · "))
        .background(GeometryReader { Color.clear.preference(key: RowHeight.self, value: $0.size.height) })
    }
}

private struct RowHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

private struct DialogButton: View {
    let title: String
    let filled: Bool
    let action: () -> Void

    var body: some View {
        let shape = RoundedRectangle(cornerRadius: pt(8))
        Button(action: action) {
            Text(title)
                .dobbyFont(15, .semibold)
                .foregroundColor(filled ? .surface : .ink)
                .frame(maxWidth: .infinity, minHeight: pt(44))
                .background(filled ? Color.ink : .clear, in: shape)
                .overlay(shape.stroke(Color.ink, lineWidth: filled ? 0 : 1.5))
                .contentShape(shape)
        }
        .buttonStyle(.plain)
    }
}

/// The doBBYVPN sock, drawn in a 40×50 box: plain when there is no subscription, holey on errors.
private struct SockIcon: View {
    let holey: Bool

    var body: some View {
        Canvas { context, size in
            context.scaleBy(x: size.width / 40, y: size.width / 40)
            let sock = Self.sock
            if holey {
                context.fill(sock, with: .color(.paper))
                context.stroke(sock, with: .color(.ink), style: StrokeStyle(lineWidth: 2.5, lineCap: .round, dash: [3.2, 2.8]))
                var hole = context
                hole.translateBy(x: 10.5, y: 41)
                hole.rotate(by: .degrees(-25))
                hole.fill(Path(ellipseIn: CGRect(x: -3.6, y: -2.9, width: 7.2, height: 5.8)), with: .color(.ink))
                context.stroke(Self.rays, with: .color(.ink), style: StrokeStyle(lineWidth: 1.2, lineCap: .round))
            } else {
                context.fill(sock, with: .color(.surface))
                context.stroke(sock, with: .color(.ink), lineWidth: 2.5)
                var stripes = Path()
                for y in [8.5, 12.5] {
                    stripes.move(to: CGPoint(x: 15.5, y: y))
                    stripes.addLine(to: CGPoint(x: 30.5, y: y))
                }
                context.stroke(stripes, with: .color(.ink), lineWidth: 2)
            }
        }
    }

    private static let sock: Path = {
        var path = Path()
        path.move(to: CGPoint(x: 17, y: 3))
        path.addLine(to: CGPoint(x: 29, y: 3))
        path.addQuadCurve(to: CGPoint(x: 30.5, y: 4.5), control: CGPoint(x: 30.5, y: 3))
        path.addLine(to: CGPoint(x: 30.5, y: 27))
        path.addQuadCurve(to: CGPoint(x: 26, y: 40), control: CGPoint(x: 32, y: 36))
        path.addLine(to: CGPoint(x: 14, y: 46.5))
        path.addQuadCurve(to: CGPoint(x: 4.8, y: 44.5), control: CGPoint(x: 8, y: 49))
        path.addQuadCurve(to: CGPoint(x: 7.5, y: 36), control: CGPoint(x: 2.2, y: 39.5))
        path.addLine(to: CGPoint(x: 13, y: 32.5))
        path.addQuadCurve(to: CGPoint(x: 15.5, y: 27.5), control: CGPoint(x: 15.5, y: 30.8))
        path.addLine(to: CGPoint(x: 15.5, y: 4.5))
        path.addQuadCurve(to: CGPoint(x: 17, y: 3), control: CGPoint(x: 15.5, y: 3))
        path.closeSubpath()
        return path
    }()

    private static let rays: Path = {
        let rays: [[CGFloat]] = [
            [5.6, 39.6, -1.5, -0.7], [6.4, 43.8, -1.2, 1.1], [9.8, 45.5, -0.3, 1.6], [13.6, 44.6, 1, 1.3],
            [15.6, 41.2, 1.6, 0.2], [14, 37.6, 1.1, -1.3], [10.2, 36.6, -0.2, -1.6], [7, 37.2, -1, -1.2],
        ]
        var path = Path()
        for ray in rays {
            path.move(to: CGPoint(x: ray[0], y: ray[1]))
            path.addLine(to: CGPoint(x: ray[0] + ray[2], y: ray[1] + ray[3]))
        }
        return path
    }()
}
