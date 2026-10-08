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
        try
        {
            InitializeComponent();

            // The Windows native UI journey uses two cold launches to verify the
            // rendered light and dark log palettes. Ordinary launches leave this
            // unset and continue to use the Windows app-mode preference.
            var testTheme = Environment.GetEnvironmentVariable("DOBBYVPN_TEST_REQUESTED_THEME");
            if (testTheme is not null)
            {
                RequestedTheme = testTheme switch
                {
                    "Light" => ApplicationTheme.Light,
                    "Dark" => ApplicationTheme.Dark,
                    _ => throw new InvalidOperationException(
                        "DOBBYVPN_TEST_REQUESTED_THEME must be Light or Dark"),
                };
            }
        }
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
            Program.Attach(_window);
        }
        catch (Exception error)
        {
            NativeDiagnostics.Current.Record(error.ToString(), "window.bootstrap");
            throw;
        }
    }
}
