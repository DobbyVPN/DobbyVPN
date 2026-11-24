using System.Diagnostics;
using System.Globalization;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace DobbyVPN.Windows;

internal sealed class LogViewport(ScrollViewer scroll, StackPanel rows, MenuFlyout menu,
    ResourceDictionary resources, NativeDiagnostics diagnostics)
{
    private bool _attached;
    private readonly List<(RoutedEvent Event, Delegate Handler)> _inputHandlers = [];
    private long _logRevision;
    private bool _clearingLogs;
    private bool _updatingLogs;
    private long _logRenderGeneration;
    private long _logTailQueueSequence;
    private double _lastLogScrollOffset;
    private double _pendingLogScrollOffset;
    private bool _pendingLogDownScroll;
    private long _logScrollGestureSequence;
    private readonly record struct ScrollGesture(long Id, string Direction);
    private ScrollGesture? _pendingGesture;
    private ScrollGesture? _lastGesture;
    private bool _logDirectManipulation;
    private bool _logScrollbarPointerDown;
    private long _logScrollbarPressSequence;
    private int _logScrollbarViewObservationsRemaining;
    private int _logScrollbarMoveObservationsRemaining;
    private (string Phase, double CaptureMs, double WriteMs)? _lastLogScrollbarTiming;
    private int _logPointerEntryObservations;
    private bool _pendingLogUpwardIntent;
    private long? _pendingTailQueue;
    private readonly LogPresentationState _logPresentation = new();
    private readonly Dictionary<LogRowKey, RichTextBlock> _rowCache = [];
    private LogRowKey[] _renderedKeys = [];
    private readonly record struct LogRowKey(string Id, string Raw);

    internal void Attach()
    {
        if (_attached) return;
        _attached = true;
        CaptureLogScrollPosition();
        try
        {
            diagnostics.RecordDebug("logs.scroll-loaded", "Log scroll viewport loaded", new
            {
                state = CaptureLogScrollDiagnosticState(),
            });
        }
        catch (Exception error)
        {
            diagnostics.Record(error.ToString(), "logs.scroll-loaded.failure");
        }
        Listen(UIElement.PointerEnteredEvent, new PointerEventHandler((_, args) =>
        {
            // Bound this observation even if layout changes repeatedly move the
            // pointer across descendants while log rows are refreshed.
            if (_logPointerEntryObservations >= 8) return;
            _logPointerEntryObservations++;
            try
            {
                var position = args.GetCurrentPoint(scroll).Position;
                diagnostics.RecordDebug("logs.pointer-entry", "Pointer entered the log viewport", new
                {
                    observation = _logPointerEntryObservations,
                    pointer_device = args.Pointer.PointerDeviceType.ToString(),
                    source_type = args.OriginalSource?.GetType().FullName,
                    x = position.X,
                    y = position.Y,
                    state = CaptureLogScrollDiagnosticState(),
                });
            }
            catch (Exception error)
            {
                diagnostics.Record(error.ToString(), "logs.pointer-entry.failure");
            }
        }));
        // Observe context input and popup lifetime without changing the native
        // ContextFlyout routing. This distinguishes input that never opens a
        // menu from a menu that opens and closes during a later log update.
        Listen(UIElement.RightTappedEvent, new RightTappedEventHandler((_, args) =>
        {
            try
            {
                var position = args.GetPosition(scroll);
                RecordLogMenuObservation("right-tap", new
                {
                    source_type = args.OriginalSource?.GetType().FullName,
                    handled = args.Handled,
                    x = position.X,
                    y = position.Y,
                });
            }
            catch (Exception error) { diagnostics.Record(error.ToString(), "logs.menu-observation.failure"); }
        }));
        menu.Opening += OnMenuOpening;
        menu.Opened += OnMenuOpened;
        menu.Closing += OnMenuClosing;
        menu.Closed += OnMenuClosed;
        Listen(UIElement.DoubleTappedEvent, new DoubleTappedEventHandler((_, args) =>
        {
            var scrollbarSource = FindLogVerticalScrollBar(args.OriginalSource) is not null;
            if (!scrollbarSource)
            {
                args.Handled = true;
                menu.ShowAt(scroll, new FlyoutShowOptions { Position = args.GetPosition(scroll) });
            }
            diagnostics.RecordDebug("logs.double-tap-dispatch", "Dispatched a log viewport double tap", new
            {
                source_type = args.OriginalSource?.GetType().FullName,
                scrollbar_source = scrollbarSource,
                action = scrollbarSource ? "ignored-scrollbar" : "opened",
            });
        }));
        scroll.SizeChanged += OnScrollSizeChanged;
        rows.SizeChanged += OnRowsSizeChanged;
        Listen(UIElement.PointerWheelChangedEvent, new PointerEventHandler((_, args) =>
        {
            var properties = args.GetCurrentPoint(scroll).Properties;
            if (properties.IsHorizontalMouseWheel || properties.MouseWheelDelta == 0) return;
            if (properties.MouseWheelDelta > 0) RequestLogFollowFreezeForUpwardIntent();
            else RegisterLogDownScrollIntent();
        }));
        Listen(UIElement.KeyDownEvent, new KeyEventHandler((_, args) =>
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
                case global::Windows.System.VirtualKey.End:
                    RegisterLogDownScrollIntent();
                    break;
            }
        }));
        Listen(UIElement.PointerPressedEvent, new PointerEventHandler((_, args) =>
        {
            if (FindLogVerticalScrollBar(args.OriginalSource) is not null)
            {
                _pendingLogDownScroll = false;
                _logScrollbarPointerDown = true;
                _logScrollbarPressSequence++;
                _lastLogScrollbarTiming = null;
                _logScrollbarViewObservationsRemaining = 12;
                _logScrollbarMoveObservationsRemaining = 6;
                RecordLogScrollbarObservation("press", pointer: args);
            }
        }));
        Listen(UIElement.PointerReleasedEvent, new PointerEventHandler((_, args) => EndScrollbarInput("release", args)));
        Listen(UIElement.PointerCaptureLostEvent, new PointerEventHandler((_, args) => EndScrollbarInput("capture-lost", args)));
        Listen(UIElement.PointerMovedEvent, new PointerEventHandler((_, args) =>
        {
            if (!_logScrollbarPointerDown || _logScrollbarMoveObservationsRemaining <= 0) return;
            _logScrollbarMoveObservationsRemaining--;
            RecordLogScrollbarObservation("move", pointer: args);
        }));
        scroll.DirectManipulationStarted += OnScrollDirectManipulationStarted;
        scroll.DirectManipulationCompleted += OnScrollDirectManipulationCompleted;
        scroll.ViewChanged += OnScrollViewChanged;
        RenderLogs();
        if (!_updatingLogs && _logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
    }

    private void Listen(RoutedEvent routedEvent, Delegate handler)
    {
        scroll.AddHandler(routedEvent, handler, true);
        _inputHandlers.Add((routedEvent, handler));
    }

    internal void Detach()
    {
        if (!_attached) return;
        _attached = false;
        ++_logRevision;
        InvalidatePendingRender();
        _logDirectManipulation = false;
        _logScrollbarPointerDown = false;
        _logScrollbarMoveObservationsRemaining = _logScrollbarViewObservationsRemaining = 0;
        foreach (var (routedEvent, handler) in _inputHandlers)
            scroll.RemoveHandler(routedEvent, handler);
        _inputHandlers.Clear();
        menu.Opening -= OnMenuOpening;
        menu.Opened -= OnMenuOpened;
        menu.Closing -= OnMenuClosing;
        menu.Closed -= OnMenuClosed;
        scroll.SizeChanged -= OnScrollSizeChanged;
        rows.SizeChanged -= OnRowsSizeChanged;
        scroll.DirectManipulationStarted -= OnScrollDirectManipulationStarted;
        scroll.DirectManipulationCompleted -= OnScrollDirectManipulationCompleted;
        scroll.ViewChanged -= OnScrollViewChanged;
    }

    private void InvalidatePendingRender()
    {
        ++_logRenderGeneration;
        _updatingLogs = false;
        _pendingTailQueue = null;
        _pendingLogUpwardIntent = false;
        _pendingLogDownScroll = false;
        _pendingGesture = null;
        _lastGesture = null;
    }

    private void EndScrollbarInput(string phase, PointerRoutedEventArgs args)
    {
        if (FindLogVerticalScrollBar(args.OriginalSource) is null) return;
        RecordLogScrollbarObservation(phase, pointer: args);
        ObserveLogScrollPosition();
        _logScrollbarPointerDown = false;
        if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
    }

    private void OnScrollSizeChanged(object? sender, SizeChangedEventArgs args)
    {
        if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
        else _pendingLogDownScroll = false;
    }

    private void OnRowsSizeChanged(object? sender, SizeChangedEventArgs args)
    {
        if (_logPresentation.FollowsTail && !_clearingLogs)
            QueueLogTailChangeView(_logRenderGeneration);
    }

    private void OnScrollDirectManipulationStarted(object? sender, object args)
    {
        _pendingLogDownScroll = false;
        _logDirectManipulation = true;
    }

    private void OnScrollDirectManipulationCompleted(object? sender, object args)
    {
        ObserveLogScrollPosition();
        _logDirectManipulation = false;
        if (_logPresentation.FollowsTail) QueueLogTailChangeView(_logRenderGeneration);
    }

    private void OnScrollViewChanged(object? sender, ScrollViewerViewChangedEventArgs args)
    {
        if (_logScrollbarViewObservationsRemaining > 0)
        {
            _logScrollbarViewObservationsRemaining--;
            RecordLogScrollbarObservation("view-changed", args.IsIntermediate);
        }
        ObserveLogScrollPosition(args.IsIntermediate);
        if (!_updatingLogs && !_pendingTailQueue.HasValue && !_logDirectManipulation &&
            !_logScrollbarPointerDown && _logPresentation.TryRequestSettledTailAlignment(
                args.IsIntermediate, scroll.VerticalOffset, scroll.ScrollableHeight,
                _logRenderGeneration))
            QueueLogTailChangeView(_logRenderGeneration);
    }

    private void OnMenuOpening(object? sender, object args) => RecordLogMenuObservation("opening");

    private void OnMenuOpened(object? sender, object args) => RecordLogMenuObservation("opened");

    private void OnMenuClosing(object? sender, object args) => RecordLogMenuObservation("closing");

    private void OnMenuClosed(object? sender, object args) => RecordLogMenuObservation("closed");

    private void RecordLogScrollbarObservation(string phase, bool? isIntermediate = null,
        PointerRoutedEventArgs? pointer = null)
    {
        try
        {
            var captureStarted = Stopwatch.GetTimestamp();
            double? offsetDelta = phase == "view-changed" ? scroll.VerticalOffset - _lastLogScrollOffset : null;
            var pointerState = pointer is null ? null : CaptureLogScrollbarPointer(pointer);
            var state = CaptureLogScrollDiagnosticState();
            var captureMs = Stopwatch.GetElapsedTime(captureStarted).TotalMilliseconds;
            var previous = _lastLogScrollbarTiming;
            var fields = new
            {
                sequence = _logScrollbarPressSequence,
                phase,
                is_intermediate = isIntermediate,
                offset_delta = offsetDelta,
                view_observations_remaining = _logScrollbarViewObservationsRemaining,
                move_observations_remaining = _logScrollbarMoveObservationsRemaining,
                pointer = pointerState,
                state,
                capture_ms = captureMs,
                previous_timing = previous is { } timing ? new
                {
                    phase = timing.Phase,
                    capture_ms = timing.CaptureMs,
                    write_duration_ms = timing.WriteMs,
                } : null,
            };
            var writeStarted = Stopwatch.GetTimestamp();
            diagnostics.RecordDebug("logs.scrollbar-observation", "Observed log scrollbar interaction", fields);
            _lastLogScrollbarTiming = (phase, captureMs, Stopwatch.GetElapsedTime(writeStarted).TotalMilliseconds);
        }
        catch (Exception error)
        {
            diagnostics.Record(error.ToString(), "logs.scrollbar-observation.failure");
        }
    }

    private object CaptureLogScrollbarPointer(PointerRoutedEventArgs args)
    {
        var point = args.GetCurrentPoint(scroll);
        var ancestry = new List<object>();
        var current = args.OriginalSource as DependencyObject;
        while (current is not null && ancestry.Count < 8)
        {
            ancestry.Add(new { type = current.GetType().FullName, automation_id = AutomationProperties.GetAutomationId(current) });
            current = VisualTreeHelper.GetParent(current);
        }
        return new
        {
            pointer_id = args.Pointer.PointerId,
            pointer_device = args.Pointer.PointerDeviceType.ToString(),
            x = point.Position.X,
            y = point.Position.Y,
            in_contact = point.IsInContact,
            left_button_pressed = point.Properties.IsLeftButtonPressed,
            source_ancestry = ancestry,
        };
    }

    private void RegisterLogDownScrollIntent()
    {
        var gesture = BeginLogScrollGesture("down");
        var remainingRange = scroll.ScrollableHeight - scroll.VerticalOffset;
        if (remainingRange <= 0.5)
        {
            _pendingLogDownScroll = false;
            _pendingLogUpwardIntent = false;
            var wasFollowingTail = _logPresentation.FollowsTail;
            _logPresentation.FollowTail();
            RecordLogScrollGestureIntent(gesture);
            RecordLogFollowTransition("down-at-bottom", wasFollowingTail, gesture);
            if (!_clearingLogs) RenderLogs();
            return;
        }
        if (_logPresentation.FollowsTail)
        {
            RecordLogScrollGestureIntent(gesture);
            return;
        }
        _pendingLogScrollOffset = scroll.VerticalOffset;
        _pendingLogDownScroll = true;
        RecordLogScrollGestureIntent(gesture);
    }

    private void RequestLogFollowFreezeForUpwardIntent()
    {
        var gesture = BeginLogScrollGesture("up");
        _pendingLogDownScroll = false;
        if (scroll.ScrollableHeight > 0.5)
        {
            var wasFollowingTail = _logPresentation.FollowsTail;
            _logPresentation.Freeze();
            RecordLogScrollGestureIntent(gesture);
            RecordLogFollowTransition("upward-scroll-intent", wasFollowingTail, gesture);
            return;
        }

        if (_updatingLogs || _pendingTailQueue.HasValue)
            _pendingLogUpwardIntent = true;
        RecordLogScrollGestureIntent(gesture);
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
        var currentOffset = scroll.VerticalOffset;
        var offsetDelta = currentOffset - _lastLogScrollOffset;
        CaptureLogScrollPosition();
        if (Math.Abs(offsetDelta) <= 0.5)
        {
            if (!isIntermediate && !_updatingLogs && !_pendingTailQueue.HasValue)
                _pendingLogDownScroll = false;
            return;
        }

        var directManipulation = _logDirectManipulation;
        var scrollbarInteraction = _logScrollbarPointerDown;
        var directedInput = _pendingLogDownScroll && currentOffset - _pendingLogScrollOffset > 0.5;
        if (!directManipulation && !scrollbarInteraction && !directedInput) return;

        if (directManipulation || scrollbarInteraction)
        {
            _pendingLogDownScroll = false;
            ObserveLogPresentationForScroll(directManipulation ? "direct-manipulation" : "scrollbar-interaction",
                isIntermediate, movedTowardEarlierEntries: offsetDelta < 0);
        }
        else
        {
            ObserveDirectedLogDownScroll(isIntermediate);
            if (!isIntermediate) _pendingLogDownScroll = false;
        }
        if (!_updatingLogs && _logPresentation.FollowsTail) RenderLogs();
    }

    private ScrollGesture BeginLogScrollGesture(string direction)
    {
        var gesture = new ScrollGesture(++_logScrollGestureSequence, direction);
        _pendingGesture = _lastGesture = gesture;
        return gesture;
    }

    private void RecordLogScrollGestureIntent(ScrollGesture gesture) =>
        diagnostics.RecordInfo("logs.scroll-gesture", "Observed an explicit log scroll gesture", new
        {
            gesture_id = gesture.Id,
            direction = gesture.Direction,
            state = CaptureLogScrollDiagnosticState(),
        });

    private void ObserveLogPresentationForScroll(string cause, bool isIntermediate,
        bool movedTowardEarlierEntries)
    {
        var wasFollowingTail = _logPresentation.FollowsTail;
        _logPresentation.ObserveUserScroll(IsLogScrollAtBottom(), movedTowardEarlierEntries);
        RecordLogFollowTransition(cause, wasFollowingTail, isIntermediate: isIntermediate);
    }

    private void ObserveDirectedLogDownScroll(bool isIntermediate)
    {
        var wasFollowingTail = _logPresentation.FollowsTail;
        _logPresentation.ObserveDirectedDownScroll(IsLogScrollAtBottom());
        RecordLogFollowTransition("directed-down-scroll", wasFollowingTail,
            _lastGesture, isIntermediate);
    }

    private void RecordLogFollowTransition(string cause, bool wasFollowingTail,
        ScrollGesture? gesture = null, bool? isIntermediate = null)
    {
        if (wasFollowingTail == _logPresentation.FollowsTail) return;
        diagnostics.RecordInfo("logs.follow-state-transition", "Log tail-follow state changed", new
        {
            gesture_id = gesture?.Id,
            direction = gesture?.Direction,
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
                vertical_offset = scroll.VerticalOffset,
                scrollable_height = scroll.ScrollableHeight,
                requested_vertical_scrollbar_visibility = scroll.VerticalScrollBarVisibility.ToString(),
                computed_vertical_scrollbar_visibility = scroll.ComputedVerticalScrollBarVisibility.ToString(),
                viewport_width = scroll.ViewportWidth,
                viewport_height = scroll.ViewportHeight,
                extent_width = scroll.ExtentWidth,
                extent_height = scroll.ExtentHeight,
                actual_width = scroll.ActualWidth,
                actual_height = scroll.ActualHeight,
                enabled = scroll.IsEnabled,
                hit_test_visible = scroll.IsHitTestVisible,
                zoom_factor = scroll.ZoomFactor,
                render_generation = _logRenderGeneration,
                updating_logs = _updatingLogs,
                clearing_logs = _clearingLogs,
                direct_manipulation = _logDirectManipulation,
                scrollbar_pointer_down = _logScrollbarPointerDown,
                pending_down_scroll = _pendingLogDownScroll,
                pending_upward_intent = _pendingLogUpwardIntent,
                tail_change_view_pending = _pendingTailQueue.HasValue,
            };
        }
        catch (Exception error)
        {
            diagnostics.Record(error.ToString(), "logs.scroll-diagnostic-geometry.failure");
            throw;
        }
    }

    private void CaptureLogScrollPosition()
    {
        _lastLogScrollOffset = scroll.VerticalOffset;
    }

    private bool IsLogScrollAtBottom() => scroll.VerticalOffset >= scroll.ScrollableHeight - 8;

    internal async Task<bool> ClearAsync(CancellationToken cancellationToken)
    {
        if (!_attached || _clearingLogs || cancellationToken.IsCancellationRequested) return false;
        _clearingLogs = true;
        try
        {
            ++_logRevision;
            InvalidatePendingRender();
            await diagnostics.ClearViewAsync();
            _logPresentation.ClearAfterBoundary();
        }
        finally { _clearingLogs = false; }
        if (cancellationToken.IsCancellationRequested) return false;
        // On reload Attach reconciles the old controls with the completed clear.
        RenderLogs();
        return true;
    }

    internal async Task<string?> RefreshAsync(CancellationToken cancellationToken)
    {
        if (!_attached || _clearingLogs || cancellationToken.IsCancellationRequested) return null;
        var revision = ++_logRevision;
        try
        {
            var preview = await diagnostics.EntriesAsync();
            if (revision != _logRevision || cancellationToken.IsCancellationRequested) return null;
            var ingestedBackendEntries = _logPresentation.ReplaceEntries(preview.Entries);
            foreach (var entry in ingestedBackendEntries)
                diagnostics.RecordInfo("logs.backend-ingested", "Backend log entry ingested", new
                {
                    backend_entry_id = entry.EntryId,
                    follows_tail = entry.FollowsTail,
                });
            if (_logPresentation.FollowsTail) RenderLogs();
            return preview.Error;
        }
        catch (Exception error) when (revision != _logRevision || cancellationToken.IsCancellationRequested)
        {
            diagnostics.Record(error.ToString(), "ui.failure");
            return null;
        }
    }

    private void RecordLogMenuObservation(string stage, object? gesture = null)
    {
        try
        {
            var target = menu.Target;
            diagnostics.RecordDebug("logs.menu-" + stage, "Observed log context menu", new
            {
                gesture,
                target_type = target?.GetType().FullName,
                target_id = target is null ? null : AutomationProperties.GetAutomationId(target),
                target_is_viewport = ReferenceEquals(target, scroll),
                is_open = menu.IsOpen,
                rendered_rows = _renderedKeys.Length,
                state = CaptureLogScrollDiagnosticState(),
            });
        }
        catch (Exception error) { diagnostics.Record(error.ToString(), "logs.menu-observation.failure"); }
    }

    private static List<NativeDiagnostics.Entry> Shown(IEnumerable<NativeDiagnostics.Entry> entries) =>
        entries.Where(entry => entry.Level is not ("DEBUG" or "TRACE")
            && !entry.Raw.Contains("\"event\":\"logs.backend-ingested\"", StringComparison.Ordinal)).ToList();

    private void RenderLogs()
    {
        if (!_attached || _updatingLogs || _clearingLogs) return;
        var entries = Shown(_logPresentation.VisibleEntries);
        var keys = entries.Select(entry => new LogRowKey(entry.Id, entry.Raw)).ToArray();
        if (keys.SequenceEqual(_renderedKeys)) return;

        var desired = keys.ToHashSet();
        var sameOrder = _renderedKeys.Where(desired.Contains).SequenceEqual(keys.Where(_rowCache.ContainsKey));
        _updatingLogs = true;
        var generation = ++_logRenderGeneration;
        try
        {
            // Leave surviving controls attached on append/trim so selection survives.
            if (!sameOrder) rows.Children.Clear();
            foreach (var key in _renderedKeys)
            {
                if (desired.Contains(key)) continue;
                if (sameOrder) rows.Children.Remove(_rowCache[key]);
                _rowCache.Remove(key);
            }
            for (var i = 0; i < keys.Length; i++)
            {
                if (_rowCache.TryGetValue(keys[i], out var row))
                {
                    if (sameOrder) continue;
                }
                else
                {
                    row = CreateLogRow(entries[i]);
                    _rowCache.Add(keys[i], row);
                }
                rows.Children.Insert(i, row);
            }
            _renderedKeys = keys;
            CaptureLogScrollPosition();
        }
        catch (Exception renderError)
        {
            _updatingLogs = false;
            try { rows.Children.Clear(); }
            catch (Exception cleanupError) { throw new AggregateException(renderError, cleanupError); }
            _rowCache.Clear();
            _renderedKeys = [];
            throw;
        }
        if (!scroll.DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (!_attached || generation != _logRenderGeneration) return;
            _updatingLogs = false;
            CaptureLogScrollPosition();
            if (_logPresentation.FollowsTail)
            {
                RenderLogs();
                if (generation != _logRenderGeneration) return;
            }
            QueueLogTailChangeView(generation);
        }))
            _updatingLogs = false;
    }

    private RichTextBlock CreateLogRow(NativeDiagnostics.Entry entry)
    {
        var text = new RichTextBlock
        {
            IsTextSelectionEnabled = true, TextWrapping = TextWrapping.Wrap,
            FontFamily = (FontFamily)resources["Mono"], FontSize = 11, LineHeight = 16,
            Foreground = (Brush)resources["Ink"], ContextFlyout = menu, SelectionFlyout = null,
        };
        var paragraph = new Paragraph();
        var time = entry.Time?.ToLocalTime().ToString("HH:mm:ss", CultureInfo.InvariantCulture) ?? entry.Timestamp;
        if (time.Length > 0) paragraph.Inlines.Add(new Run { Text = time + "  ", Foreground = (Brush)resources["Muted"] });
        var message = new Run { Text = entry.Message };
        if (entry.Level is "ERROR" or "FATAL" or "PANIC" or "WARN" or "WARNING") message.FontWeight = FontWeights.Bold;
        paragraph.Inlines.Add(message);
        text.Blocks.Add(paragraph);
        return text;
    }

    private void QueueLogTailChangeView(long renderGeneration)
    {
        var queueId = ++_logTailQueueSequence;
        // Correlate only the first tail-queue attempt for an explicit gesture.
        var gesture = _pendingGesture;
        _pendingGesture = null;
        void Observe(string stage, bool? accepted = null, string? reason = null, double? target = null, bool? changed = null)
        {
            RecordLogTailQueueObservation(queueId, stage, renderGeneration, accepted, reason, target, changed);
            if (gesture is { } input)
                RecordLogTailGestureObservation(input.Id, input.Direction, stage, renderGeneration, accepted, reason, target, changed);
        }

        if (_updatingLogs)
        {
            Observe("queue-skipped", reason: "logs-updating");
            return;
        }
        var queued = scroll.DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (!_attached) return;
            if (_pendingTailQueue == queueId) _pendingTailQueue = null;
            Observe("callback-entered");
            if (renderGeneration != _logRenderGeneration)
            {
                Observe("callback-skipped", reason: "stale-render-generation");
                return;
            }
            if (_clearingLogs || _updatingLogs || _logDirectManipulation || _logScrollbarPointerDown)
            {
                var reason = _clearingLogs ? "clearing-logs" :
                    _updatingLogs ? "logs-updating" :
                    _logDirectManipulation ? "direct-manipulation" : "scrollbar-pointer-down";
                Observe("callback-skipped", reason: reason);
                return;
            }
            if (_pendingLogUpwardIntent)
            {
                _pendingLogUpwardIntent = false;
                _pendingLogDownScroll = false;
                var wasFollowing = _logPresentation.FollowsTail;
                if (scroll.ScrollableHeight > 0.5)
                {
                    _logPresentation.Freeze();
                    RecordLogFollowTransition("tail-callback-upward-intent-freeze", wasFollowing, gesture);
                    Observe("callback-skipped", reason: "upward-intent-freeze");
                    return;
                }
                _logPresentation.FollowTail();
                RecordLogFollowTransition("tail-callback-upward-intent-follow", wasFollowing, gesture);
            }
            if (!_logPresentation.FollowsTail)
            {
                Observe("callback-skipped", reason: "tail-follow-frozen");
                return;
            }
            var targetOffset = scroll.ScrollableHeight;
            var changeViewResult = scroll.ChangeView(null, targetOffset, null, true);
            CaptureLogScrollPosition();
            Observe("change-view-returned", target: targetOffset, changed: changeViewResult);
        });
        if (queued) _pendingTailQueue = queueId;
        Observe("queue-result", accepted: queued);
    }

    private void RecordLogTailQueueObservation(long queueId, string stage, long renderGeneration,
        bool? queueAccepted = null, string? skipReason = null, double? targetOffset = null,
        bool? changeViewResult = null)
    {
        try
        {
            diagnostics.RecordDebug("logs.tail-queue", "Observed log tail alignment", new
            {
                queue_id = queueId,
                stage,
                render_generation = renderGeneration,
                current_generation = _logRenderGeneration,
                queue_accepted = queueAccepted,
                skip_reason = skipReason,
                target_offset = targetOffset,
                change_view_result = changeViewResult,
                state = CaptureLogScrollDiagnosticState(),
            });
        }
        catch (Exception error) { diagnostics.Record(error.ToString(), "logs.tail-queue.failure"); }
    }

    private void RecordLogTailGestureObservation(long gestureId, string? direction, string stage,
        long renderGeneration, bool? queueAccepted = null, string? skipReason = null,
        double? targetOffset = null, bool? changeViewResult = null) =>
        diagnostics.RecordInfo("logs.tail-gesture", "Observed the first tail alignment attempt for an explicit scroll gesture", new
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
}
