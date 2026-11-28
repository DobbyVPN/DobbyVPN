import Foundation

/// Builds the user-facing message for a failed subscription load without
/// printing the subscription link, its host, or its path and token. The error
/// dialog's Copy action copies this exact string, so redaction happens here.
func subscriptionLoadFailureMessage(
    source: String,
    accepted: String,
    configured: Bool,
    sameSource: Bool,
    detail: String
) -> String {
    let owner = configured && !sameSource && !accepted.isEmpty
        ? " Showing profiles from the previously accepted subscription." : ""
    let scrubbed = redactSubscriptionReferences(in: detail, secrets: [source, accepted])
    return "Could not load the subscription link: \(scrubbed).\(owner)"
}

/// Replaces every occurrence of the given subscription references and their
/// hosts inside a backend failure message. Failure metadata such as an HTTP
/// status, an error code or the format-help link stays readable.
private func redactSubscriptionReferences(in text: String, secrets: [String]) -> String {
    var scrubbed = text
    for secret in secrets where !secret.isEmpty {
        scrubbed = scrubbed.replacingOccurrences(
            of: secret, with: "the subscription link", options: .caseInsensitive)
    }
    for secret in secrets where !secret.isEmpty {
        guard let host = URL(string: secret)?.host, !host.isEmpty else { continue }
        scrubbed = scrubbed.replacingOccurrences(
            of: host, with: "the subscription server", options: .caseInsensitive)
    }
    return scrubbed
}
