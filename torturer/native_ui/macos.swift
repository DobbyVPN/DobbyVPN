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
        let children = try attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? []
        queue.append(contentsOf: children)
    }
    return queue
}

func names(_ element: AXUIElement) throws -> [String] {
    try [kAXIdentifierAttribute, kAXTitleAttribute, kAXValueAttribute]
        .compactMap { name in
            // Text fields are verified by type, not copied into every tree response.
            if name == kAXValueAttribute {
                let role = try label(element, kAXRoleAttribute)
                if role == kAXTextAreaRole || role == kAXTextFieldRole { return nil }
            }
            let value = try label(element, name)
            return value.isEmpty ? nil : value
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
    try require(matches.count == 1, "Expected one visible \(name), found \(matches.count)")
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
        repeat {
            observed = try label(editor, kAXValueAttribute)
            if observed == value { break }
            Thread.sleep(forTimeInterval: 0.05)
        } while Date() < deadline
        try require(observed == value, "Native pasted configuration does not match the source")
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
              let index = windows.firstIndex(where: {
                  ($0[kCGWindowOwnerPID as String] as? Int) == Int(pid) &&
                  ($0[kCGWindowLayer as String] as? Int) == 0
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
        try require(displays.prefix(Int(count)).contains { CGDisplayBounds($0).contains(bounds) }, "Native window is partly offscreen")
        for obstruction in windows[..<index] {
            if (obstruction[kCGWindowLayer as String] as? Int) == Int(CGWindowLevelForKey(.cursorWindow)) {
                continue
            }
            guard (obstruction[kCGWindowAlpha as String] as? Double ?? 1) > 0,
                  let raw = obstruction[kCGWindowBounds as String] as? NSDictionary,
                  let rect = CGRect(dictionaryRepresentation: raw as CFDictionary), rect.intersects(bounds) else { continue }
            throw HelperError("Native screenshot obstructed: \(obstruction)")
        }
        return (number, bounds)
    }
    let (id, bounds) = try windowInfo()
    let task = Process()
    task.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
    task.arguments = ["-x", "-t", "png", "-l", String(id), path]
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
            return ["ready": true, "alive": true, "pid": Int(pid), "identity": identity, "labels": labels]
        }
    } catch {
        if operation == "tree", let readError = error as? AccessibilityReadError,
           readError.code == .failure || readError.code == .cannotComplete {
            FileHandle.standardError.write(Data("\(readError) during tree discovery; retrying\n".utf8))
            return ["ready": false, "alive": true, "pid": Int(pid), "identity": identity]
        }
        throw error
    }
    func activate() throws {
        try require(app.activate(options: [.activateIgnoringOtherApps]), "Could not activate native app")
        let deadline = Date().addingTimeInterval(2)
        while NSWorkspace.shared.frontmostApplication?.processIdentifier != pid && Date() < deadline {
            Thread.sleep(forTimeInterval: 0.05)
        }
        try require(NSWorkspace.shared.frontmostApplication?.processIdentifier == pid, "Native app did not become foreground")
    }
    switch operation {
    case "focus": try activate()
    case "click":
        guard let target = request["target"] as? String else { throw HelperError("Missing control name") }
        try activate()
        try press(find(nodes, target))
    case "type":
        guard let source = request["source"] as? String else { throw HelperError("Missing source path") }
        try activate()
        try paste(find(nodes, "Connection configuration", editor: true), source: source)
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
