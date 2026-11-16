namespace DobbyVPN.Windows;

// Owns delivered activations independently of any one window. All queue and
// final-close decisions share the same gate, including handoff to a new window.
internal sealed class ActivationLifetime<TActivation, TWindow> where TWindow : class
{
    private readonly object _gate = new();
    private readonly Queue<TActivation> _pending = new();
    private readonly Action<Exception> _deliveryFailed;

    internal ActivationLifetime(Action<Exception>? deliveryFailed = null) =>
        _deliveryFailed = deliveryFailed ?? (_ => { });
    private TWindow? _window;
    private bool _accepting = true;

    internal bool Enqueue(TActivation activation, Func<bool> schedule, Action? queued = null)
    {
        lock (_gate)
        {
            if (!_accepting) return false;
            _pending.Enqueue(activation);
            try { queued?.Invoke(); }
            catch (Exception error) { _deliveryFailed(error); }
            return schedule();
        }
    }

    internal void Attach(TWindow window, Func<TWindow, TActivation, bool> accept)
    {
        lock (_gate) { _window = window; Drain(accept); }
    }

    internal void Drain(Func<TWindow, TActivation, bool> accept)
    {
        lock (_gate)
        {
            while (_window is not null && _pending.TryPeek(out var activation))
            {
                bool accepted;
                try { accepted = accept(_window, activation); }
                catch (Exception error)
                {
                    // Delivery may have partially changed the recipient. Do not
                    // replay an ambiguously delivered item on a later drain.
                    _pending.Dequeue();
                    _deliveryFailed(error);
                    continue;
                }
                if (!accepted) return;
                _pending.Dequeue();
            }
        }
    }

    internal void FinalClose(TWindow window, Func<TWindow> createWindow,
        Func<TWindow, TActivation, bool> accept, Action unregister, Action close)
    {
        lock (_gate)
        {
            if (ReferenceEquals(_window, window))
            {
                if (_pending.Count != 0)
                {
                    var replacement = createWindow();
                    _window = replacement;
                    try
                    {
                        Drain(accept);
                        if (_pending.Count != 0)
                            throw new InvalidOperationException("Replacement window did not accept pending activation.");
                    }
                    finally { close(); }
                    return;
                }
                else
                {
                    unregister();
                    _accepting = false;
                    _window = null;
                }
            }
            close();
        }
    }
}
