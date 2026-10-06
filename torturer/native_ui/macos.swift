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

func isTransientAccessibilityRead(_ error: AccessibilityReadError) -> Bool {
    error.code == .failure || error.code == .cannotComplete || error.code == .invalidUIElement ||
        (error.code == .illegalArgument && error.attribute == kAXRoleAttribute)
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

func identifier(_ element: AXUIElement) throws -> String {
    do {
        return try label(element, kAXIdentifierAttribute)
    } catch let error as AccessibilityReadError
        where error.attribute == kAXIdentifierAttribute && error.code == .illegalArgument {
        // AXIdentifier is optional matching metadata. A provider can reject its
        // value for one node; preserve that node's title/value and keep walking.
        FileHandle.standardError.write(
            Data("Optional AXIdentifier read failed; ignoring identifier: \(error)\n".utf8)
        )
        return ""
    }
}

func visibleCharacterRange(_ element: AXUIElement) throws -> (range: CFRange, characterCount: Int) {
    var lastRange = CFRange(location: -1, length: 0)
    var lastCharacterCount = -1
    for _ in 0..<10 {
        guard let value = try axValue(element, kAXVisibleCharacterRangeAttribute),
              let rawCount = try attribute(element, kAXNumberOfCharactersAttribute) as? NSNumber else {
            Thread.sleep(forTimeInterval: 0.025)
            continue
        }
        var range = CFRange(location: 0, length: 0)
        try require(AXValueGetValue(value, .cfRange, &range), "Could not read native log visible character range")
        let characterCount = rawCount.intValue
        lastRange = range
        lastCharacterCount = characterCount
        if range.location >= 0 && range.length > 0 && range.location <= characterCount &&
            range.length <= characterCount - range.location {
            return (range, characterCount)
        }
        // SwiftUI can refresh the log text between these two AX reads.
        Thread.sleep(forTimeInterval: 0.025)
    }
    throw HelperError(
        "Native log viewer returned an invalid visible character range; " +
            "range=(\(lastRange.location), \(lastRange.length)), " +
            "characters=\(lastCharacterCount) after 10 retries"
    )
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

func retryTransientAccessibilityReads<T>(
    context: String,
    deadline: Date,
    operation: () throws -> T
) throws -> T {
    var lastTransientError: AccessibilityReadError?
    var loggedRetry = false
    repeat {
        do {
            return try operation()
        } catch let error as AccessibilityReadError where isTransientAccessibilityRead(error) {
            lastTransientError = error
            if !loggedRetry {
                FileHandle.standardError.write(Data("\(error) during \(context); retrying\n".utf8))
                loggedRetry = true
            }
        }
        if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
    } while Date() < deadline
    if let lastTransientError { throw lastTransientError }
    throw HelperError("Accessibility operation did not complete before its deadline: \(context)")
}

func names(_ element: AXUIElement) throws -> [String] {
    var values = [String]()
    let role = try label(element, kAXRoleAttribute)
    let axIdentifier = try identifier(element)
    let title: String
    do {
        title = try label(element, kAXTitleAttribute)
    } catch let error as AccessibilityReadError
        where error.attribute == kAXTitleAttribute && error.code == .illegalArgument {
        let rereadRole: String
        let rereadIdentifier: String
        do {
            rereadRole = try label(element, kAXRoleAttribute)
            rereadIdentifier = try identifier(element)
        } catch {
            throw HelperError(
                "AXTitle illegalArgument could not verify AX element stability; " +
                    "initial role=\(role) identifier=\(axIdentifier); reread failed: \(error)"
            )
        }
        try require(
            role == rereadRole && axIdentifier == rereadIdentifier,
            "AXTitle illegalArgument on unstable AX element; " +
                "initial role=\(role) identifier=\(axIdentifier); " +
                "reread role=\(rereadRole) identifier=\(rereadIdentifier)"
        )
        FileHandle.standardError.write(Data((
            "AXTitle illegalArgument on stable AX element; treating title as unsupported: " +
                "role=\(role) identifier=\(axIdentifier) errorCode=\(error.code.rawValue)\n"
        ).utf8))
        title = ""
    }
    if !axIdentifier.isEmpty { values.append(axIdentifier) }
    if !title.isEmpty { values.append(title) }
    // Text fields are verified by type, not copied into every tree response.
    if role != kAXTextAreaRole && role != kAXTextFieldRole {
        let value = try label(element, kAXValueAttribute)
        if !value.isEmpty { values.append(value) }
    }
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
    let identified = try matches.filter { try identifier($0) == name }
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

func pasteWithNativeControl(_ app: NSRunningApplication, window: AXUIElement, source: String) throws -> Int64 {
    let value = try String(contentsOfFile: source, encoding: .utf8)
    let initialNodes = try retryTransientAccessibilityReads(
        context: "Paste initial tree discovery",
        deadline: Date().addingTimeInterval(5)
    ) { try elements(window) }
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
            var lastTransientReadError: AccessibilityReadError?
            repeat {
                do {
                    let availability = try retryTransientAccessibilityReads(
                        context: "Paste \(reason) clipboard availability",
                        deadline: deadline
                    ) { () -> (editor: AXUIElement, button: AXUIElement?)? in
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
                        return nil
                    }
                    lastTransientReadError = nil
                    if let availability { return availability }
                } catch let error as AccessibilityReadError where isTransientAccessibilityRead(error) {
                    lastTransientReadError = error
                }
                if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
            } while Date() < deadline
            if let lastTransientReadError {
                throw HelperError("Native Paste availability could not read a stable Accessibility tree; last error=\(lastTransientReadError)")
            }
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
        let pasteInvokedAtUnixMs = Int64(Date().timeIntervalSince1970 * 1_000)
        try press(pasteButton)
        let deadline = Date().addingTimeInterval(5)
        var observed = ""
        var lastTransientValueError: AccessibilityReadError?
        repeat {
            do {
                observed = try retryTransientAccessibilityReads(
                    context: "reading the pasted URL",
                    deadline: min(deadline, Date().addingTimeInterval(0.25))
                ) { try label(currentEditor, kAXValueAttribute) }
                lastTransientValueError = nil
            } catch let error as AccessibilityReadError where isTransientAccessibilityRead(error) {
                lastTransientValueError = error
            }
            if observed == value { break }
            if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
        } while Date() < deadline
        if observed != value, let lastTransientValueError {
            throw HelperError("Native Paste could not verify the URL after transient Accessibility read errors; last error=\(lastTransientValueError)")
        }
        try require(observed == value, "Native Paste button did not fill the subscription URL")
        board.clearContents()
        if !previous.isEmpty && !board.writeObjects(previous) {
            throw HelperError("Native Paste completed; clipboard restoration failed")
        }
        return pasteInvokedAtUnixMs
    } catch { primary = error }
    board.clearContents()
    if !previous.isEmpty && !board.writeObjects(previous) {
        throw HelperError("\(primary.map { String(describing: $0) } ?? "Paste completed"); clipboard restoration failed")
    }
    if let error = primary { throw error }
    throw HelperError("Native Paste completed without an invocation timestamp")
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
                    "window_count": windows.count, "window_id": try identifier(window),
                    "labels": labels, "enabled_controls": enabled, "link_urls": try linkURLs(nodes)]
        }
        if operation == "resize-window" {
            guard let requestedWidth = request["width"] as? NSNumber,
                  let requestedHeight = request["height"] as? NSNumber else {
                throw HelperError("Missing requested native window dimensions")
            }
            let width = requestedWidth.doubleValue
            let height = requestedHeight.doubleValue
            try require(width >= 560 && height >= 460, "Requested native window is below its supported minimum")
            guard let originalValue = try axValue(window, kAXSizeAttribute) else {
                throw HelperError("Native window size is unavailable before resize")
            }
            var originalSize = CGSize.zero
            try require(AXValueGetValue(originalValue, .cgSize, &originalSize), "Could not read original native window size")
            var requestedSize = CGSize(width: width, height: height)
            guard let requestedValue = AXValueCreate(.cgSize, &requestedSize) else {
                throw HelperError("Could not create requested native window size")
            }
            try activate()
            let setResult = AXUIElementSetAttributeValue(window, kAXSizeAttribute as CFString, requestedValue)
            try require(setResult == .success, "Native window rejected resize: \(setResult.rawValue)")
            var resizedSize = CGSize.zero
            let deadline = Date().addingTimeInterval(3)
            repeat {
                if let value = try axValue(window, kAXSizeAttribute) {
                    try require(AXValueGetValue(value, .cgSize, &resizedSize), "Could not read resized native window size")
                    if abs(resizedSize.width - width) <= 20 && abs(resizedSize.height - height) <= 20 { break }
                }
                Thread.sleep(forTimeInterval: 0.05)
            } while Date() < deadline
            try require(abs(resizedSize.width - width) <= 20 && abs(resizedSize.height - height) <= 20,
                        "Native window did not settle at the requested \(Int(width))x\(Int(height)) size")
            return ["ready": true, "width": originalSize.width, "height": originalSize.height,
                    "resized_width": resizedSize.width, "resized_height": resizedSize.height]
        }
    } catch {
        if operation == "tree", let readError = error as? AccessibilityReadError,
           isTransientAccessibilityRead(readError) {
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
        let sample = try visibleCharacterRange(view)
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
        return ["ready": true, "visible_range_start": sample.range.location,
                "visible_range_end": sample.range.location + sample.range.length,
                "character_count": sample.characterCount, "scrollbar_position": value.doubleValue]
    }
    if operation == "select-log-text" {
        let view = try find(nodes, "Connection logs", editor: true)
        let visible = try visibleCharacterRange(view)
        guard visible.characterCount > 0, visible.range.length > 0 else {
            throw HelperError("Native log viewer has no selectable text")
        }
        var range = visible.range
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
        var sample = try visibleCharacterRange(view)
        var characterCount = sample.characterCount
        try require(characterCount > 0, "Native log viewer has no content to scroll")
        func checkedVisibleRange() throws -> CFRange {
            sample = try visibleCharacterRange(view)
            characterCount = sample.characterCount
            return sample.range
        }
        func distanceFromTarget(_ range: CFRange) -> Int {
            position == "top" ? range.location : characterCount - range.location - range.length
        }
        func isAtTarget(_ range: CFRange) -> Bool {
            distanceFromTarget(range) <= 1
        }
        func rangeDescription(_ range: CFRange) -> String {
            "\(range.location)..<\(range.location + range.length)"
        }
        func scrollbarValueDescription() -> String {
            do {
                guard let value = try attribute(scrollbar, kAXValueAttribute) as? NSNumber else {
                    return "unavailable"
                }
                return value.stringValue
            } catch {
                return "read-error: \(error)"
            }
        }
        var range = sample.range
        try require(range.length < characterCount, "Native log viewer does not overflow its viewport; freeze/resume cannot be tested")
        if !isAtTarget(range) {
            FileHandle.standardError.write(Data((
                "scroll-logs target=\(position) start-range=\(rangeDescription(range)) " +
                    "characters=\(characterCount) scrollbar=\(scrollbarValueDescription())\n"
            ).utf8))
            var reached = false
            var accessibilitySetStatus = "not-settable"
            var settable = DarwinBoolean(false)
            let settableResult = AXUIElementIsAttributeSettable(
                scrollbar, kAXValueAttribute as CFString, &settable
            )
            if settableResult == .success && settable.boolValue {
                var scrollbarMinimum: NSNumber?
                var scrollbarMaximum: NSNumber?
                do {
                    scrollbarMinimum = try attribute(scrollbar, kAXMinValueAttribute) as? NSNumber
                    scrollbarMaximum = try attribute(scrollbar, kAXMaxValueAttribute) as? NSNumber
                } catch {
                    accessibilitySetStatus = "scrollbar-limit-read-error=\(error)"
                }
                if let minimum = scrollbarMinimum, let maximum = scrollbarMaximum {
                    let targetValue = position == "top" ? minimum.doubleValue : maximum.doubleValue
                    let setResult = AXUIElementSetAttributeValue(
                        scrollbar, kAXValueAttribute as CFString, NSNumber(value: targetValue) as CFTypeRef
                    )
                    accessibilitySetStatus = "set-result=\(setResult.rawValue) value=\(targetValue)"
                    if setResult == .success {
                        for _ in 0..<10 {
                            range = try checkedVisibleRange()
                            if isAtTarget(range) {
                                reached = true
                                break
                            }
                            Thread.sleep(forTimeInterval: 0.05)
                        }
                    }
                } else {
                    let targetValue = position == "top" ? 0.0 : 1.0
                    let setResult = AXUIElementSetAttributeValue(
                        scrollbar, kAXValueAttribute as CFString, NSNumber(value: targetValue) as CFTypeRef
                    )
                    let limitStatus = accessibilitySetStatus == "not-settable"
                        ? "scrollbar-min-max-unavailable"
                        : accessibilitySetStatus
                    accessibilitySetStatus =
                        "\(limitStatus) normalized-endpoint-set-result=\(setResult.rawValue) value=\(targetValue)"
                    if setResult == .success {
                        for _ in 0..<10 {
                            range = try checkedVisibleRange()
                            if isAtTarget(range) {
                                reached = true
                                break
                            }
                            Thread.sleep(forTimeInterval: 0.05)
                        }
                    }
                }
            } else {
                accessibilitySetStatus = "settable-check=\(settableResult.rawValue) settable=\(settable.boolValue)"
            }
            FileHandle.standardError.write(Data((
                "scroll-logs accessibility-set \(accessibilitySetStatus) reached=\(reached) " +
                    "range=\(rangeDescription(range)) scrollbar=\(scrollbarValueDescription())\n"
            ).utf8))
            let center = CGPoint(x: origin.x + size.width / 2, y: origin.y + size.height / 2)
            if !reached {
                guard let moved = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                                          mouseCursorPosition: center, mouseButton: .left) else {
                    throw HelperError("Could not position the pointer over native logs")
                }
                moved.post(tap: .cghidEventTap)
            }
            let maximumScrollEvents = 64
            var totalScrollEvents = 0
            for delta in (reached ? [] : [100, -100]) {
                var unchanged = 0
                var directionEventCount = 0
                var madeProgress = false
                var stopReason = "event-cap"
                while totalScrollEvents < maximumScrollEvents {
                    var batchEvents = 0
                    while batchEvents < 16 && totalScrollEvents < maximumScrollEvents {
                        guard let event = CGEvent(scrollWheelEvent2Source: nil, units: .line,
                                                  wheelCount: 1, wheel1: Int32(delta), wheel2: 0, wheel3: 0) else {
                            throw HelperError("Could not create native log scroll event")
                        }
                        event.location = center
                        event.post(tap: .cghidEventTap)
                        directionEventCount += 1
                        totalScrollEvents += 1
                        batchEvents += 1
                        Thread.sleep(forTimeInterval: 0.05)
                        let updated = try checkedVisibleRange()
                        if isAtTarget(updated) {
                            reached = true
                            range = updated
                            stopReason = "target"
                            break
                        }
                        let previousDistance = distanceFromTarget(range)
                        let updatedDistance = distanceFromTarget(updated)
                        if updatedDistance > previousDistance {
                            range = updated
                            stopReason = "moved-away"
                            break
                        }
                        if updatedDistance < previousDistance {
                            madeProgress = true
                            unchanged = 0
                        } else {
                            unchanged += 1
                        }
                        range = updated
                        if unchanged >= 2 {
                            stopReason = "stalled"
                            break
                        }
                    }
                    if reached || stopReason == "moved-away" || stopReason == "stalled" {
                        break
                    }
                    if totalScrollEvents >= maximumScrollEvents {
                        stopReason = "event-cap"
                        break
                    }
                }
                FileHandle.standardError.write(Data((
                    "scroll-logs delta=\(delta) events=\(directionEventCount) total=\(totalScrollEvents) " +
                        "progress=\(madeProgress) stop=\(stopReason) reached=\(reached) " +
                        "range=\(rangeDescription(range)) " +
                        "characters=\(characterCount) scrollbar=\(scrollbarValueDescription())\n"
                ).utf8))
                // Continue a helpful direction across batches. Try its opposite only
                // after observed movement away or repeated samples with no movement.
                if reached || stopReason == "event-cap" { break }
            }
            try require(reached,
                        "Native log viewer did not scroll to \(position); " +
                            "visible range=\(rangeDescription(range)), characters=\(characterCount)")
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
        let pasteInvokedAtUnixMs = try pasteWithNativeControl(app, window: currentWindow, source: source)
        return ["ready": true, "alive": true, "pid": Int(pid), "identity": identity,
                "paste_invoked_at_unix_ms": pasteInvokedAtUnixMs, "labels": []]
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
