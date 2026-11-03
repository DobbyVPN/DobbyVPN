using System;
using System.Collections.Generic;
using System.IO;
using System.IO.Compression;
using System.ComponentModel;
using System.Reflection;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
using System.Linq;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;
using System.Threading;

namespace DobbyVPN.Windows;

// UI-thread-owned writer; file reads allow the backend and UI to keep appending.
internal sealed class NativeDiagnostics(string backendPath, string uiPath)
{
    internal static NativeDiagnostics Current { get; } = new(
        string.IsNullOrEmpty(Environment.GetEnvironmentVariable("DOBBY_LOG_PATH"))
            ? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "DobbyVPN", "Logs", "backend.jsonl")
            : Environment.GetEnvironmentVariable("DOBBY_LOG_PATH")!,
        string.IsNullOrEmpty(Environment.GetEnvironmentVariable("DOBBY_UI_LOG_PATH"))
            ? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DobbyVPN", "Logs", "ui_diagnostics.jsonl")
            : Environment.GetEnvironmentVariable("DOBBY_UI_LOG_PATH")!);
    private readonly object _gate = new();
    private readonly string[] _paths = [backendPath, uiPath];
    private readonly Dictionary<string, string> _lastErrors = new();
    private readonly string _run = Guid.NewGuid().ToString("N");
    private long _sequence;
    internal string UiPath => uiPath;
    public string WriteFailure { get; private set; } = "";

    public void Record(string message, string category)
    {
        lock (_gate)
        {
            if (_lastErrors.GetValueOrDefault(category) == message) return;
            _lastErrors[category] = message;
            if (string.IsNullOrEmpty(message)) return;
            Write("ERROR", category, message, null);
        }
    }

    private void RecordReadFailure(string path, Exception? error)
    {
        lock (_gate)
        {
            var key = "\0read:" + path;
            if (error is null)
            {
                _lastErrors.Remove(key);
                return;
            }
            var message = error.ToString();
            if (_lastErrors.GetValueOrDefault(key) == message) return;
            _lastErrors[key] = message;
            Write("ERROR", "ui.logs.read_failed", message, new { path });
        }
    }

    public void RecordInfo(string category, string message, object fields)
    {
        lock (_gate) Write("INFO", category, message, fields);
    }

    public void RecordDebug(string category, string message, object fields)
    {
        lock (_gate) Write("DEBUG", category, message, fields);
    }

    private void Write(string level, string category, string message, object? fields)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(uiPath)!);
            var line = JsonSerializer.Serialize(new
            {
                schema = "dobby.log/v1", timestamp = DateTimeOffset.UtcNow,
                source = "windows-ui", level, @event = category, message, fields,
                process_id = Environment.ProcessId, run_id = _run, process_sequence = ++_sequence,
                build = new {
                    version = Assembly.GetExecutingAssembly().GetName().Version?.ToString(),
                    commit = Assembly.GetExecutingAssembly().GetCustomAttributes<AssemblyMetadataAttribute>()
                        .FirstOrDefault(item => item.Key == "DobbySourceCommit")?.Value,
                    configuration = Assembly.GetExecutingAssembly().GetCustomAttribute<AssemblyConfigurationAttribute>()?.Configuration,
                    platform = Environment.OSVersion.ToString(), architecture = RuntimeInformation.ProcessArchitecture.ToString()
                }
            });
            NativeLogFiles.Append(uiPath, Encoding.UTF8.GetBytes(line + "\n"));
        }
        catch (Exception error)
        {
            WriteFailure = $"UI diagnostic write failed: {error}\nOriginal diagnostic: {message}";
            System.Diagnostics.Trace.TraceError(WriteFailure);
        }
    }

    private static FileStream OpenLog(string path) =>
        new(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete, 65536, true);

    public async Task SaveAsync(string destination, string header)
    {
        var path = Path.GetFullPath(destination);
        if (_paths.SelectMany(source => new[] {source, source + ".previous", source + ".lock"}).Any(source => string.Equals(Path.GetFullPath(source), path, StringComparison.OrdinalIgnoreCase)))
            throw new IOException("Choose an export destination outside the diagnostic input files.");
        var temporary = Path.Combine(Path.GetDirectoryName(path)!, $".dobby-export-{Guid.NewGuid():N}.tmp");
        try
        {
            await using (var output = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None, 65536, true))
                await ExportAsync(output, header);
            File.Move(temporary, path, true);
        }
        catch (Exception original)
        {
            try { File.Delete(temporary); }
            catch (Exception cleanup) { throw new AggregateException(original, cleanup); }
            throw;
        }
    }

    internal sealed record Entry(string Id, string Timestamp, string Level, string Source, string Message, string Raw, DateTimeOffset? Time);

    [StructLayout(LayoutKind.Sequential)]
    private struct FileInformation
    {
        public uint Attributes;
        public System.Runtime.InteropServices.ComTypes.FILETIME Creation, Access, Write;
        public uint Volume, SizeHigh, SizeLow, Links, IndexHigh, IndexLow;
    }
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetFileInformationByHandle(SafeFileHandle file, out FileInformation information);
    private static string Identity(FileStream file)
    {
        if (!GetFileInformationByHandle(file.SafeFileHandle, out var info)) throw new Win32Exception(Marshal.GetLastWin32Error());
        return $"{info.Volume}:{info.IndexHigh}:{info.IndexLow}";
    }

    public Task ClearViewAsync() => Task.Run(() =>
    {
        var offsets = new Dictionary<string, long>();
        foreach (var path in _paths)
            NativeLogFiles.WithLock(path, false, () =>
            {
                foreach (var name in new[] { path + ".previous", path })
                {
                    try { using var file = OpenLog(name); offsets[Identity(file)] = file.Length; }
                    catch (FileNotFoundException) { }
                    catch (DirectoryNotFoundException) { }
                }
            });
        var boundary = uiPath + ".view";
        Directory.CreateDirectory(Path.GetDirectoryName(boundary)!);
        var temporary = boundary + ".tmp";
        try
        {
            using (var output = new FileStream(temporary, FileMode.Create, FileAccess.Write))
            {
                JsonSerializer.Serialize(output, offsets);
                output.Flush(true);
            }
            File.Move(temporary, boundary, true);
        }
        finally { File.Delete(temporary); }
    });

    public Task<(List<Entry> Entries, string Error)> EntriesAsync() => Task.Run(() =>
    {
        var entries = new List<Entry>();
        var issues = new List<string>();
        Dictionary<string, long> offsets;
        var boundary = uiPath + ".view";
        try
        {
            offsets = File.Exists(boundary) ? JsonSerializer.Deserialize<Dictionary<string, long>>(File.ReadAllText(boundary)) ?? [] : [];
            RecordReadFailure(boundary, null);
        }
        catch (Exception error)
        {
            RecordReadFailure(boundary, error);
            return (entries, $"Viewing boundary: {error}");
        }
        foreach (var path in _paths.SelectMany(path => new[] { path + ".previous", path }))
        {
            try
            {
                using var file = OpenLog(path);
                var id = Identity(file);
                var size = file.Length;
                var start = Math.Max(Math.Min(offsets.GetValueOrDefault(id), size), Math.Max(0, size - 131072));
                file.Position = start > 0 ? start - 1 : 0;
                var bytes = new byte[(int)Math.Min(size - file.Position, 131073)];
                file.ReadExactly(bytes);
                var position = start;
                if (start > 0 && bytes.Length > 0)
                {
                    var boundaryLine = bytes[0] == 10;
                    bytes = bytes[1..];
                    if (!boundaryLine)
                    {
                        var newline = Array.IndexOf(bytes, (byte)10);
                        if (newline < 0) { RecordReadFailure(path, null); continue; }
                        position += newline + 1;
                        bytes = bytes[(newline + 1)..];
                    }
                }
                var name = Path.GetFileName(path);
                var stream = name.Contains("stderr") ? "Backend stderr" : name.Contains("ui") ? "App" : "Backend";
                var characters = new char[bytes.Length];
                // Keep an incomplete final UTF-8 character pending until the next read.
                var length = Encoding.UTF8.GetDecoder().GetChars(bytes, 0, bytes.Length, characters, 0, flush: false);
                var lines = new string(characters, 0, length).Split('\n');
                for (var index = 0; index < lines.Length; index++)
                {
                    var raw = lines[index];
                    var entryId = $"{id}:{position}";
                    position += Encoding.UTF8.GetByteCount(raw) + 1;
                    if (raw.Length == 0 || index == lines.Length - 1 && raw.StartsWith('{')) continue;
                    entries.Add(ParseEntry(raw, entryId, stream));
                }
                RecordReadFailure(path, null);
            }
            catch (FileNotFoundException) { RecordReadFailure(path, null); }
            catch (DirectoryNotFoundException) { RecordReadFailure(path, null); }
            catch (Exception error) { issues.Add($"{path}: {error}"); RecordReadFailure(path, error); }
        }
        if (WriteFailure.Length > 0) issues.Add(WriteFailure);
        var datedPositions = entries.Select((entry, index) => (entry, index)).Where(item => item.entry.Time is not null).ToArray();
        var datedEntries = datedPositions.OrderBy(item => item.entry.Time).ThenBy(item => item.index).Select(item => item.entry).ToArray();
        var ordered = entries.ToArray();
        for (var index = 0; index < datedPositions.Length; index++) ordered[datedPositions[index].index] = datedEntries[index];
        return (ordered.ToList(), string.Join("\n", issues));
    });

    internal static Entry ParseEntry(string raw, string id, string stream)
    {
        try
        {
            using var document = JsonDocument.Parse(raw);
            var value = document.RootElement;
            string Field(string key, string fallback = "") => value.TryGetProperty(key, out var field) && field.ValueKind == JsonValueKind.String ? field.GetString()! : fallback;
            var timestamp = Field("timestamp");
            var capture = Field("event") == "stderr.capture";
            return new Entry(id, timestamp, capture ? "INFO" : Field("level", "INFO").ToUpperInvariant(),
                string.Join(" · ", new[] { stream, Field("source") }.Where(text => text.Length > 0)),
                capture ? "Stderr capture initialized" : Field("message", raw), raw,
                DateTimeOffset.TryParse(timestamp, System.Globalization.CultureInfo.InvariantCulture, System.Globalization.DateTimeStyles.RoundtripKind, out var time) ? time : null);
        }
        catch (Exception error) when (error is JsonException or InvalidOperationException)
        { return new Entry(id, "", "RAW", stream, raw, raw, null); }
    }

    public async Task ExportAsync(Stream destination, string header, CancellationToken cancellationToken = default, bool requireComplete = false)
    {
        var inputs = new List<(string Path, FileStream File, long Length)>();
        var issues = new List<string>();
        Exception? original = null;
        try
        {
            // Capture all handles and lengths before compression begins. Locks
            // are released immediately, so producers can rotate during copying.
            foreach (var path in _paths)
            {
                try
                {
                    NativeLogFiles.WithLock(path, false, () =>
                    {
                        foreach (var name in new[] {path + ".previous", path})
                        {
                            try
                            {
                                var input = OpenLog(name);
                                try { inputs.Add((name, input, input.Length)); }
                                catch { input.Dispose(); throw; }
                            }
                            catch (FileNotFoundException) { }
                            catch (DirectoryNotFoundException) { }
                            catch (Exception error) { issues.Add($"{name}: {error}"); }
                        }
                    });
                }
                catch (Exception error) { issues.Add($"{path}: {error}"); }
            }
            await using var output = new GZipStream(destination, CompressionLevel.Optimal, leaveOpen: true);
            await output.WriteAsync(Encoding.UTF8.GetBytes(header), cancellationToken);
            var buffer = new byte[65536];
            foreach (var (path, input, length) in inputs)
            {
                await output.WriteAsync(Encoding.UTF8.GetBytes($"\n--- {Path.GetFileName(path)} ---\n"), cancellationToken);
                var remaining = length;
                while (remaining > 0)
                {
                    int count;
                    try
                    {
                        count = await input.ReadAsync(buffer.AsMemory(0, (int)Math.Min(buffer.Length, remaining)), cancellationToken);
                        if (count == 0) throw new EndOfStreamException("Log shortened during export");
                    }
                    catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { throw; }
                    catch (Exception error) { issues.Add($"{path}: {error}"); break; }
                    await output.WriteAsync(buffer.AsMemory(0, count), cancellationToken);
                    remaining -= count;
                }
            }
            foreach (var (_, input, _) in inputs)
            {
                try { await input.DisposeAsync(); }
                catch (Exception error) { issues.Add(error.ToString()); }
            }
            inputs.Clear();
            if (!string.IsNullOrEmpty(WriteFailure)) issues.Add(WriteFailure);
            if (issues.Count > 0 && requireComplete) throw new IOException("Diagnostic collection failed: " + string.Join("\n", issues));
            if (issues.Count > 0)
                await output.WriteAsync(Encoding.UTF8.GetBytes("\nCollection errors\n" + string.Join("\n", issues) + "\n"), cancellationToken);
        }
        catch (Exception error) { original = error; throw; }
        finally
        {
            var failures = new List<Exception>();
            foreach (var (_, input, _) in inputs)
                try { await input.DisposeAsync(); } catch (Exception error) { failures.Add(error); }
            if (failures.Count > 0)
            {
                if (original is not null) failures.Insert(0, original);
                throw new AggregateException(failures);
            }
        }
    }
}

// The same one-byte OS lock is used by Go writers and native collectors.
internal static class NativeLogFiles
{
    internal const long Threshold = 150_000_000;
    private static readonly object Gate = new();
    private static readonly HashSet<string> Initialized = new(StringComparer.OrdinalIgnoreCase);

    [StructLayout(LayoutKind.Sequential)]
    private struct Overlapped { public IntPtr Internal, InternalHigh; public uint Offset, OffsetHigh; public IntPtr Event; }
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool LockFileEx(SafeFileHandle file, uint flags, uint reserved, uint low, uint high, ref Overlapped overlapped);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool UnlockFileEx(SafeFileHandle file, uint reserved, uint low, uint high, ref Overlapped overlapped);

    internal static void WithLock(string path, bool writing, Action action)
    {
        lock (Gate)
        {
            FileStream file;
            try { file = new FileStream(path + ".lock", writing ? FileMode.OpenOrCreate : FileMode.Open,
                writing ? FileAccess.ReadWrite : FileAccess.Read, FileShare.ReadWrite); }
            catch (FileNotFoundException) when (!writing) { action(); return; }
            catch (DirectoryNotFoundException) when (!writing) { action(); return; }
            using (file)
            {
                var overlapped = new Overlapped();
                if (!LockFileEx(file.SafeFileHandle, writing ? 2u : 0u, 0, 1, 0, ref overlapped))
                    throw new Win32Exception(Marshal.GetLastWin32Error());
                Exception? original = null;
                try { action(); } catch (Exception error) { original = error; throw; }
                finally
                {
                    if (!UnlockFileEx(file.SafeFileHandle, 0, 1, 0, ref overlapped))
                    {
                        var error = new Win32Exception(Marshal.GetLastWin32Error());
                        if (original is not null) throw new AggregateException(original, error);
                        throw error;
                    }
                }
            }
        }
    }

    internal static void Append(string path, byte[] record)
    {
        WithLock(path, true, () =>
        {
            if (!Initialized.Contains(path)) { Migrate(path); Initialized.Add(path); }
            if (File.Exists(path) && new FileInfo(path).Length >= Threshold) Rotate(path);
            using var file = new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite | FileShare.Delete);
            file.Write(record);
        });
    }

    private static void Rotate(string path) { File.Delete(path + ".previous"); File.Move(path, path + ".previous"); }

    private sealed record LegacySource(string Path, string Label);
    private sealed record LegacyCommit(LegacySource[]? Sources, bool Previous);

    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool MoveFileEx(string existing, string destination, uint flags);

    [DllImport("advapi32.dll", CharSet = CharSet.Unicode)]
    private static extern uint GetNamedSecurityInfo(string name, int type, uint information, out IntPtr owner, out IntPtr group, out IntPtr dacl, out IntPtr sacl, out IntPtr descriptor);
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode)]
    private static extern uint SetNamedSecurityInfo(string name, int type, uint information, IntPtr owner, IntPtr group, IntPtr dacl, IntPtr sacl);
    [DllImport("advapi32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetSecurityDescriptorControl(IntPtr descriptor, out ushort control, out uint revision);
    [DllImport("kernel32.dll")]
    private static extern IntPtr LocalFree(IntPtr memory);

    private static void PreserveAcl(string source, string destination)
    {
        if (!File.Exists(source)) return;
        var status = GetNamedSecurityInfo(source, 1, 4, out _, out _, out var dacl, out _, out var descriptor);
        if (status != 0) throw new Win32Exception((int)status);
        try
        {
            if (!GetSecurityDescriptorControl(descriptor, out var control, out _)) throw new Win32Exception(Marshal.GetLastWin32Error());
            var flags = 4u | ((control & 0x1000) != 0 ? 0x80000000u : 0x20000000u);
            status = SetNamedSecurityInfo(destination, 1, flags, IntPtr.Zero, IntPtr.Zero, dacl, IntPtr.Zero);
            if (status != 0) throw new Win32Exception((int)status);
        }
        finally { LocalFree(descriptor); }
    }

    private static void Publish(string source, string destination)
    {
        // MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH, matching Go.
        if (!MoveFileEx(source, destination, 9)) throw new Win32Exception(Marshal.GetLastWin32Error());
    }

    private static void Migrate(string path)
    {
        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        var cli = Path.Combine(home, ".dobbyvpn", "app_logs.txt");
        var configuredPath = Environment.GetEnvironmentVariable("DOBBY_UI_LOG_PATH");
        if (string.IsNullOrEmpty(configuredPath)) configuredPath = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DobbyVPN", "Logs", "ui_diagnostics.jsonl");
        LegacySource[] sources = string.Equals(Path.GetFullPath(configuredPath), Path.GetFullPath(path), StringComparison.OrdinalIgnoreCase)
            ? new[] {new LegacySource(cli, "cli"), new LegacySource(cli + ".stderr", "stderr")}
                .Where(source => !string.Equals(Path.GetFullPath(source.Path), Path.GetFullPath(path), StringComparison.OrdinalIgnoreCase)).ToArray()
            : Array.Empty<LegacySource>();
        var marker = path + ".legacy-commit";
        var stage = path + ".legacy-stage";
        var markerData = ReadMigrationMarker(marker);
        if (markerData != null)
        {
            var pending = JsonSerializer.Deserialize<LegacyCommit>(markerData)
                ?? throw new InvalidDataException("Invalid diagnostic migration marker.");
            // An oversized-only Go migration has an empty source list.
            if (pending.Sources is { Length: > 0 } && !pending.Sources.SequenceEqual(sources))
                throw new InvalidDataException("Diagnostic migration sources changed during recovery.");
            FinishMigration(path, stage, marker, pending);
        }
        var found = sources.Any(source => File.Exists(source.Path) || File.Exists(source.Path + ".previous"));
        var oversized = new[] {path + ".previous", path}.Any(name =>
        {
            if (!File.Exists(name)) return false;
            using var input = new FileStream(name, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
            return input.Length > Threshold && HasRecordAfterLimit(input, input.Length);
        });
        if (!found && !oversized) return;
        var migratingSources = found ? sources : Array.Empty<LegacySource>();
        File.Delete(stage);
        File.Delete(stage + ".previous");
        FileStream? output = null;
        var size = 0L;
        var atStart = true;
        try
        {
            output = new FileStream(stage, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
            foreach (var source in new[] {new LegacySource(path, "existing")}.Concat(migratingSources))
            foreach (var suffix in new[] {".previous", ""})
            {
                var inputPath = source.Path + suffix;
                if (!File.Exists(inputPath)) continue;
                if ((File.GetAttributes(inputPath) & (FileAttributes.ReparsePoint | FileAttributes.Directory)) != 0)
                    throw new IOException("Diagnostic migration input is not a regular file: " + inputPath);
                using var input = new FileStream(inputPath, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
                var header = migratingSources.Length == 0 ? Array.Empty<byte>() : Encoding.UTF8.GetBytes(
                    JsonSerializer.Serialize(new Dictionary<string, string> { ["event"] = "diagnostics.legacy", ["source"] = source.Label, ["generation"] = suffix }) + "\n");
                if (!atStart && header.Length > 0) { header = new byte[] {10}.Concat(header).ToArray(); atStart = true; }
                var needHeader = header.Length > 0;
                var buffer = new byte[65536];
                int count;
                while ((count = input.Read(buffer, 0, buffer.Length)) > 0)
                {
                    var offset = 0;
                    while (offset < count)
                    {
                        if (atStart && size >= Threshold)
                        {
                            output.Flush(true); output.Dispose(); output = null;
                            Rotate(stage);
                            output = new FileStream(stage, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
                            size = 0; needHeader = header.Length > 0;
                        }
                        if (needHeader) { output.Write(header, 0, header.Length); size += header.Length; needHeader = false; }
                        var newline = Array.IndexOf(buffer, (byte)10, offset, count - offset);
                        var length = newline < 0 ? count - offset : newline - offset + 1;
                        output.Write(buffer, offset, length); size += length;
                        atStart = newline >= 0; offset += length;
                    }
                }
            }
            output.Flush(true); output.Dispose(); output = null;
            if (File.Exists(stage + ".previous")) PreserveAcl(path, stage + ".previous");
            PreserveAcl(path, stage);
            var commit = new LegacyCommit(found ? migratingSources : null, File.Exists(stage + ".previous"));
            var temp = marker + ".tmp";
            using (var markerFile = new FileStream(temp, FileMode.Create, FileAccess.Write, FileShare.None))
            { JsonSerializer.Serialize(markerFile, commit); markerFile.Flush(true); }
            Publish(temp, marker);
            FinishMigration(path, stage, marker, commit);
        }
        finally { output?.Dispose(); }
    }

    private static byte[]? ReadMigrationMarker(string marker)
    {
        FileAttributes attributes;
        try { attributes = File.GetAttributes(marker); }
        catch (FileNotFoundException) { return null; }
        catch (DirectoryNotFoundException) { return null; }
        if ((attributes & (FileAttributes.ReparsePoint | FileAttributes.Directory | FileAttributes.Device)) != 0)
            throw new IOException("Diagnostic migration marker is not a regular file: " + marker);
        const int limit = 1_048_576;
        using var input = new FileStream(marker, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        using var data = new MemoryStream();
        var buffer = new byte[65536];
        int count;
        while ((count = input.Read(buffer, 0, Math.Min(buffer.Length, limit + 1 - (int)data.Length))) > 0)
        {
            data.Write(buffer, 0, count);
            if (data.Length > limit) throw new InvalidDataException("Diagnostic migration marker exceeds size limit.");
        }
        return data.ToArray();
    }

    private static void FinishMigration(string path, string stage, string marker, LegacyCommit commit)
    {
        if (!commit.Previous) File.Delete(path + ".previous");
        else if (File.Exists(stage + ".previous")) Publish(stage + ".previous", path + ".previous");
        if (File.Exists(stage)) Publish(stage, path);
        // A missing stage is valid only when an earlier attempt already
        // published the corresponding regular file.
        RequireRegularPublishedFile(path);
        if (commit.Previous) RequireRegularPublishedFile(path + ".previous");
        foreach (var source in commit.Sources ?? Array.Empty<LegacySource>())
        {
            File.Delete(source.Path + ".previous");
            File.Delete(source.Path);
        }
        File.Delete(marker);
    }

    private static void RequireRegularPublishedFile(string path)
    {
        var attributes = File.GetAttributes(path);
        if ((attributes & (FileAttributes.ReparsePoint | FileAttributes.Directory)) != 0)
            throw new IOException("Published diagnostic is not a regular file: " + path);
        using var file = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
    }

    private static bool HasRecordAfterLimit(FileStream input, long size)
    {
        input.Position = Threshold - 1;
        var remaining = size - Threshold;
        var buffer = new byte[65536];
        while (remaining > 0)
        {
            var count = input.Read(buffer, 0, (int)Math.Min(buffer.Length, remaining));
            if (count == 0) return false;
            if (Array.IndexOf(buffer, (byte)'\n', 0, count) >= 0) return true;
            remaining -= count;
        }
        return false;
    }
}
