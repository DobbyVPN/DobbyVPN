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
struct DobbyLogView: UIViewRepresentable {
    let entries: [DobbyLogEntry]
    @Binding var following: Bool
    let clear: Int

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
        if clear != coordinator.lastClear { following = true; coordinator.expanded.removeAll() }
        guard following else { return }
        coordinator.entries = entries
        let attributed = logText(entries, expanded: coordinator.expanded)
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
        if following || clear != coordinator.lastClear {
            view.scrollRangeToVisible(NSRange(location: view.textStorage.length, length: 0))
        } else { view.setContentOffset(offset, animated: false) }
        coordinator.lastClear = clear
        coordinator.updating = false
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        var parent: DobbyLogView
        var lastClear = 0
        var updating = false
        var entries: [DobbyLogEntry] = []
        var expanded = Set<String>()
        init(_ parent: DobbyLogView) { self.parent = parent }
        func textView(_ textView: UITextView, shouldInteractWith url: URL, in characterRange: NSRange, interaction: UITextItemInteraction) -> Bool {
            guard let index = Int(url.lastPathComponent), entries.indices.contains(index) else { return false }
            let id = entries[index].id
            if !expanded.insert(id).inserted { expanded.remove(id) }
            updating = true
            let offset = textView.contentOffset
            textView.attributedText = logText(entries, expanded: expanded)
            textView.setContentOffset(offset, animated: false)
            updating = false
            return false
        }
        func scrollViewDidScroll(_ scrollView: UIScrollView) {
            guard !updating, scrollView.isDragging || scrollView.isDecelerating else { return }
            let atBottom = scrollView.contentOffset.y + scrollView.bounds.height >= scrollView.contentSize.height - 24
            if parent.following != atBottom { parent.following = atBottom }
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
        view.isEditable = false
        view.isSelectable = true
        view.isRichText = true
        view.delegate = context.coordinator
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
        if clear != coordinator.lastClear { following = true; coordinator.expanded.removeAll() }
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
        var observer: NSObjectProtocol?
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
        deinit { if let observer { NotificationCenter.default.removeObserver(observer) } }
    }
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

private func logText(_ entries: [DobbyLogEntry], expanded: Set<String>) -> NSAttributedString {
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
