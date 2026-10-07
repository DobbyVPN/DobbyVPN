import Foundation

/// Keeps the NetworkExtension stop callback behind Go-owned generation cleanup.
public enum IOSProviderStopBoundary {
    /// Stops the Go session, retains platform callbacks on cleanup failure,
    /// cancels path monitoring, and completes the OS stop only after those
    /// operations finish.
    @discardableResult
    public static func perform(
        stopGoSessionAndWait: () -> String,
        releasePlatformCallbacks: () -> Void,
        stopPathMonitoring: () -> Void,
        completeOSStop: (_ cleanupFailure: String) -> Void
    ) -> String {
        let failure = stopGoSessionAndWait()
        if failure.isEmpty {
            releasePlatformCallbacks()
        }
        stopPathMonitoring()
        completeOSStop(failure)
        return failure
    }
}
