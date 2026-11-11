using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;
using Windows.ApplicationModel.Activation;
using System.Diagnostics;
using System.Runtime.InteropServices;

namespace DobbyVPN.Windows;

internal static class Program
{
    private static readonly ActivationLifetime<AppActivationArguments, MainWindow> Activations = new(
        error => NativeDiagnostics.Current.Record(error.ToString(), "activation.delivery.failure"));
    private static volatile DispatcherQueue? _dispatcher;
    private static AppInstance? _instance;
    private static App? _app;

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
        LogInstanceRegistration(instance);
        if (!instance.IsCurrent)
        {
            LogInstanceVersions(instance.ProcessId);
            LogActivation("activation.redirect-start", "Redirecting activation to the running app", activation, args);
            try
            {
                StaActivationRedirect.WaitForRedirect(async () => await instance.RedirectActivationToAsync(activation), instance.ProcessId);
                LogActivation("activation.redirect-complete", "Startup activation redirect completed; recipient acceptance is not confirmed", activation, args, true);
            }
            catch (Exception error)
            {
                LogActivation("activation.redirect-failed", "Startup activation redirect failed", activation, args,
                    false, error: error.ToString());
                throw;
            }
            return;
        }
        _instance = instance;
        Enqueue(activation);
        instance.Activated += (_, next) =>
        {
            Enqueue(next, () => LogActivation("activation.redirect-received", "Queued redirected activation in the running app", next));
        };
        Application.Start(parameters =>
        {
            _dispatcher = DispatcherQueue.GetForCurrentThread();
            SynchronizationContext.SetSynchronizationContext(new DispatcherQueueSynchronizationContext(_dispatcher));
            _app = new App();
        });
    }

    private static void Enqueue(AppActivationArguments activation, Action? queued = null)
    {
        try
        {
            if (!Activations.Enqueue(activation, () =>
                {
                    var dispatcher = _dispatcher;
                    return dispatcher is null || dispatcher.TryEnqueue(Drain);
                }, queued))
                NativeDiagnostics.Current.Record("Activation dispatcher rejected delivery or the application stopped accepting activations; pending delivery is retained when possible.", "activation.dispatch.failure");
        }
        catch (Exception error)
        {
            NativeDiagnostics.Current.Record(error.ToString(), "activation.dispatch.failure");
            throw;
        }
    }

    private static void LogInstanceRegistration(AppInstance instance)
    {
        object targetProcess;
        try
        {
            using var process = Process.GetProcessById(checked((int)instance.ProcessId));
            targetProcess = new
            {
                processId = process.Id,
                processName = process.ProcessName,
                executablePath = process.MainModule?.FileName,
                startTimeUtc = process.StartTime.ToUniversalTime().ToString("O"),
                sessionId = process.SessionId,
                hasExited = process.HasExited,
            };
        }
        catch (Exception error)
        {
            targetProcess = new { processId = instance.ProcessId, error = error.ToString() };
        }

        NativeDiagnostics.Current.RecordInfo("activation.instance-registration", "Resolved the Windows app instance key", new
        {
            key = instance.Key,
            isCurrent = instance.IsCurrent,
            currentProcessId = Environment.ProcessId,
            targetProcessId = instance.ProcessId,
            targetProcess,
        });
    }

    // Permanent DEBUG evidence for a surviving frontend: the version strings
    // identify whether the running target is the launch being redirected.
    private static void LogInstanceVersions(uint targetProcessId)
    {
        string? targetPath = null;
        string? targetError = null;
        try
        {
            using var process = Process.GetProcessById(checked((int)targetProcessId));
            targetPath = process.MainModule?.FileName;
        }
        catch (Exception error) { targetError = error.ToString(); }

        NativeDiagnostics.Current.RecordDebug("activation.instance-versions",
            "Logging app and target process versions before redirecting activation", new
            {
                currentProcessId = Environment.ProcessId,
                currentPath = Environment.ProcessPath,
                ownVersion = OwnVersion(),
                ownFileVersion = FileVersionInfoFor(Environment.ProcessPath),
                targetProcessId,
                targetPath,
                targetFileVersion = FileVersionInfoFor(targetPath),
                targetError,
            });
    }

    private static string? OwnVersion() =>
        System.Reflection.Assembly.GetExecutingAssembly().GetName().Version?.ToString(3);

    private static object? FileVersionInfoFor(string? path)
    {
        if (string.IsNullOrEmpty(path)) return null;
        try
        {
            var info = FileVersionInfo.GetVersionInfo(path);
            return new
            {
                fileVersion = info.FileVersion,
                productVersion = info.ProductVersion,
                originalFilename = info.OriginalFilename,
            };
        }
        catch (Exception error) { return new { error = error.ToString() }; }
    }

    internal static void Attach(MainWindow window) => Activations.Attach(window, Accept);

    internal static void FinalClose(MainWindow window, Action close) =>
        Activations.FinalClose(window,
            () => _app!.CreateActivationWindow(), Accept,
            () => _instance!.UnregisterKey(), close);

    private static void Drain() => Activations.Drain(Accept);

    private static bool Accept(MainWindow window, AppActivationArguments activation)
    {
        return window.TryAcceptActivation(() =>
        {
            if (activation.Kind == ExtendedActivationKind.Protocol && activation.Data is IProtocolActivatedEventArgs protocol)
            {
                LogActivation("activation.drain-protocol", "Dispatching protocol activation", activation, route: "protocol");
                window.ImportLink(protocol.Uri.AbsoluteUri);
            }
            else if (activation.Data is ILaunchActivatedEventArgs launch)
            {
                var arguments = Arguments(launch.Arguments).ToArray();
                LogActivation("activation.drain-launch", "Processing launch activation arguments", activation,
                    arguments, route: "launch-arguments");
                foreach (var value in arguments)
                    if (value.StartsWith("dobbyvpn:", StringComparison.OrdinalIgnoreCase)) window.ImportLink(value);
            }
            else
            {
                LogActivation("activation.drain-unhandled", "Activation had no supported URI payload", activation,
                    route: "unhandled");
            }
            LogActivation("activation.recipient-accepted", "A live window accepted the activation", activation);
        });
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
