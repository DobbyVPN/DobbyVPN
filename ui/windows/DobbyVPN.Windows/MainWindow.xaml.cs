using System.Reflection;
using System.IO;
using Windows.ApplicationModel.DataTransfer;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Media;
using Microsoft.Windows.Storage.Pickers;

namespace DobbyVPN.Windows;

public sealed partial class MainWindow : Window
{
    private const string PipeName = "DobbyVPN.Control";
    private const string ContentRootPeersPathVariable = "DOBBYVPN_NATIVE_UI_CONTENT_ROOT_PEERS_PATH";
    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool ClientToScreen(IntPtr window, ref ScreenPoint point);
    [DllImport("user32.dll", SetLastError = true)]
    private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr dpiContext);
    [StructLayout(LayoutKind.Sequential)]
    private struct ScreenPoint { public int X; public int Y; }

    private static ScreenPoint GetPhysicalClientOrigin(IntPtr window)
    {
        var previousContext = SetThreadDpiAwarenessContext(new IntPtr(-4)); // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
        if (previousContext == IntPtr.Zero)
            throw new System.ComponentModel.Win32Exception(
                Marshal.GetLastWin32Error(), "Could not enter the physical-pixel DPI context");

        var point = new ScreenPoint();
        Exception? failure = null;
        try
        {
            if (!ClientToScreen(window, ref point))
                failure = new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not map the HWND client origin to screen coordinates");
        }
        catch (Exception error)
        {
            failure = error;
        }

        if (SetThreadDpiAwarenessContext(previousContext) == IntPtr.Zero)
        {
            var restoreFailure = new System.ComponentModel.Win32Exception(
                Marshal.GetLastWin32Error(), "Could not restore the UI thread DPI context");
            failure = failure is null ? restoreFailure : new AggregateException(failure, restoreFailure);
        }
        if (failure is not null) throw failure;
        return point;
    }
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
    private long _logRenderGeneration;
    private double _lastLogScrollOffset;
    private double _pendingLogScrollOffset;
    private bool _pendingLogDownScroll;
    private bool _logDirectManipulation;
    private bool _logScrollbarPointerDown;
    private bool _pendingLogUpwardIntent;
    private bool _logTailChangeViewPending;
    private List<NativeDiagnostics.Entry> _latestLogs = [];
    private readonly HashSet<string> _expandedLogs = [];
    private readonly Dictionary<LogRowKey, RenderedLogRow> _logRowCache = [];
    private List<RenderedLogRow> _renderedLogRows = [];
    private readonly string _version;
    private readonly string _commit;
    private readonly NativeDiagnostics _diagnostics = NativeDiagnostics.Current;
    private bool _exportingLogs;

    private readonly record struct LogRowKey(string Id, string Raw);
    private sealed record RenderedLogRow(LogRowKey Key, UIElement[] Controls);

    public MainWindow()
    {
        InitializeComponent();
        if (!string.IsNullOrWhiteSpace(Environment.GetEnvironmentVariable(ContentRootPeersPathVariable)))
            Root.Loaded += (_, _) => WriteContentRootPeerDiagnostic();
        Root.LayoutUpdated += (_, _) => UpdateContentLayout();
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
        Activated += (_, _) => DispatcherQueue.TryEnqueue(RefreshClipboard);
        Closed += (_, _) => Clipboard.ContentChanged -= ClipboardChanged;
        _ = PollSnapshotsAsync(_shutdown.Token);
        _ = RefreshSnapshotAsync();
    }

    private void UpdateContentLayout()
    {
        var height = Root.ActualHeight;
        if (height <= 0) return;
        var logChrome = LogsGrid.RowDefinitions[0].ActualHeight +
            LogsGrid.RowDefinitions[1].ActualHeight +
            LogsGrid.RowSpacing * (LogsGrid.RowDefinitions.Count - 1);
        var controlsHeight = Math.Max(0, Math.Min(height * 0.6,
            height - Root.RowSpacing - logChrome - LogsScroll.MinHeight));
        var fixedControls = ControlsContent.RowDefinitions[0].ActualHeight +
            ControlsContent.RowDefinitions[1].ActualHeight +
            ControlsContent.RowSpacing * (ControlsContent.RowDefinitions.Count - 1);
        var profileHeight = Math.Max(0, Math.Min(Math.Min(180, height * 0.2),
            controlsHeight - fixedControls));

        // Header/error rows grow with the OS text size. Cap only the scrolling
        // controls so the log viewport keeps its usable minimum at that size.
        if (Math.Abs(ControlsScroll.MaxHeight - controlsHeight) > 0.5)
            ControlsScroll.MaxHeight = controlsHeight;
        if (Math.Abs(ProfilesScroll.MaxHeight - profileHeight) > 0.5)
            ProfilesScroll.MaxHeight = profileHeight;
    }

    private void WriteContentRootPeerDiagnostic()
    {
        var path = Environment.GetEnvironmentVariable(ContentRootPeersPathVariable);
        if (string.IsNullOrWhiteSpace(path)) return;

        object diagnostic;
        var hasDispatcherAccess = DispatcherQueue.HasThreadAccess;
        var content = Content;
        AutomationPeer? rootPeer = null;
        try
        {
            if (!hasDispatcherAccess)
                throw new InvalidOperationException("XAML root diagnostic did not run on the window dispatcher");

            rootPeer = FrameworkElementAutomationPeer.CreatePeerForElement(Root);
            var editorPeer = FrameworkElementAutomationPeer.CreatePeerForElement(SourceEditor)
                ?? throw new InvalidOperationException("SourceEditor has no automation peer");
            var editorBounds = SourceEditor.TransformToVisual(Root).TransformBounds(
                new global::Windows.Foundation.Rect(0, 0, SourceEditor.ActualWidth, SourceEditor.ActualHeight));
            object? screenGeometry = null;
            string? screenGeometryError = null;
            try
            {
                var screenBounds = SourceEditor.TransformToVisual(null).TransformBounds(
                    new global::Windows.Foundation.Rect(0, 0, SourceEditor.ActualWidth, SourceEditor.ActualHeight));
                var xamlRoot = SourceEditor.XamlRoot
                    ?? throw new InvalidOperationException("SourceEditor has no XamlRoot for screen geometry");
                var rasterizationScale = xamlRoot.RasterizationScale;
                var windowHandle = WinRT.Interop.WindowNative.GetWindowHandle(this);
                var clientOrigin = GetPhysicalClientOrigin(windowHandle);
                var left = clientOrigin.X + screenBounds.X * rasterizationScale;
                var top = clientOrigin.Y + screenBounds.Y * rasterizationScale;
                var width = screenBounds.Width * rasterizationScale;
                var height = screenBounds.Height * rasterizationScale;
                screenGeometry = new
                {
                    coordinateSpace = "physical-screen-pixels",
                    clientOriginDpiContext = "per-monitor-v2",
                    clientOrigin = new { x = clientOrigin.X, y = clientOrigin.Y },
                    rasterizationScale,
                    left,
                    top,
                    width,
                    height,
                    centerX = checked((int)Math.Round(left + width / 2, MidpointRounding.AwayFromZero)),
                    centerY = checked((int)Math.Round(top + height / 2, MidpointRounding.AwayFromZero)),
                };
            }
            catch (Exception error)
            {
                screenGeometryError = error.ToString();
            }
            diagnostic = new
            {
                schema = "dobbyvpn.windows-content-root-peers/v2",
                completed = true,
                diagnosticOnly = true,
                dispatcherThreadAccess = hasDispatcherAccess,
                windowContentIsRoot = ReferenceEquals(content, Root),
                windowContentType = content?.GetType().FullName,
                rootType = Root.GetType().FullName,
                rootPeerCreated = rootPeer is not null,
                rootPeerType = rootPeer?.GetType().FullName,
                editor = new
                {
                    automationId = editorPeer.GetAutomationId(),
                    name = editorPeer.GetName(),
                    controlType = editorPeer.GetAutomationControlType().ToString(),
                    peerType = editorPeer.GetType().FullName,
                    isControlElement = editorPeer.IsControlElement(),
                    isContentElement = editorPeer.IsContentElement(),
                    isLoaded = SourceEditor.IsLoaded,
                    isVisible = SourceEditor.Visibility == Visibility.Visible,
                    isEnabled = SourceEditor.IsEnabled,
                    geometry = new
                    {
                        x = editorBounds.X,
                        y = editorBounds.Y,
                        width = editorBounds.Width,
                        height = editorBounds.Height,
                    },
                    screenGeometry,
                    screenGeometryError,
                },
            };
        }
        catch (Exception error)
        {
            diagnostic = new
            {
                schema = "dobbyvpn.windows-content-root-peers/v2",
                completed = false,
                diagnosticOnly = true,
                dispatcherThreadAccess = hasDispatcherAccess,
                windowContentIsRoot = ReferenceEquals(content, Root),
                windowContentType = content?.GetType().FullName,
                rootType = Root.GetType().FullName,
                rootPeerCreated = rootPeer is not null,
                rootPeerType = rootPeer?.GetType().FullName,
                editor = (object?)null,
                error = error.ToString(),
            };
        }

        try
        {
            var fullPath = Path.GetFullPath(path);
            Directory.CreateDirectory(Path.GetDirectoryName(fullPath)!);
            var temporaryPath = fullPath + ".tmp";
            File.WriteAllText(temporaryPath, JsonSerializer.Serialize(diagnostic) + Environment.NewLine);
            File.Move(temporaryPath, fullPath, overwrite: true);
        }
        catch (Exception error)
        {
            _diagnostics.Record(error.ToString(), "test.content-root-diagnostic-write-failure");
        }
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
            var previous = _snapshot;
            if (result.SessionId == previous?.SessionId && result.Sequence < Math.Max(previous.Sequence, _acceptedSequence)) return;
            var recoveringFromSnapshotError = previous is null || StatusText.Text == "Error";
            if (result.SessionId != previous?.SessionId) _acceptedSequence = 0;
            else if (_sourceDirty && previous is not null)
            {
                result.Configured = previous.Configured;
                result.SourceUrl = previous.SourceUrl;
                result.Digest = previous.Digest;
                result.Profiles = previous.Profiles;
            }
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
        catch (OperationCanceledException) when (_shutdown.IsCancellationRequested)
        {
            // Window shutdown cancels the in-flight Snapshot request. Do not
            // report this expected lifecycle cancellation as a UI failure.
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
            var description = new TextBlock
            {
                Text = $"{(profile.Description.Length == 0 ? $"Profile {profile.Index + 1}" : profile.Description)} · {profile.Protocol}",
                TextWrapping = TextWrapping.Wrap
            };
            AutomationProperties.SetAutomationId(description, $"Profile {profile.Index + 1} description");
            row.Children.Add(description);
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
        _diagnostics.RecordInfo("subscription.paste-click", "Started handling a Paste button click", new { session_id = _snapshot?.SessionId });
        try
        {
            var pastedText = (await Clipboard.GetContent().GetTextAsync()).Trim();
            _diagnostics.RecordInfo("subscription.paste-clipboard-text-returned", "Clipboard text returned to the Paste handler", new { session_id = _snapshot?.SessionId, text_length = pastedText.Length });
            ImportSubscription(pastedText);
        }
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
        _diagnostics.RecordInfo(
            "activation.import-link",
            "Received a URI for the main window",
            new { uri = value });
        Activate();
        try
        {
            if (value.Equals("dobbyvpn://", StringComparison.OrdinalIgnoreCase) ||
                value.Equals("dobbyvpn:///", StringComparison.OrdinalIgnoreCase)) return;
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
            _diagnostics.RecordInfo("subscription.configure-preflight-snapshot-start", "Started the preflight Snapshot before Configure", new { source, session_id = _snapshot.SessionId });
            var current = await ReadSnapshotAsync();
            _diagnostics.RecordInfo("subscription.configure-preflight-snapshot-end", "Completed the preflight Snapshot before Configure", new { source, session_id = current.SessionId, sequence = current.Sequence });
            _diagnostics.RecordInfo(
                "subscription.configure-submit",
                "Submitting a subscription URL to the Go backend",
                new { source, session_id = current.SessionId, expected_sequence = current.Sequence });
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
        if (method == "Configure") _diagnostics.RecordInfo("subscription.configure-pipe-connect-start", "Started connecting the Configure named pipe", new { method, parameters });
        await pipe.ConnectAsync(3000, timeout.Token);
        if (method == "Configure") _diagnostics.RecordInfo("subscription.configure-pipe-connect-end", "Connected the Configure named pipe", new { method, parameters });
        await using var writer = new StreamWriter(pipe, new UTF8Encoding(false), 1024, leaveOpen: true) { AutoFlush = true };
        using var reader = new StreamReader(pipe, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, bufferSize: 1024, leaveOpen: true);
        var request = JsonSerializer.Serialize(new { method, @params = parameters });
        await writer.WriteLineAsync(request.AsMemory(), timeout.Token);
        if (method == "Configure") _diagnostics.RecordInfo("subscription.configure-pipe-request-written", "Wrote the Configure request to the Go backend", new { method, parameters });
        var line = await reader.ReadLineAsync(timeout.Token);
        if (method == "Configure") _diagnostics.RecordInfo("subscription.configure-pipe-response-read", "Read the Configure response from the Go backend", new { method, parameters, succeeded = line is not null });
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
        CaptureLogScrollPosition();
        LogsScroll.SizeChanged += (_, _) =>
        {
            if (!_followingLogs) ClearPendingLogDownScrollIntent();
            if (_followingLogs) QueueLogTailChangeView(_logRenderGeneration);
        };
        LogEntries.SizeChanged += (_, _) =>
        {
            if (_followingLogs && !_clearingLogs)
                QueueLogTailChangeView(_logRenderGeneration);
        };
        LogsScroll.AddHandler(UIElement.PointerWheelChangedEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, args) =>
        {
            var properties = args.GetCurrentPoint(LogsScroll).Properties;
            if (properties.IsHorizontalMouseWheel || properties.MouseWheelDelta == 0) return;
            if (properties.MouseWheelDelta > 0) RequestLogFollowFreezeForUpwardIntent();
            else RegisterLogDownScrollIntent();
        }), true);
        LogsScroll.AddHandler(UIElement.KeyDownEvent, new Microsoft.UI.Xaml.Input.KeyEventHandler((_, args) =>
        {
            switch (args.Key)
            {
                case global::Windows.System.VirtualKey.Up:
                case global::Windows.System.VirtualKey.PageUp:
                case global::Windows.System.VirtualKey.Home:
                    RequestLogFollowFreezeForUpwardIntent();
                    break;
                case global::Windows.System.VirtualKey.Down:
                case global::Windows.System.VirtualKey.PageDown:
                    RegisterLogDownScrollIntent();
                    break;
                case global::Windows.System.VirtualKey.End:
                    RegisterLogDownScrollIntent();
                    break;
            }
        }), true);
        LogsScroll.AddHandler(UIElement.PointerPressedEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, args) =>
        {
            if (FindLogVerticalScrollBar(args.OriginalSource) is not null)
            {
                ClearPendingLogDownScrollIntent();
                _logScrollbarPointerDown = true;
            }
        }), true);
        LogsScroll.AddHandler(UIElement.PointerReleasedEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, args) =>
        {
            if (FindLogVerticalScrollBar(args.OriginalSource) is null) return;
            ObserveLogScrollPosition();
            _logScrollbarPointerDown = false;
            if (_followingLogs) QueueLogTailChangeView(_logRenderGeneration);
        }), true);
        LogsScroll.AddHandler(UIElement.PointerCaptureLostEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, args) =>
        {
            if (FindLogVerticalScrollBar(args.OriginalSource) is null) return;
            ObserveLogScrollPosition();
            _logScrollbarPointerDown = false;
            if (_followingLogs) QueueLogTailChangeView(_logRenderGeneration);
        }), true);
        LogsScroll.DirectManipulationStarted += (_, _) =>
        {
            ClearPendingLogDownScrollIntent();
            _logDirectManipulation = true;
        };
        LogsScroll.DirectManipulationCompleted += (_, _) =>
        {
            ObserveLogScrollPosition();
            _logDirectManipulation = false;
            if (_followingLogs) QueueLogTailChangeView(_logRenderGeneration);
        };
        LogsScroll.ViewChanged += (_, args) =>
        {
            ObserveLogScrollPosition(args.IsIntermediate);
        };
        _ = RefreshLogsAsync();
    }

    private void RegisterLogDownScrollIntent()
    {
        if (_logScroll is null) return;
        var remainingRange = _logScroll.ScrollableHeight - _logScroll.VerticalOffset;
        if (remainingRange <= 0.5)
        {
            ClearPendingLogDownScrollIntent();
            _pendingLogUpwardIntent = false;
            _followingLogs = true;
            if (!_clearingLogs) RenderLogs();
            return;
        }
        if (_followingLogs) return;
        _pendingLogScrollOffset = _logScroll.VerticalOffset;
        _pendingLogDownScroll = true;
    }

    private void RequestLogFollowFreezeForUpwardIntent()
    {
        if (_logScroll is null) return;
        ClearPendingLogDownScrollIntent();
        if (_logScroll.ScrollableHeight > 0.5)
        {
            _followingLogs = false;
            return;
        }

        if (_updatingLogs || _logTailChangeViewPending)
            _pendingLogUpwardIntent = true;
    }

    private static ScrollBar? FindLogVerticalScrollBar(object? source)
    {
        var current = source as DependencyObject;
        while (current is not null)
        {
            if (current is ScrollBar scrollBar)
                return scrollBar.Orientation == Orientation.Vertical ? scrollBar : null;
            current = VisualTreeHelper.GetParent(current);
        }
        return null;
    }

    private void ObserveLogScrollPosition(bool isIntermediate = false)
    {
        if (_logScroll is null) return;
        var currentOffset = _logScroll.VerticalOffset;
        var offsetDelta = currentOffset - _lastLogScrollOffset;
        CaptureLogScrollPosition();
        if (Math.Abs(offsetDelta) <= 0.5)
        {
            if (!isIntermediate && !_updatingLogs && !_logTailChangeViewPending)
                ClearPendingLogDownScrollIntent();
            return;
        }

        var directManipulation = _logDirectManipulation;
        var scrollbarInteraction = _logScrollbarPointerDown;
        var directedInput = _pendingLogDownScroll && currentOffset - _pendingLogScrollOffset > 0.5;
        if (!directManipulation && !scrollbarInteraction && !directedInput) return;

        if (directManipulation || scrollbarInteraction)
            ClearPendingLogDownScrollIntent();
        else
        {
            _followingLogs = IsLogScrollAtBottom();
            if (!isIntermediate) ClearPendingLogDownScrollIntent();
        }
        if (directManipulation || scrollbarInteraction)
            _followingLogs = IsLogScrollAtBottom();
        if (!_updatingLogs && _followingLogs) RenderLogs();
    }

    private void ClearPendingLogDownScrollIntent()
    {
        _pendingLogDownScroll = false;
    }

    private void CaptureLogScrollPosition()
    {
        if (_logScroll is null) return;
        _lastLogScrollOffset = _logScroll.VerticalOffset;
    }

    private bool IsLogScrollAtBottom() => _logScroll is not null &&
        _logScroll.VerticalOffset >= _logScroll.ScrollableHeight - 8;

    private async void ClearLogs_Click(object sender, RoutedEventArgs e)
    {
        try
        {
            if (_clearingLogs) return;
            _clearingLogs = true;
            ++_logRevision;
            ++_logRenderGeneration;
            _updatingLogs = false;
            ClearPendingLogDownScrollIntent();
            _pendingLogUpwardIntent = false;
            _logTailChangeViewPending = false;
            await _diagnostics.ClearViewAsync();
            _followingLogs = true;
            _latestLogs = [];
            _expandedLogs.Clear();
            _logRowCache.Clear();
            _renderedLogRows = [];
            LogEntries.Children.Clear();
            CaptureLogScrollPosition();
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
        var keys = _latestLogs.Select(entry => new LogRowKey(entry.Id, entry.Raw)).ToArray();
        if (_updatingLogs || keys.SequenceEqual(_renderedLogRows.Select(row => row.Key))) return;

        var desiredKeys = keys.ToHashSet();
        var retainedRows = _renderedLogRows.Where(row => desiredKeys.Contains(row.Key)).ToArray();
        var retainedKeysInNewOrder = keys.Where(_logRowCache.ContainsKey).ToArray();
        var retainedOrderMatches = retainedRows.Select(row => row.Key).SequenceEqual(retainedKeysInNewOrder);
        HashSet<LogRowKey> retainedKeys = retainedOrderMatches
            ? retainedRows.Select(row => row.Key).ToHashSet()
            : [];

        _updatingLogs = true;
        var renderGeneration = ++_logRenderGeneration;

        // Normal refreshes append rows or trim the oldest preview rows. Keep
        // retained controls attached so text selection and expanded Details
        // survive those updates. If timestamps reorder streams, reattach the
        // keyed controls in the new chronological order without recreating them.
        if (!retainedOrderMatches)
            foreach (var row in _renderedLogRows)
                foreach (var control in row.Controls)
                    LogEntries.Children.Remove(control);

        foreach (var row in _renderedLogRows)
        {
            if (desiredKeys.Contains(row.Key)) continue;
            foreach (var control in row.Controls)
                LogEntries.Children.Remove(control);
            _logRowCache.Remove(row.Key);
        }

        _expandedLogs.IntersectWith(_latestLogs.Select(entry => entry.Id));
        var nextRows = new List<RenderedLogRow>(_latestLogs.Count);
        var childIndex = 0;
        for (var rowIndex = 0; rowIndex < _latestLogs.Count; rowIndex++)
        {
            var entry = _latestLogs[rowIndex];
            var key = keys[rowIndex];
            if (!_logRowCache.TryGetValue(key, out var row))
            {
                row = CreateLogRow(entry, key);
                _logRowCache.Add(key, row);
            }

            if (!retainedKeys.Contains(key))
                foreach (var control in row.Controls)
                    LogEntries.Children.Insert(childIndex++, control);
            else
                childIndex += row.Controls.Length;

            nextRows.Add(row);
        }
        _renderedLogRows = nextRows;
        CaptureLogScrollPosition();
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, () =>
        {
            if (renderGeneration != _logRenderGeneration) return;
            _updatingLogs = false;
            CaptureLogScrollPosition();
            var latestKeys = _latestLogs.Select(entry => new LogRowKey(entry.Id, entry.Raw)).ToArray();
            if (_followingLogs && !latestKeys.SequenceEqual(_renderedLogRows.Select(row => row.Key)))
            {
                RenderLogs();
                return;
            }
            QueueLogTailChangeView(renderGeneration);
        });
    }

    private RenderedLogRow CreateLogRow(NativeDiagnostics.Entry entry, LogRowKey key)
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
        var controls = new List<UIElement> { text };
        if (entry.Level != "RAW")
        {
            var details = new Expander { Header = "Details", HorizontalAlignment = HorizontalAlignment.Stretch,
                IsExpanded = _expandedLogs.Contains(entry.Id),
                Content = new TextBlock { Text = entry.Raw, TextWrapping = TextWrapping.Wrap, IsTextSelectionEnabled = true } };
            details.Expanding += (_, _) => _expandedLogs.Add(entry.Id);
            details.Collapsed += (_, _) => _expandedLogs.Remove(entry.Id);
            controls.Add(details);
        }
        return new RenderedLogRow(key, controls.ToArray());
    }

    private void QueueLogTailChangeView(long renderGeneration)
    {
        if (_updatingLogs) return;
        _logTailChangeViewPending = true;
        DispatcherQueue.TryEnqueue(Microsoft.UI.Dispatching.DispatcherQueuePriority.Low, () =>
        {
            if (renderGeneration != _logRenderGeneration)
                return;
            if (_clearingLogs || _updatingLogs || _logDirectManipulation || _logScrollbarPointerDown || _logScroll is null) return;
            _logTailChangeViewPending = false;
            if (_pendingLogUpwardIntent)
            {
                _pendingLogUpwardIntent = false;
                if (_logScroll.ScrollableHeight > 0.5)
                {
                    ClearPendingLogDownScrollIntent();
                    _followingLogs = false;
                    return;
                }
                ClearPendingLogDownScrollIntent();
                _followingLogs = true;
            }
            if (!_followingLogs) return;
            var targetOffset = _logScroll.ScrollableHeight;
            _logScroll.ChangeView(null, targetOffset, null, true);
            CaptureLogScrollPosition();
        });
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
        [JsonPropertyName("configured")] public bool Configured { get; set; }
        [JsonPropertyName("source_url")] public string SourceUrl { get; set; } = "";
        [JsonPropertyName("source_error")] public string SourceError { get; init; } = "";
        [JsonPropertyName("active_profile")] public Profile? ActiveProfile { get; init; }
        [JsonPropertyName("last_failure")] public Failure? LastFailure { get; init; }
        [JsonPropertyName("recovering")] public bool Recovering { get; init; }
        [JsonPropertyName("digest")] public string Digest { get; set; } = "";
        [JsonPropertyName("profiles")] public Profile[] Profiles { get; set; } = [];
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
