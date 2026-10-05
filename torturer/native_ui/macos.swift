import AppKit
import ApplicationServices
import CoreGraphics
import Foundation

// Test-only accessibility process. Python bounds its lifetime if an OS call stalls.
struct HelperError: Error, CustomStringConvertible {
    let description: String
    init(_ message: String) { description = message }
}

struct AccessibilityReadError: Error, CustomStringConvertible {
    let attribute: String
    let code: AXError

    var description: String { "AX read \(attribute) failed: \(code.rawValue)" }
}

func require(_ condition: Bool, _ message: String) throws {
    if !condition { throw HelperError(message) }
}

func attribute(_ element: AXUIElement, _ name: String) throws -> CFTypeRef? {
    var value: CFTypeRef?
    let code = AXUIElementCopyAttributeValue(element, name as CFString, &value)
    if code == .noValue || code == .attributeUnsupported { return nil }
    if code != .success { throw AccessibilityReadError(attribute: name, code: code) }
    return value
}

func axValue(_ element: AXUIElement, _ name: String) throws -> AXValue? {
    guard let value = try attribute(element, name), CFGetTypeID(value) == AXValueGetTypeID() else {
        return nil
    }
    return unsafeBitCast(value, to: AXValue.self)
}

func axElement(_ element: AXUIElement, _ name: String) throws -> AXUIElement? {
    guard let value = try attribute(element, name), CFGetTypeID(value) == AXUIElementGetTypeID() else {
        return nil
    }
    return unsafeBitCast(value, to: AXUIElement.self)
}

func label(_ element: AXUIElement, _ name: String) throws -> String {
    (try attribute(element, name)) as? String ?? ""
}

func elements(_ window: AXUIElement) throws -> [AXUIElement] {
    var queue = [window]
    var index = 0
    while index < queue.count {
        try require(queue.count <= 8192, "Accessibility tree exceeds 8192 elements")
        let element = queue[index]
        index += 1
        // Log details change during refresh; controls live outside the text view.
        if try label(element, kAXRoleAttribute) == kAXTextAreaRole { continue }
        let children = try attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? []
        queue.append(contentsOf: children)
    }
    return queue
}

func names(_ element: AXUIElement) throws -> [String] {
    var values = try [kAXIdentifierAttribute, kAXTitleAttribute, kAXValueAttribute]
        .compactMap { name -> String? in
            // Text fields are verified by type, not copied into every tree response.
            if name == kAXValueAttribute {
                let role = try label(element, kAXRoleAttribute)
                if role == kAXTextAreaRole || role == kAXTextFieldRole { return nil }
            }
            let value = try label(element, name)
            return value.isEmpty ? nil : value
        }
    let role = try label(element, kAXRoleAttribute)
    do {
        let description = try attribute(element, kAXDescriptionAttribute) as? String ?? ""
        if !description.isEmpty { values.append(description) }
    } catch let error as AccessibilityReadError where error.code == .failure {
        FileHandle.standardError.write(
            Data("Optional AXDescription read failed; role=\(role) error=\(error) errorCode=\(error.code.rawValue)\n".utf8)
        )
    }
    return values
}

func linkURLs(_ nodes: [AXUIElement]) throws -> [String] {
    try nodes.compactMap { element in
        guard try label(element, kAXRoleAttribute) == "AXLink",
              let value = try attribute(element, kAXURLAttribute) else { return nil }
        if let url = value as? URL { return url.absoluteString }
        return value as? String
    }
}

func find(_ nodes: [AXUIElement], _ name: String, editor: Bool = false) throws -> AXUIElement {
    var matches = try nodes.filter { node in
        if editor {
            let role = try label(node, kAXRoleAttribute)
            if role != kAXTextAreaRole && role != kAXTextFieldRole { return false }
        }
        return try names(node).contains(name)
    }
    let identified = try matches.filter { try label($0, kAXIdentifierAttribute) == name }
    if !identified.isEmpty { matches = identified }
    if matches.count > 1 {
        // SwiftUI's toolbar item and its inner button both expose AXButton with
        // the same name. Select the inner control, retaining ambiguity for peers.
        let candidates = matches
        matches = try candidates.filter { parent in
            let descendants = try elements(parent).dropFirst()
            return !descendants.contains { child in candidates.contains { CFEqual(child, $0) } }
        }
    }
    if matches.count != 1 {
        let details = try matches.map { element in
            "\(element): role=\(try label(element, kAXRoleAttribute)) " +
                "names=\(try names(element)) position=\(String(describing: try attribute(element, kAXPositionAttribute))) " +
                "size=\(String(describing: try attribute(element, kAXSizeAttribute)))"
        }
        throw HelperError("Expected one visible \(name), found \(matches.count): \(details)")
    }
    let element = matches[0]
    try require((try attribute(element, kAXEnabledAttribute) as? Bool) != false, "Control disabled: \(name)")
    return element
}

func press(_ element: AXUIElement) throws {
    let code = AXUIElementPerformAction(element, kAXPressAction as CFString)
    try require(code == .success, "Native AX press failed: \(code.rawValue)")
}

func key(_ code: CGKeyCode, command: Bool = true) throws {
    guard let down = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: true),
          let up = CGEvent(keyboardEventSource: nil, virtualKey: code, keyDown: false) else {
        throw HelperError("Could not create native keyboard event")
    }
    if command { down.flags = .maskCommand; up.flags = .maskCommand }
    down.post(tap: .cghidEventTap)
    up.post(tap: .cghidEventTap)
}

func paste(_ editor: AXUIElement, source: String) throws {
    let value = try String(contentsOfFile: source, encoding: .utf8)
    let board = NSPasteboard.general
    let previous = (board.pasteboardItems ?? []).map { original in
        let copy = NSPasteboardItem()
        for type in original.types {
            if let data = original.data(forType: type) { copy.setData(data, forType: type) }
        }
        return copy
    }
    var primary: Error?
    do {
        try require(AXUIElementSetAttributeValue(editor, kAXFocusedAttribute as CFString, kCFBooleanTrue) == .success,
                    "Could not focus native configuration editor")
        board.clearContents()
        try require(board.setString(value, forType: .string), "Could not write native clipboard")
        try key(0) // Cmd+A
        try key(9) // Cmd+V
        let deadline = Date().addingTimeInterval(5)
        var observed = ""
        var lastTransientValueError: AccessibilityReadError?
        repeat {
            do {
                observed = try label(editor, kAXValueAttribute)
            } catch let error as AccessibilityReadError where error.code == .cannotComplete {
                lastTransientValueError = error
                FileHandle.standardError.write(
                    Data("AXValue read failed; role=AXTextArea error=\(error) errorCode=\(error.code.rawValue); retrying\n".utf8)
                )
            }
            if observed == value { break }
            if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
        } while Date() < deadline
        if observed != value, let error = lastTransientValueError {
            throw HelperError(
                "Native pasted configuration does not match the source; last transient AXValue read error=\(error) errorCode=\(error.code.rawValue)"
            )
        }
        try require(observed == value, "Native pasted configuration does not match the source")
    } catch { primary = error }
    board.clearContents()
    if !previous.isEmpty && !board.writeObjects(previous) {
        throw HelperError("\(primary.map { String(describing: $0) } ?? "Paste completed"); clipboard restoration failed")
    }
    if let error = primary { throw error }
}

func pasteWithNativeControl(_ app: NSRunningApplication, window: AXUIElement, source: String) throws {
    let value = try String(contentsOfFile: source, encoding: .utf8)
    let initialNodes = try elements(window)
    let editor = try find(initialNodes, "Connection configuration", editor: true)
    let fieldBeforeClipboard = try label(editor, kAXValueAttribute)
    let board = NSPasteboard.general
    let previous = (board.pasteboardItems ?? []).map { original in
        let copy = NSPasteboardItem()
        for type in original.types {
            if let data = original.data(forType: type) { copy.setData(data, forType: type) }
        }
        return copy
    }
    var primary: Error?
    do {
        func refreshPasteAvailability(_ reason: String, expected: Bool) throws -> (editor: AXUIElement, button: AXUIElement?) {
            if let finder = NSWorkspace.shared.runningApplications.first(where: { $0.bundleIdentifier == "com.apple.finder" }) {
                _ = finder.activate(options: [.activateAllWindows])
                Thread.sleep(forTimeInterval: 0.1)
            }
            try require(app.activate(options: [.activateAllWindows]), "Could not refresh native Paste availability for \(reason) clipboard")
            let deadline = Date().addingTimeInterval(5)
            var lastAvailabilityError: HelperError?
            repeat {
                let nodes = try elements(window)
                let currentEditor = try find(nodes, "Connection configuration", editor: true)
                try require(try label(currentEditor, kAXValueAttribute) == fieldBeforeClipboard,
                            "\(reason) clipboard availability changed the configuration before Paste was tapped")
                let buttons = try nodes.filter { try label($0, kAXRoleAttribute) == kAXButtonRole }
                if expected {
                    do { return (currentEditor, try find(buttons, "Paste")) }
                    catch let error as HelperError { lastAvailabilityError = error }
                } else {
                    let pasteAvailable = try buttons.contains { try names($0).contains("Paste") }
                    if !pasteAvailable { return (currentEditor, nil) }
                }
                if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
            } while Date() < deadline
            if expected, let lastAvailabilityError { throw lastAvailabilityError }
            if expected { throw HelperError("Native Paste button did not become available for a \(reason) clipboard") }
            throw HelperError("Native Paste button was available for a \(reason) clipboard")
        }

        board.clearContents()
        _ = try refreshPasteAvailability("empty", expected: false)
        try require(
            board.setData(Data("synthetic image payload".utf8), forType: NSPasteboard.PasteboardType("public.png")),
            "Could not write synthetic non-text clipboard item"
        )
        _ = try refreshPasteAvailability("non-text", expected: false)

        board.clearContents()
        try require(board.setString(value, forType: .string), "Could not write native clipboard")
        let availability = try refreshPasteAvailability("text", expected: true)
        let currentEditor = availability.editor
        guard let pasteButton = availability.button else { throw HelperError("Native Paste button was not available for a text clipboard") }
        try press(pasteButton)
        let deadline = Date().addingTimeInterval(5)
        var observed = ""
        repeat {
            observed = try label(currentEditor, kAXValueAttribute)
            if observed == value { break }
            if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
        } while Date() < deadline
        try require(observed == value, "Native Paste button did not fill the subscription URL")
    } catch { primary = error }
    board.clearContents()
    if !previous.isEmpty && !board.writeObjects(previous) {
        throw HelperError("\(primary.map { String(describing: $0) } ?? "Paste completed"); clipboard restoration failed")
    }
    if let error = primary { throw error }
}

func capture(_ pid: pid_t, path: String) throws {
    func windowInfo() throws -> (CGWindowID, CGRect) {
        guard let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID)
                as? [[String: Any]],
              let index = windows.indices.filter({
                  (windows[$0][kCGWindowOwnerPID as String] as? Int) == Int(pid) &&
                  (windows[$0][kCGWindowLayer as String] as? Int) == 0
              }).max(by: { left, right in
                  func area(_ index: Int) -> CGFloat {
                      guard let raw = windows[index][kCGWindowBounds as String] as? NSDictionary,
                            let bounds = CGRect(dictionaryRepresentation: raw as CFDictionary) else { return 0 }
                      return bounds.width * bounds.height
                  }
                  return area(left) < area(right)
              }),
              let number = windows[index][kCGWindowNumber as String] as? UInt32,
              let raw = windows[index][kCGWindowBounds as String] as? NSDictionary,
              let bounds = CGRect(dictionaryRepresentation: raw as CFDictionary) else {
            throw HelperError("Native window geometry unavailable")
        }
        try require(bounds.width >= 300 && bounds.height >= 300, "Native window is unexpectedly small")
        var displays = [CGDirectDisplayID](repeating: 0, count: 32)
        var count: UInt32 = 0
        try require(CGGetActiveDisplayList(UInt32(displays.count), &displays, &count) == .success, "Display geometry unavailable")
        let displayBounds = displays.prefix(Int(count)).map { CGDisplayBounds($0) }
        try require(displayBounds.contains { $0.contains(bounds) }, "Native window is partly offscreen")
        for obstruction in windows[..<index] {
            if (obstruction[kCGWindowLayer as String] as? Int) == Int(CGWindowLevelForKey(.cursorWindow)) {
                continue
            }
            guard (obstruction[kCGWindowAlpha as String] as? Double ?? 1) > 0,
                  let raw = obstruction[kCGWindowBounds as String] as? NSDictionary,
                  let rect = CGRect(dictionaryRepresentation: raw as CFDictionary), rect.intersects(bounds) else { continue }
            // An attached native sheet belongs in the main-window capture.
            if (obstruction[kCGWindowOwnerPID as String] as? Int) == Int(pid), bounds.contains(rect) { continue }
            let owner = obstruction[kCGWindowOwnerName as String] as? String
            let layer = obstruction[kCGWindowLayer as String] as? Int
            let systemSurface = (owner == "Dock" && layer == 20) ||
                (owner == "Notification Center" && layer == 23)
            if systemSurface && displayBounds.contains(where: { $0 == rect }) {
                FileHandle.standardError.write(
                    Data("Ignoring full-display system backing surface: \(obstruction)\n".utf8)
                )
                continue
            }
            throw HelperError("Native screenshot obstructed: \(obstruction)")
        }
        return (number, bounds)
    }
    let (id, bounds) = try windowInfo()
    let task = Process()
    task.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    // Capture visible pixels, including sheets layered above the main window.
    let region = [bounds.minX, bounds.minY, bounds.width, bounds.height].map { String(Int($0)) }.joined(separator: ",")
    task.arguments = ["-x", "-t", "png", "-R", region, path]
    // screencapture normally writes no stdout; both original streams remain visible.
    task.standardOutput = FileHandle.standardError
    task.standardError = FileHandle.standardError
    try task.run()
    task.waitUntilExit()
    try require(task.terminationStatus == 0, "Native screencapture failed: \(task.terminationStatus)")
    let (afterID, afterBounds) = try windowInfo()
    try require(id == afterID && bounds == afterBounds, "Native window changed during screenshot")
}

func run() throws -> [String: Any] {
    let data = FileHandle.standardInput.readDataToEndOfFile()
    guard let request = try JSONSerialization.jsonObject(with: data) as? [String: Any],
          let operation = request["operation"] as? String,
          let executable = request["executable"] as? String else { throw HelperError("Invalid helper request") }
    if operation == "preflight" {
        let helper = CommandLine.arguments[0]
        try require(AXIsProcessTrusted(), "Accessibility permission unavailable for native helper \(helper)")
        try require(CGPreflightScreenCaptureAccess(), "Screen recording permission unavailable for native helper \(helper)")
        return ["ready": true]
    }
    let expected = URL(fileURLWithPath: executable).resolvingSymlinksInPath().path
    let requestedPID = request["pid"] as? Int
    let matches = NSWorkspace.shared.runningApplications.filter { app in
        app.executableURL?.resolvingSymlinksInPath().path == expected &&
        (requestedPID == nil || Int(app.processIdentifier) == requestedPID)
    }
    if matches.isEmpty { return ["ready": false, "alive": false] }
    try require(matches.count == 1, "UI executable has \(matches.count) matching processes")
    let app = matches[0]
    guard let launched = app.launchDate else { throw HelperError("UI process creation time unavailable") }
    let identity = String(launched.timeIntervalSince1970)
    if let prior = request["identity"] as? String { try require(prior == identity, "UI process creation time changed") }
    let pid = app.processIdentifier
    if operation == "probe" { return ["alive": true, "pid": Int(pid), "identity": identity] }
    if operation == "close" {
        try require(app.terminate(), "Native application quit request failed")
        return ["alive": true]
    }
    if operation == "kill" {
        try require(app.forceTerminate(), "Could not terminate owned UI process")
        return [:]
    }
    try require(AXIsProcessTrusted(), "Accessibility permission unavailable")
    func activate() throws {
        try require(app.activate(options: [.activateAllWindows]), "Could not activate native app")
        let deadline = Date().addingTimeInterval(2)
        while NSWorkspace.shared.frontmostApplication?.processIdentifier != pid && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.05)
        }
        try require(NSWorkspace.shared.frontmostApplication?.processIdentifier == pid, "Native app did not become foreground")
    }
    if operation == "scroll-logs" { try activate() }
    let root = AXUIElementCreateApplication(pid)
    AXUIElementSetMessagingTimeout(root, 1)
    let nodes: [AXUIElement]
    do {
        guard let windows = try attribute(root, kAXWindowsAttribute) as? [AXUIElement],
              let window = windows.first else {
            return ["ready": false, "pid": Int(pid), "identity": identity]
        }
        nodes = try elements(window)
        if operation == "tree" {
            let labels = try nodes.flatMap(names)
            let enabled = try nodes.filter { (try attribute($0, kAXEnabledAttribute)) as? Bool == true }.flatMap(names)
            return ["ready": true, "alive": true, "pid": Int(pid), "identity": identity,
                    "window_count": windows.count, "window_id": try label(window, kAXIdentifierAttribute),
                    "labels": labels, "enabled_controls": enabled, "link_urls": try linkURLs(nodes)]
        }
    } catch {
        if operation == "tree", let readError = error as? AccessibilityReadError,
           readError.code == .failure || readError.code == .cannotComplete || readError.code == .invalidUIElement ||
            (readError.code == .illegalArgument && readError.attribute == kAXRoleAttribute) {
            // SwiftUI can invalidate a node between children enumeration and
            // AXRole reads when loading/error controls change. Rediscover the
            // read-only tree within Python's existing action deadline.
            FileHandle.standardError.write(Data("\(readError) during tree discovery; retrying\n".utf8))
            return ["ready": false, "alive": true, "pid": Int(pid), "identity": identity]
        }
        throw error
    }
    if operation == "logs" {
        let view = try find(nodes, "Connection logs", editor: true)
        return ["ready": true, "text": try label(view, kAXValueAttribute)]
    }
    if operation == "log-position" {
        let view = try find(nodes, "Connection logs", editor: true)
        guard let rangeValue = try axValue(view, kAXVisibleCharacterRangeAttribute),
              let rawCount = try attribute(view, kAXNumberOfCharactersAttribute) as? NSNumber else {
            throw HelperError("Native log viewer does not expose its reading position")
        }
        var range = CFRange(location: 0, length: 0)
        try require(AXValueGetValue(rangeValue, .cfRange, &range), "Could not read native log visible character range")
        var parent: AXUIElement? = view
        var scrollArea: AXUIElement?
        for _ in 0..<8 {
            guard let current = parent,
                  let next = try axElement(current, kAXParentAttribute) else { break }
            if try label(next, kAXRoleAttribute) == kAXScrollAreaRole {
                scrollArea = next
                break
            }
            parent = next
        }
        guard let area = scrollArea,
              let scrollbar = try axElement(area, kAXVerticalScrollBarAttribute),
              let value = try attribute(scrollbar, kAXValueAttribute) as? NSNumber else {
            throw HelperError("Native log viewer has no readable vertical position")
        }
        return ["ready": true, "visible_range_start": range.location,
                "visible_range_end": range.location + range.length,
                "character_count": rawCount.intValue, "scrollbar_position": value.doubleValue]
    }
    if operation == "select-log-text" {
        let view = try find(nodes, "Connection logs", editor: true)
        guard let rawCount = try attribute(view, kAXNumberOfCharactersAttribute) as? NSNumber,
              rawCount.intValue > 0 else {
            throw HelperError("Native log viewer has no selectable text")
        }
        var range = CFRange(location: 0, length: rawCount.intValue)
        guard let selectedRange = AXValueCreate(.cfRange, &range) else {
            throw HelperError("Could not create native selected text range")
        }
        try require(AXUIElementSetAttributeValue(view, kAXSelectedTextRangeAttribute as CFString, selectedRange) == .success,
                    "Native log viewer rejected text selection")
        let selected = try label(view, kAXSelectedTextAttribute)
        try require(!selected.isEmpty, "Native log viewer returned no selected text")
        return ["ready": true, "selected": selected]
    }
    if operation == "scroll-logs" {
        guard let position = request["position"] as? String, position == "top" || position == "bottom" else {
            throw HelperError("Log scroll position must be top or bottom")
        }
        let view = try find(nodes, "Connection logs", editor: true)
        guard let positionValue = try axValue(view, kAXPositionAttribute),
              let sizeValue = try axValue(view, kAXSizeAttribute) else {
            throw HelperError("Native log viewer has no accessible bounds")
        }
        var origin = CGPoint.zero
        var size = CGSize.zero
        try require(AXValueGetValue(positionValue, .cgPoint, &origin), "Could not read native log position")
        try require(AXValueGetValue(sizeValue, .cgSize, &size), "Could not read native log size")
        try require(size.width > 0 && size.height > 0, "Native log viewer has empty bounds")

        var parent: AXUIElement? = view
        var scrollArea: AXUIElement?
        for _ in 0..<8 {
            guard let current = parent,
                  let next = try axElement(current, kAXParentAttribute) else { break }
            if try label(next, kAXRoleAttribute) == kAXScrollAreaRole {
                scrollArea = next
                break
            }
            parent = next
        }
        guard let area = scrollArea,
              let scrollbar = try axElement(area, kAXVerticalScrollBarAttribute) else {
            throw HelperError("Native log viewer does not expose a vertical scrollbar")
        }
        func visibleRange() throws -> CFRange {
            guard let value = try axValue(view, kAXVisibleCharacterRangeAttribute) else {
                throw HelperError("Native log viewer has no visible character range")
            }
            var range = CFRange(location: 0, length: 0)
            try require(AXValueGetValue(value, .cfRange, &range), "Could not read native log visible character range")
            return range
        }
        guard let rawCharacterCount = try attribute(view, kAXNumberOfCharactersAttribute) as? NSNumber else {
            throw HelperError("Native log viewer has no character count")
        }
        let characterCount = rawCharacterCount.intValue
        try require(characterCount > 0, "Native log viewer has no content to scroll")
        func checkedVisibleRange() throws -> CFRange {
            let range = try visibleRange()
            try require(range.location >= 0 && range.length > 0 && range.location + range.length <= characterCount,
                        "Native log viewer returned an invalid visible character range")
            return range
        }
        func distanceFromTarget(_ range: CFRange) -> Int {
            position == "top" ? range.location : characterCount - range.location - range.length
        }
        func isAtTarget(_ range: CFRange) -> Bool {
            distanceFromTarget(range) <= 1
        }
        var range = try checkedVisibleRange()
        try require(range.length < characterCount, "Native log viewer does not overflow its viewport; freeze/resume cannot be tested")
        if !isAtTarget(range) {
            let center = CGPoint(x: origin.x + size.width / 2, y: origin.y + size.height / 2)
            guard let moved = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                                      mouseCursorPosition: center, mouseButton: .left) else {
                throw HelperError("Could not position the pointer over native logs")
            }
            moved.post(tap: .cghidEventTap)
            var reached = false
            for delta in [100, -100] {
                var unchanged = 0
                for _ in 0..<16 {
                    guard let event = CGEvent(scrollWheelEvent2Source: nil, units: .line,
                                              wheelCount: 1, wheel1: Int32(delta), wheel2: 0, wheel3: 0) else {
                        throw HelperError("Could not create native log scroll event")
                    }
                    event.location = center
                    event.post(tap: .cghidEventTap)
                    Thread.sleep(forTimeInterval: 0.05)
                    let updated = try checkedVisibleRange()
                    if isAtTarget(updated) {
                        reached = true
                        range = updated
                        break
                    }
                    let previousDistance = distanceFromTarget(range)
                    let updatedDistance = distanceFromTarget(updated)
                    if updatedDistance > previousDistance {
                        range = updated
                        break
                    }
                    if updatedDistance == previousDistance {
                        unchanged += 1
                        if unchanged >= 2 { break }
                    } else {
                        unchanged = 0
                    }
                    range = updated
                }
                if reached { break }
            }
            let visibleRangeDescription = "\(range.location)..<\(range.location + range.length)"
            try require(reached,
                        "Native log viewer did not scroll to \(position); " +
                            "visible range=\(visibleRangeDescription), characters=\(characterCount)")
        }
        guard let rawScrollbarPosition = try attribute(scrollbar, kAXValueAttribute) as? NSNumber else {
            throw HelperError("Native log viewer scrollbar has no value")
        }
        let scrollbarPosition = rawScrollbarPosition.doubleValue
        return ["ready": true, "position": scrollbarPosition, "requested_position": position,
                "visible_range_start": range.location,
                "visible_range_end": range.location + range.length,
                "character_count": characterCount]
    }
    switch operation {
    case "focus": try activate()
    case "click":
        guard let target = request["target"] as? String else { throw HelperError("Missing control name") }
        try activate()
        // SwiftUI toolbar containers can inherit the button's label and identifier.
        let buttons = try nodes.filter { try label($0, kAXRoleAttribute) == kAXButtonRole }
        try press(find(buttons, target))
    case "type":
        guard let source = request["source"] as? String else { throw HelperError("Missing source path") }
        try activate()
        try paste(find(nodes, "Connection configuration", editor: true), source: source)
    case "paste":
        guard let source = request["source"] as? String else { throw HelperError("Missing source path") }
        try activate()
        guard let currentWindows = try attribute(root, kAXWindowsAttribute) as? [AXUIElement],
              let currentWindow = currentWindows.first else {
            throw HelperError("Native application has no window for Paste")
        }
        try pasteWithNativeControl(app, window: currentWindow, source: source)
    case "capture":
        guard let path = request["path"] as? String else { throw HelperError("Missing screenshot path") }
        try capture(pid, path: path)
    default: throw HelperError("Unknown helper operation: \(operation)")
    }
    return ["ready": true, "alive": true, "pid": Int(pid), "identity": identity, "labels": []]
}

do {
    let result = try run()
    FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]))
    FileHandle.standardOutput.write(Data([10]))
} catch {
    FileHandle.standardError.write(Data("\(error)\n\(Thread.callStackSymbols.joined(separator: "\n"))\n".utf8))
    exit(1)
}
