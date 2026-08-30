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
/// This shell persists the opaque configuration in the shared encrypted
/// Keychain mailbox and transports authenticated fixed commands to the packet
/// tunnel. It never owns a session, generation, state, configured bit, or
/// event sequence; every successful response is the exact JSON returned by Go.
final class IOSSessionShell: NSObject, IosSessionBridge {
    private let secrets = SharedKeychainSecretStore.shared
    private let manager: VpnManagerImpl
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
        execute(operation: .recover, requestID: commandID)
    }

    func create(commandID: String) -> String {
        execute(operation: .create, requestID: commandID)
    }

    func configure(sessionID: String, commandID: String, rawConfig: KotlinByteArray) -> String {
        let raw = data(from: rawConfig)
        guard !raw.isEmpty else { return failure(IOSProviderMessageError.malformed.rawValue) }
        guard raw.count <= IOSProviderCommand.maximumConfigurationBytes else {
            return failure("SESSIONAPI_CONFIGURATION_TOO_LARGE")
        }
        guard secrets.set(raw, for: SharedKeychainSecretStore.sessionConfigurationMailboxKey) else {
            return failure("SECURE_STORAGE_FAILED")
        }
        let result = executeResult(
            operation: .configure,
            requestID: commandID,
            sessionID: sessionID
        )
        // A mailbox is consumed after any authenticated, syntactically valid
        // Go configure result, including a typed Go rejection. Transport,
        // authentication, and malformed responses retain it for recovery.
        if result.isGoResult,
           let response = result.response.data(using: .utf8),
           IOSMailboxLifecycle.mayConsumeConfigureResponse(response) {
            secrets.remove(SharedKeychainSecretStore.sessionConfigurationMailboxKey)
        }
        return result.response
    }

    func start(sessionID: String, commandID: String, mode: String, index: Int32) -> String {
        execute(
            operation: .start,
            requestID: commandID,
            sessionID: sessionID,
            mode: mode,
            index: index
        )
    }

    func stop(sessionID: String, commandID: String, generation: Int64) -> String {
        execute(
            operation: .stop,
            requestID: commandID,
            sessionID: sessionID,
            generation: generation
        )
    }

    func snapshot(sessionID: String) -> String {
        execute(operation: .snapshot, requestID: requestID(for: .snapshot), sessionID: sessionID)
    }

    func observe(sessionID: String, afterSequence: Int64) -> String {
        execute(
            operation: .observe,
            requestID: requestID(for: .observe),
            sessionID: sessionID,
            afterSequence: afterSequence
        )
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

    private func execute(
        operation: IOSProviderOperation,
        requestID: String,
        sessionID: String? = nil,
        generation: Int64? = nil,
        mode: String? = nil,
        index: Int32? = nil,
        afterSequence: Int64? = nil
    ) -> String {
        executeResult(
            operation: operation,
            requestID: requestID,
            sessionID: sessionID,
            generation: generation,
            mode: mode,
            index: index,
            afterSequence: afterSequence
        ).response
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
        guard let secret = secrets.randomData(for: SharedKeychainSecretStore.sessionBridgeHMACKey),
              let command = try? IOSProviderCommand(
                  operation: operation,
                  requestID: requestID,
                  sessionID: sessionID,
                  generation: generation,
                  mode: mode,
                  index: index,
                  afterSequence: afterSequence
              ),
              let bytes = try? command.encoded(using: secret),
              let providerResponse = try? IOSProviderResponse.decode(
                  manager.sendProviderMessage(bytes),
                  expectedRequestID: requestID,
                  using: secret
              ),
              let response = String(data: providerResponse.payload, encoding: .utf8) else {
            return (failure("SESSIONAPI_RESPONSE_INVALID"), false)
        }
        return (response, providerResponse.kind == .go)
    }

    private func requestID(for operation: IOSProviderOperation) -> String {
        "ios-\(operation.rawValue)-\(UUID().uuidString)"
    }

    private func data(from value: KotlinByteArray) -> Data {
        let count = Int(value.size)
        return Data((0..<count).map { UInt8(bitPattern: value.get(index: Int32($0))) })
    }

    private func failure(_ code: String) -> String {
        "{\"ok\":false,\"error\":{\"code\":\"\(code)\"}}"
    }
}
