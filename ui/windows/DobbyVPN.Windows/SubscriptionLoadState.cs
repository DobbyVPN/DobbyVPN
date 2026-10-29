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
/// Time is supplied by the caller so the production decisions can be exercised deterministically.
/// </summary>
internal sealed class SubscriptionLoadState
{
    internal const int DebounceMilliseconds = 400;

    internal sealed record Request(long Revision, string Source);

    private long _revision;
    private Request? _scheduled;
    private TimeSpan? _scheduledAt;
    private Request? _pending;
    private Request? _inFlight;

    internal long Revision => _revision;
    internal bool SourceDirty { get; private set; }
    internal bool IsLoading => _inFlight is not null;

    internal long Edit(string source, bool valid, SubscriptionInputKind kind, TimeSpan now)
    {
        _revision++;
        SourceDirty = true;
        _scheduled = null;
        _scheduledAt = null;
        _pending = null;

        if (!valid) return _revision;

        var request = new Request(_revision, source);
        if (kind == SubscriptionInputKind.Typed)
        {
            _scheduled = request;
            _scheduledAt = now + TimeSpan.FromMilliseconds(DebounceMilliseconds);
        }
        else
        {
            _pending = request;
        }
        return _revision;
    }

    internal bool IsCurrent(Request request) => request.Revision == _revision;

    internal Request? BeginReady(TimeSpan now)
    {
        if (_inFlight is not null) return null;
        PromoteElapsed(now);
        if (_pending is not { } request) return null;
        _pending = null;
        _inFlight = request;
        return request;
    }

    /// <summary>Marks only the newest successful request accepted by the backend.</summary>
    internal bool MarkAccepted(Request request)
    {
        if (_inFlight != request || !IsCurrent(request)) return false;
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
