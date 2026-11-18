using Microsoft.UI.Xaml;

namespace DobbyVPN.Windows;

public partial class App : Application
{
    private MainWindow? _window;

    public App()
    {
        UnhandledException += (_, args) => NativeDiagnostics.Current.Record(args.Exception.ToString(), "app.unhandled");
        AppDomain.CurrentDomain.UnhandledException += (_, args) =>
            NativeDiagnostics.Current.Record(args.ExceptionObject.ToString() ?? "Unknown unhandled failure", "process.unhandled");
        TaskScheduler.UnobservedTaskException += (_, args) =>
        {
            // Runs on the finalizer thread; the log write can wait on a lock, so do it elsewhere.
            var text = args.Exception.ToString();
            _ = Task.Run(() => { try { NativeDiagnostics.Current.Record(text, "task.unobserved"); } catch { } });
        };
        try
        {
            InitializeComponent();
        }
        catch (Exception error)
        {
            NativeDiagnostics.Current.Record(error.ToString(), "app.bootstrap");
            throw;
        }
    }

    internal MainWindow CreateActivationWindow()
    {
        _window = new MainWindow();
        _window.Activate();
        return _window;
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        try
        {
            CreateActivationWindow();
            Program.Attach(_window!);
        }
        catch (Exception error)
        {
            NativeDiagnostics.Current.Record(error.ToString(), "window.bootstrap");
            throw;
        }
    }
}
