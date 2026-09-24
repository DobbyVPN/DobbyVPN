import SwiftUI
import CommonDI

@main
struct NativeDobbyVPNApp: App {
    @StateObject private var model: DobbySessionViewModel

    init() {
        _model = StateObject(wrappedValue: DobbySessionViewModel(client: IOSSessionClient()))
    }

    var body: some Scene {
        WindowGroup {
            DobbyRootView(model: model)
        }
    }
}
