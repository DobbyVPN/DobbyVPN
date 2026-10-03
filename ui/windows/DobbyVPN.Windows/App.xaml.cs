using Microsoft.UI.Xaml;

namespace DobbyVPN.Windows;

public partial class App : Application
{
    private Window? _window;

    public App()
    {
        UnhandledException += (_, args) => NativeDiagnostics.Current.Record(args.Exception.ToString(), "app.unhandled");
        AppDomain.CurrentDomain.UnhandledException += (_, args) =>
            NativeDiagnostics.Current.Record(args.ExceptionObject.ToString() ?? "Unknown unhandled failure", "process.unhandled");
        try { InitializeComponent(); }
        catch (Exception error)
        {
            NativeDiagnostics.Current.Record(error.ToString(), "app.bootstrap");
            throw;
        }
    }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        try
        {
            _window = new MainWindow();
            _window.Activate();
        }
        catch (Exception error)
        {
            NativeDiagnostics.Current.Record(error.ToString(), "window.bootstrap");
            throw;
        }
    }
}
