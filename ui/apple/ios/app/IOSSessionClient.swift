import Foundation
import IOSIntegration
import DobbyVPNRuntime

final class IOSSessionClient: DobbySessionClient, @unchecked Sendable {
    private let shell = IOSAppCompositionRoot.sessionShell

    var diagnosticPaths: [URL] { IOSAppCompositionRoot.diagnosticPaths() }
    var uiDiagnosticPath: URL { IOSAppCompositionRoot.goLogFilePath() }
    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }
    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        if method == "ParseImportLink" {
            return DobbyvpnParseImportLink(parameters["link"] as? String ?? "")
        }
        return shell.call(method: method, parameters: parameters)
    }
}
