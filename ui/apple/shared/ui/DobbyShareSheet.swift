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
    var onLayoutDiagnostic: ((DobbyLogTextView) -> Void)?
    private(set) var isRestoringReadingPosition = false
    private var lastLayoutSize: CGSize?
    private var anchorCharacterIndex: Int?
    private var anchorViewportY: CGFloat?

    override func layoutSubviews() {
        super.layoutSubviews()
        onLayoutDiagnostic?(self)
        let sizeChanged = lastLayoutSize.map {
            abs($0.width - bounds.width) > 0.5 || abs($0.height - bounds.height) > 0.5
        } ?? false
        lastLayoutSize = bounds.size
        if sizeChanged && preservesReadingPosition { restoreReadingPosition() }
    }

    func updateFollowingAccessibilityHint(isFollowing: Bool) {
        accessibilityHint = isFollowing
            ? NSLocalizedString("Showing newest entries. Scroll up to pause automatic updates.", comment: "")
            : NSLocalizedString("New entries are held while you read earlier logs. Scroll to the bottom to resume.", comment: "")
    }

    func captureReadingPosition() {
        guard textStorage.length > 0 else { return }
        layoutManager.ensureLayout(for: textContainer)
        let point = CGPoint(
            x: textContainerInset.left + 1,
            y: contentOffset.y + textContainerInset.top + 1
        )
        let glyph = layoutManager.glyphIndex(for: point, in: textContainer)
        let character = min(layoutManager.characterIndexForGlyph(at: glyph), textStorage.length - 1)
        let line = layoutManager.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
        anchorCharacterIndex = character
        anchorViewportY = line.minY + textContainerInset.top - contentOffset.y
    }

    private func restoreReadingPosition() {
        guard let character = anchorCharacterIndex,
              let viewportY = anchorViewportY,
              textStorage.length > 0 else { return }
        layoutManager.ensureLayout(for: textContainer)
        let glyph = layoutManager.glyphIndexForCharacter(at: min(character, textStorage.length - 1))
        let line = layoutManager.lineFragmentRect(forGlyphAt: glyph, effectiveRange: nil)
        let requestedY = line.minY + textContainerInset.top - viewportY
        let maximumY = max(-adjustedContentInset.top, contentSize.height - bounds.height + adjustedContentInset.bottom)
        let restoredY = min(max(requestedY, -adjustedContentInset.top), maximumY)
        isRestoringReadingPosition = true
        setContentOffset(CGPoint(x: contentOffset.x, y: restoredY), animated: false)
        isRestoringReadingPosition = false
    }
}

struct DobbyLogView: UIViewRepresentable {
    let entries: [DobbyLogEntry]
    let clear: Int
    let onFollowingChange: (Bool) -> Void
    let onScrollDiagnostic: ((String) -> Void)?

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
        context.coordinator.onFollowingChange = onFollowingChange
        context.coordinator.onScrollDiagnostic = onScrollDiagnostic
        view.onLayoutDiagnostic = { [weak coordinator = context.coordinator] textView in
            coordinator?.recordLayoutDiagnostic(textView)
        }
        view.updateFollowingAccessibilityHint(isFollowing: true)
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        let coordinator = context.coordinator
        coordinator.onFollowingChange = onFollowingChange
        coordinator.onScrollDiagnostic = onScrollDiagnostic
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
        if coordinator.isFollowing || cleared {
            view.scrollRangeToVisible(NSRange(location: view.textStorage.length, length: 0))
        } else { view.setContentOffset(offset, animated: false) }
        coordinator.lastClear = clear
        coordinator.updating = false
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        var lastClear = 0
        var isFollowing = true
        var isUserDragging = false
        var updating = false
        var displayedEntries: [DobbyLogEntry] = []
        var expanded = Set<String>()
        var onFollowingChange: ((Bool) -> Void)?
        var onScrollDiagnostic: ((String) -> Void)?
        var isRecordingLayoutDiagnostics = false
        private(set) var diagnosticEventCount = 0
        private let maximumDiagnosticEvents = 96

        private struct FollowingUpdate {
            let previous: Bool
            let current: Bool
            let changed: Bool
            let emitted: Bool
        }

        func textView(_ textView: UITextView, shouldInteractWith url: URL, in characterRange: NSRange, interaction: UITextItemInteraction) -> Bool {
            guard let index = Int(url.lastPathComponent), displayedEntries.indices.contains(index) else { return false }
            let id = displayedEntries[index].id
            if !expanded.insert(id).inserted { expanded.remove(id) }
            diagnosticEventCount = 0
            isRecordingLayoutDiagnostics = true
            recordScrollDiagnostic("details-text-before", for: textView, accepted: false)
            updating = true
            let offset = textView.contentOffset
            textView.attributedText = logText(displayedEntries, expanded: expanded)
            recordScrollDiagnostic("details-text-assigned", for: textView, accepted: false)
            textView.layoutIfNeeded()
            recordScrollDiagnostic("details-after-layout-if-needed", for: textView, accepted: false)
            if isFollowing {
                textView.scrollRangeToVisible(NSRange(location: textView.textStorage.length, length: 0))
            } else {
                textView.setContentOffset(offset, animated: false)
            }
            recordScrollDiagnostic("details-after-position-restore", for: textView, accepted: false)
            updating = false
            DispatchQueue.main.async { [weak self, weak textView] in
                guard let self, let textView else { return }
                self.recordScrollDiagnostic("details-next-main-turn", for: textView, accepted: false)
                self.isRecordingLayoutDiagnostics = false
            }
            return false
        }
        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            guard scrollView is DobbyLogTextView else { return }
            let gestureActive = isUserDragging || scrollView.isDragging || scrollView.isDecelerating
            guard gestureActive else { return }

            let accepted = !updating && (isUserDragging || scrollView.isDecelerating)
            let update = accepted ? updateFollowingState(for: scrollView) : nil
            recordScrollDiagnostic("didScroll", for: scrollView, accepted: accepted, update: update)
            if let update, update.changed {
                recordScrollDiagnostic(
                    "followingChange", for: scrollView, accepted: accepted, update: update
                )
            }
        }
        func scrollViewWillBeginDragging(_ scrollView: UIScrollView) {
            guard scrollView is DobbyLogTextView else { return }
            isUserDragging = true
            recordScrollDiagnostic("willBeginDragging", for: scrollView, accepted: true)
        }
        func scrollViewDidEndDragging(_ scrollView: UIScrollView, willDecelerate decelerate: Bool) {
            guard scrollView is DobbyLogTextView else { return }
            let accepted = isUserDragging && !updating
            isUserDragging = false
            let update = accepted ? updateFollowingState(for: scrollView) : nil
            recordScrollDiagnostic(
                "didEndDragging(decelerate=\(decelerate))",
                for: scrollView,
                accepted: accepted,
                update: update
            )
        }
        func scrollViewDidEndDecelerating(_ scrollView: UIScrollView) {
            guard scrollView is DobbyLogTextView else { return }
            isUserDragging = false
            let update = updateFollowingState(for: scrollView)
            recordScrollDiagnostic("didEndDecelerating", for: scrollView, accepted: true, update: update)
        }

        func recordLayoutDiagnostic(_ textView: UITextView) {
            guard textView is DobbyLogTextView else { return }
            guard isRecordingLayoutDiagnostics else { return }
            recordScrollDiagnostic("layoutSubviews", for: textView, accepted: false)
        }
        private func updateFollowingState(for scrollView: UIScrollView) -> FollowingUpdate? {
            guard let logView = scrollView as? DobbyLogTextView,
                  !logView.isRestoringReadingPosition else { return nil }
            let previous = isFollowing
            let atBottom = shouldFollowLogUpdates(
                viewportBottom: scrollView.contentOffset.y + scrollView.bounds.height
                    - scrollView.adjustedContentInset.bottom,
                contentHeight: scrollView.contentSize.height
            )
            let changed = isFollowing != atBottom
            isFollowing = atBottom
            logView.preservesReadingPosition = !atBottom
            logView.updateFollowingAccessibilityHint(isFollowing: atBottom)
            if changed { onFollowingChange?(atBottom) }
            if !atBottom { logView.captureReadingPosition() }
            return FollowingUpdate(
                previous: previous,
                current: atBottom,
                changed: changed,
                emitted: changed && onFollowingChange != nil
            )
        }

        private func recordScrollDiagnostic(
            _ event: String,
            for scrollView: UIScrollView,
            accepted: Bool,
            update: FollowingUpdate? = nil
        ) {
            guard let logView = scrollView as? DobbyLogTextView,
                  let onScrollDiagnostic,
                  diagnosticEventCount < maximumDiagnosticEvents else { return }
            diagnosticEventCount += 1
            let rawViewportBottom = scrollView.contentOffset.y + scrollView.bounds.height
            let viewportBottom = rawViewportBottom - scrollView.adjustedContentInset.bottom
            let distanceToBottom = scrollView.contentSize.height - viewportBottom
            let atBottom = shouldFollowLogUpdates(
                viewportBottom: viewportBottom,
                contentHeight: scrollView.contentSize.height
            )
            func number(_ value: CGFloat) -> String {
                String(format: "%.2f", locale: Locale(identifier: "en_US_POSIX"), value)
            }
            onScrollDiagnostic([
                "event=\(event)",
                "accepted=\(accepted)",
                "userDragging=\(isUserDragging)",
                "scrollIsDragging=\(scrollView.isDragging)",
                "decelerating=\(scrollView.isDecelerating)",
                "updating=\(updating)",
                "restoring=\(logView.isRestoringReadingPosition)",
                "offsetY=\(number(scrollView.contentOffset.y))",
                "boundsWidth=\(number(scrollView.bounds.width))",
                "viewportHeight=\(number(scrollView.bounds.height))",
                "contentHeight=\(number(scrollView.contentSize.height))",
                "textStorageLength=\(logView.textStorage.length)",
                "textContainerInsetTop=\(number(logView.textContainerInset.top))",
                "textContainerInsetBottom=\(number(logView.textContainerInset.bottom))",
                "contentInsetBottom=\(number(scrollView.contentInset.bottom))",
                "adjustedContentInsetBottom=\(number(scrollView.adjustedContentInset.bottom))",
                "rawViewportBottom=\(number(rawViewportBottom))",
                "adjustedViewportBottom=\(number(viewportBottom))",
                "distanceToBottom=\(number(distanceToBottom))",
                "atBottom=\(atBottom)",
                "followingBefore=\(update?.previous ?? isFollowing)",
                "followingAfter=\(update?.current ?? isFollowing)",
                "changed=\(update?.changed ?? false)",
                "emitted=\(update?.emitted ?? false)",
            ].joined(separator: " "))
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
