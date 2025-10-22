import NetworkExtension
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public class HealthCheck {
    private let monitor = NWPathMonitor()
    private let logs = NativeModuleHolder.logsRepository
    private let queue = DispatchQueue(label: "Monitor")
    public static let shared = HealthCheck()
    
    public func fullCheckUp() {
        do {
            logs.writeLog(log: "[fullCheckUp] START Check UP")
            let timeout: TimeInterval = 1.0
            
            try safeRepeat(4) { self.pingVPNServer() }
            try safeRepeat(4) { self.pingGoogle() }
            try safeRepeat(4) { self.pingOnes() }
            
            try safeRepeat(4) {
                let ip = self.resolveDNSWithTimeout(host: "one.one.one.one", timeout: timeout)
                self.logs.writeLog(log: "[resolveDNS] one.one.one.one -> \(ip)")
            }
            
            try safeRepeat(4) {
                let ip = self.resolveDNSWithTimeout(host: "google.com", timeout: timeout)
                self.logs.writeLog(log: "[resolveDNS] google.com -> \(ip)")
            }
            
            try safeRepeat(4) { self.pingGoogleWithDNS() }
            try safeRepeat(4) { self.pingOnesWithDNS() }
            
            try safeRepeat(4) {
                self.httpPing(urlString: "https://google.com") { success, statusCode, errorMessage in
                    if success {
                        self.logs.writeLog(log: "[httpPing] HTTP ping is working! Response code: \(statusCode ?? -1)")
                    } else {
                        self.logs.writeLog(log: "[httpPing] HTTP ping failed. Error: \(errorMessage ?? "Unknown")")
                    }
                }
            }
            
            try safeRepeat(4) {
                self.socketPing(host: "google.com", port: 443) { success in
                    if success {
                        self.logs.writeLog(log: "[socketPing] TCP connection is working!")
                    } else {
                        self.logs.writeLog(log: "[socketPing] TCP connection failed.")
                    }
                }
            }
            
            try safeRepeat(4) {
                let status = self.isVPNConnected()
                self.logs.writeLog(log: "[isVPNConnected] isVPNConnected: \(status)")
            }
            
            try safeRepeat(4) {
                let status = self.isVPNConnectedWithAddress()
                self.logs.writeLog(log: "[isVPNConnectedWithAddress] isVPNConnectedWithAddress: \(status)")
            }
            
            monitor.pathUpdateHandler = { path in
                let gateways = path.gateways
                if gateways.isEmpty {
                    self.logs.writeLog(log: "[Gateways] No gateways")
                } else {
                    for gateway in gateways {
                        self.logs.writeLog(log: "[Gateways] Gateway: \(gateway)")
                    }
                }
            }
            
            monitor.start(queue: queue)
            logs.writeLog(log: "[fullCheckUp] FINISH Check UP")
            
        } catch {
            logs.writeLog(log: "[fullCheckUp] ERROR: \(error.localizedDescription)")
        }
    }
    
    func resolveDNSWithTimeout(host: String, timeout: TimeInterval) -> String {
        do {
            var resultIP: String = "Timeout"
            let group = DispatchGroup()
            group.enter()
            
            let workItem = DispatchWorkItem {
                resultIP = self.resolveDNS(host: host)
                group.leave()
            }
            
            DispatchQueue.global(qos: .userInitiated).async(execute: workItem)
            let waitResult = group.wait(timeout: .now() + timeout)
            
            if waitResult == .timedOut {
                workItem.cancel()
                resultIP = "Timeout"
            }
            
            return resultIP
        } catch {
            logs.writeLog(log: "[resolveDNSWithTimeout] error for host \(host): \(error.localizedDescription)")
            return "Error"
        }
    }

    func resolveDNS(host: String) -> String {
        var hints = addrinfo(ai_flags: AI_PASSIVE,
                             ai_family: AF_UNSPEC,
                             ai_socktype: SOCK_STREAM,
                             ai_protocol: 0,
                             ai_addrlen: 0,
                             ai_canonname: nil,
                             ai_addr: nil,
                             ai_next: nil)
        var infoPointer: UnsafeMutablePointer<addrinfo>?
        let status = getaddrinfo(host, nil, &hints, &infoPointer)
        guard status == 0, let first = infoPointer else {
            let errorString = String(cString: gai_strerror(status))
            logs.writeLog(log: "[resolveDNS] error: \(errorString)")
            return errorString
        }
        
        var ip: String = "Can't resolve DNS, no info"
        var ptr: UnsafeMutablePointer<addrinfo>? = first
        while ptr != nil {
            if let addr = ptr?.pointee.ai_addr {
                var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                let result = getnameinfo(addr,
                                         socklen_t(ptr!.pointee.ai_addrlen),
                                         &buffer,
                                         socklen_t(buffer.count),
                                         nil, 0,
                                         NI_NUMERICHOST)
                if result == 0 {
                    ip = String(cString: buffer)
                    break
                }
            }
            ptr = ptr?.pointee.ai_next
        }
        freeaddrinfo(infoPointer)
        return ip
    }

    
    func isVPNConnectedWithAddress() -> Bool {
        do {
            let host = "apple.com"
            guard let reachability = SCNetworkReachabilityCreateWithName(nil, host) else {
                logs.writeLog(log: "[isVPNConnectedWithAddress] Couldn't create reachability reference")
                return false
            }
            
            var flags = SCNetworkReachabilityFlags()
            guard SCNetworkReachabilityGetFlags(reachability, &flags) else {
                logs.writeLog(log: "[isVPNConnectedWithAddress] Couldn't get reachability flags")
                return false
            }
            
            let isOnline = flags.contains(.reachable) && !flags.contains(.connectionRequired)
            if !isOnline {
                logs.writeLog(log: "[isVPNConnectedWithAddress] Not online, flags: \(flags)")
                return false
            }
            
            let isMobileNetwork = flags.contains(.isWWAN)
            let isTransientConnection = flags.contains(.transientConnection)
            
            if isMobileNetwork {
                if let settings = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any],
                   let scopes = settings["__SCOPED__"] as? [String: Any] {
                    for key in scopes.keys where key.contains("tap") || key.contains("tun") || key.contains("ppp") || key.contains("ipsec") || key.contains("utun") {
                        return true
                    }
                }
                return false
            } else {
                logs.writeLog(log: "[isVPNConnectedWithAddress] isOnline: \(isOnline), isTransientConnection: \(isTransientConnection)")
                return isOnline && isTransientConnection
            }
        } catch {
            logs.writeLog(log: "[isVPNConnectedWithAddress] ERROR: \(error.localizedDescription)")
            return false
        }
    }

    func isVPNConnected() -> Bool {
        do {
            guard let cfDict = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any],
                  let scoped = cfDict["__SCOPED__"] as? [String: Any] else {
                return false
            }
            
            logs.writeLog(log: "[isVPNConnected] scoped: \(scoped)")
            for key in scoped.keys where key.contains("tap") || key.contains("tun") || key.contains("ppp") || key.contains("ipsec") || key.contains("utun") {
                return true
            }
            return false
        } catch {
            logs.writeLog(log: "[isVPNConnected] ERROR: \(error.localizedDescription)")
            return false
        }
    }
    
    func socketPing(host: String, port: UInt16, completion: @escaping (Bool) -> Void) {
        do {
            let connection = NWConnection(host: NWEndpoint.Host(host),
                                          port: NWEndpoint.Port(rawValue: port)!,
                                          using: .tcp)
            connection.stateUpdateHandler = { state in
                switch state {
                case .ready:
                    completion(true); connection.cancel()
                case .failed(_):
                    completion(false); connection.cancel()
                default: break
                }
            }
            connection.start(queue: .main)
        } catch {
            logs.writeLog(log: "[socketPing] ERROR: \(error.localizedDescription)")
            completion(false)
        }
    }

    func httpPing(urlString: String, completion: @escaping (Bool, Int?, String?) -> Void) {
        do {
            let randomQuery = "?_=\(Int.random(in: 0...100000))"
            guard let url = URL(string: urlString + randomQuery) else {
                completion(false, nil, "Invalid URL")
                return
            }
            
            var request = URLRequest(url: url)
            request.httpMethod = "GET"
            request.timeoutInterval = 5
            request.cachePolicy = .reloadIgnoringLocalCacheData
            request.setValue("no-cache", forHTTPHeaderField: "Cache-Control")
            request.setValue("no-cache", forHTTPHeaderField: "Pragma")
            
            let task = URLSession.shared.dataTask(with: request) { data, response, error in
                if let error = error {
                    completion(false, nil, error.localizedDescription)
                    return
                }
                if let httpResponse = response as? HTTPURLResponse {
                    completion(true, httpResponse.statusCode, nil)
                } else {
                    completion(false, nil, "No HTTP response")
                }
            }
            task.resume()
        } catch {
            logs.writeLog(log: "[httpPing] ERROR: \(error.localizedDescription)")
            completion(false, nil, error.localizedDescription)
        }
    }

    
    func tcpPing(address: String) -> Result<Int32, Error> {
        do {
            var ret: Int32 = 0
            var err: NSError?
            let success = Cloak_outlineTcpPing(address, &ret, &err)
            
            if success { return .success(ret) }
            else if let error = err { return .failure(error) }
            else {
                return .failure(NSError(domain: "CloakTcpPing",
                                        code: -1,
                                        userInfo: [NSLocalizedDescriptionKey: "Unknown error"]))
            }
        } catch {
            logs.writeLog(log: "[tcpPing] ERROR: \(error.localizedDescription)")
            return .failure(error)
        }
    }

    func pingVPNServer() { safeCall { self.handlePing(address: "159.69.19.209:443", name: "VPNServer") } }
    func pingOnes()       { safeCall { self.handlePing(address: "1.1.1.1:80", name: "Ones") } }
    func pingGoogle()     { safeCall { self.handlePing(address: "8.8.8.8:53", name: "Google") } }
    func pingOnesWithDNS(){ safeCall { self.handlePing(address: "one.one.one.one:443", name: "OnesWithDNS") } }
    func pingGoogleWithDNS(){ safeCall { self.handlePing(address: "google.com:80", name: "GoogleWithDNS") } }

    private func handlePing(address: String, name: String) {
        switch tcpPing(address: address) {
        case .success(let ping):
            logs.writeLog(log: "[ping\(name)] Ping to \(address) takes: \(ping) ms")
        case .failure(let error):
            logs.writeLog(log: "[ping\(name)] Ping to \(address) error: \(error.localizedDescription)")
        }
    }
    
    private func safeCall(_ block: () throws -> Void) {
        do { try block() }
        catch { logs.writeLog(log: "[HealthCheck] ERROR in block: \(error.localizedDescription)") }
    }
    
    private func safeRepeat(_ times: Int, block: () throws -> Void) throws {
        for _ in 1...times {
            do { try block() }
            catch {
                logs.writeLog(log: "[HealthCheck] ERROR in loop: \(error.localizedDescription)")
            }
        }
    }
}
