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
    private var outlineStarted: Bool = false

    func startOutline(
        tunnelFileDescriptor: Int32,
        mtu: Int,
        nativeClientCreated: () -> Void
    ) throws {
        let start = Date()
        logs.writeLog(log: "[Outline] startOutline begin fd=\(tunnelFileDescriptor) mtu=\(mtu)")

        let methodPassword = configsRepository.getMethodPasswordOutline()
        let serverPort = configsRepository.getServerPort()
        let prefix = configsRepository.getPrefixOutline()
        let websocketEnabled = configsRepository.getIsWebsocketEnabled()
        let tcpPath = configsRepository.getTcpPathOutline()
        let udpPath = configsRepository.getUdpPathOutline()
        logs.writeLog(
            log: "Config snapshot: serverPort.len=\(serverPort.count) methodPassword.len=\(methodPassword.count) " +
            "ws=\(websocketEnabled) tcpPath.len=\(tcpPath.count) udpPath.len=\(udpPath.count) " +
            "tunnelFD=\(tunnelFileDescriptor) mtu=\(mtu)"
        )

        // Validate config early (prevents passing empty config into native layer).
        if methodPassword.isEmpty || serverPort.isEmpty {
            logs.writeLog(log: "[startTunnel] Empty Outline config: methodPassword.isEmpty=\(methodPassword.isEmpty) serverPort.isEmpty=\(serverPort.isEmpty) → abort")
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
            logs.writeLog(
                log: "WebSocket transport requested (wss) " +
                    "serverHost=\(maskStr(value: OutlineInteractor.extractHost(from: serverPort)))"
            )
        }

        var err: NSError?

        logs.writeLog(log: "[DEBUG][Outline] calling native NewVpnClient protocol=outline fd=\(tunnelFileDescriptor) mtu=\(mtu)")
        let newClientStart = Date()
        Cloak_outlineNewVpnClient(config, "outline", Int(tunnelFileDescriptor), mtu, &err)
        if let error = err {
            logs.writeLog(
                log: "[Outline] NewVpnClient failed in \(elapsedMs(since: newClientStart))ms: " +
                    "\(error.localizedDescription)"
            )
            throw error
        }
        logs.writeLog(log: "[Outline] NewVpnClient succeeded in \(elapsedMs(since: newClientStart))ms")
        nativeClientCreated()
        logs.writeLog(log: "[Outline] nativeClientCreated callback returned")

        logs.writeLog(log: "[DEBUG][Outline] calling native VpnConnect")
        let connectStart = Date()
        Cloak_outlineVpnConnect(&err)
        if let error = err {
            logs.writeLog(
                log: "[Outline] VpnConnect failed in \(elapsedMs(since: connectStart))ms: " +
                    "\(error.localizedDescription)"
            )
            throw error
        }
        outlineStarted = true
        logs.writeLog(
            log: "[Outline] VpnConnect succeeded in \(elapsedMs(since: connectStart))ms " +
                "totalStartMs=\(elapsedMs(since: start))"
        )
    }

    func stopOutline() throws {
        var err: NSError?

        logs.writeLog(log: "[DEBUG][Outline] calling native VpnDisconnect outlineStarted=\(outlineStarted)")
        let start = Date()
        Cloak_outlineVpnDisconnect(&err)
        if let error = err {
            logs.writeLog(
                log: "[Outline] VpnDisconnect failed in \(elapsedMs(since: start))ms: " +
                    "\(error.localizedDescription)"
            )
            throw error
        }
        outlineStarted = false
        logs.writeLog(log: "[DEBUG][Outline] VpnDisconnect returned in \(elapsedMs(since: start))ms")
    }

    func outlineStatus() -> String {
        var err: NSError?
        let start = Date()
        logs.writeLog(log: "[Outline] VpnStatus begin outlineStarted=\(outlineStarted)")
        let status = Cloak_outlineVpnStatus(&err)
        if let error = err {
            let message = "client=true localProxyAlive=false statusError=\(error.localizedDescription)"
            logs.writeLog(
                log: "[Outline] VpnStatus failed in \(elapsedMs(since: start))ms: " +
                    "\(error.localizedDescription)"
            )
            return message
        }
        logs.writeLog(log: "[Outline] VpnStatus returned in \(elapsedMs(since: start))ms status=\(status)")
        return status
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
            logs.writeLog(
                log: "[Outline] building WSS config effectiveHost=\(maskStr(value: effectiveHost)) " +
                    "tcpPath.len=\(tcpPath.count) udpPath.len=\(udpPath.count)"
            )

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

    private func elapsedMs(since start: Date) -> Int {
        Int(Date().timeIntervalSince(start) * 1000)
    }
}
