import Foundation

/// Errors raised when a provider envelope contains bytes that cannot be
/// represented as the UTF-8 text consumed by the Go bridge.
internal enum IOSProviderPayloadError: Error, Equatable {
    case invalidUTF8(hex: String)
}

/// Pure provider-payload decoding shared by the production iOS shell and the
/// platform-neutral lifecycle tests.
internal enum IOSProviderPayload {
    static func decode(_ payload: Data) throws -> String {
        guard let response = String(data: payload, encoding: .utf8) else {
            let hex = payload.map { String(format: "%02x", $0) }.joined()
            throw IOSProviderPayloadError.invalidUTF8(hex: hex)
        }
        return response
    }
}

/// Preserve invalid diagnostic bytes without silently replacing them with
/// U+FFFD before the native export path receives them.
internal func reversibleDiagnosticText(_ data: Data) -> String {
    if let text = String(data: data, encoding: .utf8) {
        return text
    }
    let hex = data.map { String(format: "%02x", $0) }.joined()
    return "[invalid-utf8-hex:\(hex)]"
}

/// Render the complete NSError details and its underlying cause chain for the
/// exported native diagnostic log. `localizedDescription` alone often drops
/// the domain, code, and NetworkExtension's underlying failure information.
internal func diagnosticErrorDescription(_ error: Error) -> String {
    var visited = Set<ObjectIdentifier>()

    func describe(_ error: Error, indentation: String) -> String {
        let nsError = error as NSError
        let identity = ObjectIdentifier(nsError)
        guard visited.insert(identity).inserted else {
            return "\(indentation)[underlying error cycle: \(nsError.domain)(\(nsError.code))]"
        }

        var lines = [
            "\(indentation)error=\(String(reflecting: error))",
            "\(indentation)domain=\(nsError.domain)",
            "\(indentation)code=\(nsError.code)",
            "\(indentation)localizedDescription=\(nsError.localizedDescription)",
        ]
        for key in nsError.userInfo.keys.sorted() where key != NSUnderlyingErrorKey {
            if let value = nsError.userInfo[key] {
                lines.append("\(indentation)userInfo[\(key)]=\(String(reflecting: value))")
            }
        }
        if let cause = nsError.userInfo[NSUnderlyingErrorKey] as? Error {
            lines.append("\(indentation)underlying:")
            lines.append(describe(cause, indentation: indentation + "  "))
        }
        return lines.joined(separator: "\n")
    }

    return describe(error, indentation: "")
}
