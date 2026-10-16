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
    /// Opens phone-sized the first time, then keeps the size the user picked.
    func applicationDidFinishLaunching(_ notification: Notification) {
        guard let window = NSApp.windows.first else { return }
        if !window.setFrameUsingName("doBBYVPN") {
            window.setContentSize(NSSize(width: 380, height: 680))
            window.center()
        }
        window.setFrameAutosaveName("doBBYVPN")
    }
}
