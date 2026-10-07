import SwiftUI
import IOSIntegration

@main
struct NativeDobbyVPNApp: App {
    @StateObject private var model: DobbySessionViewModel

    init() {
#if DOBBY_SIMULATOR_TEST
        _model = StateObject(wrappedValue: DobbySessionViewModel(client: IOSSimulatorTestSessionClient()))
#else
        _model = StateObject(wrappedValue: DobbySessionViewModel(client: IOSSessionClient()))
#endif
    }

    var body: some Scene {
        WindowGroup {
#if DOBBY_SIMULATOR_TEST
            DobbyRootView(
                model: model,
                onLogScrollDiagnostic: SimulatorLogScrollDiagnostics.shared.isEnabled
                    ? { SimulatorLogScrollDiagnostics.shared.append($0) }
                    : nil
            )
                .overlay(alignment: .topLeading) {
                    SimulatorTestSessionStateView(model: model)
                }
                .overlay(alignment: .topTrailing) {
                    if SimulatorLogScrollDiagnostics.shared.isEnabled {
                        SimulatorLogScrollDiagnosticsView()
                    }
                }
#else
            DobbyRootView(model: model)
#endif
        }
    }
}

#if DOBBY_SIMULATOR_TEST
private struct SimulatorTestSessionStateView: View {
    @ObservedObject var model: DobbySessionViewModel

    var body: some View {
        let state = (model.client as? IOSSimulatorTestSessionClient)?.accessibilityState ?? "{}"
        Text("Simulator test session state")
            .accessibilityElement()
            .accessibilityLabel("Simulator test session state")
            .accessibilityIdentifier("Simulator test session state")
            .accessibilityValue(state)
            .frame(width: 1, height: 1)
            .opacity(0.01)
            .accessibilityHidden(false)
    }
}

final class SimulatorLogScrollDiagnostics: ObservableObject {
    static let shared = SimulatorLogScrollDiagnostics()
    static let environmentKey = "DOBBY_IOS_TEST_LOG_SCROLL_TRACE"
    private static let maximumRecords = 96

    let isEnabled: Bool
    @Published private(set) var value = ""
    private var records: [String] = []

    private init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        isEnabled = environment[Self.environmentKey] == "1"
    }

    func append(_ record: String) {
        guard isEnabled else { return }
        records.append(record)
        if records.count > Self.maximumRecords {
            records.removeFirst(records.count - Self.maximumRecords)
        }
        value = records.joined(separator: "\n")
    }
}

private struct SimulatorLogScrollDiagnosticsView: View {
    @ObservedObject private var diagnostics = SimulatorLogScrollDiagnostics.shared

    var body: some View {
        Text("Log scroll diagnostics")
            .accessibilityElement()
            .accessibilityLabel("Log scroll diagnostics")
            .accessibilityIdentifier("Log scroll diagnostics")
            .accessibilityValue(diagnostics.value)
            .frame(width: 1, height: 1)
            .opacity(0.01)
            .accessibilityHidden(false)
    }
}
#endif
