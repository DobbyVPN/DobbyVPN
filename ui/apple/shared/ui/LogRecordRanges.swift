import Foundation

// The attributed record identity is the mapping used by layout, reading anchors
// and accessibility. Message equality and paragraph boundaries do not identify records.
func logRecordRange(_ id: String, in text: NSAttributedString) -> NSRange? {
    var result: NSRange?
    text.enumerateAttribute(logRecordIDKey, in: NSRange(location: 0, length: text.length)) { value, range, stop in
        if value as? String == id { result = range; stop.pointee = true }
    }
    return result
}
