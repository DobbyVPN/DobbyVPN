import SwiftUI
import Sentry
import app
import CommonDI
import UIKit

@main
struct iOSApp: App {
    init() {
        StartDIKt.startDI(nativeModules: [NativeModuleHolder.shared]) { _ in }
        let device = UIDevice.current
        NativeModuleHolder.logsRepository.writeLog(
            log: "[iOS26-RESEARCH] Device: \(device.name) model=\(device.model) systemVersion=\(device.systemVersion)"
        )
    }

    var body: some Scene {
        WindowGroup {
            ContentView()
                .ignoresSafeArea(.keyboard)
        }
    }
}
