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
private final class DobbyLogTextView: UITextView, UIGestureRecognizerDelegate {
    var onSendLogs: () -> Void = {}
    var onClear: () -> Void = {}
    private var editMenu: AnyObject?
    private var editMenuDelegate: AnyObject?
    private var showingLegacyMenu = false
    var preservesReadingPosition = false
    private(set) var isRestoringReadingPosition = false
    private(set) var readingPositionRestoreCount = 0
    private var lastLayoutSize: CGSize?
    private var anchorCharacterIndex: Int?
    private var anchorViewportY: CGFloat?

    func installLogsMenu() {
        let doubleTap = UITapGestureRecognizer(target: self, action: #selector(showLogsMenu(_:)))
        doubleTap.numberOfTapsRequired = 2
        doubleTap.delegate = self
        addGestureRecognizer(doubleTap)
        if #available(iOS 16.0, *) {
            let delegate = LogsEditMenu(self)
            let interaction = UIEditMenuInteraction(delegate: delegate)
            addInteraction(interaction)
            editMenu = interaction
            editMenuDelegate = delegate
        }
        accessibilityCustomActions = [
            UIAccessibilityCustomAction(name: "Send logs") { [weak self] _ in self?.onSendLogs(); return true },
            UIAccessibilityCustomAction(name: "Clear") { [weak self] _ in self?.onClear(); return true },
        ]
    }

    // The double tap opens the logs menu instead of selecting a word; a long press still selects text.
    func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer, shouldBeRequiredToFailBy other: UIGestureRecognizer) -> Bool {
        other is UITapGestureRecognizer && other.view === self
    }

    @objc private func showLogsMenu(_ gesture: UITapGestureRecognizer) {
        let point = gesture.location(in: self)
        if #available(iOS 16.0, *), let interaction = editMenu as? UIEditMenuInteraction {
            interaction.presentEditMenu(with: UIEditMenuConfiguration(identifier: nil, sourcePoint: point))
        } else {
            showingLegacyMenu = true
            becomeFirstResponder()
            UIMenuController.shared.menuItems = [
                UIMenuItem(title: "Send logs", action: #selector(sendLogsFromMenu)),
                UIMenuItem(title: "Clear", action: #selector(clearFromMenu)),
            ]
            UIMenuController.shared.showMenu(from: self, rect: CGRect(origin: point, size: .zero))
            NotificationCenter.default.addObserver(
                self, selector: #selector(legacyMenuHidden), name: UIMenuController.willHideMenuNotification, object: nil
            )
        }
    }

    @objc private func sendLogsFromMenu() { onSendLogs() }
    @objc private func clearFromMenu() { onClear() }

    @objc private func legacyMenuHidden() {
        showingLegacyMenu = false
        UIMenuController.shared.menuItems = nil
        NotificationCenter.default.removeObserver(self, name: UIMenuController.willHideMenuNotification, object: nil)
    }

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        if action == #selector(sendLogsFromMenu) || action == #selector(clearFromMenu) { return showingLegacyMenu }
        return !showingLegacyMenu && super.canPerformAction(action, withSender: sender)
    }

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
        let point = CGPoint(
            x: textContainerInset.left + 1,
            y: contentOffset.y - textContainerInset.top + 1
        )
        let topGlyph = layoutManager.glyphIndex(for: point, in: textContainer)
        var character = min(layoutManager.characterIndexForGlyph(at: topGlyph), textStorage.length - 1)
        // Anchor the first record shown in full, from its start: records above may wrap differently after a resize.
        let record = (textStorage.string as NSString).paragraphRange(for: NSRange(location: character, length: 0))
        let topLine = layoutManager.lineFragmentRect(forGlyphAt: topGlyph, effectiveRange: nil)
        let cutByTopEdge = topLine.minY + textContainerInset.top < contentOffset.y
        if (record.location < character || cutByTopEdge) && NSMaxRange(record) < textStorage.length {
            character = NSMaxRange(record)
        }
        let line = layoutManager.lineFragmentRect(forGlyphAt: layoutManager.glyphIndexForCharacter(at: character), effectiveRange: nil)
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
        // Keep the captured line's partially clipped top position across a size change.
        let minimumAnchorViewportY = -line.height
        let maximumAnchorViewportY = max(0, bounds.height - adjustedContentInset.bottom - line.height)
        let restoredAnchorViewportY = min(max(viewportY, minimumAnchorViewportY), maximumAnchorViewportY)
        let requestedY = line.minY + textContainerInset.top - restoredAnchorViewportY
        let maximumY = max(-adjustedContentInset.top, contentSize.height - bounds.height + adjustedContentInset.bottom)
        let restoredY = min(max(requestedY, -adjustedContentInset.top), maximumY)
        isRestoringReadingPosition = true
        setContentOffset(CGPoint(x: contentOffset.x, y: restoredY), animated: false)
        isRestoringReadingPosition = false
        readingPositionRestoreCount += 1
    }
}

@available(iOS 16.0, *)
private final class LogsEditMenu: NSObject, UIEditMenuInteractionDelegate {
    private weak var view: DobbyLogTextView?

    init(_ view: DobbyLogTextView) { self.view = view }

    func editMenuInteraction(
        _ interaction: UIEditMenuInteraction, menuFor configuration: UIEditMenuConfiguration, suggestedActions: [UIMenuElement]
    ) -> UIMenu? {
        UIMenu(children: [
            UIAction(title: "Send logs") { [weak view] _ in view?.onSendLogs() },
            UIAction(title: "Clear") { [weak view] _ in view?.onClear() },
        ])
    }
}

struct DobbyLogView: UIViewRepresentable {
    let entries: [DobbyLogEntry]
    let clear: Int
    let onSendLogs: () -> Void
    let onClear: () -> Void

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
        view.backgroundColor = DobbyPalette.surface
        view.textContainerInset = UIEdgeInsets(top: 6, left: 12, bottom: 6, right: 12)
        view.textContainer.lineFragmentPadding = 0
        view.adjustsFontForContentSizeCategory = true
        view.installLogsMenu()
        view.delegate = context.coordinator
        view.accessibilityIdentifier = "Connection logs"
        view.updateFollowingAccessibilityHint(isFollowing: true)
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        let coordinator = context.coordinator
        if let logView = view as? DobbyLogTextView {
            logView.onSendLogs = onSendLogs
            logView.onClear = onClear
        }
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
private final class DobbyLogTextView: NSTextView {
    var onSendLogs: () -> Void = {}
    var onClear: () -> Void = {}

    private var logsMenu: NSMenu {
        let menu = NSMenu()
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

struct DobbyLogView: NSViewRepresentable {
    let entries: [DobbyLogEntry]
    @Binding var following: Bool
    let clear: Int
    /// Off while a popup covers the logs, so the text cursor does not show through it.
    let interactive: Bool
    let onSendLogs: () -> Void
    let onClear: () -> Void

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.hasVerticalScroller = true
        scroll.drawsBackground = false
        let view = DobbyLogTextView(frame: .zero)
        view.autoresizingMask = [.width]
        view.isVerticallyResizable = true
        view.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        view.textContainer?.widthTracksTextView = true
        scroll.documentView = view
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
        scroll.postsFrameChangedNotifications = true
        let handleBoundsChange: (Notification) -> Void = { [weak coordinator = context.coordinator, weak scroll] _ in
            guard let coordinator, let scroll else { return }
            coordinator.handleViewportChange(scroll)
            guard !coordinator.updating, coordinator.userScrolling else { return }
            let atBottom = shouldFollowLogUpdates(
                viewportBottom: scroll.contentView.bounds.maxY,
                contentHeight: scroll.documentView?.bounds.height ?? 0
            )
            if coordinator.parent.following != atBottom {
                DispatchQueue.main.async { coordinator.parent.following = atBottom }
            }
        }
        coordinator.observers.append(NotificationCenter.default.addObserver(
            forName: NSView.boundsDidChangeNotification, object: scroll.contentView, queue: .main,
            using: handleBoundsChange
        ))
        coordinator.observers.append(NotificationCenter.default.addObserver(
            forName: NSView.frameDidChangeNotification, object: scroll, queue: .main,
            using: handleBoundsChange
        ))
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let view = scroll.documentView as? DobbyLogTextView, let storage = view.textStorage else { return }
        view.onSendLogs = onSendLogs
        view.onClear = onClear
        if view.isSelectable != interactive {
            view.isSelectable = interactive
            view.window?.invalidateCursorRects(for: view)
        }
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
        coordinator.handleViewportChange(scroll)
    }

    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: DobbyLogView
        var lastClear = 0
        var updating = false
        var observers: [NSObjectProtocol] = []
        var userScrolling = false
        var entries: [DobbyLogEntry] = []
        var lastViewportHeight: CGFloat = 0
        init(_ parent: DobbyLogView) { self.parent = parent }

        // AppKit keeps the old content offset when the viewport shrinks. If the
        // reader is following the tail, realign after the new layout settles.
        func handleViewportChange(_ scroll: NSScrollView) {
            guard !updating, !userScrolling, parent.following else { return }
            let viewportHeight = scroll.contentView.bounds.height
            guard abs(viewportHeight - lastViewportHeight) > 0.5 else { return }
            lastViewportHeight = viewportHeight
            DispatchQueue.main.async { [weak self, weak scroll] in
                guard let self, let scroll, !self.updating, !self.userScrolling,
                      self.parent.following else { return }
                self.scrollToBottom(scroll)
            }
        }

        func scrollToBottom(_ scroll: NSScrollView) {
            guard let document = scroll.documentView else { return }
            let maximumY = max(0, document.bounds.height - scroll.contentView.bounds.height)
            scroll.contentView.scroll(to: NSPoint(x: 0, y: maximumY))
            scroll.reflectScrolledClipView(scroll.contentView)
        }
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
    view.font = logFont
    view.textColor = DobbyPalette.ink
    view.backgroundColor = DobbyPalette.surface
    view.textContainerInset = NSSize(width: 12, height: 6)
    view.textContainer?.lineFragmentPadding = 0
    view.setAccessibilityIdentifier("Connection logs")
}
#endif

#if os(iOS)
typealias PlatformColor = UIColor
typealias PlatformFont = UIFont
#elseif os(macOS)
typealias PlatformColor = NSColor
typealias PlatformFont = NSFont
#endif

// The palette lives beside the log renderer: the iOS renderer tests compile this file on its own.
enum DobbyPalette {
    static let paper = PlatformColor(red: 0xD9 / 255, green: 0xD2 / 255, blue: 0xC9 / 255, alpha: 1)
    static let ink = PlatformColor(red: 0x14 / 255, green: 0x14 / 255, blue: 0x14 / 255, alpha: 1)
    static let surface = PlatformColor(red: 0xF6 / 255, green: 0xF3 / 255, blue: 0xEE / 255, alpha: 1)
    static let muted = PlatformColor(red: 0x55 / 255, green: 0x50 / 255, blue: 0x4A / 255, alpha: 1)
    static let rule = PlatformColor(red: 0xB9 / 255, green: 0xB1 / 255, blue: 0xA6 / 255, alpha: 1)
    static let tray = PlatformColor(red: 0xCC / 255, green: 0xC4 / 255, blue: 0xB9 / 255, alpha: 1)
}

#if os(iOS)
private var logFont: UIFont {
    UIFontMetrics(forTextStyle: .caption1).scaledFont(for: .monospacedSystemFont(ofSize: 11.5, weight: .regular))
}
private var boldLogFont: UIFont {
    UIFontMetrics(forTextStyle: .caption1).scaledFont(for: .monospacedSystemFont(ofSize: 11.5, weight: .bold))
}
#elseif os(macOS)
private let logFont = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
private let boldLogFont = NSFont.monospacedSystemFont(ofSize: 11, weight: .bold)
#endif

private let logTimeFormat: DateFormatter = {
    let format = DateFormatter()
    format.locale = Locale(identifier: "en_US_POSIX")
    format.dateFormat = "HH:mm:ss"
    return format
}()

func logText(_ entries: [DobbyLogEntry]) -> NSAttributedString {
    let output = NSMutableAttributedString(string: "")
    let paragraph = NSMutableParagraphStyle()
    // Extra space goes below each line so a line's text starts at the top of its fragment.
    paragraph.lineSpacing = 3
    for entry in entries {
        let time = entry.date.map(logTimeFormat.string) ?? entry.timestamp
        if !time.isEmpty {
            output.append(NSAttributedString(string: time + "  ", attributes: [
                .font: logFont, .foregroundColor: DobbyPalette.muted, .paragraphStyle: paragraph,
            ]))
        }
        let font: PlatformFont
        let color: PlatformColor
        switch entry.level {
        case "ERROR", "FATAL", "PANIC", "WARN", "WARNING": font = boldLogFont; color = DobbyPalette.ink
        case "DEBUG", "TRACE": font = logFont; color = DobbyPalette.muted
        default: font = logFont; color = DobbyPalette.ink
        }
        output.append(NSAttributedString(string: entry.message + "\n", attributes: [
            .font: font, .foregroundColor: color, .paragraphStyle: paragraph,
        ]))
    }
    return output
}
