import Foundation

/// Containing-app side of the iOS session bridge. Go in the provider owns state.
public final class IOSSessionShell: NSObject {
    private let secrets = SharedKeychainSecretStore.shared
    private let manager: VpnManagerImpl
    private let logs = IOSAppCompositionRoot.logsRepository

    init(manager: VpnManagerImpl) {
        self.manager = manager
        super.init()
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
