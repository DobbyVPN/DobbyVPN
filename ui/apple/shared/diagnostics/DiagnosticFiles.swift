import Darwin
import Foundation
import zlib

public enum DiagnosticFiles {
    public static let threshold: UInt64 = 150_000_000
    private static let processLock = NSLock()
    private static var initialized = Set<String>()
    private static let runID = UUID().uuidString
    private static var sequence: UInt64 = 0

    public struct Failure: Error, CustomStringConvertible {
        public let description: String
        init(_ message: String) { description = message }
    }

    public static func append(_ message: String, event: String, level: String, source: String, to path: URL) throws {
        processLock.lock()
        defer { processLock.unlock() }
        try FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true)
        try withLock(path, writing: true) {
            if !initialized.contains(path.path) {
                try migrate(path)
                initialized.insert(path.path)
            }
            sequence += 1
#if DEBUG
            let configuration = "Debug"
#else
            let configuration = "Release"
#endif
            let record: [String: Any] = [
                "schema": "dobby.log/v1", "timestamp": ISO8601DateFormatter().string(from: Date()),
                "source": source, "level": level, "event": event, "message": message,
                "process_id": ProcessInfo.processInfo.processIdentifier, "run_id": runID, "process_sequence": sequence,
                "build": ["version": Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "development",
                          "commit": Bundle.main.infoDictionary?["DobbySourceCommit"] as? String ?? "N/A",
                          "configuration": configuration, "platform": ProcessInfo.processInfo.operatingSystemVersionString],
            ]
            var data = try JSONSerialization.data(withJSONObject: record)
            data.append(0x0A)
            if try size(path) >= threshold { try rotate(path) }
            try withFile(try open(path, flags: O_WRONLY | O_CREAT | O_APPEND)) { try $0.write(contentsOf: data) }
        }
    }

    private static func previous(_ path: URL) -> URL { URL(fileURLWithPath: path.path + ".previous") }

    private static func open(_ path: URL, flags: Int32) throws -> FileHandle {
        let descriptor = Darwin.open(path.path, flags | O_CLOEXEC | O_NOFOLLOW, 0o600)
        guard descriptor >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        let file = FileHandle(fileDescriptor: descriptor, closeOnDealloc: true)
        var info = stat()
        guard fstat(descriptor, &info) == 0, info.st_mode & mode_t(S_IFMT) == mode_t(S_IFREG) else {
            let error = Failure("Diagnostic input is not a regular file: \(path.path); errno=\(errno)")
            do { try file.close() } catch let cleanup { throw Failure("\(error)\nClose: \(String(reflecting: cleanup))") }
            throw error
        }
        return file
    }

    private static func withFile<T>(_ file: FileHandle, _ body: (FileHandle) throws -> T) throws -> T {
        let value: T
        do { value = try body(file) } catch {
            let original = error
            do { try file.close() } catch { throw Failure("\(String(reflecting: original))\nClose: \(String(reflecting: error))") }
            throw original
        }
        try file.close()
        return value
    }

    private static func withLock<T>(_ path: URL, writing: Bool, _ body: () throws -> T) throws -> T {
        let lock: FileHandle
        do { lock = try open(URL(fileURLWithPath: path.path + ".lock"), flags: writing ? O_CREAT | O_RDWR : O_RDONLY) }
        catch let error as POSIXError where !writing && error.code == .ENOENT { return try body() }
        return try withFile(lock) { file in
            // flock locks independent open file descriptions even when Go and
            // Swift share a process. Closing this handle releases the lock.
            while flock(file.fileDescriptor, writing ? LOCK_EX : LOCK_SH) != 0 {
                if errno != EINTR { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
            }
            return try body()
        }
    }

    private static func size(_ path: URL) throws -> UInt64 {
        do { return try withFile(open(path, flags: O_RDONLY)) { try $0.seekToEnd() } }
        catch let error as POSIXError where error.code == .ENOENT { return 0 }
    }

    private static func remove(_ path: URL) throws {
        if unlink(path.path) != 0 && errno != ENOENT { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    }

    private static func move(_ from: URL, _ to: URL) throws {
        guard rename(from.path, to.path) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    }

    private static func rotate(_ path: URL) throws { try move(path, previous(path)) }

    private static func migrate(_ path: URL) throws {
        let paths = [previous(path), path]
        guard try paths.contains(where: { try size($0) > threshold }) else { return }
        let stage = URL(fileURLWithPath: path.path + ".migration-" + UUID().uuidString)
        var output: FileHandle? = try open(stage, flags: O_CREAT | O_EXCL | O_WRONLY)
        var failure: Error?
        do {
            var count: UInt64 = 0
            var atStart = true
            for source in paths {
                let input: FileHandle
                do { input = try open(source, flags: O_RDONLY) }
                catch let error as POSIXError where error.code == .ENOENT { continue }
                try withFile(input) { file in
                    while let data = try file.read(upToCount: 65_536), !data.isEmpty {
                        var offset = data.startIndex
                        while offset < data.endIndex {
                            if atStart && count >= threshold {
                                try output?.close(); output = nil
                                try rotate(stage)
                                output = try open(stage, flags: O_CREAT | O_EXCL | O_WRONLY)
                                count = 0
                            }
                            let newline = data[offset...].firstIndex(of: 0x0A)
                            let end = newline.map { data.index(after: $0) } ?? data.endIndex
                            try output?.write(contentsOf: data[offset..<end])
                            count += UInt64(end - offset)
                            atStart = newline != nil
                            offset = end
                        }
                    }
                }
            }
            try output?.close(); output = nil
            try remove(previous(path))
            if FileManager.default.fileExists(atPath: previous(stage).path) { try move(previous(stage), previous(path)) }
            try move(stage, path)
        } catch { failure = error }
        var cleanup: [String] = []
        do { try output?.close() } catch { cleanup.append(String(reflecting: error)) }
        for temporary in [stage, previous(stage)] {
            do { try remove(temporary) } catch { cleanup.append(String(reflecting: error)) }
        }
        if let failure { cleanup.insert(String(reflecting: failure), at: 0) }
        if !cleanup.isEmpty { throw Failure(cleanup.joined(separator: "\n")) }
    }

    private struct Input { let path: URL; let file: FileHandle; let length: UInt64 }

    private static func capture(_ paths: [URL], issues: inout [String]) -> [Input] {
        processLock.lock()
        defer { processLock.unlock() }
        var inputs: [Input] = []
        for path in paths {
            do {
                try withLock(path, writing: false) {
                    for name in [previous(path), path] {
                        do {
                            let file = try open(name, flags: O_RDONLY)
                            do {
                                let length = try file.seekToEnd()
                                try file.seek(toOffset: 0)
                                inputs.append(Input(path: name, file: file, length: length))
                            } catch {
                                let original = error
                                do { try file.close() } catch { issues.append("\(name.path): \(String(reflecting: error))") }
                                throw original
                            }
                        } catch let error as POSIXError where error.code == .ENOENT { continue }
                        catch { issues.append("\(name.path): \(String(reflecting: error))") }
                    }
                }
            } catch { issues.append("\(path.path): \(String(reflecting: error))") }
        }
        return inputs
    }

    public struct Entry: Identifiable, Equatable, Sendable {
        public let id: String
        public let timestamp: String
        public let level: String
        public let source: String
        public let message: String
        public let raw: String
        public let date: Date?
    }

    private static func identity(_ file: FileHandle) throws -> String {
        var info = stat()
        guard fstat(file.fileDescriptor, &info) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        return "\(info.st_dev):\(info.st_ino)"
    }

    public static func clearView(paths: [URL], boundary: URL) throws {
        var issues: [String] = []
        let inputs = capture(paths, issues: &issues)
        var offsets: [String: UInt64] = [:]
        for input in inputs {
            do { try withFile(input.file) { offsets[try identity($0)] = input.length } }
            catch { issues.append(String(reflecting: error)) }
        }
        guard issues.isEmpty else { throw Failure(issues.joined(separator: "\n")) }
        try FileManager.default.createDirectory(at: boundary.deletingLastPathComponent(), withIntermediateDirectories: true)
        try JSONEncoder().encode(offsets).write(to: boundary, options: .atomic)
    }

    public static func entries(paths: [URL], boundary: URL) -> (entries: [Entry], error: String) {
        var issues: [String] = []
        var offsets: [String: UInt64] = [:]
        do {
            if FileManager.default.fileExists(atPath: boundary.path) { offsets = try JSONDecoder().decode([String: UInt64].self, from: Data(contentsOf: boundary)) }
        } catch { return ([], "Viewing boundary: \(String(reflecting: error))") }
        var entries: [Entry] = []
        for input in capture(paths, issues: &issues) {
            do {
                try withFile(input.file) { file in
                    let id = try identity(file)
                    let cleared = min(offsets[id] ?? 0, input.length)
                    let start = max(cleared, input.length > 131_072 ? input.length - 131_072 : 0)
                    try file.seek(toOffset: start > 0 ? start - 1 : 0)
                    var data = try file.read(upToCount: 131_073) ?? Data()
                    var position = start
                    if start > 0 {
                        let atBoundary = data.first == 10
                        if !data.isEmpty { data.removeFirst() }
                        if !atBoundary {
                            if let newline = data.firstIndex(of: 10) {
                                position += UInt64(data.distance(from: data.startIndex, to: newline) + 1)
                                data = Data(data[data.index(after: newline)...])
                            } else { return }
                        }
                    }
                    // The producer may still be writing the final UTF-8 character.
                    if let lead = data.lastIndex(where: { $0 & 0xC0 != 0x80 }) {
                        let byte = data[lead]
                        let expected = byte >= 0xF0 && byte <= 0xF4 ? 4 : byte >= 0xE0 && byte <= 0xEF ? 3 : byte >= 0xC2 && byte <= 0xDF ? 2 : 1
                        if data.distance(from: lead, to: data.endIndex) < expected { data.removeSubrange(lead...) }
                    }
                    let stream = friendlyStream(input.path.lastPathComponent)
                    let lines = String(decoding: data, as: UTF8.self).split(separator: "\n", omittingEmptySubsequences: false)
                    for (index, line) in lines.enumerated() {
                        let raw = String(line)
                        defer { position += UInt64(raw.utf8.count + 1) }
                        if raw.isEmpty { continue }
                        // A growing JSON record is held until its newline arrives.
                        if index == lines.count - 1 && raw.hasPrefix("{") { continue }
                        entries.append(parseEntry(raw, id: "\(id):\(position)", stream: stream))
                    }
                }
            } catch { issues.append("\(input.path.path): \(String(reflecting: error))") }
        }
        let datedPositions = entries.indices.filter { entries[$0].date != nil }
        let datedEntries = datedPositions.map { entries[$0] }.enumerated().sorted { lhs, rhs in
            guard let left = lhs.element.date, let right = rhs.element.date else { return lhs.offset < rhs.offset }
            return left == right ? lhs.offset < rhs.offset : left < right
        }.map(\.element)
        var ordered = entries
        for (position, entry) in zip(datedPositions, datedEntries) { ordered[position] = entry }
        return (ordered, issues.joined(separator: "\n"))
    }

    public static func friendlyStream(_ name: String) -> String {
        let name = name.replacingOccurrences(of: ".previous", with: "")
        if name.contains("stderr") { return name.contains("tunnel") ? "Tunnel stderr" : "Backend stderr" }
        if name.contains("ui") || name.contains("app") { return "App" }
        if name.contains("tunnel") { return "Tunnel" }
        return "Backend"
    }

    public static func parseEntry(_ raw: String, id: String, stream: String) -> Entry {
        guard let data = raw.data(using: .utf8), let value = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return Entry(id: id, timestamp: "", level: "RAW", source: stream, message: raw, raw: raw, date: nil)
        }
        let timestamp = value["timestamp"] as? String ?? ""
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let fractional = formatter.date(from: timestamp)
        formatter.formatOptions = [.withInternetDateTime]
        let date = fractional ?? formatter.date(from: timestamp)
        let capture = value["event"] as? String == "stderr.capture"
        return Entry(id: id, timestamp: timestamp, level: capture ? "INFO" : (value["level"] as? String ?? "INFO").uppercased(),
                     source: stream + ((value["source"] as? String).map { " · " + $0 } ?? ""),
                     message: capture ? "Stderr capture initialized" : value["message"] as? String ?? raw, raw: raw, date: date)
    }

    public static func export(paths: [URL], to path: URL, header: String) throws -> String {
        var issues: [String] = []
        let inputs = capture(paths, issues: &issues)
        var compressor: gzFile?
        var created = false
        var failed = false
        do {
            let descriptor = Darwin.open(path.path, O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0o600)
            guard descriptor >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
            created = true
            compressor = gzdopen(descriptor, "wb")
            guard let compressor else {
                let cause = errno
                guard Darwin.close(descriptor) == 0 else { throw Failure("Create gzip errno=\(cause); close errno=\(errno)") }
                throw Failure("Create gzip errno=\(cause)")
            }
            try writeGzip(Data(header.utf8), to: compressor)
            for input in inputs {
                try writeGzip(Data("\n--- \(input.path.lastPathComponent) ---\n".utf8), to: compressor)
                var remaining = input.length
                while remaining > 0 {
                    let data: Data
                    do {
                        data = try input.file.read(upToCount: Int(min(65_536, remaining))) ?? Data()
                        guard !data.isEmpty else { throw Failure("Log shortened during export") }
                    } catch { issues.append("\(input.path.path): \(String(reflecting: error))"); break }
                    try writeGzip(data, to: compressor)
                    remaining -= UInt64(data.count)
                }
            }
        } catch { failed = true; issues.insert(String(reflecting: error), at: 0) }
        for input in inputs {
            do { try input.file.close() } catch { issues.append("\(input.path.path): \(String(reflecting: error))") }
        }
        if let compressor {
            if !failed && !issues.isEmpty {
                do { try writeGzip(Data("\nCollection errors\n\(issues.joined(separator: "\n"))\n".utf8), to: compressor) }
                catch { failed = true; issues.append(String(reflecting: error)) }
            }
            let result = gzclose(compressor)
            if result != Z_OK { failed = true; issues.append("Close gzip failed: zlib=\(result), errno=\(errno)") }
        }
        if failed {
            if created { do { try remove(path) } catch { issues.append("Remove partial export: \(String(reflecting: error))") } }
            throw Failure(issues.joined(separator: "\n"))
        }
        return issues.joined(separator: "\n")
    }

    private static func writeGzip(_ data: Data, to output: gzFile) throws {
        try data.withUnsafeBytes { bytes in
            guard let base = bytes.baseAddress else { return }
            var offset = 0
            while offset < bytes.count {
                let count = min(65_536, bytes.count - offset)
                guard gzwrite(output, base.advanced(by: offset), UInt32(count)) == Int32(count) else {
                    var code: Int32 = 0
                    let message = gzerror(output, &code).map { String(cString: $0) } ?? "gzip write failed"
                    throw Failure("\(message); zlib=\(code), errno=\(errno)")
                }
                offset += count
            }
        }
    }
}
