import AppKit
import Foundation
import ServiceManagement

/// A system extension request that waits for the user's approval in System
/// Settings. The preference helper (a separate process of this app, run as
/// the same user) records it next to the UI diagnostics; the window reads it.
/// A helper that Stop killed leaves its file behind, so a record only counts
/// while the process that wrote it is alive.
struct MacExtensionApproval {
    let path: URL
    var isAlive: (Int32) -> Bool = { pid in kill(pid, 0) == 0 || errno == EPERM }

    init(uiLogPath: URL) {
        path = uiLogPath.deletingLastPathComponent().appendingPathComponent("extension-approval.json")
    }

    func begin(identifier: String, pid: Int32 = getpid()) {
        do {
            try FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true)
            let record = try JSONSerialization.data(withJSONObject: ["identifier": identifier, "pid": Int(pid)])
            try record.write(to: path, options: .atomic)
        } catch {
            NSLog("DobbyVPN could not record the pending approval: %@", String(reflecting: error))
        }
    }

    func end() {
        try? FileManager.default.removeItem(at: path)
    }

    var pending: Bool {
        guard let data = try? Data(contentsOf: path),
              let record = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let pid = record["pid"] as? Int, pid > 0 else { return false }
        return isAlive(Int32(pid))
    }

    /// Opens the pane where system extensions are allowed: Login Items &
    /// Extensions (`SMAppService.openSystemSettingsLoginItems`, macOS 13 and
    /// later); on macOS 12, Security & Privacy > General.
    static func openSettings() {
        if #available(macOS 13.0, *) {
            SMAppService.openSystemSettingsLoginItems()
        } else if let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?General") {
            NSWorkspace.shared.open(url)
        }
    }
}
