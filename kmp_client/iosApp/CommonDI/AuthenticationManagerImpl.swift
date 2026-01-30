import LocalAuthentication
import app
import CoreLocation

class AuthenticationManagerImpl: NSObject, AuthenticationManager, CLLocationManagerDelegate {
    
    private var context = LAContext()
    private var manager = LocationManager()

    override init() {
        super.init()
    }

    func isAuthenticationAvailable() -> Bool {
        var error: NSError?
        return context.canEvaluatePolicy(.deviceOwnerAuthentication, error: &error)
    }

    func authenticate(
        onAuthSuccess: @escaping () -> Void,
        onAuthFailure: @escaping () -> Void
    ) {
        if !isAuthenticationAvailable() {
            onAuthFailure()
            return
        }

        context.evaluatePolicy(
            .deviceOwnerAuthentication,
            localizedReason: "Biometric login"
        ) { success, _ in
            DispatchQueue.main.async {
                success ? onAuthSuccess() : onAuthFailure()
            }
        }
    }
    
    
    func requireLocationPermission(endingFunc: @escaping (AuthPermissionState) -> any Kotlinx_coroutines_coreJob) {
        manager.requestLocationPermission(callback: endingFunc)
    }
}


class LocationManager: NSObject, CLLocationManagerDelegate {
    private var locationManager: CLLocationManager?
    private var logs = NativeModuleHolder.logsRepository
    private var callback: ((AuthPermissionState) -> Kotlinx_coroutines_coreJob)?


    override init() {
        super.init()
        self.locationManager = CLLocationManager()
        self.locationManager?.delegate = self
    }

    func requestLocationPermission(
        callback: @escaping (AuthPermissionState) -> Kotlinx_coroutines_coreJob
    ) {
        self.callback = callback

        if CLLocationManager.locationServicesEnabled() {
            locationManager?.requestWhenInUseAuthorization()
        } else {
            logs.writeLog(log: "Location services are not enabled")
            _ = callback(.denied)
        }
    }
    
    func locationManager(_ manager: CLLocationManager, didChangeAuthorization status: CLAuthorizationStatus) {
        let state: AuthPermissionState
        switch status {
        case .authorizedWhenInUse, .authorizedAlways:
            logs.writeLog(log: "Location permission granted.")
            state = .granted
        case .denied, .restricted:
            logs.writeLog(log: "Location permission denied.")
            state = .denied
        case .notDetermined:
            logs.writeLog(log: "Location permission not determined.")
            state = .notdetermined
        @unknown default:
            logs.writeLog(log: "Unknown location permission status.")
            state = .denied
        }
        _ = self.callback?(state)
        
        self.callback = nil
    }
}
