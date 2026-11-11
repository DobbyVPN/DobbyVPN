import Foundation

// The desktop legacy migration of the retained UI log. It is one behavior of
// its own, so it lives beside the shared writer rather than inside it: the
// writer keeps a readable size and the migration keeps its macOS-only code
// together. Rotation and append rules are unchanged and remain in
// DiagnosticFiles.
#if os(macOS)
extension DiagnosticFiles {
    struct LegacySource: Codable, Equatable {
        let path: String
        let label: String
        enum CodingKeys: String, CodingKey { case path = "Path", label = "Label" }
    }
    struct LegacyCommit: Codable {
        let sources: [LegacySource]?
        let previous: Bool
        enum CodingKeys: String, CodingKey { case sources = "Sources", previous = "Previous" }
        func encode(to encoder: Encoder) throws {
            var values = encoder.container(keyedBy: CodingKeys.self)
            if let sources { try values.encode(sources, forKey: .sources) }
            else { try values.encodeNil(forKey: .sources) }
            try values.encode(previous, forKey: .previous)
        }
    }

    static func syncDirectory(_ url: URL) throws {
        let fd = Darwin.open(url.path, O_RDONLY | O_CLOEXEC)
        guard fd >= 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
        defer { Darwin.close(fd) }
        guard fsync(fd) == 0 else { throw POSIXError(.init(rawValue: errno) ?? .EIO) }
    }

    static func readMigrationMarker(_ marker: URL) throws -> Data? {
        do {
            return try withFile(open(marker, flags: O_RDONLY | O_NONBLOCK)) { file in
                let limit = 1_048_576
                var data = Data()
                while data.count <= limit {
                    let chunk = try file.read(upToCount: min(65_536, limit + 1 - data.count)) ?? Data()
                    if chunk.isEmpty { break }
                    data.append(chunk)
                }
                guard data.count <= limit else { throw Failure("Diagnostic migration marker exceeds size limit") }
                return data
            }
        } catch let error as POSIXError where error.code == .ENOENT {
            return nil
        }
    }

    static func finishCommittedMigration(
        _ commit: LegacyCommit, destination path: URL, stage: URL, marker: URL, expectedSources: [LegacySource]
    ) throws {
        guard commit.sources == nil || commit.sources == expectedSources else { throw Failure("Legacy migration sources changed during recovery") }
        for isPrevious in [true, false] {
            let destination = isPrevious ? previous(path) : path
            let staged = isPrevious ? previous(stage) : stage
            if isPrevious && !commit.previous { try remove(destination); continue }
            if FileManager.default.fileExists(atPath: staged.path) { try move(staged, destination) }
            try syncDirectory(path.deletingLastPathComponent())
        }
        // A missing staged file is valid only if an earlier attempt already
        // published a regular destination. Never discard inputs otherwise.
        try withFile(open(path, flags: O_RDONLY | O_NONBLOCK)) { _ in }
        if commit.previous { try withFile(open(previous(path), flags: O_RDONLY | O_NONBLOCK)) { _ in } }
        for source in commit.sources ?? [] {
            for suffix in [".previous", ""] { try remove(URL(fileURLWithPath: source.path + suffix)) }
            let parent = URL(fileURLWithPath: source.path).deletingLastPathComponent()
            if FileManager.default.fileExists(atPath: parent.path) { try syncDirectory(parent) }
        }
        try syncDirectory(path.deletingLastPathComponent())
        try remove(marker)
        try syncDirectory(path.deletingLastPathComponent())
    }

    static func stageLegacyStreams(_ streams: [LegacySource], stage: URL, normalize: Bool) throws {
        var count: UInt64 = 0
        var atStart = true
        for source in streams {
            for suffix in [".previous", ""] {
                let inputPath = URL(fileURLWithPath: source.path + suffix)
                let input: FileHandle
                do { input = try open(inputPath, flags: O_RDONLY) }
                catch let error as POSIXError where error.code == .ENOENT { continue }
                try withFile(input) { file in
                    var header = try JSONSerialization.data(withJSONObject: ["event": "diagnostics.legacy", "source": source.label, "generation": suffix], options: [.sortedKeys])
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
        // Empty legacy inputs still publish an empty current generation.
        if !FileManager.default.fileExists(atPath: stage.path) {
            try withFile(open(stage, flags: O_WRONLY | O_CREAT | O_EXCL)) { _ in }
        }
    }

    // Shares the Go owner's durable marker and staging protocol while holding
    // the same destination lock. Never delete legacy input before publication.
    static func migrateDesktopLegacy(_ path: URL, includeLegacy: Bool, excluding: [String], homeDirectory: URL) throws {
        let home = homeDirectory.appendingPathComponent(".dobbyvpn/app_logs.txt").path
        let oldBackend = homeDirectory.appendingPathComponent("Library/Application Support/DobbyVPN/Logs/backend.jsonl").path
        var sources: [LegacySource] = []
        if includeLegacy {
            sources = [LegacySource(path: home, label: "cli"), LegacySource(path: home + ".stderr", label: "stderr")]
            if !excluding.contains(oldBackend) && oldBackend != path.path {
                sources += [LegacySource(path: oldBackend, label: "backend"), LegacySource(path: oldBackend + ".stderr", label: "stderr")]
            }
            sources = sources.filter { $0.path != path.path }
        }
        let stage = URL(fileURLWithPath: path.path + ".legacy-stage")
        let marker = URL(fileURLWithPath: path.path + ".legacy-commit")
        let markerData = try readMigrationMarker(marker)
        if let markerData {
            let commit = try JSONDecoder().decode(LegacyCommit.self, from: markerData)
            try finishCommittedMigration(commit, destination: path, stage: stage, marker: marker, expectedSources: sources)
            if commit.sources != nil { return }
        }
        try remove(stage); try remove(previous(stage))
        let hasLegacy = sources.contains(where: { source in
            [".previous", ""].contains { FileManager.default.fileExists(atPath: source.path + $0) }
        })
        var normalize = false
        if !hasLegacy {
            for existing in [previous(path), path] {
                let existingSize = try size(existing)
                if try hasRecordAfterThreshold(existing, size: existingSize) { normalize = true; break }
            }
        }
        guard hasLegacy || normalize else { return }
        let streams = [LegacySource(path: path.path, label: "existing")] + (normalize ? [] : sources)
        try stageLegacyStreams(streams, stage: stage, normalize: normalize)
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
        let commit = LegacyCommit(sources: normalize ? nil : sources, previous: FileManager.default.fileExists(atPath: previous(stage).path))
        let temporary = URL(fileURLWithPath: marker.path + ".tmp")
        try remove(temporary)
        try withFile(open(temporary, flags: O_WRONLY | O_CREAT | O_EXCL)) { output in
            try output.write(contentsOf: JSONEncoder().encode(commit))
            try output.synchronize()
        }
        try move(temporary, marker)
        try syncDirectory(path.deletingLastPathComponent())
        try finishCommittedMigration(commit, destination: path, stage: stage, marker: marker, expectedSources: sources)
    }
}
#endif
