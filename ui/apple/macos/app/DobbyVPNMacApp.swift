import AppKit
import DobbyNativeUI
import SwiftUI

@main
struct DobbyVPNMacApp: App {
    @NSApplicationDelegateAdaptor private var delegate: AppDelegate
    @StateObject private var model: DobbySessionViewModel

    init() {
        _model = StateObject(wrappedValue: DobbySessionViewModel(client: MacDesktopSessionClient()))
    }

    var body: some Scene {
        WindowGroup {
            DobbyRootView(model: model)
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var windowObserver: NSObjectProtocol?

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let window = NSApp.keyWindow {
            restoreFrame(window)
            return
        }
        // SwiftUI may create the WindowGroup window after launch finishes.
        windowObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.didBecomeKeyNotification, object: nil, queue: .main
        ) { [weak self] notification in
            guard let window = notification.object as? NSWindow, !(window is NSPanel) else { return }
            self?.restoreFrame(window)
        }
    }

    /// Opens phone-sized the first time, then keeps the size the user picked.
    private func restoreFrame(_ window: NSWindow) {
        if let windowObserver {
            NotificationCenter.default.removeObserver(windowObserver)
            self.windowObserver = nil
        }
        if !window.setFrameUsingName("doBBYVPN") {
            window.setContentSize(NSSize(width: 380, height: 680))
            window.center()
        }
        window.setFrameAutosaveName("doBBYVPN")
    }
}
