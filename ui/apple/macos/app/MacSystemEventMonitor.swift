import AppKit
import Foundation
import DobbyDiagnosticFiles
import Network

/// Reports system sleep, wake and network-path changes to the Go service so it
/// can run an immediate connected-health check instead of waiting for the next
/// 10 s timer. The service decides whether to reconnect and applies its own
/// automatic-recovery limit; this class only forwards events.
final class MacSystemEventMonitor {
    private let client: MacDesktopSessionClient
    private let queue = DispatchQueue(label: "vpn.dobby.system-events")
    private let pathMonitor = NWPathMonitor()
    private var observers: [NSObjectProtocol] = []
    private var pathEvents = MacPathEvents()
    private var pendingPathEvent: DispatchWorkItem?

    init(client: MacDesktopSessionClient) {
        self.client = client
    }

    func start() {
        let center = NSWorkspace.shared.notificationCenter
        observers.append(center.addObserver(forName: NSWorkspace.willSleepNotification, object: nil, queue: nil) { [weak self] _ in
            self?.send("sleep")
        })
        observers.append(center.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: nil) { [weak self] _ in
            self?.send("wake")
        })
        pathMonitor.pathUpdateHandler = { [weak self] path in self?.pathChanged(path) }
        pathMonitor.start(queue: queue)
    }

    private func pathChanged(_ path: NWPath) {
        // NWPath can change gateways or DNS while interface names and status stay
        // the same. Each subsequent satisfied update can warrant a probe.
        let shouldDispatch = pathEvents.update(satisfied: path.status == .satisfied)
        pendingPathEvent?.cancel()
        guard shouldDispatch else { return }
        // Interfaces often flap several times in a row; coalesce them.
        let work = DispatchWorkItem { [weak self] in self?.send("network_path_changed") }
        pendingPathEvent = work
        queue.asyncAfter(deadline: .now() + 1, execute: work)
    }

    private func send(_ reason: String) {
        queue.async { [client] in
            let response = client.call("NetworkEvent", parameters: ["reason": reason])
            guard let data = response.data(using: .utf8),
                  let envelope = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any],
                  envelope["ok"] as? Bool == true else {
                do {
                    try DiagnosticFiles.append(
                        "Network event dispatch failed", event: "network.event-dispatch-failed",
                        level: "ERROR", source: "native-ui", to: client.uiDiagnosticPath,
                        fields: ["reason": reason, "response": response], legacyDesktop: true
                    )
                } catch {
                    FileHandle.standardError.write(Data("Network event diagnostic write failed: \(error)\n".utf8))
                }
                return
            }
        }
    }

    deinit {
        pathMonitor.cancel()
        observers.forEach { NSWorkspace.shared.notificationCenter.removeObserver($0) }
    }
}

// The monitor invokes this for every NWPath update, even when interface names
// are unchanged. An unsatisfied update also cancels a pending dispatch.
struct MacPathEvents {
    private var initialized = false

    mutating func update(satisfied: Bool) -> Bool {
        guard initialized else {
            initialized = true
            return false
        }
        return satisfied
    }
}
