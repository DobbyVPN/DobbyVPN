import Foundation
import IOSIntegration
import DobbyVPNRuntime

final class IOSSessionClient: DobbyDisconnectObserving, @unchecked Sendable {
    private let shell = IOSAppCompositionRoot.sessionShell

    private let history = IOSAppCompositionRoot.vpnManager.disconnectHistory
    var disconnectFailure: String { history.message }
    func beginSnapshotObservation() -> UInt64 { history.begin("Snapshot") }
    func observedSnapshot(_ snapshot: DobbySessionSnapshot, ticket: UInt64) {
        history.observedSnapshot(ticket, state: snapshot.state, recovering: snapshot.recovering)
    }
    func acknowledgeDisconnect(_ message: String) { history.acknowledge(message) }

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
        let ticket = history.begin(method)
        let response = shell.call(method: method, parameters: parameters)
        if method == "Start", (try? DobbyResponse.check(response)) != nil { history.acceptedStart(ticket) }
        return response
    }
}
