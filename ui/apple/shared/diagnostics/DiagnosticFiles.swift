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

    public static func append(
        _ message: String, event: String, level: String, source: String, to path: URL,
        fields: [String: Any] = [:], rotating: Bool = true, legacyDesktop: Bool = false, legacyExcludedPaths: [String] = [], legacyHome: URL? = nil
    ) throws {
        processLock.lock()
        defer { processLock.unlock() }
        try FileManager.default.createDirectory(at: path.deletingLastPathComponent(), withIntermediateDirectories: true)
        try withLock(path, writing: true) {
#if os(macOS)
            if rotating && !initialized.contains(path.path) {
                let home = legacyHome ?? FileManager.default.homeDirectoryForCurrentUser
                let configured = ProcessInfo.processInfo.environment["DOBBY_UI_LOG_PATH"].flatMap { $0.isEmpty ? nil : $0 }
                    ?? home.appendingPathComponent("Library/Logs/DobbyVPN/ui_diagnostics.jsonl").path
                let ownsLegacy = legacyDesktop && (legacyHome != nil || path.standardizedFileURL.path == URL(fileURLWithPath: configured).standardizedFileURL.path)
                try migrateDesktopLegacy(path, includeLegacy: ownsLegacy, excluding: legacyExcludedPaths, homeDirectory: home)
                initialized.insert(path.path)
            }
#endif
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
            var recordWithFields = record
            if !fields.isEmpty { recordWithFields["fields"] = fields }
            var data = try JSONSerialization.data(withJSONObject: recordWithFields)
            data.append(0x0A)
            if rotating {
                if try size(path) >= threshold { try rotate(path) }
            }
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

    private static func hasRecordAfterThreshold(_ path: URL, size: UInt64) throws -> Bool {
        guard size > threshold else { return false }
        return try withFile(open(path, flags: O_RDONLY)) { file in
            try file.seek(toOffset: threshold - 1)
            var remaining = size - threshold
            while remaining > 0 {
                let data = try file.read(upToCount: Int(min(remaining, 65_536))) ?? Data()
                guard !data.isEmpty else { return false }
                if data.contains(10) { return true }
                remaining -= UInt64(data.count)
            }
            return false
        }
    }

    private static func remove(_ path: URL) throws {
        if unlink(path.path) != 0 && errno != ENOENT { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    }

    private static func move(_ from: URL, _ to: URL) throws {
        guard rename(from.path, to.path) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    }

    private static func rotate(_ path: URL) throws { try move(path, previous(path)) }

#if os(macOS)
    private struct LegacySource: Codable, Equatable { let Path: String; let Label: String }
    private struct LegacyCommit: Codable {
        let Sources: [LegacySource]?
        let Previous: Bool
        private enum CodingKeys: String, CodingKey { case Sources, Previous }
        func encode(to encoder: Encoder) throws {
            var values = encoder.container(keyedBy: CodingKeys.self)
            if let Sources { try values.encode(Sources, forKey: .Sources) }
            else { try values.encodeNil(forKey: .Sources) }
            try values.encode(Previous, forKey: .Previous)
        }
    }

    private static func syncDirectory(_ url: URL) throws {
        let fd = Darwin.open(url.path, O_RDONLY | O_CLOEXEC)
        guard fd >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        defer { Darwin.close(fd) }
        guard fsync(fd) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    }

    // Shares the Go owner's durable marker and staging protocol while holding
    // the same destination lock. Never delete legacy input before publication.
    private static func migrateDesktopLegacy(_ path: URL, includeLegacy: Bool, excluding: [String], homeDirectory: URL) throws {
        let home = homeDirectory.appendingPathComponent(".dobbyvpn/app_logs.txt").path
        let oldBackend = homeDirectory.appendingPathComponent("Library/Application Support/DobbyVPN/Logs/backend.jsonl").path
        var sources: [LegacySource] = []
        if includeLegacy {
            sources = [LegacySource(Path: home, Label: "cli"), LegacySource(Path: home + ".stderr", Label: "stderr")]
            if !excluding.contains(oldBackend) && oldBackend != path.path {
                sources += [LegacySource(Path: oldBackend, Label: "backend"), LegacySource(Path: oldBackend + ".stderr", Label: "stderr")]
            }
            sources = sources.filter { $0.Path != path.path }
        }
        let stage = URL(fileURLWithPath: path.path + ".legacy-stage")
        let marker = URL(fileURLWithPath: path.path + ".legacy-commit")
        func finish(_ commit: LegacyCommit) throws {
            guard commit.Sources == nil || commit.Sources == sources else { throw Failure("Legacy migration sources changed during recovery") }
            for isPrevious in [true, false] {
                let destination = isPrevious ? previous(path) : path
                let staged = isPrevious ? previous(stage) : stage
                if isPrevious && !commit.Previous { try remove(destination); continue }
                if FileManager.default.fileExists(atPath: staged.path) { try move(staged, destination) }
                try syncDirectory(path.deletingLastPathComponent())
            }
            for source in commit.Sources ?? [] {
                for suffix in [".previous", ""] { try remove(URL(fileURLWithPath: source.Path + suffix)) }
                let parent = URL(fileURLWithPath: source.Path).deletingLastPathComponent()
                if FileManager.default.fileExists(atPath: parent.path) { try syncDirectory(parent) }
            }
            try syncDirectory(path.deletingLastPathComponent())
            try remove(marker)
            try syncDirectory(path.deletingLastPathComponent())
        }
        if FileManager.default.fileExists(atPath: marker.path) {
            let commit = try JSONDecoder().decode(LegacyCommit.self, from: Data(contentsOf: marker))
            try finish(commit)
            if commit.Sources != nil { return }
        }
        try remove(stage); try remove(previous(stage))
        let hasLegacy = sources.contains(where: { source in
            [".previous", ""].contains { FileManager.default.fileExists(atPath: source.Path + $0) }
        })
        var normalize = false
        if !hasLegacy {
            for existing in [previous(path), path] {
                let existingSize = try size(existing)
                if try hasRecordAfterThreshold(existing, size: existingSize) { normalize = true; break }
            }
        }
        guard hasLegacy || normalize else { return }
        var count: UInt64 = 0
        var atStart = true
        let streams = [LegacySource(Path: path.path, Label: "existing")] + (normalize ? [] : sources)
        for source in streams {
            for suffix in [".previous", ""] {
                let inputPath = URL(fileURLWithPath: source.Path + suffix)
                let input: FileHandle
                do { input = try open(inputPath, flags: O_RDONLY) }
                catch let error as POSIXError where error.code == .ENOENT { continue }
                try withFile(input) { file in
                    var header = try JSONSerialization.data(withJSONObject: ["event": "diagnostics.legacy", "source": source.Label, "generation": suffix], options: [.sortedKeys])
                    header.append(10)
                    if normalize { header.removeAll() }
                    if !atStart && !header.isEmpty { header.insert(10, at: 0); atStart = true }
                    var needsHeader = !header.isEmpty
                    var output: FileHandle?
                    do {
                        while let data = try file.read(upToCount: 65_536), !data.isEmpty {
                            var offset = data.startIndex
                            while offset < data.endIndex {
                                if atStart && count >= threshold {
                                    try output?.close(); output = nil
                                    try rotate(stage); count = 0; needsHeader = !header.isEmpty
                                }
                                if output == nil { output = try open(stage, flags: O_WRONLY | O_CREAT | O_APPEND) }
                                let newline = data[offset...].firstIndex(of: 10)
                                let end = newline.map { data.index(after: $0) } ?? data.endIndex
                                if needsHeader { try output?.write(contentsOf: header); count += UInt64(header.count); needsHeader = false }
                                try output?.write(contentsOf: data[offset..<end])
                                count += UInt64(end - offset)
                                atStart = newline != nil
                                offset = end
                            }
                        }
                        try output?.close(); output = nil
                    } catch {
                        let original = error
                        do { try output?.close() }
                        catch { throw Failure("\(String(reflecting: original))\nClose migration: \(String(reflecting: error))") }
                        throw original
                    }
                }
            }
        }
        var destinationInfo = stat()
        let hasDestination = lstat(path.path, &destinationInfo) == 0
        if !hasDestination && errno != ENOENT { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        for staged in [previous(stage), stage] where FileManager.default.fileExists(atPath: staged.path) {
            try withFile(open(staged, flags: O_WRONLY | O_APPEND)) { output in
                if hasDestination {
                    guard fchmod(output.fileDescriptor, destinationInfo.st_mode & 0o777) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
                    if geteuid() == 0 && fchown(output.fileDescriptor, destinationInfo.st_uid, destinationInfo.st_gid) != 0 {
                        throw POSIXError(.init(rawValue: errno) ?? .EIO)
                    }
                }
                try output.synchronize()
            }
        }
        let commit = LegacyCommit(Sources: normalize ? nil : sources, Previous: FileManager.default.fileExists(atPath: previous(stage).path))
        let temporary = URL(fileURLWithPath: marker.path + ".tmp")
        try remove(temporary)
        try withFile(open(temporary, flags: O_WRONLY | O_CREAT | O_EXCL)) { output in
            try output.write(contentsOf: JSONEncoder().encode(commit))
            try output.synchronize()
        }
        try move(temporary, marker)
        try syncDirectory(path.deletingLastPathComponent())
        try finish(commit)
    }
#endif

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
        guard let data = raw.data(using: .utf8),
              let value = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return Entry(id: id, timestamp: "", level: "RAW", source: stream, message: raw, raw: raw, date: nil)
        }
        let hasTimestamp = !(value["timestamp"] as? String ?? "").isEmpty
        let hasStructuredMessage = value["message"] is String || value["event"] is String
        guard hasTimestamp && hasStructuredMessage else {
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

    public static func export(paths: [URL], to path: URL, header: String, checkCancellation: () throws -> Void = {}) throws -> String {
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
                try checkCancellation()
                try writeGzip(Data("\n--- \(input.path.lastPathComponent) ---\n".utf8), to: compressor)
                var remaining = input.length
                while remaining > 0 {
                    try checkCancellation()
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
