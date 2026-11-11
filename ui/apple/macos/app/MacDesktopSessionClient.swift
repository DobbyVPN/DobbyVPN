import DobbyDiagnosticFiles
import DobbyNativeUI
import Foundation

final class MacDesktopSessionClient: DobbySessionClient {
    private let socketPath: String
    private let backendLogPath: String
    private let uiLogPath: String
    private let clock: () -> TimeInterval
    private let samplingLock = NSLock()
    private var lastSnapshot: TimeInterval?

    init(environment: [String: String] = ProcessInfo.processInfo.environment, clock: @escaping () -> TimeInterval = { ProcessInfo.processInfo.systemUptime }) {
        self.clock = clock
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
        [URL(fileURLWithPath: backendLogPath),
         uiDiagnosticPath]
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

    private func sample(_ method: String) -> Bool {
        guard method == "Snapshot" else { return true }
        samplingLock.lock(); defer { samplingLock.unlock() }
        let now = clock()
        if let lastSnapshot, now >= lastSnapshot, now - lastSnapshot < 30 { return false }
        lastSnapshot = now
        return true
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        let requestID = UUID().uuidString.lowercased()
        let sampled = sample(method)
        let started = clock()
        var identity: [String: Any] = [:]
        for key in ["session_id", "generation", "expected_sequence"] {
            if let value = parameters[key], value is String || value is NSNumber { identity[key] = value }
        }
        func record(_ event: String, _ level: String, _ message: String, error: String? = nil) {
            var fields: [String: Any] = ["request_id": requestID, "method": method, "identity": identity,
                                         "elapsed_ms": (clock() - started) * 1000]
            if let error { fields["error"] = error }
            do {
                try DiagnosticFiles.append(message, event: event, level: level, source: "native-ui",
                                           to: uiDiagnosticPath, fields: fields, legacyDesktop: true,
                                           legacyExcludedPaths: [backendLogPath])
            } catch {
                FileHandle.standardError.write(Data("UI control diagnostic write failed: \(String(reflecting: error))\n".utf8))
            }
        }
        if sampled { record("control.command.begin", "INFO", "Started desktop control command") }
        let response: String
        do {
            let request = try JSONSerialization.data(withJSONObject: ["method": method, "params": parameters,
                                                                       "request_id": requestID, "diagnostic_sampled": sampled])
            var payload = request
            payload.append(0x0A)
            response = try UnixControlSocket(path: socketPath).exchange(payload)
        } catch {
            record("control.command.failure", "ERROR", "Desktop control exchange failed", error: String(reflecting: error))
            let envelope: [String: Any] = ["ok": false, "error": ["code": "PLATFORM_FAILED", "message": error.localizedDescription]]
            guard let data = try? JSONSerialization.data(withJSONObject: envelope),
                  let response = String(data: data, encoding: .utf8) else {
                return #"{"ok":false,"error":{"code":"PLATFORM_FAILED","message":"Platform operation failed"}}"#
            }
            self.recordConfigureResponse(method, parameters: parameters, response: response, responseReceived: false)
            return response
        }
        do {
            guard let data = response.data(using: .utf8),
                  let object = try JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let ok = object["ok"] as? Bool else {
                throw NSError(domain: "DobbyDesktopControl", code: 1, userInfo: [NSLocalizedDescriptionKey: "Desktop control response envelope is invalid"])
            }
            if !ok {
                record("control.command.failure", "ERROR", "Desktop control command failed", error: String(reflecting: object["error"] ?? "unavailable"))
            } else if sampled {
                record("control.command.complete", "INFO", "Completed desktop control command")
            }
        } catch {
            record("control.command.failure", "ERROR", "Desktop control response inspection failed", error: String(reflecting: error))
        }
        recordConfigureResponse(method, parameters: parameters, response: response, responseReceived: true)
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
                    "parameters": ["source": source],
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
