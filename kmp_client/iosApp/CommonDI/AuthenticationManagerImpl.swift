import app
import LocalAuthentication

class AuthenticationManagerImpl : AuthenticationManager {
    private var context = LAContext()

    func isAuthenticationAvailable() -> Bool {
        var error: NSError?
        return self.context.canEvaluatePolicy(.deviceOwnerAuthentication, error: &error)
    }

    func authenticate(
        onAuthSuccess: () -> Void,
        onAuthFailure: () -> Void
    ) {
        if (!isAuthenticationAvailable()) {
            onAuthSuccess()
            return
        }
        self.context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: "Biometric login") {
            success, authenticatedError in
        	DispatchQueue.main.async {
        		if success {
        			onAuthSuccess()
        		} else{
        			onAuthFailure()
        		}
            }
        }
    }
}