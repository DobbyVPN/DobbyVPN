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
