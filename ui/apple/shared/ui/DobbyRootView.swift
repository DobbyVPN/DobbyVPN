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
func pt(_ value: CGFloat) -> CGFloat { value * sizeScale }

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

#if os(iOS)
private struct DobbyErrorDetailView: UIViewRepresentable {
    let text: String
    @ScaledMetric(relativeTo: .caption) private var fontSize: CGFloat = 11.5

    func makeUIView(context: Context) -> UITextView {
        let view = DobbyErrorTextView()
        configureErrorDetailTextView(view)
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        let font = UIFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
        if view.font != font { view.font = font }
        view.textColor = DobbyPalette.ink
        if view.text != text {
            view.text = text
            view.setContentOffset(.zero, animated: false)
        }
        view.accessibilityValue = text
    }
}
#endif

let buttonShape = RoundedRectangle(cornerRadius: pt(10))

public struct DobbyRootView: View {
    @ObservedObject private var model: DobbySessionViewModel
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.sizeCategory) private var sizeCategory
#if os(macOS)
    @Environment(\.controlActiveState) private var controlActiveState
#endif
    @AccessibilityFocusState private var uploadTitleFocused: Bool
    @AccessibilityFocusState private var logsFocused: Bool
    @FocusState private var sourceFocused: Bool
    @State private var canPaste = false
    @State private var followingLogs = true
    @State private var exportedLogsURL: URL?
    @State private var showingExport = false
    @State private var showingLogMenu = false
    @State private var showingReferenceShare = false
    @State private var rowHeight: CGFloat = 0
    @State private var errorDetailHeight: CGFloat = 240
#if os(iOS)
    @State private var logActionsHeight: CGFloat = 0
#endif

    public init(model: DobbySessionViewModel) {
        self.model = model
    }

    public var body: some View {
        GeometryReader { geometry in
            VStack(spacing: 0) {
                VStack(spacing: pt(8)) {
                    subscriptionField
                    if !model.snapshot.offlineAcceptedAt.isEmpty {
                        Text("Using saved profiles from \(model.snapshot.offlineAcceptedAt)")
                            .font(.caption)
                            .accessibilityIdentifier("configuration.offline")
                    }
                    if model.vpnPermissionNeeded { vpnPermission }
                    mainButton
                    reconnectLine(identifier: "connection.reconnect")
                    connections
                        .frame(minHeight: pt(72), maxHeight: trayHeight(geometry.size.height))
                        .padding(.top, pt(8))
                }
                .padding(.horizontal, pt(12))
                .padding(.top, pt(8))
                .padding(.bottom, pt(12))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("Connection controls")
            logs.frame(maxHeight: .infinity)
            }
#if os(macOS)
            // On macOS the window can be stretched arbitrarily wide; let the content follow it
            // instead of leaving empty margins on either side, the way iOS's fixed screen width
            // never needed a cap for.
            .frame(maxWidth: .infinity)
#else
            .frame(maxWidth: 480)
            .frame(maxWidth: .infinity)
#endif
        }
        .disabled(showingError || model.vpnSetupExplanationVisible || showingUploadResult)
        .accessibilityHidden(showingError || model.vpnSetupExplanationVisible || showingUploadResult)
        .background(Color.paper.ignoresSafeArea().onTapGesture { sourceFocused = false })
        .ignoresSafeArea(.keyboard)
        .overlay { errorPopup }
        .overlay { vpnSetupExplanation }
        .overlay { uploadResultDialog }
        .onChange(of: showingUploadResult) { shown in
            // VoiceOver moves to the result when it appears, after the overlay is in the hierarchy.
            if shown { DispatchQueue.main.async { uploadTitleFocused = true } }
        }
        .onChange(of: model.uploadResult == nil) { closed in
            if closed { logsFocused = true }
        }
        .preferredColorScheme(.light)
#if os(macOS)
        .frame(minWidth: 320, minHeight: 460)
        .navigationTitle("doBBYVPN")
#endif
        .onAppear { model.setLogsVisible(true); refreshClipboard() }
        .onDisappear { model.setLogsVisible(false) }
        .onChange(of: model.status) { announceStatus($0) }
        .onChange(of: showingError) { if $0 { sourceFocused = false } }
        .onChange(of: scenePhase) { phase in
            model.setLogsVisible(phase == .active)
            refreshClipboard()
        }
#if os(macOS)
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            refreshClipboard()
        }
#endif
        .onOpenURL { model.importLink($0) }
        .importConfirmation(model)
#if os(iOS)
        .sheet(isPresented: $showingReferenceShare, onDismiss: {
            model.recordLogsShare(kind: "reference", phase: "dismissed")
        }) {
            if let latest = model.latestUpload {
                DobbyShareSheet(url: nil, text: latest.shareText) {
                    model.recordLogsShare(kind: "reference", phase: "controller-created")
                }.onAppear { model.recordLogsShare(kind: "reference", phase: "view-appeared") }
            }
        }
        .sheet(isPresented: $showingExport, onDismiss: {
            model.recordLogsShare(kind: "archive", phase: "dismissed")
            removeExport()
        }) {
            if let url = exportedLogsURL {
                DobbyShareSheet(url: url) {
                    model.recordLogsShare(kind: "archive", phase: "controller-created")
                }.onAppear { model.recordLogsShare(kind: "archive", phase: "view-appeared") }
            }
        }
#endif
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

    @ViewBuilder
    private var mainButton: some View {
        if model.snapshot.vpnActive && model.snapshot.state == "FAILED" && model.snapshot.canRetry {
            // Retry and Stop share one row, 2:1 as Android's weights 1 and 0.5. A hidden
            // copy of the Retry title sets the row height, so larger text grows the row;
            // both titles stay on one line, so the copy's width does not change the height.
            Text(model.mainActionTitle)
                .dobbyFont(20, .semibold)
                .lineLimit(1)
                .frame(maxWidth: .infinity, minHeight: pt(60))
                .hidden()
                .overlay(GeometryReader { row in
                    HStack(spacing: pt(8)) {
                        primaryButton(fillHeight: true).frame(width: max(0, row.size.width - pt(8)) * 2 / 3)
                        stopButton
                    }
                })
        } else {
            primaryButton()
        }
    }

    /// `fillHeight` lets the button match a taller neighbour in the Retry and Stop row.
    private func primaryButton(fillHeight: Bool = false) -> some View {
        let title = model.mainActionTitle
        let enabled = model.mainActionEnabled
        // After a refused VPN setup the main button is the filled "Allow VPN".
        let idle = title == "Auto connect" || title == "Allow VPN"
        let filled = enabled && idle
        let outlined = enabled || !idle
        return Button {
            sourceFocused = false
            model.mainAction()
        } label: {
            Text(title)
                .dobbyFont(20, .semibold)
                // In the row the title keeps one line and shrinks rather than wrapping past its height.
                .lineLimit(fillHeight ? 1 : nil)
                .minimumScaleFactor(fillHeight ? 0.5 : 1)
                .foregroundColor(filled ? .surface : outlined ? .ink : .muted)
                .frame(maxWidth: .infinity, minHeight: pt(60), maxHeight: fillHeight ? .infinity : nil)
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

    private var stopButton: some View {
        let enabled = model.canStop
        return Button(action: model.stop) {
            Text("Stop")
                .dobbyFont(18, .semibold)
                // The third of the row stays narrow; large text shrinks instead of clipping.
                .lineLimit(1)
                .minimumScaleFactor(0.5)
                .foregroundColor(enabled ? .ink : .muted)
                .padding(.horizontal, pt(4))
                .frame(maxWidth: .infinity, minHeight: pt(60), maxHeight: .infinity)
                .overlay(buttonShape.stroke(enabled ? Color.ink : .rule, lineWidth: enabled ? 2 : 1.5))
                .contentShape(buttonShape)
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .accessibilityIdentifier("connection.stop")
    }

    /// A refused or timed-out VPN setup: one sentence above the main button, which
    /// reads "Allow VPN" and retries the save; the technical detail only behind Copy.
    private var vpnPermission: some View {
        VStack(alignment: .leading, spacing: pt(6)) {
            Text("DobbyVPN needs VPN permission to connect.")
                .dobbyFont(15)
                .foregroundColor(.ink)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("vpn.permission.message")
            Button { copyError(model.vpnSetupFailureDetail, onObservation: model.recordErrorCopy) } label: {
                Text("Copy").underline().dobbyFont(12, .semibold).foregroundColor(.ink)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Copy VPN setup details")
            .accessibilityIdentifier("vpn.permission.copy")
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("vpn.permission")
    }

    /// Shown before the first save, which makes iOS ask for VPN permission.
    @ViewBuilder
    private var vpnSetupExplanation: some View {
        if model.vpnSetupExplanationVisible {
            ZStack {
                Color.black.opacity(0.32).ignoresSafeArea()
                VStack(alignment: .leading, spacing: pt(14)) {
                    HStack(alignment: .top, spacing: pt(10)) {
                        SockIcon(holey: false).frame(width: pt(26), height: pt(32.5))
                        Text("iOS will ask to add a VPN configuration. Tap Allow so DobbyVPN can connect.")
                            .dobbyFont(16)
                            .foregroundColor(.ink)
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityIdentifier("vpn.setup.explanation.message")
                    }
                    DialogButton(title: "Continue", filled: true, action: model.continueVPNSetup)
                        .accessibilityIdentifier("vpn.setup.continue")
                }
                .padding(EdgeInsets(top: pt(16), leading: pt(16), bottom: pt(12), trailing: pt(16)))
                .background(Color.surface, in: buttonShape)
                .overlay(buttonShape.stroke(Color.ink, lineWidth: 1.5))
                .frame(maxWidth: 420)
                .padding(.horizontal, pt(24))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("vpn.setup.explanation")
                .accessibilityAddTraits(.isModal)
            }
        }
    }

    private var connections: some View {
        DobbyProfileList(model: model, rowHeight: $rowHeight) { sourceFocused = false }
    }

    private var logs: some View {
        VStack(spacing: 0) {
            Rectangle().fill(Color.ink).frame(height: 1.5)
#if os(iOS)
            if showingLogMenu {
                VStack(spacing: 4) {
                    if model.snapshot.logsUploadAvailable {
                        Button { showingLogMenu = false; model.startLogsUpload() } label: {
                            Text("Upload logs").frame(maxWidth: .infinity).contentShape(Rectangle())
                        }
                            .accessibilityIdentifier("logs.upload")
                            .disabled(!model.uploadStatus.isEmpty)
                    }
                    if let latest = model.latestUpload {
                        Button { showingLogMenu = false; copyUploadReference(latest.reference) } label: {
                            Text(latest.menuTitle()).frame(maxWidth: .infinity).contentShape(Rectangle())
                        }
                            .accessibilityIdentifier("logs.upload.copy-last")
                    }
                    HStack {
                        Button("Clear") { showingLogMenu = false; model.clearLogs() }.accessibilityIdentifier("logs.clear")
                        Spacer()
                        Button("Send logs") { showingLogMenu = false; sendLogs() }.accessibilityIdentifier("logs.share")
                    }
                }.padding().background(Color.surface)
            }
            GeometryReader { geometry in
                VStack(spacing: 0) {
                    // Upload progress and errors can grow with Dynamic Type.
                    // Scroll it within the space left after reserving real log
                    // content, rather than pushing the connection controls off screen.
                    ScrollView {
                        DobbyUploadLines(model: model).background(GeometryReader { content in
                            Color.clear.preference(key: LogActionsHeight.self, value: content.size.height)
                        })
                    }
                    .frame(height: min(logActionsHeight, max(0, geometry.size.height - pt(60))))
                    .accessibilityIdentifier("logs.actions.viewport")
                    .onPreferenceChange(LogActionsHeight.self) { logActionsHeight = $0 }
                    DobbyLogView(entries: model.logEntries, clear: model.clearRevision, onSendLogs: sendLogs,
                                 onShowMenu: { showingLogMenu.toggle() },
                                 onMenuGestureObservation: model.recordLogMenuGesture, onClear: model.clearLogs)
                        .accessibilityIdentifier("Connection logs")
                        .accessibilityFocused($logsFocused)
                        .frame(minHeight: pt(60), maxHeight: .infinity)
                }
            }
            .frame(minHeight: pt(60))
#else
            DobbyUploadLines(model: model)
            DobbyLogView(
                entries: model.logEntries, following: $followingLogs, clear: model.clearRevision,
                interactive: !showingError && !showingUploadResult, onSendLogs: sendLogs,
                onUploadLogs: model.snapshot.logsUploadAvailable ? { model.startLogsUpload() } : nil,
                lastUpload: model.latestUpload, onCopyLastReference: copyUploadReference,
                onClear: { followingLogs = true; model.clearLogs() },
                onReadingPositionRestored: model.recordMacLogRestore,
                onReadingPositionCaptured: model.recordMacLogCapture,
                onAccessibilityObservation: model.recordMacLogAccessibility
            )
            .accessibilityIdentifier("Connection logs")
            .accessibilityFocused($logsFocused)
#endif
        }
        .background(Color.surface.ignoresSafeArea(edges: .bottom))
    }

    /// Waits behind the other dialogs and while the app is in the background or the window is not focused.
    private var showingUploadResult: Bool {
#if os(iOS)
        let foreground = scenePhase == .active
#else
        let foreground = controlActiveState != .inactive
#endif
        return model.uploadResultVisible(foreground: foreground)
    }

    @ViewBuilder
    private var uploadResultDialog: some View {
        if showingUploadResult, let result = model.uploadResult {
            DobbyUploadResultDialog(
                result: result, stacked: sizeCategory.isAccessibilityCategory, titleFocus: $uploadTitleFocused,
                copy: { copyUploadReference(result.reference) }, share: { shareUploadReference(result.shareText) },
                close: model.closeUploadResult
            )
        }
    }

    private func copyUploadReference(_ text: String) {
#if os(iOS)
        UIPasteboard.general.string = text
#else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
#endif
    }

    private func shareUploadReference(_ text: String) {
#if os(iOS)
        model.recordLogsShare(kind: "reference", phase: "requested", context: "scene=\(String(describing: scenePhase)) reference_binding=\(showingReferenceShare) archive_binding=\(showingExport) menu_open=\(showingLogMenu)")
        showingReferenceShare = true
#else
        DobbyMacSharePresenter.present(text: text, reportError: model.reportLogsError)
#endif
    }

    private func sendLogs() {
#if os(iOS)
        model.recordLogsShare(kind: "archive", phase: "requested", context: "scene=\(String(describing: scenePhase)) reference_binding=\(showingReferenceShare) archive_binding=\(showingExport) menu_open=\(showingLogMenu)")
#endif
        model.prepareLogsExport { url in
#if os(macOS)
            DobbyMacSharePresenter.present(url: url, reportError: model.reportLogsError)
#else
            exportedLogsURL = url
            showingExport = true
#endif
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
            GeometryReader { geometry in
              ZStack {
                Color.black.opacity(0.32).ignoresSafeArea().onTapGesture(perform: model.closeFailure)
                ScrollView(.vertical) {
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
                    VStack(alignment: .leading, spacing: pt(6)) {
#if os(iOS)
                        DobbyErrorDetailView(text: errorDisplayText(detail))
                            .frame(height: min(240, max(0, geometry.size.height / 3)))
                            .layoutPriority(-1)
#else
                        ScrollView(.vertical) {
                            Text(errorDisplayText(detail))
                                .dobbyFont(11.5, design: .monospaced)
                                .foregroundColor(.ink)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .fixedSize(horizontal: false, vertical: true)
                                .background(GeometryReader { Color.clear.preference(key: ErrorDetailHeight.self, value: $0.size.height) })
                                .accessibilityIdentifier("error.detail")
                        }
                        .onPreferenceChange(ErrorDetailHeight.self) { errorDetailHeight = $0 }
                        // Sized to the text itself up to a cap; past that it scrolls instead of
                        // reserving a fixed block that is mostly empty for a short error.
                        .frame(height: min(errorDetailHeight, min(240, max(0, geometry.size.height / 3))))
                        .accessibilityIdentifier("error.detail.scroll")
                        .layoutPriority(-1)
#endif
                        Button { copyError(detail, onObservation: model.recordErrorCopy) } label: {
                            Text("Copy").underline().dobbyFont(12, .semibold).foregroundColor(.ink)
                        }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("error.copy")
                    }
                    .padding(.horizontal, pt(10))
                    .padding(.vertical, pt(8))
                    .overlay(RoundedRectangle(cornerRadius: pt(6)).stroke(Color.rule, lineWidth: 1))
                    .padding(.top, pt(10))
                    reconnectLine(identifier: "failure.reconnect", top: pt(10))
                    Group {
                        if sizeCategory.isAccessibilityCategory {
                            VStack(spacing: pt(8)) { failureActions(loadFailed: loadFailed) }
                        } else {
                            HStack(spacing: pt(8)) { failureActions(loadFailed: loadFailed) }
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
#if os(macOS)
                // Center short dialogs while keeping tall content scrollable.
                .frame(maxWidth: .infinity, minHeight: max(0, geometry.size.height - pt(24)), alignment: .center)
#endif
                }
                .frame(maxHeight: max(0, geometry.size.height - pt(24)))
                .accessibilityIdentifier("error.dialog.scroll")
              }
            }
        }
    }

    /// "Reconnecting in 15 s…" counting down, or "Reconnecting…", next to Retry
    /// and Stop while the service reconnects a FAILED connection.
    @ViewBuilder
    private func reconnectLine(identifier: String, top: CGFloat = 0) -> some View {
        if model.reconnectText(now: Date()) != nil {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                Text(model.reconnectText(now: context.date) ?? "")
                    .dobbyFont(13)
                    .foregroundColor(.muted)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier(identifier)
            }
            .padding(.top, top)
        }
    }

    @ViewBuilder
    private func failureActions(loadFailed: Bool) -> some View {
        DialogButton(title: "Close", filled: false, action: model.closeFailure)
            .accessibilityIdentifier("error.close")
        if model.canRetryActiveSelection {
            DialogButton(title: "Retry", filled: true, action: model.retryActiveSelection)
                .accessibilityIdentifier("connection.retry")
        }
        if model.canStop {
            DialogButton(title: "Stop", filled: false, action: model.stop)
                .accessibilityIdentifier("failure.stop")
        }
        if loadFailed {
            DialogButton(title: "Retry", filled: true) { model.retryLoad() }
                .accessibilityIdentifier("subscription.retry")
        }
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

#if os(iOS)
private struct LogActionsHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}
#endif

private struct ErrorDetailHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// Upload progress or failure: one line above the logs in the app's text style.
/// A finished upload opens the result dialog instead, so nothing stays here.
private struct DobbyUploadLines: View {
    @ObservedObject var model: DobbySessionViewModel

    var body: some View {
        VStack(spacing: 0) {
            if !model.uploadError.isEmpty {
                DobbyLogActionLine(text: model.uploadError, textID: "logs.upload.error", action: "Close",
                                   actionID: "logs.upload.error.close", perform: model.dismissUploadError)
            }
            if !model.uploadStatus.isEmpty {
                DobbyLogActionLine(text: model.uploadStatus, textID: nil, action: "Cancel",
                                   actionID: "logs.upload.cancel", perform: model.cancelLogsUpload)
            }
        }
        .frame(maxWidth: .infinity)
    }
}

/// Upload progress or failure above the logs, in the app's text style.
private struct DobbyLogActionLine: View {
    let text: String
    let textID: String?
    let action: String
    let actionID: String
    let perform: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: pt(10)) {
            Text(text)
                .dobbyFont(13)
                .foregroundColor(.ink)
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier(textID ?? "")
            Button(action: perform) {
                Text(action).underline().dobbyFont(12, .semibold).foregroundColor(.ink)
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier(actionID)
        }
        .padding(.horizontal, pt(12))
        .padding(.vertical, pt(8))
        .accessibilityElement(children: .contain)
    }
}

/// Opens by itself when an upload finishes. The upload address is not shown;
/// it is in the logs (`ui.logs_upload`).
private struct DobbyUploadResultDialog: View {
    let result: DobbyUploadReference
    let stacked: Bool
    let titleFocus: AccessibilityFocusState<Bool>.Binding
    let copy: () -> Void
    let share: () -> Void
    let close: () -> Void

    var body: some View {
        ZStack {
            Color.black.opacity(0.32).ignoresSafeArea()
            ScrollView(.vertical) {
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: pt(10)) {
                        SockIcon(holey: false).frame(width: pt(26), height: pt(32.5))
                        Text("Logs uploaded").dobbyFont(18, .semibold).foregroundColor(.ink)
                            .accessibilityAddTraits(.isHeader)
                            .accessibilityIdentifier("logs.upload.title")
                            .accessibilityFocused(titleFocus)
                    }
                    Text("Send this number to support")
                        .dobbyFont(14)
                        .foregroundColor(.muted)
                        .padding(.top, pt(8))
                    Text(result.reference)
                        .dobbyFont(14, design: .monospaced)
                        .foregroundColor(.ink)
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, pt(10))
                        .padding(.vertical, pt(8))
                        .overlay(RoundedRectangle(cornerRadius: pt(6)).stroke(Color.rule, lineWidth: 1))
                        .padding(.top, pt(10))
                        .accessibilityIdentifier("logs.upload.reference")
                    Group {
                        if stacked {
                            VStack(spacing: pt(8)) { actions }
                        } else {
                            HStack(spacing: pt(8)) { actions }
                        }
                    }
                    .padding(.top, pt(14))
                }
                .padding(EdgeInsets(top: pt(16), leading: pt(16), bottom: pt(12), trailing: pt(16)))
                .background(Color.surface, in: buttonShape)
                .overlay(buttonShape.stroke(Color.ink, lineWidth: 1.5))
                .frame(maxWidth: 420)
                .padding(.horizontal, pt(24))
                .padding(.vertical, pt(12))
                .accessibilityElement(children: .contain)
                .accessibilityIdentifier("logs.upload.dialog")
                .accessibilityAddTraits(.isModal)
            }
            .accessibilityIdentifier("logs.upload.dialog.scroll")
        }
    }

    @ViewBuilder
    private var actions: some View {
        DialogButton(title: "Copy", filled: true, action: copy).accessibilityIdentifier("logs.upload.copy")
        DialogButton(title: "Share", filled: false, action: share).accessibilityIdentifier("logs.upload.share")
        DialogButton(title: "Close", filled: false, action: close).accessibilityIdentifier("logs.upload.close")
    }
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
struct SockIcon: View {
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

/// Limit presentation work without changing the original copied diagnostic.
func errorDisplayText(_ raw: String) -> String {
    let scalars = raw.unicodeScalars
    guard scalars.count > 32_768 else { return raw }
    return String(String.UnicodeScalarView(scalars.prefix(32_768))) + "\n[Display truncated; Copy includes the complete error.]"
}

struct ErrorCopyObservation {
    let phase: String
    let scalarCount: Int
    let utf8Count: Int
    let changeCount: Int
    let accepted: Bool?
}

#if os(macOS)
func copyError(_ text: String, to pasteboard: NSPasteboard = .general,
               onObservation: (ErrorCopyObservation) -> Void = { _ in }) {
    let scalarCount = text.unicodeScalars.count
    let utf8Count = text.utf8.count
    onObservation(ErrorCopyObservation(phase: "requested", scalarCount: scalarCount, utf8Count: utf8Count,
                                       changeCount: pasteboard.changeCount, accepted: nil))
    pasteboard.clearContents()
    let accepted = pasteboard.setString(text, forType: .string)
    onObservation(ErrorCopyObservation(phase: "completed", scalarCount: scalarCount, utf8Count: utf8Count,
                                       changeCount: pasteboard.changeCount, accepted: accepted))
}
#elseif os(iOS)
func copyError(_ text: String, onObservation: (ErrorCopyObservation) -> Void = { _ in }) {
    let pasteboard = UIPasteboard.general
    let scalarCount = text.unicodeScalars.count
    let utf8Count = text.utf8.count
    onObservation(ErrorCopyObservation(phase: "requested", scalarCount: scalarCount, utf8Count: utf8Count,
                                       changeCount: pasteboard.changeCount, accepted: nil))
    pasteboard.string = text
    onObservation(ErrorCopyObservation(phase: "completed", scalarCount: scalarCount, utf8Count: utf8Count,
                                       changeCount: pasteboard.changeCount, accepted: nil))
}
#endif

private extension View {
    func importConfirmation(_ model: DobbySessionViewModel) -> some View {
        alert("Replace subscription?", isPresented: Binding(
            get: { model.pendingImport != nil },
            // Both buttons act on their own, and the binding may be reset before the tapped action runs.
            set: { _ in }
        )) {
            Button("Replace") { model.confirmImport() }
            Button("Cancel", role: .cancel) { model.cancelImport() }
        } message: {
            let source = model.pendingImport ?? ""
            Text("\(URLComponents(string: source)?.host ?? "")\n\n\(source)\n\nThe saved subscription and the log upload address will be replaced.")
        }
    }
}
