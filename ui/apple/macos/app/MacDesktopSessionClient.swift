import CryptoKit
import DobbyDiagnosticFiles
import DobbyNativeUI
import Foundation

final class MacDesktopSessionClient: DobbySessionClient {
    private let socketPath: String
    private let backendLogPath: String
    private let uiLogPath: String

    init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        backendLogPath = environment["DOBBY_LOG_PATH"].flatMap { $0.isEmpty ? nil : $0 } ?? "/Library/Logs/DobbyVPN/backend.jsonl"
        uiLogPath = environment["DOBBY_UI_LOG_PATH"].flatMap { $0.isEmpty ? nil : $0 }
            ?? FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Library/Logs/DobbyVPN/ui_diagnostics.jsonl").path
        if let configured = environment["DOBBYVPN_CONTROL_SOCKET"], !configured.isEmpty {
            socketPath = configured
        } else {
            socketPath = "/var/run/dobbyvpn/control.sock"
        }
    }

    var diagnosticPaths: [URL] {
        DiagnosticFiles.diagnosticExtras(directory: URL(fileURLWithPath: backendLogPath).deletingLastPathComponent())
            + [URL(fileURLWithPath: backendLogPath), uiDiagnosticPath]
    }

    var uiDiagnosticPath: URL {
        URL(fileURLWithPath: uiLogPath)
    }

    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }

    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    /// One-way digest of a configuration input for diagnostics.
    static func digest(_ value: String) -> String {
        let hash = SHA256.hash(data: Data(value.utf8))
        return hash.map { String(format: "%02x", $0) }.joined()
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        var responseReceived = false
        let response: String
        do {
            let request = try JSONSerialization.data(withJSONObject: ["method": method, "params": parameters])
            var payload = request
            payload.append(0x0A)
            response = try UnixControlSocket(path: socketPath).exchange(payload)
            responseReceived = true
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
            self.recordConfigureResponse(method, parameters: parameters, response: response, responseReceived: false)
            return response
        }
        recordConfigureResponse(method, parameters: parameters, response: response, responseReceived: responseReceived)
        return response
    }

    private func recordConfigureResponse(
        _ method: String, parameters: [String: Any], response: String, responseReceived: Bool
    ) {
        guard method == "Configure", let source = parameters["source"] as? String else { return }
        let responseIsSuccessful: Bool
        if responseReceived, let data = response.data(using: .utf8),
           let rawEnvelope = try? JSONSerialization.jsonObject(with: data),
           let envelope = rawEnvelope as? [String: Any] {
            responseIsSuccessful = envelope["ok"] as? Bool == true
        } else {
            responseIsSuccessful = false
        }
        do {
            try DiagnosticFiles.append(
                "Read the Configure response from the Go backend",
                event: "subscription.configure-pipe-response-read",
                level: "INFO",
                source: "native-ui",
                to: uiDiagnosticPath,
                fields: [
                    // A subscription URL is a credential-bearing configuration
                    // input, so the diagnostic keeps only its digest and length.
                    "source_digest": Self.digest(source),
                    "source_length": source.count,
                    "succeeded": responseReceived && responseIsSuccessful,
                    "response_read_monotonic_uptime": ProcessInfo.processInfo.systemUptime,
                ], legacyDesktop: true, legacyExcludedPaths: [backendLogPath]
            )
        } catch {
            FileHandle.standardError.write(Data(
                "UI Configure response diagnostic write failed: \(String(reflecting: error))\n".utf8
            ))
        }
    }

}
