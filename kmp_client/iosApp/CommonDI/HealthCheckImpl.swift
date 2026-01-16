import NetworkExtension
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public final class HealthCheckImpl: HealthCheck {

    public static let shared = HealthCheckImpl()

    private let logs = NativeModuleHolder.logsRepository
    private let timeout: TimeInterval = 1.0

    public private(set) var currentMemmoryUsageMb = 0.0

    public func shortConnectionCheckUp() -> Bool {
        logs.writeLog(log: "Start shortConnectionCheckUp")

        let checks: [(String, () -> Bool)] = [
            ("HTTP https://1.1.1.1", {
                self.httpPing(urlString: "https://1.1.1.1")
            }),
            ("HTTP https://google.com/gen_204", {
                self.httpPing(urlString: "https://google.com/gen_204")
            })
        ]

        let networkOk = checks.contains { (name, check) in
            self.runWithRetry(name: name, block: check)
        }

        let vpnOk = runWithRetry(name: "VPN Interface Check", attempts: 1) {
            self.isVPNInterfaceExists()
        }

        let result = vpnOk && networkOk
        logs.writeLog(log: "End shortConnectionCheckUp => \(result)")
        return result
    }


    public func fullConnectionCheckUp() -> Bool {
        logs.writeLog(log: "[HealthCheck] START")
        logs.writeLog(log: "Start fullConnectionCheckUp")

        let groups: [(String, [(String, () -> Bool)])] = [

            // Group 1: TCP Ping
            ("TCP Ping group", [
                ("Ping 8.8.8.8", { self.pingAddress("8.8.8.8:53", name: "Google") }),
                ("Ping 1.1.1.1", { self.pingAddress("1.1.1.1:53", name: "OneOneOneOne") })
            ]),

            // Group 2: DNS Resolve
            ("DNS Resolve group", [
                ("DNS google.com", { self.resolveDNSWithTimeout(host: "google.com") != "Timeout" }),
                ("DNS one.one.one.one", { self.resolveDNSWithTimeout(host: "one.one.one.one") != "Timeout" })
            ]),

            // Group 3: DNS Ping (TCP)
            ("DNS Ping group", [
                ("Ping google.com (DNS)", { self.pingAddress("google.com:80", name: "GoogleDNS") }),
                ("Ping one.one.one.one (DNS)", { self.pingAddress("one.one.one.one:80", name: "OnesDNS") })
            ])
        ]

        var result = true

        for (groupName, checks) in groups {
            logs.writeLog(log: "[HealthCheck] Checking group: \(groupName)")

            let groupOk = checks.contains { (name, check) in
                self.runWithRetry(name: name, block: check)
            }

            if !groupOk {
                logs.writeLog(log: "[HealthCheck] Group FAILED: \(groupName)")
                result = false
            } else {
                logs.writeLog(log: "[HealthCheck] Group OK: \(groupName)")
            }
        }

        if !shortConnectionCheckUp() {
            logs.writeLog(log: "[HealthCheck] shortConnectionCheckUp FAILED inside full check")
            result = false
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
            logs.writeLog(log: "[HealthCheck] Memory usage: \(currentMemmoryUsageMb)MB")
        } else {
            logs.writeLog(log: "[HealthCheck] Memory usage: unknown (can't get XPC memory)")
        }

        logs.writeLog(log: "[HealthCheck] RESULT = \(result)")
        return result
    }

    private func runWithRetry(
        name: String,
        attempts: Int = 2,
        timeoutPerAttempt: TimeInterval? = nil,
        block: @escaping () -> Bool
    ) -> Bool {
        for attempt in 1...attempts {
            logs.writeLog(log: "[HealthCheck] \(name) attempt \(attempt)")
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
        logs.writeLog(log: "[HealthCheck] \(name) FAILED after \(attempts) attempts")
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

    private func resolveDNSWithTimeout(host: String) -> String? {
        var result: String? = nil
        let group = DispatchGroup()
        group.enter()

        DispatchQueue.global(qos: .userInitiated).async {
            result = self.resolveDNS(host: host)
            group.leave()
        }

        let wait = group.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            return "Timeout"
        }
        return result
    }

    private func resolveDNS(host: String) -> String {
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
            return String(cString: gai_strerror(status))
        }

        defer { freeaddrinfo(infoPointer) }

        var ptr: UnsafeMutablePointer<addrinfo>? = first
        while let addr = ptr?.pointee.ai_addr {
            var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
            if getnameinfo(
                addr,
                socklen_t(ptr!.pointee.ai_addrlen),
                &buffer,
                socklen_t(buffer.count),
                nil,
                0,
                NI_NUMERICHOST
            ) == 0 {
                return String(cString: buffer)
            }
            ptr = ptr?.pointee.ai_next
        }

        return "Can't resolve DNS"
    }

    private func httpPing(urlString: String) -> Bool {
        guard let url = URL(string: urlString) else { return false }

        let semaphore = DispatchSemaphore(value: 0)
        var success = false

        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        request.timeoutInterval = timeout
        request.cachePolicy = .reloadIgnoringLocalCacheData

        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = httpTimeout
        config.timeoutIntervalForResource = httpTimeout
        let session = URLSession(configuration: config)

        let task = session.dataTask(with: request) { _, response, error in
            if error == nil,
               let http = response as? HTTPURLResponse,
               (200..<400).contains(http.statusCode) {
                success = true
            }
            semaphore.signal()
        }
        task.resume()

        let wait = semaphore.wait(timeout: .now() + httpTimeout)
        if wait == .timedOut {
            task.cancel()
        }
        return success
    }

    private func pingAddress(_ address: String, name: String) -> Bool {
        switch tcpPingWithTimeout(address: address) {
        case .success(let ms):
            logs.writeLog(log: "[ping \(name)] \(ms) ms")
            return true
        case .failure(let error):
            logs.writeLog(log: "[ping \(name)] error: \(error.localizedDescription)")
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

        let wait = semaphore.wait(timeout: .now() + tcpTimeout)
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
            return false
        }
        defer { freeifaddrs(ifaddrPtr) }

        var ptr: UnsafeMutablePointer<ifaddrs>? = firstAddr
        while let p = ptr {
            let name = String(cString: p.pointee.ifa_name).lowercased()
            if name.contains("utun")
                || name.contains("tun")
                || name.contains("tap")
                || name.contains("ppp")
                || name.contains("ipsec") {
                return true
            }
            ptr = p.pointee.ifa_next
        }
        return false
    }

    private func isTunnelAliveViaXPC() -> Double {
        var memory: Double = -1
        let semaphore = DispatchSemaphore(value: 0)

        NETunnelProviderManager.loadAllFromPreferences { managers, error in
            guard
                error == nil,
                let manager = managers?.first(where: {
                    $0.localizedDescription == VpnManagerImpl.dobbyName &&
                    ($0.protocolConfiguration as? NETunnelProviderProtocol)?
                        .providerBundleIdentifier == VpnManagerImpl.dobbyBundleIdentifier
                }),
                let session = manager.connection as? NETunnelProviderSession
            else {
                semaphore.signal()
                return
            }

            do {
                try session.sendProviderMessage(
                    "getMemory".data(using: .utf8)!
                ) { response in
                    defer { semaphore.signal() }

                    guard
                        let response,
                        let str = String(data: response, encoding: .utf8),
                        str.hasPrefix("Memory:")
                    else {
                        return
                    }

                    let value = str.replacingOccurrences(of: "Memory:", with: "")
                    if let mem = Double(value) {
                        memory = mem
                    }
                }
            } catch {
                semaphore.signal()
            }
        }

        _ = semaphore.wait(timeout: .now() + timeout)
        return memory
    }

    public func getTimeToWakeUp() -> Int32 {
        return 2
    }

    public func checkServerAlive(address: String, port: Int32) -> Bool {
        return pingAddress("\(address):\(port)", name: "ServerAlive")
    }
}
