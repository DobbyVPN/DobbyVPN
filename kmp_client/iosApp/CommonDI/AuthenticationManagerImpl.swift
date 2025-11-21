import app

class AuthenticationManagerImpl : AuthenticationManager {
    func isAuthenticationAvailable() -> Bool {
        return false
    }

    func authenticate(
    onAuthSuccess: () -> Void,
    onAuthFailure: () -> Void
     ) {
        onAuthSuccess()
    }
}