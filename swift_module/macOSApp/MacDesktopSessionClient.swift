import DobbyNativeUI
import Darwin
import Foundation

final class MacDesktopSessionClient: DobbySessionClient {
    private let socketPath: String

    init(environment: [String: String] = ProcessInfo.processInfo.environment) {
        if let configured = environment["DOBBYVPN_CONTROL_SOCKET"], !configured.isEmpty {
            socketPath = configured
        } else if FileManager.default.fileExists(atPath: "/var/run/dobbyvpn/control.sock") {
            socketPath = "/var/run/dobbyvpn/control.sock"
        } else {
            socketPath = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent("Library/Application Support/DobbyVPN/control.sock").path
        }
    }

    var diagnosticPaths: [URL] {
        [URL(fileURLWithPath: "/Library/Logs/DobbyVPN/backend.jsonl")]
    }

    var version: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "Unknown"
    }

    var sourceCommit: String {
        Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A"
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        do {
            let request = try JSONSerialization.data(withJSONObject: ["method": method, "params": parameters])
            var payload = request
            payload.append(0x0A)
            return try exchange(payload)
        } catch {
            let message = error.localizedDescription.replacingOccurrences(of: "\\", with: "\\\\")
                .replacingOccurrences(of: "\"", with: "\\\"")
            return "{\"ok\":false,\"error\":{\"code\":\"PLATFORM_FAILED\",\"message\":\"\(message)\"}}"
        }
    }

    private func exchange(_ request: Data) throws -> String {
        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        defer { _ = close(descriptor) }

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let encodedPath = Array(socketPath.utf8)
        guard encodedPath.count < MemoryLayout.size(ofValue: address.sun_path) else {
            throw DobbyClientError.command(code: "PLATFORM_FAILED", message: "desktop control socket path is too long")
        }
        withUnsafeMutableBytes(of: &address.sun_path) { storage in
            storage.initializeMemory(as: UInt8.self, repeating: 0)
            storage.copyBytes(from: encodedPath)
        }
        let connected = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard connected == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }

        try request.withUnsafeBytes { bytes in
            guard let base = bytes.baseAddress else { return }
            var sent = 0
            while sent < bytes.count {
                let count = send(descriptor, base.advanced(by: sent), bytes.count - sent, 0)
                if count < 0 && errno == EINTR { continue }
                guard count > 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
                sent += count
            }
        }

        var response = Data()
        var buffer = [UInt8](repeating: 0, count: 16 * 1024)
        while response.count <= 8 * 1024 * 1024 {
            let count = recv(descriptor, &buffer, buffer.count, 0)
            if count < 0 && errno == EINTR { continue }
            guard count > 0 else { throw POSIXError(.init(rawValue: errno) ?? .ECONNRESET) }
            if let newline = buffer[..<count].firstIndex(of: 0x0A) {
                response.append(contentsOf: buffer[..<newline])
                guard let text = String(data: response, encoding: .utf8) else {
                    throw DobbyClientError.invalidResponse
                }
                return text
            }
            response.append(contentsOf: buffer[..<count])
        }
        throw DobbyClientError.invalidResponse
    }
}
