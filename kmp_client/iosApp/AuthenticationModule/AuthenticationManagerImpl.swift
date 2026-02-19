import LocalAuthentication
import UIKit
import app
import CoreLocation

public protocol AuthenticationLogger {
    func writeLog(_ log: String)
}

public class AuthenticationManagerImpl: NSObject, AuthenticationManager, CLLocationManagerDelegate {
    private let logger: AuthenticationLogger?
    private var context = LAContext()
    private var manager = LocationManager()

    public init(logger: AuthenticationLogger? = nil) {
        self.logger = logger
        super.init()
    }

    public func isAuthenticationAvailable() -> Bool {
        var error: NSError?
        return context.canEvaluatePolicy(.deviceOwnerAuthentication, error: &error)
    }

    public func authenticate(
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
    
    
    public func requireLocationPermission(endingFunc: @escaping (AuthPermissionState) -> any Kotlinx_coroutines_coreJob) {
        manager.requestLocationPermission(callback: endingFunc)
    }
    
    public func requireLocationService(endingFunc: @escaping (KotlinBoolean) -> Void) {
        let locationManager = CLLocationManager()

        func isLocationEnabled() -> Bool {
            return CLLocationManager.locationServicesEnabled()
        }

        // Если сервисы включены — сразу отвечаем
        if isLocationEnabled() {
            endingFunc(true)
            return
        }

        // Если выключены — показываем alert
        let alert = UIAlertController(
            title: "Enable location",
            message: "Location services are turned off. Please enable them to continue.",
            preferredStyle: .alert
        )

        alert.addAction(UIAlertAction(title: "Open settings", style: .default) { _ in
            
            // Подписываемся на событие возвращения в приложение
            var observer: NSObjectProtocol?
            observer = NotificationCenter.default.addObserver(
                forName: UIApplication.didBecomeActiveNotification,
                object: nil,
                queue: .main
            ) { _ in
                NotificationCenter.default.removeObserver(observer!)
                endingFunc(KotlinBoolean(value: isLocationEnabled()))
            }

            // Пытаемся открыть настройки локации
            if let url = URL(string: UIApplication.openSettingsURLString),
               UIApplication.shared.canOpenURL(url) {
                UIApplication.shared.open(url)
            } else {
                endingFunc(false)
            }
        })

        alert.addAction(UIAlertAction(title: "Cancel", style: .cancel) { _ in
            endingFunc(false)
        })

        // Получаем top-most view controller для показа алерта
        if let rootVC = UIApplication.shared.windows.first?.rootViewController {
            rootVC.present(alert, animated: true)
        } else {
            endingFunc(false)
        }
    }
}


class LocationManager: NSObject, CLLocationManagerDelegate {
    private var locationManager: CLLocationManager?
    private let logs: AuthenticationLogger?
    private var callback: ((AuthPermissionState) -> Kotlinx_coroutines_coreJob)?

    public init(logger: AuthenticationLogger? = nil) {
        self.logs = logger
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
            logs?.writeLog("Location services are not enabled")
            _ = callback(.denied)
        }
    }
    
    func locationManager(_ manager: CLLocationManager, didChangeAuthorization status: CLAuthorizationStatus) {
        let state: AuthPermissionState
        switch status {
        case .authorizedWhenInUse, .authorizedAlways:
            logs?.writeLog("Location permission granted.")
            state = .granted
        case .denied, .restricted:
            logs?.writeLog("Location permission denied.")
            state = .denied
        case .notDetermined:
            logs?.writeLog("Location permission not determined.")
            state = .notdetermined
        @unknown default:
            logs?.writeLog("Unknown location permission status.")
            state = .denied
        }
        _ = self.callback?(state)
        
        self.callback = nil
    }
}
