import Foundation
import XCTest
@testable import IOSLifecycleCore

final class IOSLifecycleCoreTests: XCTestCase {
    private let secret = Data(repeating: 0x41, count: 32)

    func testProviderCompletesBeforeContainingAppTransportTimeout() {
        XCTAssertLessThan(
            IOSProviderTiming.providerCommandCompletionTimeout,
            IOSProviderTiming.appMessageTimeout
        )
        XCTAssertGreaterThan(IOSProviderTiming.appTransportRetries, 0)
        XCTAssertGreaterThan(IOSProviderTiming.appRetryDelay, 0)
    }

    func testRetryBudgetBoundsAllAttemptsByOneMonotonicDeadline() {
        var budget = IOSProviderRetryBudget(start: 100, budget: 30, maximumRetries: 6, retryDelay: 0.5)
        XCTAssertEqual(budget.nextAttemptTimeout(now: 100), 30, accuracy: 0.0001)
        XCTAssertEqual(budget.nextRetryDelay(now: 125), 0.5, accuracy: 0.0001)
        XCTAssertEqual(budget.nextAttemptTimeout(now: 125.5), 4.5, accuracy: 0.0001)
        XCTAssertNil(budget.nextAttemptTimeout(now: 130.1))
    }

    func testLateSettingsCompletionCannotCommitAfterPoisoning() {
        let fence = IOSSettingsOperationFence()
        let oldEpoch = try! XCTUnwrap(fence.begin())
        fence.poison()

        XCTAssertFalse(fence.canCommit(oldEpoch))
        XCTAssertNil(fence.begin())
    }

    func testAuthenticatedCanonicalRoundTripContainsNoConfigurationBytes() throws {
        let command = try IOSProviderCommand(
            operation: .configure,
            requestID: "ios-configure-1",
            sessionID: "0123456789abcdef0123456789abcdef"
        )
        let bytes = try command.encoded(using: secret)
        XCTAssertFalse(String(decoding: bytes, as: UTF8.self).contains("raw_config"))
        XCTAssertEqual(try IOSProviderCommand.decode(bytes, using: secret), command)
    }

    func testWrongSecretAndTamperedCommandAreRejected() throws {
        let command = try IOSProviderCommand(
            operation: .snapshot,
            requestID: "ios-snapshot-1",
            sessionID: "session"
        )
        let bytes = try command.encoded(using: secret)
        XCTAssertThrowsError(try IOSProviderCommand.decode(bytes, using: Data(repeating: 0x42, count: 32))) { error in
            XCTAssertEqual(error as? IOSProviderMessageError, .unauthenticated)
        }
        var tampered = bytes
        tampered[tampered.count - 2] = tampered[tampered.count - 2] == 0x30 ? 0x31 : 0x30
        XCTAssertThrowsError(try IOSProviderCommand.decode(tampered, using: secret))
    }

    func testCanonicalBytesRejectReorderedEnvelopeAndUnknownFields() throws {
        let command = try IOSProviderCommand(
            operation: .create,
            requestID: "ios-create-1"
        )
        let bytes = try command.encoded(using: secret)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: bytes) as? [String: Any])
        var reordered = Data("{\"mac\":\"\(object["mac"] as! String)\",\"version\":1,\"operation\":\"create\",\"request_id\":\"ios-create-1\"}".utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(reordered, using: secret))
        reordered = bytes
        var withUnknown = object
        withUnknown["raw_config"] = "not allowed"
        reordered = try JSONSerialization.data(withJSONObject: withUnknown, options: [])
        XCTAssertThrowsError(try IOSProviderCommand.decode(reordered, using: secret))
    }

    func testOperationFieldsAreStrictlyBound() throws {
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .start,
            requestID: "start",
            sessionID: "session",
            mode: "UNKNOWN",
            index: 0
        )) { error in
            XCTAssertEqual(error as? IOSProviderMessageError, .unsupportedOperation)
        }
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .observe,
            requestID: "observe",
            sessionID: "session"
        ))
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .configure,
            requestID: "configure",
            sessionID: "session",
            generation: 1
        ))
    }

    func testOversizedMessageAndInvalidIdentifiersAreRejected() throws {
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .create,
            requestID: String(repeating: "x", count: IOSProviderCommand.maximumRequestIDBytes + 1)
        ))
        let command = try IOSProviderCommand(operation: .create, requestID: "create")
        XCTAssertThrowsError(try command.encoded(using: Data(repeating: 0x41, count: 0)))
    }

    func testRawConfigurationCeilingIsOneMiBAndDistinctFromMessageCeiling() {
        XCTAssertEqual(IOSProviderCommand.maximumConfigurationBytes, 1 * 1024 * 1024)
        XCTAssertLessThan(IOSProviderCommand.maximumBytes, IOSProviderCommand.maximumConfigurationBytes)
    }

    func testMailboxIsConsumedOnlyByValidGoEnvelope() {
        XCTAssertTrue(IOSMailboxLifecycle.mayConsumeConfigureResponse(Data(#"{"ok":true,"result":{"digest":"abc"}}"#.utf8)))
        XCTAssertTrue(IOSMailboxLifecycle.mayConsumeConfigureResponse(Data(#"{"ok":false,"error":{"code":"MALFORMED_CONFIG"}}"#.utf8)))
        XCTAssertTrue(IOSMailboxLifecycle.mayConsumeConfigureResponse(Data(#"{"ok":false,"error":{"code":"TIMEOUT"}}"#.utf8)))
        XCTAssertFalse(IOSMailboxLifecycle.mayConsumeConfigureResponse(Data(#"{"ok":false,"error":{}}"#.utf8)))
        XCTAssertFalse(IOSMailboxLifecycle.mayConsumeConfigureResponse(Data(#"not-json"#.utf8)))
    }

    func testAuthenticatedResponsePreservesExactGoBytes() throws {
        let goBytes = Data(#"{"ok":true, "result":{"digest":"exact-spacing"}}"#.utf8)
        let response = try IOSProviderResponse(requestID: "ios-response-1", payload: goBytes)
        let encoded = try response.encoded(using: secret)
        let decoded = try IOSProviderResponse.decode(encoded, expectedRequestID: "ios-response-1", using: secret)
        XCTAssertEqual(decoded.kind, .go)
        XCTAssertEqual(decoded.payload, goBytes)
    }

    func testResponseRejectsWrongSecretRequestMismatchTamperingAndUnknownFields() throws {
        let response = try IOSProviderResponse(requestID: "ios-response-2", payload: Data(#"{"ok":false}"#.utf8))
        let encoded = try response.encoded(using: secret)
        XCTAssertThrowsError(try IOSProviderResponse.decode(encoded, expectedRequestID: "other", using: secret))
        XCTAssertThrowsError(try IOSProviderResponse.decode(encoded, expectedRequestID: "ios-response-2", using: Data(repeating: 0x42, count: 32))) { error in
            XCTAssertEqual(error as? IOSProviderMessageError, .unauthenticated)
        }

        var tampered = encoded
        tampered[tampered.count - 2] = tampered[tampered.count - 2] == 0x30 ? 0x31 : 0x30
        XCTAssertThrowsError(try IOSProviderResponse.decode(tampered, expectedRequestID: "ios-response-2", using: secret))

        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: encoded) as? [String: Any])
        var unknown = object
        unknown["raw_config"] = "must not be accepted"
        let unknownBytes = try JSONSerialization.data(withJSONObject: unknown, options: [])
        XCTAssertThrowsError(try IOSProviderResponse.decode(unknownBytes, expectedRequestID: "ios-response-2", using: secret))

        let transport = try IOSProviderResponse(
            requestID: "ios-response-transport",
            kind: .transport,
            payload: Data(#"{"ok":false,"error":{"code":"SESSIONAPI_TIMEOUT"}}"#.utf8)
        )
        let transportBytes = try transport.encoded(using: secret)
        XCTAssertEqual(
            try IOSProviderResponse.decode(transportBytes, expectedRequestID: "ios-response-transport", using: secret).kind,
            .transport
        )
        XCTAssertFalse(IOSMailboxLifecycle.mayConsumeConfigureResponse(transportBytes))
    }

    func testResponseSizeIsBounded() throws {
        let payload = Data(repeating: 0x41, count: IOSProviderResponse.maximumBytes)
        let response = try IOSProviderResponse(requestID: "ios-response-large", payload: payload)
        XCTAssertThrowsError(try response.encoded(using: secret)) { error in
            XCTAssertEqual(error as? IOSProviderMessageError, .tooLarge)
        }
    }
}
