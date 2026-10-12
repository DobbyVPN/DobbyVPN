namespace DobbyVPN.Windows;

/// <summary>Owns asynchronous work and cancellation for one Windows frontend window.</summary>
internal sealed class WindowLifetime
{
    private readonly object _gate = new();
    private readonly CancellationTokenSource _shutdown = new();
    private readonly CancellationToken _shutdownToken;
    private readonly HashSet<Task> _pending = [];
    private bool _closing;
    private bool _disposed;
    private Task<bool>? _drainTask;

    public WindowLifetime() => _shutdownToken = _shutdown.Token;

    // Cache the token so callbacks already in flight never query a disposed source.
    public CancellationToken ShutdownToken => _shutdownToken;

    public bool IsClosing
    {
        get
        {
            lock (_gate) return _closing;
        }
    }

    public bool IsDisposed
    {
        get
        {
            lock (_gate) return _disposed;
        }
    }

    public bool TryRun(Func<CancellationToken, Task> operation, Action<Exception> reportError)
    {
        ArgumentNullException.ThrowIfNull(operation);
        ArgumentNullException.ThrowIfNull(reportError);

        var completion = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        lock (_gate)
        {
            if (_closing) return false;
            _pending.Add(completion.Task);
            _ = RunTrackedAsync(operation, reportError, completion);
        }
        return true;
    }

    /// <summary>Run a short dispatcher callback only while the window is still open.</summary>
    public bool TryRunCallback(Action callback)
    {
        ArgumentNullException.ThrowIfNull(callback);
        lock (_gate)
        {
            if (_closing) return false;
            callback();
            return true;
        }
    }

    public bool BeginClosing()
    {
        lock (_gate)
        {
            if (_closing) return false;
            _closing = true;
            return true;
        }
    }

    public Task DrainAsync()
    {
        TaskCompletionSource<bool> completion;
        lock (_gate)
        {
            if (!_closing)
                throw new InvalidOperationException("Window work must be rejected before draining begins.");
            if (_drainTask is not null) return _drainTask;
            completion = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            _drainTask = completion.Task;
        }

        _ = CancelDrainAndDisposeAsync(completion);
        return completion.Task;
    }

    public Task CloseAsync()
    {
        BeginClosing();
        return DrainAsync();
    }

    private async Task RunTrackedAsync(
        Func<CancellationToken, Task> operation,
        Action<Exception> reportError,
        TaskCompletionSource<bool> completion)
    {
        Exception? reportingFailure = null;
        try
        {
            if (!IsClosing)
                await operation(_shutdownToken);
        }
        catch (OperationCanceledException) when (_shutdownToken.IsCancellationRequested)
        {
        }
        catch (Exception error)
        {
            if (!IsClosing)
            {
                try { reportError(error); }
                catch (Exception reportErrorFailure)
                {
                    reportingFailure = new AggregateException(error, reportErrorFailure);
                }
            }
        }
        finally
        {
            if (reportingFailure is null) completion.TrySetResult(true);
            else completion.TrySetException(reportingFailure);
            lock (_gate) _pending.Remove(completion.Task);
        }
    }

    private async Task CancelDrainAndDisposeAsync(TaskCompletionSource<bool> completion)
    {
        var failures = new List<Exception>();
        try { await _shutdown.CancelAsync(); }
        catch (Exception error) { failures.Add(error); }

        while (true)
        {
            Task[] pending;
            lock (_gate) pending = _pending.ToArray();
            if (pending.Length == 0) break;
            try { await Task.WhenAll(pending); }
            catch (Exception error) { failures.Add(error); }
        }

        _shutdown.Dispose();
        lock (_gate) _disposed = true;
        if (failures.Count == 0) completion.TrySetResult(true);
        else completion.TrySetException(new AggregateException("Windows window shutdown did not drain cleanly.", failures));
    }
}
