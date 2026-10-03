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

namespace DobbyVPN.Windows;

// UI-thread-owned writer; file reads allow the backend and UI to keep appending.
internal sealed class NativeDiagnostics(string backendPath, string uiPath)
{
    internal static NativeDiagnostics Current { get; } = new(
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "DobbyVPN", "Logs", "backend.jsonl"),
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DobbyVPN", "Logs", "ui_diagnostics.jsonl"));
    private readonly object _gate = new();
    private readonly string[] _paths = [backendPath, backendPath + ".stderr", uiPath];
    private readonly Dictionary<string, string> _lastErrors = new();
    private readonly string _run = Guid.NewGuid().ToString("N");
    private long _sequence;
    public string WriteFailure { get; private set; } = "";

    public void Record(string message, string category)
    {
        lock (_gate)
        {
            if (_lastErrors.GetValueOrDefault(category) == message) return;
            _lastErrors[category] = message;
            if (string.IsNullOrEmpty(message)) return;
            try
            {
                Directory.CreateDirectory(Path.GetDirectoryName(uiPath)!);
                var line = JsonSerializer.Serialize(new
                {
                    schema = "dobby.log/v1", timestamp = DateTimeOffset.UtcNow,
                    source = "windows-ui", level = "ERROR", @event = category, message,
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

    public async Task<string> PreviewAsync()
    {
        var text = new StringBuilder();
        foreach (var path in _paths.SelectMany(path => new[] {path + ".previous", path}))
        {
            try
            {
                await using var input = OpenLog(path);
                var count = (int)Math.Min(131072, input.Length);
                input.Seek(-count, SeekOrigin.End);
                var bytes = new byte[count];
                await input.ReadExactlyAsync(bytes);
                text.AppendLine($"--- {Path.GetFileName(path)} ---").AppendLine(Encoding.UTF8.GetString(bytes));
            }
            catch (FileNotFoundException) { }
            catch (DirectoryNotFoundException) { }
            catch (Exception error) { text.AppendLine($"{path}: {error}"); }
        }
        return text.Append(WriteFailure).ToString();
    }

    public async Task ExportAsync(Stream destination, string header)
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
            await output.WriteAsync(Encoding.UTF8.GetBytes(header));
            var buffer = new byte[65536];
            foreach (var (path, input, length) in inputs)
            {
                await output.WriteAsync(Encoding.UTF8.GetBytes($"\n--- {Path.GetFileName(path)} ---\n"));
                var remaining = length;
                while (remaining > 0)
                {
                    int count;
                    try
                    {
                        count = await input.ReadAsync(buffer.AsMemory(0, (int)Math.Min(buffer.Length, remaining)));
                        if (count == 0) throw new EndOfStreamException("Log shortened during export");
                    }
                    catch (Exception error) { issues.Add($"{path}: {error}"); break; }
                    await output.WriteAsync(buffer.AsMemory(0, count));
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
            if (issues.Count > 0)
                await output.WriteAsync(Encoding.UTF8.GetBytes("\nCollection errors\n" + string.Join("\n", issues) + "\n"));
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

    private static void Migrate(string path)
    {
        var paths = new[] {path + ".previous", path};
        if (!paths.Any(name => File.Exists(name) && new FileInfo(name).Length > Threshold)) return;
        var stage = path + ".migration-" + Guid.NewGuid().ToString("N");
        FileStream? output = null;
        Exception? original = null;
        try
        {
            output = new FileStream(stage, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
            var buffer = new byte[65536];
            var atStart = true;
            foreach (var name in paths)
            {
                if (!File.Exists(name)) continue;
                using var input = new FileStream(name, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
                int count;
                while ((count = input.Read(buffer)) > 0)
                {
                    var offset = 0;
                    while (offset < count)
                    {
                        if (atStart && output.Length >= Threshold)
                        {
                            output.Dispose(); output = null; Rotate(stage);
                            output = new FileStream(stage, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
                        }
                        var newline = Array.IndexOf(buffer, (byte)10, offset, count - offset);
                        var length = newline < 0 ? count - offset : newline - offset + 1;
                        output.Write(buffer, offset, length);
                        atStart = newline >= 0;
                        offset += length;
                    }
                }
            }
            output.Dispose(); output = null;
            File.Delete(path + ".previous");
            if (File.Exists(stage + ".previous")) File.Move(stage + ".previous", path + ".previous");
            File.Move(stage, path, true);
        }
        catch (Exception error) { original = error; throw; }
        finally
        {
            var failures = new List<Exception>();
            foreach (var operation in new Action[] { () => output?.Dispose(), () => File.Delete(stage), () => File.Delete(stage + ".previous") })
                try { operation(); } catch (Exception error) { failures.Add(error); }
            if (failures.Count > 0)
            {
                if (original is not null) failures.Insert(0, original);
                throw new AggregateException(failures);
            }
        }
    }
}
