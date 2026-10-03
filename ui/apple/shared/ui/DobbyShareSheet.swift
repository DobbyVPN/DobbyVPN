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

    func makeNSView(context: Context) -> NSView {
        let view = NSView()
        DispatchQueue.main.async {
            NSSharingServicePicker(items: [url]).show(
                relativeTo: view.bounds,
                of: view,
                preferredEdge: .minY
            )
        }
        return view
    }

    func updateNSView(_ view: NSView, context: Context) {}
}
#endif

#if os(iOS)
struct DobbyLogView: UIViewRepresentable {
    let text: String
    @Binding var following: Bool
    let jump: Int

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeUIView(context: Context) -> UITextView {
        let view = UITextView()
        view.isEditable = false
        view.isSelectable = true
        view.backgroundColor = .secondarySystemBackground
        view.font = UIFont.preferredFont(forTextStyle: .caption1)
        view.adjustsFontForContentSizeCategory = true
        view.delegate = context.coordinator
        view.accessibilityIdentifier = "Connection logs"
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        let coordinator = context.coordinator
        coordinator.parent = self
        coordinator.updating = true
        let offset = view.contentOffset
        let selection = view.selectedRange
        if view.text != text {
            if text.hasPrefix(view.text) {
                view.textStorage.append(NSAttributedString(string: String(text.dropFirst(view.text.count)), attributes: [
                    .font: UIFont.preferredFont(forTextStyle: .caption1), .foregroundColor: UIColor.label,
                ]))
            } else { view.text = text }
            view.selectedRange = NSRange(location: min(selection.location, view.textStorage.length), length: 0)
            if NSMaxRange(selection) <= view.textStorage.length { view.selectedRange = selection }
        }
        view.layoutIfNeeded()
        if following || jump != coordinator.lastJump {
            view.scrollRangeToVisible(NSRange(location: view.textStorage.length, length: 0))
        } else { view.setContentOffset(offset, animated: false) }
        coordinator.lastJump = jump
        coordinator.updating = false
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        var parent: DobbyLogView
        var lastJump = 0
        var updating = false
        init(_ parent: DobbyLogView) { self.parent = parent }
        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            guard !updating, scrollView.isDragging || scrollView.isDecelerating else { return }
            let atBottom = scrollView.contentOffset.y + scrollView.bounds.height >= scrollView.contentSize.height - 24
            if parent.following != atBottom { parent.following = atBottom }
        }
    }
}
#elseif os(macOS)
struct DobbyLogView: NSViewRepresentable {
    let text: String
    @Binding var following: Bool
    let jump: Int

    func makeCoordinator() -> Coordinator { Coordinator(self) }

    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSTextView.scrollableTextView()
        guard let view = scroll.documentView as? NSTextView else { return scroll }
        view.isEditable = false
        view.isSelectable = true
        view.isRichText = false
        view.font = .monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular)
        view.textColor = .textColor
        view.backgroundColor = .textBackgroundColor
        view.setAccessibilityIdentifier("Connection logs")
        scroll.contentView.postsBoundsChangedNotifications = true
        context.coordinator.observer = NotificationCenter.default.addObserver(
            forName: NSView.boundsDidChangeNotification, object: scroll.contentView, queue: .main
        ) { [weak coordinator = context.coordinator, weak scroll] _ in
            guard let coordinator, let scroll, !coordinator.updating else { return }
            let atBottom = scroll.contentView.bounds.maxY >= (scroll.documentView?.bounds.height ?? 0) - 24
            if coordinator.parent.following != atBottom {
                DispatchQueue.main.async { coordinator.parent.following = atBottom }
            }
        }
        return scroll
    }

    func updateNSView(_ scroll: NSScrollView, context: Context) {
        guard let view = scroll.documentView as? NSTextView, let storage = view.textStorage else { return }
        let coordinator = context.coordinator
        coordinator.parent = self
        coordinator.updating = true
        let origin = scroll.contentView.bounds.origin
        let selection = view.selectedRange()
        if view.string != text {
            if text.hasPrefix(view.string) {
                storage.append(NSAttributedString(string: String(text.dropFirst(view.string.count)), attributes: [
                    .font: NSFont.monospacedSystemFont(ofSize: NSFont.smallSystemFontSize, weight: .regular),
                    .foregroundColor: NSColor.textColor,
                ]))
            } else { view.string = text }
            if NSMaxRange(selection) <= storage.length { view.setSelectedRange(selection) }
        }
        if let container = view.textContainer { view.layoutManager?.ensureLayout(for: container) }
        if following || jump != coordinator.lastJump {
            view.scrollRangeToVisible(NSRange(location: storage.length, length: 0))
        } else {
            scroll.contentView.scroll(to: origin)
            scroll.reflectScrolledClipView(scroll.contentView)
        }
        coordinator.lastJump = jump
        coordinator.updating = false
    }

    final class Coordinator {
        var parent: DobbyLogView
        var lastJump = 0
        var updating = false
        var observer: NSObjectProtocol?
        init(_ parent: DobbyLogView) { self.parent = parent }
        deinit { if let observer { NotificationCenter.default.removeObserver(observer) } }
    }
}
#endif
