import Foundation

/// Tracks real OS completions. A timeout only ends the caller's wait; it never
/// cancels an operation or relinquishes the settings owner.
public final class TunnelSettingsOwner {
    private struct Owner: Equatable {
        let session: String
        let generation: Int64
    }

    private final class Operation {
        let id = UUID()
        let owner: Owner
        let clearing: Bool
        var completed = false
        var error: Error?

        init(owner: Owner, clearing: Bool) {
            self.owner = owner
            self.clearing = clearing
        }
    }

    private let serial = NSLock()
    private let completion = NSCondition()
    private let operationLimit: TimeInterval
    private let diagnostic: (String) -> Void
    private var owner: Owner?
    private var pending: Operation?

    public init(operationLimit: TimeInterval = 10, diagnostic: @escaping (String) -> Void) {
        self.operationLimit = operationLimit
        self.diagnostic = diagnostic
    }

    public func apply(
        session: String, generation: Int64,
        operation: (@escaping (Error?) -> Void) -> Void
    ) -> Error? {
        serial.lock()
        defer { serial.unlock() }
        let requested = Owner(session: session, generation: generation)
        completion.lock()
        guard owner == nil else {
            completion.unlock()
            return failure("settings remain owned by another operation")
        }
        owner = requested
        completion.unlock()
        return perform(owner: requested, clearing: false, deadline: Date().addingTimeInterval(operationLimit), operation: operation)
    }

    public func release(
        session: String, generation: Int64, timeout: TimeInterval,
        operation: (@escaping (Error?) -> Void) -> Void
    ) -> Error? {
        let deadline = Date().addingTimeInterval(max(0, timeout))
        serial.lock()
        defer { serial.unlock() }
        let requested = Owner(session: session, generation: generation)
        completion.lock()
        defer { completion.unlock() }
        guard let current = owner else { return nil }
        guard current == requested else { return failure("settings release owner mismatch") }
        if let previous = pending {
            if let error = wait(previous, deadline: min(deadline, Date().addingTimeInterval(operationLimit))) {
                // A completed failed application may have partially installed
                // settings. Clear it through the same serialized owner.
                if !previous.completed { return error }
            }
            if previous.clearing && previous.completed && previous.error == nil { return nil }
        }
        guard deadline > Date() else { return failure("settings cleanup budget expired") }
        completion.unlock()
        let error = perform(owner: requested, clearing: true, deadline: min(deadline, Date().addingTimeInterval(operationLimit)), operation: operation)
        completion.lock()
        return error
    }

    private func perform(
        owner: Owner, clearing: Bool, deadline: Date,
        operation: (@escaping (Error?) -> Void) -> Void
    ) -> Error? {
        let active = Operation(owner: owner, clearing: clearing)
        completion.lock()
        pending = active
        completion.unlock()
        diagnostic("settings begin session=\(owner.session) generation=\(owner.generation) operation=\(active.id) clear=\(clearing)")
        operation { [self, active] error in
            completion.lock()
            // Identity fencing also protects against accidental duplicate OS
            // completions. The closure retains the owner until completion.
            guard pending === active, !active.completed else { completion.unlock(); return }
            active.error = error
            active.completed = true
            if active.clearing && error == nil && self.owner == active.owner { self.owner = nil }
            completion.broadcast()
            completion.unlock()
            diagnostic("settings completed session=\(owner.session) generation=\(owner.generation) operation=\(active.id) error=\(error.map { String(reflecting: $0) } ?? "none")")
        }
        completion.lock()
        defer { completion.unlock() }
        return wait(active, deadline: deadline)
    }

    // Called with completion locked; NSCondition releases it during the wait.
    private func wait(_ operation: Operation, deadline: Date) -> Error? {
        while !operation.completed {
            if !completion.wait(until: deadline) && !operation.completed {
                return failure("settings operation \(operation.id) timed out; OS completion remains pending")
            }
        }
        return operation.error
    }

    private func failure(_ message: String) -> Error {
        NSError(domain: "DobbyVPN.TunnelSettings", code: 1, userInfo: [NSLocalizedDescriptionKey: message])
    }
}
