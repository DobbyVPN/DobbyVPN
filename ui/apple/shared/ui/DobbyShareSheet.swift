#if os(iOS)
import SwiftUI
import UIKit

struct DobbyShareSheet: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: [url], applicationActivities: nil)
    }

    func updateUIViewController(_ controller: UIActivityViewController, context: Context) {}
}
#elseif os(macOS)
import AppKit
import SwiftUI

struct DobbyShareSheet: NSViewRepresentable {
    let url: URL
    @Environment(\.dismiss) private var dismiss

    func makeCoordinator() -> Coordinator { Coordinator { dismiss() } }

    func makeNSView(context: Context) -> NSView {
        let view = NSView(frame: NSRect(x: 0, y: 0, width: 240, height: 80))
        let picker = NSSharingServicePicker(items: [url])
        picker.delegate = context.coordinator
        context.coordinator.picker = picker
        DispatchQueue.main.async {
            picker.show(relativeTo: view.bounds, of: view, preferredEdge: .minY)
        }
        return view
    }

    func updateNSView(_ view: NSView, context: Context) {}

    final class Coordinator: NSObject, NSSharingServicePickerDelegate, NSSharingServiceDelegate {
        let finished: () -> Void
        var picker: NSSharingServicePicker?
        init(finished: @escaping () -> Void) { self.finished = finished }
        func sharingServicePicker(_ sharingServicePicker: NSSharingServicePicker, didChoose service: NSSharingService?) {
            if let service { service.delegate = self } else { finished() }
        }
        func sharingService(_ sharingService: NSSharingService, didShareItems items: [Any]) { finished() }
        func sharingService(_ sharingService: NSSharingService, didFailToShareItems items: [Any], error: Error) {
            NSAlert(error: error).runModal()
            finished()
        }
    }
}
#endif

#if os(iOS)
private struct DobbyTextLayoutCapture {
    let identifier: Int
    let characterIndex: Int
    let glyphIndex: Int
    let paragraphRange: NSRange
    let point: CGPoint
    let lineFragment: CGRect
    let lineUsed: CGRect
    let contentOffsetY: CGFloat
    let anchorViewportY: CGFloat
    let bounds: CGSize
    let textContainerSize: CGSize
}

private enum DobbyTextLayoutTrace {
    private static let maximumRecords = 16
    private static let testSessionDirectory: URL? = {
        guard let rawIdentifier = ProcessInfo.processInfo.environment[
            "DOBBY_SIMULATOR_TEST_LOG_DIRECTORY_ID"
        ], let identifier = UUID(uuidString: rawIdentifier) else { return nil }
        return FileManager.default.temporaryDirectory
            .appendingPathComponent("DobbyVPN-UI-\(identifier.uuidString)", isDirectory: true)
    }()

    static func canRecord(order: Int) -> Bool {
        order < maximumRecords && testSessionDirectory != nil
    }

    static func append(_ event: [String: Any], order: Int) -> Bool {
        guard canRecord(order: order) else { return false }
        guard let directory = testSessionDirectory else { return false }
        let path = directory.appendingPathComponent("textkit_layout.jsonl")

        do {
            try FileManager.default.createDirectory(
                at: directory, withIntermediateDirectories: true
            )
            var boundedEvent = event
            boundedEvent["sample_order"] = order
            let encoded = try JSONSerialization.data(withJSONObject: boundedEvent, options: [.sortedKeys])
            if !FileManager.default.fileExists(atPath: path.path) {
                guard FileManager.default.createFile(atPath: path.path, contents: nil) else {
                    return false
                }
            }
            let handle = try FileHandle(forWritingTo: path)
            defer { try? handle.close() }
            try handle.seekToEnd()
            try handle.write(contentsOf: encoded + Data([0x0a]))
            try handle.synchronize()
            return true
        } catch {
            let message = "Dobby text layout diagnostic write failed: \(String(reflecting: error))\n"
            try? FileHandle.standardError.write(contentsOf: Data(message.utf8))
            return false
        }
    }

    static func size(_ value: CGSize) -> [String: Double] {
        ["width": Double(value.width), "height": Double(value.height)]
    }

    static func point(_ value: CGPoint) -> [String: Double] {
        ["x": Double(value.x), "y": Double(value.y)]
    }

    static func rect(_ value: CGRect) -> [String: Double] {
        [
            "x": Double(value.origin.x), "y": Double(value.origin.y),
            "width": Double(value.size.width), "height": Double(value.size.height),
        ]
    }
}

private final class DobbyLogTextView: UITextView {
    var preservesReadingPosition = false
    private(set) var isRestoringReadingPosition = false
    private(set) var readingPositionRestoreCount = 0
    private var lastLayoutSize: CGSize?
    private var anchorCharacterIndex: Int?
    private var anchorViewportY: CGFloat?
    private var latestLayoutCapture: DobbyTextLayoutCapture?
    private var nextLayoutCaptureIdentifier = 0
    private var layoutTraceRecordCount = 0

    override func layoutSubviews() {
        super.layoutSubviews()
        let previousSize = lastLayoutSize
        let sizeChanged = lastLayoutSize.map {
            abs($0.width - bounds.width) > 0.5 || abs($0.height - bounds.height) > 0.5
        } ?? false
        lastLayoutSize = bounds.size
        if sizeChanged && preservesReadingPosition {
            restoreReadingPosition(from: previousSize)
        }
    }

    func updateFollowingAccessibilityHint(isFollowing: Bool) {
        accessibilityHint = isFollowing
            ? NSLocalizedString("Showing newest entries. Scroll up to pause automatic updates.", comment: "")
            : NSLocalizedString("New entries are held while you read earlier logs. Scroll to the bottom to resume.", comment: "")
    }

    func restoreScrollExtent(to height: CGFloat) {
        guard height.isFinite, height > contentSize.height + 1 else { return }
        contentSize = CGSize(width: contentSize.width, height: height)
    }

    func captureReadingPosition() {
        guard textStorage.length > 0 else { return }
        layoutManager.ensureLayout(for: textContainer)
        let point = CGPoint(
            x: textContainerInset.left + 1,
            y: contentOffset.y - textContainerInset.top + 1
        )
        let glyph = layoutManager.glyphIndex(for: point, in: textContainer)
        let character = min(layoutManager.characterIndexForGlyph(at: glyph), textStorage.length - 1)
        let line = layoutManager.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
        anchorCharacterIndex = character
        anchorViewportY = line.minY + textContainerInset.top - contentOffset.y
        guard DobbyTextLayoutTrace.canRecord(order: layoutTraceRecordCount) else { return }
        let lineUsed = layoutManager.lineFragmentUsedRect(forGlyphAt: glyph, effectiveRange: nil)
        let paragraphRange = (textStorage.string as NSString).paragraphRange(
            for: NSRange(location: character, length: 0)
        )
        nextLayoutCaptureIdentifier += 1
        latestLayoutCapture = DobbyTextLayoutCapture(
            identifier: nextLayoutCaptureIdentifier,
            characterIndex: character,
            glyphIndex: glyph,
            paragraphRange: paragraphRange,
            point: point,
            lineFragment: line,
            lineUsed: lineUsed,
            contentOffsetY: contentOffset.y,
            anchorViewportY: anchorViewportY ?? 0,
            bounds: bounds.size,
            textContainerSize: textContainer.size
        )
    }

    private func restoreReadingPosition(from previousSize: CGSize?) {
        guard let character = anchorCharacterIndex,
              let viewportY = anchorViewportY,
              textStorage.length > 0 else { return }
        layoutManager.ensureLayout(for: textContainer)
        let glyph = layoutManager.glyphIndexForCharacter(at: min(character, textStorage.length - 1))
        let line = layoutManager.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
        let traceEnabled = DobbyTextLayoutTrace.canRecord(order: layoutTraceRecordCount)
            && latestLayoutCapture != nil
        let lineUsed = traceEnabled
            ? layoutManager.lineFragmentUsedRect(forGlyphAt: glyph, effectiveRange: nil)
            : .zero
        // Keep the captured line's partially clipped top position across a size change.
        let minimumAnchorViewportY = -line.height
        let maximumAnchorViewportY = max(0, bounds.height - adjustedContentInset.bottom - line.height)
        let restoredAnchorViewportY = min(max(viewportY, minimumAnchorViewportY), maximumAnchorViewportY)
        let requestedY = line.minY + textContainerInset.top - restoredAnchorViewportY
        let maximumY = max(-adjustedContentInset.top, contentSize.height - bounds.height + adjustedContentInset.bottom)
        let restoredY = min(max(requestedY, -adjustedContentInset.top), maximumY)
        let contentOffsetBefore = contentOffset.y
        isRestoringReadingPosition = true
        setContentOffset(CGPoint(x: contentOffset.x, y: restoredY), animated: false)
        isRestoringReadingPosition = false
        readingPositionRestoreCount += 1

        if traceEnabled, let capture = latestLayoutCapture {
            let paragraph = (textStorage.string as NSString).substring(
                with: NSIntersectionRange(
                    capture.paragraphRange,
                    NSRange(location: 0, length: textStorage.length)
                )
            )
            let recordOrder = layoutTraceRecordCount
            layoutTraceRecordCount += 1
            _ = DobbyTextLayoutTrace.append([
                "restore_id": readingPositionRestoreCount,
                "size_before": DobbyTextLayoutTrace.size(previousSize ?? .zero),
                "size_after": DobbyTextLayoutTrace.size(bounds.size),
                "text_container_size": DobbyTextLayoutTrace.size(textContainer.size),
                "capture": [
                    "capture_id": capture.identifier,
                    "character_index": capture.characterIndex,
                    "glyph_index": capture.glyphIndex,
                    "paragraph_range_location": capture.paragraphRange.location,
                    "paragraph_range_length": capture.paragraphRange.length,
                    "logical_paragraph": paragraph,
                    "text_container_point": DobbyTextLayoutTrace.point(capture.point),
                    "line_fragment": DobbyTextLayoutTrace.rect(capture.lineFragment),
                    "line_used": DobbyTextLayoutTrace.rect(capture.lineUsed),
                    "content_offset_y": Double(capture.contentOffsetY),
                    "anchor_viewport_y": Double(capture.anchorViewportY),
                    "bounds": DobbyTextLayoutTrace.size(capture.bounds),
                    "text_container_size": DobbyTextLayoutTrace.size(capture.textContainerSize),
                ],
                "restore_character_index": character,
                "restore_line_fragment": DobbyTextLayoutTrace.rect(line),
                "restore_line_used": DobbyTextLayoutTrace.rect(lineUsed),
                "requested_offset_y": Double(requestedY),
                "anchor_viewport_y_after_bounds_clamp": Double(restoredAnchorViewportY),
                "content_offset_y_after_content_clamp": Double(restoredY),
                "content_offset_y_before": Double(contentOffsetBefore),
                "content_offset_y_after": Double(contentOffset.y),
                "content_size": DobbyTextLayoutTrace.size(contentSize),
                "adjusted_insets": [
                    "top": Double(adjustedContentInset.top),
                    "bottom": Double(adjustedContentInset.bottom),
                ],
            ], order: recordOrder)
        }
    }
}

struct DobbyLogView: UIViewRepresentable {
    let entries: [DobbyLogEntry]
    let clear: Int

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> UITextView {
        let view: DobbyLogTextView
        if #available(iOS 16.0, *) {
            // Reading-position anchoring below uses NSLayoutManager; select TextKit 1 before layout.
            view = DobbyLogTextView(usingTextLayoutManager: false)
        } else {
            view = DobbyLogTextView()
        }
        view.isEditable = false
        view.isSelectable = true
        view.backgroundColor = .secondarySystemBackground
        view.font = UIFont.preferredFont(forTextStyle: .caption1)
        view.adjustsFontForContentSizeCategory = true
        view.delegate = context.coordinator
        view.accessibilityIdentifier = "Connection logs"
        view.updateFollowingAccessibilityHint(isFollowing: true)
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        let coordinator = context.coordinator
        coordinator.latestEntries = entries
        let cleared = clear != coordinator.lastClear
        if cleared { coordinator.followingState.resetForClear() }
        if coordinator.followingState.isFollowing { coordinator.displayedEntries = entries }
        if let logView = view as? DobbyLogTextView {
            logView.preservesReadingPosition = !coordinator.followingState.isFollowing
            logView.updateFollowingAccessibilityHint(isFollowing: coordinator.followingState.isFollowing)
        }
        let restoreCountBeforeLayout = (view as? DobbyLogTextView)?.readingPositionRestoreCount
        let attributed = logText(coordinator.displayedEntries)
        let text = attributed.string
        coordinator.updating = true
        let offset = view.contentOffset
        let selection = view.selectedRange
        if view.text != text {
            view.attributedText = attributed
            view.selectedRange = NSRange(location: min(selection.location, view.textStorage.length), length: 0)
            if NSMaxRange(selection) <= view.textStorage.length { view.selectedRange = selection }
        }
        view.layoutIfNeeded()
        if let logView = view as? DobbyLogTextView {
            _ = coordinator.effectiveContentHeight(for: logView)
        }
        let layoutRestoredReadingPosition =
            restoreCountBeforeLayout != (view as? DobbyLogTextView)?.readingPositionRestoreCount
        if coordinator.followingState.isFollowing || cleared {
            view.scrollRangeToVisible(NSRange(location: view.textStorage.length, length: 0))
        } else if !layoutRestoredReadingPosition {
            // Keep the captured offset unless layoutSubviews already restored a newer character anchor.
            view.setContentOffset(offset, animated: false)
        }
        coordinator.lastClear = clear
        coordinator.updating = false
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        var lastClear = 0
        var followingState = DobbyLogFollowingState()
        var isUserDragging = false
        var updating = false
        var latestEntries: [DobbyLogEntry] = []
        var displayedEntries: [DobbyLogEntry] = []
        private var lastStableContentHeight: CGFloat?

        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            guard scrollView is DobbyLogTextView else { return }
            let gestureActive = isUserDragging || scrollView.isDragging || scrollView.isDecelerating
            guard gestureActive else { return }

            if !updating && (isUserDragging || scrollView.isDecelerating) {
                updateFollowingState(for: scrollView)
            }
        }
        func scrollViewWillBeginDragging(_ scrollView: UIScrollView) {
            guard scrollView is DobbyLogTextView else { return }
            isUserDragging = true
        }
        func scrollViewDidEndDragging(_ scrollView: UIScrollView, willDecelerate decelerate: Bool) {
            guard scrollView is DobbyLogTextView else { return }
            let accepted = isUserDragging && !updating
            isUserDragging = false
            if accepted { updateFollowingState(for: scrollView) }
        }
        func scrollViewDidEndDecelerating(_ scrollView: UIScrollView) {
            guard scrollView is DobbyLogTextView else { return }
            isUserDragging = false
            updateFollowingState(for: scrollView)
        }

        private func updateFollowingState(for scrollView: UIScrollView) {
            guard let logView = scrollView as? DobbyLogTextView,
                  !logView.isRestoringReadingPosition else { return }
            let contentHeight = effectiveContentHeight(for: logView)
            let atBottom = shouldFollowLogUpdates(
                viewportBottom: scrollView.contentOffset.y + scrollView.bounds.height
                    - scrollView.adjustedContentInset.bottom,
                contentHeight: contentHeight
            )
            let userGestureActive = isUserDragging || scrollView.isDragging
            let resumedFollowing = followingState.update(atBottom: atBottom, gestureActive: userGestureActive)
            logView.preservesReadingPosition = !followingState.isFollowing
            logView.updateFollowingAccessibilityHint(isFollowing: followingState.isFollowing)
            if resumedFollowing { displayLatestEntries(in: logView) }
            if !followingState.isFollowing { logView.captureReadingPosition() }
        }

        private func displayLatestEntries(in logView: DobbyLogTextView) {
            displayedEntries = latestEntries
            let attributed = logText(displayedEntries)
            let selection = logView.selectedRange
            updating = true
            if logView.text != attributed.string {
                logView.attributedText = attributed
                logView.selectedRange = NSRange(
                    location: min(selection.location, logView.textStorage.length), length: 0
                )
                if NSMaxRange(selection) <= logView.textStorage.length {
                    logView.selectedRange = selection
                }
            }
            logView.layoutIfNeeded()
            _ = effectiveContentHeight(for: logView)
            logView.scrollRangeToVisible(NSRange(location: logView.textStorage.length, length: 0))
            updating = false
        }

        fileprivate func effectiveContentHeight(for logView: DobbyLogTextView) -> CGFloat {
            let measured = logView.contentSize.height
            let emptyTextHeight = logView.textContainerInset.top + logView.textContainerInset.bottom
            if logView.textStorage.length == 0 || measured > emptyTextHeight + 1 {
                lastStableContentHeight = measured
                return measured
            }
            // UITextView can retain valid TextKit layout while its scroll extent
            // transiently collapses to the text-container insets. Restore the
            // extent from the laid-out text so a real drag can move the reader.
            logView.layoutManager.ensureLayout(for: logView.textContainer)
            let usedRect = logView.layoutManager.usedRect(for: logView.textContainer)
            let extraLineFragment = logView.layoutManager.extraLineFragmentRect
            let layoutHeight = max(usedRect.maxY, extraLineFragment.maxY)
            let restoredHeight = max(lastStableContentHeight ?? measured, ceil(layoutHeight))
            logView.restoreScrollExtent(to: restoredHeight)
            let repaired = logView.contentSize.height
            if repaired > emptyTextHeight + 1 {
                lastStableContentHeight = repaired
                return repaired
            }
            // Keep the last valid height for follow-state calculations if UIKit
            // still reports the collapsed extent after the repair attempt.
            return lastStableContentHeight ?? measured
        }
    }
}
#elseif os(macOS)
struct DobbyLogView: NSViewRepresentable {
    let entries: [DobbyLogEntry]
    @Binding var following: Bool
    let clear: Int

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        guard let view = scroll.documentView as? NSTextView else { return scroll }
        configureLogTextView(view)
        view.delegate = context.coordinator
        scroll.contentView.postsBoundsChangedNotifications = true
        let coordinator = context.coordinator
        coordinator.observers.append(NotificationCenter.default.addObserver(
            forName: NSScrollView.willStartLiveScrollNotification, object: scroll, queue: .main
        ) { [weak coordinator] _ in coordinator?.userScrolling = true })
        coordinator.observers.append(NotificationCenter.default.addObserver(
            forName: NSScrollView.didEndLiveScrollNotification, object: scroll, queue: .main
        ) { [weak coordinator] _ in coordinator?.userScrolling = false })
        coordinator.observers.append(NotificationCenter.default.addObserver(
            forName: NSView.boundsDidChangeNotification, object: scroll.contentView, queue: .main
        ) { [weak coordinator = context.coordinator, weak scroll] _ in
            guard let coordinator, let scroll, !coordinator.updating, coordinator.userScrolling else { return }
            let atBottom = shouldFollowLogUpdates(
                viewportBottom: scroll.contentView.bounds.maxY,
                contentHeight: scroll.documentView?.bounds.height ?? 0
            )
            if coordinator.parent.following != atBottom {
                DispatchQueue.main.async { coordinator.parent.following = atBottom }
            }
        })
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let view = scroll.documentView as? NSTextView, let storage = view.textStorage else { return }
        let coordinator = context.coordinator
        coordinator.parent = self
        if clear != coordinator.lastClear { resetLogFollowingForClear(following: $following) }
        guard following else { return }
        coordinator.entries = entries
        let attributed = logText(entries)
        let text = attributed.string
        coordinator.updating = true
        let origin = scroll.contentView.bounds.origin
        let selection = view.selectedRange()
        if view.string != text {
            storage.setAttributedString(attributed)
            if NSMaxRange(selection) <= storage.length { view.setSelectedRange(selection) }
        }
        if let container = view.textContainer { view.layoutManager?.ensureLayout(for: container) }
        if following || clear != coordinator.lastClear {
            view.scrollRangeToVisible(NSRange(location: storage.length, length: 0))
        } else {
            scroll.contentView.scroll(to: origin)
            scroll.reflectScrolledClipView(scroll.contentView)
        }
        coordinator.lastClear = clear
        coordinator.updating = false
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: DobbyLogView
        var lastClear = 0
        var updating = false
        var observers: [NSObjectProtocol] = []
        var userScrolling = false
        var entries: [DobbyLogEntry] = []
        init(_ parent: DobbyLogView) { self.parent = parent }
        deinit { for observer in observers { NotificationCenter.default.removeObserver(observer) } }
    }
}
#endif

func resetLogFollowingForClear(following: Binding<Bool>) {
    following.wrappedValue = true
}

func shouldFollowLogUpdates(viewportBottom: CGFloat, contentHeight: CGFloat) -> Bool {
    viewportBottom >= contentHeight - 24
}

struct DobbyLogFollowingState {
    private(set) var isFollowing = true

    mutating func update(atBottom: Bool, gestureActive: Bool) -> Bool {
        let shouldFollow = atBottom && (isFollowing || !gestureActive)
        let resumedFollowing = !isFollowing && shouldFollow
        isFollowing = shouldFollow
        return resumedFollowing
    }

    mutating func resetForClear() { isFollowing = true }
}

#if os(macOS)
func configureLogTextView(_ view: NSTextView) {
    view.isEditable = false
    view.isSelectable = true
    view.isRichText = true
    view.font = .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
    view.textColor = .textColor
    view.backgroundColor = .textBackgroundColor
    view.setAccessibilityIdentifier("Connection logs")
}
#endif

#if os(iOS)
private typealias LogColor = UIColor
private let logFont = UIFont.preferredFont(forTextStyle: .caption1)
private var normalLogColor: LogColor { .label }
private var mutedLogColor: LogColor { .secondaryLabel }
#elseif os(macOS)
private typealias LogColor = NSColor
private let logFont = NSFont.systemFont(ofSize: NSFont.smallSystemFontSize)
private var normalLogColor: LogColor { .textColor }
private var mutedLogColor: LogColor { .secondaryLabelColor }
#endif

func logText(_ entries: [DobbyLogEntry]) -> NSAttributedString {
    let output = NSMutableAttributedString(string: "")
    for entry in entries {
        let color: LogColor
        switch entry.level {
        case "ERROR", "FATAL", "PANIC": color = .systemRed
        case "WARN", "WARNING": color = .systemOrange
        case "DEBUG", "TRACE": color = mutedLogColor
        default: color = normalLogColor
        }
        let header = [entry.timestamp, entry.level, entry.source].filter { !$0.isEmpty }.joined(separator: " · ")
        output.append(NSAttributedString(string: header + "\n" + entry.message + "\n", attributes: [.font: logFont, .foregroundColor: color]))
    }
    return output
}
