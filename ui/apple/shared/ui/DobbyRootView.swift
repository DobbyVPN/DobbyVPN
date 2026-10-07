import SwiftUI
#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

public struct DobbyRootView: View {
    @ObservedObject private var model: DobbySessionViewModel
    private let onLogScrollDiagnostic: ((String) -> Void)?
    @Environment(\.scenePhase) private var scenePhase
    @FocusState private var configurationFocused: Bool
    @State private var showingAbout = false
    @State private var controlsHeight: CGFloat = 0
    @State private var canPaste = false
    @State private var followingLogs = true
    @State private var exportedLogsURL: URL?
    @State private var showingExport = false

    public init(
        model: DobbySessionViewModel,
        onLogScrollDiagnostic: ((String) -> Void)? = nil
    ) {
        self.model = model
        self.onLogScrollDiagnostic = onLogScrollDiagnostic
    }

    public var body: some View {
#if os(iOS)
        NavigationView {
            content
                .navigationTitle("DobbyVPN")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .navigationBarTrailing) {
                        Button("About") { showingAbout = true }
                    }
                    ToolbarItem(placement: .navigationBarLeading) {
                        if configurationFocused {
                            Button("Done") { configurationFocused = false }
                                .accessibilityIdentifier("Dismiss configuration keyboard")
                        }
                    }
                }
        }
        .navigationViewStyle(.stack)
        .modifier(Lifecycle(view: self))
#else
        content
            .frame(minWidth: 560, minHeight: 460)
            .toolbar {
                Button("About") { showingAbout = true }
                    .accessibilityIdentifier("About")
            }
            .modifier(Lifecycle(view: self))
#endif
    }

    private struct Lifecycle: ViewModifier {
        let view: DobbyRootView
        func body(content: Content) -> some View {
            content
                .onAppear { view.model.setLogsVisible(true); view.refreshClipboard() }
                .onDisappear { view.model.setLogsVisible(false) }
                .onChange(of: view.model.status) { status in view.announceStatus(status) }
                .onChange(of: view.scenePhase) { phase in view.model.setLogsVisible(phase == .active); view.refreshClipboard() }
#if os(macOS)
                .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                    view.refreshClipboard()
                }
#endif
                .onOpenURL { view.model.importLink($0) }
                .sheet(isPresented: view.$showingAbout) {
                    DobbyAboutView(model: view.model)
                }
                .sheet(isPresented: view.$showingExport, onDismiss: view.removeExport) {
                    if let url = view.exportedLogsURL { DobbyShareSheet(url: url) }
                }
        }
    }

    private var controlsFraction: CGFloat {
#if os(iOS)
        0.4
#else
        0.65
#endif
    }

    private var content: some View {
        GeometryReader { geometry in
            VStack(spacing: 12) {
                ScrollView {
                    VStack(alignment: .leading, spacing: 10) {
                        configurationEditor
                        connectionSummary
                        primaryAction
                        ScrollView {
                            LazyVStack(alignment: .leading, spacing: 8) {
                                ForEach(model.snapshot.profiles) { profile in
                                    HStack {
                                        VStack(alignment: .leading) {
                                            Text(profile.name)
                                            Text(profile.protocolName)
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                                .accessibilityIdentifier("Profile \(profile.index + 1) protocol")
                                                .accessibilityLabel("Profile \(profile.index + 1) protocol · \(profile.protocolName)")
                                        }
                                        Spacer()
                                        Button(model.actionTitle(profile.index)) { model.performPrimaryAction(profile.index) }
                                            .disabled(!model.canAct(profile.index))
                                            .accessibilityIdentifier("Profile \(profile.index + 1) action")
                                    }
                                }
                            }
                        }
                        .frame(maxHeight: min(180, geometry.size.height * 0.25))
                        .fixedSize(horizontal: false, vertical: true)
                    }
                    .background(GeometryReader { size in
                        Color.clear.preference(key: ControlsHeight.self, value: size.size.height)
                    })
                }
                .frame(height: min(controlsHeight, geometry.size.height * controlsFraction))
                .accessibilityIdentifier("Connection controls")
                .onPreferenceChange(ControlsHeight.self) { controlsHeight = $0 }
                logs.frame(maxHeight: .infinity)
            }
            .padding(16)
        }
    }

    private var configurationEditor: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Subscription URL").font(.headline)
            TextField("https://…", text: sourceBinding)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("Connection configuration")
                .accessibilityLabel("Subscription URL")
                .focused($configurationFocused)
#if os(iOS)
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.done)
#endif
            pasteControl
            if model.loading { ProgressView("Loading profiles…") }
            if !model.loadError.isEmpty {
                Text(model.loadError).font(.caption).foregroundStyle(.red).textSelection(.enabled)
                Button("Retry") { model.retryLoad() }
            }
        }
    }

    @ViewBuilder
    private var pasteControl: some View {
#if os(iOS)
        if #available(iOS 16.0, *) {
            PasteButton(payloadType: String.self) { values in
                if let value = values.first { model.paste(value) }
            }
        } else if canPaste {
            Button("Paste") { if let value = UIPasteboard.general.string { model.paste(value) } }
        }
#elseif os(macOS)
        if canPaste {
            Button("Paste") { if let value = NSPasteboard.general.string(forType: .string) { model.paste(value) } }
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

    private var connectionSummary: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(model.status).font(.headline).accessibilityIdentifier(model.status)
            if let profile = model.snapshot.activeProfile {
                Text([profile.protocolName, profile.description].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.subheadline)
            }
            if model.snapshot.primaryAction == "STOP" && !model.isStopTarget(nil) && !model.snapshot.profiles.contains(where: { model.isStopTarget($0.index) }) {
                Button(model.snapshot.state == "CONNECTED" ? "Disconnect" : "Stop") { model.stop() }
                    .disabled(model.busy)
            }
            if !model.importError.isEmpty {
                Text(model.importError)
                    .font(.subheadline).foregroundStyle(.red)
                    .accessibilityIdentifier("Deep link import guidance")
            }
            if !model.error.isEmpty && model.error != model.importError {
                Text(model.error)
                    .font(.subheadline).foregroundStyle(.red)
            } else if model.snapshot.lastFailure != nil {
                Text("Connection failed. See logs for details.").font(.subheadline).foregroundStyle(.red)
            }
        }
    }

    private var primaryAction: some View {
        Button {
            configurationFocused = false
            model.performPrimaryAction()
        } label: {
            HStack {
                if model.busy || ["PROBING", "PREPARING", "STOPPING"].contains(model.snapshot.state) {
                    ProgressView().controlSize(.small)
                }
                Text(model.actionTitle())
            }
#if os(iOS)
            .frame(minHeight: 30)
#endif
        }
        .buttonStyle(.borderedProminent)
        .disabled(!model.canAct())
        .accessibilityIdentifier("VPN connection action")
        .keyboardShortcut(.return, modifiers: .command)
    }

    private var logs: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Logs").font(.headline)
                Spacer()
                Button("Clear") {
                    followingLogs = true
                    model.clearLogs()
                }
                Button {
                    model.prepareLogsExport { url in
                        exportedLogsURL = url
                        showingExport = true
                    }
                } label: { Label("Share logs", systemImage: "square.and.arrow.up") }
                .labelStyle(.iconOnly)
                .accessibilityLabel("Share logs")
                .disabled(model.exportingLogs || showingExport)
            }
            if !model.logsError.isEmpty {
                Text("Some diagnostics could not be read or shared. Details are included in the logs.")
                    .font(.caption).foregroundStyle(.red)
            }
            Text("Recent logs. Shared diagnostics include both retained generations.")
                .font(.caption).foregroundStyle(.secondary)
#if os(iOS)
            DobbyLogView(
                entries: model.logEntries,
                clear: model.clearRevision,
                onScrollDiagnostic: onLogScrollDiagnostic
            )
                .accessibilityIdentifier("Connection logs")
#else
            DobbyLogView(entries: model.logEntries, following: $followingLogs, clear: model.clearRevision)
                .accessibilityIdentifier("Connection logs")
#endif
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

    private func removeExport() {
        guard let url = exportedLogsURL else { return }
        do { try FileManager.default.removeItem(at: url) } catch { model.reportLogsError(error.localizedDescription) }
        exportedLogsURL = nil
    }
}

private struct ControlsHeight: PreferenceKey {
    static var defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

public struct DobbyAboutView: View {
    @ObservedObject var model: DobbySessionViewModel
    @Environment(\.dismiss) private var dismiss

    public init(model: DobbySessionViewModel) { self.model = model }

    public var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("About DobbyVPN").font(.title2.bold())
            Text("Version: \(model.client.version)").accessibilityIdentifier("About version metadata")
            Text("Commit: \(model.client.sourceCommit.prefix(12))")
            Text("Source commit: \(model.client.sourceCommit)")
                .textSelection(.enabled).accessibilityIdentifier("About source commit metadata")
            if model.client.sourceCommit.count == 40,
               let url = URL(string: "https://github.com/DobbyVPN/DobbyVPN/tree/\(model.client.sourceCommit)") {
                Link("Source code", destination: url)
                    .accessibilityIdentifier("About source link")
                    .accessibilityValue(url.absoluteString)
            }
            Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
        }
        .padding(24)
#if os(macOS)
        .frame(width: 400)
#endif
    }
}
