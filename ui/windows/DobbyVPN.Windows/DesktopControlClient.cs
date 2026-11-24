using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Text.Json;

namespace DobbyVPN.Windows;

internal sealed class DesktopControlClient(NativeDiagnostics diagnostics)
{
    private const string PipeName = "DobbyVPN.Control";
    private readonly DesktopControlDiagnostics _requests = new();

    internal async Task<T> CallAsync<T>(string method, object parameters, CancellationToken cancellationToken)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeout.CancelAfter(TimeSpan.FromMinutes(2));
        var operation = _requests.Begin(method, parameters);
        var started = Stopwatch.GetTimestamp();
        object fields(string? error = null) => new
        {
            request_id = operation.Id, method, identity = operation.Identity,
            elapsed_ms = Stopwatch.GetElapsedTime(started).TotalMilliseconds, error,
        };
        if (operation.Sampled) diagnostics.RecordInfo("control.command.begin", "Started desktop control command", fields());
        try
        {
            await using var pipe = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
            if (method == "Configure") diagnostics.RecordInfo("subscription.configure-pipe-connect-start", "Started connecting the Configure named pipe", new { method, parameters });
            await pipe.ConnectAsync(3000, timeout.Token);
            if (method == "Configure") diagnostics.RecordInfo("subscription.configure-pipe-connect-end", "Connected the Configure named pipe", new { method, parameters });
            await using var writer = new StreamWriter(pipe, new UTF8Encoding(false), 1024, leaveOpen: true) { AutoFlush = true };
            using var reader = new StreamReader(pipe, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, bufferSize: 1024, leaveOpen: true);
            var request = operation.Encode();
            await writer.WriteLineAsync(request.AsMemory(), timeout.Token);
            if (method == "Configure") diagnostics.RecordInfo("subscription.configure-pipe-request-written", "Wrote the Configure request to the Go backend", new { method, parameters });
            var line = await reader.ReadLineAsync(timeout.Token);
            if (method == "Configure") diagnostics.RecordInfo("subscription.configure-pipe-response-read", "Read the Configure response from the Go backend", new { method, parameters, succeeded = line is not null });
            if (line is null) throw new IOException("Go backend closed the control connection without a response.");
            using var response = JsonDocument.Parse(line);
            if (!response.RootElement.GetProperty("ok").GetBoolean())
            {
                var failure = response.RootElement.GetProperty("error");
                var code = failure.GetProperty("code").GetString() ?? "INTERNAL";
                var message = failure.GetProperty("message").GetString() ?? "Go backend command failed";
                throw new BackendCommandException(code, message);
            }
            var payload = response.RootElement.GetProperty("result");
            var result = typeof(T) == typeof(Snapshot) ? (T)(object)Snapshot.Decode(payload) : payload.Deserialize<T>(JsonOptions);
            if (result is null) throw new InvalidDataException("Go backend returned an empty result.");
            if (operation.Sampled) diagnostics.RecordInfo("control.command.complete", "Completed desktop control command", fields());
            return result;
        }
        catch (Exception error)
        {
            diagnostics.RecordError("control.command.failure", "Desktop control command failed", fields(error.ToString()));
            throw;
        }
    }

    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);
}

internal sealed class BackendCommandException(string code, string message) : InvalidOperationException($"{message} ({code})")
{
    internal string Code { get; } = code;
}
