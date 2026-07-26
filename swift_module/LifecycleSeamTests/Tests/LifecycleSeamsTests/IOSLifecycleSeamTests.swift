import XCTest
@testable import LifecycleSeams

final class IOSLifecycleSeamTests: XCTestCase {
    func testEveryExtensionStateMapsToExpectedPresentation() {
        let cases: [(NetworkExtensionState, PresentedConnectionState)] = [
            (.invalid, .disconnected),
            (.disconnected, .disconnected),
            (.connecting, .connecting),
            (.connected, .connected),
            (.reasserting, .connecting),
            (.disconnecting, .connecting),
        ]

        for (extensionState, expected) in cases {
            var seam = IOSLifecycleSeam()
            let generation = seam.beginStart()
            XCTAssertTrue(seam.receiveExtensionState(extensionState, generation: generation))
            XCTAssertEqual(seam.presentedState, expected)
        }
    }

    func testStaleHealthCannotMakeNewDisconnectedGenerationConnected() {
        var seam = IOSLifecycleSeam()
        let first = seam.beginStart()
        XCTAssertTrue(seam.receiveExtensionState(.connected, generation: first))
        XCTAssertEqual(seam.presentedState, .connected)

        let second = seam.beginStart()
        XCTAssertFalse(seam.receiveDiagnosticHealth(.connected, generation: first))
        XCTAssertTrue(seam.receiveExtensionState(.disconnected, generation: second))

        XCTAssertEqual(seam.diagnosticHealthState, .disconnected)
        XCTAssertEqual(seam.presentedState, .disconnected)
    }

    func testStopCorrelatesCallbacksAndRejectsDelayedStartCompletion() {
        var seam = IOSLifecycleSeam()
        let start = seam.beginStart()
        let stop = seam.beginStop()

        XCTAssertNotEqual(start, stop)
        XCTAssertFalse(seam.receiveExtensionState(.connected, generation: start))
        XCTAssertTrue(seam.receiveExtensionState(.disconnected, generation: stop))
        XCTAssertEqual(seam.presentedState, .disconnected)
    }

    func testNetworkExtensionReconciliationIsAuthoritativeOverPersistedHealth() {
        var seam = IOSLifecycleSeam()
        let generation = seam.beginStart()

        XCTAssertTrue(seam.receiveDiagnosticHealth(.connected, generation: generation))
        XCTAssertTrue(seam.receiveExtensionState(.disconnected, generation: generation))
        XCTAssertEqual(seam.presentedState, .disconnected)

        XCTAssertTrue(seam.receiveExtensionState(.reasserting, generation: generation))
        XCTAssertEqual(seam.presentedState, .connecting)
        XCTAssertTrue(seam.receiveExtensionState(.connected, generation: generation))
        XCTAssertEqual(seam.presentedState, .connected)
    }

    func testDelayedExtensionConnectionFromStoppedGenerationCannotReconnectUI() {
        var seam = IOSLifecycleSeam()
        let first = seam.beginStart()
        let stop = seam.beginStop()

        // An old packet-tunnel callback may arrive after the app already sent
        // a generation-correlated stop. It is diagnostic at most, never UI
        // authority for the new generation.
        XCTAssertFalse(seam.receiveExtensionState(.connected, generation: first))
        XCTAssertTrue(seam.receiveExtensionState(.disconnecting, generation: stop))
        XCTAssertEqual(seam.presentedState, .connecting)
        XCTAssertTrue(seam.receiveExtensionState(.disconnected, generation: stop))
        XCTAssertEqual(seam.presentedState, .disconnected)
    }

    func testSameGenerationHealthCannotOverrideDisconnectedExtension() {
        var seam = IOSLifecycleSeam()
        let generation = seam.beginStart()
        XCTAssertTrue(seam.receiveExtensionState(.disconnected, generation: generation))
        XCTAssertTrue(seam.receiveDiagnosticHealth(.connected, generation: generation))
        XCTAssertEqual(seam.presentedState, .disconnected)
    }

    func testStartAcceptanceIsNotConnectedUntilMatchingExtensionObservation() {
        var seam = IOSSessionCommandSeam()
        seam.acceptStart(generation: 8)
        XCTAssertFalse(seam.startCompleted)
        XCTAssertFalse(seam.observe(.connected, generation: 7, cleanupComplete: false))
        XCTAssertFalse(seam.startCompleted)
        XCTAssertTrue(seam.observe(.connected, generation: 8, cleanupComplete: false))
        XCTAssertTrue(seam.startCompleted)
    }

    func testStopAcceptanceWaitsForMatchingCleanupBeforeDestroy() {
        var seam = IOSSessionCommandSeam()
        seam.acceptStart(generation: 4)
        XCTAssertTrue(seam.acceptStop(generation: 4))
        XCTAssertFalse(seam.stopCompleted)
        XCTAssertTrue(seam.observe(.disconnected, generation: 4, cleanupComplete: false))
        XCTAssertFalse(seam.stopCompleted)
        XCTAssertTrue(seam.observe(.disconnected, generation: 4, cleanupComplete: true))
        XCTAssertTrue(seam.stopCompleted)
    }

    func testCommandSeamRejectsStaleStopAndObservation() {
        var seam = IOSSessionCommandSeam()
        seam.acceptStart(generation: 12)
        XCTAssertFalse(seam.acceptStop(generation: 11))
        XCTAssertFalse(seam.observe(.connected, generation: 11, cleanupComplete: false))
        XCTAssertEqual(seam.state, .connecting)
        XCTAssertFalse(seam.cleanupComplete)
    }

    func testShellConfigurationFailuresDoNotConfigure() {
        var seam = IOSSessionShellSeam()
        XCTAssertEqual(
            seam.configure(hasBytes: false, storageSucceeded: true),
            .failure(.malformedConfiguration)
        )
        XCTAssertEqual(
            seam.configure(hasBytes: true, storageSucceeded: false),
            .failure(.secureStorageFailed)
        )
        XCTAssertFalse(seam.configured)
        XCTAssertEqual(seam.start(mode: "AUTO_SELECT", index: 0), .failure(.notConfigured))
    }

    func testShellAcceptsOnlyAutoSelectAndCorrelatesStop() {
        var seam = IOSSessionShellSeam()
        XCTAssertEqual(seam.configure(hasBytes: true, storageSucceeded: true), .success(generation: nil))
        XCTAssertEqual(seam.start(mode: "MANUAL", index: 0), .failure(.unsupported))
        XCTAssertEqual(seam.start(mode: "AUTO_SELECT", index: 1), .failure(.unsupported))
        XCTAssertEqual(seam.start(mode: "AUTO_SELECT", index: 0), .success(generation: 1))
        XCTAssertEqual(seam.stop(generation: 0), .failure(.staleGeneration))
        XCTAssertEqual(seam.stop(generation: 2), .failure(.staleGeneration))
        XCTAssertEqual(seam.stop(generation: 1), .success(generation: 1))
        XCTAssertEqual(seam.state, .disconnecting)
        XCTAssertEqual(seam.generation, 2)
    }

    func testShellObservationsAreSequencedAndDeduplicated() {
        var seam = IOSSessionShellSeam()
        _ = seam.configure(hasBytes: true, storageSucceeded: true)
        _ = seam.start(mode: "AUTO_SELECT", index: 0)

        var observation = seam.observe(afterSequence: 0)
        XCTAssertEqual(observation.events, [.connecting])
        XCTAssertEqual(observation.nextSequence, 1)

        observation = seam.observe(afterSequence: 1)
        XCTAssertTrue(observation.events.isEmpty)
        XCTAssertTrue(seam.receive(.connected, generation: 1))
        observation = seam.observe(afterSequence: 1)
        XCTAssertEqual(observation.events, [.connected])
        XCTAssertEqual(observation.nextSequence, 2)
        XCTAssertFalse(seam.receive(.disconnected, generation: 0))
        XCTAssertEqual(seam.state, .connected)
    }

    func testShellCleanupAndDestroyPolicy() {
        var seam = IOSSessionShellSeam()
        XCTAssertTrue(seam.cleanupComplete)
        _ = seam.configure(hasBytes: true, storageSucceeded: true)
        _ = seam.start(mode: "AUTO_SELECT", index: 0)
        XCTAssertFalse(seam.cleanupComplete)
        XCTAssertTrue(seam.receive(.invalid, generation: 1))
        XCTAssertTrue(seam.cleanupComplete)
        seam.destroy()
        XCTAssertFalse(seam.configured)
    }

    func testStartDecisionCoversEveryNetworkExtensionStatus() {
        let seam = IOSStartRetrySeam(maximumRetries: 2)
        XCTAssertEqual(seam.action(for: .disconnected, retryAttempt: 0), .start)
        XCTAssertEqual(seam.action(for: .invalid, retryAttempt: 0), .start)
        XCTAssertEqual(seam.action(for: .connecting, retryAttempt: 0), .waitForTransition)
        XCTAssertEqual(seam.action(for: .reasserting, retryAttempt: 0), .waitForTransition)
        XCTAssertEqual(seam.action(for: .connected, retryAttempt: 0), .stopThenRetry)
        XCTAssertEqual(seam.action(for: .disconnecting, retryAttempt: 0), .retry)
        XCTAssertEqual(seam.action(for: .connected, retryAttempt: 2), .fail)
        XCTAssertEqual(seam.action(for: .disconnecting, retryAttempt: 2), .fail)
    }

    func testScheduledRetryIsRejectedAfterGenerationInvalidation() {
        var seam = IOSStartRetrySeam()
        let scheduledGeneration = seam.begin()
        XCTAssertTrue(seam.retryIsCurrent(scheduledGeneration))
        seam.invalidate()
        XCTAssertFalse(seam.retryIsCurrent(scheduledGeneration))
    }
}
