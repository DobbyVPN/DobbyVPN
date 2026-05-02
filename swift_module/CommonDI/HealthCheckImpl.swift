import NetworkExtension
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

private final class HttpMetricsDelegate: NSObject, URLSessionDataDelegate {
    private let log: (String) -> Void
    private let url: String
    // Winning proto of the request (set before completion handler fires, safe to read after semaphore.wait)
    var winnerProto: String = "?"

    init(url: String, log: @escaping (String) -> Void) {
        self.url = url
        self.log = log
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        didFinishCollecting metrics: URLSessionTaskMetrics
    ) {
        for (i, t) in metrics.transactionMetrics.enumerated() {
            func ms(_ a: Date?, _ b: Date?) -> String {
                guard let a, let b else { return "-" }
                return "\(Int(b.timeIntervalSince(a) * 1000))ms"
            }
            // responseEndDate != nil → this transaction actually delivered the response (won the h2 vs h3 race)
            // responseEndDate == nil → transaction was aborted (lost the race or timed out)
            let won = t.responseEndDate != nil
            let proto = t.networkProtocolName ?? "?"
            if won {
                winnerProto = proto
                log("[HC] [httpPing] proto=\(proto) local=\(t.localAddress ?? "-")")
            }
            // localAddress shows which interface was used:
            // 198.18.0.1 = through VPN tunnel (good)
            // anything else = traffic bypassed the tunnel (bad)
            let local = t.localAddress ?? "-"
            let remote = "\(t.remoteAddress ?? "-"):\(t.remotePort.map { "\($0)" } ?? "-")"
            log(
                "[HC] [metrics#\(i)\(won ? "[win]" : "[drop]")] proto=\(proto) reused=\(t.isReusedConnection)" +
                " local=\(local) remote=\(remote)" +
                " dns=\(ms(t.domainLookupStartDate, t.domainLookupEndDate))" +
                " tcp=\(ms(t.connectStartDate, t.connectEndDate))" +
                " tls=\(ms(t.secureConnectionStartDate, t.secureConnectionEndDate))" +
                " ttfb=\(ms(t.requestEndDate, t.responseStartDate))" +
                " total=\(ms(t.fetchStartDate, t.responseEndDate))"
            )
        }
    }
}

public final class HealthCheckImpl: HealthCheck {

    public static let shared = HealthCheckImpl()

    private let logs = NativeModuleHolder.logsRepository
    private let timeout: TimeInterval = 8.0
    private let tunnelIPAddress = "198.18.0.1"

    public private(set) var currentMemmoryUsageMb = 0.0
    private var lastMemoryMB: Double = 0
    private var lastOutlineProxyOk = false
    private var checkSequence: Int = 0
    private let checkSequenceLock = NSLock()

    public func shortConnectionCheckUp() -> Bool {
        let checkId = nextCheckId(prefix: "short")
        logs.writeLog(log: "[HC] Start shortConnectionCheckUp id=\(checkId)")

        let checks: [(String, () -> Bool)] = [
            ("HTTP https://google.com/gen_204", {
                self.httpPing(urlString: "https://google.com/gen_204")
            }),
            ("HTTP https://one.one.one.one", {
                self.httpPing(urlString: "https://one.one.one.one")
            })
        ]

        let networkOk = checks.contains { name, check in
            self.runWithRetry(name: name, block: check)
        }

        let vpnOk = runWithRetry(name: "Dobby Tunnel IP Check", attempts: 1) {
            self.isVPNInterfaceExists()
        }

        let heartbeatOk = runWithRetry(name: "XPC heartbeat check", attempts: 1) {
            let mem = self.isTunnelAliveViaXPC()
            self.currentMemmoryUsageMb = mem
            return mem >= 0
        }

        let serverAliveProtected = runWithRetry(name: "Server reachability (protected)", attempts: 1) {
            let serverPort = DobbyConfigsRepositoryImpl.shared.getServerPort()
            if serverPort.isEmpty { return true }
            return self.pingAddressProtected(serverPort, name: "UpstreamServer")
        }

        let outlineProxyOk = runWithRetry(name: "Outline local proxy check", attempts: 1) {
            self.isOutlineProxyAliveViaXPC()
        }
        lastOutlineProxyOk = outlineProxyOk

        if currentMemmoryUsageMb >= 0 {
            let delta = currentMemmoryUsageMb - lastMemoryMB
            let deltaStr = lastMemoryMB == 0 ? "" : " (\(delta >= 0 ? "+" : "")\(String(format: "%.1f", delta))MB)"
            logs.writeLog(log: "[HC] Memory: \(String(format: "%.1f", currentMemmoryUsageMb))MB\(deltaStr)")
            lastMemoryMB = currentMemmoryUsageMb
        } else {
            logs.writeLog(log: "[HC] Memory: unknown (XPC no response)")
        }

        let result = vpnOk && networkOk && heartbeatOk && outlineProxyOk && serverAliveProtected
        logs.writeLog(
            log: "[HC] End shortConnectionCheckUp id=\(checkId) => \(result) " +
            "(vpn=\(vpnOk), network=\(networkOk), heartbeat=\(heartbeatOk), " +
            "outlineProxy=\(outlineProxyOk), serverAlive=\(serverAliveProtected))"
        )
        return result
    }

    public func fullConnectionCheckUp() -> Bool {
        let checkId = nextCheckId(prefix: "full")
        logs.writeLog(log: "[HC] Start fullConnectionCheckUp id=\(checkId)")

        // --- Standard check groups ---
        // On iOS, net.Dial based TCP probes can complete against the local
        // virtual TCP stack before the upstream proxy dial has succeeded.
        // Treat real HTTP and local Outline proxy liveness as required; run
        // TCP probes only as diagnostics.
        let requiredCheckGroups: [(String, [(String, () -> Bool)])] = [
            ("DNS Resolve group", [
                ("DNS google.com", { self.resolveDNSWithTimeout(host: "google.com") }),
                ("DNS one.one.one.one", { self.resolveDNSWithTimeout(host: "one.one.one.one") })
            ])
        ]

        let diagnosticCheckGroups: [(String, [(String, () -> Bool)])] = [
            ("Hostname TCP diagnostics", [
                ("Ping google.com:80", { self.pingAddress("google.com:80", name: "GoogleDNS") }),
                ("Ping one.one.one.one:80", { self.pingAddress("one.one.one.one:80", name: "OnesDNS") })
            ]),
            ("TCP Ping diagnostics", [
                ("Ping 8.8.8.8", { self.pingAddress("8.8.8.8:53", name: "Google") }),
                ("Ping 1.1.1.1", { self.pingAddress("1.1.1.1:53", name: "OneOneOneOne") })
            ]),
            ("TCP :443 diagnostics", [
                ("Ping 8.8.8.8:443", { self.pingAddress("8.8.8.8:443", name: "TCP443-8888") }),
                ("Ping 1.1.1.1:443", { self.pingAddress("1.1.1.1:443", name: "TCP443-1111") })
            ])
        ]

        var failedGroups: [String] = []
        var groupResults: [String: Bool] = [:]

        for (groupName, checks) in requiredCheckGroups {
            logs.writeLog(log: "[HC] Checking group: \(groupName)")
            let groupOk = checks.contains { name, check in
                self.runWithRetry(name: name, block: check)
            }
            groupResults[groupName] = groupOk
            if !groupOk {
                logs.writeLog(log: "[HC] Group FAILED: \(groupName)")
                failedGroups.append(groupName)
            } else {
                logs.writeLog(log: "[HC] Group OK: \(groupName)")
            }
        }

        logs.writeLog(log: "[HC] Checking group: Short health check group")
        let shortOk = shortConnectionCheckUp()
        groupResults["Short health check group"] = shortOk
        if !shortOk {
            logs.writeLog(log: "[HC] Group FAILED: Short health check group")
            failedGroups.append("Short health check group")
        } else {
            logs.writeLog(log: "[HC] Group OK: Short health check group")
        }

        if !failedGroups.isEmpty {
            logs.writeLog(log: "[HC] Required checks failed → running TCP diagnostic groups")
            for (groupName, checks) in diagnosticCheckGroups {
                logs.writeLog(log: "[HC] Checking group: \(groupName)")
                let groupOk = checks.contains { name, check in
                    self.runWithRetry(name: name, block: check)
                }
                groupResults[groupName] = groupOk
                logs.writeLog(log: "[HC] Group \(groupOk ? "OK" : "DIAGNOSTIC FAILED"): \(groupName)")
            }
        }

        // --- Additional diagnostic tests (do not affect the result) ---
        logs.writeLog(log: "[HC] [diag] === Diagnostic checks ===")

        // Check if tunnel IP is assigned — confirms the tunnel exists at OS level
        let tunnelIPOk = isTunnelIPAssigned()
        logs.writeLog(log: "[HC] [diag] tunnel_ip_assigned=\(tunnelIPOk)")
        logs.writeLog(log: "[HC] [diag] outline_proxy_alive=\(lastOutlineProxyOk)")

        // --- Result ---
        let result = failedGroups.isEmpty
        if !result {
            logs.writeLog(
                log: "[HC] Required check groups failed (\(failedGroups.count)): " +
                     failedGroups.joined(separator: ", ")
            )
        }
        logs.writeLog(log: "[HC] RESULT id=\(checkId) = \(result)")

        // --- DIAGNOSIS — single line with actionable verdict ---
        let tcpProxyOk = groupResults["TCP Ping diagnostics"] ?? true
        let dnsOk = groupResults["DNS Resolve group"] ?? false
        let tcp443Ok = groupResults["TCP :443 diagnostics"] ?? true
        let httpsOk = shortOk
        logDiagnosis(
            tunnelIP: tunnelIPOk,
            tcpProxy: tcpProxyOk,
            dns: dnsOk,
            tcp443: tcp443Ok,
            outlineProxy: lastOutlineProxyOk,
            https: httpsOk
        )

        return result
    }

    private func nextCheckId(prefix: String) -> String {
        checkSequenceLock.lock()
        checkSequence += 1
        let value = checkSequence
        checkSequenceLock.unlock()
        return "\(prefix)-\(value)"
    }

    private func logDiagnosis(
        tunnelIP: Bool,
        tcpProxy: Bool,
        dns: Bool,
        tcp443: Bool,
        outlineProxy: Bool,
        https: Bool
    ) {
        let diagnosis: String
        switch (tunnelIP, tcpProxy, dns, tcp443, outlineProxy, https) {
        case (false, _, _, _, _, _):
            diagnosis = "SIDE: CLIENT | REASON: tunnel IP 198.18.0.1 not assigned — tunnel failed to start at OS level"
        case (true, _, _, _, false, _):
            diagnosis = "SIDE: CLIENT | REASON: local Outline SOCKS5 proxy is unavailable — tun2socks has no working upstream proxy"
        case (true, _, false, _, _, _):
            diagnosis = "SIDE: CLIENT OR SERVER | REASON: DNS not resolving — DNS-over-tunnel is failing or blocked"
        case (true, _, true, _, true, false):
            // Check metrics[win] in logs: proto=h3+total=- → QUIC/UDP not proxied (no udpPath? pool full?);
            // proto=h2+high total → server is slow or blocking TLS
            diagnosis = "SIDE: CLIENT OR SERVER | REASON: Outline proxy is alive but HTTPS checks fail — check [win] metrics for h3/h2 timing and server behavior"
        case (true, false, true, _, true, _):
            diagnosis = "SIDE: CLIENT | REASON: TCP diagnostic failed after DNS/HTTP checks — possible tun2socks forwarding issue"
        case (true, true, true, false, true, _):
            diagnosis = "SIDE: SERVER (likely) | REASON: TCP diagnostic to :443 failed — server blocks HTTPS port or intermediate node filters it"
        case (true, true, true, true, true, true):
            diagnosis = "ALL OK — connection is working"
        default:
            diagnosis = "UNDEFINED | Pattern: tunnel=\(tunnelIP) tcp=\(tcpProxy) dns=\(dns) tcp443=\(tcp443) outlineProxy=\(outlineProxy) https=\(https)"
        }
        logs.writeLog(log: "[HC] DIAGNOSIS: \(diagnosis)")
    }

    private func runWithRetry(
        name: String,
        attempts: Int = 2,
        timeoutPerAttempt: TimeInterval? = nil,
        block: @escaping () -> Bool
    ) -> Bool {
        for attempt in 1...attempts {
            logs.writeLog(log: "[HC] \(name) attempt \(attempt)")
            let ok: Bool
            if let timeoutPerAttempt {
                ok = runWithTimeout(timeout: timeoutPerAttempt, block: block)
            } else {
                ok = block()
            }

            if ok {
                return true
            }
        }
        logs.writeLog(log: "[HC] \(name) FAILED after \(attempts) attempts")
        return false
    }

    private func runWithTimeout(
        timeout: TimeInterval,
        block: @escaping () -> Bool
    ) -> Bool {
        let semaphore = DispatchSemaphore(value: 0)
        let lock = NSLock()
        var result = false

        DispatchQueue.global(qos: .userInitiated).async {
            let ok = block()
            lock.lock()
            result = ok
            lock.unlock()
            semaphore.signal()
        }

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            return false
        }
        lock.lock()
        let value = result
        lock.unlock()
        return value
    }

    private func resolveDNSWithTimeout(host: String) -> Bool {
        var result: (ok: Bool, message: String)?
        let group = DispatchGroup()
        group.enter()

        DispatchQueue.global(qos: .userInitiated).async {
            result = self.resolveDNS(host: host)
            group.leave()
        }

        let wait = group.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            logs.writeLog(log: "[HC] [DNS] \(host) FAILED: timeout after \(Int(timeout * 1000))ms")
            return false
        }
        let value = result ?? (false, "nil result")
        logs.writeLog(log: "[HC] [DNS] \(host) \(value.ok ? "OK" : "FAILED"): \(value.message)")
        return value.ok
    }

    private func resolveDNS(host: String) -> (ok: Bool, message: String) {
        var hints = addrinfo(
            ai_flags: AI_PASSIVE,
            ai_family: AF_UNSPEC,
            ai_socktype: SOCK_STREAM,
            ai_protocol: 0,
            ai_addrlen: 0,
            ai_canonname: nil,
            ai_addr: nil,
            ai_next: nil
        )

        var infoPointer: UnsafeMutablePointer<addrinfo>?
        let status = getaddrinfo(host, nil, &hints, &infoPointer)

        guard status == 0, let first = infoPointer else {
            return (false, String(cString: gai_strerror(status)))
        }

        defer { freeaddrinfo(infoPointer) }

        var ptr: UnsafeMutablePointer<addrinfo>? = first
        while let current = ptr, let addr = current.pointee.ai_addr {
            var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            if getnameinfo(
                addr,
                socklen_t(current.pointee.ai_addrlen),
                &buffer,
                socklen_t(buffer.count),
                nil,
                0,
                NI_NUMERICHOST
            ) == 0 {
                return (true, String(cString: buffer))
            }
            ptr = current.pointee.ai_next
        }

        return (false, "Can't resolve DNS")
    }

    private func httpPing(urlString: String) -> Bool {
        guard let url = URL(string: urlString) else {
            logs.writeLog(log: "[HC] [httpPing] Invalid URL: \(urlString)")
            return false
        }

        let semaphore = DispatchSemaphore(value: 0)
        var success = false
        let startTime = Date()

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = timeout
        request.cachePolicy = .reloadIgnoringLocalCacheData

        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = timeout
        config.timeoutIntervalForResource = timeout
        logs.writeLog(
            log: "[DEBUG][HC] [httpPing] start url=\(urlString) timeoutMs=\(Int(timeout * 1000)) " +
            "cachePolicy=reloadIgnoringLocalCacheData"
        )
        let delegate = HttpMetricsDelegate(url: urlString) { [weak self] msg in
            self?.logs.writeLog(log: msg)
        }
        let session = URLSession(configuration: config, delegate: delegate, delegateQueue: nil)

        let task = session.dataTask(with: request) { _, response, error in
            let elapsed = Int(Date().timeIntervalSince(startTime) * 1000)
            if let error = error {
                let nsErr = error as NSError
                self.logs.writeLog(
                    log: "[HC] [httpPing] \(urlString) ERROR in \(elapsed)ms:" +
                         " [\(nsErr.domain) \(nsErr.code)] \(nsErr.localizedDescription)"
                )
            } else if let http = response as? HTTPURLResponse {
                let ok = (200..<400).contains(http.statusCode)
                self.logs.writeLog(
                    log: "[HC] [httpPing] \(urlString) HTTP \(http.statusCode)" +
                         " in \(elapsed)ms → \(ok ? "OK" : "FAIL")"
                )
                success = ok
            } else {
                self.logs.writeLog(
                    log: "[HC] [httpPing] \(urlString) no HTTP response in \(elapsed)ms"
                )
            }
            session.finishTasksAndInvalidate()
            semaphore.signal()
        }
        task.resume()

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            let elapsed = Int(Date().timeIntervalSince(startTime) * 1000)
            logs.writeLog(log: "[HC] [httpPing] \(urlString) TIMEOUT after \(elapsed)ms")
            task.cancel()
            session.invalidateAndCancel()
        }
        return success
    }

    // Checks if Dobby's configured tunnel IP is assigned to at least one interface.
    // If not — the tunnel did not come up at OS network level.
    private func isTunnelIPAssigned() -> Bool {
        if let name = interfaceName(forIPv4: tunnelIPAddress) {
            logs.writeLog(log: "[HC] [diag] tunnel IP \(tunnelIPAddress) found on \(name)")
            return true
        }
        logs.writeLog(log: "[HC] [diag] tunnel IP \(tunnelIPAddress) NOT found on any interface")
        return false
    }

    private func interfaceName(forIPv4 targetIP: String) -> String? {
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let first = ifaddrPtr else { return nil }
        defer { freeifaddrs(ifaddrPtr) }

        var ptr: UnsafeMutablePointer<ifaddrs>? = first
        while let addr = ptr {
            if addr.pointee.ifa_addr?.pointee.sa_family == UInt8(AF_INET) {
                var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
                var sa = addr.pointee.ifa_addr!.withMemoryRebound(to: sockaddr_in.self, capacity: 1) {
                    $0.pointee.sin_addr
                }
                if inet_ntop(AF_INET, &sa, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil {
                    let ip = String(cString: buffer)
                    if ip == targetIP {
                        return String(cString: addr.pointee.ifa_name)
                    }
                }
            }
            ptr = addr.pointee.ifa_next
        }
        return nil
    }

    private func pingAddress(_ address: String, name: String) -> Bool {
        switch tcpPingWithTimeout(address: address) {
        case .success(let ms):
            logs.writeLog(log: "[HC] [ping \(name)] \(ms) ms")
            return true
        case .failure(let error):
            logs.writeLog(log: "[HC] [ping \(name)] error: \(error.localizedDescription)")
            return false
        }
    }

    private func pingAddressProtected(_ address: String, name: String) -> Bool {
        switch tcpPingWithTimeout(address: address, protected: true) {
        case .success(let ms):
            logs.writeLog(log: "[HC] [ping-protected \(name)] \(ms) ms")
            return true
        case .failure(let error):
            logs.writeLog(log: "[HC] [ping-protected \(name)] error: \(error.localizedDescription)")
            return false
        }
    }

    private func tcpPingWithTimeout(address: String, protected: Bool = false) -> Result<Int32, Error> {
        // The Go ping helper might block longer than desired; enforce a hard wall-clock timeout.
        let semaphore = DispatchSemaphore(value: 0)
        var result: Result<Int32, Error> = .failure(
            NSError(
                domain: "CloakTcpPing",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "Timeout"]
            )
        )

        DispatchQueue.global(qos: .userInitiated).async {
            result = self.tcpPing(address: address, protected: protected)
            semaphore.signal()
        }

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            return .failure(
                NSError(
                    domain: "CloakTcpPing",
                    code: -2,
                    userInfo: [NSLocalizedDescriptionKey: "Timeout"]
                )
            )
        }
        return result
    }

    private func tcpPing(address: String, protected: Bool = false) -> Result<Int32, Error> {
        var ret: Int32 = 0
        var err: NSError?
        let success: Bool
        if protected {
            success = Cloak_outlineProtectedTcpPing(address, &ret, &err)
        } else {
            success = Cloak_outlineTcpPing(address, &ret, &err)
        }

        if success {
            return .success(ret)
        } else if let err {
            return .failure(err)
        } else {
            return .failure(
                NSError(
                    domain: "CloakTcpPing",
                    code: -1,
                    userInfo: [NSLocalizedDescriptionKey: "Unknown error"]
                )
            )
        }
    }

    private func isVPNInterfaceExists() -> Bool {
        if let name = interfaceName(forIPv4: tunnelIPAddress) {
            logs.writeLog(log: "[HC] [VPNIface] Dobby tunnel IP \(tunnelIPAddress) found on \(name)")
            return true
        }

        // iOS 26 fallback: Check for any utun interface if IP check fails
        // This helps identify if the tunnel is at least partially up
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let first = ifaddrPtr else { return false }
        defer { freeifaddrs(ifaddrPtr) }

        var ptr: UnsafeMutablePointer<ifaddrs>? = first
        while let addr = ptr {
            let name = String(cString: addr.pointee.ifa_name)
            if name.starts(with: "utun") {
                 logs.writeLog(log: "[HC] [VPNIface] Found utun interface \(name) but it doesn't have IP \(tunnelIPAddress)")
            }
            ptr = addr.pointee.ifa_next
        }

        logs.writeLog(
            log: "[HC] [VPNIface] Dobby tunnel IP \(tunnelIPAddress) not found on any interface"
        )
        return false
    }

    private func providerMessage(_ message: String, label: String) -> String? {
        var rawResponse: String?
        let semaphore = DispatchSemaphore(value: 0)

        NETunnelProviderManager.loadAllFromPreferences { managers, error in
            if let error = error {
                self.logs.writeLog(
                    log: "[HC] [\(label)] loadAllFromPreferences error: \(error.localizedDescription)"
                )
                semaphore.signal()
                return
            }
            guard
                let manager = managers?.first(where: {
                    $0.localizedDescription == VpnManagerImpl.dobbyName &&
                    ($0.protocolConfiguration as? NETunnelProviderProtocol)?
                        .providerBundleIdentifier == VpnManagerImpl.dobbyBundleIdentifier
                }),
                let session = manager.connection as? NETunnelProviderSession
            else {
                self.logs.writeLog(
                    log: "[HC] [\(label)] manager not found (managers count: \(managers?.count ?? -1))"
                )
                semaphore.signal()
                return
            }

            self.logs.writeLog(
                log: "[HC] [\(label)] session status: \(session.status.rawValue)"
            )

            do {
                try session.sendProviderMessage(
                    Data(message.utf8)
                ) { response in
                    defer { semaphore.signal() }
                    rawResponse = response.flatMap { String(data: $0, encoding: .utf8) }
                }
            } catch {
                self.logs.writeLog(
                    log: "[HC] [\(label)] sendProviderMessage error: \(error.localizedDescription)"
                )
                semaphore.signal()
            }
        }

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            logs.writeLog(log: "[HC] [\(label)] provider message timed out")
        }
        return rawResponse
    }

    private func isTunnelAliveViaXPC() -> Double {
        let raw = providerMessage("getMemory", label: "XPC")
        let memory = parseMemoryResponse(raw)
        if memory < 0 {
            logs.writeLog(log: "[HC] [XPC] unexpected response: \(raw ?? "nil")")
        }
        return memory
    }

    private func isOutlineProxyAliveViaXPC() -> Bool {
        let raw = providerMessage("getOutlineStatus", label: "Outline")
        logs.writeLog(log: "[HC] [Outline] status: \(raw ?? "nil")")
        guard let raw else { return false }
        return raw.contains("localProxyAlive=true")
    }

    private func parseMemoryResponse(_ response: String?) -> Double {
        guard let response,
              response.hasPrefix("Memory:")
        else { return -1 }
        let value = response.replacingOccurrences(of: "Memory:", with: "")
        return Double(value) ?? -1
    }

    public func getTimeToWakeUp() -> Int32 {
        return 2
    }

    public func checkServerAlive(address: String, port: Int32) -> Bool {
        return pingAddress("\(address):\(port)", name: "ServerAlive")
    }
}
