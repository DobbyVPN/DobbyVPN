import Foundation
import IOSIntegration

final class IOSSessionClient: DobbySessionClient, @unchecked Sendable {
    private let shell = IOSAppCompositionRoot.sessionShell

    var diagnosticPaths: [URL] { IOSAppCompositionRoot.diagnosticPaths() }
    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }
    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        shell.call(method: method, parameters: parameters)
    }
}
