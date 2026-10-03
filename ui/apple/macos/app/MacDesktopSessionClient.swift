import DobbyNativeUI
import Foundation

final class MacDesktopSessionClient: DobbySessionClient {
    private let socketPath: String

    init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        if let configured = environment["DOBBYVPN_CONTROL_SOCKET"], !configured.isEmpty {
            socketPath = configured
        } else {
            socketPath = "/var/run/dobbyvpn/control.sock"
        }
    }

    var diagnosticPaths: [URL] {
        [URL(fileURLWithPath: "/Library/Logs/DobbyVPN/backend.jsonl"),
         URL(fileURLWithPath: "/Library/Logs/DobbyVPN/backend.jsonl.stderr"), uiDiagnosticPath]
    }

    var uiDiagnosticPath: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/DobbyVPN/ui_diagnostics.jsonl")
    }

    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }

    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        do {
            let request = try JSONSerialization.data(withJSONObject: ["method": method, "params": parameters])
            var payload = request
            payload.append(0x0A)
            return try UnixControlSocket(path: socketPath).exchange(payload)
        } catch {
            let envelope: [String: Any] = [
                "ok": false,
                "error": [
                    "code": "PLATFORM_FAILED",
                    "message": error.localizedDescription,
                ],
            ]
            guard let data = try? JSONSerialization.data(withJSONObject: envelope),
                  let response = String(data: data, encoding: .utf8) else {
                return #"{"ok":false,"error":{"code":"PLATFORM_FAILED","message":"Platform operation failed"}}"#
            }
            return response
        }
    }

}
