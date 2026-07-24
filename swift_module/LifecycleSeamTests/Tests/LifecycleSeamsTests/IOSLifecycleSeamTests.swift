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
}
