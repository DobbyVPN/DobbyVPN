import Darwin
import Foundation
import NetworkExtension

final class PacketFlowBridge {
    private let packetFlow: NEPacketTunnelFlow
    private let mtu: Int
    private let log: (String) -> Void
    private let readQueue: DispatchQueue
    private let lock = NSLock()
    private let utunHeaderLength = 4

    private var readSource: DispatchSourceRead?
    private var swiftFileDescriptor: Int32 = -1
    private var goFileDescriptor: Int32 = -1
    private var running = false
    private var writeErrorCount = 0
    private var readErrorCount = 0

    var tunnelFileDescriptor: Int32 {
        lock.lock()
        defer { lock.unlock() }
        return goFileDescriptor
    }

    func releaseTunnelFileDescriptor() {
        lock.lock()
        defer { lock.unlock() }
        goFileDescriptor = -1
    }

    init(packetFlow: NEPacketTunnelFlow, mtu: Int, tunnelId: String, log: @escaping (String) -> Void) throws {
        self.packetFlow = packetFlow
        self.mtu = mtu
        self.log = log
        self.readQueue = DispatchQueue(label: "vpn.dobby.app.packetflow.\(tunnelId)")

        var descriptors: [Int32] = [-1, -1]
        let rc = descriptors.withUnsafeMutableBufferPointer { buffer in
            socketpair(AF_UNIX, SOCK_DGRAM, 0, buffer.baseAddress)
        }
        guard rc == 0 else {
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }

        swiftFileDescriptor = descriptors[0]
        goFileDescriptor = descriptors[1]

        do {
            try disableSigpipe(swiftFileDescriptor)
            try disableSigpipe(goFileDescriptor)
            try setNonBlocking(swiftFileDescriptor)
            try setNonBlocking(goFileDescriptor)
        } catch {
            closeIfOpen(&swiftFileDescriptor)
            closeIfOpen(&goFileDescriptor)
            throw error
        }
    }

    func start() {
        lock.lock()
        guard !running, swiftFileDescriptor >= 0, goFileDescriptor >= 0 else {
            lock.unlock()
            return
        }
        running = true
        let sourceFD = swiftFileDescriptor
        lock.unlock()

        let source = DispatchSource.makeReadSource(fileDescriptor: sourceFD, queue: readQueue)
        source.setEventHandler { [weak self] in
            self?.drainPacketsFromGo()
        }
        source.setCancelHandler {
            close(sourceFD)
        }

        lock.lock()
        readSource = source
        lock.unlock()

        source.resume()
        readPacketsFromTunnel()
        log("[PacketFlowBridge] started mtu=\(mtu)")
    }

    func stop() {
        lock.lock()
        guard running || swiftFileDescriptor >= 0 || goFileDescriptor >= 0 else {
            lock.unlock()
            return
        }
        running = false
        let source = readSource
        readSource = nil
        let shouldCloseSwiftFD = source == nil
        let swiftFD = swiftFileDescriptor
        swiftFileDescriptor = -1
        let unreleasedGoFD = goFileDescriptor
        goFileDescriptor = -1
        lock.unlock()

        source?.cancel()
        if shouldCloseSwiftFD, swiftFD >= 0 {
            close(swiftFD)
        }
        if unreleasedGoFD >= 0 {
            close(unreleasedGoFD)
        }
        log("[PacketFlowBridge] stopped")
    }

    private func readPacketsFromTunnel() {
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self, self.isRunning else {
                return
            }

            for packet in packets where !packet.isEmpty {
                self.writePacketToGo(packet)
            }

            self.readPacketsFromTunnel()
        }
    }

    private func writePacketToGo(_ packet: Data) {
        let fd = currentSwiftFileDescriptor()
        guard fd >= 0 else {
            return
        }

        let framedPacket = utunFramedPacket(packet)
        let written = framedPacket.withUnsafeBytes { rawBuffer -> Int in
            guard let baseAddress = rawBuffer.baseAddress else {
                return -1
            }
            return Darwin.write(fd, baseAddress, framedPacket.count)
        }

        if written != framedPacket.count {
            logWriteError("write packet to Go failed written=\(written) errno=\(errno)")
        }
    }

    private func drainPacketsFromGo() {
        let fd = currentSwiftFileDescriptor()
        guard fd >= 0 else {
            return
        }

        var buffer = [UInt8](repeating: 0, count: max(mtu + utunHeaderLength + 128, 2048))
        while isRunning {
            let readCount = buffer.withUnsafeMutableBytes { rawBuffer -> Int in
                guard let baseAddress = rawBuffer.baseAddress else {
                    return -1
                }
                return Darwin.read(fd, baseAddress, rawBuffer.count)
            }

            if readCount > 0 {
                guard readCount > utunHeaderLength else {
                    logReadError("short packet from Go readCount=\(readCount)")
                    continue
                }
                let packet = Data(buffer[utunHeaderLength..<readCount])
                packetFlow.writePackets([packet], withProtocols: [protocolFamily(for: packet)])
                continue
            }

            if readCount == 0 {
                logReadError("Go packet fd closed")
                return
            }

            if errno == EAGAIN || errno == EWOULDBLOCK {
                return
            }
            logReadError("read packet from Go failed errno=\(errno)")
            return
        }
    }

    private var isRunning: Bool {
        lock.lock()
        defer { lock.unlock() }
        return running
    }

    private func currentSwiftFileDescriptor() -> Int32 {
        lock.lock()
        defer { lock.unlock() }
        return swiftFileDescriptor
    }

    private func protocolFamily(for packet: Data) -> NSNumber {
        guard let first = packet.first else {
            return NSNumber(value: AF_INET)
        }
        let version = first >> 4
        if version == 6 {
            return NSNumber(value: AF_INET6)
        }
        return NSNumber(value: AF_INET)
    }

    private func utunFramedPacket(_ packet: Data) -> Data {
        let family = protocolFamily(for: packet).uint8Value
        var framed = Data([0, 0, 0, family])
        framed.append(packet)
        return framed
    }

    private func logWriteError(_ message: String) {
        lock.lock()
        writeErrorCount += 1
        let shouldLog = writeErrorCount <= 5 || writeErrorCount % 100 == 0
        lock.unlock()
        if shouldLog {
            log("[PacketFlowBridge] \(message)")
        }
    }

    private func logReadError(_ message: String) {
        lock.lock()
        readErrorCount += 1
        let shouldLog = readErrorCount <= 5 || readErrorCount % 100 == 0
        lock.unlock()
        if shouldLog {
            log("[PacketFlowBridge] \(message)")
        }
    }

    private func setNonBlocking(_ fd: Int32) throws {
        let flags = fcntl(fd, F_GETFL, 0)
        guard flags >= 0 else {
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }
        guard fcntl(fd, F_SETFL, flags | O_NONBLOCK) >= 0 else {
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }
    }

    private func disableSigpipe(_ fd: Int32) throws {
        var value: Int32 = 1
        let result = setsockopt(
            fd,
            SOL_SOCKET,
            SO_NOSIGPIPE,
            &value,
            socklen_t(MemoryLayout<Int32>.size)
        )
        guard result == 0 else {
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }
    }

    private func closeIfOpen(_ fd: inout Int32) {
        if fd >= 0 {
            close(fd)
            fd = -1
        }
    }
}
