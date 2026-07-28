/// Platform-neutral lifecycle policy compiled into the production CommonDI
/// target and directly by host-runnable Swift tests.
public enum NetworkExtensionState: Equatable {
    case invalid
    case disconnected
    case connecting
    case connected
    case reasserting
    case disconnecting

    public var presentedState: PresentedConnectionState {
        switch self {
        case .connected:
            return .connected
        case .connecting, .reasserting, .disconnecting:
            return .connecting
        case .invalid, .disconnected:
            return .disconnected
        }
    }
}

public enum PresentedConnectionState: Equatable {
    case disconnected
    case connecting
    case connected
}

/// Generation fence at the asynchronous NetworkExtension callback boundary.
public struct IOSLifecycleState: Equatable {
    public private(set) var generation: UInt64 = 0
    public private(set) var extensionState: NetworkExtensionState = .disconnected

    public init() {}

    @discardableResult
    public mutating func beginStart() -> UInt64 {
        generation &+= 1
        extensionState = .connecting
        return generation
    }

    @discardableResult
    public mutating func beginStop() -> UInt64 {
        generation &+= 1
        extensionState = .disconnecting
        return generation
    }

    @discardableResult
    public mutating func receive(
        _ state: NetworkExtensionState,
        generation candidate: UInt64
    ) -> Bool {
        guard generation == candidate else { return false }
        extensionState = state
        return true
    }

    public func isCurrent(_ candidate: UInt64) -> Bool {
        generation == candidate
    }
}

public enum IOSSessionRequestFailure: String, Equatable {
    case notConfigured = "NOT_CONFIGURED"
    case unsupported = "UNSUPPORTED"
}

/// Validation shared by the production session shell and exhaustive tests.
public enum IOSSessionRequestPolicy {
    public static func validateStart(
        configured: Bool,
        mode: String,
        index: Int
    ) -> IOSSessionRequestFailure? {
        guard configured else { return .notConfigured }
        guard mode == "AUTO_SELECT", index == 0 else { return .unsupported }
        return nil
    }
}

/// Deduplicates KMP observations while preserving a monotonic cursor.
public struct IOSStateObservation: Equatable {
    public private(set) var sequence: Int64 = 0
    private var lastState: String

    public init(initialState: String = "IDLE") {
        lastState = initialState
    }

    public mutating func observe(
        state: String,
        afterSequence: Int64
    ) -> (emit: Bool, nextSequence: Int64) {
        if state != lastState {
            sequence &+= 1
            lastState = state
        }
        return (sequence > afterSequence, sequence)
    }
}

public enum IOSStartAction: Equatable {
    case start
    case waitForTransition
    case stopThenRetry
    case retry
    case fail
}

/// Pure policy for the real `VpnManagerImpl` start/retry state machine.
public struct IOSStartPolicy {
    public let maximumRetries: Int

    public init(maximumRetries: Int = 120) {
        self.maximumRetries = maximumRetries
    }

    public func action(
        for status: NetworkExtensionState,
        retryAttempt: Int
    ) -> IOSStartAction {
        switch status {
        case .disconnected, .invalid:
            return .start
        case .connecting, .reasserting:
            return .waitForTransition
        case .connected:
            return retryAttempt < maximumRetries ? .stopThenRetry : .fail
        case .disconnecting:
            return retryAttempt < maximumRetries ? .retry : .fail
        }
    }
}
