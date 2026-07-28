import Foundation
import XCTest
@testable import IOSLifecycleCore

private final class TestClock: IOSSessionClock {
    var now = Date(timeIntervalSince1970: 0)

    func sleep(seconds: TimeInterval) async throws {
        now.addTimeInterval(seconds)
    }
}

private enum TestError: Error {
    case rejected
}

private final class TestClient: IOSProviderSessionClient {
    var calls: [String] = []
    var snapshots: [IOSProviderSessionSnapshot] = []
    var stopError: Error?
    var destroyError: Error?

    func create() throws -> String {
        calls.append("create")
        return "session"
    }

    func configure(sessionID: String, rawConfiguration: Data) throws {
        calls.append("configure")
    }

    func start(sessionID: String) throws -> Int64 {
        calls.append("start")
        return 7
    }

    func snapshot(sessionID: String) throws -> IOSProviderSessionSnapshot {
        calls.append("snapshot")
        if snapshots.count > 1 {
            return snapshots.removeFirst()
        }
        return snapshots.first ?? IOSProviderSessionSnapshot(
            generation: 7,
            state: "CONNECTED",
            cleanupComplete: false
        )
    }

    func stop(sessionID: String, generation: Int64) throws {
        calls.append("stop")
        if let stopError { throw stopError }
    }

    func destroy(sessionID: String) throws {
        calls.append("destroy")
        if let destroyError { throw destroyError }
    }
}

final class IOSProviderSessionCoordinatorTests: XCTestCase {
    func testStartWaitsForMatchingConnectedGeneration() async throws {
        let client = TestClient()
        client.snapshots = [
            .init(generation: 6, state: "CONNECTED", cleanupComplete: false),
            .init(generation: 7, state: "PREPARING", cleanupComplete: false),
            .init(generation: 7, state: "CONNECTED", cleanupComplete: false),
        ]
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        try await coordinator.start(rawConfiguration: Data([1]))

        XCTAssertEqual(coordinator.sessionID, "session")
        XCTAssertEqual(coordinator.generation, 7)
        XCTAssertEqual(
            client.calls,
            ["create", "configure", "start", "snapshot", "snapshot", "snapshot"]
        )
    }

    func testFailedStartRollsBackAndDestroys() async {
        let client = TestClient()
        client.snapshots = [
            .init(generation: 7, state: "FAILED", cleanupComplete: true),
        ]
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("start unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? IOSProviderSessionError, .failed)
        }

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(client.calls.last, "destroy")
    }

    func testTimeoutUsesVirtualTimeAndRollsBack() async {
        let client = TestClient()
        client.snapshots = [
            .init(generation: 7, state: "PREPARING", cleanupComplete: false),
            .init(generation: 7, state: "FAILED", cleanupComplete: true),
        ]
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock(),
            pollInterval: 0.1,
            timeout: 0.1
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("start unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? IOSProviderSessionError, .timeout)
        }
        XCTAssertNil(coordinator.sessionID)
    }

    func testStopWaitsForCleanupThenDestroys() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.calls.removeAll()
        client.snapshots = [
            .init(generation: 7, state: "CONNECTED", cleanupComplete: false),
            .init(generation: 7, state: "STOPPING", cleanupComplete: false),
            .init(generation: 7, state: "IDLE", cleanupComplete: true),
        ]

        try await coordinator.stop()

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(
            client.calls,
            ["snapshot", "stop", "snapshot", "snapshot", "destroy"]
        )
    }

    func testRejectedStopMayStillDestroyVerifiedCleanSession() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.calls.removeAll()
        client.stopError = TestError.rejected
        client.snapshots = [
            .init(generation: 7, state: "FAILED", cleanupComplete: false),
            .init(generation: 7, state: "FAILED", cleanupComplete: true),
            .init(generation: 7, state: "FAILED", cleanupComplete: true),
        ]

        try await coordinator.stop()

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(
            client.calls,
            ["snapshot", "stop", "snapshot", "snapshot", "destroy"]
        )
    }

    func testFailedDestroyRetainsGenerationForRetry() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.snapshots = [
            .init(generation: 7, state: "IDLE", cleanupComplete: true),
        ]
        client.destroyError = TestError.rejected

        do {
            try await coordinator.stop()
            XCTFail("stop unexpectedly succeeded")
        } catch {
            XCTAssertEqual(coordinator.sessionID, "session")
            XCTAssertEqual(coordinator.generation, 7)
        }
    }
}
