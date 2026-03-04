import XCTest

final class MaskStringFunctionalTest: XCTestCase {
    
    func testMaskRegularString() {
        XCTAssertEqual(maskStr(value: "hello"), "h***o")
    }
    
    func testMaskLongString() {
        XCTAssertEqual(maskStr(value: "password123"), "p***3")
    }
    
    func testMaskThreeCharString() {
        XCTAssertEqual(maskStr(value: "abc"), "a***c")
    }
    
    func testMaskTwoCharStringNotMasked() {
        XCTAssertEqual(maskStr(value: "ab"), "ab")
    }
    
    func testMaskSingleCharNotMasked() {
        XCTAssertEqual(maskStr(value: "a"), "a")
    }
    
    func testMaskEmptyStringNotMasked() {
        XCTAssertEqual(maskStr(value: ""), "")
    }
    
    func testMaskIPAddress() {
        XCTAssertEqual(maskStr(value: "192.168.1.1"), "1***1")
    }
    
    func testMaskEmail() {
        XCTAssertEqual(maskStr(value: "user@example.com"), "u***m")
    }
    
    func testMaskWithSpaces() {
        XCTAssertEqual(maskStr(value: "hello world"), "h***d")
    }
    
    func testMaskWithSpecialChars() {
        XCTAssertEqual(maskStr(value: "p@ss!"), "p***!")
    }
    
    func testMaskUnicodeString() {
        XCTAssertEqual(maskStr(value: "привет"), "п***т")
    }
    
    func testMaskWithNumbers() {
        XCTAssertEqual(maskStr(value: "12345"), "1***5")
    }
}

// MARK: - Testable function extracted from CopyLogsInteractorImpl

func maskStr(value: String) -> String {
    guard value.count > 2 else { return value }
    
    let first = value[value.startIndex]
    let last = value[value.index(before: value.endIndex)]
    return "\(first)***\(last)"
}
