#if os(macOS)
import AppKit

struct MacLogVisibleFragment {
    let recordID: String
    let offset: Int
    let length: Int
    let text: String
    let frame: CGRect
    let isWholeRecord: Bool
}

struct MacLogAccessibilityObservation {
    let parentRole: String
    let visibleCount: Int
    let firstID: String
    let firstRole: String
    let firstFrame: CGRect
}

// Role-specific virtual elements use AppKit's standard accessibility protocol.
// Retain them in the renderer; their parent remains weak to avoid a cycle.
final class MacLogRecordAccessibilityElement: NSObject, NSAccessibilityStaticText {
    private weak var parentView: NSView?
    private let identifier: String
    private var recordText = ""
    private var screenFrame = NSRect.zero

    init(identifier: String, parent: NSView) {
        self.identifier = identifier
        self.parentView = parent
        super.init()
    }

    func accessibilityFrame() -> NSRect { screenFrame }
    func accessibilityParent() -> Any? { parentView }
    func accessibilityIdentifier() -> String { identifier }
    func accessibilityValue() -> String? { recordText }
    @objc func accessibilityRole() -> NSAccessibility.Role? { .staticText }
    @objc func isAccessibilityElement() -> Bool { true }

    func update(text: String, screenFrame: CGRect) {
        recordText = text
        self.screenFrame = screenFrame
    }
}

final class DobbyLogTextView: NSTextView {
    var onSendLogs: () -> Void = {}
    var onUploadLogs: (() -> Void)?
    var onClear: () -> Void = {}
    var onAccessibilityObservation: ((MacLogAccessibilityObservation) -> Void)?
    private var recordElements: [String: MacLogRecordAccessibilityElement] = [:]
    private var reportedAccessibilityObservation = false

    override func accessibilityChildren() -> [Any]? {
        guard let window else { recordElements.removeAll(); return [] }
        let fragments = visibleRecordFragments()
        var visible: [String: MacLogRecordAccessibilityElement] = [:]
        let elements: [MacLogRecordAccessibilityElement] = fragments.compactMap { fragment in
            let screenFrame = window.convertToScreen(convert(fragment.frame, to: nil))
            let suffix = fragment.isWholeRecord ? "" : ".fragment.\(fragment.offset).\(fragment.length)"
            let identifier = "logs.record." + fragment.recordID + suffix
            let child: MacLogRecordAccessibilityElement
            if let existing = recordElements[identifier] {
                child = existing
            } else {
                child = MacLogRecordAccessibilityElement(identifier: identifier, parent: self)
            }
            child.update(text: fragment.text, screenFrame: screenFrame)
            visible[identifier] = child
            return child
        }
        recordElements = visible
        if !reportedAccessibilityObservation, let first = elements.first, let onAccessibilityObservation {
            reportedAccessibilityObservation = true
            onAccessibilityObservation(MacLogAccessibilityObservation(
                parentRole: accessibilityRole()?.rawValue ?? "unavailable", visibleCount: elements.count,
                firstID: first.accessibilityIdentifier(),
                firstRole: first.accessibilityRole()?.rawValue ?? "unavailable", firstFrame: first.accessibilityFrame()))
        }
        return elements
    }

    func visibleRecordFragments() -> [MacLogVisibleFragment] {
        visibleMacLogRecordFragments(in: self)
    }

    private var logsMenu: NSMenu {
        let menu = NSMenu()
        if let onUploadLogs { menu.addItem(LogsMenuItem("Upload logs", handler: onUploadLogs)) }
        menu.addItem(LogsMenuItem("Send logs") { [weak self] in self?.onSendLogs() })
        menu.addItem(LogsMenuItem("Clear") { [weak self] in self?.onClear() })
        return menu
    }

    override func menu(for event: NSEvent) -> NSMenu? { logsMenu }

    override func mouseDown(with event: NSEvent) {
        if event.clickCount == 2 {
            NSMenu.popUpContextMenu(logsMenu, with: event, for: self)
        } else {
            super.mouseDown(with: event)
        }
    }
}

func visibleMacLogRecordFragments(in view: NSTextView) -> [MacLogVisibleFragment] {
    guard let storage = view.textStorage, storage.length > 0, let layout = view.layoutManager,
          let container = view.textContainer, view.enclosingScrollView != nil else { return [] }
    layout.ensureLayout(for: container)
    let origin = view.textContainerOrigin
    let viewport = view.visibleRect.offsetBy(dx: -origin.x, dy: -origin.y)
    let visibleGlyphs = layout.glyphRange(forBoundingRect: viewport, in: container)
    guard visibleGlyphs.length > 0 else { return [] }
    let visibleCharacters = layout.characterRange(forGlyphRange: visibleGlyphs, actualGlyphRange: nil)
    let text = storage.string as NSString
    var fragments: [MacLogVisibleFragment] = []
    storage.enumerateAttribute(logRecordIDKey, in: visibleCharacters, options: []) { value, range, _ in
        guard let id = value as? String else { return }
        var fullRange = NSRange()
        _ = storage.attribute(logRecordIDKey, at: range.location, longestEffectiveRange: &fullRange,
                              in: NSRange(location: 0, length: storage.length))
        let contentRange = NSRange(location: fullRange.location, length: max(0, fullRange.length - 1))
        let glyphs = layout.glyphRange(forCharacterRange: contentRange, actualCharacterRange: nil)
        guard glyphs.length > 0 else { return }
        let fullBounds = layout.boundingRect(forGlyphRange: glyphs, in: container)
        // Ink bounds omit blank and whitespace-only lines. A whole-record
        // element is valid only when every actual laid-out line fits.
        var lineBounds = CGRect.null
        layout.enumerateLineFragments(forGlyphRange: glyphs) { rect, _, _, _, _ in
            lineBounds = lineBounds.union(rect)
        }
        if !fullBounds.isEmpty && !lineBounds.isNull && viewport.contains(fullBounds) && viewport.contains(lineBounds) {
            fragments.append(MacLogVisibleFragment(recordID: id, offset: 0, length: contentRange.length,
                text: text.substring(with: contentRange),
                frame: fullBounds.offsetBy(dx: origin.x, dy: origin.y), isWholeRecord: true))
            return
        }
        let shownGlyphs = NSIntersectionRange(glyphs, visibleGlyphs)
        layout.enumerateLineFragments(forGlyphRange: shownGlyphs) { _, _, _, lineGlyphs, _ in
            let shownLine = NSIntersectionRange(shownGlyphs, lineGlyphs)
            guard shownLine.length > 0 else { return }
            let characters = layout.characterRange(forGlyphRange: shownLine, actualGlyphRange: nil)
            let composed = NSIntersectionRange(contentRange, text.rangeOfComposedCharacterSequences(for: characters))
            guard composed.length > 0 else { return }
            let composedGlyphs = layout.glyphRange(forCharacterRange: composed, actualCharacterRange: nil)
            let bounds = layout.boundingRect(forGlyphRange: composedGlyphs, in: container).intersection(viewport)
            guard !bounds.isEmpty else { return }
            fragments.append(MacLogVisibleFragment(recordID: id, offset: composed.location - contentRange.location,
                length: composed.length, text: text.substring(with: composed),
                frame: bounds.offsetBy(dx: origin.x, dy: origin.y), isWholeRecord: false))
        }
    }
    return fragments
}


private final class LogsMenuItem: NSMenuItem {
    private let handler: () -> Void

    init(_ title: String, handler: @escaping () -> Void) {
        self.handler = handler
        super.init(title: title, action: #selector(run), keyEquivalent: "")
        target = self
    }

    required init(coder: NSCoder) { fatalError("init(coder:) is not used") }

    @objc private func run() { handler() }
}

#endif
