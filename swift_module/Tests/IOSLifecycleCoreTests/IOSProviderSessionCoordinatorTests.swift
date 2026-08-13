import Foundation
import XCTest
@testable import IOSLifecycleCore

private final class TestClock: IOSSessionClock {
    var now = Date(timeIntervalSince1970: 0)

    func sleep(seconds: TimeInterval) async throws {
        now.addTimeInterval(seconds)
    }
}

private enum TestError: Error, Equatable {
    case rejected
}

private final class TestClient: IOSProviderSessionClient {
    var calls: [String] = []
    var snapshots: [IOSProviderSessionSnapshot] = []
    var createResult = "session"
    var createError: Error?
    var configureError: Error?
    var startResult: Int64 = 7
    var startError: Error?
    var stopError: Error?
    var destroyError: Error?

    func create() throws -> String {
        calls.append("create")
        if let createError { throw createError }
        return createResult
    }

    func configure(sessionID: String, rawConfiguration: Data) throws {
        calls.append("configure")
        if let configureError { throw configureError }
    }

    func start(sessionID: String) throws -> Int64 {
        calls.append("start")
        if let startError { throw startError }
        return startResult
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
    func testErrorDescriptionsMatchStableErrorCodes() {
        XCTAssertEqual(
            IOSProviderSessionError.cleanupPending.errorDescription,
            "SESSIONAPI_CLEANUP_PENDING"
        )
        XCTAssertEqual(
            IOSProviderSessionError.failed.errorDescription,
            "SESSIONAPI_FAILED"
        )
        XCTAssertEqual(
            IOSProviderSessionError.timeout.errorDescription,
            "SESSIONAPI_TIMEOUT"
        )
        XCTAssertEqual(
            IOSProviderSessionError.cleanupTimeout.errorDescription,
            "SESSIONAPI_CLEANUP_TIMEOUT"
        )
        XCTAssertEqual(
            IOSProviderSessionError.malformed.errorDescription,
            "SESSIONAPI_MALFORMED"
        )
    }

    func testSystemClockAcceptsZeroDurationSleep() async throws {
        try await IOSSystemSessionClock().sleep(seconds: 0)
    }

    func testDuplicateStartReportsCleanupPendingWithoutTouchingSession() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        let callsBeforeDuplicateStart = client.calls

        do {
            try await coordinator.start(rawConfiguration: Data([2]))
            XCTFail("duplicate start unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? IOSProviderSessionError, .cleanupPending)
        }

        XCTAssertEqual(coordinator.sessionID, "session")
        XCTAssertEqual(coordinator.generation, 7)
        XCTAssertEqual(client.calls, callsBeforeDuplicateStart)
    }

    func testEmptyCreatedSessionIsMalformedWithoutCleanupAttempt() async {
        let client = TestClient()
        client.createResult = ""
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("empty session unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? IOSProviderSessionError, .malformed)
        }

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertEqual(client.calls, ["create"])
    }

    func testCreateFailureLeavesCoordinatorEmpty() async {
        let client = TestClient()
        client.createError = TestError.rejected
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("create failure unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? TestError, .rejected)
        }

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertEqual(client.calls, ["create"])
    }

    func testConfigureFailureDestroysUnstartedSession() async {
        let client = TestClient()
        client.configureError = TestError.rejected
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("configure failure unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? TestError, .rejected)
        }

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertEqual(client.calls, ["create", "configure", "destroy"])
    }

    func testStartFailureDestroysUnstartedSession() async {
        let client = TestClient()
        client.startError = TestError.rejected
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("start failure unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? TestError, .rejected)
        }

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertEqual(client.calls, ["create", "configure", "start", "destroy"])
    }

    func testZeroStartGenerationIsMalformedAndDestroysSession() async {
        let client = TestClient()
        client.startResult = 0
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        do {
            try await coordinator.start(rawConfiguration: Data([1]))
            XCTFail("zero generation unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? IOSProviderSessionError, .malformed)
        }

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertEqual(client.calls, ["create", "configure", "start", "destroy"])
    }

    func testStopWithoutSessionIsNoOp() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )

        try await coordinator.stop()

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertTrue(client.calls.isEmpty)
    }

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

    func testRejectedStopRetainsSessionWhenCleanupIsNotVerified() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.calls.removeAll()
        client.stopError = TestError.rejected
        client.snapshots = [
            .init(generation: 7, state: "CONNECTED", cleanupComplete: false),
        ]

        do {
            try await coordinator.stop()
            XCTFail("rejected stop unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? TestError, .rejected)
        }

        XCTAssertEqual(coordinator.sessionID, "session")
        XCTAssertEqual(coordinator.generation, 7)
        XCTAssertEqual(client.calls, ["snapshot", "stop", "snapshot"])
    }

    func testStopCleanupTimeoutRetainsSessionForRetry() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock(),
            pollInterval: 0.1,
            timeout: 0.1
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.calls.removeAll()
        client.snapshots = [
            .init(generation: 7, state: "STOPPING", cleanupComplete: false),
        ]

        do {
            try await coordinator.stop()
            XCTFail("cleanup timeout unexpectedly succeeded")
        } catch {
            XCTAssertEqual(error as? IOSProviderSessionError, .cleanupTimeout)
        }

        XCTAssertEqual(coordinator.sessionID, "session")
        XCTAssertEqual(coordinator.generation, 7)
        XCTAssertEqual(client.calls, ["snapshot", "stop", "snapshot"])
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

    func testCleanStopAllowsReconnectWithFreshSessionGeneration() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.snapshots = [
            .init(generation: 7, state: "IDLE", cleanupComplete: true),
        ]
        try await coordinator.stop()

        client.createResult = "replacement-session"
        client.startResult = 8
        client.snapshots = [
            .init(generation: 8, state: "CONNECTED", cleanupComplete: false),
        ]
        try await coordinator.start(rawConfiguration: Data([2]))

        XCTAssertEqual(coordinator.sessionID, "replacement-session")
        XCTAssertEqual(coordinator.generation, 8)
        XCTAssertEqual(
            Array(client.calls.suffix(5)),
            ["destroy", "create", "configure", "start", "snapshot"]
        )
    }

    func testUnexpectedTerminationUsesStrictCleanupAndAllowsReconnect() async throws {
        let client = TestClient()
        let coordinator = IOSProviderSessionCoordinator(
            client: client,
            clock: TestClock()
        )
        try await coordinator.start(rawConfiguration: Data([1]))
        client.calls.removeAll()
        client.snapshots = [
            .init(generation: 7, state: "FAILED", cleanupComplete: false),
            .init(generation: 7, state: "FAILED", cleanupComplete: true),
        ]

        try await coordinator.cleanupAfterUnexpectedTermination()

        XCTAssertNil(coordinator.sessionID)
        XCTAssertEqual(coordinator.generation, 0)
        XCTAssertEqual(client.calls, ["snapshot", "stop", "snapshot", "destroy"])
    }
}
