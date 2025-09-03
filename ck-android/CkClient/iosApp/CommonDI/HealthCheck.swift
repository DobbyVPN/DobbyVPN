//
//  HealthCheck.swift
//  iosApp
//
//  Created by Irina on 03.09.2025.
//  Copyright © 2025 orgName. All rights reserved.
//
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
    public static let shared = HealthCheck()
    
    public func fullCheckUp() {
        self.logs.writeLog(log: "[fullCheckUp] START Check UP")
        for _ in 1...4 { pingVPNServer() }
        for _ in 1...4 { pingGoogle() }
        for _ in 1...4 { pingOnes() }
        
        for _ in 1...4 {
            let ip = resolveDNS(host: "one.one.one.one")
            self.logs.writeLog(log: "[resolveDNS] one.one.one.one -> \(ip)")
        }
        
        for _ in 1...4 {
            let ip = resolveDNS(host: "google.com")
            self.logs.writeLog(log: "[resolveDNS] google.com -> \(ip)")
        }
        
        for _ in 1...4 { pingGoogleWithDNS() }
        
        for _ in 1...4 { pingOnesWithDNS() }
        
        for _ in 1...4 {
            let status = isVPNConnected()
            self.logs.writeLog(log: "[isVPNConnected] isVPNConnected: \(status)")
        }
        
        for _ in 1...4 {
            let status = isVPNConnectedWithAddress()
            self.logs.writeLog(log: "[isVPNConnectedWithAddress] isVPNConnectedWithAddress: \(status)")
        }
        self.logs.writeLog(log: "[fullCheckUp] FINISH Check UP")
    }

    
    func resolveDNS(host: String) -> String {
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
            let errorString = String(cString: gai_strerror(status))
            return "\(errorString)"
        }
        
        var ip: String = "Can't resolve DNS, no info"
        
        var ptr: UnsafeMutablePointer<addrinfo>? = first
        while ptr != nil {
            if let addr = ptr?.pointee.ai_addr {
                var buffer = [CChar](repeating: 0, count: Int(NI_MAXHOST))
                let result = getnameinfo(addr, socklen_t(ptr!.pointee.ai_addrlen),
                                         &buffer, socklen_t(buffer.count),
                                         nil, 0, NI_NUMERICHOST)
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
        let host = "apple.com"
        guard let reachability = SCNetworkReachabilityCreateWithName(nil, host) else {
            self.logs.writeLog(log: "[isVPNConnectedWithAddress] Couldn't create reachability reference")
            return false
        }
        var flags = SCNetworkReachabilityFlags()
        if !SCNetworkReachabilityGetFlags(reachability, &flags) {
            self.logs.writeLog(log: "[isVPNConnectedWithAddress] Couldn't get reachability flags")
            return false
        }
        
        let isOnline = flags.contains(.reachable) && !flags.contains(.connectionRequired)
        if !isOnline {
            self.logs.writeLog(log: "[isVPNConnectedWithAddress] Not online, flag: \(flags)")
            return false
        }
        
        let isMobileNetwork = flags.contains(.isWWAN)
        let isTransientConnection = flags.contains(.transientConnection)
        
        if isMobileNetwork {
            if let settings = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any],
               let scopes = settings["__SCOPED__"] as? [String: Any] {
                for key in scopes.keys {
                    if key.contains("tap") || key.contains("tun") ||
                       key.contains("ppp") || key.contains("ipsec") ||
                       key.contains("utun") {
                        return true
                    }
                }
            }
            return false
        } else {
            self.logs.writeLog(log: "[isVPNConnectedWithAddress] isOnline: \(isOnline), isTransientConnection: \(isTransientConnection)")
            return isOnline && isTransientConnection
        }
    }

    
    func isVPNConnected() -> Bool {
        guard let cfDict = CFNetworkCopySystemProxySettings()?.takeRetainedValue() as? [String: Any],
              let scoped = cfDict["__SCOPED__"] as? [String: Any] else {
            return false
        }
        
        self.logs.writeLog(log: "[isVPNConnected] scoped: \(scoped)")
        
        for key in scoped.keys {
            if key.contains("tap") ||
               key.contains("tun") ||
               key.contains("ppp") ||
               key.contains("ipsec") ||
               key.contains("utun") {
                return true
            }
        }
        
        return false
    }
    
    func pingVPNServer() {
        switch tcpPing(address: "159.69.19.209:443") {
        case .success(let ping):
            self.logs.writeLog(log: "[pingOnes] Ping to 159.69.19.209:443 takes : \(ping) ms")
        case .failure(let error):
            self.logs.writeLog(log: "[pingOnes] Ping to 159.69.19.209:443 error: \(error.localizedDescription)")
        }
    }
    
    func pingOnes() {
        switch tcpPing(address: "1.1.1.1:80") {
        case .success(let ping):
            self.logs.writeLog(log: "[pingOnes] Ping to 1.1.1.1:80 takes : \(ping) ms")
        case .failure(let error):
            self.logs.writeLog(log: "[pingOnes] Ping to 1.1.1.1:80 error: \(error.localizedDescription)")
        }
    }
    
    func pingGoogle() {
        switch tcpPing(address: "8.8.8.8:53") {
        case .success(let ping):
            self.logs.writeLog(log: "[pingGoogle] Ping to 8.8.8.8:53 takes : \(ping) ms")
        case .failure(let error):
            self.logs.writeLog(log: "[pingGoogle] Ping to 8.8.8.8:53 error: \(error.localizedDescription)")
        }
    }
    
    func pingOnesWithDNS() {
        switch tcpPing(address: "one.one.one.one:443") {
        case .success(let ping):
            self.logs.writeLog(log: "[pingOnesWithDNS] Ping to one.one.one.one:443 takes : \(ping) ms")
        case .failure(let error):
            self.logs.writeLog(log: "[pingOnesWithDNS] Ping to one.one.one.one:443 error: \(error.localizedDescription)")
        }
    }
    
    func pingGoogleWithDNS() {
        switch tcpPing(address: "google.com:80") {
        case .success(let ping):
            self.logs.writeLog(log: "[pingGoogleWithDNS] Ping to google.com:80 takes : \(ping) ms")
        case .failure(let error):
            self.logs.writeLog(log: "[pingGoogleWithDNS] Ping to google.com:80 error: \(error.localizedDescription)")
        }
    }
    
    func tcpPing(address: String) -> Result<Int32, Error> {
        var ret: Int32 = 0
        var err: NSError?

        let success = Cloak_outlineTcpPing(address, &ret, &err)

        if success {
            return .success(ret)
        } else if let error = err {
            return .failure(error)
        } else {
            return .failure(NSError(domain: "CloakTcpPing", code: -1, userInfo: [NSLocalizedDescriptionKey: "Unknown error"]))
        }
    }
}
