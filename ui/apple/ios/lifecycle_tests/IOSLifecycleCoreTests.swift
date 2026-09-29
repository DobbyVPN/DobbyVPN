import Foundation
import XCTest
@testable import IOSLifecycleCore

final class IOSLifecycleCoreTests: XCTestCase {
    func testCommandRoundTripKeepsSharedMethodAndParameters() throws {
        let params = try JSONSerialization.data(
            withJSONObject: ["expected_sequence": 4, "session_id": "session-1"],
            options: [.sortedKeys]
        )
        let command = try IOSProviderCommand(
            method: "Configure",
            requestID: "ios-configure-1",
            params: params
        )
        let bytes = try command.encoded()
        let encoded = String(decoding: bytes, as: UTF8.self)
        XCTAssertFalse(encoded.contains("super-secret-token"))
        XCTAssertTrue(encoded.contains("\"method\":\"Configure\""))
        let decoded = try IOSProviderCommand.decode(bytes)
        XCTAssertEqual(decoded.method, "Configure")
        XCTAssertEqual(decoded.requestID, "ios-configure-1")
        let decodedParams = try XCTUnwrap(JSONSerialization.jsonObject(with: decoded.params) as? [String: Any])
        XCTAssertEqual((decodedParams["expected_sequence"] as? NSNumber)?.intValue, 4)
        XCTAssertEqual(decodedParams["session_id"] as? String, "session-1")
    }

    func testCommandLeavesBusinessValidationToGo() throws {
        let params = Data(#"{"mode":"UNKNOWN","expected_sequence":-2}"#.utf8)
        let command = try IOSProviderCommand(method: "Start", requestID: "start", params: params)
        XCTAssertEqual(command.method, "Start")
        XCTAssertEqual(command.params, params)
    }

    func testCommandRequiresTransportMetadataAndValidJSON() throws {
        XCTAssertThrowsError(try IOSProviderCommand.decode(Data(#"{"method":"Snapshot","params":{},"request_id":"snapshot"}"#.utf8)))
        XCTAssertThrowsError(try IOSProviderCommand.decode(Data(#"{"method":"Snapshot","params":{},"request_id":"snapshot","version":1}"#.utf8)))
        XCTAssertThrowsError(try IOSProviderCommand.decode(Data(#"{"method":"Snapshot","params":{},"request_id":"","version":2}"#.utf8)))
        XCTAssertThrowsError(try IOSProviderCommand.decode(Data(#"{"method":"Snapshot","request_id":"snapshot","version":2}"#.utf8)))
        XCTAssertThrowsError(try IOSProviderCommand(method: "Snapshot", requestID: "snapshot", params: Data("{".utf8)))
    }

    func testConfigurationMailboxRoundTripsAndBindsTheRequest() throws {
        let rawConfiguration = Data("secret\nprofile bytes".utf8)
        let mailbox = try IOSConfigurationMailbox(requestID: "ios-configure-1", configuration: rawConfiguration)
        let decoded = try IOSConfigurationMailbox.decode(mailbox.encoded())
        XCTAssertEqual(decoded, mailbox)
        XCTAssertNotEqual(decoded.requestID, "ios-configure-2")
        XCTAssertThrowsError(try IOSConfigurationMailbox(requestID: "bad\nid", configuration: rawConfiguration))
    }

    func testEmptyConfigurationMailboxRoundTripsForGoValidation() throws {
        let mailbox = try IOSConfigurationMailbox(requestID: "ios-configure-empty", configuration: Data())
        XCTAssertEqual(try IOSConfigurationMailbox.decode(mailbox.encoded()), mailbox)
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

    func testMalformedProviderPayloadIsRejectedWithoutReplacementText() {
        let payload = Data([0x7b, 0xff, 0x00, 0x80, 0x7d])
        XCTAssertThrowsError(try IOSProviderPayload.decode(payload)) { error in
            XCTAssertEqual(
                error as? IOSProviderPayloadError,
                .invalidUTF8(hex: "7bff00807d")
            )
        }
        XCTAssertNil(String(data: payload, encoding: .utf8))
    }

    func testDiagnosticExportEscapesMalformedBytesReversibly() {
        XCTAssertEqual(
            reversibleDiagnosticText(Data([0x6f, 0xff, 0x00, 0x6b])),
            "[invalid-utf8-hex:6fff006b]"
        )
    }

    func testDiagnosticErrorDescriptionPreservesNSErrorDetailsAndCause() {
        let cause = NSError(
            domain: "DiagnosticCauseDomain",
            code: 73,
            userInfo: [NSLocalizedDescriptionKey: "underlying cause sentinel"]
        )
        let error = NSError(
            domain: "DiagnosticTopDomain",
            code: 41,
            userInfo: [
                NSLocalizedDescriptionKey: "top level failure",
                "diagnostic-sentinel": "user info sentinel",
                NSUnderlyingErrorKey: cause,
            ]
        )

        let description = diagnosticErrorDescription(error)

        XCTAssertTrue(description.contains("DiagnosticTopDomain"))
        XCTAssertTrue(description.contains("code=41"))
        XCTAssertTrue(description.contains("top level failure"))
        XCTAssertTrue(description.contains("diagnostic-sentinel"))
        XCTAssertTrue(description.contains("user info sentinel"))
        XCTAssertTrue(description.contains("DiagnosticCauseDomain"))
        XCTAssertTrue(description.contains("code=73"))
        XCTAssertTrue(description.contains("underlying cause sentinel"))
    }
}
