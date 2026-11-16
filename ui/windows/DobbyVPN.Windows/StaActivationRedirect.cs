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
    private const uint WaitForever = 0xFFFFFFFF;

    [DllImport("ole32.dll", PreserveSig = true)]
    private static extern int CoWaitForMultipleObjects(
        uint flags,
        uint timeoutMilliseconds,
        uint handleCount,
        [In] IntPtr[] handles,
        out uint handleIndex);

    internal static void WaitForRedirect(Func<Task> redirect, uint? targetProcessId = null)
    {
        ArgumentNullException.ThrowIfNull(redirect);
        if (Thread.CurrentThread.GetApartmentState() != ApartmentState.STA)
            throw new InvalidOperationException("Windows activation redirection must be waited on from an STA thread.");

        using var target = targetProcessId is null ? null : Process.GetProcessById(checked((int)targetProcessId.Value));
        using var completed = new EventWaitHandle(false, EventResetMode.ManualReset);
        var redirectTask = Task.Run(async () => await redirect().ConfigureAwait(false));
        _ = redirectTask.ContinueWith(
            _ => { try { completed.Set(); } catch (ObjectDisposedException) { } },
            CancellationToken.None,
            TaskContinuationOptions.ExecuteSynchronously,
            TaskScheduler.Default);

        var handles = target is null
            ? new[] { completed.SafeWaitHandle.DangerousGetHandle() }
            : new[] { completed.SafeWaitHandle.DangerousGetHandle(), target.Handle };
        var waitResult = CoWaitForMultipleObjects(0, WaitForever, (uint)handles.Length, handles, out var handleIndex);
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
