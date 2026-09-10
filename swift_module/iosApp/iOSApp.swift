import SwiftUI
import app
import CommonDI

@main
struct iOSApp: App {
    init() {
        NativeModuleHolder.installSessionBridge()
        StartDIKt.startDI(nativeModules: [NativeModuleHolder.shared]) { _ in }
        #if DOBBY_STARTUP_TEST && targetEnvironment(simulator)
        NativeModuleHolder.logsRepository.writeLog(log: "startup.initialized mode=startup-only")
        #else
        NativeModuleHolder.logsRepository.writeLog(log: "startup.initialized mode=normal")
        #endif
    }

    var body: some Scene {
        WindowGroup {
            #if DOBBY_STARTUP_TEST && targetEnvironment(simulator)
            // Test only: exercise real initialization without constructing the
            // Metal-backed Compose window. This is not UI launch coverage.
            Color.clear
            #else
            ContentView()
                .ignoresSafeArea(.keyboard)
                .onAppear {
                    // View attachment is a startup milestone, not proof that
                    // Metal presented a frame or that UI interactions passed.
                    NativeModuleHolder.logsRepository.writeLog(log: "startup.ui_attached mode=normal")
                }
            #endif
        }
    }
}
