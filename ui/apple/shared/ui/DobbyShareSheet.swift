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
private final class DobbyLogTextView: UITextView {
    var preservesReadingPosition = false
    private(set) var isRestoringReadingPosition = false
    private(set) var readingPositionRestoreCount = 0
    private var lastLayoutSize: CGSize?
    private var anchorCharacterIndex: Int?
    private var anchorViewportY: CGFloat?

    override func layoutSubviews() {
        super.layoutSubviews()
        let sizeChanged = lastLayoutSize.map {
            abs($0.width - bounds.width) > 0.5 || abs($0.height - bounds.height) > 0.5
        } ?? false
        lastLayoutSize = bounds.size
        if sizeChanged && preservesReadingPosition {
            restoreReadingPosition()
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
        let capturedAnchor: (character: Int, line: CGRect)
        if let detailsAnchor = firstVisibleDetailsAnchor() {
            capturedAnchor = (detailsAnchor.character, detailsAnchor.line)
        } else {
            let point = CGPoint(
                x: textContainerInset.left + 1,
                y: contentOffset.y + textContainerInset.top + 1
            )
            let glyph = layoutManager.glyphIndex(for: point, in: textContainer)
            let character = min(layoutManager.characterIndexForGlyph(at: glyph), textStorage.length - 1)
            let line = layoutManager.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
            capturedAnchor = (character, line)
        }
        let character = capturedAnchor.character
        let line = capturedAnchor.line
        anchorCharacterIndex = character
        anchorViewportY = line.minY + textContainerInset.top - contentOffset.y
    }

    private func firstVisibleDetailsAnchor() -> (character: Int, line: CGRect)? {
        var selected: (character: Int, line: CGRect, viewportY: CGFloat)?
        textStorage.enumerateAttribute(
            .link,
            in: NSRange(location: 0, length: textStorage.length),
            options: []
        ) { value, characterRange, _ in
            guard value != nil, characterRange.length > 0 else { return }
            let glyphRange = layoutManager.glyphRange(
                forCharacterRange: characterRange,
                actualCharacterRange: nil
            )
            guard glyphRange.length > 0,
                  glyphRange.location < layoutManager.numberOfGlyphs else { return }
            let line = layoutManager.lineFragmentRect(forGlyphAt: glyphRange.location, effectiveRange: nil)
            let viewportY = line.minY + textContainerInset.top - contentOffset.y
            guard viewportY < bounds.height, viewportY + line.height > 0 else { return }
            if selected.map({ $0.viewportY <= viewportY }) ?? false { return }
            selected = (characterRange.location, line, viewportY)
        }
        return selected.map { (character: $0.character, line: $0.line) }
    }

    private func restoreReadingPosition() {
        guard let character = anchorCharacterIndex,
              let viewportY = anchorViewportY,
              textStorage.length > 0 else { return }
        layoutManager.ensureLayout(for: textContainer)
        let glyph = layoutManager.glyphIndexForCharacter(at: min(character, textStorage.length - 1))
        let line = layoutManager.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
        let maximumAnchorViewportY = max(0, bounds.height - adjustedContentInset.bottom - line.height)
        let restoredAnchorViewportY = min(max(viewportY, 0), maximumAnchorViewportY)
        let requestedY = line.minY + textContainerInset.top - restoredAnchorViewportY
        let maximumY = max(-adjustedContentInset.top, contentSize.height - bounds.height + adjustedContentInset.bottom)
        let restoredY = min(max(requestedY, -adjustedContentInset.top), maximumY)
        isRestoringReadingPosition = true
        setContentOffset(CGPoint(x: contentOffset.x, y: restoredY), animated: false)
        isRestoringReadingPosition = false
        readingPositionRestoreCount += 1
    }
}

struct DobbyLogView: UIViewRepresentable {
    let entries: [DobbyLogEntry]
    let clear: Int

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> UITextView {
        let view = DobbyLogTextView()
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
        if cleared {
            coordinator.isFollowing = true
            coordinator.expanded.removeAll()
        }
        if coordinator.isFollowing { coordinator.displayedEntries = entries }
        if let logView = view as? DobbyLogTextView {
            logView.preservesReadingPosition = !coordinator.isFollowing
            logView.updateFollowingAccessibilityHint(isFollowing: coordinator.isFollowing)
        }
        let restoreCountBeforeLayout = (view as? DobbyLogTextView)?.readingPositionRestoreCount
        let attributed = logText(coordinator.displayedEntries, expanded: coordinator.expanded)
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
        if coordinator.isFollowing || cleared {
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
        var isFollowing = true
        var isUserDragging = false
        var updating = false
        var latestEntries: [DobbyLogEntry] = []
        var displayedEntries: [DobbyLogEntry] = []
        var expanded = Set<String>()
        private var lastStableContentHeight: CGFloat?

        func textView(_ textView: UITextView, shouldInteractWith url: URL, in characterRange: NSRange, interaction: UITextItemInteraction) -> Bool {
            guard let index = Int(url.lastPathComponent), displayedEntries.indices.contains(index) else { return false }
            let id = displayedEntries[index].id
            if !expanded.insert(id).inserted { expanded.remove(id) }
            updating = true
            let offset = textView.contentOffset
            textView.attributedText = logText(displayedEntries, expanded: expanded)
            textView.layoutIfNeeded()
            if let logView = textView as? DobbyLogTextView {
                _ = effectiveContentHeight(for: logView)
            }
            if isFollowing {
                textView.scrollRangeToVisible(NSRange(location: textView.textStorage.length, length: 0))
            } else {
                textView.setContentOffset(offset, animated: false)
            }
            updating = false
            return false
        }
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
            let shouldFollow = atBottom && (isFollowing || !userGestureActive)
            let resumedFollowing = !isFollowing && shouldFollow
            isFollowing = shouldFollow
            logView.preservesReadingPosition = !shouldFollow
            logView.updateFollowingAccessibilityHint(isFollowing: shouldFollow)
            if resumedFollowing { displayLatestEntries(in: logView) }
            if !shouldFollow { logView.captureReadingPosition() }
        }

        private func displayLatestEntries(in logView: DobbyLogTextView) {
            displayedEntries = latestEntries
            let attributed = logText(displayedEntries, expanded: expanded)
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
        if clear != coordinator.lastClear { resetLogPresentationForClear(following: $following, expanded: &coordinator.expanded) }
        guard following else { return }
        coordinator.entries = entries
        let attributed = logText(entries, expanded: coordinator.expanded)
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
        var expanded = Set<String>()
        init(_ parent: DobbyLogView) { self.parent = parent }
        func textView(_ textView: NSTextView, clickedOnLink link: Any, at charIndex: Int) -> Bool {
            guard let url = link as? URL, let index = Int(url.lastPathComponent), entries.indices.contains(index) else { return false }
            let id = entries[index].id
            if !expanded.insert(id).inserted { expanded.remove(id) }
            updating = true
            let scroll = textView.enclosingScrollView
            let origin = scroll?.contentView.bounds.origin
            textView.textStorage?.setAttributedString(logText(entries, expanded: expanded))
            if let origin { scroll?.contentView.scroll(to: origin) }
            updating = false
            return true
        }
        deinit { for observer in observers { NotificationCenter.default.removeObserver(observer) } }
    }
}
#endif

func resetLogPresentationForClear(following: Binding<Bool>, expanded: inout Set<String>) {
    following.wrappedValue = true
    expanded.removeAll()
}

func shouldFollowLogUpdates(viewportBottom: CGFloat, contentHeight: CGFloat) -> Bool {
    viewportBottom >= contentHeight - 24
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

func logText(_ entries: [DobbyLogEntry], expanded: Set<String>) -> NSAttributedString {
    let output = NSMutableAttributedString(string: "")
    for (index, entry) in entries.enumerated() {
        let color: LogColor
        switch entry.level {
        case "ERROR", "FATAL", "PANIC": color = .systemRed
        case "WARN", "WARNING": color = .systemOrange
        case "DEBUG", "TRACE": color = mutedLogColor
        default: color = normalLogColor
        }
        let header = [entry.timestamp, entry.level, entry.source].filter { !$0.isEmpty }.joined(separator: " · ")
        output.append(NSAttributedString(string: header + "\n" + entry.message + "\n", attributes: [.font: logFont, .foregroundColor: color]))
        if entry.level != "RAW", let link = URL(string: "dobbylog://record/\(index)") {
            output.append(NSAttributedString(string: expanded.contains(entry.id) ? "Hide details\n" : "Details\n", attributes: [.font: logFont, .link: link]))
            if expanded.contains(entry.id) {
                output.append(NSAttributedString(string: entry.raw + "\n", attributes: [.font: logFont, .foregroundColor: normalLogColor]))
            }
        }
    }
    return output
}
