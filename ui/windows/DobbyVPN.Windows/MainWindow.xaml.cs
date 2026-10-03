using System.Reflection;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.IO.Pipes;
using Windows.ApplicationModel.DataTransfer;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.Windows.Storage.Pickers;

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
    private bool _configurationText;
    private bool _followingLogs = true;
    private bool _updatingLogs;
    private ScrollViewer? _logScroll;
    private readonly string _version;
    private readonly string _commit;
    private readonly List<string> _diagnosticErrors = new();

    public MainWindow()
    {
        InitializeComponent();
        SetConnectionAction("Connect");
        ConnectionButton.IsEnabled = false;
        var assembly = Assembly.GetExecutingAssembly();
        _version = assembly.GetName().Version?.ToString(3) ?? "Unknown";
        var commit = assembly.GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(item => item.Key == "DobbySourceCommit")?.Value;
        _commit = commit ?? "N/A";
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
            {
                await RefreshSnapshotAsync();
                await RefreshLogsAsync();
            }
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
                "PROBING" or "PREPARING" => "Connecting",
                "STOPPING" => "Stopping",
                "FAILED" => "Failed",
                _ => "Disconnected"
            };
            AutomationProperties.SetAutomationId(StatusText, StatusText.Text);
            SetConnectionAction(result.PrimaryAction switch
            {
                "START" => "Connect",
                "STOP" => result.State == "CONNECTED" ? "Disconnect" : "Cancel",
                _ => result.State == "STOPPING" ? "Stopping…" : "Waiting…"
            });
            ConnectionButton.IsEnabled = !_busy && (result.PrimaryAction is "START" or "STOP");
            SourceEditor.IsEnabled = !_busy;
            var progressing = _busy || result.State is "PROBING" or "PREPARING" or "STOPPING";
            ConnectionProgress.IsActive = progressing;
            ConnectionProgress.Visibility = progressing ? Visibility.Visible : Visibility.Collapsed;
            ProfileText.Text = result.ActiveProfile is null
                ? ""
                : string.Join(" · ", new[] { result.ActiveProfile.Protocol, result.ActiveProfile.Description }.Where(value => !string.IsNullOrWhiteSpace(value)));
            FailureText.Text = result.LastFailure is null
                ? ""
                : "Connection failed. See logs for details.";
            if (result.LastFailure is not null) RecordError($"{result.LastFailure.Message} ({result.LastFailure.Code})");
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
            if (!string.IsNullOrEmpty(result.SourceError)) ShowError(result.SourceError, "Check the subscription URL or configuration. See logs for details.");
            else if (recoveringFromSnapshotError) ErrorText.Text = string.Empty;
        }
        catch (Exception error)
        {
            _snapshot = null;
            ShowError(error.ToString(), _snapshot is null ? "VPN service is unavailable. See logs for details." : "Could not complete the request. Check your configuration and logs.");
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
        if (_busy || _snapshot is null || _snapshot.PrimaryAction is not ("START" or "STOP")) return;
        _busy = true;
        ConnectionButton.IsEnabled = false;
        SourceEditor.IsEnabled = false;
        ErrorText.Text = "";
        try
        {
            var current = _snapshot;
            if (current.PrimaryAction == "STOP")
            {
                await CallAsync<JsonElement>("Stop", new { session_id = current.SessionId, generation = current.Generation });
            }
            else
            {
                var source = NormalizeSource(SourceEditor.Text).Trim();
                var includeSource = !current.Configured || _sourceDirty;
                if (includeSource && string.IsNullOrWhiteSpace(source))
                    throw new InvalidOperationException("Enter an HTTPS connection URL or inline configuration.");
                var parameters = new Dictionary<string, object>
                {
                    ["session_id"] = current.SessionId,
                    ["expected_sequence"] = current.Sequence,
                    ["mode"] = "AUTO_SELECT",
                    ["index"] = 0
                };
                if (includeSource) parameters["source"] = source;
                await CallAsync<JsonElement>("Start", parameters);
                if (includeSource) MarkSourceAccepted(source);
            }
        }
        catch (Exception error)
        {
            ShowError(error.ToString(), _snapshot is null ? "VPN service is unavailable. See logs for details." : "Could not complete the request. Check your configuration and logs.");
        }
        finally
        {
            _busy = false;
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
        try
        {
            if (value.Contains('\n') || value.TrimStart().StartsWith('[')) SetSourceMode(true);
            SourceEditor.Text = value;
        }
        finally { _updatingSource = false; }
    }

    private void MarkSourceAccepted(string source)
    {
        SetSourceText(source);
        _acceptedInThisWindow = source;
        _sourceDirty = false;
        _renderedSource = source;
    }

    private void SetConnectionAction(string value)
    {
        ConnectionButton.Content = value;
        AutomationProperties.SetName(ConnectionButton, value);
    }

    private static string NormalizeSource(string value) =>
        value.Replace("\r\n", "\n").Replace("\r", "\n");

    private void SetSourceMode(bool configurationText)
    {
        _configurationText = configurationText;
        SourceEditor.Header = configurationText ? "Configuration text" : "Subscription URL";
        SourceEditor.AcceptsReturn = configurationText;
        SourceEditor.TextWrapping = configurationText ? TextWrapping.Wrap : TextWrapping.NoWrap;
        SourceEditor.Height = configurationText ? 140 : double.NaN;
        SourceModeButton.Content = configurationText ? "Use subscription URL" : "Use configuration text…";
        AutomationProperties.SetName(SourceEditor, configurationText ? "Configuration text" : "Subscription URL");
    }

    private void SourceMode_Click(object sender, RoutedEventArgs e) => SetSourceMode(!_configurationText);

    private async void About_Click(object sender, RoutedEventArgs e)
    {
        var details = new StackPanel { Spacing = 12 };
        details.Children.Add(new TextBlock { Text = $"Version: {_version}", IsTextSelectionEnabled = true });
        details.Children.Add(new TextBlock { Text = $"Source commit: {_commit}", TextWrapping = TextWrapping.Wrap, IsTextSelectionEnabled = true });
        if (_commit.Length == 40)
            details.Children.Add(new HyperlinkButton { Content = "Source code", NavigateUri = new Uri($"https://github.com/DobbyVPN/DobbyVPN/tree/{_commit}") });
        await new ContentDialog { Title = "About DobbyVPN", Content = details, CloseButtonText = "Done", XamlRoot = Root.XamlRoot }.ShowAsync();
    }

    private void RecordError(string message)
    {
        if (_diagnosticErrors.LastOrDefault() != message) _diagnosticErrors.Add(message);
    }

    private void ShowError(string details, string message)
    {
        RecordError(details);
        ErrorText.Text = message;
    }

    private static T? FindChild<T>(DependencyObject parent) where T : DependencyObject
    {
        for (var index = 0; index < VisualTreeHelper.GetChildrenCount(parent); index++)
        {
            var child = VisualTreeHelper.GetChild(parent, index);
            if (child is T match) return match;
            if (FindChild<T>(child) is T nested) return nested;
        }
        return null;
    }

    private void LogsText_Loaded(object sender, RoutedEventArgs e)
    {
        _logScroll = FindChild<ScrollViewer>(LogsText);
        if (_logScroll is not null)
            _logScroll.ViewChanged += (_, _) =>
            {
                if (!_updatingLogs) _followingLogs = _logScroll.VerticalOffset >= _logScroll.ScrollableHeight - 4;
            };
        _ = RefreshLogsAsync();
    }

    private void JumpToLatest_Click(object sender, RoutedEventArgs e)
    {
        _followingLogs = true;
        _logScroll?.ChangeView(null, _logScroll.ScrollableHeight, null, true);
    }

    private async Task<string> ReadDiagnosticsAsync()
    {
        // Allow the backend to append while a complete, fresh snapshot is read.
        var text = "";
        try
        {
            using var stream = new FileStream(_logPath, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete, 4096, true);
            using var reader = new StreamReader(stream);
            text = await reader.ReadToEndAsync();
        }
        catch (FileNotFoundException) { }
        catch (DirectoryNotFoundException) { }
        return text + (_diagnosticErrors.Count == 0 ? "" : "\nUI diagnostics\n" + string.Join("\n", _diagnosticErrors));
    }

    private async Task RefreshLogsAsync()
    {
        try
        {
            var text = await ReadDiagnosticsAsync();
            if (text != LogsText.Text)
            {
                var offset = _logScroll?.VerticalOffset ?? 0;
                var selection = LogsText.SelectionStart;
                var length = LogsText.SelectionLength;
                _updatingLogs = true;
                LogsText.Text = text;
                LogsText.Select(Math.Min(selection, text.Length), Math.Min(length, Math.Max(0, text.Length - selection)));
                LogsText.UpdateLayout();
                _logScroll?.ChangeView(null, _followingLogs ? _logScroll.ScrollableHeight : offset, null, true);
                _updatingLogs = false;
            }
            LogsErrorText.Text = "";
        }
        catch (Exception error)
        {
            RecordError(error.ToString());
            LogsErrorText.Text = "Diagnostic files could not be read. " + error.Message;
        }
    }

    private async Task<string> ExportDiagnosticsAsync() =>
        $"DobbyVPN {_version}\nSource commit: {_commit}\nPlatform: {Environment.OSVersion}\nCaptured: {DateTimeOffset.UtcNow:O}\n\n" + await ReadDiagnosticsAsync();

    private async void CopyLogs_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            var data = new DataPackage();
            data.SetText(await ExportDiagnosticsAsync());
            Clipboard.SetContent(data);
        }
        catch (Exception error) { RecordError(error.ToString()); LogsErrorText.Text = error.Message; }
    }

    private async void SaveLogs_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            var picker = new FileSavePicker(AppWindow.Id) { SuggestedFileName = "DobbyVPN-logs" };
            picker.FileTypeChoices.Add("Text file", new List<string> { ".txt" });
            var file = await picker.PickSaveFileAsync();
            if (file is not null) await File.WriteAllTextAsync(file.Path, await ExportDiagnosticsAsync());
        }
        catch (Exception error) { RecordError(error.ToString()); LogsErrorText.Text = error.Message; }
    }

    private sealed class Snapshot
    {
        [JsonPropertyName("session_id")] public string SessionId { get; init; } = "";
        [JsonPropertyName("sequence")] public long Sequence { get; init; }
        [JsonPropertyName("generation")] public long Generation { get; init; }
        [JsonPropertyName("state")] public string State { get; init; } = "IDLE";
        [JsonPropertyName("primary_action")] public string PrimaryAction { get; init; } = "NONE";
        [JsonPropertyName("configured")] public bool Configured { get; init; }
        [JsonPropertyName("source_url")] public string SourceUrl { get; init; } = "";
        [JsonPropertyName("source_error")] public string SourceError { get; init; } = "";
        [JsonPropertyName("active_profile")] public Profile? ActiveProfile { get; init; }
        [JsonPropertyName("last_failure")] public Failure? LastFailure { get; init; }
        [JsonPropertyName("recovering")] public bool Recovering { get; init; }
    }

    private sealed class Profile
    {
        [JsonPropertyName("protocol")] public string Protocol { get; init; } = "";
        [JsonPropertyName("description")] public string Description { get; init; } = "";
    }

    private sealed class Failure
    {
        [JsonPropertyName("code")] public string Code { get; init; } = "";
        [JsonPropertyName("message")] public string Message { get; init; } = "";
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
