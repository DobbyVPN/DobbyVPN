import XCTest
@testable import LifecycleSeams

final class IOSLifecycleSeamTests: XCTestCase {
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
}
