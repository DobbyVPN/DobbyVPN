import SwiftUI
import IOSIntegration
import DobbyVPNRuntime

@main
struct NativeDobbyVPNApp: App {
    private let loggingError: String

    init() {
        loggingError = DobbyvpnInitLoggerError(IOSAppCompositionRoot.goLogFilePath().path)
    }

    var body: some Scene {
        WindowGroup {
            if loggingError.isEmpty {
                NativeAppContent()
            } else {
                Text("Unable to initialize diagnostic logging: \(loggingError)")
                    .accessibilityIdentifier("diagnostics.initialization.error")
            }
        }
    }
}

private struct NativeAppContent: View {
    @StateObject private var model = DobbySessionViewModel(client: IOSSessionClient())
    var body: some View { DobbyRootView(model: model) }
}
