#if os(iOS)
import UIKit

struct IOSLogVisibleFragment {
    let recordID: String
    let offset: Int
    let length: Int
    let text: String
    let frame: CGRect
    let isWholeRecord: Bool
    let isFullyVisible: Bool
}

final class IOSLogRecordAccessibilityElement: UIAccessibilityElement {
    weak var logView: DobbyLogTextView?

    override func accessibilityScroll(_ direction: UIAccessibilityScrollDirection) -> Bool {
        logView?.accessibilityScroll(direction) ?? false
    }
}

// Frames are in the text view's bounds coordinates; UIKit performs the screen conversion.
func visibleIOSLogRecordFragments(in view: DobbyLogTextView) -> [IOSLogVisibleFragment] {
    let storage = view.textStorage
    guard storage.length > 0, view.window != nil else { return [] }
    let layout = view.layoutManager
    let container = view.textContainer
    layout.ensureLayout(for: container)
    let inset = view.textContainerInset
    let viewport = view.bounds.offsetBy(dx: -inset.left, dy: -inset.top)
    let visibleGlyphs = layout.glyphRange(forBoundingRect: viewport, in: container)
    guard visibleGlyphs.length > 0 else { return [] }
    let visibleCharacters = layout.characterRange(forGlyphRange: visibleGlyphs, actualGlyphRange: nil)
    let text = storage.string as NSString
    var fragments: [IOSLogVisibleFragment] = []
    storage.enumerateAttribute(logRecordIDKey, in: visibleCharacters) { value, range, _ in
        guard let id = value as? String else { return }
        var record = NSRange()
        _ = storage.attribute(logRecordIDKey, at: range.location, longestEffectiveRange: &record,
                              in: NSRange(location: 0, length: storage.length))
        let content = NSRange(location: record.location, length: max(0, record.length - 1))
        let glyphs = layout.glyphRange(forCharacterRange: content, actualCharacterRange: nil)
        guard glyphs.length > 0 else { return }
        let fullBounds = layout.boundingRect(forGlyphRange: glyphs, in: container)
        // Ink bounds omit blank and whitespace-only lines. A whole-record
        // element is valid only when every actual laid-out line fits.
        var lineBounds = CGRect.null
        // Include the formatting delimiter: it may lay out a trailing blank
        // line after a message ending in a newline, although AX omits it.
        let recordGlyphs = layout.glyphRange(forCharacterRange: record, actualCharacterRange: nil)
        layout.enumerateLineFragments(forGlyphRange: recordGlyphs) { rect, _, _, _, _ in
            lineBounds = lineBounds.union(rect)
        }
        // Line allocation rectangles span the container width; only their vertical
        // extent describes blank lines. Horizontal visibility comes from ink bounds.
        if !fullBounds.isEmpty && !lineBounds.isNull && viewport.contains(fullBounds)
            && viewport.minY <= lineBounds.minY && viewport.maxY >= lineBounds.maxY {
            fragments.append(IOSLogVisibleFragment(recordID: id, offset: 0, length: content.length,
                text: text.substring(with: content), frame: fullBounds.offsetBy(dx: inset.left, dy: inset.top),
                isWholeRecord: true, isFullyVisible: true))
            return
        }
        let shown = NSIntersectionRange(glyphs, visibleGlyphs)
        layout.enumerateLineFragments(forGlyphRange: shown) { _, _, _, lineGlyphs, _ in
            let line = NSIntersectionRange(shown, lineGlyphs)
            guard line.length > 0 else { return }
            let characters = layout.characterRange(forGlyphRange: line, actualGlyphRange: nil)
            let composed = NSIntersectionRange(content, text.rangeOfComposedCharacterSequences(for: characters))
            guard composed.length > 0 else { return }
            let composedGlyphs = layout.glyphRange(forCharacterRange: composed, actualCharacterRange: nil)
            let bounds = layout.boundingRect(forGlyphRange: composedGlyphs, in: container)
            guard !bounds.isEmpty && bounds.intersects(viewport) else { return }
            fragments.append(IOSLogVisibleFragment(recordID: id, offset: composed.location - content.location,
                length: composed.length, text: text.substring(with: composed),
                frame: bounds.offsetBy(dx: inset.left, dy: inset.top), isWholeRecord: false,
                isFullyVisible: viewport.contains(bounds)))
        }
    }
    return fragments
}
#endif
