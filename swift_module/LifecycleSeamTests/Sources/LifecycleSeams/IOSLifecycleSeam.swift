import Foundation

/// A platform-neutral representation of the NetworkExtension status values
/// relevant to UI reconciliation. Production maps `NEVPNStatus` to these
/// semantics; the seam keeps that policy deterministic and independently
/// testable.
public enum NetworkExtensionState: Equatable {
    case invalid
    case disconnected
    case connecting
    case connected
    case reasserting
    case disconnecting
}

public enum PresentedConnectionState: Equatable {
    case disconnected
    case connecting
    case connected
}

/// Models the generation fence required at the platform callback boundary.
/// Diagnostic health values are intentionally not used to derive the
/// presented state: only NetworkExtension can authoritatively do that.
public struct IOSLifecycleSeam {
    public private(set) var activeGeneration: UInt64 = 0
    public private(set) var extensionState: NetworkExtensionState = .disconnected
    public private(set) var diagnosticHealthState: PresentedConnectionState = .disconnected

    public init() {}

    @discardableResult
    public mutating func beginStart() -> UInt64 {
        activeGeneration &+= 1
        return activeGeneration
    }

    /// A stop is also a new generation, invalidating delayed start/probe
    /// completions before the extension reports final teardown.
    @discardableResult
    public mutating func beginStop() -> UInt64 {
        activeGeneration &+= 1
        extensionState = .disconnecting
        return activeGeneration
    }

    @discardableResult
    public mutating func receiveExtensionState(
        _ state: NetworkExtensionState,
        generation: UInt64
    ) -> Bool {
        guard generation == activeGeneration else { return false }
        extensionState = state
        return true
    }

    @discardableResult
    public mutating func receiveDiagnosticHealth(
        _ state: PresentedConnectionState,
        generation: UInt64
    ) -> Bool {
        guard generation == activeGeneration else { return false }
        diagnosticHealthState = state
        return true
    }

    public var presentedState: PresentedConnectionState {
        switch extensionState {
        case .connected:
            return .connected
        case .connecting, .reasserting, .disconnecting:
            return .connecting
        case .invalid, .disconnected:
            return .disconnected
        }
    }
}

/// Models the distinction between a Go command reply and the completion that
/// NetworkExtension may safely report to the OS. A Start reply only carries a
/// generation; CONNECTED remains a later, correlated observation. Likewise a
/// Stop reply is not cleanup completion.
public struct IOSSessionCommandSeam {
    public private(set) var generation: UInt64 = 0
    public private(set) var startAccepted = false
    public private(set) var cleanupComplete = true
    public private(set) var state: NetworkExtensionState = .disconnected

    public init() {}

    public mutating func acceptStart(generation: UInt64) {
        self.generation = generation
        startAccepted = true
        cleanupComplete = false
        state = .connecting
    }

    public mutating func acceptStop(generation: UInt64) -> Bool {
        guard generation == self.generation else { return false }
        state = .disconnecting
        cleanupComplete = false
        return true
    }

    public mutating func observe(_ state: NetworkExtensionState, generation: UInt64, cleanupComplete: Bool) -> Bool {
        guard generation == self.generation else { return false }
        self.state = state
        self.cleanupComplete = cleanupComplete
        return true
    }

    public var startCompleted: Bool { startAccepted && state == .connected }
    public var stopCompleted: Bool { cleanupComplete && state == .disconnected }
}

public enum IOSSessionFailure: String, Equatable {
    case malformedConfiguration = "MALFORMED_CONFIG"
    case secureStorageFailed = "SECURE_STORAGE_FAILED"
    case notConfigured = "NOT_CONFIGURED"
    case unsupported = "UNSUPPORTED"
    case staleGeneration = "STALE_GENERATION"
}

public enum IOSSessionCommandResult: Equatable {
    case success(generation: UInt64?)
    case failure(IOSSessionFailure)
}

/// Platform-neutral command policy used to exhaustively test the containing
/// app's session shell. Storage and NetworkExtension remain adapters; this
/// model owns validation, generation correlation, observation sequencing, and
/// the rule that destruction removes configuration.
public struct IOSSessionShellSeam {
    public private(set) var configured = false
    public private(set) var generation: UInt64 = 0
    public private(set) var state: NetworkExtensionState = .disconnected
    public private(set) var sequence: UInt64 = 0
    private var lastObservedState: NetworkExtensionState = .disconnected

    public init() {}

    public mutating func configure(hasBytes: Bool, storageSucceeded: Bool) -> IOSSessionCommandResult {
        guard hasBytes else { return .failure(.malformedConfiguration) }
        guard storageSucceeded else { return .failure(.secureStorageFailed) }
        configured = true
        return .success(generation: nil)
    }

    public mutating func start(mode: String, index: Int) -> IOSSessionCommandResult {
        guard configured else { return .failure(.notConfigured) }
        guard mode == "AUTO_SELECT", index == 0 else { return .failure(.unsupported) }
        generation &+= 1
        state = .connecting
        return .success(generation: generation)
    }

    public mutating func stop(generation candidate: UInt64) -> IOSSessionCommandResult {
        guard candidate > 0, candidate == generation else {
            return .failure(.staleGeneration)
        }
        generation &+= 1
        state = .disconnecting
        return .success(generation: candidate)
    }

    @discardableResult
    public mutating func receive(_ newState: NetworkExtensionState, generation candidate: UInt64) -> Bool {
        guard candidate == generation else { return false }
        state = newState
        return true
    }

    public mutating func observe(afterSequence: UInt64) -> (events: [NetworkExtensionState], nextSequence: UInt64) {
        if state != lastObservedState {
            sequence &+= 1
            lastObservedState = state
        }
        return sequence > afterSequence ? ([state], sequence) : ([], sequence)
    }

    public mutating func destroy() {
        configured = false
    }

    public var cleanupComplete: Bool {
        state == .disconnected || state == .invalid
    }
}

public enum IOSStartAction: Equatable {
    case start
    case waitForTransition
    case stopThenRetry
    case retry
    case fail
}

/// Pure decision seam for `VpnManagerImpl` start behavior. Delayed execution
/// carries the generation returned by `scheduleRetry`; a later start or stop
/// invalidates it before any NetworkExtension call can occur.
public struct IOSStartRetrySeam {
    public let maximumRetries: Int
    public private(set) var generation: UInt64 = 0

    public init(maximumRetries: Int = 120) {
        self.maximumRetries = maximumRetries
    }

    @discardableResult
    public mutating func begin() -> UInt64 {
        generation &+= 1
        return generation
    }

    public mutating func invalidate() {
        generation &+= 1
    }

    public func action(for status: NetworkExtensionState, retryAttempt: Int) -> IOSStartAction {
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

    public func retryIsCurrent(_ candidate: UInt64) -> Bool {
        candidate == generation
    }
}
