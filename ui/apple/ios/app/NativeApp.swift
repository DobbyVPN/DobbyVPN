import SwiftUI
import UIKit
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
                            .frame(width: 1, height: 1)
                            .opacity(0.01)
                            .accessibilityHidden(false)
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

final class SimulatorLogScrollDiagnostics {
    static let shared = SimulatorLogScrollDiagnostics()
    static let environmentKey = "DOBBY_IOS_TEST_LOG_SCROLL_TRACE"
    private static let maximumRecords = 96

    let isEnabled: Bool
    private(set) var value = ""
    private var records: [String] = []
    private weak var accessibilityView: UILabel?

    private init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        isEnabled = environment[Self.environmentKey] == "1"
    }

    func append(_ record: String) {
        guard isEnabled else { return }
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.records.append(record)
            if self.records.count > Self.maximumRecords {
                self.records.removeFirst(self.records.count - Self.maximumRecords)
            }
            self.value = self.records.joined(separator: "\n")
            self.accessibilityView?.accessibilityValue = self.value
        }
    }

    func attachAccessibilityView(_ view: UILabel) {
        accessibilityView = view
        view.isAccessibilityElement = true
        view.accessibilityLabel = "Log scroll diagnostics"
        view.accessibilityIdentifier = "Log scroll diagnostics"
        view.accessibilityValue = value
        view.isUserInteractionEnabled = false
    }
}

private struct SimulatorLogScrollDiagnosticsView: UIViewRepresentable {
    private let diagnostics = SimulatorLogScrollDiagnostics.shared

    func makeUIView(context: Context) -> UILabel {
        let view = UILabel()
        diagnostics.attachAccessibilityView(view)
        return view
    }

    func updateUIView(_ view: UILabel, context: Context) {
        diagnostics.attachAccessibilityView(view)
    }
}
#endif
