import Foundation

/// Small native logger shared by the containing app and Packet Tunnel. Go
/// owns the VPN log schema; Swift records only native lifecycle diagnostics.
public final class DobbyLogStore {
    public let path: URL
    private let enabled: Bool
    public init(path: URL, enabled: Bool = true) { self.path = path; self.enabled = enabled }

    /// One lifecycle stage record with the same field vocabulary the Go
    /// stage records use, so a single reader can order records from both
    /// processes. It goes through the shared append path, so the destination is
    /// created when missing, appended under the file lock and its rotation owner
    /// is respected; a failure is reported and returned, never swallowed.
    @discardableResult
    public func writeStage(
        operation: String, stage: String, role: String, phase: String,
        attemptID: String? = nil, fields: [String: Any] = [:],
        result: String? = nil, elapsedMs: Int? = nil
    ) -> Bool {
        guard enabled else { return false }
        // The Go raw-capture monitor owns rotation of this destination.
        return DiagnosticFiles.stage(operation: operation, stage: stage, role: role, phase: phase,
                                    result: result, elapsedMs: elapsedMs, attemptID: attemptID,
                                    extraFields: fields, to: path, rotating: false)
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
