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
        var diagnostics = NativeDiagnostics.Current;
        diagnostics.RecordInfo("activation.process-entry", "Entered Windows app Main before WinRT initialization", new
        {
            argv = args,
            processPath = Environment.ProcessPath,
            apartment = System.Threading.Thread.CurrentThread.GetApartmentState().ToString(),
            localApplicationData = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
            uiDiagnosticsPath = diagnostics.UiPath,
        });
        if (System.Threading.Thread.CurrentThread.GetApartmentState() != System.Threading.ApartmentState.STA)
            throw new InvalidOperationException("WinUI entrypoint requires an STA thread.");

        WinRT.ComWrappersSupport.InitializeComWrappers();
        diagnostics.RecordInfo("activation.runtime-initialized", "Initialized WinRT COM wrappers", new
        {
            processPath = Environment.ProcessPath,
        });
        var activation = AppInstance.GetCurrent().GetActivatedEventArgs();
        LogActivation("activation.startup", "Captured Windows app startup activation", activation, args);
        var instance = AppInstance.FindOrRegisterForKey("DobbyVPN");
        if (!instance.IsCurrent)
        {
            LogActivation("activation.redirect-start", "Redirecting activation to the running app", activation, args);
            try
            {
                Task.Run(async () => await instance.RedirectActivationToAsync(activation)).GetAwaiter().GetResult();
                LogActivation("activation.redirect-complete", "Startup activation redirect completed", activation, args, true);
            }
            catch (Exception error)
            {
                LogActivation("activation.redirect-failed", "Startup activation redirect failed", activation, args,
                    false, error: error.ToString());
                throw;
            }
            return;
        }
        Pending.Enqueue(activation);
        instance.Activated += (_, next) =>
        {
            LogActivation("activation.redirect-received", "Received activation in the running app", next);
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
                {
                    LogActivation("activation.drain-protocol", "Dispatching protocol activation", activation, route: "protocol");
                    _window.ImportLink(protocol.Uri.AbsoluteUri);
                }
                else if (activation.Data is ILaunchActivatedEventArgs launch)
                {
                    var arguments = Arguments(launch.Arguments).ToArray();
                    LogActivation("activation.drain-launch", "Processing launch activation arguments", activation,
                        arguments, route: "launch-arguments");
                    foreach (var value in arguments)
                        if (value.StartsWith("dobbyvpn:", StringComparison.OrdinalIgnoreCase)) _window.ImportLink(value);
                }
                else
                {
                    LogActivation("activation.drain-unhandled", "Activation had no supported URI payload", activation,
                        route: "unhandled");
                }
            }
        }
    }

    private static void LogActivation(
        string category,
        string message,
        AppActivationArguments activation,
        string[]? argv = null,
        bool? redirectCompleted = null,
        string? route = null,
        string? error = null)
    {
        var data = activation.Data;
        NativeDiagnostics.Current.RecordInfo(category, message, new
        {
            kind = activation.Kind.ToString(),
            runtimeDataType = data?.GetType().FullName,
            protocolUri = (data as IProtocolActivatedEventArgs)?.Uri.AbsoluteUri,
            launchArguments = (data as ILaunchActivatedEventArgs)?.Arguments,
            argv,
            redirectCompleted,
            route,
            error,
        });
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
