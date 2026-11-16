import Foundation
#if canImport(DobbyDiagnosticFiles)
import DobbyDiagnosticFiles
#endif

/// Small native logger shared by the containing app and Packet Tunnel. Go
/// owns the VPN log schema; Swift records only native lifecycle diagnostics.
public final class DobbyLogStore {
    public let path: URL
    private let enabled: Bool
    public init(path: URL, enabled: Bool = true) { self.path = path; self.enabled = enabled }

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
