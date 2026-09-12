import Foundation
import app
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

/// Containing-app side of the iOS SessionV2 boundary.
///
/// This shell persists the opaque configuration in the shared Keychain
/// mailbox and transports fixed commands to the packet
/// tunnel. It never owns a session, generation, state, configured bit, or
/// event sequence; every successful response is the exact JSON returned by Go.
final class IOSSessionShell: NSObject, IosSessionBridge {
    private let secrets = SharedKeychainSecretStore.shared
    private let manager: VpnManagerImpl
    private let logs = NativeModuleHolder.logsRepository
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

    func recover(commandID: String) -> String {
        executeResult(operation: .recover, requestID: commandID).response
    }

    func create(commandID: String) -> String {
        executeResult(operation: .create, requestID: commandID).response
    }

    func configure(sessionID: String, commandID: String, rawConfig: KotlinByteArray) -> String {
        let raw = data(from: rawConfig)
        guard !raw.isEmpty else {
            return failure("MALFORMED_CONFIG", message: "configuration is blank")
        }
        guard secrets.set(raw, for: SharedKeychainSecretStore.sessionConfigurationMailboxKey) else {
            logs.writeLog(log: "iOS session configuration mailbox write returned failure")
            return failure("PLATFORM_FAILED", message: "configuration mailbox write returned failure")
        }
        let result = executeResult(
            operation: .configure,
            requestID: commandID,
            sessionID: sessionID
        )
        // A mailbox is consumed after any syntactically valid
        // Go configure result, including a typed Go rejection. Transport,
        // and malformed responses retain it for recovery.
        if result.isGoResult,
           let response = result.response.data(using: .utf8),
           IOSMailboxLifecycle.mayConsumeConfigureResponse(response) {
            secrets.remove(SharedKeychainSecretStore.sessionConfigurationMailboxKey)
        }
        return result.response
    }

    func start(sessionID: String, commandID: String, mode: String, index: Int32) -> String {
        executeResult(
            operation: .start,
            requestID: commandID,
            sessionID: sessionID,
            mode: mode,
            index: index
        ).response
    }

    func stop(sessionID: String, commandID: String, generation: Int64) -> String {
        executeResult(
            operation: .stop,
            requestID: commandID,
            sessionID: sessionID,
            generation: generation
        ).response
    }

    func snapshot(sessionID: String) -> String {
        executeResult(operation: .snapshot, requestID: requestID(for: .snapshot), sessionID: sessionID).response
    }

    func observe(sessionID: String, afterSequence: Int64) -> String {
        executeResult(
            operation: .observe,
            requestID: requestID(for: .observe),
            sessionID: sessionID,
            afterSequence: afterSequence
        ).response
    }

    func destroy(sessionID: String) -> String {
        let result = executeResult(operation: .destroy, requestID: requestID(for: .destroy), sessionID: sessionID)
        guard result.isGoResult,
              let response = result.response.data(using: .utf8),
              IOSMailboxLifecycle.isSuccessfulGoResponse(response) else { return result.response }
        // Go must confirm destruction before the control provider or mailbox
        // is removed. This ordering prevents a timeout from losing recovery.
        manager.stopControlProvider()
        secrets.remove(SharedKeychainSecretStore.sessionConfigurationMailboxKey)
        return result.response
    }

    /// Blocks on the cross-process Darwin wake channel. A timeout is only a
    /// cancellation/recovery boundary; it never triggers steady-state polling.
    func awaitEvent(timeoutMillis: Int64) -> Bool {
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

    private func executeResult(
        operation: IOSProviderOperation,
        requestID: String,
        sessionID: String? = nil,
        generation: Int64? = nil,
        mode: String? = nil,
        index: Int32? = nil,
        afterSequence: Int64? = nil
    ) -> (response: String, isGoResult: Bool) {
        do {
            let command = try IOSProviderCommand(
                operation: operation,
                requestID: requestID,
                sessionID: sessionID,
                generation: generation,
                mode: mode,
                index: index,
                afterSequence: afterSequence
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
            logs.writeLog(
                log: "iOS session bridge failed operation=\(operation.rawValue): \(String(reflecting: error))"
            )
            return (failure("INTERNAL", message: String(reflecting: error)), false)
        }
    }

    private func requestID(for operation: IOSProviderOperation) -> String {
        "ios-\(operation.rawValue)-\(UUID().uuidString)"
    }

    private func data(from value: KotlinByteArray) -> Data {
        let count = Int(value.size)
        return Data((0..<count).map { UInt8(bitPattern: value.get(index: Int32($0))) })
    }

    private func failure(_ code: String, message: String) -> String {
        String(decoding: VpnManagerImpl.transportFailure(code, message: message), as: UTF8.self)
    }
}
