import LocalAuthentication
import app

class AuthenticationManagerImpl: AuthenticationManager {
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
}
