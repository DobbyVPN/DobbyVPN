import SwiftUI
import CommonDI

@main
struct iOSApp: App {
    init() {
        #if DOBBY_SIMULATOR_MINI && targetEnvironment(simulator)
        IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.initialized mode=mini")
        #else
        IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.initialized mode=normal")
        #endif
    }

    var body: some Scene {
        WindowGroup {
            #if DOBBY_SIMULATOR_MINI && targetEnvironment(simulator)
            // Mini mode exercises real initialization without constructing the
            // Metal-backed Compose window. This is not UI launch coverage.
            Color.clear
            #else
            ContentView()
                .ignoresSafeArea(.keyboard)
                .onAppear {
                    // View attachment is a startup milestone, not proof that
                    // Metal presented a frame or that UI interactions passed.
                    IOSAppCompositionRoot.logsRepository.writeLog(log: "startup.ui_attached mode=normal")
                }
            #endif
        }
    }
}
