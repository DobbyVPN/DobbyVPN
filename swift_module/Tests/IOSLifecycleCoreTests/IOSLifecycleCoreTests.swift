import XCTest
@testable import IOSLifecycleCore

final class IOSLifecycleCoreTests: XCTestCase {
    func testEveryExtensionStateMapsToExpectedPresentation() {
        let cases: [(NetworkExtensionState, PresentedConnectionState)] = [
            (.invalid, .disconnected),
            (.disconnected, .disconnected),
            (.connecting, .connecting),
            (.connected, .connected),
            (.reasserting, .connecting),
            (.disconnecting, .connecting),
        ]

        for (state, expected) in cases {
            XCTAssertEqual(state.presentedState, expected)
        }
    }

    func testGenerationFenceRejectsDelayedCallbacks() {
        var state = IOSLifecycleState()
        let first = state.beginStart()
        XCTAssertEqual(state.extensionState, .connecting)
        XCTAssertTrue(state.receive(.connected, generation: first))

        let stop = state.beginStop()
        XCTAssertNotEqual(first, stop)
        XCTAssertEqual(state.extensionState, .disconnecting)
        XCTAssertFalse(state.receive(.connected, generation: first))
        XCTAssertTrue(state.receive(.disconnected, generation: stop))
        XCTAssertEqual(state.extensionState.presentedState, .disconnected)
    }

    func testStopDuringStartupCannotReconnectFromDelayedStartCallback() {
        var state = IOSLifecycleState()
        let startingGeneration = state.beginStart()
        let stoppingGeneration = state.beginStop()

        XCTAssertFalse(state.receive(.connected, generation: startingGeneration))
        XCTAssertTrue(state.receive(.disconnected, generation: stoppingGeneration))
        XCTAssertEqual(state.extensionState.presentedState, .disconnected)
    }

    func testReconnectOwnsNewGenerationAfterCleanDisconnect() {
        var state = IOSLifecycleState()
        let firstStart = state.beginStart()
        XCTAssertTrue(state.receive(.connected, generation: firstStart))
        let stop = state.beginStop()
        XCTAssertTrue(state.receive(.disconnected, generation: stop))

        let reconnect = state.beginStart()
        XCTAssertNotEqual(reconnect, firstStart)
        XCTAssertTrue(state.receive(.connected, generation: reconnect))
        XCTAssertFalse(state.receive(.disconnected, generation: stop))
        XCTAssertEqual(state.extensionState.presentedState, .connected)
    }

    func testNewStartCannotTemporarilyPresentOldConnectedState() {
        var state = IOSLifecycleState()
        let first = state.beginStart()
        XCTAssertTrue(state.receive(.connected, generation: first))

        let second = state.beginStart()
        XCTAssertEqual(state.extensionState, .connecting)
        XCTAssertFalse(state.receive(.disconnected, generation: first))
        XCTAssertTrue(state.isCurrent(second))
    }

    func testSessionRequestPolicyRequiresConfigurationAndAutoSelect() {
        XCTAssertEqual(
            IOSSessionRequestPolicy.validateStart(
                configured: false,
                mode: "AUTO_SELECT",
                index: 0
            ),
            .notConfigured
        )
        XCTAssertEqual(
            IOSSessionRequestPolicy.validateStart(
                configured: true,
                mode: "MANUAL",
                index: 0
            ),
            .unsupported
        )
        XCTAssertEqual(
            IOSSessionRequestPolicy.validateStart(
                configured: true,
                mode: "AUTO_SELECT",
                index: 1
            ),
            .unsupported
        )
        XCTAssertNil(
            IOSSessionRequestPolicy.validateStart(
                configured: true,
                mode: "AUTO_SELECT",
                index: 0
            )
        )
    }

    func testObservationsAreMonotonicAndDeduplicated() {
        var observation = IOSStateObservation()

        var result = observation.observe(state: "IDLE", afterSequence: 0)
        XCTAssertFalse(result.emit)

        result = observation.observe(state: "PREPARING", afterSequence: 0)
        XCTAssertTrue(result.emit)
        XCTAssertEqual(result.nextSequence, 1)

        result = observation.observe(state: "PREPARING", afterSequence: 1)
        XCTAssertFalse(result.emit)
        XCTAssertEqual(result.nextSequence, 1)

        result = observation.observe(state: "CONNECTED", afterSequence: 1)
        XCTAssertTrue(result.emit)
        XCTAssertEqual(result.nextSequence, 2)
    }

    func testStartPolicyCoversEveryNetworkExtensionStatusAndLimit() {
        let policy = IOSStartPolicy(maximumRetries: 2)
        XCTAssertEqual(policy.action(for: .disconnected, retryAttempt: 0), .start)
        XCTAssertEqual(policy.action(for: .invalid, retryAttempt: 0), .start)
        XCTAssertEqual(policy.action(for: .connecting, retryAttempt: 0), .waitForTransition)
        XCTAssertEqual(policy.action(for: .reasserting, retryAttempt: 0), .waitForTransition)
        XCTAssertEqual(policy.action(for: .connected, retryAttempt: 0), .stopThenRetry)
        XCTAssertEqual(policy.action(for: .disconnecting, retryAttempt: 0), .retry)
        XCTAssertEqual(policy.action(for: .connected, retryAttempt: 2), .fail)
        XCTAssertEqual(policy.action(for: .disconnecting, retryAttempt: 2), .fail)
    }
}
