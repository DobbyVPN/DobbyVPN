using System;

namespace DobbyVPN.Windows;

internal enum SubscriptionInputKind
{
    Typed,
    Paste,
    Import,
    Retry,
    Restore,
}

/// <summary>
/// Owns URL edit freshness, the typed-input debounce, and serialized Configure dispatch.
/// The latest editor text is the authoritative intent: a queued request that no longer
/// matches it can neither start nor report its outcome. Time is supplied by the caller so
/// the production decisions can be exercised deterministically.
/// </summary>
internal sealed class SubscriptionLoadState
{
    internal const int DebounceMilliseconds = 400;

    internal sealed record Request(long Revision, string Source, SubscriptionInputKind Kind);

    internal sealed record EditResult(long Revision, bool DuplicateSuppressed, bool DroppedQueued);

    private long _revision;
    private Request? _scheduled;
    private TimeSpan? _scheduledAt;
    private Request? _pending;
    private Request? _inFlight;
    private Request? _latestRequest;
    private string? _latestEditorSource;
    private string? _acceptedSource;

    internal long Revision => _revision;
    internal bool SourceDirty { get; private set; }
    internal bool IsLoading => _inFlight is not null;
    internal string? AcceptedSource => _acceptedSource;

    internal bool CanRestore(string? acceptedSource, string editorSource) =>
        !SourceDirty && !string.IsNullOrEmpty(acceptedSource) &&
        string.Equals(acceptedSource, editorSource, StringComparison.Ordinal);

    internal EditResult Edit(string source, bool valid, SubscriptionInputKind kind, TimeSpan now)
    {
        _latestEditorSource = source;

        // A duplicate typed edit for the latest intent (e.g. a spurious TextChanged) must not
        // supersede an in-flight or queued load (#331). It also drops any queued request for a
        // superseded source, so the editor intent that is on screen is the only one that can
        // start. Explicit Retry/Paste/Import/Restore always proceed.
        var duplicate = valid && kind == SubscriptionInputKind.Typed &&
            (string.Equals(_inFlight?.Source, source, StringComparison.Ordinal) ||
             string.Equals(_latestRequest?.Source, source, StringComparison.Ordinal));
        if (duplicate)
        {
            var dropped = false;
            if (_scheduled is { } scheduled && !string.Equals(scheduled.Source, source, StringComparison.Ordinal))
            {
                _scheduled = null;
                _scheduledAt = null;
                dropped = true;
            }
            if (_pending is { } pending && !string.Equals(pending.Source, source, StringComparison.Ordinal))
            {
                _pending = null;
                dropped = true;
            }
            if (string.Equals(_inFlight?.Source, source, StringComparison.Ordinal))
                _latestRequest = _inFlight;
            if (_inFlight is null && string.Equals(_acceptedSource, source, StringComparison.Ordinal))
                SourceDirty = false;
            return new EditResult(_revision, true, dropped);
        }

        var superseded = _scheduled is not null || _pending is not null;
        _revision++;
        SourceDirty = true;
        _scheduled = null;
        _scheduledAt = null;
        _pending = null;

        if (!valid)
        {
            // No request represents the intent while the text is invalid, so a later valid edit
            // for the previous source must queue again instead of being taken as a duplicate.
            _latestRequest = null;
            return new EditResult(_revision, false, superseded);
        }

        var request = new Request(_revision, source, kind);
        _latestRequest = request;
        if (kind == SubscriptionInputKind.Typed)
        {
            _scheduled = request;
            _scheduledAt = now + TimeSpan.FromMilliseconds(DebounceMilliseconds);
        }
        else
        {
            _pending = request;
        }
        return new EditResult(_revision, false, superseded);
    }

    internal bool IsCurrent(Request request) =>
        string.Equals(request.Source, _latestEditorSource, StringComparison.Ordinal);

    internal Request? BeginReady(TimeSpan now)
    {
        if (_inFlight is not null) return null;
        PromoteElapsed(now);
        if (_pending is not { } request) return null;
        if (!string.Equals(request.Source, _latestEditorSource, StringComparison.Ordinal))
        {
            _pending = null;
            return null;
        }
        _pending = null;
        _inFlight = request;
        return request;
    }

    /// <summary>Marks only the newest successful request accepted by the backend.</summary>
    internal bool MarkAccepted(Request request)
    {
        if (_inFlight != request ||
            !string.Equals(request.Source, _latestEditorSource, StringComparison.Ordinal)) return false;
        _acceptedSource = request.Source;
        SourceDirty = false;
        return true;
    }

    /// <summary>
    /// Ends the serialized load after the production snapshot refresh and starts the newest
    /// request only when its immediate trigger or debounce deadline is ready.
    /// </summary>
    internal Request? Finish(Request request, TimeSpan now)
    {
        if (_inFlight != request)
            throw new InvalidOperationException("Completed subscription load did not match the in-flight request");

        _inFlight = null;
        PromoteElapsed(now);
        if (_pending is not { } next) return null;
        // Only the latest editor intent may start; a queued request for a superseded source is
        // dropped instead of overwriting the accepted state (#331).
        if (!string.Equals(next.Source, _latestEditorSource, StringComparison.Ordinal))
        {
            _pending = null;
            return null;
        }
        _pending = null;
        _inFlight = next;
        return next;
    }

    internal bool CanAct(
        bool isStopTarget,
        bool stopInFlight,
        bool busy,
        bool hasLoadError,
        bool sequenceCurrent,
        bool configured,
        bool canStartOrSwitch)
    {
        if (isStopTarget) return !stopInFlight;
        return !busy && !stopInFlight && !SourceDirty && !IsLoading && !hasLoadError &&
            sequenceCurrent && configured && canStartOrSwitch;
    }

    private void PromoteElapsed(TimeSpan now)
    {
        if (_scheduled is not { } request || _scheduledAt is not { } due || now < due) return;
        _scheduled = null;
        _scheduledAt = null;
        _pending = request;
    }
}
