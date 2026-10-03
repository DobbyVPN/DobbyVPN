import Foundation
import XCTest
@testable import IOSLifecycleCore

final class TunnelSettingsOwnerTests: XCTestCase {
    func testTimedOutApplyKeepsOwnershipUntilActualCompletionAndClear() {
        let owner = TunnelSettingsOwner(operationLimit: 0.01, diagnostic: { _, _ in })
        var finishApply: ((Error?) -> Void)?
        XCTAssertNotNil(owner.apply(session: "first", generation: 1) { finishApply = $0 })
        var installedAgain = false
        XCTAssertNotNil(owner.apply(session: "second", generation: 2) { completed in
            installedAgain = true
            completed(nil)
        })
        XCTAssertFalse(installedAgain)
        var clears = 0
        XCTAssertNotNil(owner.release(session: "first", generation: 1, timeout: 0.01) { completed in
            clears += 1
            completed(nil)
        })
        XCTAssertEqual(clears, 0, "clear must wait for the in-flight apply")
        finishApply?(nil)
        XCTAssertNil(owner.release(session: "first", generation: 1, timeout: 1) { completed in
            clears += 1
            completed(nil)
        })
        XCTAssertEqual(clears, 1)
        XCTAssertNil(owner.apply(session: "second", generation: 2) { $0(nil) })
        finishApply?(NSError(domain: "late duplicate", code: 1))
        XCTAssertNotNil(owner.release(session: "first", generation: 1, timeout: 1) { _ in
            XCTFail("old generation cleared the new owner")
        })
        XCTAssertNil(owner.release(session: "second", generation: 2, timeout: 1) { $0(nil) })
    }

    func testFailedApplyAndFailedClearRetainOriginalErrorsAndCanRetry() {
        var records: [(String, String)] = []
        let owner = TunnelSettingsOwner(diagnostic: { records.append(($0, $1)) })
        let applyError = NSError(domain: "apply sentinel", code: 17)
        XCTAssertEqual(owner.apply(session: "session", generation: 1) { $0(applyError) } as NSError?, applyError)
        let clearError = NSError(domain: "clear sentinel", code: 18)
        XCTAssertEqual(owner.release(session: "session", generation: 1, timeout: 1) { $0(clearError) } as NSError?, clearError)
        XCTAssertTrue(records.contains { $0.0 == "ERROR" && $0.1.contains("apply sentinel") && $0.1.contains("duration_ms=") })
        XCTAssertTrue(records.contains { $0.0 == "ERROR" && $0.1.contains("clear sentinel") })
        XCTAssertNotNil(owner.apply(session: "session", generation: 2) { _ in XCTFail("failed clear lost ownership") })
        XCTAssertNil(owner.release(session: "session", generation: 1, timeout: 1) { $0(nil) })
        XCTAssertNil(owner.release(session: "session", generation: 1, timeout: 1) { _ in XCTFail("repeat release touched OS settings") })
    }

    func testLateClearCompletionCannotReleaseAnotherGeneration() {
        let owner = TunnelSettingsOwner(operationLimit: 0.01, diagnostic: { _, _ in })
        XCTAssertNil(owner.apply(session: "first", generation: 1) { $0(nil) })
        var finishClear: ((Error?) -> Void)?
        XCTAssertNotNil(owner.release(session: "first", generation: 1, timeout: 1) { finishClear = $0 })
        XCTAssertNotNil(owner.apply(session: "second", generation: 1) { _ in XCTFail("pending clear lost ownership") })
        finishClear?(nil)
        XCTAssertNil(owner.apply(session: "second", generation: 1) { $0(nil) })
        finishClear?(nil)
        XCTAssertNotNil(owner.release(session: "first", generation: 1, timeout: 1) { _ in XCTFail("old session cleared new settings") })
        XCTAssertNil(owner.release(session: "second", generation: 1, timeout: 1) { $0(nil) })
    }
}
