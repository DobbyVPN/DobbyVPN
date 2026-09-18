import Foundation
import CoreFoundation

private func dobbyDarwinEventCallback(
    _ center: CFNotificationCenter?,
    _ observer: UnsafeMutableRawPointer?,
    _ name: CFNotificationName?,
    _ object: UnsafeRawPointer?,
    _ userInfo: CFDictionary?
) {
    guard let observer else { return }
    Unmanaged<IOSSessionShell>.fromOpaque(observer).takeUnretainedValue().signalDarwinEvent()
}

/// Containing-app side of the iOS session bridge. Go in the provider owns state.
public final class IOSSessionShell: NSObject {
    private let secrets = SharedKeychainSecretStore.shared
    private let manager: VpnManagerImpl
    private let logs = IOSAppCompositionRoot.logsRepository
    private let eventCondition = NSCondition()
    private var eventGeneration: UInt64 = 0
    private var deliveredEventGeneration: UInt64 = 0
    private let darwinEventName = CFNotificationName(rawValue: IOSDarwinEventSink.notificationName as CFString)
    private var darwinObserverPointer: UnsafeMutableRawPointer?

    init(manager: VpnManagerImpl) {
        self.manager = manager
        super.init()
        let observerPointer = Unmanaged.passUnretained(self).toOpaque()
        darwinObserverPointer = observerPointer
        CFNotificationCenterAddObserver(
            CFNotificationCenterGetDarwinNotifyCenter(),
            observerPointer,
            dobbyDarwinEventCallback,
            darwinEventName.rawValue,
            nil,
            .deliverImmediately
        )
    }

    deinit {
        if let darwinObserverPointer {
            CFNotificationCenterRemoveObserver(
                CFNotificationCenterGetDarwinNotifyCenter(),
                darwinObserverPointer,
                darwinEventName,
                nil
            )
        }
    }

    public func configure(sessionID: String, expectedSequence: Int64, rawConfig: Data) -> String {
        if let failure = storeConfiguration(rawConfig) {
            return failure
        }
        let result = executeResult(
            operation: .configure,
            requestID: requestID(for: .configure),
            sessionID: sessionID,
            expectedSequence: expectedSequence
        )
        consumeConfiguration(after: result)
        return result.response
    }

    public func start(sessionID: String, expectedSequence: Int64, mode: String, index: Int32) -> String {
        executeResult(
            operation: .start,
            requestID: requestID(for: .start),
            sessionID: sessionID,
            expectedSequence: expectedSequence,
            mode: mode,
            index: index
        ).response
    }

    public func stop(sessionID: String, generation: Int64) -> String {
        executeResult(
            operation: .stop,
            requestID: requestID(for: .stop),
            sessionID: sessionID,
            generation: generation
        ).response
    }

    public func snapshot(sessionID: String) -> String {
        executeResult(
            operation: .snapshot,
            requestID: requestID(for: .snapshot),
            sessionID: sessionID.isEmpty ? nil : sessionID
        ).response
    }

    public func reset(sessionID: String, expectedSequence: Int64) -> String {
        let result = executeResult(
            operation: .reset,
            requestID: requestID(for: .reset),
            sessionID: sessionID,
            expectedSequence: expectedSequence
        )
        if result.isGoResult,
           let response = result.response.data(using: .utf8),
           let root = try? JSONSerialization.jsonObject(with: response) as? [String: Any],
           root["ok"] as? Bool == true,
           root["result"] is [String: Any] {
            secrets.remove(SharedKeychainSecretStore.sessionConfigurationMailboxKey)
        }
        return result.response
    }

    public func awaitEvent(timeoutMillis: Int64) -> Bool {
        eventCondition.lock()
        let deadline = Date().addingTimeInterval(max(0, Double(timeoutMillis) / 1000.0))
        while eventGeneration == deliveredEventGeneration {
            if !eventCondition.wait(until: deadline) && Date() >= deadline {
                eventCondition.unlock()
                return false
            }
        }
        deliveredEventGeneration = eventGeneration
        eventCondition.unlock()
        return true
    }

    fileprivate func signalDarwinEvent() {
        eventCondition.lock()
        eventGeneration &+= 1
        eventCondition.broadcast()
        eventCondition.unlock()
    }

    private func storeConfiguration(_ raw: Data) -> String? {
        guard !raw.isEmpty else { return failure("MALFORMED_CONFIG", message: "configuration is blank") }
        guard secrets.set(raw, for: SharedKeychainSecretStore.sessionConfigurationMailboxKey) else {
            logs.writeLog(log: "iOS session configuration mailbox write returned failure")
            return failure("PLATFORM_FAILED", message: "configuration mailbox write returned failure")
        }
        return nil
    }

    private func consumeConfiguration(after result: (response: String, isGoResult: Bool)) {
        if result.isGoResult,
           let response = result.response.data(using: .utf8),
           IOSMailboxLifecycle.mayConsumeConfigurationResponse(response) {
            secrets.remove(SharedKeychainSecretStore.sessionConfigurationMailboxKey)
        }
    }

    private func executeResult(
        operation: IOSProviderOperation,
        requestID: String,
        sessionID: String? = nil,
        generation: Int64? = nil,
        expectedSequence: Int64? = nil,
        mode: String? = nil,
        index: Int32? = nil
    ) -> (response: String, isGoResult: Bool) {
        do {
            // An empty Go owner is the documented first-call form. Do not put
            // an empty identifier on the wire: omission lets the provider
            // attach to its process-local manager and return the allocated
            // opaque owner in the next snapshot.
            let normalizedSessionID = sessionID?.isEmpty == true ? nil : sessionID
            let command = try IOSProviderCommand(
                operation: operation,
                requestID: requestID,
                sessionID: normalizedSessionID,
                generation: generation,
                mode: mode,
                index: index,
                expectedSequence: expectedSequence
            )
            let bytes = try command.encoded()
            let providerResponse = try IOSProviderResponse.decode(
                manager.sendProviderMessage(bytes),
                expectedRequestID: requestID
            )
            guard let response = String(data: providerResponse.payload, encoding: .utf8) else {
                throw IOSProviderMessageError.malformed
            }
            return (response, providerResponse.kind == .go)
        } catch {
            logs.writeLog(log: "iOS session bridge failed operation=\(operation.rawValue)")
            return (failure("INTERNAL", message: "iOS session provider request failed"), false)
        }
    }

    private func requestID(for operation: IOSProviderOperation) -> String {
        "ios-\(operation.rawValue)-\(UUID().uuidString)"
    }

    private func failure(_ code: String, message: String) -> String {
        String(decoding: VpnManagerImpl.transportFailure(code, message: message), as: UTF8.self)
    }
}
