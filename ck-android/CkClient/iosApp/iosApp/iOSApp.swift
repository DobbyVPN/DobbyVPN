import SwiftUI
import Sentry
import app
import CommonDI
import Combine


@main
struct iOSApp: App {
    init() {
        StartDIKt.startDI(nativeModules: [NativeModuleHolder.shared]) {_ in }
        iOSApp.writeRouteTableToLog()
        
        DispatchQueue.global(qos: .background).async {
            var howManyTimes = 6
            while howManyTimes > 0 {
                iOSApp.writeRouteTableToLog()
                Thread.sleep(forTimeInterval: 5.0)
                howManyTimes -= 1
            }
        }
    }
    
    static func writeRouteTableToLog() {
        NativeModuleHolder.logsRepository.writeLog(log: RouteTableManager.formatRouteTable().split(separator: "\n").joined(separator: "|||"))
    }
    
    var body: some Scene {
        WindowGroup {
            ContentView()
                .ignoresSafeArea(.keyboard)
        }
    }
}
