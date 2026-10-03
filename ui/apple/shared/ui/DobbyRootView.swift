import SwiftUI
#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

public struct DobbyRootView: View {
    @ObservedObject private var model: DobbySessionViewModel
    @Environment(\.scenePhase) private var scenePhase
    @FocusState private var configurationFocused: Bool
    @State private var configurationText = false
    @State private var showingAbout = false
    @State private var followingLogs = true
    @State private var jumpToLatest = 0
    @State private var exportedLogsURL: URL?
    @State private var showingExport = false

    public init(model: DobbySessionViewModel) { self.model = model }

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
                    ToolbarItemGroup(placement: .keyboard) {
                        Spacer()
                        Button("Done") { configurationFocused = false }
                            .accessibilityIdentifier("Dismiss configuration keyboard")
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
                .onAppear { view.model.setLogsVisible(true) }
                .onDisappear { view.model.setLogsVisible(false) }
                .onChange(of: view.model.status) { status in view.announceStatus(status) }
                .onChange(of: view.scenePhase) { phase in view.model.setLogsVisible(phase == .active) }
                .onChange(of: view.model.sourceText) { source in
                    if source.contains("\n") || source.trimmingCharacters(in: .whitespaces).hasPrefix("[") {
                        view.configurationText = true
                    }
                }
                .sheet(isPresented: view.$showingAbout) {
                    DobbyAboutView(model: view.model)
                }
                .sheet(isPresented: view.$showingExport, onDismiss: view.removeExport) {
                    if let url = view.exportedLogsURL { DobbyShareSheet(url: url) }
                }
        }
    }

    private var content: some View {
#if os(macOS)
        VSplitView {
            ScrollView {
                connection.padding(20).frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(minHeight: configurationText ? 260 : 160)
            logs.padding(20).frame(minHeight: 140)
        }
#else
        GeometryReader { geometry in
            VStack(spacing: 12) {
                ScrollView { connection.padding(.horizontal, 16).padding(.top, 12) }
                    .frame(maxHeight: geometry.size.height * 0.55)
                logs.padding(.horizontal, 16).padding(.bottom, 8)
            }
        }
#endif
    }

    private var connection: some View {
        VStack(alignment: .leading, spacing: 10) {
            configurationEditor
#if os(macOS)
            HStack(spacing: 20) {
                connectionSummary
                Spacer()
                primaryAction
            }
#else
            connectionSummary
            primaryAction.frame(maxWidth: .infinity)
#endif
        }
    }

    @ViewBuilder
    private var configurationEditor: some View {
        Text(configurationText ? "Configuration text" : "Subscription URL").font(.headline)
        if configurationText {
            TextEditor(text: sourceBinding)
                .frame(height: 100)
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(.secondary.opacity(0.4)))
                .accessibilityIdentifier("Connection configuration")
                .accessibilityLabel("Configuration text")
                .focused($configurationFocused)
                .disabled(model.busy)
        } else {
            TextField("https://…", text: sourceBinding)
                .textFieldStyle(.roundedBorder)
                .accessibilityIdentifier("Connection configuration")
                .accessibilityLabel("Subscription URL")
                .focused($configurationFocused)
                .disabled(model.busy)
#if os(iOS)
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.done)
#endif
        }
        Button(configurationText ? "Use subscription URL" : "Use configuration text…") {
            configurationText.toggle()
        }
        .buttonStyle(.plain)
        .foregroundStyle(.tint)
        .font(.subheadline)
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
            if !model.error.isEmpty {
                Text(model.snapshot.sessionID.isEmpty
                     ? "VPN service is unavailable. See logs for details."
                     : "Check your subscription URL or configuration. See logs for details.")
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
                Text(model.actionTitle)
            }
#if os(iOS)
            .frame(maxWidth: .infinity, minHeight: 30)
#endif
        }
        .buttonStyle(.borderedProminent)
        .disabled(model.busy || !model.canPerformPrimaryAction)
        .accessibilityIdentifier("VPN connection action")
        .keyboardShortcut(.return, modifiers: .command)
    }

    private var logs: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Logs").font(.headline)
                Spacer()
                Button("Jump to latest") { followingLogs = true; jumpToLatest += 1 }
                    .disabled(followingLogs)
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
            Text("Recent logs. Shared diagnostics include the complete files.")
                .font(.caption).foregroundStyle(.secondary)
            DobbyLogView(text: model.logs, following: $followingLogs, jump: jumpToLatest)
                .accessibilityIdentifier("Connection logs")
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

public struct DobbyAboutView: View {
    @ObservedObject var model: DobbySessionViewModel
    @Environment(\.dismiss) private var dismiss

    public init(model: DobbySessionViewModel) { self.model = model }

    public var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("About DobbyVPN").font(.title2.bold())
            Text("Version: \(model.client.version)").accessibilityIdentifier("About version metadata")
            Text("Source commit: \(model.client.sourceCommit)")
                .textSelection(.enabled).accessibilityIdentifier("About source commit metadata")
            if model.client.sourceCommit.count == 40,
               let url = URL(string: "https://github.com/DobbyVPN/DobbyVPN/tree/\(model.client.sourceCommit)") {
                Link("Source code", destination: url)
            }
            Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
        }
        .padding(24)
#if os(macOS)
        .frame(width: 400)
#endif
    }
}
