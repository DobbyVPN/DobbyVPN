import Foundation

public struct IOSProviderSessionSnapshot: Equatable {
    public let generation: Int64
    public let state: String
    public let cleanupComplete: Bool

    public init(generation: Int64, state: String, cleanupComplete: Bool) {
        self.generation = generation
        self.state = state
        self.cleanupComplete = cleanupComplete
    }
}

public protocol IOSProviderSessionClient {
    func create() throws -> String
    func configure(sessionID: String, rawConfiguration: Data) throws
    func start(sessionID: String) throws -> Int64
    func snapshot(sessionID: String) throws -> IOSProviderSessionSnapshot
    func stop(sessionID: String, generation: Int64) throws
    func destroy(sessionID: String) throws
}

public protocol IOSSessionClock {
    var now: Date { get }
    func sleep(seconds: TimeInterval) async throws
}

public struct IOSSystemSessionClock: IOSSessionClock {
    public init() {}
    public var now: Date { Date() }

    public func sleep(seconds: TimeInterval) async throws {
        let nanoseconds = UInt64(max(0, seconds) * 1_000_000_000)
        try await Task.sleep(nanoseconds: nanoseconds)
    }
}

public enum IOSProviderSessionError: String, Error, LocalizedError, Equatable {
    case cleanupPending = "SESSIONAPI_CLEANUP_PENDING"
    case failed = "SESSIONAPI_FAILED"
    case timeout = "SESSIONAPI_TIMEOUT"
    case cleanupTimeout = "SESSIONAPI_CLEANUP_TIMEOUT"
    case malformed = "SESSIONAPI_MALFORMED"

    public var errorDescription: String? { rawValue }
}

/// Transactional owner of the extension-process Go session handle.
///
/// Calls are serialized by `PacketTunnelProvider`. A failed teardown retains
/// the exact session/generation so a retry cannot create a second TUN owner.
public final class IOSProviderSessionCoordinator {
    public private(set) var sessionID: String?
    public private(set) var generation: Int64 = 0

    private let client: IOSProviderSessionClient
    private let clock: IOSSessionClock
    private let pollInterval: TimeInterval
    private let timeout: TimeInterval

    public init(
        client: IOSProviderSessionClient,
        clock: IOSSessionClock = IOSSystemSessionClock(),
        pollInterval: TimeInterval = 0.1,
        timeout: TimeInterval = 30
    ) {
        self.client = client
        self.clock = clock
        self.pollInterval = pollInterval
        self.timeout = timeout
    }

    public func start(rawConfiguration: Data) async throws {
        guard sessionID == nil else {
            throw IOSProviderSessionError.cleanupPending
        }

        let session = try client.create()
        guard !session.isEmpty else {
            throw IOSProviderSessionError.malformed
        }
        sessionID = session
        generation = 0

        do {
            try client.configure(
                sessionID: session,
                rawConfiguration: rawConfiguration
            )
            let startedGeneration = try client.start(sessionID: session)
            guard startedGeneration > 0 else {
                throw IOSProviderSessionError.malformed
            }
            generation = startedGeneration
            try await waitForConnected()
        } catch {
            do {
                try await stop()
            } catch {
                throw IOSProviderSessionError.cleanupPending
            }
            throw error
        }
    }

    public func stop() async throws {
        guard let session = sessionID else { return }

        if generation <= 0 {
            try client.destroy(sessionID: session)
            clear()
            return
        }

        let beforeStop = try client.snapshot(sessionID: session)
        let alreadyClean = matchingCleanup(beforeStop)
        if !alreadyClean {
            do {
                try client.stop(sessionID: session, generation: generation)
            } catch {
                let afterRejectedStop = try client.snapshot(sessionID: session)
                guard matchingCleanup(afterRejectedStop) else { throw error }
            }
            try await waitForCleanup()
        }

        try client.destroy(sessionID: session)
        clear()
    }

    /// Applies the same strict cleanup path when NetworkExtension reports an
    /// unexpected provider exit.  A subsequent start cannot overlap the old
    /// generation or reuse its session handle.
    public func cleanupAfterUnexpectedTermination() async throws {
        try await stop()
    }

    private func waitForConnected() async throws {
        guard let session = sessionID else {
            throw IOSProviderSessionError.malformed
        }
        let deadline = clock.now.addingTimeInterval(timeout)
        while clock.now < deadline {
            let current = try client.snapshot(sessionID: session)
            if current.generation == generation && current.state == "CONNECTED" {
                return
            }
            if current.generation == generation && current.state == "FAILED" {
                throw IOSProviderSessionError.failed
            }
            try await clock.sleep(seconds: pollInterval)
        }
        throw IOSProviderSessionError.timeout
    }

    private func waitForCleanup() async throws {
        guard let session = sessionID else {
            throw IOSProviderSessionError.malformed
        }
        let deadline = clock.now.addingTimeInterval(timeout)
        while clock.now < deadline {
            if matchingCleanup(try client.snapshot(sessionID: session)) {
                return
            }
            try await clock.sleep(seconds: pollInterval)
        }
        throw IOSProviderSessionError.cleanupTimeout
    }

    private func matchingCleanup(
        _ snapshot: IOSProviderSessionSnapshot
    ) -> Bool {
        snapshot.generation == generation && snapshot.cleanupComplete
    }

    private func clear() {
        sessionID = nil
        generation = 0
    }
}
