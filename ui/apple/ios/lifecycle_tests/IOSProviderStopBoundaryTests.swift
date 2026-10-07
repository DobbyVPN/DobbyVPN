import XCTest
@testable import IOSLifecycleCore

final class IOSProviderStopBoundaryTests: XCTestCase {
    func testOSStopWaitsForGoCleanupBeforeReleasingCallbacksAndCompleting() {
        let cleanupStarted = expectation(description: "Go cleanup started")
        let stopFinished = expectation(description: "Stop boundary returned")
        let cleanupGate = DispatchSemaphore(value: 0)
        let lock = NSLock()
        var events: [String] = []
        var returnedFailure: String?

        DispatchQueue.global().async {
            let failure = IOSProviderStopBoundary.perform(
                stopGoSessionAndWait: {
                    lock.lock()
                    events.append("go-cleanup-started")
                    lock.unlock()
                    cleanupStarted.fulfill()
                    cleanupGate.wait()
                    lock.lock()
                    events.append("go-cleanup-finished")
                    lock.unlock()
                    return ""
                },
                releasePlatformCallbacks: {
                    lock.lock()
                    events.append("callbacks-released")
                    lock.unlock()
                },
                stopPathMonitoring: {
                    lock.lock()
                    events.append("path-monitor-stopped")
                    lock.unlock()
                },
                completeOSStop: { _ in
                    lock.lock()
                    events.append("os-stop-completed")
                    lock.unlock()
                }
            )
            lock.lock()
            returnedFailure = failure
            lock.unlock()
            stopFinished.fulfill()
        }

        wait(for: [cleanupStarted], timeout: 1)
        lock.lock()
        let whileBlocked = events
        lock.unlock()
        XCTAssertEqual(whileBlocked, ["go-cleanup-started"])

        cleanupGate.signal()
        wait(for: [stopFinished], timeout: 1)
        lock.lock()
        let completedEvents = events
        lock.unlock()
        XCTAssertEqual(completedEvents, [
            "go-cleanup-started",
            "go-cleanup-finished",
            "callbacks-released",
            "path-monitor-stopped",
            "os-stop-completed",
        ])
        lock.lock()
        let completedFailure = returnedFailure
        lock.unlock()
        XCTAssertEqual(completedFailure, "")
    }

    func testCleanupFailureKeepsCallbacksAndReturnsFailureBeforeOSCompletion() {
        var events: [String] = []
        let failure = "Go cleanup sentinel"

        let returned = IOSProviderStopBoundary.perform(
            stopGoSessionAndWait: {
                events.append("go-cleanup")
                return failure
            },
            releasePlatformCallbacks: { events.append("callbacks-released") },
            stopPathMonitoring: { events.append("path-monitor-stopped") },
            completeOSStop: { error in events.append("os-stop-completed:\(error)") }
        )

        XCTAssertEqual(returned, failure)
        XCTAssertEqual(events, ["go-cleanup", "path-monitor-stopped", "os-stop-completed:\(failure)"])
    }
}
