using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;
using Windows.ApplicationModel.Activation;
using System.Runtime.InteropServices;

namespace DobbyVPN.Windows;

internal static class Program
{
    private static readonly Queue<AppActivationArguments> Pending = new();
    private static MainWindow? _window;

    [STAThread]
    public static void Main(string[] args)
    {
        WinRT.ComWrappersSupport.InitializeComWrappers();
        var activation = AppInstance.GetCurrent().GetActivatedEventArgs();
        var instance = AppInstance.FindOrRegisterForKey("DobbyVPN");
        if (!instance.IsCurrent)
        {
            Task.Run(async () => await instance.RedirectActivationToAsync(activation)).GetAwaiter().GetResult();
            return;
        }
        Pending.Enqueue(activation);
        instance.Activated += (_, next) =>
        {
            lock (Pending)
            {
                Pending.Enqueue(next);
                _window?.DispatcherQueue.TryEnqueue(Drain);
            }
        };
        Application.Start(parameters =>
        {
            SynchronizationContext.SetSynchronizationContext(new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread()));
            _ = new App();
        });
    }

    internal static void Attach(MainWindow window)
    {
        lock (Pending) { _window = window; }
        Drain();
    }

    private static void Drain()
    {
        lock (Pending)
        {
            while (_window is not null && Pending.TryDequeue(out var activation))
            {
                _window.Activate();
                if (activation.Kind == ExtendedActivationKind.Protocol && activation.Data is IProtocolActivatedEventArgs protocol)
                    _window.ImportLink(protocol.Uri.AbsoluteUri);
                else if (activation.Data is ILaunchActivatedEventArgs launch)
                    foreach (var value in Arguments(launch.Arguments))
                        if (value.StartsWith("dobbyvpn:", StringComparison.OrdinalIgnoreCase)) _window.ImportLink(value);
            }
        }
    }

    private static IEnumerable<string> Arguments(string commandLine)
    {
        if (string.IsNullOrWhiteSpace(commandLine)) return [];
        var pointer = CommandLineToArgvW(commandLine, out var count);
        if (pointer == IntPtr.Zero) throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
        try
        {
            return Enumerable.Range(0, count).Select(index => Marshal.PtrToStringUni(Marshal.ReadIntPtr(pointer, index * IntPtr.Size)) ?? "").ToArray();
        }
        finally { LocalFree(pointer); }
    }

    [DllImport("shell32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr CommandLineToArgvW(string commandLine, out int count);
    [DllImport("kernel32.dll")]
    private static extern IntPtr LocalFree(IntPtr memory);
}
