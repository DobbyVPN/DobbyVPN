import Foundation

/// Small native logger shared by the containing app and Packet Tunnel. Go
/// owns the VPN log schema; Swift records only native lifecycle diagnostics.
public final class DobbyLogStore {
    public let path: URL
    private let enabled: Bool
    public init(path: URL, enabled: Bool = true) { self.path = path; self.enabled = enabled }

    /// One lifecycle stage record with the same field vocabulary the Go
    /// timeline uses, so a single reader can order records from both processes.
    @discardableResult
    public func writeStage(
        operation: String, stage: String, role: String, phase: String,
        attemptID: String? = nil, fields: [String: Any] = [:],
        result: String? = nil, elapsedMs: Int? = nil
    ) -> Bool {
        guard enabled else { return false }
        var record: [String: Any] = [
            "event": "lifecycle.stage",
            "level": "DEBUG",
            "message": "stage \(operation) \(stage)",
            "role": role,
            "operation": operation,
            "stage": stage,
            "phase": phase,
            "attempt_id": attemptID ?? "",
            "timestamp": ISO8601DateFormatter().string(from: Date()),
        ]
        if let result, !result.isEmpty { record["result"] = result }
        if let elapsedMs { record["elapsed_ms"] = elapsedMs }
        for (key, value) in fields { record[key] = value }
        guard JSONSerialization.isValidJSONObject(record),
              let data = try? JSONSerialization.data(withJSONObject: record, options: [.sortedKeys]),
              let line = String(data: data, encoding: .utf8) else { return false }
        // Write through the same single-record append path the Go writer uses.
        do {
            let handle = try FileHandle(forWritingTo: path)
            defer { try? handle.close() }
            try handle.seekToEnd()
            try handle.write(contentsOf: Data((line + "\n").utf8))
            return true
        } catch {
            try? FileHandle.standardError.write(contentsOf: Data("stage record write failed path=\(path.path): \(String(reflecting: error))\n".utf8))
            return false
        }
    }

    @discardableResult
    public func writeLog(level: String = "INFO", log: String) -> Bool {
        guard enabled else {
            let message = "Native diagnostic unavailable before Go logger initialization. Original record: \(log)\n"
            do { try FileHandle.standardError.write(contentsOf: Data(message.utf8)) }
            catch { return false }
            return false
        }
        do {
            let source = Bundle.main.bundleIdentifier?.hasSuffix(".tunnel") == true ? "ios-tunnel" : "ios-app"
            try DiagnosticFiles.append(log, event: "native.lifecycle", level: level, source: source, to: path, rotating: false)
            return true
        } catch {
            let message = "Native diagnostic write failed path=\(path.path): \(String(reflecting: error))\nOriginal record: \(log)\n"
            do { try FileHandle.standardError.write(contentsOf: Data(message.utf8)) }
            catch { /* The caller still receives the original write failure. */ }
            return false
        }
    }
}
