#if DOBBY_SIMULATOR_TEST
import CryptoKit
import Foundation
import IOSIntegration

/// A rendered-UI-only session stand-in for the provisioning-free Simulator app.
/// Subscription bytes still travel through normal URLSession TLS validation;
/// this client never changes trust evaluation and is not compiled into iosApp.
final class IOSSimulatorTestSessionClient: DobbySessionClient, @unchecked Sendable {
    /// Torturer provides the URL to XCTest only. The rendered test exercises
    /// Paste and deep links, so it forwards just the fixture marker and stderr
    /// seed flag to the app; these names also support a test-only restored URL.
    static let subscriptionURLEnvironmentKey = "DOBBY_IOS_TEST_SUBSCRIPTION_URL"
    static let fixtureRequiredEnvironmentKey = "DOBBY_IOS_TEST_FIXTURE_REQUIRED"
    static let seedCaptureEnvironmentKey = "DOBBY_SIMULATOR_TEST_SEED_STDERR_CAPTURE"

    private struct Profile {
        let index: Int
        let protocolName: String
        let description: String

        var json: [String: Any] {
            ["index": index, "protocol": protocolName, "description": description]
        }
    }

    private let lock = NSLock()
    private let sessionID = "simulator-\(UUID().uuidString.lowercased())"
    private let logDirectory = FileManager.default.temporaryDirectory
    private var sequence: Int64 = 0
    private var generation: Int64 = 0
    private var state = "IDLE"
    private var sourceURL = ""
    private var digest = ""
    private var profiles: [Profile] = []
    private var activeMode = ""
    private var activeIndex = -1
    private var activeDigest = ""
    private var activeProfile: Profile?
    private var configureRequests = 0
    private var requestCount = 0
    private var lastRequestStartedAtUptime: TimeInterval = 0
    private var startRequests = 0
    private var stopRequests = 0

    init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        if environment[Self.fixtureRequiredEnvironmentKey] == "1",
           let source = environment[Self.subscriptionURLEnvironmentKey],
           let components = URLComponents(string: source),
           components.scheme?.lowercased() == "https",
           !(components.host ?? "").isEmpty {
            sourceURL = source
        }
        if environment[Self.seedCaptureEnvironmentKey] == "1" {
            seedStderrCaptureIfNeeded()
        }
    }

    var diagnosticPaths: [URL] {
        [
            logDirectory.appendingPathComponent("ui_diagnostics.jsonl"),
            logDirectory.appendingPathComponent("app_logs.txt"),
            logDirectory.appendingPathComponent("tunnel_native.jsonl"),
            logDirectory.appendingPathComponent("go_app_logs.jsonl"),
            logDirectory.appendingPathComponent("go_app_logs.jsonl.stderr"),
            logDirectory.appendingPathComponent("go_tunnel_logs.jsonl"),
            logDirectory.appendingPathComponent("go_tunnel_logs.jsonl.stderr"),
        ]
    }

    var uiDiagnosticPath: URL { logDirectory.appendingPathComponent("ui_diagnostics.jsonl") }
    var version: String { Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown" }
    var sourceCommit: String { Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A" }

    /// JSON on the "Simulator test session state" accessibility element.
    /// Stable test keys: session_id, sequence, generation, configured,
    /// source_url, profile_count, configure_requests, request_count,
    /// start_requests, and stop_requests. request_count records URL fetch attempts.
    var accessibilityState: String {
        lock.lock()
        defer { lock.unlock() }
        let value: [String: Any] = [
            "session_id": sessionID,
            "sequence": sequence,
            "generation": generation,
            "configured": !profiles.isEmpty,
            "source_url": sourceURL,
            "profile_count": profiles.count,
            "configure_requests": configureRequests,
            "request_count": requestCount,
            "request_started_at_uptime": lastRequestStartedAtUptime,
            "start_requests": startRequests,
            "stop_requests": stopRequests,
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]),
              let text = String(data: data, encoding: .utf8) else { return "{}" }
        return text
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        switch method {
        case "Snapshot": return snapshot(parameters: parameters)
        case "Configure": return configure(parameters: parameters)
        case "Start": return start(parameters: parameters)
        case "Stop": return stop(parameters: parameters)
        default: return failure("INVALID_ARGUMENT", "Unsupported Simulator test session command")
        }
    }

    private func snapshot(parameters: [String: Any]) -> String {
        lock.lock()
        defer { lock.unlock() }
        if let requested = parameters["session_id"] as? String,
           !requested.isEmpty, requested != sessionID {
            return failure("NOT_FOUND", "Simulator test session is no longer available")
        }
        let configured = !profiles.isEmpty
        let active: Bool
        let activeProfileValue: Any
        if state == "CONNECTED", let activeProfile {
            active = true
            activeProfileValue = activeProfile.json
        } else {
            active = false
            activeProfileValue = NSNull()
        }
        let result: [String: Any] = [
            "session_id": sessionID,
            "sequence": sequence,
            "generation": generation,
            "state": state,
            "primary_action": state == "CONNECTED" ? "STOP" : (configured ? "START" : "NONE"),
            "configured": configured,
            "source_url": sourceURL,
            "source_error": "",
            "active_profile": activeProfileValue,
            "last_failure": NSNull(),
            "recovering": false,
            "digest": digest,
            "profiles": profiles.map(\.json),
            "active_digest": active ? activeDigest : "",
            "active_mode": active ? activeMode : "",
            "active_index": active ? activeIndex : -1,
            "pending_target": NSNull(),
            "can_switch": active,
        ]
        return success(result)
    }

    private func configure(parameters: [String: Any]) -> String {
        guard let source = parameters["source"] as? String,
              let expectedSequence = integer(parameters["expected_sequence"]),
              let requestedSession = parameters["session_id"] as? String else {
            return failure("INVALID_ARGUMENT", "Simulator test Configure request is incomplete")
        }
        guard let components = URLComponents(string: source),
              components.scheme?.lowercased() == "https",
              let host = components.host, !host.isEmpty,
              components.user == nil, components.password == nil,
              let subscriptionURL = components.url else {
            return failure("INVALID_ARGUMENT", "Enter an HTTPS subscription URL with a host")
        }

        lock.lock()
        configureRequests += 1
        guard requestedSession == sessionID else {
            lock.unlock()
            return failure("NOT_FOUND", "Simulator test session is no longer available")
        }
        guard expectedSequence == sequence else {
            lock.unlock()
            return failure("STALE_SEQUENCE", "Subscription request was superseded")
        }
        requestCount += 1
        lastRequestStartedAtUptime = ProcessInfo.processInfo.systemUptime
        lock.unlock()

        let body: Data
        do {
            body = try fetchSubscription(subscriptionURL)
        } catch {
            return failure("SOURCE_FAILED", "Subscription request failed: \(error.localizedDescription)")
        }
        guard body.count <= 1 << 20, let parsedProfiles = parseProfileSummaries(body), !parsedProfiles.isEmpty else {
            return failure("MALFORMED_CONFIG", "Subscription did not contain a supported profile")
        }
        let contentDigest = SHA256.hash(data: body).map { String(format: "%02x", $0) }.joined()

        lock.lock()
        defer { lock.unlock() }
        guard requestedSession == sessionID, expectedSequence == sequence else {
            return failure("STALE_SEQUENCE", "Subscription request was superseded")
        }
        profiles = parsedProfiles
        digest = contentDigest
        sourceURL = source
        if state != "CONNECTED" { state = "CONFIGURED" }
        sequence += 1
        return success(["sequence": sequence, "digest": digest, "profiles": profiles.map(\.json), "source_kind": "URL"])
    }

    private func start(parameters: [String: Any]) -> String {
        lock.lock()
        defer { lock.unlock() }
        guard parameters["session_id"] as? String == sessionID,
              integer(parameters["expected_sequence"]) == sequence,
              parameters["digest"] as? String == digest,
              !profiles.isEmpty else {
            return failure("STALE_SEQUENCE", "Simulator test selection no longer matches the loaded profiles")
        }
        let mode = parameters["mode"] as? String ?? ""
        let selected: Int
        switch mode {
        case "AUTO_SELECT":
            selected = 0
        case "PROFILE_INDEX":
            guard let requestedIndex = integer(parameters["index"]),
                  requestedIndex >= 0,
                  requestedIndex < Int64(profiles.count) else {
                return failure("INVALID_ARGUMENT", "Simulator test profile selection is invalid")
            }
            selected = Int(requestedIndex)
        default:
            return failure("INVALID_ARGUMENT", "Simulator test profile selection is invalid")
        }
        guard profiles.indices.contains(selected) else {
            return failure("INVALID_ARGUMENT", "Simulator test profile selection is invalid")
        }
        startRequests += 1
        generation += 1
        sequence += 1
        state = "CONNECTED"
        activeMode = mode
        activeIndex = selected
        activeDigest = digest
        activeProfile = profiles[selected]
        return success(["generation": generation, "sequence": sequence])
    }

    private func stop(parameters: [String: Any]) -> String {
        lock.lock()
        defer { lock.unlock() }
        guard parameters["session_id"] as? String == sessionID,
              integer(parameters["generation"]) == generation else {
            return failure("STALE_GENERATION", "Simulator test connection is no longer active")
        }
        stopRequests += 1
        sequence += 1
        state = profiles.isEmpty ? "IDLE" : "CONFIGURED"
        activeMode = ""
        activeIndex = -1
        activeDigest = ""
        activeProfile = nil
        return success(["sequence": sequence, "generation": generation])
    }

    private func fetchSubscription(_ url: URL) throws -> Data {
        let semaphore = DispatchSemaphore(value: 0)
        let resultLock = NSLock()
        var result: Result<Data, Error>?
        var request = URLRequest(url: url, cachePolicy: .reloadIgnoringLocalCacheData, timeoutInterval: 15)
        request.httpMethod = "GET"
        URLSession.shared.dataTask(with: request) { data, response, error in
            let outcome: Result<Data, Error>
            if let error {
                outcome = .failure(error)
            } else if let response = response as? HTTPURLResponse,
                      (200..<300).contains(response.statusCode),
                      response.url?.scheme?.lowercased() == "https",
                      let data {
                outcome = .success(data)
            } else {
                outcome = .failure(SessionError.invalidSubscriptionResponse)
            }
            resultLock.lock()
            result = outcome
            resultLock.unlock()
            semaphore.signal()
        }.resume()
        guard semaphore.wait(timeout: .now() + 20) == .success else {
            throw SessionError.subscriptionTimedOut
        }
        resultLock.lock()
        defer { resultLock.unlock() }
        guard let result else { throw SessionError.invalidSubscriptionResponse }
        return try result.get()
    }

    /// Parse only TOML array-table headers and descriptions for rendered UI
    /// fixtures. Production profile validation remains owned by the Go parser.
    private func parseProfileSummaries(_ data: Data) -> [Profile]? {
        guard let text = String(data: data, encoding: .utf8),
              let headerPattern = try? NSRegularExpression(
                pattern: #"(?m)^\s*\[\[(Outline|Xray|TrustTunnel)\]\]\s*$"#
              ),
              let descriptionPattern = try? NSRegularExpression(
                pattern: #"(?m)^\s*Description\s*=\s*"([^"]*)"\s*$"#
              ) else {
            return nil
        }
        let fullRange = NSRange(text.startIndex..<text.endIndex, in: text)
        let headers = headerPattern.matches(in: text, range: fullRange)
        guard !headers.isEmpty else { return nil }
        return headers.enumerated().map { offset, match in
            let protocolRange = match.range(at: 1)
            let protocolName = (text as NSString).substring(with: protocolRange).uppercased()
            let sectionStart = NSMaxRange(match.range)
            let sectionEnd = offset + 1 < headers.count ? headers[offset + 1].range.location : fullRange.length
            let section = (text as NSString).substring(with: NSRange(location: sectionStart, length: max(0, sectionEnd - sectionStart)))
            let descriptionRange = NSRange(section.startIndex..<section.endIndex, in: section)
            let descriptionMatch = descriptionPattern.firstMatch(in: section, range: descriptionRange)
            let description = descriptionMatch.map { (section as NSString).substring(with: $0.range(at: 1)) } ?? ""
            let uiProtocol = protocolName == "TRUSTTUNNEL" ? "TRUST_TUNNEL" : protocolName
            return Profile(index: offset, protocolName: uiProtocol, description: description)
        }
    }

    private func seedStderrCaptureIfNeeded() {
        let marker = logDirectory.appendingPathComponent("dobbyvpn-simulator-stderr-capture-seeded")
        guard !FileManager.default.fileExists(atPath: marker.path) else { return }
        let stream = logDirectory.appendingPathComponent("go_tunnel_logs.jsonl.stderr")
        do {
            try DiagnosticFiles.append(
                "Stderr capture initialized",
                event: "stderr.capture",
                level: "INFO",
                source: "tunnel",
                to: stream
            )
            try Data().write(to: marker, options: .atomic)
        } catch {
            let diagnostic = "Simulator test stderr.capture seed failed: \(String(reflecting: error))\n"
            try? FileHandle.standardError.write(contentsOf: Data(diagnostic.utf8))
        }
    }

    private func integer(_ value: Any?) -> Int64? {
        if let value = value as? Int64 { return value }
        if let value = value as? Int { return Int64(value) }
        if let value = value as? NSNumber { return value.int64Value }
        return nil
    }

    private func success(_ result: [String: Any]) -> String {
        encode(["ok": true, "result": result])
    }

    private func failure(_ code: String, _ message: String) -> String {
        encode(["ok": false, "error": ["code": code, "message": message]])
    }

    private func encode(_ value: [String: Any]) -> String {
        guard let data = try? JSONSerialization.data(withJSONObject: value, options: [.sortedKeys]),
              let text = String(data: data, encoding: .utf8) else {
            return #"{"ok":false,"error":{"code":"INTERNAL","message":"Simulator test response encoding failed"}}"#
        }
        return text
    }

    private enum SessionError: LocalizedError {
        case invalidSubscriptionResponse
        case subscriptionTimedOut

        var errorDescription: String? {
            switch self {
            case .invalidSubscriptionResponse: return "subscription server returned an invalid HTTPS response"
            case .subscriptionTimedOut: return "subscription request timed out"
            }
        }
    }
}
#endif
