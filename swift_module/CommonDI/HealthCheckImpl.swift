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
            let proto = t.networkProtocolName ?? "?"
            // localAddress показывает через какой интерфейс прошёл трафик:
            // 198.18.0.1 = через VPN-туннель (хорошо)
            // любой другой = трафик обошёл туннель (плохо)
            let local = t.localAddress ?? "-"
            let remote = "\(t.remoteAddress ?? "-"):\(t.remotePort.map { "\($0)" } ?? "-")"
            log(
                "[HC] [metrics#\(i)] proto=\(proto) reused=\(t.isReusedConnection)" +
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
    private let timeout: TimeInterval = 4.0

    public private(set) var currentMemmoryUsageMb = 0.0

    public func shortConnectionCheckUp() -> Bool {
        logs.writeLog(log: "Start shortConnectionCheckUp")

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

        let heartbeatOk = runWithRetry(name: "XPC heartbeat check", attempts: 1) {
            let mem = self.isTunnelAliveViaXPC()
            self.currentMemmoryUsageMb = mem
            return mem >= 0
        }

        if currentMemmoryUsageMb >= 0 {
            logs.writeLog(log: "[HC] Memory usage: \(currentMemmoryUsageMb)MB")
        } else {
            logs.writeLog(log: "[HC] Memory usage: unknown (can't get XPC memory)")
        }

        let result = vpnOk && networkOk && heartbeatOk
        logs.writeLog(log: "End shortConnectionCheckUp => \(result)")
        return result
    }

    public func fullConnectionCheckUp() -> Bool {
        logs.writeLog(log: "[HC] Start fullConnectionCheckUp")

        // --- Стандартные группы ---
        let groups: [(String, [(String, () -> Bool)])] = [
            ("TCP Ping group", [
                ("Ping 8.8.8.8", { self.pingAddress("8.8.8.8:53", name: "Google") }),
                ("Ping 1.1.1.1", { self.pingAddress("1.1.1.1:53", name: "OneOneOneOne") })
            ]),
            ("DNS Resolve group", [
                ("DNS google.com", { self.resolveDNSWithTimeout(host: "google.com") != "Timeout" }),
                ("DNS one.one.one.one", { self.resolveDNSWithTimeout(host: "one.one.one.one") != "Timeout" })
            ]),
            ("DNS Ping group", [
                ("Ping google.com (DNS)", { self.pingAddress("google.com:80", name: "GoogleDNS") }),
                ("Ping one.one.one.one (DNS)", { self.pingAddress("one.one.one.one:80", name: "OnesDNS") })
            ]),
            // TCP к 443 — проверяет, проходит ли HTTPS-трафик на уровне TCP через прокси.
            // Если TCP ping :53 ОК, а :443 нет — сервер блокирует 443 или проблема с роутингом.
            ("TCP :443 group", [
                ("Ping 8.8.8.8:443", { self.pingAddress("8.8.8.8:443", name: "TCP443-8888") }),
                ("Ping 1.1.1.1:443", { self.pingAddress("1.1.1.1:443", name: "TCP443-1111") })
            ])
        ]

        var failedGroups: [String] = []
        var groupResults: [String: Bool] = [:]

        for (groupName, checks) in groups {
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

        // --- Дополнительные диагностические тесты (не влияют на результат) ---
        logs.writeLog(log: "[HC] [diag] === Diagnostic checks ===")

        // Проверяем, назначен ли tunnel IP — есть ли туннель вообще на уровне OS
        let tunnelIPOk = isTunnelIPAssigned()
        logs.writeLog(log: "[HC] [diag] tunnel_ip_assigned=\(tunnelIPOk)")

        // HTTPS без HTTP/3 — если этот тест проходит, а shortCheckUp нет → QUIC-проблема
        logs.writeLog(log: "[HC] [diag] Running HTTPS without HTTP/3...")
        let httpsNoQuicOk = httpPingNoQuic(urlString: "https://google.com/gen_204")
        logs.writeLog(log: "[HC] [diag] https_no_quic=\(httpsNoQuicOk)")

        // --- Итог ---
        let result = failedGroups.count <= 1
        if !result {
            logs.writeLog(
                log: "[HC] Too many failed groups (\(failedGroups.count)): " +
                     failedGroups.joined(separator: ", ")
            )
        }
        logs.writeLog(log: "[HC] RESULT = \(result)")

        // --- DIAGNOSIS — одна строка с готовым ответом ---
        let tcpProxyOk = groupResults["TCP Ping group"] ?? false
        let dnsOk = groupResults["DNS Resolve group"] ?? false
        let tcp443Ok = groupResults["TCP :443 group"] ?? false
        let httpsOk = shortOk
        logDiagnosis(
            tunnelIP: tunnelIPOk,
            tcpProxy: tcpProxyOk,
            dns: dnsOk,
            tcp443: tcp443Ok,
            https: httpsOk,
            httpsNoQuic: httpsNoQuicOk
        )

        return result
    }

    private func logDiagnosis(
        tunnelIP: Bool,
        tcpProxy: Bool,
        dns: Bool,
        tcp443: Bool,
        https: Bool,
        httpsNoQuic: Bool
    ) {
        let diagnosis: String
        switch (tunnelIP, tcpProxy, dns, tcp443, https, httpsNoQuic) {
        case (false, _, _, _, _, _):
            diagnosis = "СТОРОНА: КЛИЕНТ | ПРИЧИНА: tunnel IP 198.18.0.1 не назначен — туннель не поднялся на уровне OS"
        case (true, false, _, _, _, _):
            diagnosis = "СТОРОНА: КЛИЕНТ | ПРИЧИНА: TCP через SOCKS5-прокси не работает — tun2socks не форвардит трафик или Go-движок упал"
        case (true, true, false, _, _, _):
            diagnosis = "СТОРОНА: КЛИЕНТ | ПРИЧИНА: DNS не резолвится — неверные DNS-серверы в туннеле или их трафик не проходит"
        case (true, true, true, false, _, _):
            diagnosis = "СТОРОНА: СЕРВЕР (вероятно) | ПРИЧИНА: TCP :53 OK, но TCP :443 падает — сервер блокирует HTTPS-порт или промежуточный узел фильтрует"
        case (true, true, true, true, false, true):
            diagnosis = "СТОРОНА: КЛИЕНТ | ПРИЧИНА: HTTPS/HTTP2 работает, HTTPS/HTTP3 (QUIC/UDP) нет — tun2socks не проксирует UDP правильно"
        case (true, true, true, true, false, false):
            diagnosis = "СТОРОНА: СМЕШАННАЯ | ПРИЧИНА: TCP :443 OK, TLS/HTTP2 и HTTP3 оба падают — скорее всего сервер получает данные но не отдаёт ответ (проверить логи сервера)"
        case (true, true, true, true, true, _):
            diagnosis = "ВСЁ OK — соединение рабочее"
        default:
            diagnosis = "НЕОПРЕДЕЛЕНО | Паттерн: tunnel=\(tunnelIP) tcp=\(tcpProxy) dns=\(dns) tcp443=\(tcp443) https=\(https) noQuic=\(httpsNoQuic)"
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

    private func resolveDNSWithTimeout(host: String) -> String? {
        var result: String?
        let group = DispatchGroup()
        group.enter()

        DispatchQueue.global(qos: .userInitiated).async {
            result = self.resolveDNS(host: host)
            group.leave()
        }

        let wait = group.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            logs.writeLog(log: "[HC] [DNS] \(host) → Timeout")
            return "Timeout"
        }
        logs.writeLog(log: "[HC] [DNS] \(host) → \(result ?? "nil")")
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
                return String(cString: buffer)
            }
            ptr = current.pointee.ai_next
        }

        return "Can't resolve DNS"
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
            semaphore.signal()
        }
        task.resume()

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            let elapsed = Int(Date().timeIntervalSince(startTime) * 1000)
            logs.writeLog(log: "[HC] [httpPing] \(urlString) TIMEOUT after \(elapsed)ms")
            task.cancel()
        }
        return success
    }

    // Тот же httpPing, но с отключённым HTTP/3 (QUIC).
    // Если этот тест проходит, а обычный нет — проблема именно в QUIC-трафике через прокси.
    private func httpPingNoQuic(urlString: String) -> Bool {
        guard let url = URL(string: urlString) else { return false }

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
        let delegate = HttpMetricsDelegate(url: urlString + " [noQuic]") { [weak self] msg in
            self?.logs.writeLog(log: msg)
        }
        let session = URLSession(configuration: config, delegate: delegate, delegateQueue: nil)

        let task = session.dataTask(with: request) { _, response, error in
            let elapsed = Int(Date().timeIntervalSince(startTime) * 1000)
            if let error = error {
                let nsErr = error as NSError
                self.logs.writeLog(
                    log: "[HC] [httpNoQuic] \(urlString) ERROR in \(elapsed)ms:" +
                         " [\(nsErr.domain) \(nsErr.code)] \(nsErr.localizedDescription)"
                )
            } else if let http = response as? HTTPURLResponse {
                let ok = (200..<400).contains(http.statusCode)
                self.logs.writeLog(
                    log: "[HC] [httpNoQuic] \(urlString) HTTP \(http.statusCode)" +
                         " in \(elapsed)ms → \(ok ? "OK" : "FAIL")"
                )
                success = ok
            } else {
                self.logs.writeLog(
                    log: "[HC] [httpNoQuic] \(urlString) no HTTP response in \(elapsed)ms"
                )
            }
            semaphore.signal()
        }
        task.resume()

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            let elapsed = Int(Date().timeIntervalSince(startTime) * 1000)
            logs.writeLog(log: "[HC] [httpNoQuic] \(urlString) TIMEOUT after \(elapsed)ms")
            task.cancel()
        }
        return success
    }

    // Проверяет, назначен ли тунельный IP 198.18.0.1 хотя бы одному интерфейсу.
    // Если нет — туннель не поднялся на сетевом уровне.
    private func isTunnelIPAssigned() -> Bool {
        let tunnelIP = "198.18.0.1"
        var ifaddrPtr: UnsafeMutablePointer<ifaddrs>?
        guard getifaddrs(&ifaddrPtr) == 0, let first = ifaddrPtr else { return false }
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
                    if ip == tunnelIP {
                        let name = String(cString: addr.pointee.ifa_name)
                        logs.writeLog(log: "[HC] [diag] tunnel IP \(tunnelIP) found on \(name)")
                        return true
                    }
                }
            }
            ptr = addr.pointee.ifa_next
        }
        logs.writeLog(log: "[HC] [diag] tunnel IP \(tunnelIP) NOT found on any interface")
        return false
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
            logs.writeLog(log: "[HC] [VPNIface] getifaddrs failed")
            return false
        }
        defer { freeifaddrs(ifaddrPtr) }

        var ptr: UnsafeMutablePointer<ifaddrs>? = firstAddr
        while let addr = ptr {
            let name = String(cString: addr.pointee.ifa_name).lowercased()
            if name.contains("utun")
                || name.contains("tun")
                || name.contains("tap")
                || name.contains("ppp")
                || name.contains("ipsec") {
                logs.writeLog(log: "[HC] [VPNIface] found: \(name)")
                return true
            }
            ptr = addr.pointee.ifa_next
        }
        logs.writeLog(log: "[HC] [VPNIface] no VPN interface found")
        return false
    }

    private func isTunnelAliveViaXPC() -> Double {
        var memory: Double = -1
        let semaphore = DispatchSemaphore(value: 0)

        NETunnelProviderManager.loadAllFromPreferences { managers, error in
            if let error = error {
                self.logs.writeLog(
                    log: "[HC] [XPC] loadAllFromPreferences error: \(error.localizedDescription)"
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
                    log: "[HC] [XPC] manager not found (managers count: \(managers?.count ?? -1))"
                )
                semaphore.signal()
                return
            }

            self.logs.writeLog(
                log: "[HC] [XPC] session status: \(session.status.rawValue)"
            )

            do {
                try session.sendProviderMessage(
                    Data("getMemory".utf8)
                ) { response in
                    defer { semaphore.signal() }
                    memory = self.parseMemoryResponse(response)
                    if memory < 0 {
                        let raw = response.flatMap { String(data: $0, encoding: .utf8) } ?? "nil"
                        self.logs.writeLog(
                            log: "[HC] [XPC] unexpected response: \(raw)"
                        )
                    }
                }
            } catch {
                self.logs.writeLog(
                    log: "[HC] [XPC] sendProviderMessage error: \(error.localizedDescription)"
                )
                semaphore.signal()
            }
        }

        let wait = semaphore.wait(timeout: .now() + timeout)
        if wait == .timedOut {
            logs.writeLog(log: "[HC] [XPC] heartbeat timed out")
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
        return pingAddress("\(address):\(port)", name: "ServerAlive")
    }
}
