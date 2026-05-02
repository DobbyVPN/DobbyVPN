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
    private var statsTimer: DispatchSourceTimer?
    private var swiftFileDescriptor: Int32 = -1
    private var goFileDescriptor: Int32 = -1
    private var running = false
    private var writeErrorCount = 0
    private var readErrorCount = 0
    private var oversizedPacketCount = 0
    private var tunnelReadBatches = 0
    private var tunnelReadEmptyBatches = 0
    private var tunnelToGoPackets = 0
    private var tunnelToGoBytes = 0
    private var goToTunnelPackets = 0
    private var goToTunnelBytes = 0
    private var tunnelToGoDrops = 0
    private var goToTunnelDrops = 0
    private var firstTunnelPacketLogged = false
    private var firstGoPacketLogged = false

    var tunnelFileDescriptor: Int32 {
        lock.lock()
        defer { lock.unlock() }
        return goFileDescriptor
    }

    func releaseTunnelFileDescriptor() {
        lock.lock()
        let releasedFD = goFileDescriptor
        goFileDescriptor = -1
        lock.unlock()
        log("[DEBUG][PacketFlowBridge] Go fd ownership released fd=\(releasedFD)")
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

        log("[DEBUG][PacketFlowBridge] socketpair ready swiftFD=\(swiftFileDescriptor) goFD=\(goFileDescriptor) mtu=\(mtu)")
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
        startStatsTimer()
        readPacketsFromTunnel()
        log("[PacketFlowBridge] started mtu=\(mtu) swiftFD=\(sourceFD) goFD=\(tunnelFileDescriptor)")
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
        let timer = statsTimer
        statsTimer = nil
        let shouldCloseSwiftFD = source == nil
        let swiftFD = swiftFileDescriptor
        swiftFileDescriptor = -1
        let unreleasedGoFD = goFileDescriptor
        goFileDescriptor = -1
        lock.unlock()

        logStats(reason: "stop")
        timer?.cancel()
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
        packetFlow.readPackets { [weak self] packets, protocols in
            guard let self, self.isRunning else {
                return
            }

            self.recordTunnelReadBatch(packetCount: packets.count, protocolCount: protocols.count)
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

        if packet.count > mtu {
            logOversizedPacket(packet.count)
        }

        let framedPacket = utunFramedPacket(packet)
        let written = framedPacket.withUnsafeBytes { rawBuffer -> Int in
            guard let baseAddress = rawBuffer.baseAddress else {
                return -1
            }
            return Darwin.write(fd, baseAddress, framedPacket.count)
        }

        if written != framedPacket.count {
            recordTunnelToGoDrop()
            logWriteError(
                "write packet to Go failed packetBytes=\(packet.count) framedBytes=\(framedPacket.count) " +
                "written=\(written) errno=\(errno) \(errnoDescription())"
            )
            return
        }
        recordTunnelToGo(packetBytes: packet.count)
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
                    recordGoToTunnelDrop()
                    logReadError("short packet from Go readCount=\(readCount)")
                    continue
                }
                let packet = Data(buffer[utunHeaderLength..<readCount])
                packetFlow.writePackets([packet], withProtocols: [protocolFamily(for: packet)])
                recordGoToTunnel(packetBytes: packet.count)
                continue
            }

            if readCount == 0 {
                logReadError("Go packet fd closed")
                return
            }

            if errno == EAGAIN || errno == EWOULDBLOCK {
                return
            }
            logReadError("read packet from Go failed errno=\(errno) \(errnoDescription())")
            return
        }
    }

    private func startStatsTimer() {
        let timer = DispatchSource.makeTimerSource(queue: readQueue)
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [weak self] in
            self?.logStats(reason: "periodic")
        }

        lock.lock()
        statsTimer = timer
        lock.unlock()
        timer.resume()
    }

    private func recordTunnelReadBatch(packetCount: Int, protocolCount: Int) {
        lock.lock()
        tunnelReadBatches += 1
        if packetCount == 0 {
            tunnelReadEmptyBatches += 1
        }
        let shouldLogProtocolMismatch = packetCount != protocolCount && tunnelReadBatches <= 5
        lock.unlock()

        if shouldLogProtocolMismatch {
            log(
                "[DEBUG][PacketFlowBridge] packetFlow read protocol count mismatch " +
                "packets=\(packetCount) protocols=\(protocolCount)"
            )
        }
    }

    private func recordTunnelToGo(packetBytes: Int) {
        lock.lock()
        tunnelToGoPackets += 1
        tunnelToGoBytes += packetBytes
        let shouldLogFirst = !firstTunnelPacketLogged
        firstTunnelPacketLogged = true
        lock.unlock()

        if shouldLogFirst {
            log("[DEBUG][PacketFlowBridge] first packet tunnel->go bytes=\(packetBytes)")
        }
    }

    private func recordGoToTunnel(packetBytes: Int) {
        lock.lock()
        goToTunnelPackets += 1
        goToTunnelBytes += packetBytes
        let shouldLogFirst = !firstGoPacketLogged
        firstGoPacketLogged = true
        lock.unlock()

        if shouldLogFirst {
            log("[DEBUG][PacketFlowBridge] first packet go->tunnel bytes=\(packetBytes)")
        }
    }

    private func recordTunnelToGoDrop() {
        lock.lock()
        tunnelToGoDrops += 1
        lock.unlock()
    }

    private func recordGoToTunnelDrop() {
        lock.lock()
        goToTunnelDrops += 1
        lock.unlock()
    }

    private func logOversizedPacket(_ packetBytes: Int) {
        lock.lock()
        oversizedPacketCount += 1
        let shouldLog = oversizedPacketCount <= 5 || oversizedPacketCount % 100 == 0
        lock.unlock()

        if shouldLog {
            log("[DEBUG][PacketFlowBridge] packet larger than configured MTU packetBytes=\(packetBytes) mtu=\(mtu)")
        }
    }

    private func logStats(reason: String) {
        lock.lock()
        let batches = tunnelReadBatches
        let emptyBatches = tunnelReadEmptyBatches
        let t2gPackets = tunnelToGoPackets
        let t2gBytes = tunnelToGoBytes
        let g2tPackets = goToTunnelPackets
        let g2tBytes = goToTunnelBytes
        let t2gDrops = tunnelToGoDrops
        let g2tDrops = goToTunnelDrops
        let oversized = oversizedPacketCount
        let isActive = running
        lock.unlock()

        log(
            "[DEBUG][PacketFlowBridge] stats reason=\(reason) running=\(isActive) " +
            "batches=\(batches) emptyBatches=\(emptyBatches) " +
            "tunnel_to_go=\(t2gPackets)p/\(t2gBytes)B " +
            "go_to_tunnel=\(g2tPackets)p/\(g2tBytes)B " +
            "drops_tunnel_to_go=\(t2gDrops) drops_go_to_tunnel=\(g2tDrops) oversized=\(oversized)"
        )
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

    private func errnoDescription() -> String {
        String(cString: strerror(errno))
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
