using System.Reflection;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.IO.Pipes;
using Windows.ApplicationModel.DataTransfer;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace DobbyVPN.Windows;

public sealed partial class MainWindow : Window
{
    private const string PipeName = "DobbyVPN.Control";
    private readonly string _logPath = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
        "DobbyVPN", "Logs", "backend.jsonl");
    private readonly PeriodicTimer _pollTimer = new(TimeSpan.FromMilliseconds(750));
    private readonly CancellationTokenSource _shutdown = new();
    private Snapshot? _snapshot;
    private bool _sourceDirty;
    private bool _updatingSource;
    private bool _busy;
    private bool _snapshotInFlight;
    private string? _acceptedInThisWindow;
    private string? _renderedSource;
    private bool _sourceInitialized;

    public MainWindow()
    {
        InitializeComponent();
        SetConnectionAction("Connect");
        var assembly = Assembly.GetExecutingAssembly();
        VersionText.Text = $"Version: {assembly.GetName().Version?.ToString(3) ?? "Unknown"}";
        var commit = assembly.GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(item => item.Key == "DobbySourceCommit")?.Value;
        CommitText.Text = $"Source commit: {commit ?? "N/A"}";
        SourceEditor.TextChanged += (_, _) =>
        {
            if (!_updatingSource)
            {
                _sourceDirty = true;
                ErrorText.Text = string.Empty;
            }
        };
        Closed += (_, _) =>
        {
            _shutdown.Cancel();
            _pollTimer.Dispose();
            _shutdown.Dispose();
        };
        _ = PollSnapshotsAsync(_shutdown.Token);
        _ = RefreshSnapshotAsync();
    }

    private async Task PollSnapshotsAsync(CancellationToken cancellationToken)
    {
        try
        {
            while (await _pollTimer.WaitForNextTickAsync(cancellationToken))
                await RefreshSnapshotAsync();
        }
        catch (OperationCanceledException) { }
    }

    private async Task RefreshSnapshotAsync()
    {
        if (_snapshotInFlight) return;
        _snapshotInFlight = true;
        try
        {
            var result = await ReadSnapshotAsync();
            var recoveringFromSnapshotError = _snapshot is null || StatusText.Text == "Error";
            _snapshot = result;
            StatusText.Text = result.Recovering ? "Reconnecting" : result.State switch
            {
                "CONNECTED" => "Connected",
                "PROBING" or "PREPARING" or "STOPPING" => "Connecting",
                "FAILED" => "Failed",
                _ => "Disconnected"
            };
            AutomationProperties.SetAutomationId(StatusText, StatusText.Text);
            SetConnectionAction(result.State == "CONNECTED" ? "Disconnect" : "Connect");
            ConnectionButton.IsEnabled = !_busy;
            SourceEditor.IsEnabled = !_busy;
            ProfileText.Text = result.ActiveProfile is null
                ? ""
                : string.Join(" · ", new[] { result.ActiveProfile.Protocol, result.ActiveProfile.Description }.Where(value => !string.IsNullOrWhiteSpace(value)));
            WarningsText.Text = string.Join(Environment.NewLine, result.Warnings.Select(warning => warning.Message));
            FailureText.Text = result.LastFailure is null
                ? ""
                : $"{result.LastFailure.Message} ({result.LastFailure.Code})";
            if (!_sourceDirty && SourceEditor.FocusState == FocusState.Unfocused &&
                (!_sourceInitialized || string.Equals(NormalizeSource(SourceEditor.Text), _renderedSource, StringComparison.Ordinal)))
            {
                string sourceToDisplay;
                if (!string.IsNullOrEmpty(result.SourceUrl))
                {
                    sourceToDisplay = result.SourceUrl;
                    _acceptedInThisWindow = result.SourceUrl;
                }
                else
                {
                    sourceToDisplay = _acceptedInThisWindow ?? "";
                }
                SetSourceText(sourceToDisplay);
                _renderedSource = sourceToDisplay;
                _sourceInitialized = true;
            }
            if (!string.IsNullOrEmpty(result.SourceError)) ErrorText.Text = result.SourceError;
            else if (recoveringFromSnapshotError) ErrorText.Text = string.Empty;
        }
        catch (Exception error)
        {
            _snapshot = null;
            ErrorText.Text = error.Message;
            StatusText.Text = "Error";
            AutomationProperties.SetAutomationId(StatusText, StatusText.Text);
            SetConnectionAction("Connect");
            ConnectionButton.IsEnabled = false;
        }
        finally
        {
            _snapshotInFlight = false;
        }
    }

    private async Task<Snapshot> ReadSnapshotAsync()
    {
        var sessionId = _snapshot?.SessionId ?? "";
        try
        {
            return await CallAsync<Snapshot>("Snapshot", new { session_id = sessionId });
        }
        catch (BackendCommandException error) when (error.Code == "NOT_FOUND" && !string.IsNullOrEmpty(sessionId))
        {
            // The Go backend owns session state. A replacement backend has a
            // new session ID, so reattach through its owner-independent
            // Snapshot entry point and let it restore the saved configuration URL.
            return await CallAsync<Snapshot>("Snapshot", new { session_id = "" });
        }
    }

    private async void ConnectionButton_Click(object sender, RoutedEventArgs e)
    {
        if (_busy || _snapshot is null) return;
        _busy = true;
        ConnectionButton.IsEnabled = false;
        SourceEditor.IsEnabled = false;
        ErrorText.Text = "";
        try
        {
            var current = _snapshot;
            if (current.State == "CONNECTED")
            {
                await CallAsync<JsonElement>("Stop", new { session_id = current.SessionId, generation = current.Generation });
            }
            else
            {
                var source = NormalizeSource(SourceEditor.Text).Trim();
                var needsConfigure = !current.Configured || _sourceDirty;
                if (needsConfigure && string.IsNullOrWhiteSpace(source))
                    throw new InvalidOperationException("Enter an HTTPS connection URL or inline configuration.");
                var sequence = current.Sequence;
                if (needsConfigure)
                {
                    var configured = await CallAsync<CommandSequence>("Configure", new
                    {
                        session_id = current.SessionId,
                        expected_sequence = sequence,
                        source
                    });
                    sequence = configured.Sequence;
                    SetSourceText(source);
                    _acceptedInThisWindow = source;
                    _sourceDirty = false;
                    _renderedSource = source;
                }
                await CallAsync<JsonElement>("Start", new
                {
                    session_id = current.SessionId,
                    expected_sequence = sequence,
                    mode = "AUTO_SELECT",
                    index = 0
                });
            }
            await RefreshSnapshotAsync();
        }
        catch (Exception error)
        {
            ErrorText.Text = error.Message;
        }
        finally
        {
            _busy = false;
            ConnectionButton.IsEnabled = true;
            SourceEditor.IsEnabled = true;
            await RefreshSnapshotAsync();
        }
    }

    private async Task<T> CallAsync<T>(string method, object parameters)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(_shutdown.Token);
        timeout.CancelAfter(TimeSpan.FromMinutes(2));
        await using var pipe = new NamedPipeClientStream(".", PipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
        await pipe.ConnectAsync(3000, timeout.Token);
        await using var writer = new StreamWriter(pipe, new UTF8Encoding(false), 1024, leaveOpen: true) { AutoFlush = true };
        using var reader = new StreamReader(pipe, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, bufferSize: 1024, leaveOpen: true);
        var request = JsonSerializer.Serialize(new { method, @params = parameters });
        await writer.WriteLineAsync(request.AsMemory(), timeout.Token);
        var line = await reader.ReadLineAsync(timeout.Token) ?? throw new IOException("Go backend closed the control connection without a response.");
        using var response = JsonDocument.Parse(line);
        if (!response.RootElement.GetProperty("ok").GetBoolean())
        {
            var failure = response.RootElement.GetProperty("error");
            var code = failure.GetProperty("code").GetString() ?? "INTERNAL";
            var message = failure.GetProperty("message").GetString() ?? "Go backend command failed";
            throw new BackendCommandException(code, message);
        }
        var payload = response.RootElement.GetProperty("result");
        var result = payload.Deserialize<T>(JsonOptions);
        if (result is null) throw new InvalidDataException("Go backend returned an empty result.");
        return result;
    }

    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web);

    private void SetSourceText(string value)
    {
        _updatingSource = true;
        try { SourceEditor.Text = value; }
        finally { _updatingSource = false; }
    }

    private void SetConnectionAction(string value)
    {
        ConnectionButton.Content = value;
        AutomationProperties.SetName(ConnectionButton, value);
    }

    private static string NormalizeSource(string value) =>
        value.Replace("\r\n", "\n").Replace("\r", "\n");

    private async void Pages_SelectionChanged(object sender, SelectionChangedEventArgs args)
    {
        if (Pages.SelectedIndex == 1) await RefreshLogsAsync();
    }

    private async void RefreshLogs_Click(object sender, RoutedEventArgs e) => await RefreshLogsAsync();

    private async Task RefreshLogsAsync()
    {
        try
        {
            LogsText.Text = File.Exists(_logPath) ? await File.ReadAllTextAsync(_logPath) : "No logs are available.";
            LogsErrorText.Text = "";
        }
        catch (Exception error)
        {
            LogsErrorText.Text = $"{_logPath}: {error.Message}";
        }
    }

    private void CopyLogs_Click(object sender, RoutedEventArgs e)
    {
        var data = new DataPackage();
        data.SetText(LogsText.Text);
        Clipboard.SetContent(data);
    }

    private sealed class Snapshot
    {
        [JsonPropertyName("session_id")] public string SessionId { get; init; } = "";
        [JsonPropertyName("sequence")] public long Sequence { get; init; }
        [JsonPropertyName("generation")] public long Generation { get; init; }
        [JsonPropertyName("state")] public string State { get; init; } = "IDLE";
        [JsonPropertyName("configured")] public bool Configured { get; init; }
        [JsonPropertyName("source_url")] public string SourceUrl { get; init; } = "";
        [JsonPropertyName("source_error")] public string SourceError { get; init; } = "";
        [JsonPropertyName("active_profile")] public Profile? ActiveProfile { get; init; }
        [JsonPropertyName("warnings")] public Warning[] Warnings { get; init; } = [];
        [JsonPropertyName("last_failure")] public Failure? LastFailure { get; init; }
        [JsonPropertyName("recovering")] public bool Recovering { get; init; }
    }

    private sealed class Profile
    {
        [JsonPropertyName("protocol")] public string Protocol { get; init; } = "";
        [JsonPropertyName("description")] public string Description { get; init; } = "";
    }

    private sealed class Warning
    {
        [JsonPropertyName("message")] public string Message { get; init; } = "";
    }

    private sealed class Failure
    {
        [JsonPropertyName("code")] public string Code { get; init; } = "";
        [JsonPropertyName("message")] public string Message { get; init; } = "";
    }

    private sealed class CommandSequence
    {
        [JsonPropertyName("sequence")] public long Sequence { get; init; }
    }

    private sealed class BackendCommandException : InvalidOperationException
    {
        public BackendCommandException(string code, string message)
            : base($"{message} ({code})")
        {
            Code = code;
        }

        public string Code { get; }
    }
}
