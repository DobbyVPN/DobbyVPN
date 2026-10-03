using System;
using System.IO;
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
            var destination = Path.Combine(directory, "export.txt");
            var block = Enumerable.Range(0, 65536).Select(i => (byte)i).ToArray();
            await using (var source = File.Create(backend))
                for (var index = 0; index < 1024; index++) await source.WriteAsync(block);
            var diagnostics = new NativeDiagnostics(backend, ui);
            for (var index = 0; index < 100; index++)
                diagnostics.Record($"error-{index}:" + new string('x', 10000), "ui.failure");
            // A new owner must read the complete persisted UI history.
            diagnostics = new NativeDiagnostics(backend, ui);
            var preview = await diagnostics.PreviewAsync();
            Require(preview.Length < 530000, "preview is not bounded");
            Require(!preview.Contains("error-0:") && preview.Contains("error-99:"), "preview is not the recent tail");
            await diagnostics.SaveAsync(destination, "test metadata\n");
            await using (var exported = File.OpenRead(destination))
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
            using var partial = new MemoryStream();
            await new NativeDiagnostics(directory, ui).ExportAsync(partial, "read failure\n");
            var details = Encoding.UTF8.GetString(partial.ToArray());
            Require(details.Contains(directory) && details.Contains("error-0:"), "read failure hid available logs");
            Console.WriteLine("Native diagnostics: 64 MiB exact export, bounded preview, persisted history, input protection, and failure cleanup passed");
        }
        finally { Directory.Delete(directory, recursive: true); }
    }

    private static void Require(bool condition, string message)
    {
        if (!condition) throw new InvalidOperationException(message);
    }
}
