using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Json;
using System.Threading.Tasks;

namespace DobbyVPN.Windows;

// UI-thread-owned writer; file reads allow the backend and UI to keep appending.
internal sealed class NativeDiagnostics(string backendPath, string uiPath)
{
    private readonly string[] _paths = [backendPath, uiPath];
    private readonly Dictionary<string, string> _lastErrors = new();
    public string WriteFailure { get; private set; } = "";

    public void Record(string message, string category)
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
                source = "windows-ui", level = "ERROR", @event = category, message
            });
            File.AppendAllText(uiPath, line + "\n", new UTF8Encoding(false));
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
        if (_paths.Any(source => string.Equals(Path.GetFullPath(source), path, StringComparison.OrdinalIgnoreCase)))
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
        foreach (var path in _paths)
        {
            try
            {
                await using var input = OpenLog(path);
                var count = (int)Math.Min(262144, input.Length);
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

    public async Task ExportAsync(Stream output, string header)
    {
        await output.WriteAsync(Encoding.UTF8.GetBytes(header));
        var buffer = new byte[65536];
        foreach (var path in _paths)
        {
            FileStream input;
            try { input = OpenLog(path); }
            catch (FileNotFoundException) { continue; }
            catch (DirectoryNotFoundException) { continue; }
            catch (Exception error)
            {
                await output.WriteAsync(Encoding.UTF8.GetBytes($"\n{path}: {error}\n"));
                continue;
            }
            await using (input)
            {
                await output.WriteAsync(Encoding.UTF8.GetBytes($"\n--- {Path.GetFileName(path)} ---\n"));
                var remaining = input.Length;
                while (remaining > 0)
                {
                    int count;
                    try
                    {
                        count = await input.ReadAsync(buffer.AsMemory(0, (int)Math.Min(buffer.Length, remaining)));
                        if (count == 0) throw new EndOfStreamException("Log shortened during export");
                    }
                    catch (Exception error)
                    {
                        await output.WriteAsync(Encoding.UTF8.GetBytes($"\n{path}: {error}\n"));
                        break;
                    }
                    // Destination errors propagate to the Save/Copy handler.
                    await output.WriteAsync(buffer.AsMemory(0, count));
                    remaining -= count;
                }
            }
        }
        if (!string.IsNullOrEmpty(WriteFailure))
            await output.WriteAsync(Encoding.UTF8.GetBytes($"\n{WriteFailure}\n"));
    }
}
