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
    private let requestedSocketBufferBytes = 1 * 1024 * 1024
    private let maxBatchPackets = 64
    private let maxBatchBytes = 64 * 1024
    private let maxPacketClassificationSamples = 25
    private let maxPacketFlowSummaryItems = 12

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
    private var goToTunnelWriteBatches = 0
    private var goToTunnelMaxBatchPackets = 0
    private var tunnelToGoDrops = 0
    private var goToTunnelDrops = 0
    private var lastTunnelToGoPacketAt: Date?
    private var lastGoToTunnelPacketAt: Date?
    private var previousStatsSnapshot: TrafficSnapshot?
    private var firstTunnelPacketLogged = false
    private var firstGoPacketLogged = false
    private var tunnelToGoClassification = PacketClassStats()
    private var goToTunnelClassification = PacketClassStats()

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
        if releasedFD < 0 {
            log("[PacketFlowBridge] Go fd ownership release requested but fd was already released")
            return
        }
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
            let socketErrno = errno
            log(
                "[PacketFlowBridge] socketpair failed errno=\(socketErrno) " +
                "\(errnoDescription(socketErrno))"
            )
            throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
        }

        swiftFileDescriptor = descriptors[0]
        goFileDescriptor = descriptors[1]

        do {
            try disableSigpipe(swiftFileDescriptor)
            try disableSigpipe(goFileDescriptor)
            let swiftBuffers = configureSocketBuffers(swiftFileDescriptor, requestedBytes: requestedSocketBufferBytes)
            let goBuffers = configureSocketBuffers(goFileDescriptor, requestedBytes: requestedSocketBufferBytes)
            try setNonBlocking(swiftFileDescriptor)
            try setNonBlocking(goFileDescriptor)
            log(
                "[DEBUG][PacketFlowBridge] socket buffers " +
                "swiftFD=\(swiftFileDescriptor) snd=\(swiftBuffers.send) rcv=\(swiftBuffers.receive) " +
                "goFD=\(goFileDescriptor) snd=\(goBuffers.send) rcv=\(goBuffers.receive)"
            )
        } catch {
            log(
                "[PacketFlowBridge] socket setup failed swiftFD=\(swiftFileDescriptor) " +
                "goFD=\(goFileDescriptor) error=\(error.localizedDescription)"
            )
            closeIfOpen(&swiftFileDescriptor)
            closeIfOpen(&goFileDescriptor)
            throw error
        }

        log("[DEBUG][PacketFlowBridge] socketpair ready swiftFD=\(swiftFileDescriptor) goFD=\(goFileDescriptor) mtu=\(mtu)")
    }

    func start() {
        lock.lock()
        if running || swiftFileDescriptor < 0 || goFileDescriptor < 0 {
            let isActive = running
            let swiftFD = swiftFileDescriptor
            let goFD = goFileDescriptor
            lock.unlock()
            log("[PacketFlowBridge] start skipped running=\(isActive) swiftFD=\(swiftFD) goFD=\(goFD)")
            return
        }
        running = true
        let sourceFD = swiftFileDescriptor
        lock.unlock()

        let source = DispatchSource.makeReadSource(fileDescriptor: sourceFD, queue: readQueue)
        source.setEventHandler { [weak self] in
            self?.drainPacketsFromGo()
        }
        let closeLog = log
        source.setCancelHandler {
            let rc = Darwin.close(sourceFD)
            if rc == 0 {
                closeLog("[PacketFlowBridge] read source cancel closed swiftFD=\(sourceFD)")
            } else {
                let closeErrno = errno
                closeLog(
                    "[PacketFlowBridge] read source cancel close failed swiftFD=\(sourceFD) " +
                    "errno=\(closeErrno) \(String(cString: strerror(closeErrno)))"
                )
            }
        }

        lock.lock()
        readSource = source
        lock.unlock()

        source.resume()
        startStatsTimer()
        readPacketsFromTunnel()
        // iOS 26 research: Log when packet flow starts - track if packets flow correctly
        log("[iOS26-RESEARCH] PacketFlowBridge started: mtu=\(mtu) - monitoring for packet flow issues")
        log("[PacketFlowBridge] started mtu=\(mtu) swiftFD=\(sourceFD) goFD=\(tunnelFileDescriptor)")
    }

    func stop() {
        lock.lock()
        if !running && swiftFileDescriptor < 0 && goFileDescriptor < 0 {
            lock.unlock()
            log("[PacketFlowBridge] stop skipped; already stopped")
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
            closeFD(swiftFD, label: "swiftFD")
        }
        if unreleasedGoFD >= 0 {
            closeFD(unreleasedGoFD, label: "unreleasedGoFD")
        }
        log("[PacketFlowBridge] stopped")
    }

    func diagnosticsSnapshot() -> String {
        let now = Date()
        lock.lock()
        let snapshot = TrafficSnapshot(
            tunnelToGoPackets: tunnelToGoPackets,
            tunnelToGoBytes: tunnelToGoBytes,
            goToTunnelPackets: goToTunnelPackets,
            goToTunnelBytes: goToTunnelBytes,
            tunnelToGoDrops: tunnelToGoDrops,
            goToTunnelDrops: goToTunnelDrops,
            writeErrors: writeErrorCount,
            readErrors: readErrorCount
        )
        let isActive = running
        let swiftFD = swiftFileDescriptor
        let goFD = goFileDescriptor
        let batches = tunnelReadBatches
        let emptyBatches = tunnelReadEmptyBatches
        let g2tBatches = goToTunnelWriteBatches
        let g2tMaxBatch = goToTunnelMaxBatchPackets
        let oversized = oversizedPacketCount
        let lastTunnelToGo = lastTunnelToGoPacketAt
        let lastGoToTunnel = lastGoToTunnelPacketAt
        let tunnelToGoClassSummary = tunnelToGoClassification.summary(maxItems: maxPacketFlowSummaryItems)
        let goToTunnelClassSummary = goToTunnelClassification.summary(maxItems: maxPacketFlowSummaryItems)
        lock.unlock()

        return "running=\(isActive) swiftFD=\(swiftFD) goFD=\(goFD) mtu=\(mtu) " +
            "batches=\(batches) emptyBatches=\(emptyBatches) " +
            "tunnel_to_go=\(snapshot.tunnelToGoPackets)p/\(snapshot.tunnelToGoBytes)B " +
            "go_to_tunnel=\(snapshot.goToTunnelPackets)p/\(snapshot.goToTunnelBytes)B " +
            "go_to_tunnel_batches=\(g2tBatches) max_batch=\(g2tMaxBatch) " +
            "drops_tunnel_to_go=\(snapshot.tunnelToGoDrops) drops_go_to_tunnel=\(snapshot.goToTunnelDrops) " +
            "oversized=\(oversized) writeErrors=\(snapshot.writeErrors) readErrors=\(snapshot.readErrors) " +
            "lastPacketAge(tunnel_to_go=\(ageDescription(lastTunnelToGo, now: now))," +
            "go_to_tunnel=\(ageDescription(lastGoToTunnel, now: now))) " +
            "diagnosis=\(flowDiagnosis(running: isActive, snapshot: snapshot)) " +
            "classify_tunnel_to_go=[\(tunnelToGoClassSummary)] " +
            "classify_go_to_tunnel=[\(goToTunnelClassSummary)]"
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
            recordTunnelToGoDrop()
            logWriteError("write packet to Go skipped because swift fd is closed packetBytes=\(packet.count)")
            return
        }

        if packet.count > mtu {
            logOversizedPacket(packet.count)
        }

        let framedPacket = utunFramedPacket(packet)
        let written = framedPacket.withUnsafeBytes { rawBuffer -> Int in
            guard let baseAddress = rawBuffer.baseAddress else {
                logWriteError("write packet to Go failed: empty raw buffer packetBytes=\(packet.count)")
                return -1
            }
            return Darwin.write(fd, baseAddress, framedPacket.count)
        }
        let writeErrno = errno

        if written != framedPacket.count {
            recordTunnelToGoDrop()
            logWriteError(
                "write packet to Go failed packetBytes=\(packet.count) framedBytes=\(framedPacket.count) " +
                "written=\(written) errno=\(writeErrno) \(errnoDescription(writeErrno))"
            )
            return
        }
        recordTunnelToGo(packet: packet)
    }

    private func drainPacketsFromGo() {
        let fd = currentSwiftFileDescriptor()
        guard fd >= 0 else {
            return
        }

        var buffer = [UInt8](repeating: 0, count: max(mtu + utunHeaderLength + 128, 2048))
        var batchPackets: [Data] = []
        var batchProtocols: [NSNumber] = []
        batchPackets.reserveCapacity(maxBatchPackets)
        batchProtocols.reserveCapacity(maxBatchPackets)
        var batchBytes = 0

        while isRunning {
            let readCount = buffer.withUnsafeMutableBytes { rawBuffer -> Int in
                guard let baseAddress = rawBuffer.baseAddress else {
                    return -1
                }
                return Darwin.read(fd, baseAddress, rawBuffer.count)
            }
            let readErrno = errno

            if readCount > 0 {
                guard readCount > utunHeaderLength else {
                    recordGoToTunnelDrop()
                    logReadError("short packet from Go readCount=\(readCount)")
                    continue
                }
                let packet = Data(buffer[utunHeaderLength..<readCount])
                batchPackets.append(packet)
                batchProtocols.append(protocolFamily(for: packet))
                batchBytes += packet.count

                if batchPackets.count >= maxBatchPackets || batchBytes >= maxBatchBytes {
                    flushBatchToTunnel(&batchPackets, &batchProtocols, &batchBytes)
                }
                continue
            }

            flushBatchToTunnel(&batchPackets, &batchProtocols, &batchBytes)

            if readCount == 0 {
                logReadError("Go packet fd closed")
                return
            }

            if readErrno == EAGAIN || readErrno == EWOULDBLOCK {
                return
            }
            logReadError("read packet from Go failed errno=\(readErrno) \(errnoDescription(readErrno))")
            return
        }

        flushBatchToTunnel(&batchPackets, &batchProtocols, &batchBytes)
    }

    private func flushBatchToTunnel(
        _ packets: inout [Data],
        _ protocols: inout [NSNumber],
        _ batchBytes: inout Int
    ) {
        guard !packets.isEmpty else { return }
        let count = packets.count
        let bytes = batchBytes
        packetFlow.writePackets(packets, withProtocols: protocols)
        log(
            "[DEBUG][PacketFlowBridge] wrote batch go->tunnel packets=\(count) bytes=\(bytes) " +
            "protocols=\(protocols.count)"
        )
        recordGoToTunnelBatch(packetCount: count, byteCount: bytes, packets: packets)
        packets.removeAll(keepingCapacity: true)
        protocols.removeAll(keepingCapacity: true)
        batchBytes = 0
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

    private func recordTunnelToGo(packet: Data) {
        let classification = classifyPacket(packet)
        lock.lock()
        tunnelToGoPackets += 1
        tunnelToGoBytes += packet.count
        lastTunnelToGoPacketAt = Date()
        let sample = tunnelToGoClassification.record(
            classification,
            sampleLimit: maxPacketClassificationSamples
        )
        let shouldLogFirst = !firstTunnelPacketLogged
        firstTunnelPacketLogged = true
        lock.unlock()

        if shouldLogFirst {
            log("[DEBUG][PacketFlowBridge] first packet tunnel->go \(classification.sampleDescription)")
        }
        if let sample {
            log("[DEBUG][PacketFlowBridge] sample tunnel->go \(sample)")
        }
    }

    private func recordGoToTunnelBatch(packetCount: Int, byteCount: Int, packets: [Data]) {
        let classifications = packets.map { classifyPacket($0) }
        lock.lock()
        goToTunnelPackets += packetCount
        goToTunnelBytes += byteCount
        lastGoToTunnelPacketAt = Date()
        goToTunnelWriteBatches += 1
        goToTunnelMaxBatchPackets = max(goToTunnelMaxBatchPackets, packetCount)
        var samples: [String] = []
        for classification in classifications {
            if let sample = goToTunnelClassification.record(
                classification,
                sampleLimit: maxPacketClassificationSamples
            ) {
                samples.append(sample)
            }
        }
        let shouldLogFirst = !firstGoPacketLogged
        firstGoPacketLogged = true
        lock.unlock()

        if shouldLogFirst {
            let firstPacket = classifications.first?.sampleDescription ?? "no_packet_classification"
            log(
                "[DEBUG][PacketFlowBridge] first batch go->tunnel packets=\(packetCount) " +
                "bytes=\(byteCount) first=\(firstPacket)"
            )
        }
        for sample in samples {
            log("[DEBUG][PacketFlowBridge] sample go->tunnel \(sample)")
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
        let now = Date()
        lock.lock()
        let batches = tunnelReadBatches
        let emptyBatches = tunnelReadEmptyBatches
        let t2gPackets = tunnelToGoPackets
        let t2gBytes = tunnelToGoBytes
        let g2tPackets = goToTunnelPackets
        let g2tBytes = goToTunnelBytes
        let g2tBatches = goToTunnelWriteBatches
        let g2tMaxBatch = goToTunnelMaxBatchPackets
        let t2gDrops = tunnelToGoDrops
        let g2tDrops = goToTunnelDrops
        let oversized = oversizedPacketCount
        let writeErrors = writeErrorCount
        let readErrors = readErrorCount
        let isActive = running
        let lastTunnelToGo = lastTunnelToGoPacketAt
        let lastGoToTunnel = lastGoToTunnelPacketAt
        let tunnelToGoClassSummary = tunnelToGoClassification.summary(maxItems: maxPacketFlowSummaryItems)
        let goToTunnelClassSummary = goToTunnelClassification.summary(maxItems: maxPacketFlowSummaryItems)
        let snapshot = TrafficSnapshot(
            tunnelToGoPackets: t2gPackets,
            tunnelToGoBytes: t2gBytes,
            goToTunnelPackets: g2tPackets,
            goToTunnelBytes: g2tBytes,
            tunnelToGoDrops: t2gDrops,
            goToTunnelDrops: g2tDrops,
            writeErrors: writeErrors,
            readErrors: readErrors
        )
        let previousSnapshot = previousStatsSnapshot
        previousStatsSnapshot = snapshot
        lock.unlock()

        log(
            "[DEBUG][PacketFlowBridge] stats reason=\(reason) running=\(isActive) " +
            "batches=\(batches) emptyBatches=\(emptyBatches) " +
            "tunnel_to_go=\(t2gPackets)p/\(t2gBytes)B " +
            "go_to_tunnel=\(g2tPackets)p/\(g2tBytes)B " +
            "delta=\(snapshot.deltaDescription(from: previousSnapshot)) " +
            "lastPacketAge(tunnel_to_go=\(ageDescription(lastTunnelToGo, now: now))," +
            "go_to_tunnel=\(ageDescription(lastGoToTunnel, now: now))) " +
            "go_to_tunnel_batches=\(g2tBatches) max_batch=\(g2tMaxBatch) " +
            "drops_tunnel_to_go=\(t2gDrops) drops_go_to_tunnel=\(g2tDrops) " +
            "oversized=\(oversized) writeErrors=\(writeErrors) readErrors=\(readErrors) " +
            "diagnosis=\(flowDiagnosis(running: isActive, snapshot: snapshot))"
        )
        log("[PacketFlowBridge][classify] tunnel_to_go \(tunnelToGoClassSummary)")
        log("[PacketFlowBridge][classify] go_to_tunnel \(goToTunnelClassSummary)")
    }

    private struct TrafficSnapshot {
        let tunnelToGoPackets: Int
        let tunnelToGoBytes: Int
        let goToTunnelPackets: Int
        let goToTunnelBytes: Int
        let tunnelToGoDrops: Int
        let goToTunnelDrops: Int
        let writeErrors: Int
        let readErrors: Int

        func deltaDescription(from previous: TrafficSnapshot?) -> String {
            guard let previous else {
                return "first"
            }
            return "tunnel_to_go=+\(tunnelToGoPackets - previous.tunnelToGoPackets)p/" +
                "+\(tunnelToGoBytes - previous.tunnelToGoBytes)B " +
                "go_to_tunnel=+\(goToTunnelPackets - previous.goToTunnelPackets)p/" +
                "+\(goToTunnelBytes - previous.goToTunnelBytes)B " +
                "drops=+\(tunnelToGoDrops - previous.tunnelToGoDrops)/" +
                "+\(goToTunnelDrops - previous.goToTunnelDrops) " +
                "errors=+\(writeErrors - previous.writeErrors)/+\(readErrors - previous.readErrors)"
        }
    }

    private func ageDescription(_ date: Date?, now: Date) -> String {
        guard let date else { return "never" }
        return "\(Int(now.timeIntervalSince(date) * 1000))ms"
    }

    private func flowDiagnosis(running: Bool, snapshot: TrafficSnapshot) -> String {
        var flags: [String] = []
        if running && snapshot.tunnelToGoPackets == 0 && snapshot.goToTunnelPackets == 0 {
            flags.append("NO_TRAFFIC_YET")
        }
        if snapshot.tunnelToGoPackets > 0 && snapshot.goToTunnelPackets == 0 {
            flags.append("ONE_WAY_APP_TO_GO")
        }
        if snapshot.goToTunnelPackets > 0 && snapshot.tunnelToGoPackets == 0 {
            flags.append("ONE_WAY_GO_TO_APP")
        }
        if snapshot.tunnelToGoDrops > 0 || snapshot.goToTunnelDrops > 0 {
            flags.append("DROPS_PRESENT")
        }
        if snapshot.writeErrors > 0 || snapshot.readErrors > 0 {
            flags.append("SOCKET_ERRORS_PRESENT")
        }
        return flags.isEmpty ? "OK_OR_IDLE" : flags.joined(separator: ",")
    }

    private struct PacketClassification {
        let family: String
        let transport: String
        let source: String
        let destination: String
        let sourcePort: Int?
        let destinationPort: Int?
        let byteCount: Int
        let parseError: String?

        var isDNS: Bool {
            sourcePort == 53 || destinationPort == 53
        }

        var flowKey: String {
            "\(transport) \(endpoint(source, sourcePort))->\(endpoint(destination, destinationPort))"
        }

        var sampleKey: String {
            if let parseError {
                return "\(family)/\(transport) parseError=\(parseError) bytes=\(byteCount)"
            }
            return "\(family)/\(transport) \(endpoint(source, sourcePort))->\(endpoint(destination, destinationPort))"
        }

        var sampleDescription: String {
            var parts = [
                "family=\(family)",
                "transport=\(transport)",
                "src=\(endpoint(source, sourcePort))",
                "dst=\(endpoint(destination, destinationPort))",
                "bytes=\(byteCount)"
            ]
            if isDNS {
                parts.append("dns=true")
            }
            if let parseError {
                parts.append("parseError=\(parseError)")
            }
            return parts.joined(separator: " ")
        }

        private func endpoint(_ ip: String, _ port: Int?) -> String {
            guard let port else { return ip }
            return "\(ip):\(port)"
        }
    }

    private struct PacketClassStats {
        var packets = 0
        var bytes = 0
        var parseErrors = 0
        var dnsPackets = 0
        var dnsBytes = 0
        var familyPackets: [String: Int] = [:]
        var familyBytes: [String: Int] = [:]
        var transportPackets: [String: Int] = [:]
        var transportBytes: [String: Int] = [:]
        var flowPackets: [String: Int] = [:]
        var flowBytes: [String: Int] = [:]
        var samples: [String] = []
        var sampleKeys = Set<String>()

        mutating func record(_ packet: PacketClassification, sampleLimit: Int) -> String? {
            packets += 1
            bytes += packet.byteCount
            familyPackets[packet.family, default: 0] += 1
            familyBytes[packet.family, default: 0] += packet.byteCount
            transportPackets[packet.transport, default: 0] += 1
            transportBytes[packet.transport, default: 0] += packet.byteCount
            flowPackets[packet.flowKey, default: 0] += 1
            flowBytes[packet.flowKey, default: 0] += packet.byteCount

            if packet.isDNS {
                dnsPackets += 1
                dnsBytes += packet.byteCount
            }
            if packet.parseError != nil {
                parseErrors += 1
            }

            guard samples.count < sampleLimit, !sampleKeys.contains(packet.sampleKey) else {
                return nil
            }
            sampleKeys.insert(packet.sampleKey)
            let sample = packet.sampleDescription
            samples.append(sample)
            return sample
        }

        func summary(maxItems: Int) -> String {
            "packets=\(packets) bytes=\(bytes) " +
            "families={\(breakdown(familyPackets, familyBytes, maxItems: maxItems))} " +
            "transports={\(breakdown(transportPackets, transportBytes, maxItems: maxItems))} " +
            "dns=\(dnsPackets)p/\(dnsBytes)B parseErrors=\(parseErrors) " +
            "topFlows={\(breakdown(flowPackets, flowBytes, maxItems: maxItems))} " +
            "samples=[\(samples.joined(separator: "; "))]"
        }

        private func breakdown(_ packets: [String: Int], _ bytes: [String: Int], maxItems: Int) -> String {
            guard !packets.isEmpty else { return "none" }
            return packets
                .sorted { lhs, rhs in
                    if lhs.value == rhs.value {
                        return lhs.key < rhs.key
                    }
                    return lhs.value > rhs.value
                }
                .prefix(maxItems)
                .map { key, count in
                    "\(key)=\(count)p/\(bytes[key] ?? 0)B"
                }
                .joined(separator: ",")
        }
    }

    private func classifyPacket(_ packet: Data) -> PacketClassification {
        packet.withUnsafeBytes { rawBuffer in
            let bytes = rawBuffer.bindMemory(to: UInt8.self)
            guard let base = bytes.baseAddress, bytes.count > 0 else {
                return PacketClassification(
                    family: "unknown",
                    transport: "unknown",
                    source: "-",
                    destination: "-",
                    sourcePort: nil,
                    destinationPort: nil,
                    byteCount: packet.count,
                    parseError: "empty_packet"
                )
            }

            let version = base[0] >> 4
            switch version {
            case 4:
                return classifyIPv4(base: base, count: bytes.count, byteCount: packet.count)
            case 6:
                return classifyIPv6(base: base, count: bytes.count, byteCount: packet.count)
            default:
                return PacketClassification(
                    family: "unknown_v\(version)",
                    transport: "unknown",
                    source: "-",
                    destination: "-",
                    sourcePort: nil,
                    destinationPort: nil,
                    byteCount: packet.count,
                    parseError: "unsupported_ip_version_\(version)"
                )
            }
        }
    }

    private func classifyIPv4(
        base: UnsafePointer<UInt8>,
        count: Int,
        byteCount: Int
    ) -> PacketClassification {
        guard count >= 20 else {
            return malformedPacket(family: "ipv4", byteCount: byteCount, reason: "short_ipv4_header_\(count)")
        }

        let headerLength = Int(base[0] & 0x0F) * 4
        guard headerLength >= 20, count >= headerLength else {
            return malformedPacket(
                family: "ipv4",
                byteCount: byteCount,
                reason: "invalid_ipv4_header_length_\(headerLength)_count_\(count)"
            )
        }

        let protocolNumber = base[9]
        let source = ipv4String(base.advanced(by: 12))
        let destination = ipv4String(base.advanced(by: 16))
        let transport = classifyTransport(
            base: base,
            count: count,
            offset: headerLength,
            protocolNumber: protocolNumber,
            family: "ipv4",
            byteCount: byteCount,
            source: source,
            destination: destination
        )
        return transport
    }

    private func classifyIPv6(
        base: UnsafePointer<UInt8>,
        count: Int,
        byteCount: Int
    ) -> PacketClassification {
        guard count >= 40 else {
            return malformedPacket(family: "ipv6", byteCount: byteCount, reason: "short_ipv6_header_\(count)")
        }

        let source = ipv6String(base.advanced(by: 8))
        let destination = ipv6String(base.advanced(by: 24))
        let transportLocation = ipv6TransportLocation(base: base, count: count)
        if let parseError = transportLocation.parseError {
            return PacketClassification(
                family: "ipv6",
                transport: "proto\(transportLocation.protocolNumber)",
                source: source,
                destination: destination,
                sourcePort: nil,
                destinationPort: nil,
                byteCount: byteCount,
                parseError: parseError
            )
        }

        return classifyTransport(
            base: base,
            count: count,
            offset: transportLocation.offset,
            protocolNumber: transportLocation.protocolNumber,
            family: "ipv6",
            byteCount: byteCount,
            source: source,
            destination: destination
        )
    }

    private func ipv6TransportLocation(
        base: UnsafePointer<UInt8>,
        count: Int
    ) -> (protocolNumber: UInt8, offset: Int, parseError: String?) {
        var protocolNumber = base[6]
        var offset = 40
        var extensionCount = 0

        while isIPv6ExtensionHeader(protocolNumber) {
            extensionCount += 1
            if extensionCount > 8 {
                return (protocolNumber, offset, "too_many_ipv6_extension_headers")
            }
            guard count >= offset + 2 else {
                return (protocolNumber, offset, "short_ipv6_extension_header_offset_\(offset)")
            }

            let nextHeader = base[offset]
            let headerLength: Int
            switch protocolNumber {
            case 0, 43, 60:
                headerLength = (Int(base[offset + 1]) + 1) * 8
            case 44:
                headerLength = 8
            case 51:
                headerLength = (Int(base[offset + 1]) + 2) * 4
            default:
                headerLength = 0
            }

            guard headerLength > 0, count >= offset + headerLength else {
                return (protocolNumber, offset, "invalid_ipv6_extension_length_\(headerLength)_offset_\(offset)")
            }
            offset += headerLength
            protocolNumber = nextHeader
        }

        return (protocolNumber, offset, nil)
    }

    private func isIPv6ExtensionHeader(_ protocolNumber: UInt8) -> Bool {
        switch protocolNumber {
        case 0, 43, 44, 51, 60:
            return true
        default:
            return false
        }
    }

    private func classifyTransport(
        base: UnsafePointer<UInt8>,
        count: Int,
        offset: Int,
        protocolNumber: UInt8,
        family: String,
        byteCount: Int,
        source: String,
        destination: String
    ) -> PacketClassification {
        let transport = transportName(protocolNumber)
        var sourcePort: Int?
        var destinationPort: Int?
        var parseError: String?

        if protocolNumber == 6 || protocolNumber == 17 {
            if count >= offset + 4 {
                sourcePort = readUInt16(base.advanced(by: offset))
                destinationPort = readUInt16(base.advanced(by: offset + 2))
            } else {
                parseError = "short_\(transport)_header_offset_\(offset)_count_\(count)"
            }
        }

        return PacketClassification(
            family: family,
            transport: transport,
            source: source,
            destination: destination,
            sourcePort: sourcePort,
            destinationPort: destinationPort,
            byteCount: byteCount,
            parseError: parseError
        )
    }

    private func transportName(_ protocolNumber: UInt8) -> String {
        switch protocolNumber {
        case 6:
            return "tcp"
        case 17:
            return "udp"
        case 1:
            return "icmp"
        case 58:
            return "icmpv6"
        default:
            return "proto\(protocolNumber)"
        }
    }

    private func malformedPacket(family: String, byteCount: Int, reason: String) -> PacketClassification {
        PacketClassification(
            family: family,
            transport: "malformed",
            source: "-",
            destination: "-",
            sourcePort: nil,
            destinationPort: nil,
            byteCount: byteCount,
            parseError: reason
        )
    }

    private func readUInt16(_ pointer: UnsafePointer<UInt8>) -> Int {
        (Int(pointer[0]) << 8) | Int(pointer[1])
    }

    private func ipv4String(_ pointer: UnsafePointer<UInt8>) -> String {
        var address = in_addr()
        memcpy(&address, pointer, MemoryLayout<in_addr>.size)
        var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
        guard inet_ntop(AF_INET, &address, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil else {
            return "ipv4_ntop_error"
        }
        return String(cString: buffer)
    }

    private func ipv6String(_ pointer: UnsafePointer<UInt8>) -> String {
        var address = in6_addr()
        memcpy(&address, pointer, MemoryLayout<in6_addr>.size)
        var buffer = [CChar](repeating: 0, count: Int(INET6_ADDRSTRLEN))
        guard inet_ntop(AF_INET6, &address, &buffer, socklen_t(INET6_ADDRSTRLEN)) != nil else {
            return "ipv6_ntop_error"
        }
        return String(cString: buffer)
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

    private func errnoDescription(_ value: Int32) -> String {
        String(cString: strerror(value))
    }

    private func setNonBlocking(_ fd: Int32) throws {
        let flags = fcntl(fd, F_GETFL, 0)
        guard flags >= 0 else {
            let flagErrno = errno
            log("[PacketFlowBridge] fcntl(F_GETFL) failed fd=\(fd) errno=\(flagErrno) \(errnoDescription(flagErrno))")
            throw POSIXError(POSIXErrorCode(rawValue: flagErrno) ?? .EIO)
        }
        guard fcntl(fd, F_SETFL, flags | O_NONBLOCK) >= 0 else {
            let setErrno = errno
            log("[PacketFlowBridge] fcntl(F_SETFL O_NONBLOCK) failed fd=\(fd) errno=\(setErrno) \(errnoDescription(setErrno))")
            throw POSIXError(POSIXErrorCode(rawValue: setErrno) ?? .EIO)
        }
        log("[DEBUG][PacketFlowBridge] set non-blocking fd=\(fd) oldFlags=\(flags) newFlags=\(flags | O_NONBLOCK)")
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
            let sigpipeErrno = errno
            log("[PacketFlowBridge] setsockopt(SO_NOSIGPIPE) failed fd=\(fd) errno=\(sigpipeErrno) \(errnoDescription(sigpipeErrno))")
            throw POSIXError(POSIXErrorCode(rawValue: sigpipeErrno) ?? .EIO)
        }
        log("[DEBUG][PacketFlowBridge] disabled SIGPIPE fd=\(fd)")
    }

    private func configureSocketBuffers(_ fd: Int32, requestedBytes: Int) -> (send: Int, receive: Int) {
        var sendBuffer = Int32(requestedBytes)
        let sendResult = setsockopt(
            fd,
            SOL_SOCKET,
            SO_SNDBUF,
            &sendBuffer,
            socklen_t(MemoryLayout<Int32>.size)
        )
        if sendResult != 0 {
            let setErrno = errno
            log(
                "[PacketFlowBridge] failed to set SO_SNDBUF fd=\(fd) " +
                "requested=\(requestedBytes) errno=\(setErrno) \(errnoDescription(setErrno))"
            )
        }

        var receiveBuffer = Int32(requestedBytes)
        let receiveResult = setsockopt(
            fd,
            SOL_SOCKET,
            SO_RCVBUF,
            &receiveBuffer,
            socklen_t(MemoryLayout<Int32>.size)
        )
        if receiveResult != 0 {
            let setErrno = errno
            log(
                "[PacketFlowBridge] failed to set SO_RCVBUF fd=\(fd) " +
                "requested=\(requestedBytes) errno=\(setErrno) \(errnoDescription(setErrno))"
            )
        }

        return (
            send: socketBufferSize(fd: fd, option: SO_SNDBUF),
            receive: socketBufferSize(fd: fd, option: SO_RCVBUF)
        )
    }

    private func socketBufferSize(fd: Int32, option: Int32) -> Int {
        var value: Int32 = 0
        var length = socklen_t(MemoryLayout<Int32>.size)
        let result = getsockopt(fd, SOL_SOCKET, option, &value, &length)
        if result != 0 {
            let getErrno = errno
            log("[PacketFlowBridge] getsockopt option=\(option) failed fd=\(fd) errno=\(getErrno) \(errnoDescription(getErrno))")
            return -1
        }
        return Int(value)
    }

    private func closeIfOpen(_ fd: inout Int32) {
        if fd >= 0 {
            closeFD(fd, label: "fd")
            fd = -1
        }
    }

    private func closeFD(_ fd: Int32, label: String) {
        let rc = Darwin.close(fd)
        if rc == 0 {
            log("[DEBUG][PacketFlowBridge] close OK \(label)=\(fd)")
        } else {
            let closeErrno = errno
            log(
                "[PacketFlowBridge] close failed \(label)=\(fd) " +
                "errno=\(closeErrno) \(errnoDescription(closeErrno))"
            )
        }
    }
}
