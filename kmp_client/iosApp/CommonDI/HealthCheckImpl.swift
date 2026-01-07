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
    // Keep checks snappy; HealthCheckManager is tolerant to short flaps.
    private let tcpTimeout: TimeInterval = 1.0
    private let dnsTimeout: TimeInterval = 1.0
    private let httpTimeout: TimeInterval = 1.0
    private let xpcTimeout: TimeInterval = 1.0

    public private(set) var currentMemmoryUsageMb = 0.0

    public func isConnected() -> Bool {
        logs.writeLog(log: "[HealthCheck] START")

        let checks: [(String, () -> Bool)] = [
            ("Ping 8.8.8.8", {
                self.pingAddress("8.8.8.8:53", name: "Google")
            }),

            ("DNS google.com", {
                self.resolveDNSWithTimeout(host: "google.com") != "Timeout"
            }),

            ("Ping google.com (DNS)", {
                self.pingAddress("google.com:80", name: "GoogleDNS")
            }),

            ("Ping one.one.one.one (DNS)", {
                self.pingAddress("one.one.one.one:80", name: "OnesDNS")
            }),

            ("HTTP https://google.com/gen_204", {
                self.httpPing(urlString: "https://google.com/gen_204")
            })
        ]

        var networkPassed = 0

        for (name, check) in checks {
            if runWithRetry(name: name, block: check) {
                networkPassed += 1
            }
        }

        let interfaceOk = runWithRetry(name: "VPN Interface Check", attempts: 2, block: {
            self.isVPNInterfaceExists()
        })

        let heartbeatOk = runWithRetry(name: "XPC heartbeat check", attempts: 2, block: {
            let mem = self.isTunnelAliveViaXPC()
            self.currentMemmoryUsageMb = mem
            return mem >= 0
        })
        
        let networkOk = networkPassed >= 2
        logs.writeLog(log: "[HealthCheck] Network checks: \(networkPassed)/\(checks.count) passed")

        // If the VPN interface is missing, VPN is not up.
        let ok = heartbeatOk && interfaceOk && networkOk
        
        if self.currentMemmoryUsageMb >= 0 {
            logs.writeLog(
                log: "[HealthCheck] Memory usage: \(currentMemmoryUsageMb)MB, max: 50 MB"
            )
        } else {
            logs.writeLog(
                log: "[HealthCheck] Memory usage: unknown (can't get it by XPC) MB, max: 50 MB"
            )
        }

        logs.writeLog(log: "[HealthCheck] RESULT = \(ok)")
        return ok
    }

    private func runWithRetry(
        name: String,
        attempts: Int = 2,
        block: () -> Bool
    ) -> Bool {
        for attempt in 1...attempts {
            logs.writeLog(log: "[HealthCheck] \(name) attempt \(attempt)")
            if block() {
                return true
            }
        }
        logs.writeLog(log: "[HealthCheck] \(name) FAILED after \(attempts) attempts")
        return false
    }

    private func resolveDNSWithTimeout(host: String) -> String {
        var result: String? = nil
        let group = DispatchGroup()
        group.enter()

        DispatchQueue.global(qos: .userInitiated).async {
            let resolved = self.resolveDNS(host: host)
            result = resolved
            group.leave()
        }

        let wait = group.wait(timeout: .now() + dnsTimeout)
        if wait == .timedOut {
            return "Timeout"
        }
        return result ?? "Timeout"
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
        request.timeoutInterval = httpTimeout
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

        _ = semaphore.wait(timeout: .now() + xpcTimeout)
        return memory
    }

    public func getTimeToWakeUp() -> Int32 {
        return 2
    }
}
