import Darwin
import Foundation

/// One nonblocking exchange, with a short connect deadline inside the request budget.
struct UnixControlSocket {
    let path: String
    var connectTimeout: TimeInterval = 3
    var requestTimeout: TimeInterval = 120

    func exchange(_ request: Data) throws -> String {
        let start = ProcessInfo.processInfo.systemUptime
        let deadline = start + requestTimeout
        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw currentError() }
        defer { _ = close(descriptor) }
        guard fcntl(descriptor, F_SETFL, O_NONBLOCK) == 0,
              fcntl(descriptor, F_SETFD, FD_CLOEXEC) == 0 else { throw currentError() }
        var noSignal: Int32 = 1
        guard setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &noSignal, socklen_t(MemoryLayout<Int32>.size)) == 0 else {
            throw currentError()
        }
        try connectSocket(descriptor, deadline: min(deadline, start + connectTimeout))
        try request.withUnsafeBytes { bytes in
            guard let base = bytes.baseAddress else { return }
            var sent = 0
            while sent < bytes.count {
                try wait(descriptor, events: Int16(POLLOUT), deadline: deadline)
                let count = send(descriptor, base.advanced(by: sent), bytes.count - sent, 0)
                if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
                guard count >= 0 else { throw currentError() }
                guard count > 0 else { throw POSIXError(.EPIPE) }
                sent += count
            }
        }
        return try receive(descriptor, deadline: deadline)
    }

    private func connectSocket(_ descriptor: Int32, deadline: TimeInterval) throws {
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let encoded = Array(path.utf8)
        guard encoded.count < MemoryLayout.size(ofValue: address.sun_path) else { throw POSIXError(.ENAMETOOLONG) }
        address.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)
        withUnsafeMutableBytes(of: &address.sun_path) { bytes in
            bytes.initializeMemory(as: UInt8.self, repeating: 0)
            bytes.copyBytes(from: encoded)
        }
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if result == 0 { return }
        guard errno == EINPROGRESS || errno == EINTR || errno == EAGAIN else { throw currentError() }
        try wait(descriptor, events: Int16(POLLOUT), deadline: deadline)
        var status: Int32 = 0
        var size = socklen_t(MemoryLayout<Int32>.size)
        guard getsockopt(descriptor, SOL_SOCKET, SO_ERROR, &status, &size) == 0 else { throw currentError() }
        guard status == 0 else { throw POSIXError(.init(rawValue: status) ?? .EIO) }
    }

    private func receive(_ descriptor: Int32, deadline: TimeInterval) throws -> String {
        var response = Data()
        var buffer = [UInt8](repeating: 0, count: 16 * 1024)
        let limit = 8 * 1024 * 1024
        while true {
            try wait(descriptor, events: Int16(POLLIN), deadline: deadline)
            let count = recv(descriptor, &buffer, buffer.count, 0)
            if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
            guard count >= 0 else { throw currentError() }
            guard count > 0 else { throw POSIXError(.ECONNRESET) }
            let newline = buffer[..<count].firstIndex(of: 0x0A)
            let end = newline ?? count
            guard response.count + end <= limit else { throw POSIXError(.EMSGSIZE) }
            response.append(contentsOf: buffer[..<end])
            if newline != nil {
                guard let text = String(data: response, encoding: .utf8) else { throw POSIXError(.EILSEQ) }
                return text
            }
        }
    }

    private func wait(_ descriptor: Int32, events: Int16, deadline: TimeInterval) throws {
        while true {
            let remaining = deadline - ProcessInfo.processInfo.systemUptime
            guard remaining > 0 else { throw POSIXError(.ETIMEDOUT) }
            var item = pollfd(fd: descriptor, events: events, revents: 0)
            let result = poll(&item, 1, Int32(min(ceil(remaining * 1000), Double(Int32.max))))
            if result < 0 && errno == EINTR { continue }
            guard result >= 0 else { throw currentError() }
            if result == 0 { continue }
            guard item.revents & Int16(POLLNVAL) == 0 else { throw POSIXError(.EBADF) }
            // Let send/recv/getsockopt preserve the original socket error on HUP/ERR.
            return
        }
    }

    private func currentError() -> POSIXError { POSIXError(.init(rawValue: errno) ?? .EIO) }
}
