using System;
using System.Runtime.InteropServices;
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

    internal static void WaitForRedirect(Func<Task> redirect)
    {
        ArgumentNullException.ThrowIfNull(redirect);
        if (Thread.CurrentThread.GetApartmentState() != ApartmentState.STA)
            throw new InvalidOperationException("Windows activation redirection must be waited on from an STA thread.");

        using var completed = new EventWaitHandle(false, EventResetMode.ManualReset);
        var redirectTask = Task.Run(async () => await redirect().ConfigureAwait(false));
        _ = redirectTask.ContinueWith(
            _ => { completed.Set(); },
            CancellationToken.None,
            TaskContinuationOptions.ExecuteSynchronously,
            TaskScheduler.Default);

        var waitResult = CoWaitForMultipleObjects(
            0, WaitForever, 1, [completed.SafeWaitHandle.DangerousGetHandle()], out var handleIndex);
        Marshal.ThrowExceptionForHR(waitResult);
        if (handleIndex != 0)
            throw new InvalidOperationException($"Activation redirection wait returned unexpected handle index {handleIndex}.");

        redirectTask.GetAwaiter().GetResult();
    }
}
