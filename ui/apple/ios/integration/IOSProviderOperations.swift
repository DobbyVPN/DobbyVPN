import Foundation

/// The containing app's actual encoded command exchange. The transport closure
/// is VpnManagerImpl in production and a controlled in-process boundary in tests.
public enum IOSSessionExchange {
    public static func execute(
        method: String, requestID: String, params: Data, trace: Bool,
        send: (Data) throws -> Data,
        failure: (String, String) -> String,
        log: (String, String) -> Void
    ) -> (response: String, isGoResult: Bool) {
        let started = ProcessInfo.processInfo.systemUptime
        var stage = "encode"
        do {
            let command = try IOSProviderCommand(method: method, requestID: requestID, params: params)
            if trace { log("INFO", "[session] command begin \(command.diagnosticIdentity)") }
            stage = "provider-message"
            let providerResponse = try IOSProviderResponse.decode(send(command.encoded()), expectedRequestID: requestID)
            stage = "decode-payload"
            let response: String
            do {
                response = try IOSProviderPayload.decode(providerResponse.payload)
            } catch let error as IOSProviderPayloadError {
                log("ERROR", "iOS session provider payload decoding failed request_id=\(requestID) method=\(method) "
                    + "stage=\(stage) elapsed=\(ProcessInfo.processInfo.systemUptime - started); "
                    + "reversible_payload=\(reversibleDiagnosticText(providerResponse.payload))\n\(diagnosticErrorDescription(error))")
                return (failure("INTERNAL", "iOS session provider response was not valid UTF-8"), false)
            }
            if providerResponse.kind != .go {
                log("ERROR", "[session] command transport failure \(command.diagnosticIdentity) stage=\(stage) elapsed=\(ProcessInfo.processInfo.systemUptime - started) response=\(response)")
            } else if trace {
                log("INFO", "[session] command end \(command.diagnosticIdentity) kind=\(providerResponse.kind) elapsed=\(ProcessInfo.processInfo.systemUptime - started)")
            }
            return (response, providerResponse.kind == .go)
        } catch {
            log("ERROR", "iOS session bridge failed request_id=\(requestID) method=\(method) stage=\(stage) elapsed=\(ProcessInfo.processInfo.systemUptime - started):\n\(diagnosticErrorDescription(error))")
            return (failure("INTERNAL", "iOS session provider request failed"), false)
        }
    }
}

/// The provider's actual command decoder, mailbox fence, Go dispatch and
/// response envelope. NetworkExtension supplies these operations in production.
public enum IOSProviderCommandHandler {
    public static func dispatch(
        _ messageData: Data, trace: Bool, tunnelID: String,
        loadMailbox: () -> Data?,
        callGo: (String, String, Data, Data?) -> Data,
        failure: (String, String) -> Data,
        log: (String, String) -> Void
    ) -> Data {
        let command: IOSProviderCommand
        do { command = try IOSProviderCommand.decode(messageData) }
        catch {
            log("ERROR", "[tunnel:\(tunnelID)] provider command decode failed: \(String(reflecting: error))")
            return failure("INTERNAL", String(reflecting: error))
        }
        func response(_ kind: IOSProviderResponseKind, _ payload: Data) -> Data {
            do { return try IOSProviderResponse(requestID: command.requestID, kind: kind, payload: payload).encoded() }
            catch {
                log("ERROR", "[tunnel:\(tunnelID)] provider response encoding failed request_id=\(command.requestID): \(String(reflecting: error))")
                return failure("PLATFORM_FAILED", String(reflecting: error))
            }
        }
        let started = ProcessInfo.processInfo.systemUptime
        let identity = command.diagnosticIdentity
        var stage = "mailbox"
        var completed = false
        if trace { log("INFO", "[tunnel:\(tunnelID)] provider command begin \(identity)") }
        defer {
            if trace || !completed { log(completed ? "INFO" : "ERROR", "[tunnel:\(tunnelID)] provider command end \(identity) stage=\(stage) completed=\(completed) elapsed=\(ProcessInfo.processInfo.systemUptime - started)") }
        }
        var configuration: Data?
        if command.method == "Configure" || command.method == "StartWithSource" {
            guard let bytes = loadMailbox() else {
                log("INFO", "[tunnel:\(tunnelID)] configuration mailbox is unavailable request_id=\(command.requestID)")
                return response(.transport, failure("PLATFORM_FAILED", "configuration mailbox is unavailable for this request"))
            }
            let mailbox: IOSConfigurationMailbox
            do { mailbox = try IOSConfigurationMailbox.decode(bytes) }
            catch {
                log("ERROR", "[tunnel:\(tunnelID)] configuration mailbox decode failed request_id=\(command.requestID):\n\(diagnosticErrorDescription(error))")
                return response(.transport, failure("PLATFORM_FAILED", "configuration mailbox is malformed"))
            }
            guard mailbox.requestID == command.requestID else {
                log("INFO", "[tunnel:\(tunnelID)] configuration mailbox request mismatch command_id=\(command.requestID) mailbox_id=\(mailbox.requestID)")
                return response(.transport, failure("PLATFORM_FAILED", "configuration mailbox is unavailable for this request"))
            }
            configuration = mailbox.configuration
        }
        stage = "go-dispatch"
        if trace { log("INFO", "[tunnel:\(tunnelID)] Go dispatch begin \(identity)") }
        let result = callGo(command.method == "StartWithSource" ? "Start" : command.method, command.requestID, command.params, configuration)
        let outcome = ((try? JSONSerialization.jsonObject(with: result)) as? [String: Any])?["ok"] as? Bool
        if outcome != true {
            log("ERROR", "[tunnel:\(tunnelID)] Go dispatch failed \(identity) stage=\(stage) elapsed=\(ProcessInfo.processInfo.systemUptime - started) response=\(reversibleDiagnosticText(result))")
        } else if trace {
            log("INFO", "[tunnel:\(tunnelID)] Go dispatch end \(identity) ok=true elapsed=\(ProcessInfo.processInfo.systemUptime - started)")
        }
        completed = true
        return response(.go, result)
    }
}
