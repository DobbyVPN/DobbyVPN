import AppKit
import Foundation
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
    private var lastPathSignature: String?
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
        let interfaces = path.availableInterfaces.map(\.name).sorted().joined(separator: ",")
        let signature = "\(path.status)-\(interfaces)"
        defer { lastPathSignature = signature }
        // The first callback only reports the initial state.
        guard let previous = lastPathSignature, previous != signature else { return }
        pendingPathEvent?.cancel()
        guard path.status == .satisfied else { return }
        // Interfaces often flap several times in a row; coalesce them.
        let work = DispatchWorkItem { [weak self] in self?.send("network_path_changed") }
        pendingPathEvent = work
        queue.asyncAfter(deadline: .now() + 1, execute: work)
    }

    private func send(_ reason: String) {
        queue.async { [client] in
            _ = client.call("NetworkEvent", parameters: ["reason": reason])
        }
    }

    deinit {
        pathMonitor.cancel()
        observers.forEach { NSWorkspace.shared.notificationCenter.removeObserver($0) }
    }
}
