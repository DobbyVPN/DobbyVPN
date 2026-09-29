import Foundation

/// Containing-app side of the iOS session bridge. Go in the provider owns state.
public final class IOSSessionShell: NSObject {
    private let secrets = SharedKeychainSecretStore.shared
    private let manager: VpnManagerImpl
    private let logs = IOSAppCompositionRoot.logsRepository
    private let configurationLock = NSLock()

    init(manager: VpnManagerImpl) {
        self.manager = manager
        super.init()
    }

    /// Sends the frontend's method and parameters through the provider as JSON.
    /// Source bytes stay in a request-scoped Keychain mailbox because the
    /// NetworkExtension message is a control transport, not a secret channel.
    public func call(method: String, parameters: [String: Any]) -> String {
        let hasStartSource = method == "Start" && parameters["source"] is String
        let usesMailbox = method == "Configure" || hasStartSource
        let providerMethod = hasStartSource ? "StartWithSource" : method
        if usesMailbox { configurationLock.lock() }
        defer {
            if usesMailbox { configurationLock.unlock() }
        }

        let requestID = requestID(for: providerMethod)
        var forwardedParameters = parameters
        var hasConfigurationMailbox = false

        if usesMailbox {
            let source = parameters["source"] as? String ?? ""
            let mailbox: IOSConfigurationMailbox
            do {
                mailbox = try IOSConfigurationMailbox(
                    requestID: requestID,
                    configuration: Data(source.utf8)
                )
            } catch {
                return failure("INTERNAL", message: "source mailbox could not be encoded")
            }
            guard secrets.set(mailbox.encoded(), for: SharedKeychainSecretStore.sessionConfigurationMailboxKey) else {
                logs.writeLog(log: "iOS session source mailbox write returned failure request_id=\(requestID)")
                return failure("PLATFORM_FAILED", message: "source mailbox write returned failure")
            }
            hasConfigurationMailbox = true
            forwardedParameters.removeValue(forKey: "source")
        }

        let params: Data
        do {
            params = try JSONSerialization.data(
                withJSONObject: forwardedParameters,
                options: [.sortedKeys, .withoutEscapingSlashes]
            )
        } catch {
            logs.writeLog(
                log: "iOS session command encoding failed method=\(providerMethod):\n\(diagnosticErrorDescription(error))"
            )
            return failure("INVALID_ARGUMENT", message: "command parameters are invalid")
        }

        let result = executeResult(method: providerMethod, requestID: requestID, params: params)
        if hasConfigurationMailbox,
           result.isGoResult,
           let response = result.response.data(using: .utf8),
           IOSMailboxLifecycle.mayConsumeConfigurationResponse(response) {
            // The app is the sole mailbox consumer. Transport failures retain
            // this request's bytes for a later retry.
            consumeConfiguration(requestID: requestID)
        }
        return result.response
    }

    private func executeResult(
        method: String,
        requestID: String,
        params: Data
    ) -> (response: String, isGoResult: Bool) {
        do {
            let command = try IOSProviderCommand(method: method, requestID: requestID, params: params)
            let providerResponse = try IOSProviderResponse.decode(
                manager.sendProviderMessage(command.encoded()),
                expectedRequestID: requestID
            )
            // The provider envelope preserves exact bytes, but Go's C bridge
            // accepts UTF-8 text. Never feed replacement characters to Go:
            // malformed protocol data is a transport failure, and the full
            // byte sequence is retained reversibly in the native diagnostic.
            let response: String
            do {
                response = try IOSProviderPayload.decode(providerResponse.payload)
            } catch let error as IOSProviderPayloadError {
                logs.writeLog(
                    log: "iOS session provider payload decoding failed; reversible_payload=\(reversibleDiagnosticText(providerResponse.payload))\n\(diagnosticErrorDescription(error))"
                )
                return (failure("INTERNAL", message: "iOS session provider response was not valid UTF-8"), false)
            }
            return (response, providerResponse.kind == .go)
        } catch {
            logs.writeLog(
                log: "iOS session bridge failed method=\(method):\n\(diagnosticErrorDescription(error))"
            )
            return (failure("INTERNAL", message: "iOS session provider request failed"), false)
        }
    }

    private func requestID(for method: String) -> String {
        "ios-\(method)-\(UUID().uuidString)"
    }

    private func consumeConfiguration(requestID: String) {
        let key = SharedKeychainSecretStore.sessionConfigurationMailboxKey
        guard let bytes = secrets.data(for: key),
              let mailbox = try? IOSConfigurationMailbox.decode(bytes),
              mailbox.requestID == requestID else {
            logs.writeLog(log: "iOS session configuration mailbox identity did not match request_id=\(requestID)")
            return
        }
        secrets.remove(key)
    }

    internal static func decodeProviderPayload(_ payload: Data) throws -> String {
        try IOSProviderPayload.decode(payload)
    }

    private func failure(_ code: String, message: String) -> String {
        String(decoding: VpnManagerImpl.transportFailure(code, message: message), as: UTF8.self)
    }
}
