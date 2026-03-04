import XCTest

final class OutlineConfigBuildFunctionalTest: XCTestCase {
    
    func testBasicSsUrlGeneration() {
        let config = buildOutlineConfig(
            methodPassword: "aes-256-gcm:secret123",
            serverPort: "1.2.3.4:443",
            prefix: "",
            websocketEnabled: false,
            tcpPath: "",
            udpPath: ""
        )
        
        let encoded = "aes-256-gcm:secret123".data(using: .utf8)!.base64EncodedString()
        XCTAssertEqual(config, "ss://\(encoded)@1.2.3.4:443")
    }
    
    func testPrefixAppendedAsQueryParam() {
        let config = buildOutlineConfig(
            methodPassword: "chacha20:pass",
            serverPort: "example.org:8443",
            prefix: "HTTP/1.1",
            websocketEnabled: false,
            tcpPath: "",
            udpPath: ""
        )
        
        let encoded = "chacha20:pass".data(using: .utf8)!.base64EncodedString()
        let encodedPrefix = "HTTP/1.1".addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed)!
        XCTAssertEqual(config, "ss://\(encoded)@example.org:8443?prefix=\(encodedPrefix)")
    }
    
    func testWebsocketEnabledWrapsInTlsWs() {
        let config = buildOutlineConfig(
            methodPassword: "aes-128-gcm:key",
            serverPort: "ws.example.com:443",
            prefix: "",
            websocketEnabled: true,
            tcpPath: "",
            udpPath: ""
        )
        
        let encoded = "aes-128-gcm:key".data(using: .utf8)!.base64EncodedString()
        XCTAssertTrue(config.hasPrefix("tls:sni=ws.example.com|ws:|"))
        XCTAssertTrue(config.hasSuffix("ss://\(encoded)@ws.example.com:443"))
    }
    
    func testWebsocketWithTcpAndUdpPaths() {
        let config = buildOutlineConfig(
            methodPassword: "aes-256-gcm:secret",
            serverPort: "server.io:443",
            prefix: "",
            websocketEnabled: true,
            tcpPath: "/tcp",
            udpPath: "/udp"
        )
        
        XCTAssertTrue(config.contains("tls:sni=server.io"))
        XCTAssertTrue(config.contains("ws:tcp_path=/tcp&udp_path=/udp"))
    }
    
    func testWebsocketWithOnlyTcpPath() {
        let config = buildOutlineConfig(
            methodPassword: "method:pass",
            serverPort: "host.com:443",
            prefix: "",
            websocketEnabled: true,
            tcpPath: "/ws/tcp",
            udpPath: ""
        )
        
        XCTAssertTrue(config.contains("ws:tcp_path=/ws/tcp|"))
        XCTAssertFalse(config.contains("udp_path"))
    }
    
    func testEmptyPrefixIsOmitted() {
        let config = buildOutlineConfig(
            methodPassword: "method:pass",
            serverPort: "1.2.3.4:443",
            prefix: "",
            websocketEnabled: false,
            tcpPath: "",
            udpPath: ""
        )
        
        XCTAssertFalse(config.contains("prefix="))
        XCTAssertFalse(config.contains("?"))
    }
    
    func testPrefixWithSpecialCharsIsUrlEncoded() {
        let config = buildOutlineConfig(
            methodPassword: "m:p",
            serverPort: "1.2.3.4:443",
            prefix: "GET / HTTP/1.1\r\nHost: example.com",
            websocketEnabled: false,
            tcpPath: "",
            udpPath: ""
        )
        
        XCTAssertTrue(config.contains("prefix="))
        XCTAssertFalse(config.contains("\r\n"))
    }
    
    func testWebsocketWithPrefixCombined() {
        let config = buildOutlineConfig(
            methodPassword: "method:pass",
            serverPort: "server.com:443",
            prefix: "test-prefix",
            websocketEnabled: true,
            tcpPath: "/path",
            udpPath: ""
        )
        
        XCTAssertTrue(config.hasPrefix("tls:sni=server.com"))
        XCTAssertTrue(config.contains("prefix=test-prefix"))
    }
}

// MARK: - Testable function extracted from OutlineInteractor

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
            if let start = trimmed.firstIndex(of: "["), let end = trimmed.firstIndex(of: "]"), start < end {
                return String(trimmed[trimmed.index(after: start)..<end])
            }
        }
        if let lastColon = trimmed.lastIndex(of: ":"), trimmed.filter({ $0 == ":" }).count == 1 {
            return String(trimmed[..<lastColon])
        }
        return trimmed
    }

    let ssUrl: String
    if !prefix.isEmpty {
        let separator = serverPort.contains("?") ? "&" : "?"
        let encodedPrefix = prefix.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? prefix
        ssUrl = "\(baseUrl)\(separator)prefix=\(encodedPrefix)"
    } else {
        ssUrl = baseUrl
    }

    if websocketEnabled {
        let effectiveHost = extractHost(serverPort).trimmingCharacters(in: .whitespacesAndNewlines)

        var wsParams: [String] = []
        if !tcpPath.isEmpty { wsParams.append("tcp_path=\(tcpPath)") }
        if !udpPath.isEmpty { wsParams.append("udp_path=\(udpPath)") }
        
        let wsParamsStr = wsParams.joined(separator: "&")
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
