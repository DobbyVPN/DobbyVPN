import SwiftUI
import Sentry
import app
import CommonDI
import Combine


@main
struct iOSApp: App {
    init() {
        let logger = Logger(logsRepository: NativeModuleHolder.logsRepository)
        StartDIKt.startDI(nativeModules: [NativeModuleHolder.shared]) { koinApp in
            koinApp.koin.declare(
                instance: logger,
                qualifier: nil,
                secondaryTypes: [],
                allowOverride: true
            )
        }
    }
    
    var body: some Scene {
        WindowGroup {
            ContentView()
                .ignoresSafeArea(.keyboard)
        }
    }
}
