import Foundation
import IOSIntegration
import DobbyVPNRuntime

final class IOSSessionClient: DobbyDisconnectObserving, DobbyVPNSetupProviding, @unchecked Sendable {
    private let shell = IOSAppCompositionRoot.sessionShell

    private let history = IOSAppCompositionRoot.vpnManager.disconnectHistory
    var disconnectFailure: String { history.message }
    func beginSnapshotObservation() -> UInt64 { history.begin("Snapshot") }
    func observedSnapshot(_ snapshot: DobbySessionSnapshot, ticket: UInt64) {
        history.observedSnapshot(ticket, state: snapshot.state, recovering: snapshot.recovering)
        let path = IOSAppCompositionRoot.sharedLogPath("provider-run.json")
        if let previous = (try? IOSProviderRunRecord.load(from: path))?.unexpectedPreviousLaunch {
            history.observedReplacement(of: previous)
        }
    }
    func acknowledgeDisconnect(_ message: String) { history.acknowledge(message) }

    func setUpVPNSnapshot() -> String {
        shell.call(method: "Snapshot", parameters: ["session_id": ""], setUpVPN: true)
    }

    var diagnosticPaths: [URL] { IOSAppCompositionRoot.diagnosticPaths() }
    var uiDiagnosticPath: URL { IOSAppCompositionRoot.goLogFilePath() }
    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }
    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    func flushDiagnostics() throws {
        do {
            try DobbyResponse.check(shell.call(method: "FlushDiagnostics", parameters: [:]))
        } catch DobbyClientError.command(code: "PROVIDER_INACTIVE", message: _) {
            // Existing diagnostic files are available without starting the provider.
        }
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
