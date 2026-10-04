@testable import DobbyNativeUI
import XCTest

final class SubscriptionTests: XCTestCase {
    func testImportDecodesExactlyOnce() throws {
        XCTAssertNil(try subscriptionFromLink("dobbyvpn://"))
        XCTAssertEqual(try subscriptionFromLink("dobbyvpn://import?url=https%3A%2F%2Fexample.com%2F%252F"), "https://example.com/%2F")
        for invalid in [
            "dobbyvpn://import", "dobbyvpn://import?url=", "dobbyvpn://import?url=https%3A%2F%2Fexample.com&url=https%3A%2F%2Fother.com",
            "dobbyvpn://import?url=http%3A%2F%2Fexample.com", "dobbyvpn://import?url=https%3A%2F%2F",
            "dobbyvpn://import?url=https%3A%2F%2Fexample.com%XX", "dobbyvpn://import?url=https%253A%252F%252Fexample.com",
            "other://import?url=https%3A%2F%2Fexample.com", "dobbyvpn://user@import?url=https%3A%2F%2Fexample.com",
        ] { XCTAssertThrowsError(try subscriptionFromLink(invalid), invalid) }
    }
}
