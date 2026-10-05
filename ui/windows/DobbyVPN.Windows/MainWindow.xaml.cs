using System.Reflection;
using Windows.ApplicationModel.DataTransfer;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.IO.Pipes;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Media;
using Microsoft.Windows.Storage.Pickers;

namespace DobbyVPN.Windows;

public sealed partial class MainWindow : Window
{
    private const string PipeName = "DobbyVPN.Control";
    private readonly PeriodicTimer _pollTimer = new(TimeSpan.FromMilliseconds(750));
    private readonly CancellationTokenSource _shutdown = new();
    private Snapshot? _snapshot;
    private bool _sourceDirty;
    private bool _updatingSource;
    // TextBox may report a programmatic Text assignment after SetSourceText returns.
    private string? _programmaticSourceText;
    private bool _busy;
    private bool _stopInFlight;
    private bool _snapshotInFlight;
    private string? _acceptedInThisWindow;
    private string? _renderedSource;
    private bool _sourceInitialized;
    private long _loadRevision;
    private long _acceptedSequence;
    private bool _loading;
    private string _actionsKey = "";
    private string? _pendingLoad;
    private string _loadError = "";
    private string _restoredLoad = "";
    private CancellationTokenSource? _debounce;
    private long _logRevision;
    private bool _clearingLogs;
    private bool _followingLogs = true;
    private bool _updatingLogs;
    private ScrollViewer? _logScroll;
    private bool _userScrolling;
    private List<NativeDiagnostics.Entry> _latestLogs = [];
    private readonly HashSet<string> _expandedLogs = [];
    private string _renderedLogs = "";
    private readonly string _version;
    private readonly string _commit;
    private readonly NativeDiagnostics _diagnostics = NativeDiagnostics.Current;
    private bool _exportingLogs;

    public MainWindow()
    {
        InitializeComponent();
        Root.SizeChanged += (_, args) =>
        {
            ControlsScroll.MaxHeight = args.NewSize.Height * 0.6;
            ProfilesScroll.MaxHeight = Math.Min(180, args.NewSize.Height * 0.2);
        };
        foreach (var details in new[] { LoadStatus, ProfileText, FailureText, ErrorText, LogsErrorText })
        {
            details.Visibility = Visibility.Collapsed;
            details.RegisterPropertyChangedCallback(TextBlock.TextProperty, (sender, _) =>
            {
                var text = (TextBlock)sender;
                text.Visibility = string.IsNullOrEmpty(text.Text) ? Visibility.Collapsed : Visibility.Visible;
            });
        }
        SetConnectionAction("Connect");
        ConnectionButton.IsEnabled = false;
        var assembly = Assembly.GetExecutingAssembly();
        _version = assembly.GetName().Version?.ToString(3) ?? "Unknown";
        var commit = assembly.GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(item => item.Key == "DobbySourceCommit")?.Value;
        _commit = commit ?? "N/A";
        SourceEditor.TextChanged += (_, _) =>
        {
            if (_updatingSource || SourceEditor.Text == _programmaticSourceText) return;
            _programmaticSourceText = null;
            SourceChanged();
        };
        Closed += (_, _) =>
        {
            _shutdown.Cancel();
            _pollTimer.Dispose();
            _shutdown.Dispose();
        };
        Clipboard.ContentChanged += ClipboardChanged;
        Activated += (_, _) => RefreshClipboard();
        Closed += (_, _) => Clipboard.ContentChanged -= ClipboardChanged;
        RefreshClipboard();
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
            if (result.SessionId == _snapshot?.SessionId && result.Sequence < Math.Max(_snapshot.Sequence, _acceptedSequence)) return;
            var recoveringFromSnapshotError = _snapshot is null || StatusText.Text == "Error";
            if (result.SessionId != _snapshot?.SessionId) _acceptedSequence = 0;
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
            RenderActions();
            var progressing = _busy || _stopInFlight || result.State is "PROBING" or "PREPARING" or "STOPPING";
            ConnectionProgress.IsActive = progressing;
            ConnectionProgress.Visibility = progressing ? Visibility.Visible : Visibility.Collapsed;
            ProfileText.Text = result.ActiveProfile is null
                ? ""
                : string.Join(" · ", new[] { result.ActiveProfile.Protocol, result.ActiveProfile.Description }.Where(value => !string.IsNullOrWhiteSpace(value)));
            FailureText.Text = result.LastFailure is null
                ? ""
                : "Connection failed. See logs for details.";
            _diagnostics.Record(result.LastFailure is null ? "" : $"{result.LastFailure.Message} ({result.LastFailure.Code})", "backend.failure");
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
            var restoreKey = result.SessionId + "|" + SourceEditor.Text;
            if (!result.Configured && !_sourceDirty && SourceEditor.Text.Length > 0 && _restoredLoad != restoreKey)
            {
                _restoredLoad = restoreKey;
                SourceChanged(immediate: true);
            }
            _ = LoadNextAsync();
            if (!string.IsNullOrEmpty(result.SourceError)) ShowError(result.SourceError, "Check the subscription URL or configuration. See logs for details.", "source.failure");
            else
            {
                _diagnostics.Record("", "source.failure");
                if (recoveringFromSnapshotError) ErrorText.Text = string.Empty;
            }
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

    private bool IsStopTarget(int? index)
    {
        if (_snapshot is not { } s) return false;
        if (s.PendingTarget is { } pending)
            return pending.Digest == s.Digest && (index is null ? pending.Mode == "AUTO_SELECT" : pending.Mode == "PROFILE_INDEX" && pending.Index == index);
        if (s.PrimaryAction != "STOP") return false;
        if (index is null) return s.ActiveMode == "AUTO_SELECT" && s.ActiveDigest == s.Digest;
        return s.ActiveDigest == s.Digest && (s.State == "CONNECTED" ? s.ActiveProfile?.Index == index : s.ActiveMode == "PROFILE_INDEX" && s.ActiveIndex == index);
    }

    private bool CanAct(int? index)
    {
        if (_snapshot is not { } s) return false;
        if (IsStopTarget(index)) return !_stopInFlight;
        return !_busy && !_stopInFlight && !_sourceDirty && !_loading && _loadError.Length == 0 &&
            s.Sequence >= _acceptedSequence && s.Configured && (s.PrimaryAction == "START" || s.CanSwitch);
    }

    private string ActionTitle(int? index) => IsStopTarget(index)
        ? (_snapshot?.State == "CONNECTED" && _snapshot.PendingTarget is null ? "Disconnect" : "Stop")
        : (index is null ? "Auto connect" : "Connect");

    private void RenderActions()
    {
        SetConnectionAction(ActionTitle(null));
        ConnectionButton.IsEnabled = CanAct(null);
        if (_snapshot is not { } current) return;
        var actionsKey = JsonSerializer.Serialize(new { current.Sequence, _busy, _stopInFlight, _sourceDirty, _loading, _loadError });
        if (actionsKey == _actionsKey) return;
        _actionsKey = actionsKey;
        ProfileList.Children.Clear();
        ActiveStopButton.Visibility = current.PrimaryAction == "STOP" && !IsStopTarget(null) && !current.Profiles.Any(profile => IsStopTarget(profile.Index)) ? Visibility.Visible : Visibility.Collapsed;
        ActiveStopButton.Content = current.State == "CONNECTED" ? "Disconnect" : "Stop";
        ActiveStopButton.IsEnabled = !_stopInFlight;
        foreach (var profile in current.Profiles)
        {
            var row = new Grid { ColumnSpacing = 12 };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            row.Children.Add(new TextBlock { Text = $"{(profile.Description.Length == 0 ? $"Profile {profile.Index + 1}" : profile.Description)} · {profile.Protocol}", TextWrapping = TextWrapping.Wrap });
            var button = new Button { Content = ActionTitle(profile.Index), IsEnabled = CanAct(profile.Index) };
            AutomationProperties.SetAutomationId(button, $"Profile {profile.Index + 1} action");
            button.Click += async (_, _) => await PerformActionAsync(profile.Index);
            Grid.SetColumn(button, 1);
            row.Children.Add(button);
            ProfileList.Children.Add(row);
        }
    }

    private async void ConnectionButton_Click(object sender, RoutedEventArgs e) => await PerformActionAsync(null);
    private async void ActiveStop_Click(object sender, RoutedEventArgs e) => await PerformActionAsync(null, forceStop: true);

    private async Task PerformActionAsync(int? index, bool forceStop = false)
    {
        if (_snapshot is not { } current) return;
        if (forceStop || IsStopTarget(index))
        {
            if (_stopInFlight || current.PrimaryAction != "STOP") return;
            _stopInFlight = true;
            RenderActions();
            ErrorText.Text = "";
            try
            {
                await CallAsync<JsonElement>("Stop", new { session_id = current.SessionId, generation = current.Generation });
            }
            catch (Exception error) { ShowError(error.ToString(), error.Message); }
            finally { _stopInFlight = false; await RefreshSnapshotAsync(); RenderActions(); }
            return;
        }
        if (_busy || !CanAct(index)) return;
        _busy = true;
        RenderActions();
        ErrorText.Text = "";
        try
        {
            await CallAsync<JsonElement>("Start", new {
                session_id = current.SessionId, expected_sequence = current.Sequence,
                mode = index is null ? "AUTO_SELECT" : "PROFILE_INDEX", index = index ?? 0,
                digest = current.Digest, replace_current = true
            });
        }
        catch (Exception error) { ShowError(error.ToString(), error.Message); }
        finally { _busy = false; await RefreshSnapshotAsync(); RenderActions(); }
    }

    private static bool ValidSubscription(string source) => Uri.TryCreate(source, UriKind.Absolute, out var uri) && uri.Scheme == "https" && uri.Host.Length > 0;

    private async void SourceChanged(bool immediate = false)
    {
        _sourceDirty = true;
        _loadError = "";
        ErrorText.Text = "";
        RetryButton.Visibility = Visibility.Collapsed;
        _loadRevision++;
        _pendingLoad = null;
        _debounce?.Cancel();
        _debounce?.Dispose();
        _debounce = CancellationTokenSource.CreateLinkedTokenSource(_shutdown.Token);
        var cancellation = _debounce.Token;
        RenderActions();
        var source = SourceEditor.Text.Trim();
        if (!ValidSubscription(source)) { LoadStatus.Text = "Enter an HTTPS subscription URL"; return; }
        try
        {
            if (!immediate) await Task.Delay(400, cancellation);
            cancellation.ThrowIfCancellationRequested();
            _pendingLoad = source;
            await LoadNextAsync();
        }
        catch (OperationCanceledException) { }
    }

    private void ClipboardChanged(object? sender, object e) => DispatcherQueue.TryEnqueue(RefreshClipboard);
    private void RefreshClipboard()
    {
        try { PasteButton.Visibility = Clipboard.GetContent().Contains(StandardDataFormats.Text) ? Visibility.Visible : Visibility.Collapsed; }
        catch (Exception error) { RecordError(error.ToString()); PasteButton.Visibility = Visibility.Collapsed; }
    }
    private async void Paste_Click(object sender, RoutedEventArgs e)
    {
        try { ImportSubscription((await Clipboard.GetContent().GetTextAsync()).Trim()); }
        catch (Exception error) { ShowError(error.ToString(), error.Message); }
    }
    private void ImportSubscription(string source)
    {
        if (!ValidSubscription(source)) throw new InvalidOperationException("Enter an HTTPS subscription URL with a host");
        if (SourceEditor.Text == source && (_loading || !_sourceDirty && _snapshot?.Configured == true)) return;
        SetSourceText(source);
        SourceChanged(immediate: true);
    }
    public void ImportLink(string value)
    {
        Activate();
        try
        {
            if (value.Equals("dobbyvpn://", StringComparison.OrdinalIgnoreCase)) return;
            if (System.Text.RegularExpressions.Regex.IsMatch(value, "%(?![0-9a-fA-F]{2})")) throw new FormatException("Invalid URL escape");
            var uri = new Uri(value);
            if (uri.Scheme != "dobbyvpn" || uri.Host != "import" || uri.AbsolutePath is not ("" or "/") || uri.UserInfo.Length > 0 || !uri.IsDefaultPort || uri.Fragment.Length > 0)
                throw new FormatException("Invalid import link");
            var query = uri.Query.TrimStart('?').Split('&');
            var pair = query[0].Split('=', 2);
            if (query.Length != 1 || pair.Length != 2 || pair[0] != "url") throw new FormatException("The import link requires one url parameter");
            ImportSubscription(Uri.UnescapeDataString(pair[1]));
        }
        catch (Exception error) { ShowError(error.ToString(), "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL"); }
    }

    private void Retry_Click(object sender, RoutedEventArgs e) => SourceChanged(immediate: true);

    private async Task LoadNextAsync()
    {
        if (_loading || _pendingLoad is not { } source || _snapshot is null) return;
        _pendingLoad = null;
        _loading = true;
        var revision = _loadRevision;
        LoadStatus.Text = "Loading profiles…";
        RenderActions();
        try
        {
            var current = await ReadSnapshotAsync();
            var configured = await CallAsync<JsonElement>("Configure", new { session_id = current.SessionId, expected_sequence = current.Sequence, source });
            if (revision == _loadRevision) { _acceptedSequence = configured.GetProperty("sequence").GetInt64(); MarkSourceAccepted(source); LoadStatus.Text = ""; }
        }
        catch (Exception error)
        {
            if (revision == _loadRevision)
            {
                _loadError = error.Message;
                LoadStatus.Text = _loadError;
                RetryButton.Visibility = Visibility.Visible;
                RecordError(error.ToString());
            }
        }
        finally
        {
            _loading = false;
            await RefreshSnapshotAsync();
            RenderActions();
        }
        await LoadNextAsync();
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
        _programmaticSourceText = value;
        try
        {
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

    private async void About_Click(object sender, RoutedEventArgs e)
    {
        var details = new StackPanel { Spacing = 12 };
        var version = new TextBlock { Text = $"Version: {_version}", IsTextSelectionEnabled = true };
        AutomationProperties.SetAutomationId(version, "About version metadata");
        details.Children.Add(version);
        var compactCommit = new TextBlock { Text = $"Commit: {_commit[..Math.Min(12, _commit.Length)]}" };
        AutomationProperties.SetAutomationId(compactCommit, "About compact commit metadata");
        details.Children.Add(compactCommit);
        var sourceCommit = new TextBlock { Text = $"Source commit: {_commit}", TextWrapping = TextWrapping.Wrap, IsTextSelectionEnabled = true };
        AutomationProperties.SetAutomationId(sourceCommit, "About source commit metadata");
        details.Children.Add(sourceCommit);
        if (_commit.Length == 40 && _commit.All(Uri.IsHexDigit))
        {
            var sourceUrl = $"https://github.com/DobbyVPN/DobbyVPN/tree/{_commit}";
            var sourceLink = new HyperlinkButton { Content = "Source code", NavigateUri = new Uri(sourceUrl) };
            AutomationProperties.SetAutomationId(sourceLink, "About source link");
            AutomationProperties.SetHelpText(sourceLink, sourceUrl);
            details.Children.Add(sourceLink);
        }
        await new ContentDialog { Title = "About DobbyVPN", Content = details, CloseButtonText = "Done", XamlRoot = Root.XamlRoot }.ShowAsync();
    }

    private void RecordError(string message) => _diagnostics.Record(message, "ui.failure");

    private void ShowError(string details, string message, string category = "ui.failure")
    {
        _diagnostics.Record(details, category);
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
        _logScroll = LogsScroll;
        LogsScroll.AddHandler(UIElement.PointerWheelChangedEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, _) => _userScrolling = true), true);
        LogsScroll.AddHandler(UIElement.PointerPressedEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, _) => _userScrolling = true), true);
        LogsScroll.AddHandler(UIElement.KeyDownEvent, new Microsoft.UI.Xaml.Input.KeyEventHandler((_, args) =>
        {
            if (args.Key is global::Windows.System.VirtualKey.Up or global::Windows.System.VirtualKey.Down
                or global::Windows.System.VirtualKey.PageUp or global::Windows.System.VirtualKey.PageDown
                or global::Windows.System.VirtualKey.Home or global::Windows.System.VirtualKey.End) _userScrolling = true;
        }), true);
        LogsScroll.ViewChanged += (_, _) =>
        {
            if (_updatingLogs || !_userScrolling) return;
            _followingLogs = LogsScroll.VerticalOffset >= LogsScroll.ScrollableHeight - 8;
            if (_followingLogs) RenderLogs();
        };
        _ = RefreshLogsAsync();
    }

    private async void ClearLogs_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            if (_clearingLogs) return;
            _clearingLogs = true;
            ++_logRevision;
            await _diagnostics.ClearViewAsync();
            _followingLogs = true;
            _latestLogs = [];
            _expandedLogs.Clear();
            _renderedLogs = "";
            LogEntries.Children.Clear();
            _clearingLogs = false;
            await RefreshLogsAsync();
        }
        catch (Exception error) { RecordError(error.ToString()); LogsErrorText.Text = error.Message; }
        finally { _clearingLogs = false; }
    }

    private async Task RefreshLogsAsync()
    {
        try
        {
            if (_clearingLogs) return;
            var revision = ++_logRevision;
            var preview = await _diagnostics.EntriesAsync();
            if (revision != _logRevision) return;
            _latestLogs = preview.Entries;
            LogsErrorText.Text = preview.Error;
            if (_followingLogs) RenderLogs();
        }
        catch (Exception error) { RecordError(error.ToString()); LogsErrorText.Text = error.Message; }
    }

    private void RenderLogs()
    {
        var key = string.Join("|", _latestLogs.Select(entry => entry.Id + entry.Raw));
        if (_updatingLogs || key == _renderedLogs) return;
        _updatingLogs = true;
        _renderedLogs = key;
        LogEntries.Children.Clear();
        _expandedLogs.IntersectWith(_latestLogs.Select(entry => entry.Id));
        foreach (var entry in _latestLogs)
        {
            var resource = entry.Level switch
            {
                "ERROR" or "FATAL" or "PANIC" => "SystemFillColorCriticalBrush",
                "WARN" or "WARNING" => "SystemFillColorCautionBrush",
                "DEBUG" or "TRACE" => "TextFillColorSecondaryBrush",
                _ => "TextFillColorPrimaryBrush"
            };
            var text = new RichTextBlock { IsTextSelectionEnabled = true, TextWrapping = TextWrapping.Wrap,
                Foreground = (Brush)Application.Current.Resources[resource] };
            var paragraph = new Paragraph();
            paragraph.Inlines.Add(new Run { Text = string.Join(" · ", new[] { entry.Timestamp, entry.Level, entry.Source }.Where(value => value.Length > 0)) + "\n" + entry.Message });
            text.Blocks.Add(paragraph);
            LogEntries.Children.Add(text);
            if (entry.Level != "RAW")
            {
                var details = new Expander { Header = "Details", HorizontalAlignment = HorizontalAlignment.Stretch,
                    IsExpanded = _expandedLogs.Contains(entry.Id),
                    Content = new TextBlock { Text = entry.Raw, TextWrapping = TextWrapping.Wrap, IsTextSelectionEnabled = true } };
                details.Expanding += (_, _) => _expandedLogs.Add(entry.Id);
                details.Collapsed += (_, _) => _expandedLogs.Remove(entry.Id);
                LogEntries.Children.Add(details);
            }
        }
        LogEntries.UpdateLayout();
        _logScroll?.ChangeView(null, _logScroll.ScrollableHeight, null, true);
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, () => { _updatingLogs = false; _userScrolling = false; });
    }

    private string ExportHeader =>
        $"DobbyVPN {_version}\nSource commit: {_commit}\nPlatform: {Environment.OSVersion}\nCaptured: {DateTimeOffset.UtcNow:O}\n\n";

    private async void SaveLogs_Click(object sender, RoutedEventArgs e)
    {
        if (_exportingLogs) return;
        _exportingLogs = true;
        try
        {
            var picker = new FileSavePicker(AppWindow.Id) { SuggestedFileName = "DobbyVPN-logs" };
            picker.FileTypeChoices.Add("Compressed diagnostics", new List<string> { ".gz" });
            var file = await picker.PickSaveFileAsync();
            if (file is not null)
            {
                await _diagnostics.SaveAsync(file.Path, ExportHeader);
            }
        }
        catch (Exception error) { RecordError(error.ToString()); LogsErrorText.Text = "Logs could not be saved. Try another location."; }
        finally { _exportingLogs = false; }
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
        [JsonPropertyName("digest")] public string Digest { get; init; } = "";
        [JsonPropertyName("profiles")] public Profile[] Profiles { get; init; } = [];
        [JsonPropertyName("active_digest")] public string ActiveDigest { get; init; } = "";
        [JsonPropertyName("active_mode")] public string ActiveMode { get; init; } = "";
        [JsonPropertyName("active_index")] public int ActiveIndex { get; init; }
        [JsonPropertyName("pending_target")] public Selection? PendingTarget { get; init; }
        [JsonPropertyName("can_switch")] public bool CanSwitch { get; init; }
    }

    private sealed class Selection
    {
        public string Digest { get; init; } = "";
        public string Mode { get; init; } = "";
        public int Index { get; init; }
    }

    private sealed class Profile
    {
        [JsonPropertyName("index")] public int Index { get; init; }
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
