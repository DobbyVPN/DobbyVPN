import XCTest

final class HostExtractionFunctionalTest: XCTestCase {
    
    // MARK: - extractHost tests (from OutlineInteractor)
    
    func testExtractHostFromIPv4WithPort() {
        XCTAssertEqual(extractHost(from: "1.2.3.4:443"), "1.2.3.4")
    }
    
    func testExtractHostFromIPv6WithPort() {
        XCTAssertEqual(extractHost(from: "[2001:db8::1]:443"), "2001:db8::1")
    }
    
    func testExtractHostFromDomainWithPort() {
        XCTAssertEqual(extractHost(from: "example.org:8443"), "example.org")
    }
    
    func testExtractHostStripsQueryParams() {
        XCTAssertEqual(extractHost(from: "host:port?query=1"), "host")
    }
    
    func testExtractHostTrimsWhitespace() {
        XCTAssertEqual(extractHost(from: "  host:443  "), "host")
    }
    
    func testExtractHostNoPort() {
        XCTAssertEqual(extractHost(from: "no-port-at-all"), "no-port-at-all")
    }
    
    func testExtractHostEmptyString() {
        XCTAssertEqual(extractHost(from: ""), "")
    }
    
    func testExtractHostIPv6NoBrackets() {
        let result = extractHost(from: "2001:db8::1:443")
        XCTAssertEqual(result, "2001:db8::1:443")
    }
    
    // MARK: - extractIP tests (from PacketTunnelProvider)
    
    func testExtractIPFromServerPort() {
        XCTAssertEqual(extractIP(from: "1.2.3.4:443"), "1.2.3.4")
    }
    
    func testExtractIPEmptyString() {
        XCTAssertNil(extractIP(from: ""))
    }
    
    func testExtractIPNoPort() {
        XCTAssertEqual(extractIP(from: "host-no-port"), "host-no-port")
    }
    
    func testExtractIPWithMultipleColons() {
        XCTAssertEqual(extractIP(from: "host:port:extra"), "host")
    }
    
    // MARK: - extractRemoteHost tests (from PacketTunnelProvider)
    
    func testExtractRemoteHostValidJSON() {
        let json = """
        {"RemoteHost": "proxy.example.com", "Port": 443}
        """
        XCTAssertEqual(extractRemoteHost(from: json), "proxy.example.com")
    }
    
    func testExtractRemoteHostEmptyString() {
        XCTAssertNil(extractRemoteHost(from: ""))
    }
    
    func testExtractRemoteHostNoRemoteHostKey() {
        let json = """
        {"Host": "example.com", "Port": 443}
        """
        XCTAssertNil(extractRemoteHost(from: json))
    }
    
    func testExtractRemoteHostInvalidJSON() {
        XCTAssertNil(extractRemoteHost(from: "not valid json"))
    }
    
    func testExtractRemoteHostEmptyRemoteHost() {
        let json = """
        {"RemoteHost": "", "Port": 443}
        """
        XCTAssertNil(extractRemoteHost(from: json))
    }
    
    func testExtractRemoteHostNullValue() {
        let json = """
        {"RemoteHost": null, "Port": 443}
        """
        XCTAssertNil(extractRemoteHost(from: json))
    }
    
    // MARK: - isValidIPv4 tests
    
    func testIsValidIPv4Valid() {
        XCTAssertTrue(isValidIPv4("192.168.1.1"))
        XCTAssertTrue(isValidIPv4("0.0.0.0"))
        XCTAssertTrue(isValidIPv4("255.255.255.255"))
        XCTAssertTrue(isValidIPv4("10.0.0.1"))
    }
    
    func testIsValidIPv4Invalid() {
        XCTAssertFalse(isValidIPv4("256.1.1.1"))
        XCTAssertFalse(isValidIPv4("1.2.3"))
        XCTAssertFalse(isValidIPv4("1.2.3.4.5"))
        XCTAssertFalse(isValidIPv4("abc.def.ghi.jkl"))
        XCTAssertFalse(isValidIPv4(""))
        XCTAssertFalse(isValidIPv4("example.com"))
    }
}

// MARK: - Testable functions extracted from production code

func extractHost(from hostPortMaybeWithQuery: String) -> String {
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

func extractIP(from serverPort: String) -> String? {
    guard !serverPort.isEmpty else { return nil }
    return serverPort.split(separator: ":").first.map(String.init)
}

func extractRemoteHost(from cloakConfig: String) -> String? {
    guard
        !cloakConfig.isEmpty,
        let data = cloakConfig.data(using: .utf8),
        let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
        let remoteHost = json["RemoteHost"] as? String,
        !remoteHost.isEmpty
    else {
        return nil
    }
    return remoteHost
}

func isValidIPv4(_ s: String) -> Bool {
    let parts = s.split(separator: ".")
    guard parts.count == 4 else { return false }
    for p in parts {
        guard let n = Int(p), (0...255).contains(n) else { return false }
    }
    return true
}
