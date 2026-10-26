import Foundation
import IOSIntegration

final class IOSSessionClient: DobbyOnDemandClient, @unchecked Sendable {
    private let shell = IOSAppCompositionRoot.sessionShell

    var diagnosticPaths: [URL] { IOSAppCompositionRoot.diagnosticPaths() }
    var uiDiagnosticPath: URL { IOSAppCompositionRoot.sharedLogPath("ui_diagnostics.jsonl") }
    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }
    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        shell.call(method: method, parameters: parameters)
    }

    func onDemandEnabled() async throws -> Bool {
        try await IOSAppCompositionRoot.vpnManager.onDemandEnabled()
    }

    func setOnDemandEnabled(_ enabled: Bool) async throws {
        try await IOSAppCompositionRoot.vpnManager.setOnDemandEnabled(enabled)
    }
}
