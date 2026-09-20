import Foundation
import XCTest
@testable import IOSLifecycleCore

final class IOSLifecycleCoreTests: XCTestCase {
    func testCommandRoundTripContainsNoConfigurationBytes() throws {
        let command = try IOSProviderCommand(
            operation: .configure,
            requestID: "ios-configure-1",
            sessionID: "0123456789abcdef0123456789abcdef",
            expectedSequence: 4
        )
        let bytes = try command.encoded()
        XCTAssertFalse(String(decoding: bytes, as: UTF8.self).contains("raw_config"))
        XCTAssertEqual(try IOSProviderCommand.decode(bytes), command)
    }

    func testCommandDecoderIgnoresUnrelatedFields() throws {
        let bytes = Data(#"{"operation":"snapshot","request_id":"ios-snapshot-1","version":1,"future_field":true}"#.utf8)
        XCTAssertEqual(try IOSProviderCommand.decode(bytes).operation, .snapshot)
    }

    func testOperationFieldsAreBound() throws {
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .start,
            requestID: "start",
            sessionID: "session",
            mode: "UNKNOWN",
            index: 0,
            expectedSequence: 1
        )) { error in
            XCTAssertEqual(error as? IOSProviderMessageError, .unsupportedOperation)
        }
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .reset,
            requestID: "reset",
            sessionID: "session"
        ))
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .configure,
            requestID: "configure",
            sessionID: "session",
            generation: 1,
            expectedSequence: 1
        ))
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .snapshot,
            requestID: "snapshot",
            sessionID: ""
        ))
        XCTAssertThrowsError(try IOSProviderCommand(
            operation: .stop,
            requestID: "stop",
            sessionID: "session"
        ))
    }

    func testMalformedRequiredNumbersAreRejected() {
        let generation = Data(#"{"generation":"not-a-number","operation":"stop","request_id":"stop","session_id":"session","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(generation))

        let index = Data(#"{"index":"not-a-number","mode":"AUTO_SELECT","operation":"start","request_id":"start","session_id":"session","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(index))
    }

    func testIntegralJSONNumbersRemainCompatible() throws {
        let configure = Data(#"{"expected_sequence":4.0,"operation":"configure","request_id":"configure","version":1.0}"#.utf8)
        let configureCommand = try IOSProviderCommand.decode(configure)
        XCTAssertEqual(configureCommand.expectedSequence, 4)

        let start = Data(#"{"expected_sequence":5.0,"index":0.0,"mode":"AUTO_SELECT","operation":"start","request_id":"start","version":1.0}"#.utf8)
        let startCommand = try IOSProviderCommand.decode(start)
        XCTAssertEqual(startCommand.expectedSequence, 5)
        XCTAssertEqual(startCommand.index, 0)

        let stop = Data(#"{"generation":6.0,"operation":"stop","request_id":"stop","session_id":"session","version":1.0}"#.utf8)
        XCTAssertEqual(try IOSProviderCommand.decode(stop).generation, 6)

        let response = Data(#"{"kind":"go","payload":"e30=","request_id":"response","version":1.0}"#.utf8)
        XCTAssertEqual(try IOSProviderResponse.decode(response, expectedRequestID: "response").payload, Data(#"{}"#.utf8))
    }

    func testFractionalNumericFieldsAreRejected() {
        let version = Data(#"{"operation":"snapshot","request_id":"snapshot","version":1.5}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(version))

        let generation = Data(#"{"generation":1.5,"operation":"stop","request_id":"stop","session_id":"session","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(generation))

        let expectedSequence = Data(#"{"expected_sequence":2.5,"operation":"configure","request_id":"configure","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(expectedSequence))

        let index = Data(#"{"expected_sequence":1,"index":0.5,"mode":"AUTO_SELECT","operation":"start","request_id":"start","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(index))

        let response = Data(#"{"kind":"go","payload":"e30=","request_id":"response","version":1.5}"#.utf8)
        XCTAssertThrowsError(try IOSProviderResponse.decode(response, expectedRequestID: "response"))
    }

    func testInvalidNumericRangesAreRejected() {
        let negativeGeneration = Data(#"{"generation":-1,"operation":"stop","request_id":"stop","session_id":"session","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(negativeGeneration))

        let negativeSequence = Data(#"{"expected_sequence":-1,"operation":"configure","request_id":"configure","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(negativeSequence))

        let negativeIndex = Data(#"{"expected_sequence":1,"index":-1,"mode":"AUTO_SELECT","operation":"start","request_id":"start","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(negativeIndex))

        let sequenceOverflow = Data(#"{"expected_sequence":9223372036854775808,"operation":"configure","request_id":"configure","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(sequenceOverflow))

        let indexOverflow = Data(#"{"expected_sequence":1,"index":2147483648,"mode":"AUTO_SELECT","operation":"start","request_id":"start","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(indexOverflow))
    }

    func testNonFiniteNumericFieldsAreRejected() {
        let nonFiniteVersion = Data(#"{"operation":"snapshot","request_id":"snapshot","version":1e9999}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(nonFiniteVersion))

        let nonFiniteSequence = Data(#"{"expected_sequence":1e9999,"operation":"configure","request_id":"configure","version":1}"#.utf8)
        XCTAssertThrowsError(try IOSProviderCommand.decode(nonFiniteSequence))
    }

    func testEmptyIdentifiersAreRejected() throws {
        XCTAssertThrowsError(try IOSProviderCommand.decode(Data(#"{"operation":"snapshot","request_id":"","version":1}"#.utf8)))
        let longID = String(repeating: "x", count: 1_024)
        XCTAssertNoThrow(try IOSProviderCommand(operation: .snapshot, requestID: longID))
    }

    func testMailboxIsConsumedOnlyByValidGoEnvelope() {
        XCTAssertTrue(IOSMailboxLifecycle.mayConsumeConfigurationResponse(Data(#"{"ok":true,"result":{"digest":"abc"}}"#.utf8)))
        XCTAssertTrue(IOSMailboxLifecycle.mayConsumeConfigurationResponse(Data(#"{"ok":false,"error":{"code":"MALFORMED_CONFIG"}}"#.utf8)))
        XCTAssertFalse(IOSMailboxLifecycle.mayConsumeConfigurationResponse(Data(#"{"ok":false,"error":{}}"#.utf8)))
        XCTAssertFalse(IOSMailboxLifecycle.mayConsumeConfigurationResponse(Data(#"not-json"#.utf8)))
    }

    func testResponsePreservesExactGoBytes() throws {
        let goBytes = Data(#"{"ok":true, "result":{"digest":"exact-spacing"}}"#.utf8)
        let response = try IOSProviderResponse(requestID: "ios-response-1", payload: goBytes)
        let encoded = try response.encoded()
        let decoded = try IOSProviderResponse.decode(encoded, expectedRequestID: "ios-response-1")
        XCTAssertEqual(decoded.kind, .go)
        XCTAssertEqual(decoded.payload, goBytes)
    }

    func testResponseRequiresMatchingRequest() throws {
        let response = try IOSProviderResponse(requestID: "ios-response-2", payload: Data(#"{"ok":false}"#.utf8))
        let encoded = try response.encoded()
        XCTAssertThrowsError(try IOSProviderResponse.decode(encoded, expectedRequestID: "other"))
        XCTAssertThrowsError(try IOSProviderResponse.decode(Data(#"{}"#.utf8), expectedRequestID: "ios-response-2"))
    }

    func testResponseRequiresANonEmptyRequestID() throws {
        let payload = Data(repeating: 0x41, count: 256 * 1024)
        let response = try IOSProviderResponse(requestID: "ios-response-large", payload: payload)
        XCTAssertNoThrow(try response.encoded())
        XCTAssertThrowsError(try IOSProviderResponse(requestID: "", payload: Data()))
    }
}
