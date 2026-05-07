import NetworkExtension
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

private final class HttpMetricsDelegate: NSObject, URLSessionDataDelegate {
    private let url: String
    private let log: (String) -> Void

    init(url: String, log: @escaping (String) -> Void) {
        self.url = url
        self.log = log
    }

    func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        didFinishCollecting metrics: URLSessionTaskMetrics
    ) {
        for (index, transaction) in metrics.transactionMetrics.enumerated() {
            func ms(_ start: Date?, _ end: Date?) -> String {
                guard let start, let end else { return "-" }
                return "\(Int(end.timeIntervalSince(start) * 1000))ms"
            }

            let completed = transaction.responseEndDate != nil
            let proto = transaction.networkProtocolName ?? "?"
            let local = transaction.localAddress ?? "-"
            let remote = "\(transaction.remoteAddress ?? "-"):\(transaction.remotePort.map { "\($0)" } ?? "-")"
            log(
                "[HC] [httpMetrics#\(index)\(completed ? "[complete]" : "[partial]")] url=\(url) " +
                "proto=\(proto) reused=\(transaction.isReusedConnection) local=\(local) remote=\(remote) " +
                "dns=\(ms(transaction.domainLookupStartDate, transaction.domainLookupEndDate)) " +
                "tcp=\(ms(transaction.connectStartDate, transaction.connectEndDate)) " +
                "tls=\(ms(transaction.secureConnectionStartDate, transaction.secureConnectionEndDate)) " +
                "ttfb=\(ms(transaction.requestEndDate, transaction.responseStartDate)) " +
                "total=\(ms(transaction.fetchStartDate, transaction.responseEndDate))"
            )
        }
    }
}

public final class HealthCheckImpl: HealthCheck {

    public static let shared = HealthCheckImpl()

    private let logs = NativeModuleHolder.logsRepository
    private let timeout: TimeInterval = 4.0

    private enum DNSCheckResult {
        case success(String)
        case failure(String)
        case timeout
    }

    public private(set) var currentMemmoryUsageMb = 0.0
    private var checkSequence = 0
    private let checkSequenceLock = NSLock()

    public func shortConnectionCheckUp() -> Bool {
        let checkId = nextCheckId(prefix: "short")
        logs.writeLog(log: "[HC] Start shortConnectionCheckUp id=\(checkId) userStopRequested=\(isUserStopRequested())")

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

        let vpnOk = runWithRetry(name: "VPN Interface Check", attempts: 1) {
            self.isVPNInterfaceExists()
        }

        let result = vpnOk && networkOk
        logs.writeLog(log: "[HC] End shortConnectionCheckUp id=\(checkId) => \(result) (vpn=\(vpnOk), network=\(networkOk), userStopRequested=\(isUserStopRequested()))")
        return result
    }

    public func fullConnectionCheckUp() -> Bool {
        let checkId = nextCheckId(prefix: "full")
        logs.writeLog(log: "[HC] Start fullConnectionCheckUp id=\(checkId) userStopRequested=\(isUserStopRequested())")

        let groups: [(String, [(String, () -> Bool)])] = [
            ("TCP Ping group", [
                ("Ping 8.8.8.8", { self.pingAddress("8.8.8.8:53", name: "Google") }),
                ("Ping 1.1.1.1", { self.pingAddress("1.1.1.1:53", name: "OneOneOneOne") })
            ]),
            ("DNS Resolve group", [
                ("DNS google.com", { self.resolveDNSCheck(host: "google.com") }),
                ("DNS one.one.one.one", { self.resolveDNSCheck(host: "one.one.one.one") })
            ]),
            ("DNS Ping group", [
                ("Ping google.com (DNS)", { self.pingAddress("google.com:80", name: "GoogleDNS") }),
                ("Ping one.one.one.one (DNS)", { self.pingAddress("one.one.one.one:80", name: "OnesDNS") })
            ])
        ]

        var failedGroups: [String] = []

        for (groupName, checks) in groups {
            logs.writeLog(log: "[HC] Checking group: \(groupName)")

            let groupOk = checks.contains { name, check in
                self.runWithRetry(name: name, block: check)
            }

            if !groupOk {
                logs.writeLog(log: "[HC] Group FAILED: \(groupName)")
                failedGroups.append(groupName)
            } else {
                logs.writeLog(log: "[HC] Group OK: \(groupName)")
            }
        }

        logs.writeLog(log: "[HC] Checking group: Short health check group")

        let shortOk = shortConnectionCheckUp()

        if !shortOk {
            logs.writeLog(log: "[HC] Group FAILED: Short health check group")
            failedGroups.append("Short health check group")
        } else {
            logs.writeLog(log: "[HC] Group OK: Short health check group")
        }

        var result = failedGroups.count <= 1
        if !result {
            logs.writeLog(
                log: "[HC] Too many failed groups (\(failedGroups.count)): " +
                     failedGroups.joined(separator: ", ")
            )
        }

        let heartbeatOk = runWithRetry(name: "XPC heartbeat check", attempts: 1) {
            let mem = self.isTunnelAliveViaXPC()
            self.currentMemmoryUsageMb = mem
            return mem >= 0
        }

        if !heartbeatOk {
            result = false
        }

        if currentMemmoryUsageMb >= 0 {
            logs.writeLog(log: "[HC] Memory usage: \(currentMemmoryUsageMb)MB")
        } else {
            logs.writeLog(log: "[HC] Memory usage: unknown (can't get XPC memory)")
        }

        logs.writeLog(log: "[HC] RESULT id=\(checkId) = \(result) failedGroups=\(failedGroups.joined(separator: ",")) userStopRequested=\(isUserStopRequested())")
        return result
    }

    private func nextCheckId(prefix: String) -> String {
        checkSequenceLock.lock()
        checkSequence += 1
        let value = checkSequence
        checkSequenceLock.unlock()
        return "\(prefix)-\(value)"
    }

    private func isUserStopRequested() -> Bool {
        DobbyConfigsRepositoryImpl.shared.getIsUserInitStop()
    }

    private func runWithRetry(
        name: String,
        attempts: Int = 2,
        timeoutPerAttempt: TimeInterval? = nil,
        block: @escaping () -> Bool
    ) -> Bool {
        for attempt in 1...attempts {
            logs.writeLog(log: "[HC] \(name) attempt \(attempt)")
            let started = Date()
            let ok: Bool
            if let timeoutPerAttempt {
                ok = runWithTimeout(timeout: timeoutPerAttempt, block: block)
            } else {
                ok = block()
            }

            if ok {
                logs.writeLog(log: "[HC] \(name) attempt \(attempt) OK in \(elapsedMs(since: started))ms")
                return true
            }
            logs.writeLog(log: "[HC] \(name) attempt \(attempt) failed in \(elapsedMs(since: started))ms")
        }
        logs.writeLog(log: "[HC] \(name) FAILED after \(attempts) attempts")
        return false
    }

    private func elapsedMs(since started: Date) -> Int {
        Int(Date().timeIntervalSince(started) * 1000)
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

    private func resolveDNSCheck(host: String) -> Bool {
        switch resolveDNSWithTimeout(host: host) {
        case .success(let ip):
            logs.writeLog(log: "[HC] DNS \(host) resolved to \(ip)")
            return true
        case .failure(let error):
            logs.writeLog(log: "[HC] DNS \(host) failed: \(error)")
            return false
        case .timeout:
            logs.writeLog(log: "[HC] DNS \(host) timed out after \(Int(timeout * 1000))ms")
            return false
        }
    }

    private func resolveDNSWithTimeout(host: String) -> DNSCheckResult {
        let lock = NSLock()
        var result = DNSCheckResult.failure("No resolver result")
        let group = DispatchGroup()
        group.enter()

        DispatchQueue.global(qos: .userInitiated).async {
            let value = self.resolveDNS(host: host)
            lock.lock()
            result = value
            lock.unlock()
            group.leave()
        }

        let wait = group.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            return .timeout
        }

        lock.lock()
        let value = result
        lock.unlock()
        return value
    }

    private func resolveDNS(host: String) -> DNSCheckResult {
        var hints = addrinfo(
            ai_flags: 0,
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
            return .failure(String(cString: gai_strerror(status)))
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
                return .success(String(cString: buffer))
            }
            ptr = current.pointee.ai_next
        }

        return .failure("No numeric address returned")
    }

    private func httpPing(urlString: String) -> Bool {
        guard let url = URL(string: urlString) else {
            logs.writeLog(log: "[HC] HTTP invalid URL: \(urlString)")
            return false
        }

        let semaphore = DispatchSemaphore(value: 0)
        let lock = NSLock()
        var success = false
        var detail = "no callback"
        let started = Date()

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = timeout
        request.cachePolicy = .reloadIgnoringLocalCacheData

        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = timeout
        config.timeoutIntervalForResource = timeout
        let metricsDelegate = HttpMetricsDelegate(url: urlString) { [weak self] message in
            self?.logs.writeLog(log: message)
        }
        let session = URLSession(configuration: config, delegate: metricsDelegate, delegateQueue: nil)

        let task = session.dataTask(with: request) { _, response, error in
            var ok = false
            var message: String
            if error == nil,
               let http = response as? HTTPURLResponse,
               (200..<400).contains(http.statusCode) {
                ok = true
                message = "status=\(http.statusCode)"
            } else if let error {
                let nsError = error as NSError
                message = "errorDomain=\(nsError.domain) code=\(nsError.code) message=\(error.localizedDescription)"
            } else if let http = response as? HTTPURLResponse {
                message = "status=\(http.statusCode)"
            } else {
                message = "nonHTTP response"
            }
            lock.lock()
            success = ok
            detail = message
            lock.unlock()
            semaphore.signal()
        }
        task.resume()

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            task.cancel()
            session.invalidateAndCancel()
            logs.writeLog(log: "[HC] HTTP \(urlString) timed out after \(Int(timeout * 1000))ms")
            return false
        }
        session.finishTasksAndInvalidate()

        lock.lock()
        let ok = success
        let message = detail
        lock.unlock()

        logs.writeLog(log: "[HC] HTTP \(urlString) result=\(ok) \(message) elapsed=\(elapsedMs(since: started))ms")
        return ok
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

    private func tcpPingWithTimeout(address: String) -> Result<Int32, Error> {
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
            result = self.tcpPing(address: address)
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

    private func tcpPing(address: String) -> Result<Int32, Error> {
        var ret: Int32 = 0
        var err: NSError?
        let success = Cloak_outlineTcpPing(address, &ret, &err)

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
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let firstAddr = ifaddrPtr else {
            logs.writeLog(log: "[HC] VPN interface scan failed")
            return false
        }
        defer { freeifaddrs(ifaddrPtr) }

        var matches: [String] = []
        var ptr: UnsafeMutablePointer<ifaddrs>? = firstAddr
        while let addr = ptr {
            let name = String(cString: addr.pointee.ifa_name).lowercased()
            if name.contains("utun")
                || name.contains("tun")
                || name.contains("tap")
                || name.contains("ppp")
                || name.contains("ipsec") {
                matches.append(name)
            }
            ptr = addr.pointee.ifa_next
        }

        if matches.isEmpty {
            logs.writeLog(log: "[HC] VPN interfaces: none")
            return false
        }
        logs.writeLog(log: "[HC] VPN interfaces: \(Array(Set(matches)).sorted().joined(separator: ","))")
        return true
    }

    private func isTunnelAliveViaXPC() -> Double {
        var memory: Double = -1
        let semaphore = DispatchSemaphore(value: 0)

        NETunnelProviderManager.loadAllFromPreferences { managers, error in
            if let error {
                self.logs.writeLog(log: "[HC] XPC manager load failed: \(error.localizedDescription)")
                semaphore.signal()
                return
            }

            guard let manager = managers?.first(where: {
                $0.localizedDescription == VpnManagerImpl.dobbyName &&
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?
                    .providerBundleIdentifier == VpnManagerImpl.dobbyBundleIdentifier
            }) else {
                self.logs.writeLog(log: "[HC] XPC manager not found")
                semaphore.signal()
                return
            }

            guard let session = manager.connection as? NETunnelProviderSession else {
                self.logs.writeLog(log: "[HC] XPC session unavailable status=\(manager.connection.status.rawValue)")
                semaphore.signal()
                return
            }

            self.logs.writeLog(log: "[HC] XPC heartbeat send status=\(manager.connection.status.rawValue)")

            do {
                try session.sendProviderMessage(
                    Data("getMemory".utf8)
                ) { response in
                    defer { semaphore.signal() }
                    memory = self.parseMemoryResponse(response)
                    if memory >= 0 {
                        self.logs.writeLog(log: "[HC] XPC heartbeat OK memory=\(memory)MB")
                    } else {
                        self.logs.writeLog(log: "[HC] XPC heartbeat invalid response")
                    }
                }
            } catch {
                self.logs.writeLog(log: "[HC] XPC heartbeat send failed: \(error.localizedDescription)")
                semaphore.signal()
            }
        }

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            logs.writeLog(log: "[HC] XPC heartbeat timed out after \(Int(timeout * 1000))ms")
        }
        return memory
    }

    private func parseMemoryResponse(_ response: Data?) -> Double {
        guard let response,
              let str = String(data: response, encoding: .utf8),
              str.hasPrefix("Memory:")
        else { return -1 }
        let value = str.replacingOccurrences(of: "Memory:", with: "")
        return Double(value) ?? -1
    }

    public func getTimeToWakeUp() -> Int32 {
        return 2
    }

    public func checkServerAlive(address: String, port: Int32) -> Bool {
        let start = Date()
        logs.writeLog(log: "[HC] [ServerAlive] native CheckServerAlive begin address=\(address) port=\(port)")
        let status = Cloak_outlineCheckServerAlive(address, Int(port))
        let ok = status == 0
        logs.writeLog(
            log: "[HC] [ServerAlive] native CheckServerAlive result=\(ok) status=\(status) " +
                "elapsed=\(elapsedMs(since: start))ms"
        )
        return ok
    }
}
