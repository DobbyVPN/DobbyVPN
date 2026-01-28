import LocalAuthentication
import app
import Combine
import CoreLocation

class AuthenticationManagerImpl: AuthenticationManager {
    private let locationManager = LocationManager()
    private var context = LAContext()

    func isAuthenticationAvailable() -> Bool {
        var error: NSError?
        return self.context.canEvaluatePolicy(.deviceOwnerAuthentication, error: &error)
    }

    func authenticate(
        onAuthSuccess: @escaping () -> Void,
        onAuthFailure: @escaping () -> Void
    ) {
        if !isAuthenticationAvailable() {
            onAuthFailure()  // Аутентификация не доступна — вызываем onAuthFailure
            return
        }
        
        self.context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: "Biometric login") { success, authenticatedError in
            DispatchQueue.main.async {
                if success {
                    onAuthSuccess()
                } else {
                    onAuthFailure()
                }
            }
        }
    }
    
    func requireLocationPermission() {
        locationManager.requestLocationPermission()
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
