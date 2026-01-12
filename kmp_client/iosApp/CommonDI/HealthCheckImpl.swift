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

        var ok = true

        for (name, check) in checks {
            if !runWithRetry(name: name, block: check) {
                ok = false
            }
        }

        if !runWithRetry(name: "VPN Interface Check", attempts: 1, block: {
            self.isVPNInterfaceExists()
        }) {
            ok = false
        }

        if !runWithRetry(name: "XPC heartbeat check", attempts: 1, block: {
            let mem = self.isTunnelAliveViaXPC()
            self.currentMemmoryUsageMb = mem
            return mem >= 0
        }) {
            ok = false
        }
        
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
        var result = "Timeout"
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

        URLSession.shared.dataTask(with: request) { _, response, error in
            if error == nil,
               let http = response as? HTTPURLResponse,
               (200..<400).contains(http.statusCode) {
                success = true
            }
            semaphore.signal()
        }.resume()

        _ = semaphore.wait(timeout: .now() + timeout)
        return success
    }

    private func pingAddress(_ address: String, name: String) -> Bool {
        switch tcpPing(address: address) {
        case .success(let ms):
            logs.writeLog(log: "[ping \(name)] \(ms) ms")
            return true
        case .failure(let error):
            logs.writeLog(log: "[ping \(name)] error: \(error.localizedDescription)")
            return false
        }
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
        guard
            let dict = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any],
            let scoped = dict["__SCOPED__"] as? [String: Any]
        else {
            return false
        }

        for key in scoped.keys {
            if key.contains("utun")
                || key.contains("tun")
                || key.contains("tap")
                || key.contains("ppp")
                || key.contains("ipsec") {
                return true
            }
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
        var ret: Int32 = 0
        let success = Cloak_outlineCheckServerAlive(address, port, &ret)

        if success {
            return ret == 0
        }
        return false
    }
}
