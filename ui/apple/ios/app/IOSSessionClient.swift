import Foundation
import IOSIntegration

final class IOSSessionClient: DobbyOnDemandClient, @unchecked Sendable {
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
        // One user command is one attempt. The same identity crosses the
        // provider boundary, so app and provider stage records of this command
        // join; the reply's generation joins the Go engine records.
        let attempt = DiagnosticsAttempt.reset()
        let operation = method == "Stop" ? "cancel" : "connect"
        let store = DobbyLogStore(path: IOSAppCompositionRoot.appLogPath())
        let started = ProcessInfo.processInfo.systemUptime
        store.writeStage(operation: operation, stage: "command", role: "ios-app", phase: "begin",
                         attemptID: attempt, fields: ["command": method])
        let reply = shell.call(method: method, parameters: parameters, attemptID: attempt)
        let succeeded = !reply.contains("\"error\"")
        let elapsed = Int(((ProcessInfo.processInfo.systemUptime - started) * 1000).rounded())
        var fields: [String: Any] = ["command": method]
        if let generation = Self.generation(in: reply) { fields["generation"] = generation }
        store.writeStage(operation: operation, stage: "command", role: "ios-app", phase: "end",
                         attemptID: attempt, fields: fields,
                         result: succeeded ? "ok" : "failed", elapsedMs: elapsed)
        return reply
    }

    /// The generation a reply reports, when it carries one.
    static func generation(in reply: String) -> Int64? {
        guard let data = reply.data(using: .utf8),
              let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        if let value = object["generation"] as? Int64 { return value }
        if let value = object["generation"] as? Int { return Int64(value) }
        if let result = object["result"] as? [String: Any] {
            if let value = result["generation"] as? Int64 { return value }
            if let value = result["generation"] as? Int { return Int64(value) }
        }
        return nil
    }

    func onDemandEnabled() async throws -> Bool {
        try await IOSAppCompositionRoot.vpnManager.onDemandEnabled()
    }

    func setOnDemandEnabled(_ enabled: Bool) async throws {
        try await IOSAppCompositionRoot.vpnManager.setOnDemandEnabled(enabled)
    }
}
