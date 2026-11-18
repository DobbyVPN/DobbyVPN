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

// Effective viewport in the text view's bounds coordinates, including scroll offset.
private func effectiveVisibleTextBounds(_ view: UITextView) -> CGRect? {
    guard let window = view.window else { return nil }
    var bounds = view.bounds.intersection(view.convert(window.bounds, from: window))
    var ancestor = view.superview
    while let current = ancestor {
        if current.clipsToBounds {
            bounds = bounds.intersection(view.convert(current.bounds, from: current))
        }
        ancestor = current.superview
    }
    return bounds.isNull || bounds.isEmpty ? nil : bounds
}

// Frames are in the text view's bounds coordinates; UIKit performs the screen conversion.
func visibleIOSLogRecordFragments(in view: DobbyLogTextView) -> [IOSLogVisibleFragment] {
    let storage = view.textStorage
    guard storage.length > 0, let visibleBounds = effectiveVisibleTextBounds(view) else { return [] }
    let layout = view.layoutManager
    let container = view.textContainer
    layout.ensureLayout(for: container)
    let inset = view.textContainerInset
    let viewport = visibleBounds.offsetBy(dx: -inset.left, dy: -inset.top)
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
// UTF-16 ranges describe only the visible line fragments of the rendered detail.
struct IOSErrorDetailFragment {
    let range: NSRange
    let text: String
    let frame: CGRect
    let isFullyVisible: Bool
}

final class IOSErrorDetailElement: UIAccessibilityElement {
    weak var detailView: DobbyErrorTextView?
    override func accessibilityScroll(_ direction: UIAccessibilityScrollDirection) -> Bool {
        detailView?.accessibilityScroll(direction) ?? false
    }
}

func configureErrorDetailTextView(_ view: DobbyErrorTextView) {
    view.isEditable = false
    view.isSelectable = false
    view.isScrollEnabled = true
    view.backgroundColor = .clear
    view.textContainerInset = .zero
    view.textContainer.lineFragmentPadding = 0
    view.accessibilityIdentifier = "error.detail"
    view.isAccessibilityElement = false
}

final class DobbyErrorTextView: UITextView {
    private var elementsByID: [String: IOSErrorDetailElement] = [:]

    override var accessibilityElements: [Any]? {
        get {
            var visible: [String: IOSErrorDetailElement] = [:]
            let elements = visibleIOSErrorDetailFragments(in: self).map { fragment in
                let id = "error.detail.fragment.\(fragment.range.location).\(fragment.range.length)" +
                    (fragment.isFullyVisible ? "" : ".partial")
                let element = elementsByID[id] ?? IOSErrorDetailElement(accessibilityContainer: self)
                element.detailView = self
                element.accessibilityIdentifier = id
                element.accessibilityLabel = fragment.text
                element.accessibilityTraits = .staticText
                element.accessibilityFrame = UIAccessibility.convertToScreenCoordinates(fragment.frame, in: self)
                visible[id] = element
                return element
            }
            elementsByID = visible
            return elements
        }
        set { super.accessibilityElements = newValue }
    }

    override func accessibilityScroll(_ direction: UIAccessibilityScrollDirection) -> Bool {
        let delta: CGFloat
        switch direction {
        case .up: delta = -1
        case .down: delta = 1
        default: return false
        }
        let minimum = -adjustedContentInset.top
        let maximum = max(minimum, contentSize.height - bounds.height + adjustedContentInset.bottom)
        let page = max(1, bounds.height - adjustedContentInset.top - adjustedContentInset.bottom) * 0.8
        let next = min(max(contentOffset.y + delta * page, minimum), maximum)
        guard abs(next - contentOffset.y) > 0.5 else { return false }
        setContentOffset(CGPoint(x: contentOffset.x, y: next), animated: false)
        UIAccessibility.post(notification: .pageScrolled, argument: nil)
        return true
    }
}

func visibleIOSErrorDetailFragments(in view: DobbyErrorTextView) -> [IOSErrorDetailFragment] {
    let storage = view.textStorage
    guard storage.length > 0, let visibleBounds = effectiveVisibleTextBounds(view) else { return [] }
    let layout = view.layoutManager
    let container = view.textContainer
    layout.ensureLayout(for: container)
    let inset = view.textContainerInset
    let viewport = visibleBounds.offsetBy(dx: -inset.left, dy: -inset.top)
    let glyphs = layout.glyphRange(forBoundingRect: viewport, in: container)
    guard glyphs.length > 0 else { return [] }
    let text = storage.string as NSString
    var result: [IOSErrorDetailFragment] = []
    layout.enumerateLineFragments(forGlyphRange: glyphs) { _, _, _, lineGlyphs, _ in
        let shown = NSIntersectionRange(glyphs, lineGlyphs)
        guard shown.length > 0 else { return }
        let characters = layout.characterRange(forGlyphRange: shown, actualGlyphRange: nil)
        var composed = text.rangeOfComposedCharacterSequences(for: characters)
        while composed.length > 0 {
            let character = text.character(at: NSMaxRange(composed) - 1)
            guard character == 10 || character == 13 else { break }
            composed.length -= 1
        }
        guard composed.length > 0 else { return }
        let fragmentGlyphs = layout.glyphRange(forCharacterRange: composed, actualCharacterRange: nil)
        let bounds = layout.boundingRect(forGlyphRange: fragmentGlyphs, in: container)
        guard !bounds.isEmpty && bounds.intersects(viewport) else { return }
        result.append(IOSErrorDetailFragment(range: composed, text: text.substring(with: composed),
            frame: bounds.offsetBy(dx: inset.left, dy: inset.top), isFullyVisible: viewport.contains(bounds)))
    }
    return result
}
#endif
