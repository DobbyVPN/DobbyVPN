import SwiftUI
import Sentry
import app
import CommonDI

@main
struct iOSApp: App {
    
    init() {
        StartDIKt.startDI(nativeModules: [NativeModuleHolder.shared]) {_ in }
    }
    
    var body: some Scene {
        WindowGroup {
            ContentView()
                .ignoresSafeArea(.keyboard)
        }
    }
}
