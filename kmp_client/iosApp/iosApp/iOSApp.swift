import SwiftUI
import Sentry
import app
import CommonDI
import Combine


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
