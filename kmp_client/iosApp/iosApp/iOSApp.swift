import SwiftUI
import Sentry
import app
import CommonDI
import Combine
import CoreLocation

@main
struct iOSApp: App {
    private let locationManager = LocationManager()

    init() {
        StartDIKt.startDI(nativeModules: [NativeModuleHolder.shared]) { _ in }
        
        locationManager.requestLocationPermission()
    }
    
    var body: some Scene {
        WindowGroup {
            ContentView()
                .ignoresSafeArea(.keyboard)
        }
    }
}

class LocationManager: NSObject, CLLocationManagerDelegate {
    private var locationManager: CLLocationManager?
    private var logs = NativeModuleHolder.logsRepository

    override init() {
        super.init()
        self.locationManager = CLLocationManager()
        self.locationManager?.delegate = self
    }

    func requestLocationPermission() {
        if CLLocationManager.locationServicesEnabled() {
            locationManager?.requestWhenInUseAuthorization()  // Запрашиваем разрешение на использование геолокации
        } else {
            logs.writeLog(log: "Location services are not enabled")
        }
    }
    
    func locationManager(_ manager: CLLocationManager, didChangeAuthorization status: CLAuthorizationStatus) {
        switch status {
        case .authorizedWhenInUse, .authorizedAlways:
            logs.writeLog(log: "Location permission granted.")
        case .denied, .restricted:
            logs.writeLog(log: "Location permission denied.")
        case .notDetermined:
            logs.writeLog(log: "Location permission not determined.")
        @unknown default:
            logs.writeLog(log: "Unknown location permission status.")
        }
    }
}
