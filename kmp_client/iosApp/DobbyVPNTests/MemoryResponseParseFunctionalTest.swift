import XCTest

final class MemoryResponseParseFunctionalTest: XCTestCase {
    
    func testParseValidMemoryResponse() {
        let response = "Memory:42.5".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), 42.5)
    }
    
    func testParseMemoryResponseInteger() {
        let response = "Memory:100".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), 100.0)
    }
    
    func testParseMemoryResponseZero() {
        let response = "Memory:0".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), 0.0)
    }
    
    func testParseMemoryResponseEmptyValue() {
        let response = "Memory:".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), -1)
    }
    
    func testParseMemoryResponseNilData() {
        XCTAssertEqual(parseMemoryResponse(nil), -1)
    }
    
    func testParseMemoryResponseWrongPrefix() {
        let response = "NotMemory:10".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), -1)
    }
    
    func testParseMemoryResponseInvalidNumber() {
        let response = "Memory:abc".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), -1)
    }
    
    func testParseMemoryResponseEmptyData() {
        let response = Data()
        XCTAssertEqual(parseMemoryResponse(response), -1)
    }
    
    func testParseMemoryResponseNegativeValue() {
        let response = "Memory:-5.5".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), -5.5)
    }
    
    func testParseMemoryResponseLargeValue() {
        let response = "Memory:1024.75".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), 1024.75)
    }
    
    func testParseMemoryResponseWithWhitespace() {
        let response = "Memory: 42.5".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), -1)
    }
    
    func testParseMemoryResponseScientificNotation() {
        let response = "Memory:1.5e2".data(using: .utf8)
        XCTAssertEqual(parseMemoryResponse(response), 150.0)
    }
}

// MARK: - Testable function extracted from HealthCheckImpl

func parseMemoryResponse(_ response: Data?) -> Double {
    guard let response,
          let str = String(data: response, encoding: .utf8),
          str.hasPrefix("Memory:")
    else { return -1 }
    let value = str.replacingOccurrences(of: "Memory:", with: "")
    return Double(value) ?? -1
}
