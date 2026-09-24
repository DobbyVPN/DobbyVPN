import Foundation
import CommonDI

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
        let sessionID = parameters["session_id"] as? String ?? ""
        switch method {
        case "Snapshot":
            return shell.snapshot(sessionID: sessionID)
        case "Configure":
            let sequence = (parameters["expected_sequence"] as? NSNumber)?.int64Value ?? 0
            let source = parameters["source"] as? String ?? ""
            return shell.configure(
                sessionID: sessionID,
                expectedSequence: sequence,
                rawConfig: Data(source.utf8)
            )
        case "Start":
            let sequence = (parameters["expected_sequence"] as? NSNumber)?.int64Value ?? 0
            let mode = parameters["mode"] as? String ?? "AUTO_SELECT"
            let index = (parameters["index"] as? NSNumber)?.int32Value ?? 0
            return shell.start(sessionID: sessionID, expectedSequence: sequence, mode: mode, index: index)
        case "Stop":
            let generation = (parameters["generation"] as? NSNumber)?.int64Value ?? 0
            return shell.stop(sessionID: sessionID, generation: generation)
        default:
            return "{\"ok\":false,\"error\":{\"code\":\"INVALID_ARGUMENT\",\"message\":\"unknown command\"}}"
        }
    }
}
