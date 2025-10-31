import app

class AuthenticationManagerImpl : AuthenticationManager {
    func authenticate(onAuthSuccess: () -> Void) {
        onAuthSuccess()
    }
}