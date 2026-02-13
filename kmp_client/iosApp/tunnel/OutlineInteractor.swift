import NetworkExtension
import CommonDI
import MyLibrary
import os
import app
import Foundation
import SystemConfiguration
import Network

public final class OutlineInteractor {
    private var logs = NativeModuleHolder.logsRepository

    func startOutline() throws {
        
        let methodPassword = configsRepository.getMethodPasswordOutline()
        let serverPort = configsRepository.getServerPortOutline()
        let prefix = configsRepository.getPrefixOutline()
        let websocketEnabled = configsRepository.getIsWebsocketEnabled()
        let tcpPath = configsRepository.getTcpPathOutline()
        let udpPath = configsRepository.getUdpPathOutline()
        logs.writeLog(log: "Config snapshot: serverPort.len=\(serverPort.count) methodPassword.len=\(methodPassword.count) ws=\(websocketEnabled) tcpPath.len=\(tcpPath.count) udpPath.len=\(udpPath.count)")

        // Validate config early (prevents passing empty config into native layer).
        if methodPassword.isEmpty || serverPort.isEmpty {
            logs.writeLog(log: "[startTunnel] Empty Outline config (methodPassword/serverPort) → abort")
            throw NSError(
                domain: "PacketTunnelProvider",
                code: -2,
                userInfo: [NSLocalizedDescriptionKey: "Empty Outline configuration"]
            )
        }
        
        let config = buildOutlineConfig(
            methodPassword: methodPassword,
            serverPort: serverPort,
            prefix: prefix,
            websocketEnabled: websocketEnabled,
            tcpPath: tcpPath,
            udpPath: udpPath
        )
        logs.writeLog(log: "Outline config built (prefix=\(!prefix.isEmpty), ws=\(websocketEnabled), tcpPath=\(!tcpPath.isEmpty), udpPath=\(!udpPath.isEmpty))")
        if websocketEnabled {
            logs.writeLog(log: "WebSocket transport requested (wss)")
        }
        
        var err: NSError?

        Cloak_outlineNewOutlineClient(config, &err)
        if let error = err {
            throw error
        }

        Cloak_outlineOutlineConnect(&err)
        if let error = err {
            throw error
        }
    }

    func stopOutline() throws {
        var err: NSError?

        Cloak_outlineOutlineDisconnect(&err)
        if let error = err {
            throw error
        }
    }
    
    func buildOutlineConfig(
        methodPassword: String,
        serverPort: String,
        prefix: String = "",
        websocketEnabled: Bool = false,
        tcpPath: String = "",
        udpPath: String = ""
    ) -> String {
        let encoded = methodPassword.data(using: .utf8)?.base64EncodedString() ?? ""
        let baseUrl = "ss://\(encoded)@\(serverPort)"

        func extractHost(_ hostPortMaybeWithQuery: String) -> String {
            let hostPort = hostPortMaybeWithQuery.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: true).first.map(String.init) ?? hostPortMaybeWithQuery
            let trimmed = hostPort.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.hasPrefix("[") {
                // IPv6 wrapped in square brackets: [2001:db8::1]:443
                if let start = trimmed.firstIndex(of: "["), let end = trimmed.firstIndex(of: "]"), start < end {
                    return String(trimmed[trimmed.index(after: start)..<end])
                }
            }
            if let lastColon = trimmed.lastIndex(of: ":"), trimmed.filter({ $0 == ":" }).count == 1 {
                return String(trimmed[..<lastColon])
            }
            return trimmed
        }

        // Add the `prefix` query param if present (URL-encoded)
        let ssUrl: String
        if !prefix.isEmpty {
            let separator = serverPort.contains("?") ? "&" : "?"
            let encodedPrefix = prefix.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? prefix
            ssUrl = "\(baseUrl)\(separator)prefix=\(encodedPrefix)"
        } else {
            ssUrl = baseUrl
        }

        // If WebSocket is enabled, wrap the Shadowsocks URL into WebSocket-over-TLS transport (wss://)
        if websocketEnabled {
            let effectiveHost = extractHost(serverPort).trimmingCharacters(in: .whitespacesAndNewlines)

            var wsParams: [String] = []
            // outline-sdk v0.0.16: ws: does not support the `host=` option ("Unsupported option host")
            // Domain-related routing should be configured via TLS SNI (tls:sni=...).
            if !tcpPath.isEmpty { wsParams.append("tcp_path=\(tcpPath)") }
            if !udpPath.isEmpty { wsParams.append("udp_path=\(udpPath)") }
            
            let wsParamsStr = wsParams.joined(separator: "&")
            // Use tls:sni|ws: for WebSocket-over-TLS (wss://) via SNI
            let tlsPrefix = "tls:sni=\(effectiveHost)"
            if !wsParamsStr.isEmpty {
                return "\(tlsPrefix)|ws:\(wsParamsStr)|\(ssUrl)"
            } else {
                return "\(tlsPrefix)|ws:|\(ssUrl)"
            }
        } else {
            return ssUrl
        }
    }
    
    public static func extractHost(from hostPortMaybeWithQuery: String) -> String {
        let hostPort = hostPortMaybeWithQuery.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: true).first.map(String.init) ?? hostPortMaybeWithQuery
        let trimmed = hostPort.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("[") {
            // IPv6 in brackets: [2001:db8::1]:443
            if let start = trimmed.firstIndex(of: "["), let end = trimmed.firstIndex(of: "]"), start < end {
                return String(trimmed[trimmed.index(after: start)..<end])
            }
        }
        if let lastColon = trimmed.lastIndex(of: ":"), trimmed.filter({ $0 == ":" }).count == 1 {
            return String(trimmed[..<lastColon])
        }
        return trimmed
    }
}
