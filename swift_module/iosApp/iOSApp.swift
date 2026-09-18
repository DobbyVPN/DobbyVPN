import SwiftUI
import CommonDI

@main
struct iOSApp: App {
    init() {
        IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.initialized mode=normal")
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .ignoresSafeArea(.keyboard)
                .onAppear {
                    // This target is a diagnostics host. The release app's
                    // visible controls are rendered by the Go/Fyne binary.
                    IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.ui_attached mode=normal")
                }
        }
    }
}
