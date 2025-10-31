import app

class AuthenticationManagerImpl : AuthenticationManager {
    func authenticate(
    onAuthSuccess: () -> Void,
    onAuthFailure: () -> Void
     ) {
        onAuthSuccess()
    }
}