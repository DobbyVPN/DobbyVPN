using System;
using System.Runtime.InteropServices;
using System.Diagnostics;
using System.Threading;
using System.Threading.Tasks;

namespace DobbyVPN.Windows;

/// <summary>
/// Waits for WinUI's asynchronous activation redirection without blocking the
/// entrypoint's STA with a managed task wait.
/// </summary>
internal static class StaActivationRedirect
{
    /// <summary>
    /// How long the second process waits for the running instance to take over
    /// its activation. A healthy hand-off takes well under a second; ten
    /// seconds tolerates a loaded machine while a frozen first instance cannot
    /// keep the second process waiting forever.
    /// </summary>
    internal static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(10);

    /// <summary>Shown when a link or second launch cannot be handed to a running instance.</summary>
    internal const string NotRespondingMessage = "DobbyVPN is not responding. Close it and open the link again.";

    private const uint WaitForever = 0xFFFFFFFF;
    private const int RpcSCallPending = unchecked((int)0x80010115);
    private const int WaitTimeout = 0x102;

    [DllImport("ole32.dll", PreserveSig = true)]
    private static extern int CoWaitForMultipleObjects(
        uint flags,
        uint timeoutMilliseconds,
        uint handleCount,
        [In] IntPtr[] handles,
        out uint handleIndex);

    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern int MessageBoxW(IntPtr window, string text, string caption, uint type);

    /// <summary>
    /// Redirects activation and reports whether the running instance took it.
    /// On a timeout, target exit or any other redirect failure it logs through
    /// <paramref name="onFailure"/>, tells the user with <paramref name="show"/>
    /// (a system message box by default), and returns false so the caller can
    /// exit normally. Nothing is written to disk.
    /// </summary>
    internal static bool TryRedirect(
        Func<Task> redirect, uint? targetProcessId, Action<Exception> onFailure,
        Action<string>? show = null, TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(onFailure);
        try
        {
            WaitForRedirect(redirect, targetProcessId, timeout);
            return true;
        }
        catch (Exception error)
        {
            try { onFailure(error); } catch (Exception) { /* logging must not hide the user message */ }
            ShowNotRespondingMessage(show);
            return false;
        }
    }

    internal static void ShowNotRespondingMessage(Action<string>? show = null)
    {
        try { (show ?? ShowSystemMessage)(NotRespondingMessage); }
        catch (Exception) { /* nothing else can be shown; the caller still exits normally */ }
    }

    private static void ShowSystemMessage(string message) =>
        MessageBoxW(IntPtr.Zero, message, "DobbyVPN", 0x00000010 /* MB_ICONERROR */ | 0x00010000 /* MB_SETFOREGROUND */);

    /// <exception cref="TimeoutException">The redirect neither completed nor lost its target within <paramref name="timeout"/>.</exception>
    internal static void WaitForRedirect(Func<Task> redirect, uint? targetProcessId = null, TimeSpan? timeout = null)
    {
        ArgumentNullException.ThrowIfNull(redirect);
        var limit = timeout ?? DefaultTimeout;
        if (limit <= TimeSpan.Zero || limit.TotalMilliseconds >= WaitForever)
            throw new ArgumentOutOfRangeException(nameof(timeout), limit, "The activation redirect timeout must be positive and finite.");
        if (Thread.CurrentThread.GetApartmentState() != ApartmentState.STA)
            throw new InvalidOperationException("Windows activation redirection must be waited on from an STA thread.");

        using var target = targetProcessId is null ? null : Process.GetProcessById(checked((int)targetProcessId.Value));
        using var completed = new EventWaitHandle(false, EventResetMode.ManualReset);
        var redirectTask = Task.Run(async () => await redirect().ConfigureAwait(false));
        _ = redirectTask.ContinueWith(
            finished =>
            {
                // A redirect abandoned after a timeout may fault later; observe it.
                _ = finished.Exception;
                try { completed.Set(); } catch (ObjectDisposedException) { }
            },
            CancellationToken.None,
            TaskContinuationOptions.ExecuteSynchronously,
            TaskScheduler.Default);

        var handles = target is null
            ? new[] { completed.SafeWaitHandle.DangerousGetHandle() }
            : new[] { completed.SafeWaitHandle.DangerousGetHandle(), target.Handle };
        var waitResult = CoWaitForMultipleObjects(
            0, (uint)Math.Ceiling(limit.TotalMilliseconds), (uint)handles.Length, handles, out var handleIndex);
        if (waitResult is RpcSCallPending or WaitTimeout)
            throw new TimeoutException(
                $"Activation redirection did not complete within {limit.TotalSeconds:0.###} s; the running instance did not answer.");
        Marshal.ThrowExceptionForHR(waitResult);
        if (handleIndex >= handles.Length)
            throw new InvalidOperationException($"Activation redirection wait returned unexpected handle index {handleIndex}.");

        // Preserve the redirect's original failure if it has completed, even if
        // the target exit was observed at the same time. Completion alone does
        // not acknowledge that a recipient accepted the payload.
        CompleteRedirect(redirectTask, target is not null && target.HasExited, handleIndex != 0, targetProcessId);
    }

    internal static void CompleteRedirect(Task redirectTask, bool targetExited, bool targetSignaled, uint? targetProcessId)
    {
        if (redirectTask.IsCompleted) redirectTask.GetAwaiter().GetResult();
        if (targetExited || targetSignaled)
            throw new InvalidOperationException($"Activation target process {targetProcessId} exited during redirection.");
        redirectTask.GetAwaiter().GetResult();
    }
}
