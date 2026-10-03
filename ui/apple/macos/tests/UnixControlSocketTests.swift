import Darwin
@testable import DobbyVPNMacApp
import Foundation
import XCTest

final class UnixControlSocketTests: XCTestCase {
    func testExchangeAndExplicitDevelopmentEndpoint() throws {
        try withServer { path, descriptor in
            let finished = expectation(description: "server finished")
            DispatchQueue.global().async {
                defer { finished.fulfill() }
                let client = accept(descriptor, nil, nil)
                guard client >= 0 else { return XCTFail("accept errno=\(errno)") }
                defer { _ = close(client) }
                var bytes = [UInt8](repeating: 0, count: 1024)
                XCTAssertGreaterThan(recv(client, &bytes, bytes.count, 0), 0)
                let reply = #"{"ok":true,"result":{"primary_action":"START"}}"# + "\n"
                reply.withCString { XCTAssertEqual(send(client, $0, strlen($0), 0), reply.utf8.count) }
            }
            let client = MacDesktopSessionClient(environment: ["DOBBYVPN_CONTROL_SOCKET": path])
            let result = client.call("Snapshot", parameters: [:])
            XCTAssertTrue(result.contains(#""primary_action":"START""#), result)
            wait(for: [finished], timeout: 2)
        }
    }

    func testSilentServerConsumesOneRequestBudget() throws {
        try withServer { path, descriptor in
            let finished = expectation(description: "server finished")
            DispatchQueue.global().async {
                defer { finished.fulfill() }
                let client = accept(descriptor, nil, nil)
                guard client >= 0 else { return XCTFail("accept errno=\(errno)") }
                defer { _ = close(client) }
                // The client must time out while the connected peer stays open.
                Thread.sleep(forTimeInterval: 0.3)
            }
            let start = ProcessInfo.processInfo.systemUptime
            XCTAssertThrowsError(try UnixControlSocket(path: path, requestTimeout: 0.05).exchange(Data("request\n".utf8))) {
                XCTAssertEqual(($0 as? POSIXError)?.code, .ETIMEDOUT)
            }
            XCTAssertLessThan(ProcessInfo.processInfo.systemUptime - start, 0.25)
            wait(for: [finished], timeout: 2)
        }
    }

    func testConnectionFailurePreservesOriginalPOSIXCause() {
        XCTAssertThrowsError(try UnixControlSocket(path: "/tmp/dobby-missing-\(UUID().uuidString)").exchange(Data())) {
            XCTAssertEqual(($0 as? POSIXError)?.code, .ENOENT)
        }
    }

    private func withServer(_ body: (String, Int32) throws -> Void) throws {
        let path = "/tmp/dobby-control-\(UUID().uuidString)"
        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        XCTAssertGreaterThanOrEqual(descriptor, 0)
        defer { _ = close(descriptor); _ = unlink(path) }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: path.utf8) }
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.bind(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0, listen(descriptor, 1) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        try body(path, descriptor)
    }
}
