using System;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using DobbyVPN.Windows;

internal static class NativeDiagnosticsTests
{
    public static async Task RunAsync()
    {
        var directory = Path.Combine(Path.GetTempPath(), "dobby-diagnostics-" + Guid.NewGuid());
        Directory.CreateDirectory(directory);
        try
        {
            var backend = Path.Combine(directory, "backend.jsonl");
            var ui = Path.Combine(directory, "ui.jsonl");
            var destination = Path.Combine(directory, "export.gz");
            var block = Enumerable.Range(0, 65536).Select(i => (byte)i).ToArray();
            await using (var source = File.Create(backend))
                for (var index = 0; index < 1024; index++) await source.WriteAsync(block);
            var diagnostics = new NativeDiagnostics(backend, ui);
            for (var index = 0; index < 100; index++)
                diagnostics.Record($"error-{index}:" + new string('x', 10000), "ui.failure");
            // A new owner must read the complete persisted UI history.
            diagnostics = new NativeDiagnostics(backend, ui);
            var previewEntries = await diagnostics.EntriesAsync();
            Require(previewEntries.Error == "", previewEntries.Error);
            var preview = string.Join("\n", previewEntries.Entries.Select(entry => entry.Message));
            Require(preview.Length < 530000, "preview is not bounded");
            Require(!preview.Contains("error-0:") && preview.Contains("error-99:"), "preview is not the recent tail");
            await diagnostics.SaveAsync(destination, "test metadata\n");
            await using (var compressed = File.OpenRead(destination))
            await using (var exported = new GZipStream(compressed, CompressionMode.Decompress))
            {
                var prefix = Encoding.UTF8.GetBytes("test metadata\n\n--- backend.jsonl ---\n");
                var actualPrefix = new byte[prefix.Length];
                await exported.ReadExactlyAsync(actualPrefix);
                Require(prefix.SequenceEqual(actualPrefix), "metadata missing");
                var actual = new byte[block.Length];
                for (var index = 0; index < 1024; index++)
                {
                    await exported.ReadExactlyAsync(actual);
                    Require(block.SequenceEqual(actual), "export changed diagnostic bytes");
                }
                using var reader = new StreamReader(exported);
                var history = await reader.ReadToEndAsync();
                Require(history.Contains("error-0:") && history.Contains("error-99:"), "persisted errors missing");
            }
            try
            {
                await diagnostics.SaveAsync(backend, "overwrite");
                throw new InvalidOperationException("export overwrote diagnostic input");
            }
            catch (IOException) { Require(new FileInfo(backend).Length == 67108864, "source changed"); }
            // A failed replacement must preserve the old destination and remove the temporary file.
            await using (var locked = new FileStream(destination, FileMode.Open, FileAccess.Read, FileShare.None))
            {
                try
                {
                    await diagnostics.SaveAsync(destination, "replacement");
                    throw new InvalidOperationException("locked destination was overwritten");
                }
                catch (Exception failure) when (failure is IOException or UnauthorizedAccessException) { }
            }
            Require(!Directory.EnumerateFiles(directory, ".dobby-export-*.tmp").Any(), "partial export left behind");
            var native = Path.Combine(directory, "rotation.jsonl");
            using (var legacy = File.Create(native))
            {
                legacy.SetLength(NativeLogFiles.Threshold - 1);
                legacy.Position = NativeLogFiles.Threshold - 1;
                legacy.Write(Encoding.UTF8.GetBytes("\nlegacy-tail\n"));
            }
            NativeLogFiles.Append(native, Encoding.UTF8.GetBytes("first\n"));
            Require(new FileInfo(native + ".previous").Length == NativeLogFiles.Threshold, "migration split a record");
            Require(File.ReadAllText(native) == "legacy-tail\nfirst\n", "migration lost the retained tail");
            await using (var held = new FileStream(native, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
            {
                foreach (var record in new[] {"second\n", "third\n"})
                {
                    using (var growing = new FileStream(native, FileMode.Open, FileAccess.Write, FileShare.ReadWrite | FileShare.Delete))
                        growing.SetLength(NativeLogFiles.Threshold);
                    NativeLogFiles.Append(native, Encoding.UTF8.GetBytes(record));
                }
                var retained = new byte[18];
                await held.ReadExactlyAsync(retained);
                Require(Encoding.UTF8.GetString(retained) == "legacy-tail\nfirst\n", "rotation lost captured bytes");
            }
            File.WriteAllText(native + ".previous", "retained prior\n");
            await new NativeDiagnostics(native, ui).SaveAsync(destination, "both generations\n");
            using (var compressed = File.OpenRead(destination))
            using (var gzip = new GZipStream(compressed, CompressionMode.Decompress))
            using (var reader = new StreamReader(gzip))
            {
                var both = await reader.ReadToEndAsync();
                Require(both.Contains("retained prior") && both.Contains("third"), "export lost a generation");
            }
            using var partial = new MemoryStream();
            await new NativeDiagnostics(directory, ui).ExportAsync(partial, "read failure\n");
            partial.Position = 0;
            using var uncompressed = new GZipStream(partial, CompressionMode.Decompress);
            using var text = new StreamReader(uncompressed);
            var details = await text.ReadToEndAsync();
            Require(details.Contains(directory) && details.Contains("error-0:"), "read failure hid available logs");
            var structured = Path.Combine(directory, "structured.jsonl");
            var structuredUI = Path.Combine(directory, "structured-ui.jsonl");
            const string earlier = "{\"timestamp\":\"2026-01-01T00:00:01Z\",\"level\":\"WARN\",\"message\":\"earlier λ\",\"extra\":42}\n";
            const string later = "{\"timestamp\":\"2026-01-01T00:00:02Z\",\"message\":\"later\"}\n";
            File.WriteAllText(structured, later + "trace line 1\ntrace line 2\n");
            File.WriteAllText(structuredUI, earlier + "{\"message\":\"incomplete");
            var view = new NativeDiagnostics(structured, structuredUI);
            var parsed = await view.EntriesAsync();
            Require(parsed.Error == "", parsed.Error);
            Require(parsed.Entries.Select(e => e.Message).SequenceEqual(new[] { "earlier λ", "later", "trace line 1", "trace line 2" }), "structured ordering or partial record failed");
            Require(parsed.Entries[0].Level == "WARN" && parsed.Entries[0].Raw.Contains("extra"), "structured details lost");
            await view.ClearViewAsync();
            File.Move(structured, structured + ".previous");
            File.WriteAllText(structured, "new after rotation\n");
            File.AppendAllText(structuredUI, " record\"}\n");
            view = new NativeDiagnostics(structured, structuredUI);
            parsed = await view.EntriesAsync();
            Require(parsed.Error == "" && parsed.Entries.Select(e => e.Message).SequenceEqual(new[] { "new after rotation" }), "Clear did not survive rotation/restart or partial boundary");
            Require(File.ReadAllText(structured + ".previous").StartsWith(later), "Clear modified retained bytes");
            Console.WriteLine("Native diagnostics: 64 MiB exact export, bounded preview, persisted history, input protection, and failure cleanup passed");
        }
        finally { Directory.Delete(directory, recursive: true); }
    }

    private static void Require(bool condition, string message)
    {
        if (!condition) throw new InvalidOperationException(message);
    }
}
