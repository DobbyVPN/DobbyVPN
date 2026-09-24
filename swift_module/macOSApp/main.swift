import DobbyNativeUI
import SwiftUI

@main
struct DobbyVPNMacApp: App {
    @StateObject private var model: DobbySessionViewModel

    init() {
        _model = StateObject(wrappedValue: DobbySessionViewModel(client: MacDesktopSessionClient()))
    }

    var body: some Scene {
        WindowGroup {
            DobbyRootView(model: model)
        }
    }
}
