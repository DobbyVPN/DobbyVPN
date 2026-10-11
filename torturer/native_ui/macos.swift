import AppKit
import ApplicationServices
import CoreGraphics
import Darwin
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

func elements(_ window: AXUIElement, settingsContent: Bool = false, includeTextAreaChildren: Bool = false) throws -> [AXUIElement] {
    var queue = [window]
    var index = 0
    while index < queue.count {
        try require(queue.count <= 8192, "Accessibility tree exceeds 8192 elements")
        let element = queue[index]
        index += 1
        // Log details change during refresh; controls live outside the text view.
        let role = try label(element, kAXRoleAttribute)
        if (role == kAXTextAreaRole && !includeTextAreaChildren) || (settingsContent && role == "AXToolbar") { continue }
        let children = try attribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? []
        if settingsContent && role == "AXSplitGroup" {
            let splitters = try attribute(element, "AXSplitters") as? [AXUIElement] ?? []
            try require(splitters.count == 1, "Settings content must expose one splitter")
            let navigation = try attribute(splitters[0], "AXPreviousContents") as? [AXUIElement] ?? []
            let content = try attribute(splitters[0], "AXNextContents") as? [AXUIElement] ?? []
            try require(navigation.count == 1 && content.count == 1 &&
                        !CFEqual(navigation[0], content[0]) &&
                        children.contains(where: { CFEqual($0, navigation[0]) }) &&
                        children.contains(where: { CFEqual($0, content[0]) }),
                        "Settings splitter did not identify distinct navigation and content children")
            // Traverse every content child; stale sidebar peers cannot prevent
            // inspecting Display controls or the complete Text Size sheet.
            queue.append(contentsOf: children.filter { !CFEqual($0, navigation[0]) })
        } else {
            queue.append(contentsOf: children)
        }
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
        } catch let readError as AccessibilityReadError where isTransientAccessibilityRead(readError) {
            FileHandle.standardError.write(Data((
                "AXTitle illegalArgument could not verify AX element stability; " +
                    "initial role=\(role) identifier=\(axIdentifier); reread failed: \(readError)\n"
            ).utf8))
            throw readError
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

func bounds(_ element: AXUIElement) throws -> CGRect {
    guard let positionValue = try axValue(element, kAXPositionAttribute),
          let sizeValue = try axValue(element, kAXSizeAttribute) else {
        throw HelperError("Native control bounds are unavailable: \(element)")
    }
    var origin = CGPoint.zero
    var size = CGSize.zero
    try require(AXValueGetValue(positionValue, .cgPoint, &origin), "Could not read native control position")
    try require(AXValueGetValue(sizeValue, .cgSize, &size), "Could not read native control size")
    try require(size.width > 0 && size.height > 0, "Native control has empty bounds")
    return CGRect(origin: origin, size: size)
}

func rectangle(_ bounds: CGRect) -> [String: Double] {
    ["x": Double(bounds.minX), "y": Double(bounds.minY),
     "width": Double(bounds.width), "height": Double(bounds.height)]
}


func contained(_ child: CGRect, by parent: CGRect, tolerance: CGFloat = 1) -> Bool {
    child.minX >= parent.minX - tolerance && child.minY >= parent.minY - tolerance &&
        child.maxX <= parent.maxX + tolerance && child.maxY <= parent.maxY + tolerance
}

func profileScrollParts(_ profileView: AXUIElement) throws -> (area: AXUIElement, bounds: CGRect, scrollbar: AXUIElement?) {
    let identifiedBounds = try bounds(profileView)
    var candidates = [AXUIElement]()
    for element in try elements(profileView) {
        guard try label(element, kAXRoleAttribute) == kAXScrollAreaRole,
              contained(try bounds(element), by: identifiedBounds, tolerance: 2) else { continue }
        candidates.append(element)
    }

    var current: AXUIElement? = profileView
    for _ in 0..<8 {
        guard let element = current else { break }
        if try label(element, kAXRoleAttribute) == kAXScrollAreaRole,
           contained(try bounds(element), by: identifiedBounds, tolerance: 2),
           !candidates.contains(where: { CFEqual($0, element) }) {
            candidates.append(element)
        }
        current = try axElement(element, kAXParentAttribute)
    }
    guard let area = candidates.first else {
        throw HelperError("Profile list identifier is not associated with its native scroll viewport")
    }
    return (area, try bounds(area), try axElement(area, kAXVerticalScrollBarAttribute))
}

func profileScrollRange(
    _ scrollbar: AXUIElement,
    viewport: CGRect,
    required: Bool = false
) throws -> (value: Double, minimum: Double, maximum: Double)? {
    let role = try label(scrollbar, kAXRoleAttribute)
    try require(role == kAXScrollBarRole, "Profile list vertical control is not an AXScrollBar: \(role)")
    let rawValue = try attribute(scrollbar, kAXValueAttribute)
    let rawMinimum = try attribute(scrollbar, kAXMinValueAttribute)
    let rawMaximum = try attribute(scrollbar, kAXMaxValueAttribute)
    let details = "Profile list viewport=\(rectangle(viewport)); AXScrollBar role=\(role) " +
        "AXMinValue=\(String(describing: rawMinimum)) AXMaxValue=\(String(describing: rawMaximum)) " +
        "AXValue=\(String(describing: rawValue))"

    if rawMinimum == nil && rawMaximum == nil {
        guard let rawValue else {
            if required { throw HelperError("Profile list AXScrollBar has no numeric value; \(details)") }
            return nil
        }
        guard CFGetTypeID(rawValue) != CFBooleanGetTypeID(),
              let value = rawValue as? NSNumber,
              value.doubleValue.isFinite, (0.0...1.0).contains(value.doubleValue) else {
            throw HelperError("Profile list AXScrollBar has an invalid normalized value; \(details)")
        }
        // Inference, not an AX contract: NSScroller documents 0 at the top and 1 at the bottom.
        return (value.doubleValue, 0, 1)
    }

    guard let rawValue, CFGetTypeID(rawValue) != CFBooleanGetTypeID(),
          let value = rawValue as? NSNumber,
          let rawMinimum, CFGetTypeID(rawMinimum) != CFBooleanGetTypeID(),
          let minimum = rawMinimum as? NSNumber,
          let rawMaximum, CFGetTypeID(rawMaximum) != CFBooleanGetTypeID(),
          let maximum = rawMaximum as? NSNumber else {
        throw HelperError("Profile list AXScrollBar exposes a partial or non-numeric range; \(details)")
    }
    let current = value.doubleValue
    let lower = minimum.doubleValue
    let upper = maximum.doubleValue
    guard current.isFinite, lower.isFinite, upper.isFinite, upper > lower,
          current >= lower, current <= upper else {
        throw HelperError("Profile list AXScrollBar exposes an invalid numeric range; \(details)")
    }
    return (current, lower, upper)
}

func profileScrollPercent(_ profileView: AXUIElement) throws -> Double? {
    let parts = try profileScrollParts(profileView)
    guard let scrollbar = parts.scrollbar,
          let range = try profileScrollRange(scrollbar, viewport: parts.bounds) else { return nil }
    return min(100, max(0, (range.value - range.minimum) / (range.maximum - range.minimum) * 100))
}

func profileControlParts(_ identifier: String) -> (digest: String, index: Int, role: String)? {
    guard identifier.hasPrefix("profile.") else { return nil }
    let values = identifier.dropFirst("profile.".count).split(separator: ".", omittingEmptySubsequences: false)
    guard values.count == 3, !values[0].isEmpty,
          let index = Int(values[1]), index >= 0,
          ["description", "protocol", "action"].contains(String(values[2])) else { return nil }
    return (String(values[0]), index, String(values[2]))
}

func profileRowDetails(_ nodes: [AXUIElement]) throws -> [[String: Any]] {

    var descriptions = [Int: String]()
    var descriptionOrder = [Int]()
    var protocols = [Int: String]()
    var actions = [Int: String]()
    var inventoryDigests = Set<String>()
    for element in nodes {
        let id = try identifier(element)
        guard let parts = profileControlParts(id) else { continue }
        let index = parts.index
        inventoryDigests.insert(parts.digest)
        if parts.role == "description" {
            let role = try label(element, kAXRoleAttribute)
            try require(role == kAXStaticTextRole,
                        "Profile \(index) description identifier is not native text")
            guard let description = try names(element).first(where: { $0 != id }), !description.isEmpty else {
                throw HelperError("Profile \(index) has no native description text")
            }
            try require(descriptions[index] == nil, "Profile \(index) has duplicate native description labels")
            descriptions[index] = description
            descriptionOrder.append(index)
        } else if parts.role == "protocol" {
            let role = try label(element, kAXRoleAttribute)
            try require(role == kAXStaticTextRole,
                        "Profile \(index) protocol identifier is not native text")
            let protocolNames = try names(element).filter { $0 != id }
            guard let protocolLabel = protocolNames.first(where: { $0.contains(" · ") }) ?? protocolNames.first else {
                throw HelperError("Profile \(index) has no native protocol text")
            }
            let protocolName: String
            if let separator = protocolLabel.range(of: " · ", options: .backwards) {
                protocolName = String(protocolLabel[separator.upperBound...])
            } else {
                protocolName = protocolLabel
            }
            try require(protocols[index] == nil, "Profile \(index) has duplicate native protocol labels")
            protocols[index] = protocolName
        } else if parts.role == "action" {
            let role = try label(element, kAXRoleAttribute)
            try require(role == kAXButtonRole,
                        "Profile \(index) action identifier is not a native button")
            let title = try names(element).first { $0 != id } ?? ""
            try require(!title.isEmpty, "Profile \(index) has no native action title")
            if let existing = actions[index] {
                try require(existing == title, "Profile \(index) has conflicting native action titles")
                throw HelperError("Profile \(index) has duplicate native action controls")
            }
            actions[index] = title
        }
    }

    let descriptionIndices = Set(descriptions.keys)
    let protocolIndices = Set(protocols.keys)
    let actionIndices = Set(actions.keys)
    try require(descriptionIndices == protocolIndices,
                "Rendered profile descriptions and protocols did not have matching identifiers")
    try require(descriptionIndices == actionIndices,
                "Rendered profile descriptions and actions did not have matching identifiers")
    try require(inventoryDigests.count <= 1,
                "Rendered profile controls refer to different accepted inventory digests")
    let profileDigest = inventoryDigests.first ?? ""
    return try descriptionOrder.map { index in
        guard let name = descriptions[index], let protocolName = protocols[index], let action = actions[index] else {
            throw HelperError("Profile \(index) is missing rendered description, protocol, or action metadata")
        }
        return [
            "index": index + 1,
            "profile_index": index,
            "profile_digest": profileDigest,
            "name": name,
            "protocol": protocolName,
            "action": action,
            "description_automation_id": "profile.\(profileDigest).\(index).description",
            "protocol_automation_id": "profile.\(profileDigest).\(index).protocol",
            "action_automation_id": "profile.\(profileDigest).\(index).action",
        ]
    }
}

func profileListLayout(_ nodes: [AXUIElement]) throws -> [String: Any] {
    guard let window = nodes.first,
          try label(window, kAXRoleAttribute) == kAXWindowRole else {
        throw HelperError("Native profile layout has no window root")
    }
    let windowBounds = try bounds(window)
    let controlsBounds = try bounds(find(nodes, "Connection controls"))
    let profileView = try find(nodes, "Profile list viewport")
    let profileParts = try profileScrollParts(profileView)
    let profileBounds = profileParts.bounds
    let actionBounds = try bounds(find(nodes, "VPN connection action"))
    let logText = try find(nodes, "Connection logs", editor: true)
    var logAncestor: AXUIElement? = logText
    var logScrollArea: AXUIElement?
    for _ in 0..<8 {
        guard let element = logAncestor else { break }
        if try label(element, kAXRoleAttribute) == kAXScrollAreaRole {
            logScrollArea = element
            break
        }
        logAncestor = try axElement(element, kAXParentAttribute)
    }
    guard let logViewport = logScrollArea else {
        throw HelperError("Native log text has no containing scroll viewport")
    }
    let logsBounds = try bounds(logViewport)
    let visibleActions = try nodes.compactMap { element -> (Int, String)? in
        let id = try identifier(element)
        guard let parts = profileControlParts(id), parts.role == "action",
              try label(element, kAXRoleAttribute) == kAXButtonRole else {
            return nil
        }
        return contained(try bounds(element), by: profileBounds) ? (parts.index, id) : nil
    }.sorted { $0.0 < $1.0 }.map { $0.1 }
    let scrollPosition: Any
    if let percent = try profileScrollPercent(profileView) {
        scrollPosition = percent
    } else {
        scrollPosition = NSNull()
    }
    return [
        "ready": true,
        "window": rectangle(windowBounds),
        "controls": rectangle(controlsBounds),
        "profile_viewport": rectangle(profileBounds),
        "connection_action": rectangle(actionBounds),
        "logs": rectangle(logsBounds),
        "scroll_position": scrollPosition,
        "profile_rows": try profileRowDetails(nodes),
        "visible_profile_actions": visibleActions,
    ]
}

func scrollProfileList(_ nodes: [AXUIElement], position: String, targetActionID: String?) throws -> [String: Any] {
    let targetPercent: Double
    if position == "top" { targetPercent = 0 }
    else if position == "bottom" { targetPercent = 100 }
    else if let requested = Double(position), requested.isFinite, requested >= 0, requested <= 100 {
        targetPercent = requested
    } else {
        throw HelperError("Profile list position must be top, bottom, or a finite percentage from 0 to 100")
    }
    guard let window = nodes.first else { throw HelperError("Profile list window is unavailable") }
    let profileView = try find(nodes, "Profile list viewport")
    let profileParts = try profileScrollParts(profileView)
    guard let scrollbar = profileParts.scrollbar else {
        throw HelperError("Profile list viewport has no native vertical scrollbar")
    }
    let targetID = targetActionID
    func currentLayout() throws -> [String: Any] {
        try retryTransientAccessibilityReads(
            context: "reading native profile-list geometry",
            deadline: Date().addingTimeInterval(0.5)
        ) {
            try profileListLayout(elements(window))
        }
    }
    func targetVisible(_ layout: [String: Any]) -> Bool {
        if let targetID {
            return (layout["visible_profile_actions"] as? [String])?.contains(targetID) == true
        }
        guard let actual = layout["scroll_position"] as? Double else { return false }
        return abs(actual - targetPercent) <= 1
    }

    var initialLayout: [String: Any]?
    var initialLayoutError: Error?
    do {
        initialLayout = try currentLayout()
    } catch {
        initialLayoutError = error
    }

    guard let range = try profileScrollRange(scrollbar, viewport: profileParts.bounds, required: true) else {
        throw HelperError("Profile list AXScrollBar has no measurable position")
    }
    guard var layout = initialLayout else {
        if let initialLayoutError { throw initialLayoutError }
        throw HelperError("Native profile-list layout was unavailable before scrolling")
    }
    if !targetVisible(layout) {
        var settable = DarwinBoolean(false)
        if AXUIElementIsAttributeSettable(scrollbar, kAXValueAttribute as CFString, &settable) == .success,
           settable.boolValue {
            let targetValue = range.minimum + (range.maximum - range.minimum) * targetPercent / 100
            if AXUIElementSetAttributeValue(
                scrollbar, kAXValueAttribute as CFString, NSNumber(value: targetValue) as CFTypeRef
            ) == .success {
                for _ in 0..<12 {
                    layout = try currentLayout()
                    if targetVisible(layout) { return layout }
                    Thread.sleep(forTimeInterval: 0.05)
                }
            }
        }

        let profileBounds = profileParts.bounds
        let center = CGPoint(x: profileBounds.midX, y: profileBounds.midY)
        guard let moved = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                                  mouseCursorPosition: center, mouseButton: .left) else {
            throw HelperError("Could not position the pointer over the profile list")
        }
        moved.post(tap: .cghidEventTap)
        var reached = false
        for delta in [100, -100] {
            var unchanged = 0
            var priorDistance: Double
            if position == "top" {
                priorDistance = layout["scroll_position"] as? Double ?? 0
            } else if position == "bottom" {
                priorDistance = 100 - (layout["scroll_position"] as? Double ?? 0)
            } else {
                priorDistance = abs((layout["scroll_position"] as? Double ?? targetPercent) - targetPercent)
            }
            for _ in 0..<24 {
                guard let event = CGEvent(scrollWheelEvent2Source: nil, units: .line,
                                          wheelCount: 1, wheel1: Int32(delta), wheel2: 0, wheel3: 0) else {
                    throw HelperError("Could not create native profile-list scroll event")
                }
                event.location = center
                event.post(tap: .cghidEventTap)
                Thread.sleep(forTimeInterval: 0.05)
                layout = try currentLayout()
                if targetVisible(layout) {
                    reached = true
                    break
                }
                let scrollPosition = layout["scroll_position"] as? Double ?? targetPercent
                let distance: Double
                if position == "top" { distance = scrollPosition }
                else if position == "bottom" { distance = 100 - scrollPosition }
                else { distance = abs(scrollPosition - targetPercent) }
                if distance < priorDistance {
                    unchanged = 0
                } else {
                    unchanged += 1
                }
                priorDistance = distance
                if unchanged >= 3 { break }
            }
            if reached { break }
        }
        try require(reached, "Profile list did not scroll to \(position)")
    }
    layout = try currentLayout()
    try require(targetVisible(layout), "Profile list did not settle at \(position)")
    return layout
}

func press(_ element: AXUIElement) throws {
    let code = AXUIElementPerformAction(element, kAXPressAction as CFString)
    try require(code == .success, "Native AX press failed: \(code.rawValue)")
}

func verifiedConnectionAction(_ nodes: [AXUIElement], identifier expectedIdentifier: String) throws -> AXUIElement {
    let element = try find(nodes, expectedIdentifier)
    let actualIdentifier = try identifier(element)
    try require(actualIdentifier == expectedIdentifier,
                "Native connection action did not expose the expected identifier: \(expectedIdentifier)")
    guard let enabled = try attribute(element, kAXEnabledAttribute) as? Bool else {
        throw HelperError("Could not confirm native connection action is enabled: \(expectedIdentifier)")
    }
    try require(enabled, "Native connection action is disabled: \(expectedIdentifier)")

    var rawActionNames: CFArray?
    let status = AXUIElementCopyActionNames(element, &rawActionNames)
    guard status == .success else {
        throw AccessibilityReadError(attribute: "AXActionNames", code: status)
    }
    let actionNames = rawActionNames as? [String] ?? []
    try require(actionNames.contains(kAXPressAction as String),
                "Native connection action does not advertise AXPress: \(expectedIdentifier); actions=\(actionNames)")
    return element
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
    try pasteValue(editor, value: value)
}

func pasteValue(_ editor: AXUIElement, value: String) throws {
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

func pasteValueWithNativeControl(
    _ app: NSRunningApplication, window: AXUIElement, value: String, expectedSource: String? = nil,
    onInvocation: (() -> Void)? = nil
) throws -> Int64 {
    let expectedValue = expectedSource ?? value
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
        onInvocation?()
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
            if observed == expectedValue { break }
            if Date() < deadline { Thread.sleep(forTimeInterval: 0.05) }
        } while Date() < deadline
        if observed != expectedValue, let lastTransientValueError {
            throw HelperError("Native Paste could not verify the URL after transient Accessibility read errors; last error=\(lastTransientValueError)")
        }
        try require(
            observed == expectedValue,
            expectedSource == nil
                ? "Native Paste button did not fill the subscription URL"
                : "Native Paste did not preserve the expected subscription field value"
        )
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

func inspectMacOSTextSize(
    candidateBundle: MacOSCandidateApp, candidateApp: NSRunningApplication, candidatePID: pid_t,
    candidateIdentity: String, reactivateCandidate: () throws -> Void
) throws -> [String: Any] {
    var settingsApp: NSRunningApplication?
    var settingsOpenedByHelper = false
    var baselineSheets = [AXUIElement]()
    var buttonPressed = false
    var cleanupErrors = [String]()
    // Reserve the caller's remaining budget for owned Settings cleanup.
    let inspectionDeadline = Date().addingTimeInterval(20)

    func textSizeSheet(_ nodes: [AXUIElement]) throws -> AXUIElement? {
        let sheets = try nodes.filter { node in
            guard try label(node, kAXRoleAttribute) == "AXSheet" else { return false }
            return try elements(node).contains {
                try label($0, kAXRoleAttribute) == "AXHeading" && names($0).contains("Text Size")
            }
        }
        try require(sheets.count <= 1, "Found \(sheets.count) Text Size sheets")
        return sheets.first
    }

    func appName(_ popup: AXUIElement, in sheetNodes: [AXUIElement]) throws -> String? {
        guard let labelElement = try axElement(popup, kAXTitleUIElementAttribute),
              sheetNodes.contains(where: { CFEqual($0, labelElement) }),
              try label(labelElement, kAXRoleAttribute) == kAXStaticTextRole,
              let rawName = try attribute(labelElement, kAXValueAttribute),
              CFGetTypeID(rawName) == CFStringGetTypeID(),
              let value = rawName as? String else {
            return nil
        }
        let displayName = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return displayName.isEmpty ? nil : displayName
    }

    func readSettingsTree<T>(
        context: String, deadline: Date,
        read: ([AXUIElement]) throws -> T
    ) throws -> (ownerPID: pid_t, value: T)? {
        try require(Date() < deadline, "\(context): read deadline expired")
        guard let settings = settingsApp else { throw HelperError("System Settings process is unavailable") }
        do {
            let snapshot: (ownerPID: pid_t, value: T)? = try retryTransientAccessibilityReads(
                context: context, deadline: deadline
            ) {
                let root = AXUIElementCreateApplication(settings.processIdentifier)
                AXUIElementSetMessagingTimeout(root, 1)
                var window = try axElement(root, kAXFocusedWindowAttribute)
                if window == nil {
                    let windows = try attribute(root, kAXWindowsAttribute) as? [AXUIElement] ?? []
                    window = try windows.first { (try attribute($0, kAXMainAttribute)) as? Bool == true }
                    if window == nil && windows.count == 1 { window = windows[0] }
                }
                guard let window else { return nil }
                var ownerPID: pid_t = 0
                let ownerStatus = AXUIElementGetPid(window, &ownerPID)
                guard ownerStatus == .success else {
                    throw AccessibilityReadError(attribute: "AXWindowOwnerPID", code: ownerStatus)
                }
                try require(ownerPID == settings.processIdentifier,
                            "System Settings AX window belongs to PID \(ownerPID), expected \(settings.processIdentifier)")
                // Inspect the declared content region, including complete attached sheets.
                // Unreadable controls here still fail the observation.
                let nodes = try elements(window, settingsContent: true)
                return (ownerPID, try read(nodes))
            }
            return snapshot
        } catch {
            throw HelperError("\(context): \(error)")
        }
    }

    let outcome: Result<[String: Any], Error> = {
        defer {
            if buttonPressed, settingsApp != nil {
                do {
                    let deadline = Date().addingTimeInterval(3)
                    var cleanupRead: (ownerPID: pid_t, value: AXUIElement?)?
                    repeat {
                        cleanupRead = try readSettingsTree(
                            context: "Text Size cleanup sheet read", deadline: deadline
                        ) { nodes -> AXUIElement? in
                            guard let sheet = try textSizeSheet(nodes),
                                  !baselineSheets.contains(where: { CFEqual($0, sheet) }) else { return nil }
                            return try find(elements(sheet), "Done")
                        }
                        if cleanupRead != nil { break }
                        Thread.sleep(forTimeInterval: 0.05)
                    } while Date() < deadline
                    guard let cleanupRead else {
                        throw HelperError("System Settings AX window unavailable during Text Size cleanup")
                    }
                    if let done = cleanupRead.value { try press(done) }
                } catch { cleanupErrors.append("Close helper-opened Text Size sheet: \(error)") }
            }
            if settingsOpenedByHelper, let settings = settingsApp, !settings.isTerminated {
                do {
                    try require(settings.terminate(), "System Settings quit request failed")
                    let deadline = Date().addingTimeInterval(3)
                    while !settings.isTerminated && Date() < deadline {
                        _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
                    }
                    try require(settings.isTerminated, "Helper-owned System Settings did not terminate")
                } catch { cleanupErrors.append("Close helper-owned System Settings: \(error)") }
            }
            do { try reactivateCandidate() }
            catch { cleanupErrors.append("Reactivate installed app: \(error)") }
        }
        do {
            let bundleURL = candidateBundle.bundleURL
            guard let info = Bundle(url: bundleURL)?.infoDictionary,
                  let bundleID = info["CFBundleIdentifier"] as? String else {
                throw HelperError("Installed app bundle identity is unavailable: \(bundleURL.path)")
            }
            let bundleName = (info["CFBundleDisplayName"] as? String) ??
                (info["CFBundleName"] as? String) ?? bundleURL.deletingPathExtension().lastPathComponent
            let candidateNames = Set([bundleName, info["CFBundleName"] as? String ?? "",
                                       candidateApp.localizedName ?? ""].filter { !$0.isEmpty }.map { $0.lowercased() })
            let version = info["CFBundleShortVersionString"] as? String ?? ""
            try require(AXIsProcessTrusted(), "Accessibility permission unavailable")

            let settingsID = "com.apple.systempreferences"
            let running = NSWorkspace.shared.runningApplications.filter {
                $0.bundleIdentifier == settingsID && !$0.isTerminated
            }
            try require(running.count <= 1, "Found \(running.count) System Settings app processes")
            if let existing = running.first {
                settingsApp = existing
            } else {
                guard let appURL = NSWorkspace.shared.urlForApplication(withBundleIdentifier: settingsID) else {
                    throw HelperError("System Settings app bundle was not found")
                }
                let configuration = NSWorkspace.OpenConfiguration()
                configuration.activates = true
                var launched: NSRunningApplication?
                var launchError: Error?
                var launchFinished = false
                NSWorkspace.shared.openApplication(at: appURL, configuration: configuration) { app, error in
                    launched = app
                    launchError = error
                    launchFinished = true
                }
                let deadline = min(Date().addingTimeInterval(8), inspectionDeadline)
                while !launchFinished && Date() < deadline {
                    _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
                }
                try require(launchFinished, "System Settings launch timed out")
                if let launchError { throw HelperError("System Settings launch failed: \(launchError)") }
                guard let launched, launched.bundleIdentifier == settingsID else {
                    throw HelperError("System Settings launch returned no matching process")
                }
                settingsApp = launched
                settingsOpenedByHelper = true
            }
            guard let settings = settingsApp else { throw HelperError("System Settings process is unavailable") }
            try require(settings.activate(options: [.activateAllWindows]), "System Settings activation failed")
            let activationDeadline = min(Date().addingTimeInterval(3), inspectionDeadline)
            while NSWorkspace.shared.frontmostApplication?.processIdentifier != settings.processIdentifier &&
                    Date() < activationDeadline {
                _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
            }
            try require(NSWorkspace.shared.frontmostApplication?.processIdentifier == settings.processIdentifier,
                        "System Settings did not become foreground")

            let windowDeadline = min(Date().addingTimeInterval(8), inspectionDeadline)
            var baselineRead = false
            repeat {
                if let snapshot = try readSettingsTree(
                    context: "Text Size baseline sheet read", deadline: windowDeadline,
                    read: { nodes -> AXUIElement? in try textSizeSheet(nodes) }
                ) {
                    baselineSheets = snapshot.value.map { [$0] } ?? []
                    baselineRead = true
                }
                if !baselineRead { Thread.sleep(forTimeInterval: 0.05) }
            } while !baselineRead && Date() < windowDeadline
            try require(baselineRead, "System Settings AX window unavailable during Text Size baseline read")

            guard let displayURL = URL(string: "x-apple.systempreferences:com.apple.Accessibility-Settings.extension?Display") else {
                throw HelperError("Could not construct the Accessibility Display URL")
            }
            let urlConfiguration = NSWorkspace.OpenConfiguration()
            urlConfiguration.activates = true
            var urlError: Error?
            var urlFinished = false
            NSWorkspace.shared.open(displayURL, configuration: urlConfiguration) { _, error in
                urlError = error
                urlFinished = true
            }
            let urlDeadline = min(Date().addingTimeInterval(6), inspectionDeadline)
            while !urlFinished && Date() < urlDeadline {
                _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.05))
            }
            try require(urlFinished, "Accessibility Display URL open timed out")
            if let urlError { throw HelperError("Accessibility Display URL open failed: \(urlError)") }

            let displayDeadline = min(Date().addingTimeInterval(10), inspectionDeadline)
            let displaySearchStarted = ProcessInfo.processInfo.systemUptime
            var displaySearchAttempts = 0
            var displayTreeObserved = false
            var displayTreeRows: [[String: Any]]?
            var displayTreeOwnerPID: pid_t?
            var button: AXUIElement?
            var buttonDescription = ""
            var buttonEnabled = false
            var textSizeSheetVisible = false
            repeat {
                displaySearchAttempts += 1
                let snapshot = try readSettingsTree(
                    context: "Accessibility Display Text Size control read", deadline: displayDeadline,
                    read: { nodes -> (
                        sheetVisible: Bool, button: AXUIElement?, description: String,
                        enabled: Bool, rows: [[String: Any]]
                    ) in
                        if try textSizeSheet(nodes) != nil { return (true, nil, "", false, []) }
                        var rows = [[String: Any]]()
                        rows.reserveCapacity(nodes.count)
                        var buttons = [(element: AXUIElement, description: String, rowIndex: Int)]()
                        for (index, node) in nodes.enumerated() {
                            let role = try label(node, kAXRoleAttribute)
                            var row: [String: Any] = ["index": index, "role": role]
                            if role == kAXButtonRole {
                                let description = try label(node, kAXDescriptionAttribute)
                                row["description"] = description
                                let normalized = description.lowercased()
                                if normalized.hasPrefix("text size") &&
                                    normalized.contains("preferred reading size") &&
                                    normalized.contains("supported apps") {
                                    buttons.append((node, description, index))
                                }
                            }
                            rows.append(row)
                        }
                        try require(buttons.count <= 1, "Found \(buttons.count) Text size settings buttons")
                        guard let found = buttons.first else { return (false, nil, "", false, rows) }
                        let rawEnabled = try attribute(found.element, kAXEnabledAttribute) as? Bool
                        if let rawEnabled {
                            rows[found.rowIndex]["enabled"] = rawEnabled
                        } else {
                            rows[found.rowIndex]["enabled"] = NSNull()
                        }
                        return (false, found.element, found.description, rawEnabled == true, rows)
                    }
                )
                if let snapshot {
                    displayTreeObserved = true
                    displayTreeOwnerPID = snapshot.ownerPID
                    textSizeSheetVisible = snapshot.value.sheetVisible
                    displayTreeRows = snapshot.value.rows
                    button = snapshot.value.button
                    buttonDescription = snapshot.value.description
                    buttonEnabled = snapshot.value.enabled
                    if textSizeSheetVisible || button != nil { break }
                } else {
                    displayTreeObserved = true
                    displayTreeRows = nil
                    displayTreeOwnerPID = nil
                }
                if Date() < displayDeadline { Thread.sleep(forTimeInterval: 0.1) }
            } while Date() < displayDeadline

            let displaySearchElapsedMs = (ProcessInfo.processInfo.systemUptime - displaySearchStarted) * 1000
            let inspectionElapsedBeforeCollectionMs = (20 - inspectionDeadline.timeIntervalSinceNow) * 1000
            if !textSizeSheetVisible {
                guard let button, buttonEnabled else {
                    let settings = settingsApp
                    let frontmost = NSWorkspace.shared.frontmostApplication
                    let windowList = CGWindowListCopyWindowInfo(
                        [.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID
                    ) as? [[String: Any]]
                    let settingsWindowMetadata: Any
                    let settingsWindowMetadataError: String?
                    if let windowList, let settings {
                        settingsWindowMetadata = windowList.filter {
                            ($0[kCGWindowOwnerPID as String] as? Int) == Int(settings.processIdentifier)
                        }
                        settingsWindowMetadataError = nil
                    } else if windowList == nil {
                        settingsWindowMetadata = NSNull()
                        settingsWindowMetadataError = "CGWindowListCopyWindowInfo returned no window metadata"
                    } else {
                        settingsWindowMetadata = NSNull()
                        settingsWindowMetadataError = "System Settings process identifier unavailable for filtering"
                    }
                    let diagnostics: [String: Any] = [
                        "scope": "roles and button descriptions observed by original search",
                        "settings_pid": settings.map { Int($0.processIdentifier) } as Any? ?? NSNull(),
                        "settings_opened_by_helper": settingsOpenedByHelper,
                        "settings_window_owner_pid": displayTreeOwnerPID.map { Int($0) } as Any? ?? NSNull(),
                        "settings_window_metadata": settingsWindowMetadata,
                        "settings_window_metadata_error": settingsWindowMetadataError as Any? ?? NSNull(),
                        "frontmost_pid": frontmost.map { Int($0.processIdentifier) } as Any? ?? NSNull(),
                        "last_tree_observed": displayTreeObserved,
                        "last_tree_available": displayTreeRows != nil,
                        "last_tree_node_count": displayTreeRows?.count ?? 0,
                        "last_tree_rows": displayTreeRows as Any? ?? NSNull(),
                        "display_search_attempts": displaySearchAttempts,
                        "display_search_elapsed_ms": displaySearchElapsedMs,
                        "inspection_elapsed_before_collection_ms": inspectionElapsedBeforeCollectionMs,
                        "text_size_button_found": button != nil,
                        "text_size_button_enabled": button != nil ? buttonEnabled as Any : NSNull(),
                        "text_size_button_description": button != nil ? buttonDescription as Any : NSNull(),
                    ]
                    do {
                        let data = try JSONSerialization.data(withJSONObject: diagnostics, options: [.sortedKeys])
                        var line = Data("[macos-text-size-diagnostics] ".utf8)
                        line.append(data)
                        line.append(10)
                        FileHandle.standardError.write(line)
                    } catch {
                        FileHandle.standardError.write(
                            Data("[macos-text-size-diagnostics] collection failed; original failure preserved: \(error)\n".utf8)
                        )
                    }
                    throw HelperError("Display settings did not expose an enabled Text size button")
                }
                buttonPressed = true
                do { try press(button) }
                catch { throw HelperError("Press Text Size button: \(error)") }
            }

            var inventory: [String: Any]?
            let sheetDeadline = min(Date().addingTimeInterval(10), inspectionDeadline)
            repeat {
                if let snapshot = try readSettingsTree(
                    context: "Text Size complete app-row inventory", deadline: sheetDeadline,
                    read: { nodes -> (Bool, [String: Any]?) in
                        guard let sheet = try textSizeSheet(nodes) else { return (false, nil) }
                        let sheetNodes = try elements(sheet)
                        let popups = try sheetNodes.filter { try label($0, kAXRoleAttribute) == "AXPopUpButton" }
                        var rows = [[String: Any]]()
                        var unmapped = 0
                        var targetMatches = 0
                        var targetEnabled = false
                        for popup in popups {
                            guard let appName = try appName(popup, in: sheetNodes),
                                  let size = try attribute(popup, kAXValueAttribute) as? String,
                                  let enabled = try attribute(popup, kAXEnabledAttribute) as? Bool else {
                                unmapped += 1
                                continue
                            }
                            rows.append(["app": appName, "size": size, "enabled": enabled])
                            if candidateNames.contains(appName.lowercased()) {
                                targetMatches += 1
                                targetEnabled = enabled
                            }
                        }
                        let rowNames = rows.compactMap { $0["app"] as? String }.map { $0.lowercased() }
                        let complete = !popups.isEmpty && rows.count == popups.count &&
                            Set(rowNames).count == rowNames.count && targetMatches <= 1
                        let eligible: Any
                        if complete { eligible = targetMatches == 1 && targetEnabled }
                        else { eligible = NSNull() }
                        return (true, [
                            "ready": true, "available": true, "rows_complete": complete, "eligible": eligible,
                            "candidate_bundle_identifier": bundleID, "candidate_name": bundleName,
                            "candidate_version": version, "candidate_pid": Int(candidatePID),
                            "candidate_identity": candidateIdentity, "settings_pid": Int(settings.processIdentifier),
                            "settings_opened_by_helper": settingsOpenedByHelper,
                            "os_version": ProcessInfo.processInfo.operatingSystemVersionString,
                            "text_size_button_description": buttonDescription, "app_rows": rows,
                            "unmapped_control_count": unmapped,
                        ])
                    }
                ) {
                    if snapshot.value.0, var details = snapshot.value.1 {
                        details["settings_window_owner_pid"] = Int(snapshot.ownerPID)
                        inventory = details
                        break
                    }
                }
                if Date() < sheetDeadline { Thread.sleep(forTimeInterval: 0.05) }
            } while Date() < sheetDeadline
            guard let inventory else { throw HelperError("Text Size sheet did not expose its app-row inventory") }
            return .success(inventory)
        } catch { return .failure(error) }
    }()
    if case let .failure(error) = outcome {
        if cleanupErrors.isEmpty { throw error }
        throw HelperError("\(error); cleanup errors: \(cleanupErrors.joined(separator: "; "))")
    }
    if !cleanupErrors.isEmpty { throw HelperError("Text Size cleanup failed: \(cleanupErrors.joined(separator: "; "))") }
    if case let .success(result) = outcome { return result }
    throw HelperError("Text Size inspection returned no result")
}

struct MacOSProcessIdentity {
    let executablePath: String
    let started: String
    let startedUTC: String
}

struct MacOSProcessMissing: Error {}

struct MacOSCandidateApp {
    let bundleURL: URL
    let executablePath: String

    init(path: String) throws {
        let bundleURL = URL(fileURLWithPath: path).standardizedFileURL.resolvingSymlinksInPath()
        var isDirectory: ObjCBool = false
        let exists = FileManager.default.fileExists(atPath: bundleURL.path, isDirectory: &isDirectory)
        try require(bundleURL.pathExtension.lowercased() == "app" && exists && isDirectory.boolValue,
                    "Candidate application path must be an existing .app bundle: \(path)")
        guard let bundle = Bundle(url: bundleURL), let executableURL = bundle.executableURL else {
            throw HelperError("Candidate .app bundle does not identify an executable: \(bundleURL.path)")
        }
        let executablePath = executableURL.resolvingSymlinksInPath().path
        try require(FileManager.default.isExecutableFile(atPath: executablePath),
                    "Candidate .app executable is missing or not executable: \(executablePath)")
        self.bundleURL = bundleURL
        self.executablePath = executablePath
    }
}

func macOSProcessIdentity(_ pid: pid_t) throws -> MacOSProcessIdentity {
    var info = proc_bsdinfo()
    let expectedSize = Int32(MemoryLayout<proc_bsdinfo>.size)
    let receivedSize = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, expectedSize)
    guard receivedSize == expectedSize else {
        if kill(pid, 0) != 0 && errno == ESRCH { throw MacOSProcessMissing() }
        throw HelperError("Could not read OS process start identity for pid \(pid): bytes=\(receivedSize), errno=\(errno)")
    }
    try require(Int(info.pbi_pid) == Int(pid) && info.pbi_start_tvsec > 0,
                "OS process identity was incomplete for pid \(pid)")

    var pathBuffer = [CChar](repeating: 0, count: Int(MAXPATHLEN) * 4)
    let pathBytes = pathBuffer.withUnsafeMutableBufferPointer { buffer in
        proc_pidpath(pid, buffer.baseAddress, UInt32(buffer.count))
    }
    guard pathBytes > 0 else {
        if kill(pid, 0) != 0 && errno == ESRCH { throw MacOSProcessMissing() }
        throw HelperError("Could not read OS executable path for pid \(pid): bytes=\(pathBytes), errno=\(errno)")
    }
    let executablePath = URL(fileURLWithPath: String(cString: pathBuffer)).resolvingSymlinksInPath().path
    let seconds = Double(info.pbi_start_tvsec)
    let microseconds = Double(info.pbi_start_tvusec)
    let started = "\(info.pbi_start_tvsec):\(info.pbi_start_tvusec)"
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    let startedUTC = formatter.string(from: Date(timeIntervalSince1970: seconds + microseconds / 1_000_000))
    return MacOSProcessIdentity(executablePath: executablePath, started: started, startedUTC: startedUTC)
}

struct MacOSLogsCaseDeadline {
    let start: TimeInterval
    let duration: TimeInterval

    init(_ seconds: TimeInterval) {
        start = ProcessInfo.processInfo.systemUptime
        duration = seconds
    }

    var elapsed: TimeInterval { ProcessInfo.processInfo.systemUptime - start }
    var remaining: TimeInterval { max(0, duration - elapsed) }

    func check(_ stage: String) throws {
        try require(remaining > 0,
                    "macOS \(stage) exceeded the single \(String(format: "%.3f", duration)) second native-case deadline")
    }
}

final class MacOSOwnedLogsApp {
    let application: NSRunningApplication
    let candidate: MacOSCandidateApp
    let pid: pid_t
    var executablePath: String { candidate.executablePath }
    private(set) var identity: String?
    var result: [String: Any]

    init(application: NSRunningApplication, candidate: MacOSCandidateApp) {
        self.application = application
        self.candidate = candidate
        pid = application.processIdentifier
        identity = nil
        result = [
            "pid": Int(application.processIdentifier),
            "identity": NSNull(),
            "process_start_utc": NSNull(),
            "executable": candidate.executablePath,
        ]
    }

    func accept(_ process: MacOSProcessIdentity) {
        identity = process.started
        result["identity"] = process.started
        result["process_start_utc"] = process.startedUTC
        result["executable"] = process.executablePath
    }
}

struct MacOSLogsFixture {
    let backend: URL
    let ui: URL

    var paths: [URL] { [backend, URL(fileURLWithPath: backend.path + ".stderr"), ui] }
    var boundary: URL { ui.appendingPathExtension("view") }

    func entries() throws -> [DiagnosticFiles.Entry] {
        let read = DiagnosticFiles.entries(paths: paths, boundary: boundary)
        try require(read.error.isEmpty, "Production diagnostic reader failed: \(read.error)")
        return read.entries
    }

    func entry(message: String) throws -> DiagnosticFiles.Entry {
        guard let value = try entries().first(where: { $0.message == message }) else {
            throw HelperError("Production diagnostic reader did not return fixture record: \(message)")
        }
        return value
    }

    func append(_ marker: String) throws {
        try DiagnosticFiles.append(marker, event: "torturer.fixture", level: "INFO",
                                   source: "macos-test-fixture", to: backend)
    }
}

struct MacOSConfigureResponseObservation {
    let diagnosticID: String
    let monotonicUptime: TimeInterval
}

func waitMacOSConfigureResponse(
    fixture: MacOSLogsFixture, source: String, deadline: MacOSLogsCaseDeadline
) throws -> MacOSConfigureResponseObservation {
    while deadline.remaining > 0 {
        for entry in try fixture.entries() {
            guard let record = try? JSONSerialization.jsonObject(with: Data(entry.raw.utf8)) as? [String: Any],
                  record["event"] as? String == "subscription.configure-pipe-response-read",
                  let fields = record["fields"] as? [String: Any],
                  let parameters = fields["parameters"] as? [String: Any],
                  parameters["source"] as? String == source else { continue }
            try require(fields["succeeded"] as? Bool == true,
                        "Configure did not return a response for performance URL (source)")
            guard let uptime = fields["response_read_monotonic_uptime"] as? NSNumber,
                  CFGetTypeID(uptime) != CFBooleanGetTypeID(), uptime.doubleValue.isFinite,
                  uptime.doubleValue > 0 else {
                throw HelperError("Configure response diagnostic omitted its monotonic response timestamp")
            }
            return MacOSConfigureResponseObservation(
                diagnosticID: entry.id, monotonicUptime: uptime.doubleValue
            )
        }
        Thread.sleep(forTimeInterval: min(0.05, max(0.001, deadline.remaining)))
    }
    throw HelperError("Configure did not return a response for performance URL (source) before the case deadline")
}

func writeMacOSLogsCase(_ result: [String: Any], to path: String) throws {
    let destination = URL(fileURLWithPath: path)
    try FileManager.default.createDirectory(at: destination.deletingLastPathComponent(), withIntermediateDirectories: true)
    let data = try JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys])
    var newline = data
    newline.append(0x0A)
    try newline.write(to: destination, options: .atomic)
}

func macOSLogsAssertion(_ assertions: inout [[String: Any]], id: String, expected: Any, actual: Any) throws {
    let expectedData = try JSONSerialization.data(withJSONObject: [expected], options: [.sortedKeys])
    let actualData = try JSONSerialization.data(withJSONObject: [actual], options: [.sortedKeys])
    let passed = expectedData == actualData
    assertions.append(["id": id, "outcome": passed ? "passed" : "failed", "expected": expected, "actual": actual])
    try require(passed,
                "macOS logs-case assertion \(id) failed: expected=\(String(data: expectedData, encoding: .utf8) ?? "?") actual=\(String(data: actualData, encoding: .utf8) ?? "?")")
}

func macOSNativeAssertion(_ assertions: inout [[String: Any]], id: String, expected: Any, actual: Any) throws {
    let expectedData = try JSONSerialization.data(withJSONObject: [expected], options: [.sortedKeys])
    let actualData = try JSONSerialization.data(withJSONObject: [actual], options: [.sortedKeys])
    let passed = expectedData == actualData
    assertions.append(["id": id, "outcome": passed ? "passed" : "failed", "expected": expected, "actual": actual])
    try require(passed,
                "macOS native-case assertion \(id) failed: expected=\(String(data: expectedData, encoding: .utf8) ?? "?") actual=\(String(data: actualData, encoding: .utf8) ?? "?")")
}

typealias MacOSNativeObserver = (_ operation: String, _ timeoutSeconds: Double, _ phase: String?) throws -> [String: Any]

func macOSNativeObserverCall(
    _ operation: String, timeoutSeconds: Double, deadline: MacOSLogsCaseDeadline, phase: String? = nil
) throws -> [String: Any] {
    try deadline.check("native observer \(operation)")
    var message: [String: Any] = [
        "operation": operation,
        "timeout_seconds": max(0.1, min(timeoutSeconds, deadline.remaining)),
    ]
    if let phase { message["phase"] = phase }
    let payload = try JSONSerialization.data(withJSONObject: message, options: [.sortedKeys])
    var line = Data("DOBBY_NATIVE_OBSERVE ".utf8)
    line.append(payload)
    line.append(0x0A)
    FileHandle.standardOutput.write(line)
    guard let replyLine = readLine(strippingNewline: true),
          let replyData = replyLine.data(using: .utf8),
          let reply = try JSONSerialization.jsonObject(with: replyData) as? [String: Any] else {
        throw HelperError("Native observer \(operation) returned no valid JSON line")
    }
    guard reply["ok"] as? Bool == true else {
        throw HelperError("Native observer \(operation) failed: \(reply["error"] as? String ?? "unknown observer error")")
    }
    guard let value = reply["value"] as? [String: Any] else {
        throw HelperError("Native observer \(operation) returned a non-object value")
    }
    return value
}

func macOSNativeSnapshotValue(_ snapshot: [String: Any], _ name: String) -> String? {
    snapshot[name] as? String
}

func macOSNativeSnapshotInteger(_ snapshot: [String: Any], _ name: String) -> Int? {
    guard let value = snapshot[name] as? NSNumber, CFGetTypeID(value) != CFBooleanGetTypeID() else { return nil }
    return value.intValue
}

func macOSNativeSnapshotBoolean(_ snapshot: [String: Any], _ name: String) -> Bool? {
    guard let value = snapshot[name] as? NSNumber, CFGetTypeID(value) == CFBooleanGetTypeID() else { return nil }
    return value.boolValue
}

func macOSNativeSnapshotHasNoValue(_ snapshot: [String: Any], _ name: String) -> Bool {
    guard let value = snapshot[name] else { return true }
    return value is NSNull
}

func waitMacOSNativeSnapshot(
    deadline: MacOSLogsCaseDeadline, stage: String, observer: MacOSNativeObserver,
    predicate: ([String: Any]) -> Bool
) throws -> [String: Any] {
    var last: [String: Any] = [:]
    while deadline.remaining > 0 {
        last = try observer("snapshot", min(30, deadline.remaining), nil)
        if predicate(last) { return last }
        if macOSNativeSnapshotValue(last, "state") == "FAILED" {
            throw HelperError("Go observer reported FAILED during \(stage): \(last)")
        }
        Thread.sleep(forTimeInterval: min(0.1, max(0.001, deadline.remaining)))
    }
    throw HelperError("Go observer did not reach \(stage) before the native-case deadline; last=\(last)")
}

func macOSNativeAssertTraffic(
    _ observations: [String: Any], phase: String, assertions: inout [[String: Any]]
) throws {
    for name in ["tunnel_interface", "routing_verified", "stability_verified", "throughput_positive"] {
        try macOSNativeAssertion(&assertions, id: "\(phase)-\(name)", expected: true,
                                 actual: observations[name] ?? NSNull())
    }
}

func macOSNativeProfileActions(_ nodes: [AXUIElement]) throws -> [(identifier: String, enabled: Bool, title: String)] {
    try nodes.compactMap { node in
        let id = try identifier(node)
        guard let parts = profileControlParts(id), parts.role == "action" else { return nil }
        return (id, try attribute(node, kAXEnabledAttribute) as? Bool ?? false,
                try names(node).first(where: { $0 != id }) ?? "")
    }
}

func macOSNativePressButton(_ nodes: [AXUIElement], identifier expected: String) throws {
    let action = try find(nodes, expected)
    try require(try label(action, kAXRoleAttribute) == kAXButtonRole,
                "Native control \(expected) is not a button")
    try require(try attribute(action, kAXEnabledAttribute) as? Bool == true,
                "Native control \(expected) is disabled")
    try press(action)
}

func macOSNativeWaitForStatus(
    _ owned: MacOSOwnedLogsApp, expected: String, deadline: MacOSLogsCaseDeadline
) throws -> [AXUIElement] {
    try waitMacOSLogsCase(deadline, stage: "rendered \(expected) status") {
        let nodes = try macOSLogsAXNodes(owned)
        guard !nodes.isEmpty,
              try macOSRenderedText(find(nodes, "connection.status"), identifier: "connection.status") == expected else {
            return nil
        }
        return nodes
    }
}

func macOSNativeUniqueAttemptURL(_ source: String) throws -> String {
    guard var components = URLComponents(string: source), components.scheme?.lowercased() == "https" else {
        throw HelperError("Subscription failure URL is not an absolute HTTPS URL")
    }
    var queryItems = components.queryItems ?? []
    queryItems.append(URLQueryItem(name: "attempt", value: UUID().uuidString.lowercased()))
    components.queryItems = queryItems
    guard let result = components.string else { throw HelperError("Could not create a unique fail-once subscription URL") }
    return result
}

func macOSNativeWaitForSubscription(
    _ request: [String: Any], deadline: MacOSLogsCaseDeadline, count: Int64
) throws -> MacOSSubscriptionFixtureObservation {
    try waitMacOSSubscriptionRequest(request: request, deadline: deadline, count: count)
}

func macOSNativeWaitForNoAdditionalGet(
    _ request: [String: Any], deadline: MacOSLogsCaseDeadline, count: Int64
) throws -> MacOSSubscriptionFixtureObservation {
    try waitMacOSForNoAdditionalSubscriptionGet(request: request, deadline: deadline,
                                                expectedCount: count, quietMilliseconds: 1_500)
}

func macOSNativeIsActiveGeneration(_ current: [String: Any], sameAs previous: [String: Any]) -> Bool {
    macOSNativeSnapshotValue(current, "state") == "CONNECTED" &&
        macOSNativeSnapshotInteger(current, "generation") == macOSNativeSnapshotInteger(previous, "generation") &&
        macOSNativeSnapshotValue(current, "active_digest") == macOSNativeSnapshotValue(previous, "active_digest") &&
        macOSNativeSnapshotValue(current, "active_mode") == macOSNativeSnapshotValue(previous, "active_mode") &&
        macOSNativeSnapshotInteger(current, "active_index") == macOSNativeSnapshotInteger(previous, "active_index")
}

func macOSNativeActiveProfileMatches(_ current: [String: Any], previous: [String: Any]) throws -> Bool {
    guard macOSNativeIsActiveGeneration(current, sameAs: previous),
          let currentProfile = current["active_profile"],
          let previousProfile = previous["active_profile"] else { return false }
    let currentData = try JSONSerialization.data(withJSONObject: currentProfile, options: [.sortedKeys])
    let previousData = try JSONSerialization.data(withJSONObject: previousProfile, options: [.sortedKeys])
    return currentData == previousData
}

func macOSNativeProfileTransitionMatches(_ state: [String: Any], index: Int) -> Bool {
    if let pending = state["pending_target"] as? [String: Any] {
        return pending["mode"] as? String == "PROFILE_INDEX" &&
            (pending["index"] as? Int) == index &&
            pending["digest"] as? String == state["digest"] as? String
    }
    return ["PROBING", "PREPARING"].contains(macOSNativeSnapshotValue(state, "state") ?? "") &&
        macOSNativeSnapshotValue(state, "active_mode") == "PROFILE_INDEX" &&
        macOSNativeSnapshotInteger(state, "active_index") == index &&
        macOSNativeSnapshotValue(state, "active_digest") == macOSNativeSnapshotValue(state, "digest")
}

func macOSNativeRenderedProfileTransition(
    _ owned: MacOSOwnedLogsApp, digest: String, index: Int, competingIndex: Int
) throws -> Bool {
    let nodes = try macOSLogsAXNodes(owned)
    guard !nodes.isEmpty else { return false }
    let actions = try macOSNativeProfileActions(nodes)
    let target = "profile.\(digest).\(index).action"
    let competing = "profile.\(digest).\(competingIndex).action"
    guard let targetAction = actions.first(where: { $0.identifier == target }),
          targetAction.enabled, targetAction.title == "Stop" else { return false }
    if let other = actions.first(where: { $0.identifier == competing }), other.enabled {
        throw HelperError("Profile selection exposed competing Connect action \(competing)")
    }
    return true
}

func waitMacOSLogsCase<T>(
    _ deadline: MacOSLogsCaseDeadline, stage: String, condition: () throws -> T?
) throws -> T {
    var lastError = "not ready"
    while deadline.remaining > 0 {
        do {
            if let value = try condition() { return value }
            lastError = "condition was false"
        } catch is MacOSProcessMissing {
            throw HelperError("macOS \(stage) process disappeared before readiness")
        } catch {
            lastError = String(reflecting: error)
        }
        Thread.sleep(forTimeInterval: min(0.05, max(0.001, deadline.remaining)))
    }
    throw HelperError("macOS \(stage) exceeded the native-case deadline; last observation: \(lastError)")
}

func macOSLogsAXNodes(_ owned: MacOSOwnedLogsApp, includeTextAreaChildren: Bool = false) throws -> [AXUIElement] {
    let root = AXUIElementCreateApplication(owned.pid)
    AXUIElementSetMessagingTimeout(root, 0.3)
    guard let windows = try attribute(root, kAXWindowsAttribute) as? [AXUIElement],
          let window = windows.first else { return [] }
    AXUIElementSetMessagingTimeout(window, 0.3)
    return try elements(window, includeTextAreaChildren: includeTextAreaChildren)
}

func macOSLogsView(_ owned: MacOSOwnedLogsApp) throws -> (nodes: [AXUIElement], view: AXUIElement, text: String) {
    let nodes = try macOSLogsAXNodes(owned)
    guard !nodes.isEmpty else { throw HelperError("Candidate app has no native window") }
    let view = try find(nodes, "Connection logs", editor: true)
    return (nodes, view, try label(view, kAXValueAttribute))
}

func macOSLogDetailsURL(_ entry: DiagnosticFiles.Entry) -> URL {
    var components = URLComponents()
    components.scheme = "dobbylog"
    components.host = "record"
    components.path = "/\(entry.id)"
    return components.url!
}

func macOSLogDetailsIdentifier(_ url: URL) -> String? {
    guard url.scheme == "dobbylog", url.host == "record" else { return nil }
    let identity = url.lastPathComponent
    return identity.isEmpty ? nil : "log.details.\(identity)"
}

func macOSLogDetailsLink(_ nodes: [AXUIElement], entry: DiagnosticFiles.Entry) throws -> AXUIElement {
    let expectedURL = macOSLogDetailsURL(entry).absoluteString
    var matches = [AXUIElement]()
    for element in nodes {
        guard try label(element, kAXRoleAttribute) == "AXLink" else { continue }
        guard let rawURL = try attribute(element, kAXURLAttribute) else { continue }
        let actualURL = (rawURL as? URL)?.absoluteString ?? rawURL as? String
        if actualURL == expectedURL { matches.append(element) }
    }
    try require(matches.count == 1,
                "Expected one native Details link for \(entry.id), found \(matches.count); expected URL=\(expectedURL)")
    return matches[0]
}

func macOSLogsWindowIsReady(_ owned: MacOSOwnedLogsApp) throws -> Bool {
    let nodes = try macOSLogsAXNodes(owned)
    guard !nodes.isEmpty else { return false }
    _ = try find(nodes, "Connection logs", editor: true)
    return true
}

func writeMacOSNativeCaseProgress(caseID: String, stage: String) {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    formatter.timeZone = TimeZone(secondsFromGMT: 0)!
    let timestamp = formatter.string(from: Date())
    FileHandle.standardError.write(
        Data("native-case-progress utc=\(timestamp) case=\(caseID) stage=\(stage)\n".utf8)
    )
}

func activateMacOSLogsApp(
    _ owned: MacOSOwnedLogsApp, caseID: String, deadline: MacOSLogsCaseDeadline
) throws {
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "activation-identity-check-start")
    try verifyMacOSLogsApp(owned)
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "activation-request-start")
    try require(owned.application.activate(options: [.activateAllWindows]), "Could not activate candidate app")
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "activation-request-returned")
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "activation-readiness-start")
    _ = try waitMacOSLogsCase(deadline, stage: "app foreground activation") { () throws -> Bool? in
        NSWorkspace.shared.frontmostApplication?.processIdentifier == owned.pid ? true : nil
    }
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "activation-readiness-complete")
}

func verifyMacOSLogsApp(_ owned: MacOSOwnedLogsApp) throws {
    guard let identity = owned.identity else {
        throw HelperError("Refusing UI action without a valid OS process start identity for pid \(owned.pid)")
    }
    let current = try macOSProcessIdentity(owned.pid)
    try require(current.executablePath == owned.executablePath && current.started == identity,
                "Refusing UI action for a process whose OS executable/start identity changed: pid=\(owned.pid)")
}

func startMacOSLogsApp(
    candidate: MacOSCandidateApp, fixture: MacOSLogsFixture, caseID: String, deadline: MacOSLogsCaseDeadline,
    launches: inout [MacOSOwnedLogsApp], opening urls: [URL] = []
) throws -> MacOSOwnedLogsApp {
    try deadline.check("candidate app launch")
    for running in NSWorkspace.shared.runningApplications where !running.isTerminated {
        if running.executableURL?.resolvingSymlinksInPath().path == candidate.executablePath {
            throw HelperError("macOS native-case requires a cold launch; exact candidate process \(running.processIdentifier) is already running")
        }
    }
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.activates = true
    configuration.environment = [
        "DOBBY_LOG_PATH": fixture.backend.path,
        "DOBBY_UI_LOG_PATH": fixture.ui.path,
    ]
    var completed = false
    var launched: NSRunningApplication?
    var launchError: Error?
    if urls.isEmpty {
        writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-launch-request-start")
        NSWorkspace.shared.openApplication(at: candidate.bundleURL, configuration: configuration) { application, error in
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-launch-callback-start")
            launched = application
            launchError = error
            completed = true
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-launch-callback-complete")
        }
    } else {
        writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-open-url-request-start")
        NSWorkspace.shared.open(urls, withApplicationAt: candidate.bundleURL, configuration: configuration) { application, error in
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-open-url-callback-start")
            launched = application
            launchError = error
            completed = true
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-open-url-callback-complete")
        }
    }
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "workspace-launch-request-returned")
    while !completed && deadline.remaining > 0 {
        _ = RunLoop.current.run(mode: .default, before: Date(timeIntervalSinceNow: min(0.05, max(0.001, deadline.remaining))))
    }
    writeMacOSNativeCaseProgress(
        caseID: caseID,
        stage: completed ? "workspace-launch-callback-observed" : "workspace-launch-callback-deadline-expired"
    )
    try require(completed, "Candidate app launch did not return before the native-case deadline")
    if let launchError { throw HelperError("Candidate app launch failed: \(launchError)") }
    guard let launched, !launched.isTerminated else { throw HelperError("Candidate app launch returned no live process") }
    try require(launched.executableURL?.resolvingSymlinksInPath().path == candidate.executablePath,
                "Workspace returned a different executable than requested: \(launched.executableURL?.path ?? "unknown")")
    let owned = MacOSOwnedLogsApp(application: launched, candidate: candidate)
    launches.append(owned)
    let pid = launched.processIdentifier
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "owned-process-identity-read-start")
    let process = try waitMacOSLogsCase(deadline, stage: "candidate OS process identity") { () -> MacOSProcessIdentity? in
        try macOSProcessIdentity(pid)
    }
    try require(process.executablePath == candidate.executablePath,
                "Candidate app process executable differs from the requested installed path: \(process.executablePath)")
    owned.accept(process)
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "owned-process-identity-accepted")
    try activateMacOSLogsApp(owned, caseID: caseID, deadline: deadline)
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "window-readiness-start")
    _ = try waitMacOSLogsCase(deadline, stage: "native application window readiness") { () throws -> Bool? in
        try macOSLogsWindowIsReady(owned) ? true : nil
    }
    writeMacOSNativeCaseProgress(caseID: caseID, stage: "window-readiness-complete")
    return owned
}

func openMacOSProtocolURL(
    _ url: URL, candidate: MacOSCandidateApp, deadline: MacOSLogsCaseDeadline
) throws -> NSRunningApplication {
    try deadline.check("protocol URL activation")
    let configuration = NSWorkspace.OpenConfiguration()
    configuration.activates = true
    var completed = false
    var openedApplication: NSRunningApplication?
    var openError: Error?
    NSWorkspace.shared.open([url], withApplicationAt: candidate.bundleURL, configuration: configuration) { application, error in
        openedApplication = application
        openError = error
        completed = true
    }
    while !completed && deadline.remaining > 0 {
        _ = RunLoop.current.run(mode: .default, before: Date(timeIntervalSinceNow: min(0.05, max(0.001, deadline.remaining))))
    }
    try require(completed, "Protocol URL activation did not return before the native-case deadline")
    if let openError { throw HelperError("Candidate app protocol URL activation failed: \(openError)") }
    guard let application = openedApplication, !application.isTerminated,
          application.executableURL?.resolvingSymlinksInPath().path == candidate.executablePath else {
        throw HelperError("Protocol URL activation returned no live candidate app process")
    }
    return application
}

func closeMacOSLogsApp(_ owned: MacOSOwnedLogsApp, deadline: MacOSLogsCaseDeadline) throws {
    if let identity = owned.identity {
        do {
            let current = try macOSProcessIdentity(owned.pid)
            try require(current.executablePath == owned.executablePath && current.started == identity,
                        "Refusing to close a process whose OS executable/start identity changed: pid=\(owned.pid)")
        } catch is MacOSProcessMissing {
            owned.result["alive_after_case"] = false
            return
        }
    } else if owned.application.executableURL?.resolvingSymlinksInPath().path != owned.executablePath {
        throw HelperError("Refusing to clean up an unidentified process whose executable path changed")
    }
    if !owned.application.isTerminated {
        try require(owned.application.terminate(), "Candidate app quit request failed")
    }
    do {
        _ = try waitMacOSLogsCase(deadline, stage: "owned app close") { () throws -> Bool? in
            guard let identity = owned.identity else {
                return owned.application.isTerminated ? true : nil
            }
            do {
                let current = try macOSProcessIdentity(owned.pid)
                try require(current.executablePath == owned.executablePath && current.started == identity,
                            "Candidate process identity changed while closing")
                return nil
            } catch is MacOSProcessMissing {
                return true
            }
        }
    } catch {
        if let identity = owned.identity {
            let current = try macOSProcessIdentity(owned.pid)
            try require(current.executablePath == owned.executablePath && current.started == identity,
                        "Refusing to force-terminate a process whose OS executable/start identity changed")
        } else if owned.application.executableURL?.resolvingSymlinksInPath().path != owned.executablePath {
            throw HelperError("Refusing to force-terminate an unidentified process whose executable path changed")
        }
        try require(owned.application.forceTerminate(), "Could not terminate owned candidate process")
        _ = try waitMacOSLogsCase(deadline, stage: "owned app termination") { () throws -> Bool? in
            if owned.identity == nil { return owned.application.isTerminated ? true : nil }
            do { _ = try macOSProcessIdentity(owned.pid); return nil }
            catch is MacOSProcessMissing { return true }
        }
    }
    owned.result["alive_after_case"] = false
}

func macOSLogsCaseScreenshot(
    _ owned: MacOSOwnedLogsApp, name: String, directory: String,
    deadline: MacOSLogsCaseDeadline, screenshots: inout [String]
) throws {
    try deadline.check("screenshot capture")
    try verifyMacOSLogsApp(owned)
    try FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
    let path = URL(fileURLWithPath: directory).appendingPathComponent(name + ".png").path
    try capture(owned.pid, path: path)
    screenshots.append(path)
}

func postMacOSLogsScroll(
    _ owned: MacOSOwnedLogsApp, position: String, deadline: MacOSLogsCaseDeadline
) throws -> (start: Int, end: Int, count: Int) {
    try require(position == "top" || position == "bottom", "Log scroll position must be top or bottom")
    try verifyMacOSLogsApp(owned)
    let first = try macOSLogsView(owned)
    var parent: AXUIElement? = first.view
    var scrollArea: AXUIElement?
    for _ in 0..<8 {
        guard let element = parent, let next = try axElement(element, kAXParentAttribute) else { break }
        if try label(next, kAXRoleAttribute) == kAXScrollAreaRole { scrollArea = next; break }
        parent = next
    }
    guard let area = scrollArea, let window = first.nodes.first else {
        throw HelperError("Native log viewer has no visible scroll area")
    }
    let target = try bounds(first.view).intersection(bounds(area)).intersection(bounds(window))
    try require(!target.isNull && target.width > 0 && target.height > 0,
                "Native log viewer is outside its scroll viewport")
    let point = CGPoint(x: target.midX, y: target.midY)
    var sample = try visibleCharacterRange(first.view)
    func distance(_ range: CFRange, count: Int) -> Int {
        position == "top" ? range.location : count - range.location - range.length
    }
    let initialDistance = distance(sample.range, count: sample.characterCount)
    if initialDistance <= 1 {
        return (sample.range.location, sample.range.location + sample.range.length, sample.characterCount)
    }
    guard let moved = CGEvent(mouseEventSource: nil, mouseType: .mouseMoved,
                              mouseCursorPosition: point, mouseButton: .left) else {
        throw HelperError("Could not position the pointer over native logs")
    }
    moved.post(tap: .cghidEventTap)
    var totalEvents = 0
    var reached = false
    for delta in [240, -240] {
        var unchanged = 0
        while totalEvents < 72 && deadline.remaining > 0 {
            guard let event = CGEvent(scrollWheelEvent2Source: nil, units: .pixel,
                                      wheelCount: 1, wheel1: Int32(delta), wheel2: 0, wheel3: 0) else {
                throw HelperError("Could not create native log scroll event")
            }
            event.location = point
            let phase: CGScrollPhase = totalEvents == 0 ? .began : .changed
            event.setIntegerValueField(.scrollWheelEventScrollPhase, value: Int64(phase.rawValue))
            event.post(tap: .cghidEventTap)
            totalEvents += 1
            Thread.sleep(forTimeInterval: min(0.05, max(0.001, deadline.remaining)))
            let current = try macOSLogsView(owned)
            sample = try visibleCharacterRange(current.view)
            let currentDistance = distance(sample.range, count: sample.characterCount)
            if currentDistance <= 1 { reached = true; break }
            if currentDistance < initialDistance { unchanged = 0 }
            else { unchanged += 1 }
            if unchanged >= 3 { break }
        }
        if reached { break }
    }
    if totalEvents > 0, let ended = CGEvent(scrollWheelEvent2Source: nil, units: .pixel,
                                            wheelCount: 1, wheel1: 0, wheel2: 0, wheel3: 0) {
        ended.location = point
        ended.setIntegerValueField(.scrollWheelEventScrollPhase, value: Int64(CGScrollPhase.ended.rawValue))
        ended.post(tap: .cghidEventTap)
    }
    try require(reached, "Native log viewer did not scroll to \(position)")
    return (sample.range.location, sample.range.location + sample.range.length, sample.characterCount)
}

struct MacOSSubscriptionFixtureObservation {
    let stats: [String: Any]
    let requestFinishedUnixMs: Int64
    let roundTripMilliseconds: Int64
}

func requiredMacOSSubscriptionURL(_ request: [String: Any]) throws -> String {
    guard let value = request["subscription_url"] as? String,
          let components = URLComponents(string: value),
          components.scheme?.lowercased() == "https", !(components.host ?? "").isEmpty else {
        throw HelperError("macOS native case requires an absolute HTTPS subscription_url")
    }
    return value
}

func macOSImportURL(_ subscription: String) throws -> URL {
    var components = URLComponents()
    components.scheme = "dobbyvpn"
    components.host = "import"
    components.queryItems = [URLQueryItem(name: "url", value: subscription)]
    guard let url = components.url else { throw HelperError("Could not construct the native subscription import URL") }
    return url
}

func macOSURLAppendingQuery(_ source: String, name: String, value: String) throws -> String {
    guard var components = URLComponents(string: source),
          components.scheme?.lowercased() == "https", !(components.host ?? "").isEmpty else {
        throw HelperError("Cannot add (name) to a non-HTTPS subscription URL")
    }
    var items = components.queryItems ?? []
    items.append(URLQueryItem(name: name, value: value))
    components.queryItems = items
    guard let result = components.string else {
        throw HelperError("Could not create subscription URL with (name)")
    }
    return result
}

func macOSSubscriptionFixtureObservation(
    request: [String: Any], deadline: MacOSLogsCaseDeadline
) throws -> MacOSSubscriptionFixtureObservation {
    guard let rawURL = request["subscription_control_url"] as? String,
          let url = URL(string: rawURL), url.scheme?.lowercased() == "https",
          let key = request["subscription_control_key"] as? String, !key.isEmpty else {
        throw HelperError("macOS subscription case requires subscription_control_url and subscription_control_key")
    }
    try deadline.check("subscription fixture control read")
    var controlRequest = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData,
                                    timeoutInterval: min(5, max(0.1, deadline.remaining)))
    controlRequest.httpMethod = "GET"
    controlRequest.setValue(key, forHTTPHeaderField: "X-DobbyVPN-Torturer-Key")
    let start = ProcessInfo.processInfo.systemUptime
    var responseData: Data?
    var responseError: Error?
    let completed = DispatchSemaphore(value: 0)
    let task = URLSession.shared.dataTask(with: controlRequest) { data, response, error in
        responseData = data
        if let error { responseError = error }
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            responseError = HelperError("Subscription fixture control returned HTTP \(http.statusCode)")
        }
        completed.signal()
    }
    task.resume()
    let wait = completed.wait(timeout: .now() + min(5, max(0.1, deadline.remaining)))
    let end = ProcessInfo.processInfo.systemUptime
    let endUnixMs = Int64(Date().timeIntervalSince1970 * 1_000)
    if wait == .timedOut {
        task.cancel()
        throw HelperError("Subscription fixture control did not return before its deadline")
    }
    if let responseError { throw HelperError("Subscription fixture control failed: \(responseError)") }
    guard let responseData,
          let stats = try JSONSerialization.jsonObject(with: responseData) as? [String: Any] else {
        throw HelperError("Subscription fixture control returned invalid JSON")
    }
    for name in ["subscription_gets", "last_subscription_get_started_at_unix_ms", "in_flight_gets",
                 "max_in_flight_gets", "server_now_unix_ms"] {
        guard let value = stats[name] as? NSNumber else {
            throw HelperError("Subscription fixture control omitted integer field \(name)")
        }
        try require(CFGetTypeID(value) != CFBooleanGetTypeID(),
                    "Subscription fixture control field \(name) was not an integer")
    }
    return MacOSSubscriptionFixtureObservation(
        stats: stats, requestFinishedUnixMs: endUnixMs,
        roundTripMilliseconds: Int64(max(0, (end - start) * 1_000))
    )
}

func macOSReplaceSubscriptionFixtureProfile(
    request: [String: Any], content: Data, deadline: MacOSLogsCaseDeadline
) throws {
    guard let rawURL = request["subscription_control_url"] as? String,
          let url = URL(string: rawURL), url.scheme?.lowercased() == "https",
          let key = request["subscription_control_key"] as? String, !key.isEmpty,
          !content.isEmpty, content.count <= 1 << 20 else {
        throw HelperError("macOS layout case requires the fixture control endpoint and a profile from 1 byte through 1 MiB")
    }
    try deadline.check("subscription fixture profile update")
    var controlRequest = URLRequest(url: url.appendingPathComponent("profile"),
                                    cachePolicy: .reloadIgnoringLocalCacheData,
                                    timeoutInterval: min(5, max(0.1, deadline.remaining)))
    controlRequest.httpMethod = "POST"
    controlRequest.setValue(key, forHTTPHeaderField: "X-DobbyVPN-Torturer-Key")
    controlRequest.setValue("application/octet-stream", forHTTPHeaderField: "Content-Type")
    controlRequest.httpBody = content
    var responseError: Error?
    let completed = DispatchSemaphore(value: 0)
    let task = URLSession.shared.dataTask(with: controlRequest) { _, response, error in
        if let error { responseError = error }
        if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
            responseError = HelperError("Subscription fixture profile update returned HTTP \(http.statusCode)")
        }
        if response == nil && responseError == nil {
            responseError = HelperError("Subscription fixture profile update returned no HTTP response")
        }
        completed.signal()
    }
    task.resume()
    if completed.wait(timeout: .now() + min(5, max(0.1, deadline.remaining))) == .timedOut {
        task.cancel()
        throw HelperError("Subscription fixture profile update did not return before the case deadline")
    }
    if let responseError { throw HelperError("Subscription fixture profile update failed: \(responseError)") }
}

func macOSSubscriptionStatsInteger(_ observation: MacOSSubscriptionFixtureObservation, _ name: String) throws -> Int64 {
    guard let value = observation.stats[name] as? NSNumber else {
        throw HelperError("Subscription fixture observation omitted \(name)")
    }
    return value.int64Value
}

func waitMacOSSubscriptionRequest(
    request: [String: Any], deadline: MacOSLogsCaseDeadline, count: Int64
) throws -> MacOSSubscriptionFixtureObservation {
    var last: MacOSSubscriptionFixtureObservation?
    while deadline.remaining > 0 {
        let current = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
        last = current
        let observedCount = try macOSSubscriptionStatsInteger(current, "subscription_gets")
        try require(observedCount <= count,
                    "Subscription fixture observed duplicate native Configure requests: expected=\(count), actual=\(observedCount)")
        if observedCount == count,
           try macOSSubscriptionStatsInteger(current, "in_flight_gets") == 0 {
            return current
        }
        Thread.sleep(forTimeInterval: min(0.05, max(0.001, deadline.remaining)))
    }
    throw HelperError("Subscription fixture did not complete exactly \(count) GETs before the case deadline; last=\(String(describing: last?.stats))")
}

func waitMacOSForAcceptedInventory(
    _ owned: MacOSOwnedLogsApp, source: String, minimumRows: Int, deadline: MacOSLogsCaseDeadline
) throws -> (nodes: [AXUIElement], layout: [String: Any], rows: [[String: Any]]) {
    try waitMacOSLogsCase(deadline, stage: "accepted profile inventory rendering") {
        let nodes = try macOSLogsAXNodes(owned)
        guard !nodes.isEmpty,
              try label(find(nodes, "Connection configuration", editor: true), kAXValueAttribute) == source else {
            return nil
        }
        let rows = try profileRowDetails(nodes)
        let digests = Set(rows.compactMap { $0["profile_digest"] as? String })
        guard rows.count >= minimumRows, digests.count == 1, !(digests.first ?? "").isEmpty else { return nil }
        return (nodes, try profileListLayout(nodes), rows)
    }
}

func macOSRenderedText(_ element: AXUIElement, identifier expected: String) throws -> String {
    let value = try attribute(element, kAXValueAttribute) as? String ?? ""
    if !value.isEmpty { return value }
    let title = try attribute(element, kAXTitleAttribute) as? String ?? ""
    if !title.isEmpty { return title }
    return try names(element).first(where: { $0 != expected }) ?? ""
}

func macOSProfileSemanticIDsAreConsistent(_ rows: [[String: Any]]) -> Bool {
    guard !rows.isEmpty else { return false }
    return rows.enumerated().allSatisfy { offset, row in
        guard let index = row["profile_index"] as? Int,
              let digest = row["profile_digest"] as? String,
              !digest.isEmpty else { return false }
        return index == offset &&
            row["description_automation_id"] as? String == "profile.\(digest).\(index).description" &&
            row["protocol_automation_id"] as? String == "profile.\(digest).\(index).protocol" &&
            row["action_automation_id"] as? String == "profile.\(digest).\(index).action"
    }
}

func macOSProfileActionIsEnabled(_ nodes: [AXUIElement], automationID: String) throws -> Bool {
    let matches = try nodes.filter { try identifier($0) == automationID }
    guard matches.count == 1, try label(matches[0], kAXRoleAttribute) == kAXButtonRole,
          let enabled = try attribute(matches[0], kAXEnabledAttribute) as? Bool else { return false }
    return enabled
}

func waitMacOSForEnabledProfileAction(
    _ owned: MacOSOwnedLogsApp, source: String, automationID: String,
    deadline: MacOSLogsCaseDeadline
) throws {
    _ = try waitMacOSLogsCase(deadline, stage: "enabled digest-scoped profile action") { () throws -> Bool? in
        let nodes = try macOSLogsAXNodes(owned)
        guard !nodes.isEmpty,
              try label(find(nodes, "Connection configuration", editor: true), kAXValueAttribute) == source,
              try macOSProfileActionIsEnabled(nodes, automationID: automationID) else { return nil }
        return true
    }
}

func macOSRect(_ value: Any?) -> CGRect? {
    guard let value = value as? [String: Any],
          let x = value["x"] as? NSNumber,
          let y = value["y"] as? NSNumber,
          let width = value["width"] as? NSNumber,
          let height = value["height"] as? NSNumber,
          [x, y, width, height].allSatisfy({ $0.doubleValue.isFinite }),
          width.doubleValue > 0, height.doubleValue > 0 else { return nil }
    return CGRect(x: CGFloat(x.doubleValue), y: CGFloat(y.doubleValue),
                  width: CGFloat(width.doubleValue), height: CGFloat(height.doubleValue))
}

func macOSProfileLayoutGeometryIsUsable(_ layout: [String: Any]) -> Bool {
    guard let window = macOSRect(layout["window"]),
          let controls = macOSRect(layout["controls"]),
          let profile = macOSRect(layout["profile_viewport"]),
          let action = macOSRect(layout["connection_action"]),
          let logs = macOSRect(layout["logs"]) else { return false }
    return contained(controls, by: window) && contained(profile, by: window) &&
        contained(action, by: window) && contained(logs, by: window) &&
        contained(profile, by: controls) && contained(action, by: controls) &&
        logs.width >= 160 && logs.height >= 120 &&
        !profile.intersects(action) && !profile.intersects(logs) && !action.intersects(logs)
}

func macOSProfileActionIsUsable(
    _ nodes: [AXUIElement], layout: [String: Any], automationID: String,
    requireEnabled: Bool = true
) throws -> Bool {
    guard (layout["visible_profile_actions"] as? [String])?.contains(automationID) == true,
          let viewport = macOSRect(layout["profile_viewport"]) else { return false }
    let actions = try nodes.filter { try identifier($0) == automationID }
    guard actions.count == 1, try label(actions[0], kAXRoleAttribute) == kAXButtonRole else { return false }
    if requireEnabled {
        let enabled = try macOSProfileActionIsEnabled(nodes, automationID: automationID)
        if !enabled { return false }
    }
    return contained(try bounds(actions[0]), by: viewport)
}

func macOSMergeRenderedProfileRows(
    _ rows: [[String: Any]], digest: String, into accumulated: inout [Int: [String: Any]]
) throws {
    for row in rows {
        guard let index = row["profile_index"] as? Int,
              row["index"] as? Int == index + 1,
              row["profile_digest"] as? String == digest,
              row["name"] as? String == (index == 0 ? "Profile 1" : "Layout profile \(index + 1)"),
              row["protocol"] as? String == "OUTLINE",
              row["action"] as? String == "Connect",
              row["description_automation_id"] as? String == "profile.\(digest).\(index).description",
              row["protocol_automation_id"] as? String == "profile.\(digest).\(index).protocol",
              row["action_automation_id"] as? String == "profile.\(digest).\(index).action" else {
            throw HelperError("A progressively rendered profile row did not match the accepted 24-row inventory")
        }
        if let previous = accumulated[index] {
            let oldBytes = try JSONSerialization.data(withJSONObject: previous, options: [.sortedKeys])
            let newBytes = try JSONSerialization.data(withJSONObject: row, options: [.sortedKeys])
            try require(oldBytes == newBytes,
                        "Rendered profile \(index) changed metadata while scrolling")
        } else {
            if let last = accumulated.keys.max() {
                try require(index > last, "Profile \(index) was realized after a later source row")
            }
            accumulated[index] = row
        }
    }
}

func macOSVerifyTwentyFourProfileLayout(
    _ owned: MacOSOwnedLogsApp, digest: String, caseID: String,
    requireEnabledActions: Bool,
    deadline: MacOSLogsCaseDeadline, screenshotDirectory: String,
    assertions: inout [[String: Any]], screenshots: inout [String]
) throws -> [String: Any] {
    let firstAction = "profile.\(digest).0.action"
    let lastAction = "profile.\(digest).23.action"
    var realized = [Int: [String: Any]]()
    var realizedTargets = [Int]()
    var usableActions = [Int]()
    var geometryValid = true

    _ = try scrollProfileList(try macOSLogsAXNodes(owned), position: "top", targetActionID: firstAction)
    let topNodes = try macOSLogsAXNodes(owned)
    let top = try profileListLayout(topNodes)
    guard let topPosition = top["scroll_position"] as? Double,
          abs(topPosition) <= 1,
          let topRows = top["profile_rows"] as? [[String: Any]], !topRows.isEmpty else {
        throw HelperError("Profile list did not expose its first realized rows at the top")
    }
    realizedTargets.append(0)
    geometryValid = geometryValid && macOSProfileLayoutGeometryIsUsable(top)
    try macOSMergeRenderedProfileRows(topRows, digest: digest, into: &realized)
    let firstActionUsable = try macOSProfileActionIsUsable(
        topNodes, layout: top, automationID: firstAction, requireEnabled: requireEnabledActions
    )
    if firstActionUsable { usableActions.append(0) }
    try macOSNativeAssertion(&assertions, id: "first-profile-action-is-reachable-at-list-top",
                             expected: true, actual: firstActionUsable)
    try macOSLogsCaseScreenshot(owned, name: caseID + "-top", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    for index in 1..<23 {
        let actionID = "profile.\(digest).\(index).action"
        let targetPercent = Int((Double(index) * 100 / 23).rounded())
        _ = try scrollProfileList(try macOSLogsAXNodes(owned), position: String(targetPercent),
                                  targetActionID: actionID)
        let nodes = try macOSLogsAXNodes(owned)
        let layout = try profileListLayout(nodes)
        guard let rows = layout["profile_rows"] as? [[String: Any]],
              rows.contains(where: { $0["profile_index"] as? Int == index }),
              (layout["visible_profile_actions"] as? [String])?.contains(actionID) == true else {
            throw HelperError("Profile list did not realize and expose profile \(index) within its viewport")
        }
        realizedTargets.append(index)
        geometryValid = geometryValid && macOSProfileLayoutGeometryIsUsable(layout)
        try macOSMergeRenderedProfileRows(rows, digest: digest, into: &realized)
        try require(try macOSProfileActionIsUsable(
            nodes, layout: layout, automationID: actionID, requireEnabled: requireEnabledActions
        ), "Profile action \(index) was not reachable within its native list viewport")
        usableActions.append(index)
    }

    _ = try scrollProfileList(try macOSLogsAXNodes(owned), position: "bottom", targetActionID: lastAction)
    let bottomNodes = try macOSLogsAXNodes(owned)
    let bottom = try profileListLayout(bottomNodes)
    guard let bottomPosition = bottom["scroll_position"] as? Double,
          bottomPosition >= 99,
          let bottomRows = bottom["profile_rows"] as? [[String: Any]], !bottomRows.isEmpty else {
        throw HelperError("Profile list did not expose its last realized rows at the bottom")
    }
    realizedTargets.append(23)
    geometryValid = geometryValid && macOSProfileLayoutGeometryIsUsable(bottom)
    try macOSMergeRenderedProfileRows(bottomRows, digest: digest, into: &realized)
    let lastActionUsable = try macOSProfileActionIsUsable(
        bottomNodes, layout: bottom, automationID: lastAction, requireEnabled: requireEnabledActions
    )
    if lastActionUsable { usableActions.append(23) }
    try macOSNativeAssertion(&assertions, id: "last-profile-action-is-reachable-at-list-bottom",
                             expected: true, actual: lastActionUsable)
    try macOSNativeAssertion(&assertions, id: "progressive-scroll-realizes-all-24-source-ordered-profile-rows",
                             expected: Array(0..<24), actual: realizedTargets)
    try macOSNativeAssertion(&assertions, id: requireEnabledActions
                                ? "all-24-profile-actions-remain-enabled-and-reachable"
                                : "all-24-profile-actions-remain-reachable-while-connected",
                             expected: Array(0..<24), actual: usableActions)
    try macOSNativeAssertion(&assertions, id: "empty-description-renders-profile-one-label",
                             expected: "Profile 1", actual: realized[0]?["name"] as? String ?? "")
    try macOSNativeAssertion(&assertions, id: "profile-controls-use-accepted-digest-and-index-ids",
                             expected: true,
                             actual: macOSProfileSemanticIDsAreConsistent(
                                realized.keys.sorted().compactMap { realized[$0] }
                             ))
    try macOSNativeAssertion(&assertions, id: "profile-layout-bounds-remain-usable-through-scroll",
                             expected: true, actual: geometryValid)
    try macOSNativeAssertion(&assertions, id: "profile-layout-begins-at-top-and-reaches-bottom",
                             expected: [0, 100],
                             actual: [Int(topPosition.rounded()), Int(bottomPosition.rounded())])
    try macOSNativeAssertion(&assertions, id: "profile-digest-stays-stable-through-scroll",
                             expected: [digest],
                             actual: Set(realized.values.compactMap { $0["profile_digest"] as? String }).sorted())
    try macOSLogsCaseScreenshot(owned, name: caseID + "-bottom", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)
    return ["digest": digest, "realized_profile_indices": realizedTargets,
            "usable_profile_actions": usableActions, "geometry_valid": geometryValid]
}

func macOSDisconnectedStatus(_ nodes: [AXUIElement]) throws -> String {
    let status = try find(nodes, "connection.status")
    return try macOSRenderedText(status, identifier: "connection.status")
}

func waitMacOSForNoAdditionalSubscriptionGet(
    request: [String: Any], deadline: MacOSLogsCaseDeadline, expectedCount: Int64,
    quietMilliseconds: Int64 = 800
) throws -> MacOSSubscriptionFixtureObservation {
    let quietUntil = ProcessInfo.processInfo.systemUptime + Double(quietMilliseconds) / 1_000
    var last: MacOSSubscriptionFixtureObservation?
    repeat {
        let current = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
        last = current
        try require(try macOSSubscriptionStatsInteger(current, "subscription_gets") == expectedCount,
                    "Protocol re-import unexpectedly fetched the accepted subscription again")
        try require(try macOSSubscriptionStatsInteger(current, "in_flight_gets") == 0,
                    "Subscription fixture still had an in-flight GET after acceptance")
        if ProcessInfo.processInfo.systemUptime < quietUntil && deadline.remaining > 0 {
            Thread.sleep(forTimeInterval: min(0.05, max(0.001, deadline.remaining)))
        }
    } while ProcessInfo.processInfo.systemUptime < quietUntil && deadline.remaining > 0
    guard let last else { throw HelperError("Could not observe subscription fixture after protocol activation") }
    return last
}

func macOSPerformanceURL(_ source: String, group: String, index: Int) throws -> String {
    guard var components = URLComponents(string: source), components.scheme?.lowercased() == "https" else {
        throw HelperError("Performance subscription URL is invalid")
    }
    var queryItems = components.queryItems ?? []
    queryItems.append(URLQueryItem(
        name: "dobby-macos-performance",
        value: "\(group)-\(index)-\(UUID().uuidString.lowercased())"
    ))
    components.queryItems = queryItems
    guard let url = components.string else { throw HelperError("Could not construct a unique performance subscription URL") }
    return url
}

func macOSSubscriptionPerformanceMeasurements(
    _ owned: MacOSOwnedLogsApp, source: String, request: [String: Any], fixture: MacOSLogsFixture,
    deadline: MacOSLogsCaseDeadline
) throws -> [String: Any] {
    let warmupCount = 5
    let sampleCount = 30
    var warmups = [[String: Any]]()
    var samples = [[String: Any]]()
    var sampleDurations = [Double]()
    var maximumFixtureControlRoundTripMilliseconds: Int64 = 0
    let initial = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
    var expectedCount = try macOSSubscriptionStatsInteger(initial, "subscription_gets")
    maximumFixtureControlRoundTripMilliseconds = initial.roundTripMilliseconds

    for overallIndex in 0..<(warmupCount + sampleCount) {
        try deadline.check("subscription performance sample")
        let isWarmup = overallIndex < warmupCount
        let index = isWarmup ? overallIndex : overallIndex - warmupCount
        let group = isWarmup ? "warmup" : "sample"
        let sampleURL = try macOSPerformanceURL(source, group: group, index: index)
        let anchor = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
        maximumFixtureControlRoundTripMilliseconds = max(
            maximumFixtureControlRoundTripMilliseconds, anchor.roundTripMilliseconds
        )
        let countBefore = try macOSSubscriptionStatsInteger(anchor, "subscription_gets")
        try require(countBefore == expectedCount,
                    "Subscription fixture request count changed outside the serial Paste samples")
        let appView = try macOSLogsView(owned)
        var invokedAtMonotonicUptime: TimeInterval?
        _ = try pasteValueWithNativeControl(
            owned.application, window: appView.nodes[0], value: sampleURL, expectedSource: sampleURL,
            onInvocation: { invokedAtMonotonicUptime = ProcessInfo.processInfo.systemUptime }
        )
        guard let invokedAtMonotonicUptime else {
            throw HelperError("Performance sample did not capture the native Paste invocation")
        }
        let configureResponse = try waitMacOSConfigureResponse(
            fixture: fixture, source: sampleURL, deadline: deadline
        )
        let accepted = try waitMacOSForAcceptedInventory(owned, source: sampleURL, minimumRows: 1, deadline: deadline)
        guard let actionID = accepted.rows.first?["action_automation_id"] as? String,
              !actionID.isEmpty else {
            throw HelperError("Accepted performance inventory omitted its digest-scoped profile action ID")
        }
        try waitMacOSForEnabledProfileAction(
            owned, source: sampleURL, automationID: actionID, deadline: deadline
        )
        let completedAtMonotonicUptime = ProcessInfo.processInfo.systemUptime
        try require(configureResponse.monotonicUptime >= invokedAtMonotonicUptime &&
                    configureResponse.monotonicUptime <= completedAtMonotonicUptime,
                    "Configure response and rendered-action timestamps did not follow the Paste invocation")
        let elapsedMilliseconds = (completedAtMonotonicUptime - invokedAtMonotonicUptime) * 1_000
        let responseToEnabledActionMilliseconds =
            (completedAtMonotonicUptime - configureResponse.monotonicUptime) * 1_000
        let completed = try waitMacOSSubscriptionRequest(request: request, deadline: deadline, count: countBefore + 1)
        maximumFixtureControlRoundTripMilliseconds = max(
            maximumFixtureControlRoundTripMilliseconds, completed.roundTripMilliseconds
        )
        expectedCount = countBefore + 1
        let rowIDsValid = macOSProfileSemanticIDsAreConsistent(accepted.rows)
        try require(rowIDsValid, "Accepted profile rows did not retain digest-scoped semantic IDs")
        let status = try macOSDisconnectedStatus(accepted.nodes)
        try require(status == "Disconnected", "Paste performance changed the session state: \(status)")

        let timing: [String: Any] = [
            "index": index,
            "action": "paste",
            "elapsed_milliseconds": elapsedMilliseconds,
            "configure_response_to_enabled_action_milliseconds": responseToEnabledActionMilliseconds,
            "paste_invoked_monotonic_uptime": invokedAtMonotonicUptime,
            "configure_response_monotonic_uptime": configureResponse.monotonicUptime,
            "enabled_action_observed_monotonic_uptime": completedAtMonotonicUptime,
            "configure_response_diagnostic_id": configureResponse.diagnosticID,
            "enabled_profile_action_automation_id": actionID,
            "fixture_control_round_trip_ms_before": anchor.roundTripMilliseconds,
            "fixture_control_round_trip_ms_after": completed.roundTripMilliseconds,
        ]
        if isWarmup { warmups.append(timing) }
        else {
            samples.append(timing)
            sampleDurations.append(elapsedMilliseconds)
        }
    }
    try require(warmups.count == warmupCount && samples.count == sampleCount && sampleDurations.count == sampleCount,
                "Performance report did not retain the fixed warmup and sample sets")
    let sortedDurations = sampleDurations.sorted()
    let median = (sortedDurations[14] + sortedDurations[15]) / 2
    let summary: [String: Any] = [
        "median_milliseconds": median,
        "p95_milliseconds": sortedDurations[28],
        "maximum_milliseconds": sortedDurations[29],
    ]
    return [
        "action": "paste", "warmup_count": warmupCount, "sample_count": sampleCount,
        "measurement_boundary": "Rendered Paste invocation to successful Configure response and enabled digest-scoped profile action",
        "monotonic_clock": "ProcessInfo.systemUptime shared by the app and native helper",
        "fixture_clock_uncertainty": "Fixture wall-clock timestamps are not used in the measured interval; no cross-host clock synchronization is assumed",
        "maximum_fixture_control_round_trip_ms": maximumFixtureControlRoundTripMilliseconds,
        "reference_ms": 1_500, "blocking_gate": false,
        "summary": summary, "warmups": warmups, "samples": samples,
    ]
}

func macOSFixtureMarkers(caseID: String) -> [String] {
    guard caseID.hasPrefix("logs-") else { return [] }
    let prefix = "dobby-macos-\(caseID)-\(UUID().uuidString.lowercased())"
    if caseID == "logs-freeze-resume" {
        return (0..<120).map { "\(prefix)-row-\(String(format: "%03d", $0))-\(String(repeating: "x", count: 72))" }
    }
    return ["\(prefix)-before-clear"]
}

func assertMacOSFixtureBytes(_ fixture: MacOSLogsFixture, original: Data,
                             expectedRecords: [DiagnosticFiles.Entry],
                             assertions: inout [[String: Any]]) throws {
    let current = try Data(contentsOf: fixture.backend)
    let prefixMatches = current.count >= original.count && current.prefix(original.count).elementsEqual(original)
    try macOSLogsAssertion(&assertions, id: "clear-preserves-original-fixture-bytes", expected: true, actual: prefixMatches)
    for entry in expectedRecords {
        let exactRecordExists = current.range(of: Data(entry.raw.utf8)) != nil
        try macOSLogsAssertion(&assertions, id: "fixture-retains-record-\(entry.id)", expected: true, actual: exactRecordExists)
    }
}

func runMacOSConnectedSessionCase(
    request: [String: Any], candidateApp: MacOSCandidateApp, fixture: MacOSLogsFixture,
    subscriptionURL: String, deadline: MacOSLogsCaseDeadline, screenshotDirectory: String,
    activeApp: inout MacOSOwnedLogsApp?, launches: inout [MacOSOwnedLogsApp],
    assertions: inout [[String: Any]], screenshots: inout [String]
) throws -> [String: Any] {
    try require(request["observer_enabled"] as? Bool == true,
                "connected-session requires the duplex Go observer")
    guard let failureSource = request["subscription_failure_url"] as? String, !failureSource.isEmpty else {
        throw HelperError("connected-session requires subscription_failure_url")
    }
    let failureURL = try macOSNativeUniqueAttemptURL(failureSource)
    let observer: MacOSNativeObserver = { operation, timeoutSeconds, phase in
        try macOSNativeObserverCall(operation, timeoutSeconds: timeoutSeconds, deadline: deadline, phase: phase)
    }
    let initialFixture = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
    let initialCount = try macOSSubscriptionStatsInteger(initialFixture, "subscription_gets")
    try macOSNativeAssertion(&assertions, id: "connected-session-fixture-starts-empty", expected: 0,
                             actual: initialCount)
    try require(initialCount == 0, "Connected-session fixture must start with zero GETs")

    let firstView = try macOSLogsView(activeApp!)
    _ = try pasteValueWithNativeControl(activeApp!.application, window: firstView.nodes[0],
                                        value: subscriptionURL, expectedSource: subscriptionURL)
    let firstInventory = try waitMacOSForAcceptedInventory(
        activeApp!, source: subscriptionURL, minimumRows: 2, deadline: deadline
    )
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: initialCount + 1)
    let configured = try observer("snapshot", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "connected-session-native-paste-accepts-inventory",
                             expected: true,
                             actual: firstInventory.rows.count >= 2 &&
                                macOSProfileSemanticIDsAreConsistent(firstInventory.rows) &&
                                macOSNativeSnapshotValue(configured, "source_url") == subscriptionURL)
    try macOSNativeAssertion(&assertions, id: "connected-session-status-starts-disconnected",
                             expected: "Disconnected", actual: try macOSDisconnectedStatus(firstInventory.nodes))
    try macOSLogsCaseScreenshot(activeApp!, name: "connected-configured", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    let prepared = try observer("prepare-connect", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "connected-session-observer-prepares-native-connect",
                             expected: true, actual: prepared["prepared"] ?? NSNull())
    try macOSNativePressButton(firstInventory.nodes, identifier: "VPN connection action")
    let auto = try waitMacOSNativeSnapshot(deadline: deadline, stage: "native Auto connection",
                                           observer: observer) {
        macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "active_mode") == "AUTO_SELECT" &&
            ($0["active_profile"] as? [String: Any]) != nil
    }
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Connected", deadline: deadline)
    let autoObservations = try observer("observe", min(90, deadline.remaining), "initial-auto")
    try macOSNativeAssertTraffic(autoObservations, phase: "initial-auto", assertions: &assertions)
    try macOSLogsCaseScreenshot(activeApp!, name: "connected-auto", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    guard let active = auto["active_profile"] as? [String: Any],
          let activeIndex = active["index"] as? Int,
          let digest = macOSNativeSnapshotValue(auto, "digest"), !digest.isEmpty,
          firstInventory.rows.count >= 2,
          firstInventory.rows.allSatisfy({ $0["profile_digest"] as? String == digest }) else {
        throw HelperError("Auto connection did not match the accepted digest-scoped profile inventory")
    }
    let cancelIndex = activeIndex == 1 ? 0 : 1
    let cancelID = "profile.\(digest).\(cancelIndex).action"
    let initialGeneration = macOSNativeSnapshotInteger(auto, "generation") ?? -1
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: cancelID)
    var renderedCancel = false
    while deadline.remaining > 0 {
        let state = try observer("snapshot", min(30, deadline.remaining), "profile-switch-cancel")
        if macOSNativeProfileTransitionMatches(state, index: cancelIndex),
           try macOSNativeRenderedProfileTransition(activeApp!, digest: digest, index: cancelIndex,
                                                    competingIndex: 1 - cancelIndex) {
            renderedCancel = true
            try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: cancelID)
            break
        }
        if macOSNativeSnapshotValue(state, "state") == "CONNECTED",
           (macOSNativeSnapshotInteger(state, "generation") ?? initialGeneration) > initialGeneration {
            throw HelperError("Selected profile connected before the rendered cancellation action was observed")
        }
        Thread.sleep(forTimeInterval: min(0.08, max(0.001, deadline.remaining)))
    }
    try macOSNativeAssertion(&assertions, id: "profile-switch-cancel-exposes-rendered-stop-without-competing-action",
                             expected: true, actual: renderedCancel)
    try require(renderedCancel, "Profile switch did not expose its enabled digest-scoped Stop action")
    let canceled = try waitMacOSNativeSnapshot(deadline: deadline, stage: "profile switch cancellation",
                                                observer: observer) {
        ["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue($0, "state") ?? "") &&
            macOSNativeSnapshotHasNoValue($0, "pending_target") &&
            macOSNativeSnapshotHasNoValue($0, "active_profile")
    }
    try macOSNativeAssertion(&assertions, id: "profile-switch-cancel-clears-pending-and-active-profile",
                             expected: true,
                             actual: macOSNativeSnapshotHasNoValue(canceled, "pending_target") &&
                                macOSNativeSnapshotHasNoValue(canceled, "active_profile"))
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Disconnected", deadline: deadline)
    try macOSLogsCaseScreenshot(activeApp!, name: "profile-switch-cancelled", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "profile.\(digest).1.action")
    var profileTwoStopSeen = false
    var profileTwo: [String: Any]?
    while deadline.remaining > 0 {
        let state = try observer("snapshot", min(30, deadline.remaining), "profile-two-connect")
        if macOSNativeProfileTransitionMatches(state, index: 1),
           try macOSNativeRenderedProfileTransition(activeApp!, digest: digest, index: 1, competingIndex: 0) {
            profileTwoStopSeen = true
        }
        if macOSNativeSnapshotValue(state, "state") == "CONNECTED",
           (macOSNativeSnapshotInteger(state, "generation") ?? -1) >
                (macOSNativeSnapshotInteger(canceled, "generation") ?? -1),
           macOSNativeSnapshotValue(state, "active_mode") == "PROFILE_INDEX",
           macOSNativeSnapshotInteger(state, "active_index") == 1,
           (state["active_profile"] as? [String: Any])?["index"] as? Int == 1 {
            profileTwo = state
            break
        }
        if macOSNativeSnapshotValue(state, "state") == "FAILED" {
            throw HelperError("Profile 2 native selection failed: \(state)")
        }
        Thread.sleep(forTimeInterval: min(0.08, max(0.001, deadline.remaining)))
    }
    guard let profileTwo else { throw HelperError("Profile 2 did not establish a new connected generation") }
    try macOSNativeAssertion(&assertions, id: "profile-two-selection-renders-stop-before-connected",
                             expected: true, actual: profileTwoStopSeen)
    try require(profileTwoStopSeen, "Profile 2 connected without an enabled rendered Stop transition")
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Connected", deadline: deadline)
    try macOSLogsCaseScreenshot(activeApp!, name: "profile-two-connected", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    let beforeFailure = try macOSSubscriptionStatsInteger(
        macOSSubscriptionFixtureObservation(request: request, deadline: deadline), "subscription_gets")
    try pasteValue(try macOSLogsView(activeApp!).view, value: failureURL)
    _ = try waitMacOSLogsCase(deadline, stage: "visible failed-load Retry control") { () throws -> Bool? in
        let nodes = try macOSLogsAXNodes(activeApp!)
        guard let retry = try? find(nodes, "subscription.retry"),
              try attribute(retry, kAXEnabledAttribute) as? Bool == true else { return nil }
        return true
    }
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: beforeFailure + 1)
    let failedState = try observer("snapshot", min(30, deadline.remaining), nil)
    let failedNodes = try macOSLogsAXNodes(activeApp!)
    let failedActions = try macOSNativeProfileActions(failedNodes)
    try macOSNativeAssertion(&assertions, id: "failed-load-retains-profile-two-active-generation",
                             expected: true,
                             actual: macOSNativeIsActiveGeneration(failedState, sameAs: profileTwo) &&
                                (failedState["active_profile"] as? [String: Any])?["index"] as? Int == 1)
    try macOSNativeAssertion(&assertions, id: "failed-load-keeps-retry-visible-and-session-connected",
                             expected: true,
                             actual: try macOSRenderedText(find(failedNodes, "connection.status"),
                                                           identifier: "connection.status") == "Connected" &&
                                failedActions.contains(where: { $0.enabled && $0.title == "Disconnect" }))
    let failedStats = try macOSNativeWaitForNoAdditionalGet(request, deadline: deadline, count: beforeFailure + 1)
    try macOSNativeAssertion(&assertions, id: "failed-load-does-not-automatically-retry",
                             expected: beforeFailure + 1,
                             actual: try macOSSubscriptionStatsInteger(failedStats, "subscription_gets"))
    try macOSLogsCaseScreenshot(activeApp!, name: "failed-load-retry-visible", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "subscription.retry")
    let retryCount = beforeFailure + 2
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: retryCount)
    let retriedInventory = try waitMacOSForAcceptedInventory(activeApp!, source: failureURL,
                                                              minimumRows: 2, deadline: deadline)
    let retried = try waitMacOSNativeSnapshot(deadline: deadline, stage: "successful same-URL Retry",
                                              observer: observer) {
        macOSNativeSnapshotValue($0, "source_url") == failureURL &&
            macOSNativeIsActiveGeneration($0, sameAs: profileTwo)
    }
    try macOSNativeAssertion(&assertions, id: "retry-reuses-failed-url-and-retains-active-generation",
                             expected: true,
                             actual: try label(find(retriedInventory.nodes, "Connection configuration", editor: true),
                                               kAXValueAttribute) == failureURL &&
                                macOSNativeIsActiveGeneration(retried, sameAs: profileTwo))
    let afterRetryQuiet = try macOSNativeWaitForNoAdditionalGet(request, deadline: deadline, count: retryCount)
    try macOSNativeAssertion(&assertions, id: "retry-makes-exactly-one-additional-request",
                             expected: retryCount,
                             actual: try macOSSubscriptionStatsInteger(afterRetryQuiet, "subscription_gets"))

    let warmURL = try macOSPerformanceURL(subscriptionURL, group: "connected-warm-import", index: 1)
    let beforeWarm = retryCount
    let warmApp = try openMacOSProtocolURL(try macOSImportURL(warmURL), candidate: candidateApp,
                                            deadline: deadline)
    try macOSNativeAssertion(&assertions, id: "warm-os-link-reuses-current-app-process",
                             expected: Int(activeApp!.pid), actual: Int(warmApp.processIdentifier))
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: beforeWarm + 1)
    let warmedInventory = try waitMacOSForAcceptedInventory(activeApp!, source: warmURL,
                                                            minimumRows: 2, deadline: deadline)
    let warmed = try waitMacOSNativeSnapshot(deadline: deadline, stage: "warm OS protocol import",
                                             observer: observer) {
        macOSNativeSnapshotValue($0, "source_url") == warmURL &&
            macOSNativeIsActiveGeneration($0, sameAs: profileTwo)
    }
    try macOSNativeAssertion(&assertions, id: "warm-os-link-preserves-active-generation-and-digest",
                             expected: true,
                             actual: macOSNativeIsActiveGeneration(warmed, sameAs: profileTwo) &&
                                macOSNativeSnapshotValue(warmed, "digest") == digest &&
                                macOSProfileSemanticIDsAreConsistent(warmedInventory.rows))
    let warmQuiet = try macOSNativeWaitForNoAdditionalGet(request, deadline: deadline, count: beforeWarm + 1)
    try macOSNativeAssertion(&assertions, id: "warm-os-link-performs-one-get",
                             expected: beforeWarm + 1,
                             actual: try macOSSubscriptionStatsInteger(warmQuiet, "subscription_gets"))
    try macOSLogsCaseScreenshot(activeApp!, name: "warm-import-connected", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let disconnected = try waitMacOSNativeSnapshot(deadline: deadline, stage: "native Disconnect cleanup",
                                                    observer: observer) {
        ["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue($0, "state") ?? "") &&
            macOSNativeSnapshotHasNoValue($0, "pending_target") &&
            macOSNativeSnapshotHasNoValue($0, "active_profile")
    }
    let cleanup = try observer("cleanup", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "native-disconnect-leaves-no-tunnel-and-verifies-cleanup",
                             expected: ["connected": false, "cleanup_verified": true],
                             actual: ["connected": cleanup["connected"] ?? NSNull(),
                                      "cleanup_verified": cleanup["cleanup_verified"] ?? NSNull()])
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Disconnected", deadline: deadline)
    try macOSNativeAssertion(&assertions, id: "native-disconnect-clears-active-selection",
                             expected: true,
                             actual: macOSNativeSnapshotHasNoValue(disconnected, "active_profile") &&
                                macOSNativeSnapshotHasNoValue(disconnected, "pending_target"))
    try macOSLogsCaseScreenshot(activeApp!, name: "disconnected-cleanup", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    guard let savedSource = macOSNativeSnapshotValue(warmed, "source_url"), !savedSource.isEmpty else {
        throw HelperError("Connected-session did not retain a saved subscription URL")
    }
    let countBeforeRestore = beforeWarm + 1
    try closeMacOSLogsApp(activeApp!, deadline: MacOSLogsCaseDeadline(min(10, deadline.remaining)))
    activeApp = nil
    let restart = try observer("restart-service", min(60, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "service-restart-verifies-process-loss",
                             expected: true, actual: restart["process_loss_verified"] ?? NSNull())
    let emptyInventory = try observer("snapshot", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "service-restart-keeps-saved-url-with-empty-inventory",
                             expected: true,
                             actual: macOSNativeSnapshotBoolean(emptyInventory, "configured") == false &&
                                ((emptyInventory["profiles"] as? [Any])?.isEmpty == true) &&
                                macOSNativeSnapshotValue(emptyInventory, "source_url") == savedSource)
    activeApp = try startMacOSLogsApp(candidate: candidateApp, fixture: fixture,
                                      caseID: request["case"] as? String ?? "connected-session", deadline: deadline,
                                      launches: &launches)
    _ = try waitMacOSForAcceptedInventory(activeApp!, source: savedSource, minimumRows: 2, deadline: deadline)
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: countBeforeRestore + 1)
    let restored = try waitMacOSNativeSnapshot(deadline: deadline, stage: "saved URL inventory restoration",
                                               observer: observer) {
        macOSNativeSnapshotBoolean($0, "configured") == true &&
            macOSNativeSnapshotValue($0, "source_url") == savedSource &&
            macOSNativeSnapshotHasNoValue($0, "active_profile") &&
            macOSNativeSnapshotHasNoValue($0, "pending_target") &&
            ["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue($0, "state") ?? "")
    }
    try macOSNativeAssertion(&assertions, id: "reopen-restores-saved-inventory-while-idle",
                             expected: true,
                             actual: macOSNativeSnapshotBoolean(restored, "configured") == true &&
                                macOSNativeSnapshotHasNoValue(restored, "active_profile"))
    let restoreQuiet = try macOSNativeWaitForNoAdditionalGet(request, deadline: deadline,
                                                              count: countBeforeRestore + 1)
    try macOSNativeAssertion(&assertions, id: "saved-url-restoration-fetches-once",
                             expected: countBeforeRestore + 1,
                             actual: try macOSSubscriptionStatsInteger(restoreQuiet, "subscription_gets"))
    try macOSNativeAssertion(&assertions, id: "saved-url-restoration-renders-disconnected",
                             expected: "Disconnected",
                             actual: try macOSDisconnectedStatus(try macOSLogsAXNodes(activeApp!)))

    _ = try observer("prepare-connect", min(30, deadline.remaining), nil)
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let reconnected = try waitMacOSNativeSnapshot(deadline: deadline, stage: "native reconnect after inventory restore",
                                                   observer: observer) {
        macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "active_mode") == "AUTO_SELECT" &&
            ($0["active_profile"] as? [String: Any]) != nil
    }
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Connected", deadline: deadline)
    let reconnectObservations = try observer("observe", min(90, deadline.remaining), "restored-inventory-reconnect")
    try macOSNativeAssertTraffic(reconnectObservations, phase: "restored-inventory-reconnect", assertions: &assertions)
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "About")
    let about = try waitMacOSLogsCase(deadline, stage: "About metadata") { () -> (String, String)? in
        let nodes = try macOSLogsAXNodes(activeApp!)
        guard let version = try? macOSRenderedText(find(nodes, "About version metadata"), identifier: "About version metadata"),
              let sourceCommit = try? macOSRenderedText(find(nodes, "About source commit metadata"), identifier: "About source commit metadata"),
              version.hasPrefix("Version: "), sourceCommit.hasPrefix("Source commit: ") else { return nil }
        return (version, sourceCommit)
    }
    try macOSNativeAssertion(&assertions, id: "connected-session-about-version-and-source-commit-render",
                             expected: true,
                             actual: about.0.dropFirst("Version: ".count).count > 0 &&
                                about.1.dropFirst("Source commit: ".count).count > 0)
    if let sourceSHA = request["source_sha"] as? String, !sourceSHA.isEmpty {
        try macOSNativeAssertion(&assertions, id: "connected-session-about-source-matches-selected-revision",
                                 expected: "Source commit: \(sourceSHA)", actual: about.1)
    }
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "Done")
    try macOSLogsCaseScreenshot(activeApp!, name: "about-metadata", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    let coldURL = try macOSPerformanceURL(subscriptionURL, group: "connected-cold-import", index: 1)
    let beforeCold = countBeforeRestore + 1
    let connectedBeforeCold = try observer("snapshot", min(30, deadline.remaining), nil)
    try closeMacOSLogsApp(activeApp!, deadline: MacOSLogsCaseDeadline(min(10, deadline.remaining)))
    activeApp = nil
    activeApp = try startMacOSLogsApp(candidate: candidateApp, fixture: fixture,
                                      caseID: request["case"] as? String ?? "connected-session", deadline: deadline,
                                      launches: &launches, opening: [try macOSImportURL(coldURL)])
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: beforeCold + 1)
    let coldInventory = try waitMacOSForAcceptedInventory(activeApp!, source: coldURL, minimumRows: 2,
                                                           deadline: deadline)
    let coldConnected = try waitMacOSNativeSnapshot(deadline: deadline, stage: "cold OS import while connected",
                                                     observer: observer) {
        macOSNativeSnapshotValue($0, "source_url") == coldURL &&
            macOSNativeIsActiveGeneration($0, sameAs: connectedBeforeCold)
    }
    let coldQuiet = try macOSNativeWaitForNoAdditionalGet(request, deadline: deadline, count: beforeCold + 1)
    try macOSNativeAssertion(&assertions, id: "cold-os-link-reopens-and-imports-in-one-get",
                             expected: beforeCold + 1,
                             actual: try macOSSubscriptionStatsInteger(coldQuiet, "subscription_gets"))
    try macOSNativeAssertion(&assertions, id: "cold-os-link-preserves-connected-generation",
                             expected: true,
                             actual: macOSNativeIsActiveGeneration(coldConnected, sameAs: connectedBeforeCold) &&
                                macOSProfileSemanticIDsAreConsistent(coldInventory.rows))
    try macOSLogsCaseScreenshot(activeApp!, name: "cold-import-connected", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    try closeMacOSLogsApp(activeApp!, deadline: MacOSLogsCaseDeadline(min(10, deadline.remaining)))
    activeApp = nil
    activeApp = try startMacOSLogsApp(candidate: candidateApp, fixture: fixture,
                                      caseID: request["case"] as? String ?? "connected-session", deadline: deadline,
                                      launches: &launches)
    _ = try waitMacOSForAcceptedInventory(activeApp!, source: coldURL, minimumRows: 2, deadline: deadline)
    let reused = try waitMacOSNativeSnapshot(deadline: deadline, stage: "reopened accepted inventory reuse",
                                              observer: observer) {
        macOSNativeSnapshotValue($0, "source_url") == coldURL &&
            macOSNativeSnapshotValue($0, "digest") == macOSNativeSnapshotValue(coldConnected, "digest") &&
            macOSNativeIsActiveGeneration($0, sameAs: coldConnected)
    }
    let reusedQuiet = try macOSNativeWaitForNoAdditionalGet(request, deadline: deadline, count: beforeCold + 1)
    try macOSNativeAssertion(&assertions, id: "reopened-connected-inventory-does-not-refetch",
                             expected: beforeCold + 1,
                             actual: try macOSSubscriptionStatsInteger(reusedQuiet, "subscription_gets"))
    try macOSNativeAssertion(&assertions, id: "reopened-inventory-retains-active-generation-and-digest",
                             expected: true, actual: macOSNativeIsActiveGeneration(reused, sameAs: coldConnected))
    try macOSLogsCaseScreenshot(activeApp!, name: "reopened-inventory-reused", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    let loss = try observer("restart-service", min(60, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "connected-session-process-loss-is-verified",
                             expected: true, actual: loss["process_loss_verified"] ?? NSNull())
    _ = try observer("prepare-connect", min(30, deadline.remaining), nil)
    let beforeRecoveryConfigure = beforeCold + 1
    let recoveryView = try macOSLogsView(activeApp!)
    _ = try pasteValueWithNativeControl(activeApp!.application, window: recoveryView.nodes[0],
                                        value: subscriptionURL, expectedSource: subscriptionURL)
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: beforeRecoveryConfigure + 1)
    _ = try waitMacOSForAcceptedInventory(activeApp!, source: subscriptionURL, minimumRows: 2,
                                          deadline: deadline)
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let recovered = try waitMacOSNativeSnapshot(deadline: deadline, stage: "native UI process-loss reconnect",
                                                 observer: observer) {
        macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "active_mode") == "AUTO_SELECT" &&
            ($0["active_profile"] as? [String: Any]) != nil
    }
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Connected", deadline: deadline)
    let recoveryObservations = try observer("observe", min(90, deadline.remaining), "process-loss-reconnect")
    try macOSNativeAssertTraffic(recoveryObservations, phase: "process-loss-reconnect", assertions: &assertions)
    try macOSNativeAssertion(&assertions, id: "process-loss-reconnect-establishes-new-auto-generation",
                             expected: "AUTO_SELECT", actual: macOSNativeSnapshotValue(recovered, "active_mode") ?? "")
    try macOSLogsCaseScreenshot(activeApp!, name: "process-loss-reconnected", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    _ = try waitMacOSNativeSnapshot(deadline: deadline, stage: "final native Disconnect cleanup", observer: observer) {
        ["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue($0, "state") ?? "") &&
            macOSNativeSnapshotHasNoValue($0, "active_profile") &&
            macOSNativeSnapshotHasNoValue($0, "pending_target")
    }
    let finalCleanup = try observer("cleanup", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "final-disconnect-clears-tunnel-and-verifies-cleanup",
                             expected: ["connected": false, "cleanup_verified": true],
                             actual: ["connected": finalCleanup["connected"] ?? NSNull(),
                                      "cleanup_verified": finalCleanup["cleanup_verified"] ?? NSNull()])
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Disconnected", deadline: deadline)
    try macOSLogsCaseScreenshot(activeApp!, name: "final-disconnected", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)
    return ["initial_generation": initialGeneration,
            "profile_two_generation": macOSNativeSnapshotInteger(profileTwo, "generation") ?? -1,
            "restored_reconnect_generation": macOSNativeSnapshotInteger(reconnected, "generation") ?? -1,
            "process_loss_reconnect_generation": macOSNativeSnapshotInteger(recovered, "generation") ?? -1,
            "subscription_gets": try macOSSubscriptionStatsInteger(
                macOSSubscriptionFixtureObservation(request: request, deadline: deadline), "subscription_gets")]
}

func macOSSyntheticLayoutInventory() -> Data {
    let profiles = (0..<24).map { index in
        let description = index == 0 ? "" : "Layout profile \(index + 1)"
        return "[[Outline]]\nDescription = \"\(description)\"\nServer = \"127.0.0.1\"\nPort = 9\nPassword = \"never-connect-layout-\(index + 1)\""
    }
    return Data(profiles.joined(separator: "\n\n").utf8)
}

func runMacOSConnectedLayoutCase(
    request: [String: Any], candidateApp: MacOSCandidateApp, subscriptionURL: String, deadline: MacOSLogsCaseDeadline,
    screenshotDirectory: String, activeApp: inout MacOSOwnedLogsApp?,
    assertions: inout [[String: Any]], screenshots: inout [String]
) throws -> [String: Any] {
    try require(request["observer_enabled"] as? Bool == true,
                "connected-layout requires the duplex Go observer")
    guard let profilePath = request["subscription_profile_path"] as? String, !profilePath.isEmpty else {
        throw HelperError("connected-layout requires the source profile path for exact inventory restoration")
    }
    let originalBytes = try Data(contentsOf: URL(fileURLWithPath: profilePath))
    try require(!originalBytes.isEmpty && originalBytes.count <= 1 << 20,
                "connected-layout source profile must contain 1 byte through 1 MiB")
    let observer: MacOSNativeObserver = { operation, timeoutSeconds, phase in
        try macOSNativeObserverCall(operation, timeoutSeconds: timeoutSeconds, deadline: deadline, phase: phase)
    }
    let initialStats = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
    let initialCount = try macOSSubscriptionStatsInteger(initialStats, "subscription_gets")
    try macOSNativeAssertion(&assertions, id: "connected-layout-fixture-starts-empty",
                             expected: 0, actual: initialCount)
    try require(initialCount == 0, "Connected-layout fixture must start with zero GETs")

    let input = try macOSLogsView(activeApp!)
    _ = try pasteValueWithNativeControl(activeApp!.application, window: input.nodes[0],
                                        value: subscriptionURL, expectedSource: subscriptionURL)
    let originalInventory = try waitMacOSForAcceptedInventory(
        activeApp!, source: subscriptionURL, minimumRows: 2, deadline: deadline
    )
    _ = try macOSNativeWaitForSubscription(request, deadline: deadline, count: initialCount + 1)
    let originalSnapshot = try observer("snapshot", min(30, deadline.remaining), nil)
    guard let originalDigest = originalInventory.rows.first?["profile_digest"] as? String,
          !originalDigest.isEmpty,
          let originalProfiles = originalSnapshot["profiles"] as? [[String: Any]],
          !originalProfiles.isEmpty else {
        throw HelperError("Connected-layout Configure did not expose its accepted real profile inventory")
    }
    try require(originalProfiles.count >= 2 && originalInventory.rows.count >= 2,
                "Connected-layout requires at least two profiles in the original inventory")
    try macOSNativeAssertion(&assertions, id: "connected-layout-source-inventory-is-accepted",
                             expected: ["source_url": subscriptionURL,
                                        "digest": originalDigest,
                                        "profile_count": originalProfiles.count],
                             actual: ["source_url": macOSNativeSnapshotValue(originalSnapshot, "source_url") ?? "",
                                      "digest": macOSNativeSnapshotValue(originalSnapshot, "digest") ?? "",
                                      "profile_count": originalProfiles.count])
    try require(macOSNativeSnapshotValue(originalSnapshot, "source_url") == subscriptionURL &&
                macOSNativeSnapshotValue(originalSnapshot, "digest") == originalDigest,
                "Go snapshot and rendered native profile inventory did not agree before connecting")
    try macOSNativeAssertion(&assertions, id: "connected-layout-source-profile-controls-use-accepted-digest",
                             expected: true, actual: macOSProfileSemanticIDsAreConsistent(originalInventory.rows))
    try macOSLogsCaseScreenshot(activeApp!, name: "connected-layout-original-inventory",
                                directory: screenshotDirectory, deadline: deadline, screenshots: &screenshots)

    let prepared = try observer("prepare-connect", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "connected-layout-observer-prepares-native-connect",
                             expected: true, actual: prepared["prepared"] ?? NSNull())
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let connected = try waitMacOSNativeSnapshot(deadline: deadline, stage: "connected layout Auto session",
                                                 observer: observer) {
        macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "active_mode") == "AUTO_SELECT" &&
            ($0["active_profile"] as? [String: Any]) != nil &&
            macOSNativeSnapshotValue($0, "digest") == originalDigest
    }
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Connected", deadline: deadline)
    let traffic = try observer("observe", min(90, deadline.remaining), "connected-layout-before-inventory-change")
    try macOSNativeAssertTraffic(traffic, phase: "connected-layout-before-inventory-change", assertions: &assertions)
    let originalGeneration = macOSNativeSnapshotInteger(connected, "generation") ?? -1
    try macOSLogsCaseScreenshot(activeApp!, name: "connected-layout-before-replacement",
                                directory: screenshotDirectory, deadline: deadline, screenshots: &screenshots)

    let synthetic = macOSSyntheticLayoutInventory()
    try macOSReplaceSubscriptionFixtureProfile(request: request, content: synthetic, deadline: deadline)
    let layoutURL = try macOSURLAppendingQuery(subscriptionURL, name: "connected-layout", value: "24")
    let beforeLayout = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
    let beforeLayoutCount = try macOSSubscriptionStatsInteger(beforeLayout, "subscription_gets")
    let layoutOpen = try openMacOSProtocolURL(macOSImportURL(layoutURL), candidate: candidateApp, deadline: deadline)
    try macOSNativeAssertion(&assertions, id: "connected-layout-import-reuses-owned-native-process",
                             expected: Int(activeApp!.pid), actual: Int(layoutOpen.processIdentifier))
    let largeInventory = try waitMacOSForAcceptedInventory(
        activeApp!, source: layoutURL, minimumRows: 1, deadline: deadline
    )
    let largeRequest = try macOSNativeWaitForSubscription(request, deadline: deadline,
                                                         count: beforeLayoutCount + 1)
    let largeSnapshot = try waitMacOSNativeSnapshot(deadline: deadline, stage: "connected 24-profile inventory",
                                                     observer: observer) {
        macOSNativeSnapshotValue($0, "source_url") == layoutURL &&
            macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "digest") != originalDigest
    }
    guard let layoutDigest = largeInventory.rows.first?["profile_digest"] as? String,
          !layoutDigest.isEmpty,
          let layoutProfiles = largeSnapshot["profiles"] as? [[String: Any]] else {
        throw HelperError("Connected 24-profile inventory omitted its digest or Go profile inventory")
    }
    try macOSNativeAssertion(&assertions, id: "connected-layout-import-loads-all-24-source-profiles-once",
                             expected: ["profile_count": 24, "subscription_gets": beforeLayoutCount + 1],
                             actual: ["profile_count": layoutProfiles.count,
                                      "subscription_gets": try macOSSubscriptionStatsInteger(largeRequest, "subscription_gets")])
    try require(layoutProfiles.count == 24 && largeInventory.rows.count >= 1 &&
                macOSProfileSemanticIDsAreConsistent(largeInventory.rows),
                "Connected layout import did not expose the new 24-profile inventory and scoped IDs")
    let activeDigest = macOSNativeSnapshotValue(largeSnapshot, "active_digest") ?? ""
    try macOSNativeAssertion(&assertions, id: "connected-layout-inventory-digest-differs-from-active-digest",
                             expected: true, actual: !activeDigest.isEmpty && layoutDigest != activeDigest)
    try require(!activeDigest.isEmpty && layoutDigest != activeDigest,
                "Accepted replacement inventory must differ from the still-active profile digest")
    try macOSNativeAssertion(&assertions, id: "connected-layout-replacement-preserves-active-profile-and-generation",
                             expected: true, actual: try macOSNativeActiveProfileMatches(largeSnapshot, previous: connected))
    try require(try macOSNativeActiveProfileMatches(largeSnapshot, previous: connected),
                "Loading the long profile inventory changed the live active profile or generation")
    try macOSNativeAssertion(&assertions, id: "connected-layout-replacement-keeps-session-rendered-connected",
                             expected: "Connected",
                             actual: try macOSDisconnectedStatus(try macOSLogsAXNodes(activeApp!)))
    try require(try macOSDisconnectedStatus(try macOSLogsAXNodes(activeApp!)) == "Connected",
                "Connected 24-profile inventory did not preserve the rendered connection status")
    let layoutChecks = try macOSVerifyTwentyFourProfileLayout(
        activeApp!, digest: layoutDigest, caseID: "connected-layout", requireEnabledActions: true,
        deadline: deadline,
        screenshotDirectory: screenshotDirectory, assertions: &assertions, screenshots: &screenshots
    )

    try macOSReplaceSubscriptionFixtureProfile(request: request, content: originalBytes, deadline: deadline)
    let restoreURL = try macOSURLAppendingQuery(subscriptionURL, name: "connected-layout", value: "restore")
    let beforeRestore = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
    let beforeRestoreCount = try macOSSubscriptionStatsInteger(beforeRestore, "subscription_gets")
    let restoreOpen = try openMacOSProtocolURL(macOSImportURL(restoreURL), candidate: candidateApp, deadline: deadline)
    try macOSNativeAssertion(&assertions, id: "connected-layout-restore-reuses-owned-native-process",
                             expected: Int(activeApp!.pid), actual: Int(restoreOpen.processIdentifier))
    let restoredInventory = try waitMacOSForAcceptedInventory(
        activeApp!, source: restoreURL, minimumRows: 2, deadline: deadline
    )
    let restoredRequest = try macOSNativeWaitForSubscription(request, deadline: deadline,
                                                             count: beforeRestoreCount + 1)
    let restored = try waitMacOSNativeSnapshot(deadline: deadline, stage: "restored connected real inventory",
                                                observer: observer) {
        macOSNativeSnapshotValue($0, "source_url") == restoreURL &&
            macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "digest") == originalDigest
    }
    let restoredProfiles = restored["profiles"] as? [[String: Any]] ?? []
    try macOSNativeAssertion(&assertions, id: "connected-layout-restores-original-inventory-with-one-get",
                             expected: ["digest": originalDigest, "profile_count": originalProfiles.count,
                                        "subscription_gets": beforeRestoreCount + 1],
                             actual: ["digest": macOSNativeSnapshotValue(restored, "digest") ?? "",
                                      "profile_count": restoredProfiles.count,
                                      "subscription_gets": try macOSSubscriptionStatsInteger(restoredRequest, "subscription_gets")])
    try macOSNativeAssertion(&assertions, id: "connected-layout-restore-preserves-active-profile-and-generation",
                             expected: true, actual: try macOSNativeActiveProfileMatches(restored, previous: connected))
    try macOSNativeAssertion(&assertions, id: "connected-layout-restore-renders-original-profile-controls",
                             expected: true,
                             actual: restoredProfiles.count == originalProfiles.count &&
                                restoredInventory.rows.first?["profile_digest"] as? String == originalDigest &&
                                macOSProfileSemanticIDsAreConsistent(restoredInventory.rows))
    try macOSNativeAssertion(&assertions, id: "connected-layout-restore-remains-connected-in-native-window",
                             expected: "Connected",
                             actual: try macOSDisconnectedStatus(try macOSLogsAXNodes(activeApp!)))
    try macOSLogsCaseScreenshot(activeApp!, name: "connected-layout-restored-inventory",
                                directory: screenshotDirectory, deadline: deadline, screenshots: &screenshots)

    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let disconnected = try waitMacOSNativeSnapshot(deadline: deadline, stage: "connected-layout Disconnect cleanup",
                                                    observer: observer) {
        ["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue($0, "state") ?? "") &&
            macOSNativeSnapshotHasNoValue($0, "active_profile") &&
            macOSNativeSnapshotHasNoValue($0, "pending_target")
    }
    let cleanup = try observer("cleanup", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "connected-layout-final-disconnect-verifies-tunnel-cleanup",
                             expected: ["connected": false, "cleanup_verified": true],
                             actual: ["connected": cleanup["connected"] ?? NSNull(),
                                      "cleanup_verified": cleanup["cleanup_verified"] ?? NSNull()])
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Disconnected", deadline: deadline)
    try macOSLogsCaseScreenshot(activeApp!, name: "connected-layout-disconnected",
                                directory: screenshotDirectory, deadline: deadline, screenshots: &screenshots)
    return ["initial_generation": originalGeneration,
            "layout_generation": macOSNativeSnapshotInteger(largeSnapshot, "generation") ?? -1,
            "restored_generation": macOSNativeSnapshotInteger(restored, "generation") ?? -1,
            "final_generation": macOSNativeSnapshotInteger(disconnected, "generation") ?? -1,
            "original_digest": originalDigest, "layout_digest": layoutDigest,
            "layout_checks": layoutChecks,
            "subscription_gets": try macOSSubscriptionStatsInteger(
                macOSSubscriptionFixtureObservation(request: request, deadline: deadline), "subscription_gets")]
}

func runMacOSAutoRecoveryStopCase(
    request: [String: Any], subscriptionURL: String, deadline: MacOSLogsCaseDeadline,
    screenshotDirectory: String, activeApp: inout MacOSOwnedLogsApp?,
    assertions: inout [[String: Any]], screenshots: inout [String]
) throws -> [String: Any] {
    try require(request["observer_enabled"] as? Bool == true,
                "auto-recovery-stop requires the duplex Go observer")
    guard let marker = request["auto_recovery_marker"] as? String, !marker.isEmpty else {
        throw HelperError("auto-recovery-stop requires auto_recovery_marker")
    }
    try require(!FileManager.default.fileExists(atPath: marker),
                "Auto-recovery marker already exists before verified route and traffic")
    let observer: MacOSNativeObserver = { operation, timeoutSeconds, phase in
        try macOSNativeObserverCall(operation, timeoutSeconds: timeoutSeconds, deadline: deadline, phase: phase)
    }
    let startView = try macOSLogsView(activeApp!)
    _ = try pasteValueWithNativeControl(activeApp!.application, window: startView.nodes[0],
                                        value: subscriptionURL, expectedSource: subscriptionURL)
    _ = try waitMacOSForAcceptedInventory(activeApp!, source: subscriptionURL, minimumRows: 2,
                                          deadline: deadline)
    let configuredCount = try macOSSubscriptionStatsInteger(
        macOSSubscriptionFixtureObservation(request: request, deadline: deadline), "subscription_gets")
    try macOSNativeAssertion(&assertions, id: "recovery-stop-configure-makes-one-request",
                             expected: 1, actual: configuredCount)
    try require(configuredCount == 1, "Auto-recovery fixture was not loaded exactly once")
    let prepared = try observer("prepare-connect", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "recovery-stop-observer-prepares-native-connect",
                             expected: true, actual: prepared["prepared"] ?? NSNull())
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let initial = try waitMacOSNativeSnapshot(deadline: deadline, stage: "Auto connected before recovery marker",
                                               observer: observer) {
        macOSNativeSnapshotValue($0, "state") == "CONNECTED" &&
            macOSNativeSnapshotValue($0, "active_mode") == "AUTO_SELECT" &&
            ($0["active_profile"] as? [String: Any]) != nil
    }
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Connected", deadline: deadline)
    let traffic = try observer("observe", min(90, deadline.remaining), "auto-before-recovery-marker")
    try macOSNativeAssertTraffic(traffic, phase: "auto-before-recovery-marker", assertions: &assertions)
    try macOSLogsCaseScreenshot(activeApp!, name: "auto-recovery-connected", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)

    try FileManager.default.createDirectory(at: URL(fileURLWithPath: marker).deletingLastPathComponent(),
                                            withIntermediateDirectories: true)
    try Data("armed after real route and traffic\n".utf8)
        .write(to: URL(fileURLWithPath: marker), options: .withoutOverwriting)
    let initialGeneration = macOSNativeSnapshotInteger(initial, "generation") ?? -1
    let recovering = try waitMacOSNativeSnapshot(deadline: deadline, stage: "subsequent Auto recovery generation",
                                                  observer: observer) {
        macOSNativeSnapshotBoolean($0, "recovering") == true &&
            (macOSNativeSnapshotInteger($0, "generation") ?? initialGeneration) > initialGeneration
    }
    let recoveryGeneration = macOSNativeSnapshotInteger(recovering, "generation") ?? -1
    var stopRendered = false
    _ = try waitMacOSLogsCase(deadline, stage: "Auto recovery enabled Stop and disabled profile actions") { () throws -> Bool? in
        let nodes = try macOSLogsAXNodes(activeApp!)
        let main = try verifiedConnectionAction(nodes, identifier: "VPN connection action")
        let mainIsStop = try names(main).contains("Stop")
        let profileActions = try macOSNativeProfileActions(nodes)
        guard mainIsStop && profileActions.allSatisfy({ !$0.enabled }) else { return nil }
        stopRendered = true
        return true
    }
    try macOSNativeAssertion(&assertions, id: "auto-recovery-renders-enabled-stop-with-no-competing-profile-actions",
                             expected: true, actual: stopRendered)
    try macOSLogsCaseScreenshot(activeApp!, name: "auto-recovery-stop", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)
    try macOSNativePressButton(try macOSLogsAXNodes(activeApp!), identifier: "VPN connection action")
    let stopped = try waitMacOSNativeSnapshot(deadline: deadline, stage: "recovery Stop settles and cleans up",
                                               observer: observer) {
        ["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue($0, "state") ?? "") &&
            macOSNativeSnapshotBoolean($0, "cleanup_complete") == true &&
            macOSNativeSnapshotBoolean($0, "recovering") == false &&
            macOSNativeSnapshotHasNoValue($0, "pending_target") &&
            macOSNativeSnapshotHasNoValue($0, "active_profile")
    }
    _ = try macOSNativeWaitForStatus(activeApp!, expected: "Disconnected", deadline: deadline)
    let cleanup = try observer("cleanup", min(30, deadline.remaining), nil)
    try macOSNativeAssertion(&assertions, id: "auto-recovery-stop-verifies-no-tunnel-and-cleanup",
                             expected: ["connected": false, "cleanup_verified": true],
                             actual: ["connected": cleanup["connected"] ?? NSNull(),
                                      "cleanup_verified": cleanup["cleanup_verified"] ?? NSNull()])
    let stoppedGeneration = macOSNativeSnapshotInteger(stopped, "generation") ?? -1
    let stableUntil = ProcessInfo.processInfo.systemUptime + 3
    var stable = true
    while ProcessInfo.processInfo.systemUptime < stableUntil {
        let current = try observer("snapshot", min(10, max(0.1, stableUntil - ProcessInfo.processInfo.systemUptime)),
                                   "recovery-stop-stability")
        if macOSNativeSnapshotInteger(current, "generation") != stoppedGeneration ||
            macOSNativeSnapshotBoolean(current, "recovering") != false ||
            !macOSNativeSnapshotHasNoValue(current, "pending_target") ||
            !macOSNativeSnapshotHasNoValue(current, "active_profile") ||
            !["IDLE", "CONFIGURED"].contains(macOSNativeSnapshotValue(current, "state") ?? "") ||
            macOSNativeSnapshotBoolean(current, "cleanup_complete") != true {
            stable = false
            break
        }
        Thread.sleep(forTimeInterval: min(0.1, max(0.001, stableUntil - ProcessInfo.processInfo.systemUptime)))
    }
    try macOSNativeAssertion(&assertions, id: "no-recovery-generation-resumes-after-stop", expected: true,
                             actual: stable)
    try require(stable, "Auto connection resumed after rendered recovery Stop")
    try macOSLogsCaseScreenshot(activeApp!, name: "auto-recovery-stopped", directory: screenshotDirectory,
                                deadline: deadline, screenshots: &screenshots)
    return ["initial_generation": initialGeneration, "recovery_generation": recoveryGeneration,
            "stopped_generation": stoppedGeneration]
}

func runMacOSNativeCase(_ request: [String: Any]) throws -> [String: Any] {
    guard let resultPath = request["result_path"] as? String, !resultPath.isEmpty else {
        throw HelperError("native-case request is missing result_path")
    }
    let caseID = request["case"] as? String ?? "unknown"
    var assertions = [[String: Any]]()
    var screenshots = [String]()
    var diagnosticErrors = [String]()
    var cleanupErrors = [String]()
    var launches = [MacOSOwnedLogsApp]()
    var activeApp: MacOSOwnedLogsApp?
    var result: [String: Any] = [
        "case_id": caseID, "outcome": "not-run", "assertions": [],
        "elapsed_seconds": 0.0, "setup_seconds": 0.0, "cleanup_seconds": 0.0,
        "cleanup_errors": [], "error": NSNull(), "screenshots": [],
        "diagnostic_errors": [], "app_processes": [],
    ]

    let overallStart = ProcessInfo.processInfo.systemUptime
    var setupComplete = false
    var originalFailure: String?
    var screenshotDirectory = ""
    var caseDeadline: MacOSLogsCaseDeadline?
    var fixtureEntries = [DiagnosticFiles.Entry]()
    var originalFixtureBytes = Data()
    var markers = [String]()
    var subscriptionURL = ""
    var fixtureStatsBefore: MacOSSubscriptionFixtureObservation?
    var performanceMeasurements: [String: Any]?
    do {
        guard let executable = request["executable"] as? String, !executable.isEmpty,
              let fixturePath = request["fixture_log"] as? String, !fixturePath.isEmpty,
              let requestedDirectory = request["screenshot_directory"] as? String, !requestedDirectory.isEmpty,
              let rawTimeout = request["timeout_seconds"] as? NSNumber else {
            throw HelperError("native-case request is missing executable, fixture_log, screenshot_directory, or timeout_seconds")
        }
        let timeout = rawTimeout.doubleValue
        try require(timeout.isFinite && timeout > 0 && timeout <= 600,
                    "macOS native-case timeout must be finite and between zero and 600 seconds")
        let deadline = MacOSLogsCaseDeadline(timeout)
        caseDeadline = deadline
        let supportedCases: Set<String> = [
            "logs-details", "logs-freeze-resume", "logs-clear", "logs-reopen-clear-boundary",
            "configure-acceptance", "paste-input", "imports-cold", "imports-warm",
            "profiles-layout", "about", "text-size-eligibility", "subscription-load-performance",
            "connected-session", "connected-layout", "auto-recovery-stop",
        ]
        try require(supportedCases.contains(caseID), "Unsupported macOS native-case: \(caseID)")
        let candidateApp = try MacOSCandidateApp(path: executable)
        try require(AXIsProcessTrusted(), "Accessibility permission unavailable for macOS native-case")
        try require(CGPreflightScreenCaptureAccess(), "Screen recording permission unavailable for macOS native-case")

        let subscriptionCases: Set<String> = [
            "configure-acceptance", "paste-input", "imports-cold", "imports-warm",
            "profiles-layout", "subscription-load-performance", "connected-session", "connected-layout",
            "auto-recovery-stop",
        ]
        if subscriptionCases.contains(caseID) {
            subscriptionURL = try requiredMacOSSubscriptionURL(request)
            fixtureStatsBefore = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
            try require(try macOSSubscriptionStatsInteger(fixtureStatsBefore!, "subscription_gets") == 0,
                        "macOS native subscription fixture must start with zero GETs")
        }

        let backend = URL(fileURLWithPath: fixturePath).standardizedFileURL
        let ui = URL(fileURLWithPath: fixturePath + ".ui.jsonl").standardizedFileURL
        let configuredFixture = MacOSLogsFixture(backend: backend, ui: ui)
        screenshotDirectory = URL(fileURLWithPath: requestedDirectory).standardizedFileURL.path
        for candidate in configuredFixture.paths + [configuredFixture.boundary] {
            for path in [candidate, URL(fileURLWithPath: candidate.path + ".previous"), URL(fileURLWithPath: candidate.path + ".lock")] {
                try require(!FileManager.default.fileExists(atPath: path.path),
                            "macOS native-case requires fresh fixture paths; existing path: \(path.path)")
            }
        }
        try FileManager.default.createDirectory(at: backend.deletingLastPathComponent(), withIntermediateDirectories: true)
        try FileManager.default.createDirectory(atPath: screenshotDirectory, withIntermediateDirectories: true)
        markers = macOSFixtureMarkers(caseID: caseID)
        if !markers.isEmpty {
            if caseID == "logs-freeze-resume" {
                for marker in markers { try configuredFixture.append(marker) }
            }
            else { try configuredFixture.append(markers[0]) }
            fixtureEntries = try markers.map { try configuredFixture.entry(message: $0) }
        } else {
            try require(FileManager.default.createFile(atPath: backend.path, contents: Data()),
                        "Could not create empty backend log fixture")
            try require(FileManager.default.createFile(atPath: ui.path, contents: Data()),
                        "Could not create empty UI log fixture")
        }
        originalFixtureBytes = try Data(contentsOf: backend)
        let openingURLs = caseID == "imports-cold" ? [try macOSImportURL(subscriptionURL)] : []
        activeApp = try startMacOSLogsApp(candidate: candidateApp,
                                         fixture: configuredFixture, caseID: caseID, deadline: deadline, launches: &launches,
                                         opening: openingURLs)
        if !markers.isEmpty {
            _ = try waitMacOSLogsCase(deadline, stage: "initial diagnostic fixture rendering") {
                let observed = try macOSLogsView(activeApp!)
                return markers.allSatisfy { observed.text.contains($0) } ? observed : nil
            }
        } else {
            _ = try waitMacOSLogsCase(deadline, stage: "initial native window rendering") { () throws -> Bool? in
                try macOSLogsWindowIsReady(activeApp!) ? true : nil
            }
        }
        result["setup_seconds"] = ProcessInfo.processInfo.systemUptime - overallStart
        result["fixture_log"] = backend.path
        result["fixture_ui_log"] = ui.path
        result["screenshot_directory"] = screenshotDirectory
        setupComplete = true

        switch caseID {
        case "connected-session":
            result["case_context"] = try runMacOSConnectedSessionCase(
                request: request, candidateApp: candidateApp, fixture: configuredFixture,
                subscriptionURL: subscriptionURL, deadline: deadline, screenshotDirectory: screenshotDirectory,
                activeApp: &activeApp, launches: &launches, assertions: &assertions, screenshots: &screenshots
            )

        case "connected-layout":
            result["case_context"] = try runMacOSConnectedLayoutCase(
                request: request, candidateApp: candidateApp, subscriptionURL: subscriptionURL, deadline: deadline,
                screenshotDirectory: screenshotDirectory, activeApp: &activeApp,
                assertions: &assertions, screenshots: &screenshots
            )

        case "auto-recovery-stop":
            result["case_context"] = try runMacOSAutoRecoveryStopCase(
                request: request, subscriptionURL: subscriptionURL, deadline: deadline,
                screenshotDirectory: screenshotDirectory, activeApp: &activeApp,
                assertions: &assertions, screenshots: &screenshots
            )

        case "logs-details":
            let entry = fixtureEntries[0]
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "details-discovery-start")
            let details = try waitMacOSLogsCase(deadline, stage: "stable Details link discovery") { () -> (AXUIElement, URL)? in
                let nodes = try macOSLogsAXNodes(activeApp!, includeTextAreaChildren: true)
                let link = try macOSLogDetailsLink(nodes, entry: entry)
                let raw = try attribute(link, kAXURLAttribute)
                let url = (raw as? URL) ?? ((raw as? String).flatMap(URL.init(string:)))
                return url.map { (link, $0) }
            }
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "details-discovery-complete")
            let controlID = macOSLogDetailsIdentifier(details.1) ?? ""
            try macOSLogsAssertion(&assertions, id: "details-link-stable-entry-identifier",
                                  expected: "log.details.\(entry.id)", actual: controlID)
            let accessibleName = try names(details.0).first(where: { $0 != details.1.absoluteString }) ?? ""
            try macOSLogsAssertion(&assertions, id: "details-link-accessible-name", expected: "Details", actual: accessibleName)
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "details-press-start")
            let pressCode = AXUIElementPerformAction(details.0, kAXPressAction as CFString)
            try require(pressCode == .success, "Native Details link AXPress failed: \(pressCode.rawValue)")
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "details-press-complete")
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "details-expansion-start")
            let expandedText = try waitMacOSLogsCase(deadline, stage: "original record Details expansion") {
                let value = try macOSLogsView(activeApp!).text
                return value.contains(entry.raw) ? value : nil
            }
            writeMacOSNativeCaseProgress(caseID: caseID, stage: "details-expansion-complete")
            let rawRange = (expandedText as NSString).range(of: entry.raw)
            let displayedRaw: Any = rawRange.location == NSNotFound
                ? NSNull() : (expandedText as NSString).substring(with: rawRange)
            try macOSLogsAssertion(&assertions, id: "details-expands-original-json-record", expected: entry.raw, actual: displayedRaw)
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-expanded", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "logs-freeze-resume":
            let initial = try macOSLogsView(activeApp!)
            let initialRange = try visibleCharacterRange(initial.view)
            try macOSLogsAssertion(&assertions, id: "initial-view-follows-tail", expected: true,
                                  actual: initialRange.range.location + initialRange.range.length >= initialRange.characterCount - 2)
            let frozenRange = try postMacOSLogsScroll(activeApp!, position: "top", deadline: deadline)
            try require(frozenRange.end < frozenRange.count, "Fixture did not overflow the native log viewport")
            let refreshInterval = 0.8
            try require(deadline.remaining > refreshInterval,
                        "Case deadline expired before a production log refresh could observe the frozen view")
            Thread.sleep(forTimeInterval: refreshInterval)
            let anchor = try macOSLogsView(activeApp!)
            let appendPrefix = markers[0].components(separatedBy: "-row-")[0]
            let appendedMarkers = ["\(appendPrefix)-append-a", "\(appendPrefix)-append-b"]
            for marker in appendedMarkers { try configuredFixture.append(marker) }
            let appended = try appendedMarkers.map { try configuredFixture.entry(message: $0) }
            try macOSLogsAssertion(&assertions, id: "scroll-pauses-live-log-view", expected: false,
                                  actual: appendedMarkers.contains { anchor.text.contains($0) })
            _ = try waitMacOSLogsCase(deadline, stage: "controlled app-side log polling") { () throws -> Bool? in
                if try configuredFixture.entries().contains(where: { $0.id == appended[1].id }) { return true }
                return nil
            }
            let frozen = try macOSLogsView(activeApp!)
            let frozenPosition = try visibleCharacterRange(frozen.view)
            try macOSLogsAssertion(&assertions, id: "frozen-view-holds-new-records", expected: false,
                                  actual: appendedMarkers.contains { frozen.text.contains($0) })
            try macOSLogsAssertion(&assertions, id: "frozen-reading-position-does-not-advance",
                                  expected: frozenRange.start, actual: frozenPosition.range.location)
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-frozen", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)
            let afterEventBytes = try Data(contentsOf: configuredFixture.backend)
            try require(afterEventBytes.count >= originalFixtureBytes.count &&
                        afterEventBytes.prefix(originalFixtureBytes.count).elementsEqual(originalFixtureBytes),
                        "Controlled producer changed original fixture bytes")
            let resumedRange = try postMacOSLogsScroll(activeApp!, position: "bottom", deadline: deadline)
            let resumed = try waitMacOSLogsCase(deadline, stage: "live log follow resume") {
                let value = try macOSLogsView(activeApp!).text
                return appendedMarkers.allSatisfy { value.contains($0) } ? value : nil
            }
            try macOSLogsAssertion(&assertions, id: "resumed-view-shows-concurrent-appends", expected: true,
                                  actual: appendedMarkers.allSatisfy(resumed.contains))
            try macOSLogsAssertion(&assertions, id: "resumed-view-follows-tail", expected: true,
                                  actual: resumedRange.end >= resumedRange.count - 2)
            try assertMacOSFixtureBytes(configuredFixture, original: originalFixtureBytes,
                                        expectedRecords: fixtureEntries + appended, assertions: &assertions)
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-resumed", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "logs-clear", "logs-reopen-clear-boundary":
            let before = fixtureEntries[0]
            let clearMarker = try macOSLogsView(activeApp!)
            try macOSLogsAssertion(&assertions, id: "pre-clear-record-is-rendered", expected: true,
                                  actual: clearMarker.text.contains(before.message))
            try activateMacOSLogsApp(activeApp!, caseID: caseID, deadline: deadline)
            let clearNodes = try macOSLogsAXNodes(activeApp!)
            let clear = try find(clearNodes, "logs.clear")
            try macOSLogsAssertion(&assertions, id: "clear-control-accessible-name", expected: "Clear",
                                  actual: try names(clear).first(where: { $0 != "logs.clear" }) ?? "")
            try press(clear)
            _ = try waitMacOSLogsCase(deadline, stage: "production Clear boundary") {
                let observed = try macOSLogsView(activeApp!)
                return observed.text.contains(before.message) ? nil : observed
            }
            let clearedEntries = try configuredFixture.entries()
            try macOSLogsAssertion(&assertions, id: "clear-hides-pre-clear-entry-by-production-identity",
                                  expected: false, actual: clearedEntries.contains(where: { $0.id == before.id }))
            let afterMarker = markers[0].replacingOccurrences(of: "-before-clear", with: "-after-clear")
            try configuredFixture.append(afterMarker)
            let after = try configuredFixture.entry(message: afterMarker)
            _ = try waitMacOSLogsCase(deadline, stage: "post-Clear record rendering") {
                let observed = try macOSLogsView(activeApp!)
                return observed.text.contains(after.message) ? observed : nil
            }
            let afterNodes = try macOSLogsAXNodes(activeApp!, includeTextAreaChildren: true)
            let afterLink = try macOSLogDetailsLink(afterNodes, entry: after)
            let afterURLValue = try attribute(afterLink, kAXURLAttribute)
            let afterURL = (afterURLValue as? URL) ?? ((afterURLValue as? String).flatMap(URL.init(string:)))
            try macOSLogsAssertion(&assertions, id: "post-clear-details-control-is-entry-scoped",
                                  expected: "log.details.\(after.id)",
                                  actual: afterURL.flatMap(macOSLogDetailsIdentifier) ?? "")

            if caseID == "logs-reopen-clear-boundary" {
                let previousPID = activeApp!.pid
                let previousIdentity = activeApp!.identity
                try closeMacOSLogsApp(activeApp!, deadline: deadline)
                activeApp = try startMacOSLogsApp(candidate: candidateApp,
                                                  fixture: configuredFixture, caseID: caseID, deadline: deadline,
                                                  launches: &launches)
                let reopened = try waitMacOSLogsCase(deadline, stage: "reopened clear-boundary rendering") {
                    let observed = try macOSLogsView(activeApp!)
                    return observed.text.contains(after.message) && !observed.text.contains(before.message) ? observed : nil
                }
                try macOSLogsAssertion(&assertions, id: "reopen-starts-new-os-process",
                                      expected: true,
                                      actual: activeApp!.pid != previousPID && activeApp!.identity != previousIdentity)
                try macOSLogsAssertion(&assertions, id: "pre-clear-record-stays-hidden-after-reopen",
                                      expected: false, actual: reopened.text.contains(before.message))
                try macOSLogsAssertion(&assertions, id: "post-clear-record-survives-reopen",
                                      expected: true, actual: reopened.text.contains(after.message))
            }
            try assertMacOSFixtureBytes(configuredFixture, original: originalFixtureBytes,
                                        expectedRecords: [before, after], assertions: &assertions)
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + (caseID == "logs-clear" ? "-post-append" : "-reopened"),
                                        directory: screenshotDirectory, deadline: deadline, screenshots: &screenshots)

        case "configure-acceptance", "paste-input":
            let before = fixtureStatsBefore!
            let beforeCount = try macOSSubscriptionStatsInteger(before, "subscription_gets")
            let appView = try macOSLogsView(activeApp!)
            let invokedAt: Int64?
            if caseID == "paste-input" {
                invokedAt = try pasteValueWithNativeControl(
                    activeApp!.application, window: appView.nodes[0], value: subscriptionURL,
                    expectedSource: subscriptionURL
                )
            } else {
                try pasteValue(find(appView.nodes, "Connection configuration", editor: true), value: subscriptionURL)
                invokedAt = nil
            }
            let accepted = try waitMacOSForAcceptedInventory(
                activeApp!, source: subscriptionURL, minimumRows: 1, deadline: deadline
            )
            let after = try waitMacOSSubscriptionRequest(
                request: request, deadline: deadline, count: beforeCount + 1
            )
            let acceptedCount = accepted.rows.count
            try macOSNativeAssertion(&assertions, id: "subscription-field-renders-requested-url",
                                     expected: subscriptionURL,
                                     actual: try label(find(accepted.nodes, "Connection configuration", editor: true), kAXValueAttribute))
            try macOSNativeAssertion(&assertions, id: "accepted-profile-inventory-is-nonempty",
                                     expected: true, actual: acceptedCount > 0)
            try macOSNativeAssertion(&assertions, id: "profile-controls-use-accepted-inventory-digest",
                                     expected: true, actual: macOSProfileSemanticIDsAreConsistent(accepted.rows))
            try macOSNativeAssertion(&assertions, id: "subscription-action-makes-one-profile-request",
                                     expected: beforeCount + 1,
                                     actual: try macOSSubscriptionStatsInteger(after, "subscription_gets"))
            try macOSNativeAssertion(&assertions, id: "accepted-inventory-keeps-session-disconnected",
                                     expected: "Disconnected", actual: try macOSDisconnectedStatus(accepted.nodes))
            if let invokedAt {
                try macOSNativeAssertion(&assertions, id: "paste-action-records-native-invocation-time",
                                         expected: true, actual: invokedAt > 0)
            }
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-accepted", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "imports-cold":
            let before = fixtureStatsBefore!
            let beforeCount = try macOSSubscriptionStatsInteger(before, "subscription_gets")
            let accepted = try waitMacOSForAcceptedInventory(
                activeApp!, source: subscriptionURL, minimumRows: 1, deadline: deadline
            )
            let after = try waitMacOSSubscriptionRequest(
                request: request, deadline: deadline, count: beforeCount + 1
            )
            try macOSNativeAssertion(&assertions, id: "cold-protocol-import-populates-subscription-field",
                                     expected: subscriptionURL,
                                     actual: try label(find(accepted.nodes, "Connection configuration", editor: true), kAXValueAttribute))
            try macOSNativeAssertion(&assertions, id: "cold-protocol-import-accepts-inventory",
                                     expected: true, actual: !accepted.rows.isEmpty)
            try macOSNativeAssertion(&assertions, id: "cold-protocol-import-uses-digest-scoped-profile-controls",
                                     expected: true, actual: macOSProfileSemanticIDsAreConsistent(accepted.rows))
            try macOSNativeAssertion(&assertions, id: "cold-protocol-import-makes-one-profile-request",
                                     expected: beforeCount + 1,
                                     actual: try macOSSubscriptionStatsInteger(after, "subscription_gets"))
            try macOSNativeAssertion(&assertions, id: "cold-protocol-import-keeps-session-disconnected",
                                     expected: "Disconnected", actual: try macOSDisconnectedStatus(accepted.nodes))
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-accepted", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "imports-warm":
            let before = fixtureStatsBefore!
            let beforeCount = try macOSSubscriptionStatsInteger(before, "subscription_gets")
            let appView = try macOSLogsView(activeApp!)
            try pasteValue(find(appView.nodes, "Connection configuration", editor: true), value: subscriptionURL)
            let configured = try waitMacOSForAcceptedInventory(
                activeApp!, source: subscriptionURL, minimumRows: 1, deadline: deadline
            )
            _ = try waitMacOSSubscriptionRequest(request: request, deadline: deadline, count: beforeCount + 1)
            let previousPID = activeApp!.pid
            let opened = try openMacOSProtocolURL(
                macOSImportURL(subscriptionURL),
                candidate: candidateApp,
                deadline: deadline
            )
            try require(opened.processIdentifier == previousPID,
                        "Warm protocol import did not reuse the existing candidate process")
            let reimported = try waitMacOSForAcceptedInventory(
                activeApp!, source: subscriptionURL, minimumRows: configured.rows.count, deadline: deadline
            )
            let after = try waitMacOSForNoAdditionalSubscriptionGet(
                request: request, deadline: deadline, expectedCount: beforeCount + 1
            )
            try macOSNativeAssertion(&assertions, id: "warm-protocol-import-reuses-one-candidate-process",
                                     expected: true,
                                     actual: opened.processIdentifier == previousPID && activeApp!.pid == previousPID)
            try macOSNativeAssertion(&assertions, id: "warm-protocol-import-preserves-accepted-profile-digest",
                                     expected: configured.rows[0]["profile_digest"] as? String ?? "",
                                     actual: reimported.rows[0]["profile_digest"] as? String ?? "")
            try macOSNativeAssertion(&assertions, id: "warm-protocol-import-does-not-refetch",
                                     expected: beforeCount + 1,
                                     actual: try macOSSubscriptionStatsInteger(after, "subscription_gets"))
            try macOSNativeAssertion(&assertions, id: "warm-protocol-import-retains-semantic-control-ids",
                                     expected: true, actual: macOSProfileSemanticIDsAreConsistent(reimported.rows))
            try macOSNativeAssertion(&assertions, id: "warm-protocol-import-keeps-session-disconnected",
                                     expected: "Disconnected", actual: try macOSDisconnectedStatus(reimported.nodes))
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-accepted", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "profiles-layout":
            let before = fixtureStatsBefore!
            let beforeCount = try macOSSubscriptionStatsInteger(before, "subscription_gets")
            let appView = try macOSLogsView(activeApp!)
            try pasteValue(find(appView.nodes, "Connection configuration", editor: true), value: subscriptionURL)
            let accepted = try waitMacOSForAcceptedInventory(
                activeApp!, source: subscriptionURL, minimumRows: 1, deadline: deadline
            )
            let after = try waitMacOSSubscriptionRequest(
                request: request, deadline: deadline, count: beforeCount + 1
            )
            guard let digest = accepted.rows.first?["profile_digest"] as? String, !digest.isEmpty else {
                throw HelperError("Accepted 24-row layout inventory omitted its authoritative digest")
            }
            try macOSNativeAssertion(&assertions, id: "layout-subscription-makes-one-profile-request",
                                     expected: beforeCount + 1,
                                     actual: try macOSSubscriptionStatsInteger(after, "subscription_gets"))
            try macOSNativeAssertion(&assertions, id: "profile-layout-keeps-session-disconnected",
                                     expected: "Disconnected", actual: try macOSDisconnectedStatus(accepted.nodes))

            result["case_context"] = try macOSVerifyTwentyFourProfileLayout(
                activeApp!, digest: digest, caseID: caseID, requireEnabledActions: true, deadline: deadline,
                screenshotDirectory: screenshotDirectory, assertions: &assertions,
                screenshots: &screenshots
            )

        case "about":
            let initialNodes = try macOSLogsAXNodes(activeApp!)
            try press(find(initialNodes, "About"))
            let about = try waitMacOSLogsCase(deadline, stage: "About sheet metadata rendering") { () -> [String: Any]? in
                let nodes = try macOSLogsAXNodes(activeApp!)
                guard let version = try? macOSRenderedText(find(nodes, "About version metadata"),
                                                           identifier: "About version metadata"),
                      let sourceCommit = try? macOSRenderedText(find(nodes, "About source commit metadata"),
                                                               identifier: "About source commit metadata"),
                      version.hasPrefix("Version: "), sourceCommit.hasPrefix("Source commit: ") else { return nil }
                return ["nodes": nodes, "version": version, "source_commit": sourceCommit]
            }
            try macOSNativeAssertion(&assertions, id: "about-version-metadata-is-rendered",
                                     expected: true, actual: !(about["version"] as? String ?? "").dropFirst("Version: ".count).isEmpty)
            let sourceCommit = about["source_commit"] as? String ?? ""
            if let sourceSHA = request["source_sha"] as? String, !sourceSHA.isEmpty {
                try macOSNativeAssertion(&assertions, id: "about-source-commit-matches-selected-source",
                                         expected: "Source commit: \(sourceSHA)", actual: sourceCommit)
            } else {
                try macOSNativeAssertion(&assertions, id: "about-source-commit-is-rendered",
                                         expected: true, actual: sourceCommit.count > "Source commit: ".count)
            }
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-metadata", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "text-size-eligibility":
            guard let processIdentity = activeApp!.identity else {
                throw HelperError("Installed app has no verified process identity for Text Size inspection")
            }
            let details = try inspectMacOSTextSize(
                candidateBundle: candidateApp,
                candidateApp: activeApp!.application, candidatePID: activeApp!.pid,
                candidateIdentity: processIdentity,
                reactivateCandidate: { try activateMacOSLogsApp(activeApp!, caseID: caseID, deadline: deadline) }
            )
            try macOSNativeAssertion(&assertions, id: "text-size-settings-inventory-is-complete",
                                     expected: true, actual: details["available"] as? Bool == true &&
                                        details["rows_complete"] as? Bool == true)
            try macOSNativeAssertion(&assertions, id: "installed-app-text-size-eligibility-is-established",
                                     expected: false, actual: details["eligible"] as? Bool ?? true)
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-observed", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        case "subscription-load-performance":
            let measurements = try macOSSubscriptionPerformanceMeasurements(
                activeApp!, source: subscriptionURL, request: request,
                fixture: configuredFixture, deadline: deadline
            )
            performanceMeasurements = measurements
            try macOSNativeAssertion(&assertions, id: "performance-warmup-and-sample-counts",
                                     expected: ["warmup_count": 5, "sample_count": 30],
                                     actual: ["warmup_count": measurements["warmup_count"] as? Int ?? 0,
                                              "sample_count": measurements["sample_count"] as? Int ?? 0])
            try macOSNativeAssertion(&assertions, id: "performance-reports-1500ms-reference-without-a-gate",
                                     expected: ["reference_ms": 1_500, "blocking_gate": false],
                                     actual: ["reference_ms": measurements["reference_ms"] as? Int ?? 0,
                                              "blocking_gate": measurements["blocking_gate"] as? Bool ?? true])
            try macOSNativeAssertion(
                &assertions, id: "performance-measures-paste-through-response-and-enabled-semantic-action",
                expected: "Rendered Paste invocation to successful Configure response and enabled digest-scoped profile action",
                actual: measurements["measurement_boundary"] as? String ?? ""
            )
            try macOSNativeAssertion(
                &assertions, id: "performance-does-not-assume-fixture-clock-synchronization",
                expected: "Fixture wall-clock timestamps are not used in the measured interval; no cross-host clock synchronization is assumed",
                actual: measurements["fixture_clock_uncertainty"] as? String ?? ""
            )
            let finalStats = try macOSSubscriptionFixtureObservation(request: request, deadline: deadline)
            let finalCount = try macOSSubscriptionStatsInteger(finalStats, "subscription_gets")
            try macOSNativeAssertion(&assertions, id: "performance-issues-exactly-35-serial-paste-requests",
                                     expected: 35, actual: finalCount)
            try macOSNativeAssertion(&assertions, id: "performance-has-no-concurrent-subscription-requests",
                                     expected: 1,
                                     actual: try macOSSubscriptionStatsInteger(finalStats, "max_in_flight_gets"))
            try macOSNativeAssertion(&assertions, id: "performance-finishes-with-no-in-flight-request",
                                     expected: 0,
                                     actual: try macOSSubscriptionStatsInteger(finalStats, "in_flight_gets"))
            try macOSLogsCaseScreenshot(activeApp!, name: caseID + "-summary", directory: screenshotDirectory,
                                        deadline: deadline, screenshots: &screenshots)

        default:
            throw HelperError("Unsupported macOS native-case: \(caseID)")
        }
        try require(!assertions.isEmpty && assertions.allSatisfy { $0["outcome"] as? String == "passed" },
                    "macOS native-case completed without all required assertions passing")
        result["outcome"] = "passed"
    } catch {
        originalFailure = String(reflecting: error)
        if !setupComplete { result["setup_seconds"] = ProcessInfo.processInfo.systemUptime - overallStart }
        result["outcome"] = "failed"
        result["error"] = originalFailure ?? "unknown case failure"
        if let activeApp, !screenshotDirectory.isEmpty,
           let deadline = caseDeadline, deadline.remaining > 0 {
            do {
                try macOSLogsCaseScreenshot(activeApp, name: caseID + "-failure", directory: screenshotDirectory,
                                            deadline: deadline, screenshots: &screenshots)
            } catch { diagnosticErrors.append("Failure screenshot: \(String(reflecting: error))") }
        }
    }

    let cleanupStart = ProcessInfo.processInfo.systemUptime
    let cleanupDeadline = MacOSLogsCaseDeadline(10)
    for owned in launches.reversed() {
        do { try closeMacOSLogsApp(owned, deadline: cleanupDeadline) }
        catch { cleanupErrors.append(String(reflecting: error)); owned.result["alive_after_case"] = true }
    }
    result["cleanup_seconds"] = ProcessInfo.processInfo.systemUptime - cleanupStart
    result["elapsed_seconds"] = ProcessInfo.processInfo.systemUptime - overallStart
    result["assertions"] = assertions
    result["screenshots"] = screenshots
    result["diagnostic_errors"] = diagnosticErrors
    result["cleanup_errors"] = cleanupErrors
    result["app_processes"] = launches.map(\.result)
    if let performanceMeasurements { result["measurements"] = performanceMeasurements }
    if !cleanupErrors.isEmpty {
        let cleanupFailure = "macOS native-case cleanup failed: \(cleanupErrors.joined(separator: "; "))"
        result["outcome"] = "failed"
        result["error"] = [originalFailure, cleanupFailure].compactMap { $0 }.joined(separator: "\n")
    }
    if (result["outcome"] as? String) == "passed" && (assertions.isEmpty || assertions.contains(where: { $0["outcome"] as? String != "passed" })) {
        result["outcome"] = "failed"
        result["error"] = "macOS native-case result has no passing assertions"
    }
    try writeMacOSLogsCase(result, to: resultPath)
    if (result["outcome"] as? String) != "passed" {
        FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]))
        FileHandle.standardOutput.write(Data([0x0A]))
        try require(false, "macOS native-case failed: \(result["error"] as? String ?? caseID)")
    }
    return result
}

func run() throws -> [String: Any] {
    guard let requestLine = readLine(strippingNewline: true),
          let data = requestLine.data(using: .utf8),
          let request = try JSONSerialization.jsonObject(with: data) as? [String: Any],
          let operation = request["operation"] as? String else {
        throw HelperError("Missing or invalid helper request line")
    }
    if operation == "preflight" {
        let helper = CommandLine.arguments[0]
        try require(AXIsProcessTrusted(), "Accessibility permission unavailable for native helper \(helper)")
        try require(CGPreflightScreenCaptureAccess(), "Screen recording permission unavailable for native helper \(helper)")
        return ["ready": true]
    }
    if operation == "native-case" { return try runMacOSNativeCase(request) }
    throw HelperError("Unknown native helper operation: \(operation)")
}

do {
    let result = try run()
    FileHandle.standardOutput.write(try JSONSerialization.data(withJSONObject: result, options: [.sortedKeys]))
    FileHandle.standardOutput.write(Data([10]))
} catch {
    FileHandle.standardError.write(Data("\(error)\n\(Thread.callStackSymbols.joined(separator: "\n"))\n".utf8))
    exit(1)
}
