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
