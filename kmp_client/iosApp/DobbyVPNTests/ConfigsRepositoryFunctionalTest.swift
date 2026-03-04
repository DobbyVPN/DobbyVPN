import XCTest

final class ConfigsRepositoryFunctionalTest: XCTestCase {
    
    private var userDefaults: UserDefaults!
    private var repository: TestableConfigsRepository!
    
    override func setUp() {
        super.setUp()
        userDefaults = UserDefaults(suiteName: "com.dobby.test.\(UUID().uuidString)")!
        repository = TestableConfigsRepository(userDefaults: userDefaults)
    }
    
    override func tearDown() {
        userDefaults.removePersistentDomain(forName: userDefaults.description)
        userDefaults = nil
        repository = nil
        super.tearDown()
    }
    
    // MARK: - Connection URL tests
    
    func testConnectionURLRoundTrip() {
        repository.setConnectionURL(connectionURL: "https://example.com/config")
        XCTAssertEqual(repository.getConnectionURL(), "https://example.com/config")
    }
    
    func testConnectionURLDefaultEmpty() {
        XCTAssertEqual(repository.getConnectionURL(), "")
    }
    
    // MARK: - Connection Config tests
    
    func testConnectionConfigRoundTrip() {
        let config = """
        {"server": "1.2.3.4", "port": 443}
        """
        repository.setConnectionConfig(connectionConfig: config)
        XCTAssertEqual(repository.getConnectionConfig(), config)
    }
    
    // MARK: - Cloak Config tests
    
    func testCloakConfigRoundTrip() {
        let config = """
        {"RemoteHost": "proxy.example.com", "Port": 443}
        """
        repository.setCloakConfig(newConfig: config)
        XCTAssertEqual(repository.getCloakConfig(), config)
    }
    
    func testCloakConfigDefaultEmpty() {
        XCTAssertEqual(repository.getCloakConfig(), "")
    }
    
    // MARK: - Cloak Enabled tests
    
    func testIsCloakEnabledRoundTrip() {
        repository.setIsCloakEnabled(isCloakEnabled: true)
        XCTAssertTrue(repository.getIsCloakEnabled())
        
        repository.setIsCloakEnabled(isCloakEnabled: false)
        XCTAssertFalse(repository.getIsCloakEnabled())
    }
    
    func testIsCloakEnabledDefaultFalse() {
        XCTAssertFalse(repository.getIsCloakEnabled())
    }
    
    // MARK: - Cloak Local Port tests
    
    func testCloakLocalPortRoundTrip() {
        repository.setCloakLocalPort(port: 2000)
        XCTAssertEqual(repository.getCloakLocalPort(), 2000)
    }
    
    func testCloakLocalPortDefault1984() {
        XCTAssertEqual(repository.getCloakLocalPort(), 1984)
    }
    
    // MARK: - Outline Server Port tests
    
    func testServerPortOutlineRoundTrip() {
        repository.setServerPortOutline(newConfig: "1.2.3.4:443")
        XCTAssertEqual(repository.getServerPortOutline(), "1.2.3.4:443")
    }
    
    // MARK: - Outline Method Password tests
    
    func testMethodPasswordOutlineRoundTrip() {
        repository.setMethodPasswordOutline(newConfig: "aes-256-gcm:secret")
        XCTAssertEqual(repository.getMethodPasswordOutline(), "aes-256-gcm:secret")
    }
    
    // MARK: - Outline Enabled tests
    
    func testIsOutlineEnabledRoundTrip() {
        repository.setIsOutlineEnabled(isOutlineEnabled: true)
        XCTAssertTrue(repository.getIsOutlineEnabled())
    }
    
    // MARK: - Prefix tests
    
    func testPrefixOutlineRoundTrip() {
        repository.setPrefixOutline(prefix: "HTTP/1.1")
        XCTAssertEqual(repository.getPrefixOutline(), "HTTP/1.1")
    }
    
    func testPrefixOutlineDefaultEmpty() {
        XCTAssertEqual(repository.getPrefixOutline(), "")
    }
    
    // MARK: - WebSocket tests
    
    func testIsWebsocketEnabledRoundTrip() {
        repository.setIsWebsocketEnabled(enabled: true)
        XCTAssertTrue(repository.getIsWebsocketEnabled())
        
        repository.setIsWebsocketEnabled(enabled: false)
        XCTAssertFalse(repository.getIsWebsocketEnabled())
    }
    
    func testIsWebsocketEnabledDefaultFalse() {
        XCTAssertFalse(repository.getIsWebsocketEnabled())
    }
    
    // MARK: - TCP/UDP Path tests
    
    func testTcpPathOutlineRoundTrip() {
        repository.setTcpPathOutline(tcpPath: "/ws/tcp")
        XCTAssertEqual(repository.getTcpPathOutline(), "/ws/tcp")
    }
    
    func testUdpPathOutlineRoundTrip() {
        repository.setUdpPathOutline(udpPath: "/ws/udp")
        XCTAssertEqual(repository.getUdpPathOutline(), "/ws/udp")
    }
    
    // MARK: - User Init Stop tests
    
    func testIsUserInitStopRoundTrip() {
        repository.setIsUserInitStop(isUserInitStop: true)
        XCTAssertTrue(repository.getIsUserInitStop())
        
        repository.setIsUserInitStop(isUserInitStop: false)
        XCTAssertFalse(repository.getIsUserInitStop())
    }
    
    func testIsUserInitStopDefaultFalse() {
        XCTAssertFalse(repository.getIsUserInitStop())
    }
    
    // MARK: - Multiple values persistence
    
    func testMultipleValuesPersistedIndependently() {
        repository.setCloakConfig(newConfig: "cloak-config")
        repository.setServerPortOutline(newConfig: "1.2.3.4:443")
        repository.setMethodPasswordOutline(newConfig: "method:pass")
        repository.setIsCloakEnabled(isCloakEnabled: true)
        repository.setCloakLocalPort(port: 3000)
        
        XCTAssertEqual(repository.getCloakConfig(), "cloak-config")
        XCTAssertEqual(repository.getServerPortOutline(), "1.2.3.4:443")
        XCTAssertEqual(repository.getMethodPasswordOutline(), "method:pass")
        XCTAssertTrue(repository.getIsCloakEnabled())
        XCTAssertEqual(repository.getCloakLocalPort(), 3000)
    }
}

// MARK: - Testable repository implementation (mirrors DobbyConfigsRepositoryImpl)

class TestableConfigsRepository {
    private var userDefaults: UserDefaults
    
    private let cloakConfigKey = "cloakConfigKey"
    private let isCloakEnabledKey = "isCloakEnabledKey"
    private let cloakLocalPortKey = "cloakLocalPortKey"
    private let methodPasswordOutlineKey = "MethodPasswordOutlineKey"
    private let serverPortOutlineKey = "ServerPortOutlineKey"
    private let isOutlineEnabledKey = "isOutlineEnabledKey"
    private let connectionURLKey = "connectionURLKey"
    private let connectionConfigKey = "connectionConfigKey"
    private let prefixOutlineKey = "PrefixOutlineKey"
    private let tcpPathOutlineKey = "TcpPathOutlineKey"
    private let isWebsocketEnabledKey = "isWebsocketEnabledKey"
    private let udpPathOutlineKey = "UdpPathOutlineKey"
    private let isUserInitStopKey = "isUserInitStopKey"
    
    init(userDefaults: UserDefaults) {
        self.userDefaults = userDefaults
    }
    
    func getConnectionURL() -> String {
        return userDefaults.string(forKey: connectionURLKey) ?? ""
    }
    
    func setConnectionURL(connectionURL: String) {
        userDefaults.set(connectionURL, forKey: connectionURLKey)
    }
    
    func getConnectionConfig() -> String {
        return userDefaults.string(forKey: connectionConfigKey) ?? ""
    }
    
    func setConnectionConfig(connectionConfig: String) {
        userDefaults.set(connectionConfig, forKey: connectionConfigKey)
    }
    
    func getCloakConfig() -> String {
        return userDefaults.string(forKey: cloakConfigKey) ?? ""
    }
    
    func setCloakConfig(newConfig: String) {
        userDefaults.set(newConfig, forKey: cloakConfigKey)
    }
    
    func getIsCloakEnabled() -> Bool {
        return userDefaults.bool(forKey: isCloakEnabledKey)
    }
    
    func setIsCloakEnabled(isCloakEnabled: Bool) {
        userDefaults.set(isCloakEnabled, forKey: isCloakEnabledKey)
    }
    
    func getCloakLocalPort() -> Int32 {
        let portValue = userDefaults.object(forKey: cloakLocalPortKey) as? Int ?? 1984
        return Int32(portValue)
    }
    
    func setCloakLocalPort(port: Int32) {
        userDefaults.set(Int(port), forKey: cloakLocalPortKey)
    }
    
    func getServerPortOutline() -> String {
        return userDefaults.string(forKey: serverPortOutlineKey) ?? ""
    }
    
    func setServerPortOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: serverPortOutlineKey)
    }
    
    func getMethodPasswordOutline() -> String {
        return userDefaults.string(forKey: methodPasswordOutlineKey) ?? ""
    }
    
    func setMethodPasswordOutline(newConfig: String) {
        userDefaults.set(newConfig, forKey: methodPasswordOutlineKey)
    }
    
    func getIsOutlineEnabled() -> Bool {
        return userDefaults.bool(forKey: isOutlineEnabledKey)
    }
    
    func setIsOutlineEnabled(isOutlineEnabled: Bool) {
        userDefaults.set(isOutlineEnabled, forKey: isOutlineEnabledKey)
    }
    
    func getPrefixOutline() -> String {
        return userDefaults.string(forKey: prefixOutlineKey) ?? ""
    }
    
    func setPrefixOutline(prefix: String) {
        userDefaults.set(prefix, forKey: prefixOutlineKey)
    }
    
    func getTcpPathOutline() -> String {
        return userDefaults.string(forKey: tcpPathOutlineKey) ?? ""
    }
    
    func setTcpPathOutline(tcpPath: String) {
        userDefaults.set(tcpPath, forKey: tcpPathOutlineKey)
    }
    
    func getIsWebsocketEnabled() -> Bool {
        return userDefaults.bool(forKey: isWebsocketEnabledKey)
    }
    
    func setIsWebsocketEnabled(enabled: Bool) {
        userDefaults.set(enabled, forKey: isWebsocketEnabledKey)
    }
    
    func getUdpPathOutline() -> String {
        return userDefaults.string(forKey: udpPathOutlineKey) ?? ""
    }
    
    func setUdpPathOutline(udpPath: String) {
        userDefaults.set(udpPath, forKey: udpPathOutlineKey)
    }
    
    func getIsUserInitStop() -> Bool {
        return userDefaults.bool(forKey: isUserInitStopKey)
    }
    
    func setIsUserInitStop(isUserInitStop: Bool) {
        userDefaults.set(isUserInitStop, forKey: isUserInitStopKey)
    }
}
