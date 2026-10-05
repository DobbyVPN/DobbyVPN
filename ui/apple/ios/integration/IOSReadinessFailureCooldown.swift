import Foundation

/// Keeps passive provider readiness polling from retrying a recent failure on
/// every Snapshot request. Callers supply monotonic time so expiry is stable
/// across wall-clock changes and can be tested without waiting.
public struct IOSReadinessFailureCooldown: Equatable {
    public static let duration: TimeInterval = 5

    private var failure: String?
    private var retryAfter: TimeInterval?

    public init() {}

    public func cachedFailure(at monotonicTime: TimeInterval) -> String? {
        guard let failure, let retryAfter, monotonicTime < retryAfter else { return nil }
        return failure
    }

    public mutating func recordFailure(_ failure: String, at monotonicTime: TimeInterval) {
        self.failure = failure
        retryAfter = monotonicTime + Self.duration
    }

    public mutating func clear() {
        failure = nil
        retryAfter = nil
    }
}
