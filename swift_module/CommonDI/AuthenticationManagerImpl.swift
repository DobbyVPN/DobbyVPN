import LocalAuthentication
import UIKit
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

    func requireLocationService(endingFunc: @escaping (KotlinBoolean) -> Void) {
        let locationManager = CLLocationManager()

        func isLocationEnabled() -> Bool {
            return CLLocationManager.locationServicesEnabled()
        }

        // If location services are enabled, respond immediately
        if isLocationEnabled() {
            endingFunc(KotlinBoolean(value: true))
            return
        }

        // If disabled, show an alert
        let alert = UIAlertController(
            title: "Enable location",
            message: "Location services are turned off. Please enable them to continue.",
            preferredStyle: .alert
        )

        alert.addAction(UIAlertAction(title: "Open settings", style: .default) { _ in
            guard let url = URL(string: UIApplication.openSettingsURLString),
                  UIApplication.shared.canOpenURL(url) else {
                endingFunc(KotlinBoolean(value: false))
                return
            }
            
            var observer: NSObjectProtocol?
            observer = NotificationCenter.default.addObserver(
                forName: UIApplication.didBecomeActiveNotification,
                object: nil,
                queue: .main
            ) { _ in
                if let obs = observer { NotificationCenter.default.removeObserver(obs) }
                endingFunc(KotlinBoolean(value: isLocationEnabled()))
            }

            UIApplication.shared.open(url) { success in
                if !success {
                    if let obs = observer { NotificationCenter.default.removeObserver(obs) }
                    endingFunc(KotlinBoolean(value: false))
                }
            }
        })

        alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in
            endingFunc(KotlinBoolean(value: false))
        })

        // Get the top-most view controller to present the alert
        if let rootVC = UIApplication.shared.windows.first?.rootViewController {
            rootVC.present(alert, animated: true)
        } else {
            endingFunc(KotlinBoolean(value: false))
        }
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
