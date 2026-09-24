import SwiftUI

public struct DobbyRootView: View {
    @ObservedObject private var model: DobbySessionViewModel
    @FocusState private var configurationFocused: Bool

    public init(model: DobbySessionViewModel) {
        self.model = model
    }

    public var body: some View {
        TabView {
            connection
                .tabItem { Label("Connection", systemImage: "network") }
            logs
                .tabItem { Label("Logs", systemImage: "doc.text.magnifyingglass") }
            settings
                .tabItem { Label("Settings", systemImage: "gear") }
        }
#if os(macOS)
        .frame(minWidth: 540, minHeight: 560)
#endif
        .sheet(isPresented: $showingExport) { sheetContent }
    }

    private var connection: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("DobbyVPN").font(.largeTitle.bold())
                Text(model.status)
                    .font(.title2.weight(.semibold))
                    .accessibilityIdentifier(model.status)
                if let profile = model.snapshot.activeProfile {
                    Text([profile.protocolName, profile.description]
                        .filter { !$0.isEmpty }
                        .joined(separator: " · "))
                }
                if !model.snapshot.warnings.isEmpty {
                    Text(model.snapshot.warnings.map(\.message).joined(separator: "\n"))
                        .foregroundStyle(.orange)
                }
                if let failure = model.snapshot.lastFailure {
                    Text("\(failure.message) (\(failure.code))")
                        .foregroundStyle(.red)
                }
                if !model.error.isEmpty {
                    Text(model.error).foregroundStyle(.red)
                }
                Text("Connection configuration")
                    .font(.headline)
                TextEditor(text: Binding(
                    get: { model.sourceText },
                    set: { model.sourceChanged($0) }
                ))
                .frame(height: 180)
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(.secondary.opacity(0.4)))
                .accessibilityIdentifier("Connection configuration")
                .focused($configurationFocused)
                .disabled(model.busy)
                Text("Enter an HTTPS connection URL or inline configuration.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                Button(action: model.performPrimaryAction) {
                    if model.busy { ProgressView().frame(maxWidth: .infinity) }
                    else { Text(model.actionTitle).frame(maxWidth: .infinity) }
                }
                .buttonStyle(.borderedProminent)
                .disabled(model.busy)
                .accessibilityIdentifier("VPN connection action")
            }
            .padding(24)
        }
#if os(iOS)
        .toolbar {
            ToolbarItemGroup(placement: .keyboard) {
                Spacer()
                Button("Done") { configurationFocused = false }
                    .accessibilityIdentifier("Dismiss configuration keyboard")
            }
        }
#endif
    }

    private var logs: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Logs").font(.largeTitle.bold())
                Spacer()
                Button("Refresh", action: model.refreshLogs)
                Button("Export") { prepareLogsExport() }
                    .disabled(model.logs.isEmpty)
            }
            if !model.logsError.isEmpty { Text(model.logsError).foregroundStyle(.red) }
            ScrollView { Text(model.logs.isEmpty ? "No logs are available" : model.logs)
                .frame(maxWidth: .infinity, alignment: .leading)
                .textSelection(.enabled)
                .font(.system(.caption, design: .monospaced))
            }
        }
        .padding(20)
        .onAppear(perform: model.refreshLogs)
    }

    private var settings: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Settings").font(.largeTitle.bold())
            Text("Version: \(model.client.version)")
                .accessibilityIdentifier("Settings version metadata")
            Text("Source commit: \(model.client.sourceCommit)")
                .textSelection(.enabled)
                .accessibilityIdentifier("Settings source commit metadata")
            if let url = URL(string: "https://github.com/DobbyVPN/DobbyVPN/tree/\(model.client.sourceCommit)"),
               model.client.sourceCommit.count == 40 {
                Link("Open source", destination: url)
            }
            Spacer()
        }
        .padding(24)
    }

    @State private var exportedLogsURL: URL?
    @State private var showingExport = false

    private func prepareLogsExport() {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("DobbyVPN-logs-\(UUID().uuidString).txt")
        do {
            try Data(model.logs.utf8).write(to: url, options: .atomic)
            exportedLogsURL = url
            showingExport = true
        } catch {
            model.reportLogsError(error.localizedDescription)
        }
    }

    public var sheetContent: some View {
        Group {
            if let exportedLogsURL {
                DobbyShareSheet(url: exportedLogsURL)
            }
        }
    }
}
