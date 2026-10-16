using System.Reflection;
using System.IO;
using System.Diagnostics;
using Windows.ApplicationModel.DataTransfer;
using System.Collections.Generic;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.IO.Pipes;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Windowing;
using Microsoft.Windows.Storage.Pickers;

namespace DobbyVPN.Windows;

public sealed partial class MainWindow : Window
{
    private const string PipeName = "DobbyVPN.Control";
    private readonly PeriodicTimer _pollTimer = new(TimeSpan.FromMilliseconds(750));
    private readonly WindowLifetime _lifetime = new();
    private readonly long _loadClockOrigin = Stopwatch.GetTimestamp();
    private readonly SubscriptionLoadState _subscriptionLoad = new();
    private Snapshot? _snapshot;
    private bool _updatingSource;
    // TextBox may report a programmatic Text assignment after SetSourceText returns.
    private string? _programmaticSourceText;
    private bool _busy;
    private bool _stopInFlight;
    private bool _snapshotInFlight;
    private string? _acceptedInThisWindow;
    private string? _renderedSource;
    private bool _sourceInitialized;
    private long _acceptedSequence;
    private string _actionsKey = "";
    private string _loadError = "";
    private string _restoredLoad = "";
    private CancellationTokenSource? _debounce;
    private long _logRevision;
    private bool _clearingLogs;
    private bool _updatingLogs;
    private ScrollViewer? _logScroll;
    private long _logRenderGeneration;
    private double _lastLogScrollOffset;
    private double _pendingLogScrollOffset;
    private bool _pendingLogDownScroll;
    private long _logScrollGestureSequence;
    private long? _pendingLogScrollGestureId;
    private string? _pendingLogScrollGestureDirection;
    private long? _lastLogScrollGestureId;
    private string? _lastLogScrollGestureDirection;
    private bool _logDirectManipulation;
    private bool _logScrollbarPointerDown;
    private bool _pendingLogUpwardIntent;
    private bool _logTailChangeViewPending;
    private readonly LogPresentationState _logPresentation = new();
    private readonly Dictionary<LogRowKey, RenderedLogRow> _logRowCache = [];
    private List<RenderedLogRow> _renderedLogRows = [];
    private ContentDialog? _aboutDialog;
    private global::Windows.Foundation.IAsyncInfo? _savePickerOperation;
    private bool _allowFinalClose;
    private bool _shutdownDrainCompleted;
    private readonly string _version;
    private readonly string _commit;
    private readonly NativeDiagnostics _diagnostics = NativeDiagnostics.Current;
    private bool _exportingLogs;

    private readonly record struct LogRowKey(string Id, string Raw);
    private sealed record RenderedLogRow(LogRowKey Key, UIElement[] Controls);

    public MainWindow()
    {
        InitializeComponent();
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
        SetConnectionStatus("Disconnected");
        ConnectionButton.IsEnabled = false;
        var assembly = Assembly.GetExecutingAssembly();
        _version = assembly.GetName().Version?.ToString(3) ?? "Unknown";
        var commit = assembly.GetCustomAttributes<AssemblyMetadataAttribute>()
            .FirstOrDefault(item => item.Key == "DobbySourceCommit")?.Value;
        _commit = commit ?? "N/A";
        SourceEditor.TextChanged += (_, _) =>
        {
            if (_lifetime.IsClosing || _updatingSource || SourceEditor.Text == _programmaticSourceText) return;
            _programmaticSourceText = null;
            SourceChanged();
        };
        AppWindow.Closing += MainWindow_Closing;
        Closed += MainWindow_Closed;
        Clipboard.ContentChanged += ClipboardChanged;
        Activated += MainWindow_Activated;
        StartWindowWork(PollSnapshotsAsync);
        StartWindowWork(RefreshSnapshotAsync);
    }

    private void MainWindow_Activated(object sender, WindowActivatedEventArgs args) =>
        EnqueueUiCallback(RefreshClipboard);

    private void MainWindow_Closing(AppWindow sender, AppWindowClosingEventArgs args)
    {
        if (_allowFinalClose) return;
        if (_shutdownDrainCompleted)
        {
            // If the dispatcher rejected the programmatic close request, a
            // later user/system close may safely complete the already-drained
            // shutdown instead of leaving the window permanently inert.
            _allowFinalClose = true;
            return;
        }
        args.Cancel = true;
        if (!_lifetime.BeginClosing()) return;
        _diagnostics.RecordInfo("window.close-requested", "The Windows frontend began draining before close", new
        {
            process_id = Environment.ProcessId,
            pending_work = "draining",
        });
        _pollTimer.Dispose();
        Clipboard.ContentChanged -= ClipboardChanged;
        Activated -= MainWindow_Activated;
        _ = CompleteShutdownBeforeCloseAsync();
    }

    private async Task CompleteShutdownBeforeCloseAsync()
    {
        try
        {
            await _lifetime.DrainBeforeCloseAsync(
                CancelPendingModalOperations,
                () =>
                {
                    _diagnostics.RecordInfo("window.shutdown-drained", "The Windows frontend drained its asynchronous work", new
                    {
                        process_id = Environment.ProcessId,
                    });
                },
                error => _diagnostics.Record(error.ToString(), "window.shutdown.failure"),
                RequestFinalCloseAsync);
        }
        catch (Exception error)
        {
            _diagnostics.Record(error.ToString(), "window.shutdown.failure");
        }
    }

    private Task RequestFinalCloseAsync()
    {
        var completion = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        _shutdownDrainCompleted = true;
        void CloseAfterDrain()
        {
            try
            {
                _allowFinalClose = true;
                Close();
                completion.TrySetResult(true);
            }
            catch (Exception error) { completion.TrySetException(error); }
        }

        if (DispatcherQueue.HasThreadAccess)
        {
            CloseAfterDrain();
        }
        else if (!DispatcherQueue.TryEnqueue(CloseAfterDrain))
        {
            completion.TrySetException(new InvalidOperationException(
                "The Windows dispatcher rejected the final close request after shutdown drain."));
        }
        return completion.Task;
    }

    private void MainWindow_Closed(object sender, WindowEventArgs args)
    {
        AppWindow.Closing -= MainWindow_Closing;
        _debounce?.Dispose();
        _debounce = null;
        _diagnostics.RecordInfo("window.closed", "The Windows frontend window entered its closed state", new
        {
            process_id = Environment.ProcessId,
        });
    }

    private void CancelPendingModalOperations()
    {
        try { _aboutDialog?.Hide(); }
        catch (Exception error) { _diagnostics.Record(error.ToString(), "window.about-dialog.cancel.failure"); }
        var pickerOperation = _savePickerOperation;
        if (pickerOperation is null) return;
        RecordSavePickerOperationState("before-cancel", pickerOperation);
        try
        {
            pickerOperation.Cancel();
            RecordSavePickerOperationState("after-cancel", pickerOperation);
        }
        catch (Exception error) { _diagnostics.Record(error.ToString(), "window.save-picker.cancel.failure"); }
    }

    private void RecordSavePickerOperationState(string phase, global::Windows.Foundation.IAsyncInfo operation)
    {
        try
        {
            _diagnostics.RecordInfo("window.file-picker-operation-state", "Observed the system file picker operation", new
            {
                process_id = Environment.ProcessId,
                phase,
                operation_id = operation.Id,
                status = operation.Status.ToString(),
                error_code = operation.ErrorCode?.ToString(),
            });
        }
        catch (Exception error) { _diagnostics.Record(error.ToString(), "window.save-picker.state.failure"); }
    }

    private bool StartWindowWork(Func<CancellationToken, Task> operation) =>
        _lifetime.TryRun(operation, ReportWindowWorkFailure);

    private void ReportWindowWorkFailure(Exception error)
    {
        _diagnostics.Record(error.ToString(), "window.async.failure");
        EnqueueUiCallback(() => ShowError(error.ToString(), "Could not complete the request. Check your configuration and logs."));
    }

    private bool EnqueueUiCallback(Action callback, Microsoft.UI.Dispatching.DispatcherQueuePriority priority =
        Microsoft.UI.Dispatching.DispatcherQueuePriority.Normal)
    {
        if (_lifetime.IsClosing) return false;
        return DispatcherQueue.TryEnqueue(priority, () => _lifetime.TryRunCallback(callback));
    }

    internal bool TryActivate() => _lifetime.TryRunCallback(Activate);

    private void UpdateContentLayout()
    {
        if (_lifetime.IsClosing) return;
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

    private async Task PollSnapshotsAsync(CancellationToken cancellationToken)
    {
        try
        {
            while (await _pollTimer.WaitForNextTickAsync(cancellationToken))
            {
                await RefreshSnapshotAsync(cancellationToken);
                if (cancellationToken.IsCancellationRequested) return;
                await RefreshLogsAsync(cancellationToken);
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested) { }
    }

    private async Task RefreshSnapshotAsync(CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing) return;
        if (_snapshotInFlight) return;
        _snapshotInFlight = true;
        try
        {
            var result = await ReadSnapshotAsync(cancellationToken);
            if (_lifetime.IsClosing) return;
            var previous = _snapshot;
            if (result.SessionId == previous?.SessionId && result.Sequence < Math.Max(previous.Sequence, _acceptedSequence)) return;
            var recoveringFromSnapshotError = previous is null || StatusText.Text == "Error";
            if (result.SessionId != previous?.SessionId) _acceptedSequence = 0;
            else if (_subscriptionLoad.SourceDirty && previous is not null)
            {
                result.Configured = previous.Configured;
                result.SourceUrl = previous.SourceUrl;
                result.Digest = previous.Digest;
                result.Profiles = previous.Profiles;
            }
            _snapshot = result;
            SetConnectionStatus(result.Recovering ? "Reconnecting" : result.State switch
            {
                "CONNECTED" => "Connected",
                "PROBING" or "PREPARING" => "Connecting",
                "STOPPING" => "Stopping",
                "FAILED" => "Failed",
                _ => "Disconnected"
            });
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
            if (!_subscriptionLoad.SourceDirty && SourceEditor.FocusState == FocusState.Unfocused &&
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
            if (!result.Configured && !_subscriptionLoad.SourceDirty && SourceEditor.Text.Length > 0 && _restoredLoad != restoreKey)
            {
                _restoredLoad = restoreKey;
                await SourceChangedAsync(SubscriptionInputKind.Restore, cancellationToken);
            }
            await LoadNextAsync(cancellationToken);
            if (_lifetime.IsClosing) return;
            if (!string.IsNullOrEmpty(result.SourceError)) ShowError(result.SourceError, "Check the subscription URL or configuration. See logs for details.", "source.failure");
            else
            {
                _diagnostics.Record("", "source.failure");
                if (recoveringFromSnapshotError) ErrorText.Text = string.Empty;
            }
        }
        catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
        {
            // Window shutdown cancels the in-flight Snapshot request. Do not
            // report this expected lifecycle cancellation as a UI failure.
        }
        catch (Exception error) when (!_lifetime.IsClosing)
        {
            _snapshot = null;
            ShowError(error.ToString(), _snapshot is null ? "VPN service is unavailable. See logs for details." : "Could not complete the request. Check your configuration and logs.");
            SetConnectionStatus("Error");
            SetConnectionAction("Connect");
            ConnectionButton.IsEnabled = false;
        }
        finally
        {
            _snapshotInFlight = false;
        }
    }

    private async Task<Snapshot> ReadSnapshotAsync(CancellationToken cancellationToken)
    {
        var sessionId = _snapshot?.SessionId ?? "";
        try
        {
            return await CallAsync<Snapshot>("Snapshot", new { session_id = sessionId }, cancellationToken);
        }
        catch (BackendCommandException error) when (error.Code == "NOT_FOUND" && !string.IsNullOrEmpty(sessionId))
        {
            // The Go backend owns session state. A replacement backend has a
            // new session ID, so reattach through its owner-independent
            // Snapshot entry point and let it restore the saved configuration URL.
            return await CallAsync<Snapshot>("Snapshot", new { session_id = "" }, cancellationToken);
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
        return _subscriptionLoad.CanAct(
            isStopTarget: IsStopTarget(index),
            stopInFlight: _stopInFlight,
            busy: _busy,
            hasLoadError: _loadError.Length > 0,
            sequenceCurrent: s.Sequence >= _acceptedSequence,
            configured: s.Configured,
            canStartOrSwitch: s.PrimaryAction == "START" || s.CanSwitch);
    }

    private string ActionTitle(int? index) => IsStopTarget(index)
        ? (_snapshot?.State == "CONNECTED" && _snapshot.PendingTarget is null ? "Disconnect" : "Stop")
        : (index is null ? "Auto connect" : "Connect");

    private void RenderActions()
    {
        if (_lifetime.IsClosing) return;
        SetConnectionAction(ActionTitle(null));
        ConnectionButton.IsEnabled = CanAct(null);
        if (_snapshot is not { } current) return;
        var actionsKey = JsonSerializer.Serialize(new
        {
            current.Sequence,
            _busy,
            _stopInFlight,
            _subscriptionLoad.SourceDirty,
            _subscriptionLoad.IsLoading,
            _subscriptionLoad.Revision,
            _loadError,
            current.Digest,
        });
        if (actionsKey == _actionsKey) return;
        _actionsKey = actionsKey;
        ProfileList.Children.Clear();
        ActiveStopButton.Visibility = current.PrimaryAction == "STOP" && !IsStopTarget(null) && !current.Profiles.Any(profile => IsStopTarget(profile.Index)) ? Visibility.Visible : Visibility.Collapsed;
        ActiveStopButton.Content = current.State == "CONNECTED" ? "Disconnect" : "Stop";
        ActiveStopButton.IsEnabled = !_stopInFlight;
        foreach (var profile in current.Profiles)
        {
            var row = new Grid { ColumnSpacing = 0 };
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            var descriptionText = profile.Description.Length == 0 ? $"Profile {profile.Index + 1}" : profile.Description;
            var description = new TextBlock
            {
                Text = descriptionText,
                TextWrapping = TextWrapping.Wrap
            };
            AutomationProperties.SetAutomationId(description, ProfileAutomationIds.Create(current.Digest, profile.Index, "description"));
            row.Children.Add(description);
            var separator = new TextBlock { Text = " · " };
            Grid.SetColumn(separator, 1);
            row.Children.Add(separator);
            var protocol = new TextBlock { Text = profile.Protocol, TextWrapping = TextWrapping.Wrap };
            AutomationProperties.SetAutomationId(protocol, ProfileAutomationIds.Create(current.Digest, profile.Index, "protocol"));
            Grid.SetColumn(protocol, 2);
            row.Children.Add(protocol);
            var button = new Button
            {
                Content = ActionTitle(profile.Index),
                IsEnabled = CanAct(profile.Index),
                Margin = new Thickness(12, 0, 0, 0)
            };
            AutomationProperties.SetAutomationId(button, ProfileAutomationIds.Create(current.Digest, profile.Index, "action"));
            button.Click += (_, _) => StartWindowWork(token => PerformActionAsync(profile.Index, false, token));
            Grid.SetColumn(button, 3);
            row.Children.Add(button);
            ProfileList.Children.Add(row);
        }
    }

    private void ConnectionButton_Click(object sender, RoutedEventArgs e) =>
        StartWindowWork(token => PerformActionAsync(null, false, token));

    private void ActiveStop_Click(object sender, RoutedEventArgs e) =>
        StartWindowWork(token => PerformActionAsync(null, true, token));

    private async Task PerformActionAsync(int? index, bool forceStop, CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing) return;
        if (_snapshot is not { } current) return;
        if (forceStop || IsStopTarget(index))
        {
            if (_stopInFlight || current.PrimaryAction != "STOP") return;
            _stopInFlight = true;
            RenderActions();
            ErrorText.Text = "";
            try
            {
                await CallAsync<JsonElement>("Stop", new { session_id = current.SessionId, generation = current.Generation }, cancellationToken);
            }
            catch (Exception error) when (!_lifetime.IsClosing) { ShowError(error.ToString(), error.Message); }
            finally
            {
                _stopInFlight = false;
                if (!_lifetime.IsClosing)
                {
                    await RefreshSnapshotAsync(cancellationToken);
                    RenderActions();
                }
            }
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
            }, cancellationToken);
        }
        catch (Exception error) when (!_lifetime.IsClosing) { ShowError(error.ToString(), error.Message); }
        finally
        {
            _busy = false;
            if (!_lifetime.IsClosing)
            {
                await RefreshSnapshotAsync(cancellationToken);
                RenderActions();
            }
        }
    }

    private static bool ValidSubscription(string source) => Uri.TryCreate(source, UriKind.Absolute, out var uri) && uri.Scheme == "https" && uri.Host.Length > 0;

    private void SourceChanged(SubscriptionInputKind kind = SubscriptionInputKind.Typed) =>
        StartWindowWork(token => SourceChangedAsync(kind, token));

    private async Task SourceChangedAsync(SubscriptionInputKind kind, CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing) return;
        var source = SourceEditor.Text.Trim();
        var valid = ValidSubscription(source);
        var revision = _subscriptionLoad.Edit(source, valid, kind, LoadNow);
        _loadError = "";
        ErrorText.Text = "";
        RetryButton.Visibility = Visibility.Collapsed;
        _debounce?.Cancel();
        _debounce?.Dispose();
        var debounce = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        _debounce = debounce;
        var cancellation = debounce.Token;
        RenderActions();
        if (!valid) { LoadStatus.Text = "Enter an HTTPS subscription URL"; return; }
        try
        {
            if (kind == SubscriptionInputKind.Typed)
                await Task.Delay(SubscriptionLoadState.DebounceMilliseconds, cancellation);
            cancellation.ThrowIfCancellationRequested();
            if (revision != _subscriptionLoad.Revision) return;
            await LoadNextAsync(cancellationToken);
        }
        catch (OperationCanceledException) { }
        finally
        {
            if (ReferenceEquals(_debounce, debounce)) _debounce = null;
            debounce.Dispose();
        }
    }

    private void ClipboardChanged(object? sender, object e) => EnqueueUiCallback(RefreshClipboard);
    private void RefreshClipboard()
    {
        if (_lifetime.IsClosing) return;
        try { PasteButton.Visibility = Clipboard.GetContent().Contains(StandardDataFormats.Text) ? Visibility.Visible : Visibility.Collapsed; }
        catch (Exception error) { RecordError(error.ToString()); PasteButton.Visibility = Visibility.Collapsed; }
    }
    private void Paste_Click(object sender, RoutedEventArgs e) => StartWindowWork(PasteAsync);

    private async Task PasteAsync(CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing) return;
        _diagnostics.RecordInfo("subscription.paste-click", "Started handling a Paste button click", new { session_id = _snapshot?.SessionId });
        try
        {
            var pastedText = (await Clipboard.GetContent().GetTextAsync()).Trim();
            if (cancellationToken.IsCancellationRequested) return;
            _diagnostics.RecordInfo("subscription.paste-clipboard-text-returned", "Clipboard text returned to the Paste handler", new { session_id = _snapshot?.SessionId, text_length = pastedText.Length });
            ImportSubscription(pastedText, SubscriptionInputKind.Paste);
        }
        catch (Exception error) when (!_lifetime.IsClosing) { ShowError(error.ToString(), error.Message); }
    }
    private void ImportSubscription(string source, SubscriptionInputKind kind)
    {
        if (!ValidSubscription(source)) throw new InvalidOperationException("Enter an HTTPS subscription URL with a host");
        if (SourceEditor.Text == source && (_subscriptionLoad.IsLoading || !_subscriptionLoad.SourceDirty && _snapshot?.Configured == true)) return;
        SetSourceText(source);
        SourceChanged(kind);
    }
    public void ImportLink(string value)
    {
        if (_lifetime.IsClosing) return;
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
            ImportSubscription(Uri.UnescapeDataString(pair[1]), SubscriptionInputKind.Import);
        }
        catch (Exception error) { ShowError(error.ToString(), "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL"); }
    }

    private void Retry_Click(object sender, RoutedEventArgs e) => SourceChanged(SubscriptionInputKind.Retry);

    private TimeSpan LoadNow => Stopwatch.GetElapsedTime(_loadClockOrigin);

    private async Task LoadNextAsync(CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested) return;
        if (_snapshot is null) return;
        var request = _subscriptionLoad.BeginReady(LoadNow);
        if (request is null) return;
        await ExecuteLoadAsync(request, cancellationToken);
    }

    private async Task ExecuteLoadAsync(SubscriptionLoadState.Request request, CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing) return;
        var source = request.Source;
        LoadStatus.Text = "Loading profiles…";
        RenderActions();
        try
        {
            var snapshotBeforeConfigure = _snapshot;
            _diagnostics.RecordInfo("subscription.configure-preflight-snapshot-start", "Started the preflight Snapshot before Configure", new { source, session_id = snapshotBeforeConfigure?.SessionId });
            var current = await ReadSnapshotAsync(cancellationToken);
            if (_lifetime.IsClosing) return;
            _diagnostics.RecordInfo("subscription.configure-preflight-snapshot-end", "Completed the preflight Snapshot before Configure", new { source, session_id = current.SessionId, sequence = current.Sequence });
            _diagnostics.RecordInfo(
                "subscription.configure-submit",
                "Submitting a subscription URL to the Go backend",
                new { source, session_id = current.SessionId, expected_sequence = current.Sequence });
            var configured = await CallAsync<JsonElement>("Configure", new { session_id = current.SessionId, expected_sequence = current.Sequence, source }, cancellationToken);
            if (_lifetime.IsClosing) return;
            if (_subscriptionLoad.MarkAccepted(request))
            {
                _acceptedSequence = configured.GetProperty("sequence").GetInt64();
                MarkSourceAccepted(source);
                LoadStatus.Text = "";
            }
        }
        catch (Exception error) when (!_lifetime.IsClosing)
        {
            if (_subscriptionLoad.IsCurrent(request))
            {
                _loadError = error.Message;
                LoadStatus.Text = _loadError;
                RetryButton.Visibility = Visibility.Visible;
                RecordError(error.ToString());
            }
        }
        finally
        {
            SubscriptionLoadState.Request? next = null;
            try
            {
                if (!_lifetime.IsClosing) await RefreshSnapshotAsync(cancellationToken);
            }
            finally
            {
                next = _subscriptionLoad.Finish(request, LoadNow);
                if (!_lifetime.IsClosing) RenderActions();
            }
            if (!_lifetime.IsClosing && next is not null) await ExecuteLoadAsync(next, cancellationToken);
        }
        if (!_lifetime.IsClosing) await LoadNextAsync(cancellationToken);
    }

    private async Task<T> CallAsync<T>(string method, object parameters, CancellationToken cancellationToken)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
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
        _renderedSource = source;
    }

    private void SetConnectionAction(string value)
    {
        ConnectionButton.Content = value;
        AutomationProperties.SetName(ConnectionButton, value);
    }

    private static string NormalizeSource(string value) =>
        value.Replace("\r\n", "\n").Replace("\r", "\n");

    private void About_Click(object sender, RoutedEventArgs e) => StartWindowWork(ShowAboutAsync);

    private async Task ShowAboutAsync(CancellationToken cancellationToken)
    {
        if (cancellationToken.IsCancellationRequested) return;
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
        var dialog = new ContentDialog
        {
            Title = "About DobbyVPN", Content = details, CloseButtonText = "Done", XamlRoot = Root.XamlRoot,
        };
        _aboutDialog = dialog;
        try { await dialog.ShowAsync(); }
        finally
        {
            if (ReferenceEquals(_aboutDialog, dialog)) _aboutDialog = null;
        }
    }

    private void RecordError(string message) => _diagnostics.Record(message, "ui.failure");

    private void ShowError(string details, string message, string category = "ui.failure")
    {
        if (_lifetime.IsClosing) return;
        _diagnostics.Record(details, category);
        ErrorText.Text = message;
    }

    private void SetConnectionStatus(string value)
    {
        if (_lifetime.IsClosing) return;
        StatusText.Text = value;
        AutomationProperties.SetAutomationId(StatusText, "connection.status");
        AutomationProperties.SetName(StatusText, value);
    }

    private void LogsText_Loaded(object sender, RoutedEventArgs e)
    {
        if (_lifetime.IsClosing) return;
        _logScroll = LogsScroll;
        CaptureLogScrollPosition();
        LogsScroll.SizeChanged += (_, _) =>
        {
            if (!_logPresentation.FollowsTail) ClearPendingLogDownScrollIntent();
            if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
        };
        LogEntries.SizeChanged += (_, _) =>
        {
            if (_logPresentation.FollowsTail && !_clearingLogs)
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
            if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
        }), true);
        LogsScroll.AddHandler(UIElement.PointerCaptureLostEvent, new Microsoft.UI.Xaml.Input.PointerEventHandler((_, args) =>
        {
            if (FindLogVerticalScrollBar(args.OriginalSource) is null) return;
            ObserveLogScrollPosition();
            _logScrollbarPointerDown = false;
            if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
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
            if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
        };
        LogsScroll.ViewChanged += (_, args) =>
        {
            ObserveLogScrollPosition(args.IsIntermediate);
            if (!_updatingLogs && !_logTailChangeViewPending && !_logDirectManipulation &&
                !_logScrollbarPointerDown && _logPresentation.TryRequestSettledTailAlignment(
                    args.IsIntermediate, LogsScroll.VerticalOffset, LogsScroll.ScrollableHeight,
                    _logRenderGeneration))
                QueueLogTailChangeView(_logRenderGeneration);
        };
        StartWindowWork(RefreshLogsAsync);
    }

    private void RegisterLogDownScrollIntent()
    {
        if (_logScroll is null) return;
        var gestureId = BeginLogScrollGesture("down");
        var remainingRange = _logScroll.ScrollableHeight - _logScroll.VerticalOffset;
        if (remainingRange <= 0.5)
        {
            ClearPendingLogDownScrollIntent();
            _pendingLogUpwardIntent = false;
            var wasFollowingTail = _logPresentation.FollowsTail;
            _logPresentation.FollowTail();
            RecordLogScrollGestureIntent(gestureId, "down");
            RecordLogFollowTransition("down-at-bottom", wasFollowingTail, gestureId, "down");
            if (!_clearingLogs) RenderLogs();
            return;
        }
        if (_logPresentation.FollowsTail)
        {
            RecordLogScrollGestureIntent(gestureId, "down");
            return;
        }
        _pendingLogScrollOffset = _logScroll.VerticalOffset;
        _pendingLogDownScroll = true;
        RecordLogScrollGestureIntent(gestureId, "down");
    }

    private void RequestLogFollowFreezeForUpwardIntent()
    {
        if (_logScroll is null) return;
        var gestureId = BeginLogScrollGesture("up");
        ClearPendingLogDownScrollIntent();
        if (_logScroll.ScrollableHeight > 0.5)
        {
            var wasFollowingTail = _logPresentation.FollowsTail;
            _logPresentation.Freeze();
            RecordLogScrollGestureIntent(gestureId, "up");
            RecordLogFollowTransition("upward-scroll-intent", wasFollowingTail, gestureId, "up");
            return;
        }

        if (_updatingLogs || _logTailChangeViewPending)
            _pendingLogUpwardIntent = true;
        RecordLogScrollGestureIntent(gestureId, "up");
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
            ObserveDirectedLogDownScroll(isIntermediate);
            if (!isIntermediate) ClearPendingLogDownScrollIntent();
        }
        if (directManipulation || scrollbarInteraction)
            ObserveLogPresentationForScroll(directManipulation ? "direct-manipulation" : "scrollbar-interaction",
                isIntermediate);
        if (!_updatingLogs && _logPresentation.FollowsTail) RenderLogs();
    }

    private long BeginLogScrollGesture(string direction)
    {
        var gestureId = ++_logScrollGestureSequence;
        _pendingLogScrollGestureId = gestureId;
        _pendingLogScrollGestureDirection = direction;
        _lastLogScrollGestureId = gestureId;
        _lastLogScrollGestureDirection = direction;
        return gestureId;
    }

    private void RecordLogScrollGestureIntent(long gestureId, string direction) =>
        _diagnostics.RecordInfo("logs.scroll-gesture", "Observed an explicit log scroll gesture", new
        {
            gesture_id = gestureId,
            direction,
            state = CaptureLogScrollDiagnosticState(),
        });

    private void ObserveLogPresentationForScroll(string cause, bool isIntermediate,
        long? gestureId = null, string? direction = null)
    {
        var wasFollowingTail = _logPresentation.FollowsTail;
        _logPresentation.ObserveUserScroll(IsLogScrollAtBottom());
        RecordLogFollowTransition(cause, wasFollowingTail, gestureId, direction, isIntermediate);
    }

    private void ObserveDirectedLogDownScroll(bool isIntermediate)
    {
        var wasFollowingTail = _logPresentation.FollowsTail;
        _logPresentation.ObserveDirectedDownScroll(IsLogScrollAtBottom());
        RecordLogFollowTransition("directed-down-scroll", wasFollowingTail,
            _lastLogScrollGestureId, _lastLogScrollGestureDirection, isIntermediate);
    }

    private void RecordLogFollowTransition(string cause, bool wasFollowingTail,
        long? gestureId = null, string? direction = null, bool? isIntermediate = null)
    {
        if (wasFollowingTail == _logPresentation.FollowsTail) return;
        _diagnostics.RecordInfo("logs.follow-state-transition", "Log tail-follow state changed", new
        {
            gesture_id = gestureId,
            direction,
            cause,
            is_intermediate = isIntermediate,
            follows_tail_before = wasFollowingTail,
            follows_tail_after = _logPresentation.FollowsTail,
            state = CaptureLogScrollDiagnosticState(),
        });
    }

    private object CaptureLogScrollDiagnosticState()
    {
        try
        {
            return new
            {
                follows_tail = _logPresentation.FollowsTail,
                vertical_offset = _logScroll?.VerticalOffset,
                scrollable_height = _logScroll?.ScrollableHeight,
                render_generation = _logRenderGeneration,
                updating_logs = _updatingLogs,
                clearing_logs = _clearingLogs,
                direct_manipulation = _logDirectManipulation,
                scrollbar_pointer_down = _logScrollbarPointerDown,
                pending_down_scroll = _pendingLogDownScroll,
                pending_upward_intent = _pendingLogUpwardIntent,
                tail_change_view_pending = _logTailChangeViewPending,
            };
        }
        catch (Exception error)
        {
            _diagnostics.Record(error.ToString(), "logs.scroll-diagnostic-geometry.failure");
            throw;
        }
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

    private void ClearLogs_Click(object sender, RoutedEventArgs e) => StartWindowWork(ClearLogsAsync);

    private async Task ClearLogsAsync(CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested) return;
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
            _pendingLogScrollGestureId = null;
            _pendingLogScrollGestureDirection = null;
            _lastLogScrollGestureId = null;
            _lastLogScrollGestureDirection = null;
            await _diagnostics.ClearViewAsync();
            if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested) return;
            _logPresentation.ClearAfterBoundary();
            _logRowCache.Clear();
            _renderedLogRows = [];
            LogEntries.Children.Clear();
            CaptureLogScrollPosition();
            _clearingLogs = false;
            await RefreshLogsAsync(cancellationToken);
        }
        catch (Exception error) when (!_lifetime.IsClosing) { RecordError(error.ToString()); LogsErrorText.Text = error.Message; }
        finally { _clearingLogs = false; }
    }

    private async Task RefreshLogsAsync(CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested) return;
        try
        {
            if (_clearingLogs) return;
            var revision = ++_logRevision;
            var preview = await _diagnostics.EntriesAsync();
            if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested || revision != _logRevision) return;
            var ingestedBackendEntries = _logPresentation.ReplaceEntries(preview.Entries);
            foreach (var entry in ingestedBackendEntries)
                _diagnostics.RecordInfo("logs.backend-ingested", "Backend log entry ingested", new
                {
                    backend_entry_id = entry.EntryId,
                    follows_tail = entry.FollowsTail,
                });
            LogsErrorText.Text = preview.Error;
            if (_logPresentation.FollowsTail) RenderLogs();
        }
        catch (Exception error) when (!_lifetime.IsClosing) { RecordError(error.ToString()); LogsErrorText.Text = error.Message; }
    }

    private void RenderLogs()
    {
        if (_lifetime.IsClosing) return;
        var entries = _logPresentation.VisibleEntries;
        var keys = entries.Select(entry => new LogRowKey(entry.Id, entry.Raw)).ToArray();
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

        // Keep retained controls attached during refresh so text selection
        // survives appends and preview trimming. Reattach keyed controls only
        // when timestamps reorder streams.
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

        var nextRows = new List<RenderedLogRow>(entries.Count);
        var childIndex = 0;
        for (var rowIndex = 0; rowIndex < entries.Count; rowIndex++)
        {
            var entry = entries[rowIndex];
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
        EnqueueUiCallback(() =>
        {
            if (renderGeneration != _logRenderGeneration) return;
            _updatingLogs = false;
            CaptureLogScrollPosition();
            var latestKeys = _logPresentation.LatestEntries.Select(entry => new LogRowKey(entry.Id, entry.Raw)).ToArray();
            if (_logPresentation.FollowsTail && !latestKeys.SequenceEqual(_renderedLogRows.Select(row => row.Key)))
            {
                RenderLogs();
                return;
            }
            QueueLogTailChangeView(renderGeneration);
        }, Microsoft.UI.Dispatching.DispatcherQueuePriority.Low);
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
        return new RenderedLogRow(key, [text]);
    }

    private void QueueLogTailChangeView(long renderGeneration)
    {
        // Correlate only the first tail-queue attempt for an explicit gesture.
        var gestureId = _pendingLogScrollGestureId;
        var gestureDirection = _pendingLogScrollGestureDirection;
        _pendingLogScrollGestureId = null;
        _pendingLogScrollGestureDirection = null;

        if (_updatingLogs)
        {
            if (gestureId is { } skippedGestureId)
                RecordLogTailGestureObservation(skippedGestureId, gestureDirection, "queue-skipped",
                    renderGeneration, skipReason: "logs-updating");
            return;
        }
        _logTailChangeViewPending = true;
        var queued = EnqueueUiCallback(() =>
        {
            if (gestureId is { } callbackGestureId)
                RecordLogTailGestureObservation(callbackGestureId, gestureDirection, "callback-entered", renderGeneration);

            if (renderGeneration != _logRenderGeneration)
            {
                if (gestureId is { } staleGestureId)
                    RecordLogTailGestureObservation(staleGestureId, gestureDirection, "callback-skipped",
                        renderGeneration, skipReason: "stale-render-generation");
                return;
            }
            if (_clearingLogs || _updatingLogs || _logDirectManipulation || _logScrollbarPointerDown || _logScroll is null)
            {
                if (gestureId is { } callbackSkippedGestureId)
                {
                    var skipReason = _clearingLogs ? "clearing-logs" :
                        _updatingLogs ? "logs-updating" :
                        _logDirectManipulation ? "direct-manipulation" :
                        _logScrollbarPointerDown ? "scrollbar-pointer-down" :
                        "scroll-viewer-unavailable";
                    RecordLogTailGestureObservation(callbackSkippedGestureId, gestureDirection,
                        "callback-skipped", renderGeneration, skipReason: skipReason);
                }
                return;
            }
            _logTailChangeViewPending = false;
            if (_pendingLogUpwardIntent)
            {
                _pendingLogUpwardIntent = false;
                if (_logScroll.ScrollableHeight > 0.5)
                {
                    ClearPendingLogDownScrollIntent();
                    var wasFollowingTailBeforeFreeze = _logPresentation.FollowsTail;
                    _logPresentation.Freeze();
                    RecordLogFollowTransition("tail-callback-upward-intent-freeze",
                        wasFollowingTailBeforeFreeze, gestureId, gestureDirection);
                    return;
                }
                ClearPendingLogDownScrollIntent();
                var wasFollowingTailBeforeFollow = _logPresentation.FollowsTail;
                _logPresentation.FollowTail();
                RecordLogFollowTransition("tail-callback-upward-intent-follow",
                    wasFollowingTailBeforeFollow, gestureId, gestureDirection);
            }
            if (!_logPresentation.FollowsTail)
            {
                if (gestureId is { } frozenGestureId)
                    RecordLogTailGestureObservation(frozenGestureId, gestureDirection, "callback-skipped",
                        renderGeneration, skipReason: "tail-follow-frozen");
                return;
            }
            var targetOffset = _logScroll.ScrollableHeight;
            var changeViewResult = _logScroll.ChangeView(null, targetOffset, null, true);
            CaptureLogScrollPosition();
            if (gestureId is { } alignedGestureId)
                RecordLogTailGestureObservation(alignedGestureId, gestureDirection, "change-view-returned",
                    renderGeneration, targetOffset: targetOffset, changeViewResult: changeViewResult);
        }, Microsoft.UI.Dispatching.DispatcherQueuePriority.Low);
        if (gestureId is { } queuedGestureId)
            RecordLogTailGestureObservation(queuedGestureId, gestureDirection, "queue-result",
                renderGeneration, queueAccepted: queued);
    }

    private void RecordLogTailGestureObservation(long gestureId, string? direction, string stage,
        long renderGeneration, bool? queueAccepted = null, string? skipReason = null,
        double? targetOffset = null, bool? changeViewResult = null) =>
        _diagnostics.RecordInfo("logs.tail-gesture", "Observed the first tail alignment attempt for an explicit scroll gesture", new
        {
            gesture_id = gestureId,
            direction,
            stage,
            queue_accepted = queueAccepted,
            skip_reason = skipReason,
            render_generation = renderGeneration,
            target_offset = targetOffset,
            change_view_result = changeViewResult,
            state = CaptureLogScrollDiagnosticState(),
        });

    private string ExportHeader =>
        $"DobbyVPN {_version}\nSource commit: {_commit}\nPlatform: {Environment.OSVersion}\nCaptured: {DateTimeOffset.UtcNow:O}\n\n";

    private void SaveLogs_Click(object sender, RoutedEventArgs e) => StartWindowWork(SaveLogsAsync);

    private async Task SaveLogsAsync(CancellationToken cancellationToken)
    {
        if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested) return;
        if (_exportingLogs) return;
        _exportingLogs = true;
        global::Windows.Foundation.IAsyncInfo? pickerOperation = null;
        try
        {
            var picker = new FileSavePicker(AppWindow.Id) { SuggestedFileName = "DobbyVPN-logs" };
            picker.FileTypeChoices.Add("Compressed diagnostics", new List<string> { ".gz" });
            var operation = picker.PickSaveFileAsync();
            pickerOperation = operation;
            _savePickerOperation = operation;
            RecordSavePickerOperationState("opened", operation);
            _diagnostics.RecordInfo("window.file-picker-open", "Opened the system file picker for log export", new
            {
                process_id = Environment.ProcessId,
            });
            var file = await operation.AsTask(cancellationToken);
            RecordSavePickerOperationState("await-returned", operation);
            _diagnostics.RecordInfo("window.file-picker-returned", "The system file picker returned to the log export", new
            {
                process_id = Environment.ProcessId,
                selected = file is not null,
            });
            if (_lifetime.IsClosing || cancellationToken.IsCancellationRequested) return;
            if (file is not null)
            {
                await _diagnostics.SaveAsync(file.Path, ExportHeader);
            }
        }
        catch (Exception error) when (!_lifetime.IsClosing) { RecordError(error.ToString()); LogsErrorText.Text = "Logs could not be saved. Try another location."; }
        finally
        {
            if (pickerOperation is not null)
                RecordSavePickerOperationState("await-finally", pickerOperation);
            _savePickerOperation = null;
            _exportingLogs = false;
        }
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
