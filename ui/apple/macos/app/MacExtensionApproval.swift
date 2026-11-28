import AppKit
import Darwin
import Foundation
import ServiceManagement

/// A system extension request that waits for the user's approval in System
/// Settings. The preference helper (this app's binary, run by the service as
/// the same user) records it next to the UI diagnostics; the window reads it.
/// The service reads the helper's output only when it exits, so it cannot
/// report this wait itself. A record counts only while the exact process that
/// wrote it still runs: same executable, same start time, same user.
struct MacExtensionApproval {
    static let identifiers: Set<String> = ["vpn.dobby.desktop.Protection", "vpn.dobby.desktop.PacketTunnel"]

    let path: URL

    init(uiLogPath: URL) {
        path = uiLogPath.deletingLastPathComponent().appendingPathComponent("extension-approval.json")
    }

    /// The identity of a running process: its executable and start time.
    struct ProcessIdentity: Equatable {
        let path: String
        let startSeconds: UInt64
        let startMicroseconds: UInt64

        init?(pid: Int32) {
            var info = proc_bsdinfo()
            let size = Int32(MemoryLayout<proc_bsdinfo>.size)
            guard pid > 0, proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, size) == size, info.pbi_uid == getuid() else { return nil }
            var buffer = [CChar](repeating: 0, count: Int(MAXPATHLEN) * 4)
            guard proc_pidpath(pid, &buffer, UInt32(buffer.count)) > 0 else { return nil }
            path = String(cString: buffer)
            startSeconds = info.pbi_start_tvsec
            startMicroseconds = info.pbi_start_tvusec
        }
    }

    func begin(identifier: String) {
        guard let process = ProcessIdentity(pid: getpid()) else { return }
        do {
            try FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true)
            let record: [String: Any] = [
                "identifier": identifier, "pid": Int(getpid()), "path": process.path,
                "start_sec": process.startSeconds, "start_usec": process.startMicroseconds,
            ]
            try JSONSerialization.data(withJSONObject: record).write(to: path, options: .atomic)
        } catch {
            NSLog("DobbyVPN could not record the pending approval: %@", String(reflecting: error))
        }
    }

    /// Removes only this process's own record, so one helper never clears another's.
    func end() {
        if recordedPID() == getpid() { try? FileManager.default.removeItem(at: path) }
    }

    /// True while a request of this app waits for approval. A record that no
    /// longer matches a running helper of this app is removed.
    var pending: Bool {
        guard FileManager.default.fileExists(atPath: path.path) else { return false }
        if valid { return true }
        try? FileManager.default.removeItem(at: path)
        return false
    }

    private var record: [String: Any]? {
        guard let data = try? Data(contentsOf: path) else { return nil }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
    }

    private func recordedPID() -> Int32? {
        guard let value = (record?["pid"] as? NSNumber)?.int64Value, value > 0, value <= Int64(Int32.max) else { return nil }
        return Int32(value)
    }

    private var valid: Bool {
        guard let record, let identifier = record["identifier"] as? String, Self.identifiers.contains(identifier),
              let pid = recordedPID(), let process = ProcessIdentity(pid: pid),
              let own = ProcessIdentity(pid: getpid()), process.path == own.path, process.path == record["path"] as? String,
              (record["start_sec"] as? NSNumber)?.uint64Value == process.startSeconds,
              (record["start_usec"] as? NSNumber)?.uint64Value == process.startMicroseconds else { return false }
        return true
    }

    /// Opens the pane where the extension is allowed. macOS 15 lists system
    /// extensions in Login Items & Extensions (`SMAppService`); macOS 12-14 in
    /// Privacy & Security. If that pane does not open, System Settings itself.
    static func openSettings() {
        if #available(macOS 15.0, *) {
            SMAppService.openSystemSettingsLoginItems()
            return
        }
        if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?General"),
           NSWorkspace.shared.open(url) { return }
        for app in ["/System/Applications/System Settings.app", "/System/Applications/System Preferences.app"]
            where FileManager.default.fileExists(atPath: app) {
            NSWorkspace.shared.open(URL(fileURLWithPath: app))
            return
        }
    }
}
