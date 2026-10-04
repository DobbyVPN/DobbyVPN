import DobbyNativeUI
import SwiftUI

@main
struct DobbyVPNMacApp: App {
    @State private var showingAbout = false
    @StateObject private var model: DobbySessionViewModel

    init() {
        _model = StateObject(wrappedValue: DobbySessionViewModel(client: MacDesktopSessionClient()))
    }

    var body: some Scene {
        WindowGroup {
            DobbyRootView(model: model)
                .sheet(isPresented: $showingAbout) { DobbyAboutView(model: model) }
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
        }
        .commands {
            CommandGroup(replacing: .appInfo) {
                Button("About DobbyVPN") { showingAbout = true }
            }
        }
    }
}
