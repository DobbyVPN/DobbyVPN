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
            DobbyRootView(model: model)
                .overlay(alignment: .topLeading) {
                    SimulatorTestSessionStateView(model: model)
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
#endif
