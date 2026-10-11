using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Net.Http;
using System.Runtime.InteropServices;
using System.Runtime.ExceptionServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Threading;
using System.Windows.Automation;
using DobbyVPN.Windows;
using Forms = System.Windows.Forms;

internal static partial class Program
{
    private const double LogsCaseCleanupSeconds = 10;
    private static readonly JsonSerializerOptions NativeCaseJsonOptions = new()
    {
        NumberHandling = JsonNumberHandling.AllowNamedFloatingPointLiterals,
    };
    private static readonly JsonSerializerOptions IndentedNativeCaseJsonOptions = new()
    {
        NumberHandling = JsonNumberHandling.AllowNamedFloatingPointLiterals,
        WriteIndented = true,
    };

    private static string SerializeNativeCaseJson(object? value, bool indented = false) =>
        JsonSerializer.Serialize(value, indented ? IndentedNativeCaseJsonOptions : NativeCaseJsonOptions);

    private sealed record VisibleLogWindow(string AutomationIds, double FirstVisibleTop);

    private sealed class LogsCaseDeadline(double seconds)
    {
        private readonly long _started = Stopwatch.GetTimestamp();
        public double ElapsedSeconds => Stopwatch.GetElapsedTime(_started).TotalSeconds;
        public double RemainingSeconds => Math.Max(0, seconds - ElapsedSeconds);

        public void Check(string stage)
        {
            if (RemainingSeconds <= 0)
                throw new TimeoutException($"Windows {stage} exceeded the single {seconds.ToString("F3", CultureInfo.InvariantCulture)} second case deadline");
        }
    }

    private sealed class OwnedLogsApp(Process process, string executable, bool ownsMainInstance = true)
    {
        public Process Process { get; } = process;
        public string Executable { get; } = executable;
        public bool OwnsMainInstance { get; } = ownsMainInstance;
        public long StartTicks { get; private set; }
        public bool HasIdentity { get; private set; }
        public bool IsDisposed { get; private set; }
        public IntPtr Window { get; set; }

        public Dictionary<string, object?> Result { get; } = new();

        public void CaptureIdentity()
        {
            Result["pid"] = Process.Id;
            var started = Process.StartTime.ToUniversalTime();
            StartTicks = started.Ticks;
            HasIdentity = true;
            Result["identity"] = StartTicks.ToString(CultureInfo.InvariantCulture);
            Result["process_start_utc"] = started.ToString("O", CultureInfo.InvariantCulture);
            Result["native_ui_cleanup_owner"] = OwnsMainInstance;
            if (OwnsMainInstance) WriteNativeUiChildMarker(this);
        }

        public void Dispose()
        {
            if (IsDisposed) return;
            Process.Dispose();
            IsDisposed = true;
        }
    }

    private static void WriteNativeUiChildMarker(OwnedLogsApp app)
    {
        var configuredPath = Environment.GetEnvironmentVariable("DOBBYVPN_NATIVE_UI_CHILD_PID_FILE");
        if (string.IsNullOrWhiteSpace(configuredPath)) return;
        if (!app.HasIdentity || app.StartTicks <= 0)
            throw new InvalidOperationException("Cannot publish a native UI cleanup marker without a verified process identity");

        var path = Path.GetFullPath(configuredPath);
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        var temporaryPath = path + "." + app.Process.Id.ToString(CultureInfo.InvariantCulture) + ".tmp";
        File.WriteAllText(temporaryPath,
            app.Process.Id.ToString(CultureInfo.InvariantCulture) + "|" + app.StartTicks.ToString(CultureInfo.InvariantCulture) + Environment.NewLine,
            Encoding.ASCII);
        File.Move(temporaryPath, path, overwrite: true);
    }

    private static void ClearNativeUiChildMarker(OwnedLogsApp app)
    {
        if (!app.OwnsMainInstance || !app.HasIdentity || !app.Process.HasExited) return;
        var configuredPath = Environment.GetEnvironmentVariable("DOBBYVPN_NATIVE_UI_CHILD_PID_FILE");
        if (string.IsNullOrWhiteSpace(configuredPath)) return;
        var path = Path.GetFullPath(configuredPath);
        if (!File.Exists(path)) return;
        var expected = app.Process.Id.ToString(CultureInfo.InvariantCulture) + "|" +
            app.StartTicks.ToString(CultureInfo.InvariantCulture);
        if (string.Equals(File.ReadAllText(path).Trim(), expected, StringComparison.Ordinal))
            File.Delete(path);
    }

    private static int RunNativeCase(JsonElement request) => RunWindowsNativeCase(request);

    private static int RunWindowsNativeCase(JsonElement request)
    {
        var resultPath = Path.GetFullPath(request.GetProperty("result_path").GetString()
            ?? throw new ArgumentException("native-case request is missing result_path"));
        var caseId = request.TryGetProperty("case", out var caseValue) && caseValue.ValueKind == JsonValueKind.String
            ? caseValue.GetString() ?? "unknown"
            : "unknown";
        var assertions = new List<object>();
        var screenshots = new List<string>();
        var diagnosticErrors = new List<string>();
        var cleanupErrors = new List<string>();
        var launches = new List<OwnedLogsApp>();
        var result = new Dictionary<string, object?>
        {
            ["case_id"] = caseId,
            ["outcome"] = "not-run",
            ["assertions"] = assertions,
            ["elapsed_seconds"] = 0.0,
            ["setup_seconds"] = 0.0,
            ["cleanup_errors"] = cleanupErrors,
            ["error"] = null,
            ["screenshots"] = screenshots,
            ["diagnostic_errors"] = diagnosticErrors,
            ["app_processes"] = Array.Empty<object>(),
        };
        Directory.CreateDirectory(Path.GetDirectoryName(resultPath)!);
        WriteLogsCaseResult(resultPath, result);

        string appPath;
        string fixturePath;
        string screenshotDirectory;
        double timeoutSeconds;
        try
        {
            appPath = Path.GetFullPath(request.GetProperty("executable").GetString()
                ?? throw new ArgumentException("native case request is missing executable"));
            fixturePath = Path.GetFullPath(request.GetProperty("fixture_log").GetString()
                ?? throw new ArgumentException("native case request is missing fixture_log"));
            screenshotDirectory = Path.GetFullPath(request.GetProperty("screenshot_directory").GetString()
                ?? throw new ArgumentException("native case request is missing screenshot_directory"));
            timeoutSeconds = request.GetProperty("timeout_seconds").GetDouble();
            if (!double.IsFinite(timeoutSeconds) || timeoutSeconds <= 0 || timeoutSeconds > 600)
                throw new ArgumentOutOfRangeException("timeout_seconds", "Windows native-case timeout must be finite and between zero and 600 seconds");
        }
        catch (Exception error)
        {
            result["error"] = error.ToString();
            result["outcome"] = "failed";
            WriteLogsCaseResult(resultPath, result);
            Console.Out.WriteLine(SerializeNativeCaseJson(result));
            ExceptionDispatchInfo.Capture(error).Throw();
            throw;
        }

        var overall = Stopwatch.StartNew();
        var deadline = new LogsCaseDeadline(timeoutSeconds);
        OwnedLogsApp? activeApp = null;
        SubscriptionFixtureStats? fixtureBefore = null;
        string? fixtureControlUrl = null;
        string? fixtureControlKey = null;
        string? fixtureMarker = null;
        Exception? originalFailure = null;
        var setupComplete = false;
        try
        {
            ValidateWindowsNativeCase(caseId);
            deadline.Check("fixture setup");
            if (!File.Exists(appPath)) throw new FileNotFoundException("Windows native-case app executable does not exist", appPath);
            Directory.CreateDirectory(Path.GetDirectoryName(fixturePath)!);
            Directory.CreateDirectory(screenshotDirectory);
            WriteInitialNativeFixture(caseId, fixturePath, out var fixtureMarkers);
            fixtureMarker = caseId == "logs-severity" && fixtureMarkers.Length > 0
                ? "synthetic rendered palette fixture " + fixtureMarkers[0] + "-light-debug"
                : fixtureMarkers.FirstOrDefault();
            result["fixture_log"] = fixturePath;

            if (NeedsSubscriptionFixture(caseId))
            {
                fixtureControlUrl = RequiredSubscriptionControlUrl(request);
                fixtureControlKey = RequiredSubscriptionControlKey(request);
                fixtureBefore = ReadSubscriptionFixtureStats(fixtureControlUrl, fixtureControlKey, deadline);
                AssertLogsCase(assertions, caseId + "-fixture-starts-with-no-subscription-get", 0,
                    fixtureBefore.SubscriptionGets);
                AssertLogsCase(assertions, caseId + "-fixture-starts-with-no-request-in-flight", 0,
                    fixtureBefore.InFlightGets);
            }

            if (caseId == "logs-severity")
            {
                result["setup_seconds"] = overall.Elapsed.TotalSeconds;
                setupComplete = true;
            }
            else
            {
                activeApp = caseId == "imports-cold"
                    ? StartOwnedProtocolApp(appPath, fixturePath, RequiredSubscriptionUrl(request), deadline, launches)
                    : StartOwnedLogsApp(appPath, fixturePath, deadline, launches);
                WaitForLogsViewport(activeApp, deadline);
                result["setup_seconds"] = overall.Elapsed.TotalSeconds;
                setupComplete = true;
            }
            var diagnostics = CreateFixtureDiagnostics(fixturePath);

            switch (caseId)
            {
                case "logs-details":
                    RunLogsDetailsCase(activeApp ?? throw new InvalidOperationException("Details case has no launched app"), diagnostics, fixtureMarkers[0], deadline,
                        assertions, screenshots, screenshotDirectory);
                    break;
                case "logs-freeze-resume":
                    RunLogsFreezeResumeCase(activeApp ?? throw new InvalidOperationException("Freeze case has no launched app"), diagnostics, fixtureMarkers, fixturePath, deadline,
                        assertions, screenshots, screenshotDirectory);
                    break;
                case "logs-clear":
                    RunLogsClearCase(activeApp ?? throw new InvalidOperationException("Clear case has no launched app"), diagnostics, fixtureMarkers[0], fixturePath, deadline,
                        assertions, screenshots, screenshotDirectory);
                    break;
                case "logs-reopen-clear-boundary":
                    RunLogsReopenClearBoundaryCase(ref activeApp, appPath, fixturePath, diagnostics,
                        fixtureMarkers[0], deadline, launches, assertions, screenshots, screenshotDirectory);
                    break;
                case "configure-acceptance":
                    RunSubscriptionAcceptanceCase(activeApp ?? throw new InvalidOperationException("Configure case has no launched app"), RequiredSubscriptionUrl(request), paste: false,
                        deadline, assertions, screenshots, screenshotDirectory);
                    break;
                case "paste-input":
                    RunSubscriptionAcceptanceCase(activeApp ?? throw new InvalidOperationException("Paste case has no launched app"), RequiredSubscriptionUrl(request), paste: true,
                        deadline, assertions, screenshots, screenshotDirectory);
                    break;
                case "subscription-load-performance":
                    result["performance"] = RunSubscriptionLoadPerformanceCase(
                        activeApp ?? throw new InvalidOperationException("Performance case has no launched app"), RequiredSubscriptionUrl(request),
                        RequiredSubscriptionControlUrl(request), RequiredSubscriptionControlKey(request),
                        diagnostics, deadline,
                        assertions, screenshots, screenshotDirectory);
                    break;
                case "imports-cold":
                case "imports-warm":
                    RunProtocolImportCase(activeApp ?? throw new InvalidOperationException("Import case has no launched app"), caseId, RequiredSubscriptionUrl(request),
                        diagnostics, fixtureBefore, fixtureControlUrl, fixtureControlKey, deadline,
                        launches, assertions, screenshots, screenshotDirectory);
                    break;
                case "profiles-layout":
                    RunProfilesLayoutCase(activeApp ?? throw new InvalidOperationException("Profiles case has no launched app"), RequiredSubscriptionUrl(request), deadline,
                        assertions, screenshots, screenshotDirectory);
                    break;
                case "text-size-layout":
                    RunTextSizeLayoutCase(ref activeApp, appPath, fixturePath,
                        RequiredSubscriptionUrl(request), deadline, launches,
                        assertions, cleanupErrors, screenshots, screenshotDirectory);
                    break;
                case "about":
                    RunAboutCase(activeApp ?? throw new InvalidOperationException("About case has no launched app"), request, deadline, assertions, screenshots, screenshotDirectory);
                    break;
                case "connected-session":
                {
                    var observerResults = new List<object>();
                    result["native_observations"] = observerResults;
                    RunConnectedSessionCase(activeApp ?? throw new InvalidOperationException("Connected case has no launched app"), request, appPath, fixturePath, diagnostics,
                        fixtureBefore ?? throw new InvalidOperationException("Connected session case has no initial fixture counters"),
                        RequiredSubscriptionUrl(request), RequiredSubscriptionControlUrl(request),
                        RequiredSubscriptionControlKey(request), deadline, launches, assertions,
                        observerResults, screenshots, screenshotDirectory,
                        current => activeApp = current);
                    break;
                }
                case "connected-layout":
                {
                    var observerResults = new List<object>();
                    result["native_observations"] = observerResults;
                    RunConnectedLayoutCase(activeApp ?? throw new InvalidOperationException("Connected layout case has no launched app"),
                        request, appPath, diagnostics, RequiredSubscriptionUrl(request),
                        RequiredSubscriptionControlUrl(request), RequiredSubscriptionControlKey(request),
                        RequiredSubscriptionProfilePath(request),
                        fixtureBefore ?? throw new InvalidOperationException("Connected layout case has no initial fixture counters"),
                        deadline, launches, assertions, cleanupErrors, observerResults,
                        screenshots, screenshotDirectory);
                    break;
                }
                case "auto-recovery-stop":
                {
                    var observerResults = new List<object>();
                    result["native_observations"] = observerResults;
                    RunAutoRecoveryStopCase(activeApp ?? throw new InvalidOperationException("Auto recovery case has no launched app"),
                        request, RequiredSubscriptionUrl(request), RequiredSubscriptionControlUrl(request),
                        RequiredSubscriptionControlKey(request),
                        deadline, assertions, observerResults, screenshots, screenshotDirectory);
                    break;
                }
                case "logs-severity":
                {
                    result["severity_palette"] = RunWindowsSeverityPaletteCase(
                        appPath, fixturePath, fixtureMarkers[0], diagnostics, deadline, launches,
                        assertions, screenshots, screenshotDirectory,
                        current => activeApp = current);
                    break;
                }
                default:
                    throw new ArgumentOutOfRangeException(nameof(caseId), caseId, "Unsupported Windows native case");
            }

            if (caseId is not ("subscription-load-performance" or "connected-session" or "connected-layout" or "logs-severity") &&
                fixtureBefore is not null && fixtureControlUrl is not null && fixtureControlKey is not null)
            {
                var expectedGets = fixtureBefore.SubscriptionGets + 1;
                var fixtureAfter = WaitForSubscriptionFixtureGets(
                    fixtureControlUrl, fixtureControlKey, expectedGets, deadline, TimeSpan.Zero);
                AssertSubscriptionFixtureRequest(assertions, caseId, fixtureBefore, fixtureAfter);
            }
            result["outcome"] = "passed";
        }
        catch (Exception error)
        {
            originalFailure = error;
            if (!setupComplete) result["setup_seconds"] = overall.Elapsed.TotalSeconds;
            result["error"] = error.ToString();
            result["outcome"] = "failed";
            var screenshotApp = activeApp ?? launches.LastOrDefault(launch => launch.Window != IntPtr.Zero);
            if (screenshotApp is not null && screenshotApp.Window != IntPtr.Zero)
            {
                try
                {
                    result["failure_log_control_metadata"] = ReadLogsControlFailureMetadata(
                        screenshotApp, fixturePath, fixtureMarker);
                }
                catch (Exception metadataError)
                {
                    diagnosticErrors.Add("Failure log control metadata: " + metadataError);
                }
                try
                {
                    Directory.CreateDirectory(screenshotDirectory);
                    var path = Path.Combine(screenshotDirectory, caseId + "-failure.png");
                    Capture(screenshotApp.Window, screenshotApp.Process.Id, path);
                    screenshots.Add(path);
                }
                catch (Exception screenshotError)
                {
                    diagnosticErrors.Add("Failure screenshot: " + screenshotError);
                }
            }
        }
        finally
        {
            var cleanup = Stopwatch.StartNew();
            var cleanupDeadline = new LogsCaseDeadline(LogsCaseCleanupSeconds);
            foreach (var launch in launches)
            {
                if (launch.IsDisposed) continue;
                try
                {
                    CloseOwnedLogsApp(launch, cleanupDeadline);
                }
                catch (Exception error)
                {
                    cleanupErrors.Add(error.ToString());
                    try { TerminateOwnedLogsAppProcess(launch, cleanupDeadline, "owned app cleanup fallback"); }
                    catch (Exception terminationError) { cleanupErrors.Add(terminationError.ToString()); }
                }
                finally
                {
                    TryRecordLogsAppExit(launch);
                    try { ClearNativeUiChildMarker(launch); }
                    catch (Exception error) { cleanupErrors.Add(error.ToString()); }
                    try { launch.Dispose(); }
                    catch (Exception error) { cleanupErrors.Add(error.ToString()); }
                }
            }
            result["cleanup_seconds"] = cleanup.Elapsed.TotalSeconds;
            result["elapsed_seconds"] = overall.Elapsed.TotalSeconds;
            result["app_processes"] = launches.Select(launch => launch.Result).ToArray();
        }

        Exception? finalFailure = originalFailure;
        if (cleanupErrors.Count > 0)
        {
            var cleanupFailure = new AggregateException("Windows native-case app cleanup failed", cleanupErrors.Select(value => new InvalidOperationException(value)));
            finalFailure = finalFailure is null ? cleanupFailure
                : new AggregateException("Windows native-case assertion and app cleanup both failed", finalFailure, cleanupFailure);
            result["outcome"] = "failed";
            result["error"] = finalFailure.ToString();
        }
        if (finalFailure is null) result["outcome"] = "passed";

        try
        {
            WriteLogsCaseResult(resultPath, result);
            Console.Out.WriteLine(SerializeNativeCaseJson(result));
            Console.Out.Flush();
        }
        catch (Exception resultError)
        {
            var writeFailure = new IOException($"Could not write Windows native-case result file {resultPath}", resultError);
            finalFailure = finalFailure is null ? writeFailure
                : new AggregateException("Windows logs-case failure and result collection both failed", finalFailure, writeFailure);
            ExceptionDispatchInfo.Capture(finalFailure).Throw();
        }

        if (finalFailure is not null) ExceptionDispatchInfo.Capture(finalFailure).Throw();
        return 0;
    }

    private static bool IsLogsCase(string caseId) =>
        caseId is "logs-details" or "logs-freeze-resume" or "logs-clear" or "logs-reopen-clear-boundary";

    private static void ValidateWindowsNativeCase(string caseId)
    {
        if (!IsLogsCase(caseId) && caseId is not (
            "configure-acceptance" or "paste-input" or "subscription-load-performance" or "imports-cold" or "imports-warm" or
            "profiles-layout" or "text-size-layout" or "about" or "connected-session" or "connected-layout" or
            "auto-recovery-stop" or "logs-severity"))
            throw new ArgumentOutOfRangeException(nameof(caseId), caseId, "Unsupported Windows native case");
    }

    private static void WriteInitialNativeFixture(string caseId, string path, out string[] markers)
    {
        var uiPath = FixtureUiPath(path);
        var sidecars = new[]
        {
            path, path + ".previous", path + ".lock", path + ".stderr", path + ".stderr.previous", path + ".stderr.lock",
            uiPath, uiPath + ".previous", uiPath + ".lock", uiPath + ".stderr", uiPath + ".stderr.previous", uiPath + ".stderr.lock",
            uiPath + ".view", uiPath + ".view.tmp",
        };
        var existing = sidecars.FirstOrDefault(File.Exists);
        if (existing is not null)
            throw new IOException($"Windows native case requires a new fixture path; existing file: {existing}");

        if (!IsLogsCase(caseId))
        {
            markers = caseId == "logs-severity"
                ? ["dobby-windows-palette-" + Guid.NewGuid().ToString("N")]
                : Array.Empty<string>();
            using var empty = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
            empty.Flush(true);
            return;
        }

        var prefix = "dobby-windows-" + caseId + "-" + Guid.NewGuid().ToString("N");
        if (caseId == "logs-freeze-resume")
        {
            markers = Enumerable.Range(0, 180).Select(index => $"{prefix}-row-{index:D3}-" + new string('x', 90)).ToArray();
        }
        else
        {
            markers = [$"{prefix}-before-clear"];
        }

        var firstTimestamp = DateTimeOffset.UtcNow.AddSeconds(-markers.Length);
        using var output = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.Read);
        foreach (var (marker, index) in markers.Select((marker, index) => (marker, index)))
        {
            var record = CreateLogsFixtureRecord(caseId, marker, firstTimestamp.AddSeconds(index));
            var bytes = Encoding.UTF8.GetBytes(record + "\n");
            output.Write(bytes);
        }
        output.Flush(true);
    }

    private static string CreateLogsFixtureRecord(string caseId, string marker, DateTimeOffset timestamp) =>
        JsonSerializer.Serialize(new
        {
            schema = "dobby.log/v1",
            timestamp = timestamp.ToString("O", CultureInfo.InvariantCulture),
            source = "windows-test-fixture",
            level = "INFO",
            @event = "torturer.fixture",
            message = marker,
            fields = new { case_id = caseId, fixture = true },
        });

    private static void AppendLogsFixtureRecord(string fixturePath, string caseId, string marker)
    {
        var record = CreateLogsFixtureRecord(caseId, marker, DateTimeOffset.UtcNow);
        NativeLogFiles.Append(fixturePath, Encoding.UTF8.GetBytes(record + "\n"));
    }

    private static NativeDiagnostics CreateFixtureDiagnostics(string fixturePath)
    {
        return new NativeDiagnostics(fixturePath, FixtureUiPath(fixturePath));
    }

    private static string FixtureUiPath(string fixturePath) =>
        Path.Combine(Path.GetDirectoryName(fixturePath)!, "ui.jsonl");

    private static NativeDiagnostics.Entry ReadFixtureEntry(NativeDiagnostics diagnostics, string marker)
    {
        var read = diagnostics.EntriesAsync().GetAwaiter().GetResult();
        if (!string.IsNullOrEmpty(read.Error)) throw new IOException("Could not read the Windows logs-case fixture: " + read.Error);
        return read.Entries.SingleOrDefault(entry => entry.Message == marker)
            ?? throw new InvalidOperationException($"Windows logs-case fixture record was not readable: {marker}");
    }

    private static OwnedLogsApp StartOwnedLogsApp(
        string executable, string fixturePath, LogsCaseDeadline deadline, List<OwnedLogsApp> launches,
        string? requestedTheme = null)
    {
        deadline.Check("candidate app launch");
        RequireNoExistingLogsApp(executable);
        var startInfo = new ProcessStartInfo(executable)
        {
            UseShellExecute = false,
            WorkingDirectory = Path.GetDirectoryName(executable)!,
        };
        startInfo.Environment["DOBBY_LOG_PATH"] = fixturePath;
        startInfo.Environment["DOBBY_UI_LOG_PATH"] = FixtureUiPath(fixturePath);
        startInfo.Environment.Remove("DOBBYVPN_TEST_REQUESTED_THEME");
        if (requestedTheme is not null)
        {
            if (requestedTheme is not ("Light" or "Dark"))
                throw new ArgumentOutOfRangeException(nameof(requestedTheme), requestedTheme, "Requested native test theme must be Light or Dark");
            startInfo.Environment["DOBBYVPN_TEST_REQUESTED_THEME"] = requestedTheme;
        }
        var process = Process.Start(startInfo)
            ?? throw new InvalidOperationException("Windows logs-case could not start the candidate app process");
        var app = new OwnedLogsApp(process, executable);
        launches.Add(app);
        try { app.CaptureIdentity(); }
        catch (Exception error)
        {
            app.Result["identity_error"] = error.ToString();
            throw;
        }

        WaitLogsCase(deadline, "app launch", () =>
        {
            if (process.HasExited)
                throw new InvalidOperationException($"Candidate UI process exited during launch with code {process.ExitCode}");
            process.Refresh();
            var window = process.MainWindowHandle;
            if (window == IntPtr.Zero)
                window = EnumerateProcessWindows(process).FirstOrDefault(candidate => IsWindowVisible(candidate) && !IsIconic(candidate));
            if (window == IntPtr.Zero) return false;
            GetWindowThreadProcessId(window, out var ownerPid);
            if (ownerPid != process.Id) throw new InvalidOperationException("Candidate UI window owner changed during logs-case launch");
            if (!IsWindowVisible(window) || IsIconic(window)) return false;
            app.Window = window;
            app.Result["window_handle"] = $"0x{window.ToInt64():X}";
            return true;
        }, "Candidate UI did not expose a visible window before the logs-case deadline");
        return app;
    }

    private static void RequireNoExistingLogsApp(string executable)
    {
        var processName = Path.GetFileNameWithoutExtension(executable);
        foreach (var process in Process.GetProcessesByName(processName))
        {
            using (process)
            {
                try
                {
                    if (process.HasExited) continue;
                    var path = process.MainModule?.FileName;
                    if (string.Equals(path, executable, StringComparison.OrdinalIgnoreCase))
                        throw new InvalidOperationException(
                            $"Windows logs-case requires a cold app launch; exact candidate process {process.Id} is already running");
                }
                catch (ArgumentException)
                {
                    // A process may exit between enumeration and identity read.
                }
            }
        }
    }

    private static void WaitForLogsViewport(OwnedLogsApp app, LogsCaseDeadline deadline)
    {
        WaitLogsCase(deadline, "window readiness", () =>
        {
            var root = AutomationElement.FromHandle(app.Window);
            var viewport = FindUniqueAutomationId(root, "Backend logs");
            return viewport is not null && viewport.Current.IsControlElement &&
                !viewport.Current.IsOffscreen && HasUsableBounds(viewport.Current.BoundingRectangle);
        }, "Candidate UI did not expose the visible Backend logs control before the case deadline");
    }

    private static string RequiredSubscriptionUrl(JsonElement request)
    {
        var source = request.TryGetProperty("subscription_url", out var value) && value.ValueKind == JsonValueKind.String
            ? value.GetString()
            : null;
        if (string.IsNullOrWhiteSpace(source) || !Uri.TryCreate(source, UriKind.Absolute, out var uri) ||
            uri.Scheme != Uri.UriSchemeHttps || string.IsNullOrWhiteSpace(uri.Host))
            throw new ArgumentException("Windows native case requires an absolute HTTPS subscription_url");
        return source;
    }

    private static string RequiredSubscriptionControlUrl(JsonElement request)
    {
        var value = request.TryGetProperty("subscription_control_url", out var controlValue) &&
            controlValue.ValueKind == JsonValueKind.String ? controlValue.GetString() : null;
        if (string.IsNullOrWhiteSpace(value) || !Uri.TryCreate(value, UriKind.Absolute, out var uri) ||
            uri.Scheme != Uri.UriSchemeHttps || string.IsNullOrWhiteSpace(uri.Host))
            throw new ArgumentException("Windows subscription native case requires an absolute HTTPS subscription_control_url");
        return value;
    }

    private static string RequiredSubscriptionControlKey(JsonElement request)
    {
        var value = request.TryGetProperty("subscription_control_key", out var keyValue) &&
            keyValue.ValueKind == JsonValueKind.String ? keyValue.GetString() : null;
        return !string.IsNullOrWhiteSpace(value)
            ? value
            : throw new ArgumentException("Windows subscription native case requires subscription_control_key");
    }

    private static string RequiredSubscriptionProfilePath(JsonElement request)
    {
        var value = request.TryGetProperty("subscription_profile_path", out var profileValue) &&
            profileValue.ValueKind == JsonValueKind.String ? profileValue.GetString() : null;
        return !string.IsNullOrWhiteSpace(value)
            ? Path.GetFullPath(value)
            : throw new ArgumentException("Windows connected layout case requires subscription_profile_path");
    }

    private static bool NeedsSubscriptionFixture(string caseId) => caseId is
        "configure-acceptance" or "paste-input" or "imports-cold" or "imports-warm" or
        "profiles-layout" or "text-size-layout" or "connected-session" or "connected-layout" or "auto-recovery-stop";

    private static void AssertSubscriptionFixtureRequest(
        List<object> assertions, string caseId,
        SubscriptionFixtureStats before, SubscriptionFixtureStats after)
    {
        AssertLogsCase(assertions, caseId + "-fixture-observed-one-subscription-get",
            before.SubscriptionGets + 1, after.SubscriptionGets);
        AssertLogsCase(assertions, caseId + "-fixture-maximum-concurrency-is-one", 1L,
            after.MaximumInFlightGets);
        AssertLogsCase(assertions, caseId + "-fixture-has-no-request-in-flight-at-completion", 0L,
            after.InFlightGets);
    }

    private static void WaitTextSizeSettings(
        Func<bool> condition, string message, LogsCaseDeadline? deadline, double seconds)
    {
        if (deadline is null) WaitFor(condition, message, seconds);
        else WaitLogsCase(deadline, "Windows Text size Settings", condition, message);
    }

    private static void RunSubscriptionAcceptanceCase(
        OwnedLogsApp app, string subscriptionUrl, bool paste, LogsCaseDeadline deadline,
        List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        ApplySubscriptionThroughRenderedControl(app, subscriptionUrl, paste, deadline);
        var layout = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 1);
        AssertLogsCase(assertions, "subscription-field-renders-requested-url", subscriptionUrl,
            ReadSourceEditor(app));
        AssertProfileRowsHaveSemanticIds(assertions, layout);
        var rows = ProfileRows(layout);
        AssertLogsCase(assertions, "accepted-profile-inventory-is-nonempty", true, rows.Length > 0);
        AddScreenshot(app, paste ? "paste-input" : "configure-acceptance", "accepted", deadline,
            screenshots, screenshotDirectory);
    }

    private static Dictionary<string, object?> RunSubscriptionLoadPerformanceCase(
        OwnedLogsApp app, string subscriptionUrl, string controlUrl, string controlKey,
        NativeDiagnostics diagnostics, LogsCaseDeadline deadline,
        List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        const int warmupCount = 5;
        const int sampleCount = 30;
        const double historicalReferenceMilliseconds = 1500;
        var warmups = new List<double>(warmupCount);
        var samples = new List<double>(sampleCount);
        var observedSources = new List<string>(warmupCount + sampleCount);
        var maximumClockUncertaintyMilliseconds = 0.0;
        var fixtureBefore = ReadSubscriptionFixtureStats(controlUrl, controlKey, deadline);

        for (var sampleIndex = 0; sampleIndex < warmupCount + sampleCount; sampleIndex++)
        {
            deadline.Check("subscription load performance sample");
            var source = WithPerformanceSample(subscriptionUrl, sampleIndex);
            MonotonicClockPoint? start = null;
            ApplySubscriptionThroughRenderedControl(app, source, paste: true, deadline,
                beforePasteInvoke: () => start = CaptureMonotonicClockPoint());
            WaitForConfigureResponse(diagnostics, source, deadline);
            WaitForAcceptedRenderedAction(app, source, deadline);
            var end = CaptureMonotonicClockPoint();
            var started = start ?? throw new InvalidOperationException("Paste button invocation timestamp was not captured");
            var elapsedMilliseconds = Stopwatch.GetElapsedTime(started.Timestamp, end.Timestamp).TotalMilliseconds;
            var uncertaintyMilliseconds = started.Uncertainty.TotalMilliseconds + end.Uncertainty.TotalMilliseconds;
            maximumClockUncertaintyMilliseconds = Math.Max(maximumClockUncertaintyMilliseconds, uncertaintyMilliseconds);
            observedSources.Add(source);
            if (sampleIndex < warmupCount) warmups.Add(elapsedMilliseconds);
            else samples.Add(elapsedMilliseconds);
        }

        var uniqueSources = observedSources.Distinct(StringComparer.Ordinal).Count();
        AssertLogsCase(assertions, "performance-paste-ran-five-warmups-and-thirty-fresh-configures",
            warmupCount + sampleCount, uniqueSources);
        AssertLogsCase(assertions, "performance-measured-sample-count", sampleCount, samples.Count);
        var fixtureAfter = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureBefore.SubscriptionGets + warmupCount + sampleCount,
            deadline, TimeSpan.Zero);
        var fixtureGetDelta = fixtureAfter.SubscriptionGets - fixtureBefore.SubscriptionGets;
        AssertLogsCase(assertions, "performance-fixture-observed-one-subscription-get-per-sample",
            warmupCount + sampleCount, fixtureGetDelta);
        AssertLogsCase(assertions, "performance-fixture-had-no-concurrent-subscription-gets", 1,
            fixtureAfter.MaximumInFlightGets);
        AssertLogsCase(assertions, "performance-fixture-has-no-in-flight-get-at-completion", 0,
            fixtureAfter.InFlightGets);
        var ordered = samples.OrderBy(value => value).ToArray();
        var medianMilliseconds = (ordered[(sampleCount / 2) - 1] + ordered[sampleCount / 2]) / 2.0;
        var p95Milliseconds = ordered[(int)Math.Ceiling(sampleCount * 0.95) - 1];
        var maximumMilliseconds = ordered[^1];
        var performance = new Dictionary<string, object?>
        {
            ["measurement_boundary"] = "Rendered Paste button invocation to Configure response and enabled semantic profile action",
            ["warmup_count"] = warmupCount,
            ["warmup_samples_milliseconds"] = warmups,
            ["sample_count"] = sampleCount,
            ["raw_samples_milliseconds"] = samples,
            ["median_milliseconds"] = medianMilliseconds,
            ["p95_milliseconds"] = p95Milliseconds,
            ["p95_method"] = "Nearest rank",
            ["max_milliseconds"] = maximumMilliseconds,
            ["max_clock_uncertainty_milliseconds"] = maximumClockUncertaintyMilliseconds,
            ["clock_uncertainty_method"] = "Each Stopwatch timestamp uses the greater of one Stopwatch tick and half the bracketing UTC-read interval; start and end bounds are added.",
            ["historical_reference_milliseconds"] = historicalReferenceMilliseconds,
            ["historical_reference_is_nonblocking"] = true,
            ["median_vs_historical_reference"] = medianMilliseconds / historicalReferenceMilliseconds,
            ["successful_configure_source_count"] = uniqueSources,
            ["fixture_gets_before"] = fixtureBefore.SubscriptionGets,
            ["fixture_gets_after"] = fixtureAfter.SubscriptionGets,
            ["fixture_maximum_in_flight_gets"] = fixtureAfter.MaximumInFlightGets,
        };
        AddScreenshot(app, "subscription-load-performance", "complete", deadline,
            screenshots, screenshotDirectory);
        return performance;
    }

    private static string WithPerformanceSample(string subscriptionUrl, int sampleIndex)
    {
        var separator = subscriptionUrl.IndexOf('?') >= 0 ? "&" : "?";
        return subscriptionUrl + separator + "dobby_native_sample=" + sampleIndex.ToString(CultureInfo.InvariantCulture);
    }

    private readonly record struct MonotonicClockPoint(long Timestamp, TimeSpan Uncertainty);

    private static MonotonicClockPoint CaptureMonotonicClockPoint()
    {
        var before = DateTimeOffset.UtcNow;
        var timestamp = Stopwatch.GetTimestamp();
        var after = DateTimeOffset.UtcNow;
        var bracketTicks = Math.Abs((after - before).Ticks) / 2;
        var wallClockBound = TimeSpan.FromTicks(bracketTicks);
        var stopwatchTickBound = TimeSpan.FromSeconds(1.0 / Stopwatch.Frequency);
        return new MonotonicClockPoint(timestamp,
            wallClockBound > stopwatchTickBound ? wallClockBound : stopwatchTickBound);
    }

    private sealed record SubscriptionFixtureStats(long SubscriptionGets, long InFlightGets, long MaximumInFlightGets);

    private static SubscriptionFixtureStats ReadSubscriptionFixtureStats(
        string controlUrl, string controlKey, LogsCaseDeadline deadline)
    {
        deadline.Check("subscription fixture count observation");
        var timeoutSeconds = Math.Min(10, deadline.RemainingSeconds);
        if (timeoutSeconds <= 0) deadline.Check("subscription fixture count observation");
        using var client = new HttpClient
        {
            Timeout = TimeSpan.FromSeconds(timeoutSeconds),
        };
        using var request = new HttpRequestMessage(HttpMethod.Get, controlUrl);
        request.Headers.Add("X-DobbyVPN-Torturer-Key", controlKey);
        using var response = client.Send(request);
        deadline.Check("subscription fixture count observation");
        response.EnsureSuccessStatusCode();
        using var document = JsonDocument.Parse(response.Content.ReadAsStringAsync().GetAwaiter().GetResult());
        long ReadCounter(string name)
        {
            if (!document.RootElement.TryGetProperty(name, out var value) || !value.TryGetInt64(out var counter))
                throw new InvalidDataException($"Windows subscription fixture control response omitted integer {name}");
            return counter;
        }
        return new SubscriptionFixtureStats(
            ReadCounter("subscription_gets"),
            ReadCounter("in_flight_gets"),
            ReadCounter("max_in_flight_gets"));
    }

    private static SubscriptionFixtureStats WaitForSubscriptionFixtureGets(
        string controlUrl, string controlKey, long expectedGets,
        LogsCaseDeadline deadline, TimeSpan quietDuration)
    {
        var quietUntil = 0L;
        SubscriptionFixtureStats? latest = null;
        while (deadline.RemainingSeconds > 0)
        {
            latest = ReadSubscriptionFixtureStats(controlUrl, controlKey, deadline);
            if (latest.SubscriptionGets > expectedGets)
                throw new InvalidOperationException(
                    $"Windows subscription fixture observed duplicate Configure requests: expected={expectedGets}, actual={latest.SubscriptionGets}");
            if (latest.SubscriptionGets == expectedGets && latest.InFlightGets == 0)
            {
                if (quietDuration <= TimeSpan.Zero) return latest;
                if (quietUntil == 0)
                    quietUntil = Stopwatch.GetTimestamp() +
                        (long)Math.Ceiling(quietDuration.TotalSeconds * Stopwatch.Frequency);
                if (Stopwatch.GetTimestamp() >= quietUntil) return latest;
            }
            else
            {
                quietUntil = 0;
            }
            deadline.Check("subscription fixture request completion");
            var sleepMilliseconds = quietUntil == 0
                ? 50
                : Math.Max(1, Math.Min(50, (int)Math.Ceiling(
                    (quietUntil - Stopwatch.GetTimestamp()) * 1000.0 / Stopwatch.Frequency)));
            Thread.Sleep(sleepMilliseconds);
        }
        throw new TimeoutException(
            $"Windows subscription fixture did not observe exactly {expectedGets} completed GETs before the case deadline; " +
            $"last={SerializeNativeCaseJson(latest)}");
    }

    private static void WaitForConfigureResponse(
        NativeDiagnostics diagnostics, string source, LogsCaseDeadline deadline)
    {
        WaitLogsCase(deadline, "subscription Configure response", () =>
        {
            var read = diagnostics.EntriesAsync().GetAwaiter().GetResult();
            if (!string.IsNullOrEmpty(read.Error))
                throw new IOException("Could not read the Windows UI diagnostics during the subscription load performance case: " + read.Error);
            foreach (var entry in read.Entries)
            {
                try
                {
                    using var record = JsonDocument.Parse(entry.Raw);
                    var root = record.RootElement;
                    if (root.ValueKind != JsonValueKind.Object ||
                        !root.TryGetProperty("event", out var eventValue) || eventValue.ValueKind != JsonValueKind.String ||
                        eventValue.GetString() != "subscription.configure-pipe-response-read" ||
                        !root.TryGetProperty("fields", out var fields) || fields.ValueKind != JsonValueKind.Object ||
                        !fields.TryGetProperty("parameters", out var parameters) || parameters.ValueKind != JsonValueKind.Object ||
                        !parameters.TryGetProperty("source", out var sourceValue) || sourceValue.ValueKind != JsonValueKind.String ||
                        sourceValue.GetString() != source)
                        continue;
                    if (!fields.TryGetProperty("succeeded", out var succeeded) || succeeded.ValueKind != JsonValueKind.True)
                        throw new IOException($"Configure returned no response for performance sample URL {source}");
                    return true;
                }
                catch (JsonException)
                {
                    // Raw text records are valid log entries but have no structured Configure event.
                }
            }
            return false;
        }, $"Configure did not return a response for performance sample URL {source}");
    }

    private static void WaitForAcceptedRenderedAction(OwnedLogsApp app, string source, LogsCaseDeadline deadline)
    {
        WaitLogsCase(deadline, "rendered subscription acceptance", () =>
        {
            var root = AutomationElement.FromHandle(app.Window);
            var editor = FindUniqueAutomationId(root, "Connection configuration");
            if (editor is null || !editor.TryGetCurrentPattern(ValuePattern.Pattern, out var pattern) ||
                ((ValuePattern)pattern).Current.Value != source)
                return false;
            var layout = ProfileListLayout(root, app.Window);
            var rows = ProfileRows(layout);
            if (rows.Length == 0 || string.IsNullOrWhiteSpace(layout.GetValueOrDefault("profile_digest") as string))
                return false;
            var actionId = rows[0].GetValueOrDefault("action_automation_id") as string;
            if (string.IsNullOrWhiteSpace(actionId))
                throw new InvalidOperationException("Rendered profile row had no semantic action identifier after Configure");
            var action = FindUniqueAutomationId(root, actionId);
            return action is not null && action.Current.IsEnabled;
        }, "Configure response did not become an accepted, enabled rendered profile action");
    }

    private static void ApplySubscriptionThroughRenderedControl(
        OwnedLogsApp app, string subscriptionUrl, bool paste, LogsCaseDeadline deadline,
        Action? beforePasteInvoke = null)
    {
        var root = AutomationElement.FromHandle(app.Window);
        var editor = FindRequiredAutomationId(root, "Connection configuration");
        if (!editor.TryGetCurrentPattern(ValuePattern.Pattern, out var editorPattern))
            throw new InvalidOperationException("Rendered subscription field does not expose ValuePattern");

        if (!paste)
        {
            if (beforePasteInvoke is not null)
                throw new ArgumentException("A Paste invocation callback cannot be used with Configure input", nameof(beforePasteInvoke));
            ((ValuePattern)editorPattern).SetValue(subscriptionUrl);
            return;
        }

        RequireForeground(app.Window, "pasting a subscription URL");
        var previousClipboard = Forms.Clipboard.GetDataObject();
        var savedClipboard = CloneClipboard(previousClipboard);
        Exception? primary = null;
        try
        {
            Forms.Clipboard.SetText(subscriptionUrl, Forms.TextDataFormat.UnicodeText);
            WaitLogsCase(deadline, "Paste availability", () =>
            {
                var current = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), "Paste");
                return current is not null && current.Current.ControlType == ControlType.Button &&
                    current.Current.IsEnabled && !current.Current.IsOffscreen &&
                    current.TryGetCurrentPattern(InvokePattern.Pattern, out _);
            }, "Rendered Paste button did not become available for text clipboard content");
            var pasteButton = FindRequiredAutomationId(AutomationElement.FromHandle(app.Window), "Paste");
            if (!pasteButton.TryGetCurrentPattern(InvokePattern.Pattern, out var invoke))
                throw new InvalidOperationException("Rendered Paste button does not expose InvokePattern");
            beforePasteInvoke?.Invoke();
            ((InvokePattern)invoke).Invoke();
        }
        catch (Exception error)
        {
            primary = error;
            throw;
        }
        finally
        {
            try
            {
                if (previousClipboard is null) Forms.Clipboard.Clear();
                else Forms.Clipboard.SetDataObject(savedClipboard, true);
            }
            catch (Exception cleanupError)
            {
                if (primary is null) throw;
                throw new AggregateException("Windows Paste case and clipboard restoration both failed", primary, cleanupError);
            }
        }
    }

    private static Forms.DataObject CloneClipboard(Forms.IDataObject? source)
    {
        var snapshot = new Forms.DataObject();
        if (source is null) return snapshot;
        foreach (var format in source.GetFormats(false))
        {
            try { snapshot.SetData(format, false, source.GetData(format, false)); }
            catch (Exception error) when (error is ExternalException or InvalidOperationException or ArgumentException)
            {
                throw new InvalidOperationException($"Could not preserve clipboard format {format}", error);
            }
        }
        return snapshot;
    }

    private static string ReadSourceEditor(OwnedLogsApp app)
    {
        var editor = FindRequiredAutomationId(AutomationElement.FromHandle(app.Window), "Connection configuration");
        if (!editor.TryGetCurrentPattern(ValuePattern.Pattern, out var pattern))
            throw new InvalidOperationException("Rendered subscription field does not expose ValuePattern");
        return ((ValuePattern)pattern).Current.Value;
    }

    private static Dictionary<string, object?> WaitForAcceptedProfiles(
        OwnedLogsApp app, string subscriptionUrl, LogsCaseDeadline deadline, int minimumRows)
    {
        Dictionary<string, object?>? latest = null;
        WaitLogsCase(deadline, "subscription profile acceptance", () =>
        {
            var root = AutomationElement.FromHandle(app.Window);
            var editor = FindUniqueAutomationId(root, "Connection configuration");
            if (editor is null || !editor.TryGetCurrentPattern(ValuePattern.Pattern, out var pattern) ||
                !string.Equals(((ValuePattern)pattern).Current.Value, subscriptionUrl, StringComparison.Ordinal))
                return false;
            latest = ProfileListLayout(root, app.Window);
            return ProfileRows(latest).Length >= minimumRows &&
                latest["profile_digest"] is string digest && !string.IsNullOrWhiteSpace(digest);
        }, "Subscription URL did not produce a rendered accepted profile inventory before the case deadline");
        return latest ?? throw new InvalidOperationException("Accepted profile inventory was not captured");
    }

    private static Dictionary<string, object?>[] ProfileRows(Dictionary<string, object?> layout) =>
        layout.TryGetValue("profile_rows", out var value) && value is Dictionary<string, object?>[] rows
            ? rows
            : throw new InvalidOperationException("Native UI profile layout did not return semantic profile rows");

    private static void AssertProfileRowsHaveSemanticIds(
        List<object> assertions, Dictionary<string, object?> layout)
    {
        var rows = ProfileRows(layout);
        var digest = layout["profile_digest"] as string ?? "";
        var valid = rows.Length > 0 && rows.All(row =>
            row.TryGetValue("profile_index", out var indexValue) && indexValue is int index &&
            Equals(row.GetValueOrDefault("profile_digest"), digest) &&
            Equals(row.GetValueOrDefault("description_automation_id"), DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "description")) &&
            Equals(row.GetValueOrDefault("protocol_automation_id"), DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "protocol")) &&
            Equals(row.GetValueOrDefault("action_automation_id"), DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "action")) &&
            !string.IsNullOrWhiteSpace(row.GetValueOrDefault("name") as string) &&
            !string.IsNullOrWhiteSpace(row.GetValueOrDefault("protocol") as string) &&
            !string.IsNullOrWhiteSpace(row.GetValueOrDefault("action") as string));
        AssertLogsCase(assertions, "rendered-profile-rows-use-stable-role-identifiers", true, valid);
    }

    private static void RunProtocolImportCase(
        OwnedLogsApp app, string caseId, string subscriptionUrl,
        NativeDiagnostics diagnostics, SubscriptionFixtureStats? fixtureBefore,
        string? fixtureControlUrl, string? fixtureControlKey, LogsCaseDeadline deadline,
        List<OwnedLogsApp> launches, List<object> assertions, List<string> screenshots,
        string screenshotDirectory)
    {
        Dictionary<string, object?> layout;
        if (caseId == "imports-warm")
        {
            if (fixtureBefore is null || fixtureControlUrl is null || fixtureControlKey is null)
                throw new InvalidOperationException("Warm protocol import has no subscription fixture observation");

            ApplySubscriptionThroughRenderedControl(app, subscriptionUrl, paste: true, deadline);
            var initialLayout = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 1);
            var initialRows = ProfileRows(initialLayout);
            AssertLogsCase(assertions, "warm-import-first-acceptance-renders-nonempty-inventory", true,
                initialRows.Length > 0);
            var initialDigest = initialLayout.GetValueOrDefault("profile_digest") as string ?? "";
            var initialPid = app.Process.Id;
            var initialStartTicks = app.StartTicks;
            var initialWindow = app.Window;
            var expectedGets = fixtureBefore.SubscriptionGets + 1;
            _ = WaitForSubscriptionFixtureGets(
                fixtureControlUrl, fixtureControlKey, expectedGets, deadline, TimeSpan.Zero);

            TrackWarmProtocolActivation(app, subscriptionUrl, deadline, launches, assertions);
            WaitForProtocolImportEvent(diagnostics, subscriptionUrl, deadline);
            VerifyOwnedLogsApp(app);
            uint ownerPid = 0;
            var ownerThread = IsWindow(initialWindow)
                ? GetWindowThreadProcessId(initialWindow, out ownerPid)
                : 0;
            var retainedWindowOwner = ownerThread != 0 && ownerPid == (uint)initialPid;
            AssertLogsCase(assertions, "warm-protocol-import-keeps-exact-candidate-process", initialPid,
                app.Process.Id);
            AssertLogsCase(assertions, "warm-protocol-import-keeps-exact-process-start-identity", initialStartTicks,
                app.StartTicks);
            AssertLogsCase(assertions, "warm-protocol-import-keeps-exact-window-owner", true,
                retainedWindowOwner && initialWindow == app.Window);

            var immediate = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, initialRows.Length);
            DelayLogsCase(deadline, TimeSpan.FromMilliseconds(900), "warm protocol import normal Snapshot poll");
            layout = ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window);
            var rowsAfterPoll = ProfileRows(layout);
            AssertLogsCase(assertions, "warm-protocol-import-preserves-accepted-inventory-digest-after-poll",
                initialDigest, layout.GetValueOrDefault("profile_digest"));
            AssertLogsCase(assertions, "warm-protocol-import-preserves-semantic-row-identifiers-after-poll",
                true, SameProfileAutomationIds(initialLayout, layout));
            AssertLogsCase(assertions, "warm-protocol-import-preserves-rendered-row-count-after-poll",
                initialRows.Length, rowsAfterPoll.Length);
            AssertLogsCase(assertions, "warm-protocol-import-keeps-immediate-inventory-digest",
                initialDigest, immediate.GetValueOrDefault("profile_digest"));
            _ = WaitForSubscriptionFixtureGets(
                fixtureControlUrl, fixtureControlKey, expectedGets, deadline,
                TimeSpan.FromMilliseconds(800));
        }
        else
        {
            layout = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 1);
        }
        AssertLogsCase(assertions, "os-protocol-activation-renders-imported-url", subscriptionUrl,
            ReadSourceEditor(app));
        AssertProfileRowsHaveSemanticIds(assertions, layout);
        AddScreenshot(app, caseId, "imported", deadline, screenshots, screenshotDirectory);
    }

    private static void TrackWarmProtocolActivation(
        OwnedLogsApp app, string subscriptionUrl, LogsCaseDeadline deadline,
        List<OwnedLogsApp> launches, List<object> assertions)
    {
        var activationStarted = DateTime.UtcNow;
        var activationProcess = LaunchProtocolImport(subscriptionUrl);
        var forwarders = new List<OwnedLogsApp>();
        var seenPids = new HashSet<int> { app.Process.Id };
        if (activationProcess is not null)
        {
            var keepActivationHandle = false;
            try
            {
                var candidate = TrackWarmProtocolProcess(
                    activationProcess, app, activationStarted, launches, forwarders, seenPids);
                keepActivationHandle = candidate is not null;
            }
            finally
            {
                if (!keepActivationHandle && !launches.Any(launch => ReferenceEquals(launch.Process, activationProcess)))
                    activationProcess.Dispose();
            }
        }

        var processName = Path.GetFileNameWithoutExtension(app.Executable);
        foreach (var candidate in Process.GetProcessesByName(processName))
        {
            var keepCandidate = false;
            try
            {
                var tracked = TrackWarmProtocolProcess(
                    candidate, app, activationStarted, launches, forwarders, seenPids);
                keepCandidate = tracked is not null;
            }
            catch (ArgumentException) when (candidate.HasExited)
            {
                // A protocol forwarder can exit between process enumeration and inspection.
            }
            catch (InvalidOperationException) when (candidate.HasExited)
            {
                // A protocol forwarder can exit between process enumeration and identity inspection.
            }
            finally
            {
                if (!keepCandidate && !launches.Any(launch => ReferenceEquals(launch.Process, candidate)))
                    candidate.Dispose();
            }
        }

        foreach (var forwarder in forwarders)
        {
            var forwarderPid = forwarder.Process.Id;
            var forwarderStart = Stopwatch.GetTimestamp();
            while (!forwarder.Process.HasExited && Stopwatch.GetElapsedTime(forwarderStart).TotalSeconds < 5)
            {
                deadline.Check("warm protocol activation forwarder exit");
                Thread.Sleep(50);
            }
            AssertLogsCase(assertions, "warm-protocol-activation-forwarder-exits-after-redirection-" + forwarderPid,
                true, forwarder.Process.HasExited);
            if (!forwarder.Process.HasExited)
                throw new TimeoutException($"Warm protocol activation process {forwarderPid} did not exit after forwarding to the running app");
        }
    }

    private static OwnedLogsApp? TrackWarmProtocolProcess(
        Process candidate, OwnedLogsApp app, DateTime activationStarted,
        List<OwnedLogsApp> launches, List<OwnedLogsApp> forwarders, HashSet<int> seenPids)
    {
        if (candidate.HasExited || seenPids.Contains(candidate.Id)) return null;
        var path = candidate.MainModule?.FileName;
        if (!string.Equals(path, app.Executable, StringComparison.OrdinalIgnoreCase)) return null;
        var started = candidate.StartTime.ToUniversalTime();
        if (started < activationStarted.AddSeconds(-1)) return null;

        seenPids.Add(candidate.Id);
        var tracked = new OwnedLogsApp(candidate, app.Executable, ownsMainInstance: false);
        launches.Add(tracked);
        tracked.Result["launch_kind"] = "warm-os-protocol-activation-forwarder";
        forwarders.Add(tracked);
        try { tracked.CaptureIdentity(); }
        catch (Exception error)
        {
            tracked.Result["identity_error"] = error.ToString();
            throw;
        }
        return tracked;
    }

    private static void WaitForProtocolImportEvent(
        NativeDiagnostics diagnostics, string subscriptionUrl, LogsCaseDeadline deadline)
    {
        var expectedUri = "dobbyvpn://import?url=" + Uri.EscapeDataString(subscriptionUrl);
        WaitLogsCase(deadline, "warm protocol activation delivery", () =>
        {
            var read = diagnostics.EntriesAsync().GetAwaiter().GetResult();
            if (!string.IsNullOrEmpty(read.Error))
                throw new IOException("Could not read Windows UI diagnostics during warm protocol import: " + read.Error);
            foreach (var entry in read.Entries)
            {
                try
                {
                    using var record = JsonDocument.Parse(entry.Raw);
                    var root = record.RootElement;
                    if (!root.TryGetProperty("event", out var eventValue) || eventValue.ValueKind != JsonValueKind.String ||
                        eventValue.GetString() != "activation.import-link" ||
                        !root.TryGetProperty("fields", out var fields) || fields.ValueKind != JsonValueKind.Object ||
                        !fields.TryGetProperty("uri", out var uriValue) || uriValue.ValueKind != JsonValueKind.String)
                        continue;
                    if (string.Equals(uriValue.GetString(), expectedUri, StringComparison.Ordinal)) return true;
                    if (TryImportedSubscription(uriValue.GetString()!, out var imported) &&
                        string.Equals(imported, subscriptionUrl, StringComparison.Ordinal)) return true;
                }
                catch (JsonException)
                {
                    // Valid raw diagnostics can be unstructured or unrelated to activation.
                }
            }
            return false;
        }, "Running Windows app did not record the same URL from OS protocol activation");
    }

    private static bool TryImportedSubscription(string protocolUri, out string subscriptionUrl)
    {
        subscriptionUrl = "";
        if (!Uri.TryCreate(protocolUri, UriKind.Absolute, out var uri) ||
            !string.Equals(uri.Scheme, "dobbyvpn", StringComparison.OrdinalIgnoreCase) ||
            !string.Equals(uri.Host, "import", StringComparison.OrdinalIgnoreCase)) return false;
        var pairs = uri.Query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries);
        if (pairs.Length != 1) return false;
        var pair = pairs[0].Split('=', 2);
        if (pair.Length != 2 || !string.Equals(Uri.UnescapeDataString(pair[0]), "url", StringComparison.Ordinal)) return false;
        subscriptionUrl = Uri.UnescapeDataString(pair[1]);
        return true;
    }

    private static Process? LaunchProtocolImport(string subscriptionUrl)
    {
        var link = "dobbyvpn://import?url=" + Uri.EscapeDataString(subscriptionUrl);
        return Process.Start(new ProcessStartInfo(link) { UseShellExecute = true });
    }

    private static OwnedLogsApp StartOwnedProtocolApp(
        string executable, string fixturePath, string subscriptionUrl,
        LogsCaseDeadline deadline, List<OwnedLogsApp> launches)
    {
        deadline.Check("cold protocol activation");
        RequireNoExistingLogsApp(executable);
        var activationStarted = DateTime.UtcNow;
        var previousBackendPath = Environment.GetEnvironmentVariable("DOBBY_LOG_PATH");
        var previousUiPath = Environment.GetEnvironmentVariable("DOBBY_UI_LOG_PATH");
        OwnedLogsApp? app = null;
        Process? activationProcess = null;
        try
        {
            Environment.SetEnvironmentVariable("DOBBY_LOG_PATH", fixturePath);
            Environment.SetEnvironmentVariable("DOBBY_UI_LOG_PATH", FixtureUiPath(fixturePath));
            activationProcess = LaunchProtocolImport(subscriptionUrl);
        }
        finally
        {
            Environment.SetEnvironmentVariable("DOBBY_LOG_PATH", previousBackendPath);
            Environment.SetEnvironmentVariable("DOBBY_UI_LOG_PATH", previousUiPath);
        }

        if (activationProcess is not null)
        {
            var ownsActivationProcess = false;
            try
            {
                var activationPath = activationProcess.MainModule?.FileName;
                if (string.Equals(activationPath, executable, StringComparison.OrdinalIgnoreCase))
                {
                    app = new OwnedLogsApp(activationProcess, executable);
                    launches.Add(app);
                    ownsActivationProcess = true;
                    app.Result["launch_kind"] = "cold-os-protocol";
                    try { app.CaptureIdentity(); }
                    catch (Exception error)
                    {
                        app.Result["identity_error"] = error.ToString();
                        throw;
                    }
                    if (app.StartTicks < activationStarted.Ticks)
                    {
                        launches.Remove(app);
                        app = null;
                        ownsActivationProcess = false;
                        throw new InvalidOperationException(
                            "Cold protocol activation resolved to an app process that predated the case");
                    }
                }
            }
            finally
            {
                if (!ownsActivationProcess) activationProcess.Dispose();
                activationProcess = null;
            }
        }

        WaitLogsCase(deadline, "cold protocol app window", () =>
        {
            if (app is null)
            {
                var processName = Path.GetFileNameWithoutExtension(executable);
                foreach (var candidate in Process.GetProcessesByName(processName))
                {
                    var keep = false;
                    try
                    {
                        if (candidate.HasExited) continue;
                        var path = candidate.MainModule?.FileName;
                        var started = candidate.StartTime.ToUniversalTime();
                        if (!string.Equals(path, executable, StringComparison.OrdinalIgnoreCase) ||
                            started < activationStarted.AddSeconds(-1)) continue;
                        app = new OwnedLogsApp(candidate, executable);
                        launches.Add(app);
                        app.Result["launch_kind"] = "cold-os-protocol";
                        keep = true;
                        try { app.CaptureIdentity(); }
                        catch (Exception error)
                        {
                            app.Result["identity_error"] = error.ToString();
                            throw;
                        }
                        break;
                    }
                    catch (ArgumentException)
                    {
                        // A process can exit between enumeration and identity inspection.
                    }
                    catch (InvalidOperationException) when (candidate.HasExited)
                    {
                        // A process can exit between enumeration and identity inspection.
                    }
                    finally
                    {
                        if (!keep) candidate.Dispose();
                    }
                }
            }
            if (app is null) return false;
            if (app.Process.HasExited)
                throw new InvalidOperationException($"Cold protocol-activated UI exited during launch with code {app.Process.ExitCode}");
            app.Process.Refresh();
            var window = app.Process.MainWindowHandle;
            if (window == IntPtr.Zero)
                window = EnumerateProcessWindows(app.Process).FirstOrDefault(candidate => IsWindowVisible(candidate) && !IsIconic(candidate));
            if (window == IntPtr.Zero) return false;
            GetWindowThreadProcessId(window, out var ownerPid);
            if (ownerPid != app.Process.Id)
                throw new InvalidOperationException("Cold protocol UI window owner changed during activation");
            if (!IsWindowVisible(window) || IsIconic(window)) return false;
            app.Window = window;
            app.Result["window_handle"] = $"0x{window.ToInt64():X}";
            return true;
        }, "Windows protocol activation did not expose the candidate app window before the case deadline");
        return app ?? throw new InvalidOperationException("Cold protocol activation did not identify its app process");
    }

    private static void RunProfilesLayoutCase(
        OwnedLogsApp app, string subscriptionUrl, LogsCaseDeadline deadline,
        List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        ApplySubscriptionThroughRenderedControl(app, subscriptionUrl, paste: false, deadline);
        var accepted = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 1);
        var digest = accepted.GetValueOrDefault("profile_digest") as string
            ?? throw new InvalidOperationException("Accepted profile inventory has no configuration digest");
        RunRenderedProfileLayoutRows(app, accepted, digest, "profile-layout", "profiles-layout",
            deadline, assertions, screenshots, screenshotDirectory);
    }

    private static void RunRenderedProfileLayoutRows(
        OwnedLogsApp app, Dictionary<string, object?> accepted, string digest,
        string assertionPrefix, string screenshotCaseName, LogsCaseDeadline deadline,
        List<object> assertions, List<string> screenshots, string screenshotDirectory,
        bool requireUsableActions = true)
    {
        AssertProfileLayoutGeometry(assertions, assertionPrefix, accepted, requireReadableLogs: true);
        var observedIndexes = new List<int>();
        for (var index = 0; index < 24; index++)
        {
            var layout = ScrollProfileRowForCase(app, digest, index, 24, deadline, requireEnabled: requireUsableActions);
            var row = AssertRenderedProfileRow(assertions, layout, digest, index, assertionPrefix);
            observedIndexes.Add((int)row["profile_index"]!);
            AssertLogsCase(assertions, $"{assertionPrefix}-row-{index + 1}-keeps-accepted-digest", digest,
                row.GetValueOrDefault("profile_digest"));
            if (requireUsableActions)
            {
                var actionId = (string)row["action_automation_id"]!;
                var action = GetAction(app, actionId);
                var includedInVisibleActions = VisibleActions(layout).Contains(actionId, StringComparer.Ordinal);
                AddLayoutAssertion(assertions, $"{assertionPrefix}-row-{index + 1}-action-is-usable",
                    Equals(row.GetValueOrDefault("action"), "Connect") &&
                    action is { Name: "Connect", Enabled: true, Offscreen: false } && includedInVisibleActions,
                    new { rendered_name = "Connect", enabled = true, fully_visible = true },
                    new
                    {
                        rendered_row_action = row.GetValueOrDefault("action"),
                        rendered_action = action,
                        included_in_visible_actions = includedInVisibleActions,
                    });
            }
            else
                AssertLogsCase(assertions, $"{assertionPrefix}-row-{index + 1}-action-is-visible", true,
                    VisibleActions(layout).Contains((string)row["action_automation_id"]!, StringComparer.Ordinal));
            AddProfileRowOrderAssertion(assertions, index, layout, assertionPrefix);
            if (index == 0)
            {
                AssertLogsCase(assertions, assertionPrefix + "-empty-description-renders-a-meaningful-profile-label", "Profile 1",
                    row.GetValueOrDefault("name"));
                AddScreenshot(app, screenshotCaseName, "first-row", deadline, screenshots, screenshotDirectory);
            }
            if (index == 23)
                AddScreenshot(app, screenshotCaseName, "last-row", deadline, screenshots, screenshotDirectory);
        }
        AssertLogsCase(assertions, assertionPrefix + "-scrolls-through-source-order", Enumerable.Range(0, 24).ToArray(),
            observedIndexes.ToArray());
        AssertLogsCase(assertions, assertionPrefix + "-digest-remains-stable-through-scroll", digest,
            ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window).GetValueOrDefault("profile_digest"));
    }

    private static void RunTextSizeLayoutCase(
        ref OwnedLogsApp? activeApp, string executable, string fixturePath, string subscriptionUrl,
        LogsCaseDeadline deadline, List<OwnedLogsApp> launches, List<object> assertions,
        List<string> cleanupErrors, List<string> screenshots, string screenshotDirectory)
    {
        var app = activeApp ?? throw new InvalidOperationException("Text-size layout case has no owned app process");
        ApplySubscriptionThroughRenderedControl(app, subscriptionUrl, paste: false, deadline);
        var before = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 1);
        var digest = before.GetValueOrDefault("profile_digest") as string
            ?? throw new InvalidOperationException("Text-size layout inventory has no configuration digest");
        AssertProfileLayoutGeometry(assertions, "text-size-before", before, requireReadableLogs: true);
        AddScreenshot(app, "text-size-layout", "before", deadline, screenshots, screenshotDirectory);

        var settingsCleanupDeadline = new LogsCaseDeadline(45);
        var inspect = RunTextSizeSettingsAction(executable, "inspect",
            caseDeadline: deadline, cleanupDeadline: settingsCleanupDeadline);
        RecordSettingsObservation(assertions, "text-size-original-setting-is-available", inspect);
        var originalValue = SettingsTextSizeValue(inspect);
        var original = (int)Math.Round(originalValue);
        if (Math.Abs(originalValue - original) > 0.01)
            throw new InvalidOperationException($"Windows Text size setting is not an integer percentage: {originalValue.ToString(CultureInfo.InvariantCulture)}");
        var range = SettingsTextSizeRange(inspect);
        if (range.Minimum > 150 || range.Maximum < 150)
            throw new InvalidOperationException($"Windows Text size range does not include the required 150% setting: {range.Minimum}..{range.Maximum}");
        var settingsRestored = false;
        try
        {
            var current = original;
            if (current == 150)
            {
                var intermediate = range.Minimum <= 125 && range.Maximum >= 125
                    ? 125
                    : range.Minimum <= 100 && range.Maximum >= 100 ? 100 : (int?)null;
                if (intermediate is null || intermediate == current)
                    throw new InvalidOperationException($"Windows Text size range has no supported integer step away from 150%: {range.Minimum}..{range.Maximum}");
                var intermediateResult = RunTextSizeSettingsAction(executable, "apply", intermediate,
                    current, deadline, settingsCleanupDeadline);
                RecordSettingsObservation(assertions, "text-size-temporary-setting-uses-native-settings", intermediateResult);
                var observedIntermediate = TextSizeResponseValueAfterAction(intermediateResult);
                AssertLogsCase(assertions, "text-size-temporary-step-is-applied-before-150", (double)intermediate.Value, observedIntermediate);
                current = intermediate.Value;
            }

            deadline.Check("Windows Text size 150% apply");
            var changed = RunTextSizeSettingsAction(executable, "apply", 150, current,
                deadline, settingsCleanupDeadline);
            RecordSettingsObservation(assertions, "text-size-setting-applies-exactly-150-through-native-settings", changed);
            AssertLogsCase(assertions, "text-size-native-settings-apply-result-is-150-percent", 150.0,
                TextSizeResponseValueAfterAction(changed));

            var inspect150 = RunTextSizeSettingsAction(executable, "inspect",
                caseDeadline: deadline, cleanupDeadline: settingsCleanupDeadline);
            RecordSettingsObservation(assertions, "text-size-150-setting-remains-available", inspect150);
            AssertLogsCase(assertions, "text-size-os-setting-is-150-percent", 150.0,
                SettingsTextSizeValue(inspect150));

            Dictionary<string, object?>? after = null;
            WaitLogsCase(deadline, "native window reflow after Text size change", () =>
            {
                var current = ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window);
                if (!Equals(current.GetValueOrDefault("profile_digest"), digest))
                    return false;
                if (!ProfileLayoutGeometryValid(current, requireReadableLogs: true)) return false;
                after = current;
                return true;
            }, "Native window layout did not remain rendered after the Windows Text size change");
            if (after is null) throw new InvalidOperationException("Text size reflow did not capture a native layout");
            var changedFirstLayout = ScrollProfileRowForCase(app, digest, 0, 24, deadline);
            var changedFirst = AssertRenderedProfileRow(assertions, changedFirstLayout, digest, 0, "text-size-150-live");
            AssertLogsCase(assertions, "text-size-change-preserves-first-profile-identifiers",
                DobbyVPN.Windows.ProfileAutomationIds.Create(digest, 0, "action"),
                changedFirst.GetValueOrDefault("action_automation_id"));
            AssertLogsCase(assertions, "text-size-change-keeps-controls-inside-native-window", true,
                WindowContainsControlBounds(after));
            AddScreenshot(app, "text-size-layout", "changed", deadline, screenshots, screenshotDirectory);

            CloseOwnedLogsApp(app, deadline);
            var size150App = StartOwnedLogsApp(executable, fixturePath, deadline, launches);
            activeApp = size150App;
            WaitForLogsViewport(size150App, deadline);
            var reopenedAt150 = WaitForAcceptedProfiles(size150App, subscriptionUrl, deadline, minimumRows: 1);
            AssertLogsCase(assertions, "text-size-150-reopen-preserves-accepted-inventory", digest,
                reopenedAt150.GetValueOrDefault("profile_digest"));
            var reopenedSettings150 = RunTextSizeSettingsAction(executable, "inspect",
                caseDeadline: deadline, cleanupDeadline: settingsCleanupDeadline);
            RecordSettingsObservation(assertions, "text-size-150-setting-remains-available-after-reopen", reopenedSettings150);
            AssertLogsCase(assertions, "text-size-reopened-app-runs-at-150-percent", 150.0,
                SettingsTextSizeValue(reopenedSettings150));
            AssertProfileLayoutGeometry(assertions, "text-size-150-reopen", reopenedAt150, requireReadableLogs: true);
            var at150First = ScrollProfileRowForCase(size150App, digest, 0, 24, deadline);
            _ = AssertRenderedProfileRow(assertions, at150First, digest, 0, "text-size-150-reopen");
            AddScreenshot(size150App, "text-size-layout", "reopened-at-150", deadline, screenshots, screenshotDirectory);
            app = size150App;
        }
        finally
        {
            try
            {
                var restoreDeadline = new LogsCaseDeadline(45);
                var restoreCleanupDeadline = new LogsCaseDeadline(15);
                var currentResponse = RunTextSizeSettingsAction(executable, "inspect",
                    caseDeadline: restoreDeadline, cleanupDeadline: restoreCleanupDeadline);
                var currentValue = SettingsTextSizeValue(currentResponse);
                var currentInt = (int)Math.Round(currentValue);
                if (Math.Abs(currentValue - original) > 0.01)
                {
                    if (Math.Abs(currentValue - currentInt) > 0.01)
                        throw new InvalidOperationException($"Windows Text size setting changed to a non-integer value: {currentValue.ToString(CultureInfo.InvariantCulture)}");
                    var restored = RunTextSizeSettingsAction(executable, "apply", original, currentInt,
                        restoreDeadline, restoreCleanupDeadline);
                    RecordSettingsObservation(assertions, "text-size-setting-restored-through-native-settings", restored);
                    if (Math.Abs(TextSizeResponseValueAfterAction(restored) - original) > 0.01)
                        throw new InvalidOperationException($"Windows Text size restore action did not apply {original}%: {restored.GetRawText()}");
                }
                var final = RunTextSizeSettingsAction(executable, "inspect",
                    caseDeadline: restoreDeadline, cleanupDeadline: restoreCleanupDeadline);
                var finalValue = SettingsTextSizeValue(final);
                settingsRestored = SettingsAvailable(final) && Math.Abs(finalValue - original) <= 0.01;
                assertions.Add(new
                {
                    id = "text-size-setting-restored-to-original",
                    outcome = settingsRestored ? "passed" : "failed",
                    expected = original,
                    actual = new { available = SettingsAvailable(final), value = finalValue, response = final },
                });
                if (!settingsRestored)
                    cleanupErrors.Add($"Windows Text size restoration did not return to {original}%: {SerializeNativeCaseJson(final)}");
            }
            catch (Exception restoreError)
            {
                cleanupErrors.Add("Windows Text size restoration: " + restoreError);
                assertions.Add(new
                {
                    id = "text-size-setting-restored-to-original",
                    outcome = "failed",
                    expected = original,
                    actual = restoreError.ToString(),
                });
            }

            if (settingsRestored)
            {
                try
                {
                    var restoreDeadline = new LogsCaseDeadline(45);
                    var currentApp = activeApp;
                    if (currentApp is not null && !currentApp.Process.HasExited)
                        CloseOwnedLogsApp(currentApp, restoreDeadline);
                    activeApp = null;
                    var restoredApp = StartOwnedLogsApp(executable, fixturePath, deadline, launches);
                    activeApp = restoredApp;
                    WaitForLogsViewport(restoredApp, deadline);
                    var restoredLayout = WaitForAcceptedProfiles(restoredApp, subscriptionUrl, deadline, minimumRows: 1);
                    AssertLogsCase(assertions, "text-size-restored-reopen-preserves-accepted-inventory", digest,
                        restoredLayout.GetValueOrDefault("profile_digest"));
                    var reopenedOriginalSettings = RunTextSizeSettingsAction(executable, "inspect",
                        caseDeadline: deadline, cleanupDeadline: new LogsCaseDeadline(15));
                    RecordSettingsObservation(assertions, "text-size-original-setting-remains-available-after-reopen", reopenedOriginalSettings);
                    AssertLogsCase(assertions, "text-size-restored-app-runs-at-original-percent", (double)original,
                        SettingsTextSizeValue(reopenedOriginalSettings));
                    AssertProfileLayoutGeometry(assertions, "text-size-restored-reopen", restoredLayout, requireReadableLogs: true);
                    var restoredFirst = ScrollProfileRowForCase(restoredApp, digest, 0, 24, deadline);
                    _ = AssertRenderedProfileRow(assertions, restoredFirst, digest, 0, "text-size-restored-reopen");
                    AddScreenshot(restoredApp, "text-size-layout", "reopened-at-original", deadline, screenshots, screenshotDirectory);
                }
                catch (Exception reopenError)
                {
                    cleanupErrors.Add("Windows text-size restored-setting reopen: " + reopenError);
                    assertions.Add(new
                    {
                        id = "text-size-restored-setting-reopen",
                        outcome = "failed",
                        expected = new { setting = original, digest },
                        actual = reopenError.ToString(),
                    });
                }
            }
        }
    }

    private static double TextSizeResponseValueAfterAction(JsonElement response) =>
        response.TryGetProperty("valueAfterApply", out var value) && value.TryGetDouble(out var actual)
            ? actual
            : SettingsTextSizeValue(response);

    private static void AssertProfileLayoutGeometry(
        List<object> assertions, string prefix, Dictionary<string, object?> layout, bool requireReadableLogs)
    {
        var hasWindow = TryReadLayoutRect(layout.GetValueOrDefault("window"), out var window);
        var hasControls = TryReadLayoutRect(layout.GetValueOrDefault("controls"), out var controls);
        var hasProfiles = TryReadLayoutRect(layout.GetValueOrDefault("profile_viewport"), out var profiles);
        var hasAction = TryReadLayoutRect(layout.GetValueOrDefault("connection_action"), out var action);
        var hasLogs = TryReadLayoutRect(layout.GetValueOrDefault("logs"), out var logs);
        var geometry = new
        {
            window = layout.GetValueOrDefault("window"),
            controls = layout.GetValueOrDefault("controls"),
            profile_viewport = layout.GetValueOrDefault("profile_viewport"),
            connection_action = layout.GetValueOrDefault("connection_action"),
            logs = layout.GetValueOrDefault("logs"),
        };
        AddLayoutAssertion(assertions, prefix + "-reports-positive-native-bounds",
            hasWindow && hasControls && hasProfiles && hasAction && hasLogs,
            "positive physical-pixel bounds for window, controls, profile viewport, connection action, and logs", geometry);
        AddLayoutAssertion(assertions, prefix + "-controls-fit-inside-window",
            hasWindow && hasControls && ContainsRect(window, controls),
            "connection controls contained within the native window", geometry);
        AddLayoutAssertion(assertions, prefix + "-profile-viewport-fits-inside-controls",
            hasControls && hasProfiles && ContainsRect(controls, profiles),
            "profile viewport contained within connection controls", geometry);
        AddLayoutAssertion(assertions, prefix + "-profile-viewport-does-not-overlap-logs",
            hasProfiles && hasLogs && !OverlapsRect(profiles, logs),
            "profile viewport and logs occupy separate screen bounds", geometry);
        AddLayoutAssertion(assertions, prefix + "-profile-viewport-does-not-overlap-main-action",
            hasProfiles && hasAction && !OverlapsRect(profiles, action),
            "profile viewport and main connection action occupy separate screen bounds", geometry);
        AddLayoutAssertion(assertions, prefix + "-logs-do-not-overlap-main-action",
            hasLogs && hasAction && !OverlapsRect(logs, action),
            "logs and main connection action occupy separate screen bounds", geometry);
        if (requireReadableLogs)
        {
            AddLayoutAssertion(assertions, prefix + "-logs-meet-readable-minimum",
                hasLogs && logs.Width >= 160 && logs.Height >= 120,
                new { minimum_width = 160, minimum_height = 120, coordinate_space = "physical-screen-pixels" }, geometry);
        }
        if (!ProfileLayoutGeometryValid(layout, requireReadableLogs))
            throw new InvalidOperationException($"Windows {prefix} bounds do not meet the rendered layout contract: {SerializeNativeCaseJson(geometry)}");
    }

    private static bool ProfileLayoutGeometryValid(
        Dictionary<string, object?> layout, bool requireReadableLogs)
    {
        if (!TryReadLayoutRect(layout.GetValueOrDefault("window"), out var window) ||
            !TryReadLayoutRect(layout.GetValueOrDefault("controls"), out var controls) ||
            !TryReadLayoutRect(layout.GetValueOrDefault("profile_viewport"), out var profiles) ||
            !TryReadLayoutRect(layout.GetValueOrDefault("connection_action"), out var action) ||
            !TryReadLayoutRect(layout.GetValueOrDefault("logs"), out var logs)) return false;
        return ContainsRect(window, controls) && ContainsRect(window, profiles) && ContainsRect(window, logs) &&
            ContainsRect(controls, profiles) && !OverlapsRect(profiles, logs) &&
            !OverlapsRect(profiles, action) && !OverlapsRect(logs, action) &&
            (!requireReadableLogs || logs.Width >= 160 && logs.Height >= 120);
    }

    private static void AddLayoutAssertion(
        List<object> assertions, string id, bool passed, object expected, object actual)
    {
        assertions.Add(new { id, outcome = passed ? "passed" : "failed", expected, actual });
        if (!passed)
            throw new InvalidOperationException(
                $"Windows native layout assertion {id} failed: expected={SerializeNativeCaseJson(expected)} actual={SerializeNativeCaseJson(actual)}");
    }

    private static bool TryReadLayoutRect(object? value, out System.Windows.Rect rectangle)
    {
        rectangle = default;
        if (value is null) return false;
        var rect = value is JsonElement element ? element : JsonSerializer.SerializeToElement(value);
        if (rect.ValueKind != JsonValueKind.Object ||
            !rect.TryGetProperty("x", out var x) || !x.TryGetDouble(out var left) ||
            !rect.TryGetProperty("y", out var y) || !y.TryGetDouble(out var top) ||
            !rect.TryGetProperty("width", out var width) || !width.TryGetDouble(out var measuredWidth) ||
            !rect.TryGetProperty("height", out var height) || !height.TryGetDouble(out var measuredHeight) ||
            !double.IsFinite(left) || !double.IsFinite(top) ||
            !double.IsFinite(measuredWidth) || !double.IsFinite(measuredHeight) ||
            measuredWidth <= 0 || measuredHeight <= 0) return false;
        rectangle = new System.Windows.Rect(left, top, measuredWidth, measuredHeight);
        return true;
    }

    private static bool ContainsRect(System.Windows.Rect outer, System.Windows.Rect inner) =>
        inner.Left >= outer.Left - 1 && inner.Top >= outer.Top - 1 &&
        inner.Right <= outer.Right + 1 && inner.Bottom <= outer.Bottom + 1;

    private static bool OverlapsRect(System.Windows.Rect first, System.Windows.Rect second) =>
        first.Left < second.Right && first.Right > second.Left &&
        first.Top < second.Bottom && first.Bottom > second.Top;

    private static Dictionary<string, object?> ScrollProfileRowForCase(
        OwnedLogsApp app, string digest, int index, int totalRows, LogsCaseDeadline deadline,
        bool requireEnabled = true)
    {
        if (index < 0 || index >= totalRows || totalRows <= 0)
            throw new ArgumentOutOfRangeException(nameof(index), "Profile row index is outside the controlled inventory");
        var actionId = DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "action");
        var root = AutomationElement.FromHandle(app.Window);
        var viewport = FindRequiredAutomationId(root, "Profile list viewport");
        if (!viewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var viewportPattern))
            throw new InvalidOperationException("Rendered profile list viewport does not expose ScrollPattern");
        var scroll = (ScrollPattern)viewportPattern;
        if (scroll.Current.VerticalScrollPercent < 0)
            throw new InvalidOperationException("Rendered profile list has no vertical scroll range");
        var targetPercent = totalRows == 1 ? 0 : index * 100.0 / (totalRows - 1);
        scroll.SetScrollPercent(ScrollPattern.NoScroll, targetPercent);

        var scrollItemRequested = false;
        WaitLogsCase(deadline, $"profile row {index + 1} realization and scrolling", () =>
        {
            var currentRoot = AutomationElement.FromHandle(app.Window);
            var currentViewport = FindUniqueAutomationId(currentRoot, "Profile list viewport");
            if (currentViewport is null || !HasUsableBounds(currentViewport.Current.BoundingRectangle)) return false;
            var target = FindUniqueAutomationId(currentRoot, actionId);
            if (target is null) return false;
            var current = target.Current;
            var bounds = current.BoundingRectangle;
            var viewportBounds = currentViewport.Current.BoundingRectangle;
            if (current.ControlType != ControlType.Button || current.IsOffscreen ||
                !HasUsableBounds(bounds) || !FullyInside(bounds, viewportBounds))
            {
                if (!scrollItemRequested && target.TryGetCurrentPattern(ScrollItemPattern.Pattern, out var itemPattern))
                {
                    ((ScrollItemPattern)itemPattern).ScrollIntoView();
                    scrollItemRequested = true;
                }
                return false;
            }
            if (requireEnabled && !current.IsEnabled)
                throw new InvalidOperationException($"Rendered profile action is disabled while visible: {actionId}");
            return true;
        }, $"Profile action {actionId} did not become a fully visible enabled button inside the native list viewport");

        var layout = ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window);
        if (!Equals(layout.GetValueOrDefault("profile_digest"), digest) ||
            !VisibleActions(layout).Contains(actionId, StringComparer.Ordinal))
            throw new InvalidOperationException($"Profile row {index + 1} was not reported as fully visible for digest {digest}");
        return layout;
    }

    private static Dictionary<string, object?> AssertRenderedProfileRow(
        List<object> assertions, Dictionary<string, object?> layout, string digest, int index, string prefix = "profiles-layout")
    {
        var rows = ProfileRows(layout);
        var matches = rows.Where(row => row.GetValueOrDefault("profile_index") is int rowIndex && rowIndex == index).ToArray();
        var actual = matches.Length == 1 ? matches[0] : null;
        var expected = new
        {
            profile_index = index,
            index = index + 1,
            profile_digest = digest,
            description_automation_id = DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "description"),
            protocol_automation_id = DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "protocol"),
            action_automation_id = DobbyVPN.Windows.ProfileAutomationIds.Create(digest, index, "action"),
            description_protocol_action_are_meaningful = true,
        };
        var valid = actual is not null &&
            Equals(actual.GetValueOrDefault("index"), index + 1) &&
            Equals(actual.GetValueOrDefault("profile_digest"), digest) &&
            Equals(actual.GetValueOrDefault("description_automation_id"), expected.description_automation_id) &&
            Equals(actual.GetValueOrDefault("protocol_automation_id"), expected.protocol_automation_id) &&
            Equals(actual.GetValueOrDefault("action_automation_id"), expected.action_automation_id) &&
            !string.IsNullOrWhiteSpace(actual.GetValueOrDefault("name") as string) &&
            !string.IsNullOrWhiteSpace(actual.GetValueOrDefault("protocol") as string) &&
            !string.IsNullOrWhiteSpace(actual.GetValueOrDefault("action") as string);
        AddLayoutAssertion(assertions, $"{prefix}-row-{index + 1}-renders-description-protocol-and-action",
            valid, expected, (object?)actual ?? new { observed_matching_rows = matches.Length, realized_rows = rows });
        return actual!;
    }

    private static void AddProfileRowOrderAssertion(
        List<object> assertions, int targetIndex, Dictionary<string, object?> layout,
        string prefix = "profile-layout")
    {
        var indices = ProfileRows(layout).Select(row => row.GetValueOrDefault("profile_index"))
            .OfType<int>().ToArray();
        var ordered = indices.SequenceEqual(indices.OrderBy(index => index));
        AddLayoutAssertion(assertions, $"{prefix}-row-{targetIndex + 1}-preserves-visible-source-order",
            ordered, "visible rows ordered by zero-based source index", indices);
    }

    private static bool SameProfileAutomationIds(
        Dictionary<string, object?> before, Dictionary<string, object?> after)
    {
        if (!Equals(before.GetValueOrDefault("profile_digest"), after.GetValueOrDefault("profile_digest"))) return false;
        var beforeRows = ProfileRows(before);
        var afterRows = ProfileRows(after);
        return beforeRows.Length == afterRows.Length && beforeRows.Zip(afterRows).All(pair =>
            Equals(pair.First.GetValueOrDefault("description_automation_id"), pair.Second.GetValueOrDefault("description_automation_id")) &&
            Equals(pair.First.GetValueOrDefault("protocol_automation_id"), pair.Second.GetValueOrDefault("protocol_automation_id")) &&
            Equals(pair.First.GetValueOrDefault("action_automation_id"), pair.Second.GetValueOrDefault("action_automation_id")));
    }

    private static bool WindowContainsControlBounds(Dictionary<string, object?> layout)
    {
        return TryReadLayoutRect(layout.GetValueOrDefault("window"), out var window) &&
            TryReadLayoutRect(layout.GetValueOrDefault("controls"), out var controls) &&
            TryReadLayoutRect(layout.GetValueOrDefault("profile_viewport"), out var profile) &&
            TryReadLayoutRect(layout.GetValueOrDefault("logs"), out var logs) &&
            ContainsRect(window, controls) && ContainsRect(window, profile) && ContainsRect(window, logs);
    }

    private static JsonElement RunTextSizeSettingsAction(
        string executable, string action, int? target = null, int? expectedCurrent = null,
        LogsCaseDeadline? caseDeadline = null, LogsCaseDeadline? cleanupDeadline = null)
    {
        var request = new Dictionary<string, object?>
        {
            ["operation"] = "settings-text-size",
            ["executable"] = executable,
            ["action"] = action,
            ["uri"] = WindowsTextSizeSettingsUri,
        };
        if (action == "apply")
        {
            request["target"] = target ?? throw new ArgumentNullException(nameof(target));
            request["expectedCurrent"] = expectedCurrent ?? throw new ArgumentNullException(nameof(expectedCurrent));
        }
        using var document = JsonDocument.Parse(JsonSerializer.Serialize(request));
        ValidateSettingsTextSizeRequest(document.RootElement);
        return JsonSerializer.SerializeToElement(ReadWindowsTextSizeSettings(
            document.RootElement, caseDeadline, cleanupDeadline));
    }

    private static bool SettingsAvailable(JsonElement response)
    {
        if (!response.TryGetProperty("available", out var available) || available.ValueKind != JsonValueKind.True)
            return false;
        return !response.TryGetProperty("cleanupErrors", out var cleanup) ||
            cleanup.ValueKind == JsonValueKind.Array && cleanup.GetArrayLength() == 0;
    }

    private static double SettingsTextSizeValue(JsonElement response)
    {
        if (!SettingsAvailable(response) || !response.TryGetProperty("slider", out var slider) ||
            !slider.TryGetProperty("range", out var range) || !range.TryGetProperty("value", out var value) ||
            !value.TryGetDouble(out var current))
            throw new InvalidOperationException("Windows Text size inspection was unavailable: " + response);
        return current;
    }

    private static (double Minimum, double Maximum) SettingsTextSizeRange(JsonElement response)
    {
        var slider = response.GetProperty("slider");
        var range = slider.GetProperty("range");
        return (range.GetProperty("minimum").GetDouble(), range.GetProperty("maximum").GetDouble());
    }

    private static void RequireSettingsAvailable(JsonElement response, string message)
    {
        if (!SettingsAvailable(response))
            throw new InvalidOperationException(message + ": " + response.GetRawText());
    }

    private static void RecordSettingsObservation(List<object> assertions, string id, JsonElement response)
    {
        var available = SettingsAvailable(response);
        assertions.Add(new { id, outcome = available ? "passed" : "failed", expected = true, actual = response });
        if (!available) throw new InvalidOperationException($"Windows native Settings action failed: {response.GetRawText()}");
    }

    private static string[] VisibleActions(Dictionary<string, object?> layout) =>
        layout.GetValueOrDefault("visible_profile_actions") is string[] actions
            ? actions
            : throw new InvalidOperationException("Profile layout did not return visible semantic action identifiers");

    private static bool HasPositiveRect(object? value)
    {
        if (value is null) return false;
        var rect = JsonSerializer.SerializeToElement(value);
        return rect.ValueKind == JsonValueKind.Object &&
            rect.TryGetProperty("width", out var widthValue) && widthValue.TryGetDouble(out var width) && width > 0 &&
            rect.TryGetProperty("height", out var heightValue) && heightValue.TryGetDouble(out var height) && height > 0;
    }

    private static void RunAboutCase(
        OwnedLogsApp app, JsonElement request, LogsCaseDeadline deadline,
        List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        var about = FindRequiredAutomationId(AutomationElement.FromHandle(app.Window), "About");
        if (!about.TryGetCurrentPattern(InvokePattern.Pattern, out var invoke))
            throw new InvalidOperationException("Rendered About button does not expose InvokePattern");
        ((InvokePattern)invoke).Invoke();
        string version = "";
        string compactCommit = "";
        string sourceCommit = "";
        WaitLogsCase(deadline, "About metadata dialog", () =>
        {
            var root = AutomationElement.FromHandle(app.Window);
            version = FindUniqueAutomationId(root, "About version metadata")?.Current.Name ?? "";
            compactCommit = FindUniqueAutomationId(root, "About compact commit metadata")?.Current.Name ?? "";
            sourceCommit = FindUniqueAutomationId(root, "About source commit metadata")?.Current.Name ?? "";
            return !string.IsNullOrWhiteSpace(version) && !string.IsNullOrWhiteSpace(compactCommit) &&
                !string.IsNullOrWhiteSpace(sourceCommit);
        }, "Rendered About dialog did not expose version and source metadata");
        AssertLogsCase(assertions, "about-version-metadata-is-rendered", true, version.StartsWith("Version: ", StringComparison.Ordinal));
        AssertLogsCase(assertions, "about-compact-commit-metadata-is-rendered", true, compactCommit.StartsWith("Commit: ", StringComparison.Ordinal));
        var sourceSha = request.TryGetProperty("source_sha", out var shaValue) && shaValue.ValueKind == JsonValueKind.String
            ? shaValue.GetString()
            : null;
        if (!string.IsNullOrWhiteSpace(sourceSha))
            AssertLogsCase(assertions, "about-source-commit-matches-selected-source", "Source commit: " + sourceSha, sourceCommit);
        else
            AssertLogsCase(assertions, "about-source-commit-is-rendered", true,
                sourceCommit.StartsWith("Source commit: ", StringComparison.Ordinal) && sourceCommit.Length > "Source commit: ".Length);
        AddScreenshot(app, "about", "metadata", deadline, screenshots, screenshotDirectory);
    }

    private readonly record struct PalettePixelMeasurement(
        Color Background, Color Foreground, long DistanceSquared, int ForegroundPixels);

    private static Dictionary<string, object?> RunWindowsSeverityPaletteCase(
        string appPath, string fixturePath, string markerPrefix, NativeDiagnostics diagnostics,
        LogsCaseDeadline deadline, List<OwnedLogsApp> launches, List<object> assertions,
        List<string> screenshots, string screenshotDirectory,
        Action<OwnedLogsApp?> publishActiveApp)
    {
        var levels = new[] { "DEBUG", "INFO", "WARN", "ERROR" };
        var themeEvidence = new Dictionary<string, Dictionary<string, object?>>(StringComparer.Ordinal);
        var themeColors = new Dictionary<string, Dictionary<string, Color>>(StringComparer.Ordinal);

        foreach (var theme in new[] { "Light", "Dark" })
        {
            deadline.Check("start Windows " + theme + " palette app");
            var app = StartOwnedLogsApp(appPath, fixturePath, deadline, launches, requestedTheme: theme);
            publishActiveApp(app);
            WaitForLogsViewport(app, deadline);

            var rows = new Dictionary<string, object?>(StringComparer.Ordinal);
            var colors = new Dictionary<string, Color>(StringComparer.Ordinal);
            foreach (var severity in levels)
            {
                var marker = markerPrefix + "-" + theme.ToLowerInvariant() + "-" + severity.ToLowerInvariant();
                AppendPaletteFixtureRecord(fixturePath, theme, severity, marker);
                var entry = ReadFixtureEntry(diagnostics, marker);
                AssertLogsCase(assertions, $"{theme}-{severity}-fixture-has-expected-severity", severity, entry.Level);
                var rendered = WaitForRenderedPaletteText(app, entry, marker, severity, deadline);
                var capture = CapturePaletteRow(app, theme, severity, rendered, deadline,
                    screenshots, screenshotDirectory);
                var measured = MeasurePalettePixels(capture);
                AssertLogsCase(assertions, $"{theme}-{severity}-row-is-visible-in-native-logs-viewport", true,
                    !rendered.TextElement.Current.IsOffscreen && FullyInside(
                        rendered.TextElement.Current.BoundingRectangle, rendered.ViewportBounds));
                AssertLogsCase(assertions, $"{theme}-{severity}-row-has-visible-foreground-pixels", true,
                    measured.DistanceSquared > 0 && measured.ForegroundPixels >= 3);

                int? uiAutomationColor = null;
                if (rendered.TextElement.TryGetCurrentPattern(TextPattern.Pattern, out var textPattern))
                {
                    var value = ((TextPattern)textPattern).DocumentRange
                        .GetAttributeValue(TextPattern.ForegroundColorAttribute);
                    if (value is int color) uiAutomationColor = color;
                }
                rows[severity] = new Dictionary<string, object?>
                {
                    ["marker"] = marker,
                    ["automation_id"] = LogPresentationState.DetailsAutomationId(entry),
                    ["accessible_name"] = LogPresentationState.DetailsAccessibleName(entry),
                    ["rendered_text"] = rendered.Text,
                    ["bounds"] = RectJson(rendered.Bounds),
                    ["logs_viewport"] = RectJson(rendered.ViewportBounds),
                    ["uia_foreground_colorref"] = uiAutomationColor,
                    ["pixel_background_rgb"] = Rgb(measured.Background),
                    ["pixel_foreground_rgb"] = Rgb(measured.Foreground),
                    ["pixel_distance_squared"] = measured.DistanceSquared,
                    ["foreground_pixel_count"] = measured.ForegroundPixels,
                    ["screenshot"] = screenshots[^1],
                };
                colors[severity] = measured.Foreground;
            }

            var rawMarker = markerPrefix + "-" + theme.ToLowerInvariant() + "-stderr";
            var rawLine = "raw stderr exact bytes " + rawMarker + " café Ω";
            var rawBytes = Encoding.UTF8.GetBytes(rawLine + "\n");
            var previousStderr = File.Exists(fixturePath + ".stderr")
                ? File.ReadAllBytes(fixturePath + ".stderr") : Array.Empty<byte>();
            NativeLogFiles.Append(fixturePath + ".stderr", rawBytes);
            var expectedStderr = new byte[previousStderr.Length + rawBytes.Length];
            Buffer.BlockCopy(previousStderr, 0, expectedStderr, 0, previousStderr.Length);
            Buffer.BlockCopy(rawBytes, 0, expectedStderr, previousStderr.Length, rawBytes.Length);
            AssertLogsCase(assertions, $"{theme}-stderr-fixture-retains-exact-UTF8-bytes", Convert.ToBase64String(expectedStderr),
                Convert.ToBase64String(File.ReadAllBytes(fixturePath + ".stderr")));
            var rawEntry = ReadFixtureEntry(diagnostics, rawLine);
            AssertLogsCase(assertions, $"{theme}-stderr-entry-retains-exact-raw-record", rawLine, rawEntry.Raw);
            var rawRendered = WaitForRenderedRawLogText(app, rawEntry, rawMarker, deadline);
            AssertLogsCase(assertions, $"{theme}-stderr-is-visible-in-native-rendered-log", true,
                rawRendered.Text.Contains(rawLine, StringComparison.Ordinal) &&
                !rawRendered.TextElement.Current.IsOffscreen &&
                FullyInside(rawRendered.TextElement.Current.BoundingRectangle, rawRendered.ViewportBounds));
            rows["RAW"] = new Dictionary<string, object?>
            {
                ["marker"] = rawMarker,
                ["raw_record"] = rawEntry.Raw,
                ["rendered_text"] = rawRendered.Text,
                ["bounds"] = RectJson(rawRendered.TextElement.Current.BoundingRectangle),
                ["logs_viewport"] = RectJson(rawRendered.ViewportBounds),
            };

            var uniqueColors = colors.Values.Select(color => color.ToArgb()).Distinct().Count();
            AssertLogsCase(assertions, $"{theme}-four-severity-rows-have-distinct-rendered-colors", levels.Length, uniqueColors);
            AssertLogsCase(assertions, $"{theme}-DEBUG-is-muted-relative-to-INFO", true,
                ColorDistanceSquared(colors["DEBUG"], RgbBackground(rows["DEBUG"])) <
                ColorDistanceSquared(colors["INFO"], RgbBackground(rows["INFO"])));
            AssertLogsCase(assertions, $"{theme}-WARN-is-amber", true,
                colors["WARN"].R > colors["WARN"].G && colors["WARN"].G > colors["WARN"].B);
            AssertLogsCase(assertions, $"{theme}-ERROR-is-red", true,
                colors["ERROR"].R > colors["ERROR"].G && colors["ERROR"].R > colors["ERROR"].B);

            themeEvidence[theme] = rows;
            themeColors[theme] = colors;
            AddScreenshot(app, "logs-severity", theme.ToLowerInvariant() + "-complete",
                deadline, screenshots, screenshotDirectory);
            CloseOwnedLogsApp(app, deadline);
            app.Dispose();
            publishActiveApp(null);
        }

        AssertLogsCase(assertions, "Light-and-Dark-INFO-colors-differ", true,
            themeColors["Light"]["INFO"].ToArgb() != themeColors["Dark"]["INFO"].ToArgb());

        var normalApp = StartOwnedLogsApp(appPath, fixturePath, deadline, launches);
        publishActiveApp(normalApp);
        WaitForLogsViewport(normalApp, deadline);
        AddScreenshot(normalApp, "logs-severity", "normal-launch", deadline,
            screenshots, screenshotDirectory);
        var normalFrontend = new Dictionary<string, object?>
        {
            ["pid"] = normalApp.Process.Id,
            ["identity"] = normalApp.StartTicks.ToString(CultureInfo.InvariantCulture),
            ["theme_override_cleared"] = true,
            ["window_handle"] = $"0x{normalApp.Window.ToInt64():X}",
        };
        return new Dictionary<string, object?>
        {
            ["themes"] = themeEvidence,
            ["normal_frontend"] = normalFrontend,
        };
    }

    private readonly record struct RenderedPaletteText(
        AutomationElement TextElement, string Text, System.Windows.Rect Bounds,
        System.Windows.Rect ViewportBounds);

    private static void AppendPaletteFixtureRecord(
        string fixturePath, string theme, string severity, string marker)
    {
        var line = JsonSerializer.Serialize(new
        {
            schema = "dobby.log/v1",
            timestamp = DateTimeOffset.UtcNow.ToString("O", CultureInfo.InvariantCulture),
            source = "windows-native-ui-test",
            level = severity,
            @event = "test.rendered-palette",
            message = "synthetic rendered palette fixture " + marker,
            fields = new { case_id = "logs-severity", theme, severity },
        });
        NativeLogFiles.Append(fixturePath, Encoding.UTF8.GetBytes(line + "\n"));
    }

    private static RenderedPaletteText WaitForRenderedPaletteText(
        OwnedLogsApp app, NativeDiagnostics.Entry entry, string marker, string severity,
        LogsCaseDeadline deadline)
    {
        RenderedPaletteText? result = null;
        ScrollLogsToTail(app);
        WaitLogsCase(deadline, "rendered " + severity + " palette row", () =>
        {
            var viewportNow = RequireLogsViewport(app);
            var viewportBounds = PhysicalBounds(viewportNow, "Backend logs");
            var matches = Walk(viewportNow)
                .Where(element => element.Current.ControlType == ControlType.Text)
                .Select(element => (Element: element, Text: ReadAutomationText(element)))
                .Where(item => item.Text.Contains(marker, StringComparison.Ordinal))
                .ToArray();
            if (matches.Length == 0) return false;
            if (matches.Length != 1)
                throw new InvalidOperationException($"Expected one rendered {severity} palette row for {marker}; found {matches.Length}");
            var item = matches[0];
            var heading = item.Text.Split('\n', StringSplitOptions.RemoveEmptyEntries).FirstOrDefault() ?? string.Empty;
            var columns = heading.Split(" · ", StringSplitOptions.None);
            if (columns.Length < 3 || !string.Equals(columns[1], severity, StringComparison.Ordinal))
                throw new InvalidOperationException($"Rendered palette row did not show the expected severity heading: {item.Text}");
            var current = item.Element.Current;
            var bounds = current.BoundingRectangle;
            if (current.IsOffscreen || !HasUsableBounds(bounds) || !FullyInside(bounds, viewportBounds))
                return false;
            var details = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window),
                LogPresentationState.DetailsAutomationId(entry));
            if (details is null || details.Current.Name != LogPresentationState.DetailsAccessibleName(entry))
                throw new InvalidOperationException($"Rendered {severity} palette record omitted its stable Details identity");
            result = new RenderedPaletteText(item.Element, item.Text, bounds, viewportBounds);
            return true;
        }, $"Rendered {severity} palette row was not visible inside the Backend logs viewport");
        return result ?? throw new InvalidOperationException($"Rendered {severity} palette row disappeared after it became visible");
    }

    private static RenderedPaletteText WaitForRenderedRawLogText(
        OwnedLogsApp app, NativeDiagnostics.Entry entry, string marker, LogsCaseDeadline deadline)
    {
        RenderedPaletteText? result = null;
        ScrollLogsToTail(app);
        WaitLogsCase(deadline, "rendered backend stderr row", () =>
        {
            var viewportNow = RequireLogsViewport(app);
            var viewportBounds = PhysicalBounds(viewportNow, "Backend logs");
            var matches = Walk(viewportNow)
                .Where(element => element.Current.ControlType == ControlType.Text)
                .Select(element => (Element: element, Text: ReadAutomationText(element)))
                .Where(item => item.Text.Contains(marker, StringComparison.Ordinal))
                .ToArray();
            if (matches.Length == 0) return false;
            if (matches.Length != 1)
                throw new InvalidOperationException($"Expected one rendered raw stderr row for {marker}; found {matches.Length}");
            var item = matches[0];
            var current = item.Element.Current;
            var bounds = current.BoundingRectangle;
            if (!item.Text.Contains(entry.Message, StringComparison.Ordinal) || current.IsOffscreen ||
                !HasUsableBounds(bounds) || !FullyInside(bounds, viewportBounds))
                return false;
            result = new RenderedPaletteText(item.Element, item.Text, bounds, viewportBounds);
            return true;
        }, "Raw backend stderr record was not visible inside the Backend logs viewport");
        return result ?? throw new InvalidOperationException("Raw backend stderr record disappeared after becoming visible");
    }

    private static string ReadAutomationText(AutomationElement element)
    {
        if (element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern))
            return ((TextPattern)pattern).DocumentRange.GetText(-1).TrimEnd('\r', '\n');
        return element.Current.Name;
    }

    private static void ScrollLogsToTail(OwnedLogsApp app)
    {
        var viewport = RequireLogsViewport(app);
        if (!viewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var pattern))
            throw new InvalidOperationException("Rendered Backend logs control did not expose scrolling");
        var scroll = (ScrollPattern)pattern;
        if (scroll.Current.VerticallyScrollable)
            scroll.SetScrollPercent(ScrollPattern.NoScroll, 100);
    }

    private readonly record struct CapturedPaletteImage(
        string Path, Rectangle ScreenBounds, System.Windows.Rect RowBounds);

    private static CapturedPaletteImage CapturePaletteRow(
        OwnedLogsApp app, string theme, string severity, RenderedPaletteText rendered,
        LogsCaseDeadline deadline, List<string> screenshots, string screenshotDirectory)
    {
        deadline.Check("capture " + theme + " " + severity + " palette row");
        Directory.CreateDirectory(screenshotDirectory);
        var path = Path.Combine(screenshotDirectory,
            $"logs-severity-{theme.ToLowerInvariant()}-{severity.ToLowerInvariant()}.png");
        var screenshotBounds = Capture(app.Window, app.Process.Id, path);
        screenshots.Add(path);
        var rowBounds = rendered.TextElement.Current.BoundingRectangle;
        if (!FullyInside(rowBounds,
                new System.Windows.Rect(screenshotBounds.X, screenshotBounds.Y, screenshotBounds.Width, screenshotBounds.Height)))
            throw new InvalidOperationException($"Rendered {severity} palette row fell outside the captured app window");
        return new CapturedPaletteImage(path, screenshotBounds, rowBounds);
    }

    private static PalettePixelMeasurement MeasurePalettePixels(CapturedPaletteImage capture)
    {
        var left = Math.Max(0, (int)Math.Floor(capture.RowBounds.Left - capture.ScreenBounds.Left));
        var top = Math.Max(0, (int)Math.Floor(capture.RowBounds.Top - capture.ScreenBounds.Top));
        var right = Math.Min(capture.ScreenBounds.Width, (int)Math.Ceiling(capture.RowBounds.Right - capture.ScreenBounds.Left));
        var bottom = Math.Min(capture.ScreenBounds.Height, (int)Math.Ceiling(capture.RowBounds.Bottom - capture.ScreenBounds.Top));
        if (right <= left || bottom <= top)
            throw new InvalidOperationException("Rendered palette row produced an empty screenshot crop");

        using var bitmap = new Bitmap(capture.Path);
        if (bitmap.Width != capture.ScreenBounds.Width || bitmap.Height != capture.ScreenBounds.Height)
            throw new InvalidOperationException("Windows palette screenshot dimensions changed from the captured screen rectangle");
        var counts = new Dictionary<int, int>();
        for (var y = top; y < bottom; y++)
        for (var x = left; x < right; x++)
        {
            var color = bitmap.GetPixel(x, y);
            var key = (color.R << 16) | (color.G << 8) | color.B;
            counts[key] = counts.GetValueOrDefault(key) + 1;
        }
        var backgroundValue = counts.OrderByDescending(pair => pair.Value).First().Key;
        var background = Color.FromArgb(255,
            (backgroundValue >> 16) & 0xff, (backgroundValue >> 8) & 0xff, backgroundValue & 0xff);
        var candidates = counts
            .Where(pair => pair.Value >= 3 && pair.Key != backgroundValue)
            .Select(pair =>
            {
                var color = Color.FromArgb(255,
                    (pair.Key >> 16) & 0xff, (pair.Key >> 8) & 0xff, pair.Key & 0xff);
                return (Color: color, Count: pair.Value, Distance: ColorDistanceSquared(background, color));
            })
            .OrderByDescending(candidate => candidate.Distance)
            .FirstOrDefault();
        if (candidates.Count < 3 || candidates.Distance <= 0)
            throw new InvalidOperationException("Windows palette screenshot crop contained no visible foreground color supported by three pixels");
        return new PalettePixelMeasurement(background, candidates.Color, candidates.Distance, candidates.Count);
    }

    private static object Rgb(Color color) => new[] { (int)color.R, color.G, color.B };

    private static long ColorDistanceSquared(Color left, Color right)
    {
        var red = left.R - right.R;
        var green = left.G - right.G;
        var blue = left.B - right.B;
        return (long)red * red + (long)green * green + (long)blue * blue;
    }

    private static Color RgbBackground(object? row)
    {
        if (row is not Dictionary<string, object?> values ||
            !values.TryGetValue("pixel_background_rgb", out var raw) || raw is not int[] channels || channels.Length != 3)
            throw new InvalidOperationException("Windows palette row did not retain its measured background color");
        return Color.FromArgb(channels[0], channels[1], channels[2]);
    }

    private static void RunLogsDetailsCase(
        OwnedLogsApp app, NativeDiagnostics diagnostics, string marker, LogsCaseDeadline deadline,
        List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        var entry = ReadFixtureEntry(diagnostics, marker);
        var automationId = LogPresentationState.DetailsAutomationId(entry);
        var details = WaitForRenderedDetails(app, entry, deadline);
        AssertLogsCase(assertions, "details-control-automation-id", automationId, details.Current.AutomationId);
        var accessibleName = LogPresentationState.DetailsAccessibleName(entry);
        AssertLogsCase(assertions, "details-control-accessible-name", accessibleName, details.Current.Name);
        AssertLogsCase(assertions, "details-control-name-is-separate", true,
            details.Current.AutomationId != details.Current.Name);

        if (!details.TryGetCurrentPattern(ExpandCollapsePattern.Pattern, out var pattern))
            throw new InvalidOperationException("Rendered log Details control did not expose ExpandCollapsePattern");
        var initialState = ((ExpandCollapsePattern)pattern).Current.ExpandCollapseState;
        AssertLogsCase(assertions, "details-initially-collapsed", ExpandCollapseState.Collapsed.ToString(), initialState.ToString());
        ((ExpandCollapsePattern)pattern).Expand();
        WaitLogsCase(deadline, "Details expansion", () =>
        {
            var current = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), automationId);
            return current is not null && current.TryGetCurrentPattern(ExpandCollapsePattern.Pattern, out var currentPattern) &&
                ((ExpandCollapsePattern)currentPattern).Current.ExpandCollapseState == ExpandCollapseState.Expanded;
        }, "Rendered log Details control did not expand");
        var rawText = FindLogDetailsRawText(app, entry);
        AssertLogsCase(assertions, "details-shows-original-json-record", entry.Raw, rawText);
        AddScreenshot(app, "logs-details", "expanded", deadline, screenshots, screenshotDirectory);
    }

    private static void RunLogsFreezeResumeCase(
        OwnedLogsApp app, NativeDiagnostics diagnostics, string[] fixtureMarkers, string fixturePath,
        LogsCaseDeadline deadline, List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        var initialEntry = ReadFixtureEntry(diagnostics, fixtureMarkers[^1]);
        WaitForRenderedDetails(app, initialEntry, deadline);
        var viewport = RequireLogsViewport(app);
        if (!viewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var scrollPattern))
            throw new InvalidOperationException("Rendered Backend logs control did not expose scrolling");
        var scroll = (ScrollPattern)scrollPattern;
        var initialPercent = scroll.Current.VerticalScrollPercent;
        AssertLogsCase(assertions, "initial-view-follows-tail", 100.0, initialPercent);
        var initialWindow = FirstVisibleLogWindow(app)
            ?? throw new InvalidOperationException("Initial log viewport did not expose a visible structured record");

        RequireForeground(app.Window, "freezing log follow with a wheel input over the rendered log viewport");
        var freezeAssertion = new Dictionary<string, object?>
        {
            ["id"] = "upward-wheel-moves-rendered-log-viewport",
            ["outcome"] = "not-run",
            ["expected"] = new { vertical_percent_decreased = true, visible_window_changed = true },
            ["actual"] = new
            {
                vertical_percent_before = initialPercent,
                visible_window_before = initialWindow,
            },
        };
        assertions.Add(freezeAssertion);
        var frozenPercent = initialPercent;
        VisibleLogWindow? frozenWindowAfterWheel = null;
        SendLogsViewportWheel(app, 120, "freeze log follow", deadline);
        try
        {
            WaitLogsCase(deadline, "log follow freeze", () =>
            {
                frozenPercent = CurrentVerticalScrollPercent(app);
                frozenWindowAfterWheel = FirstVisibleLogWindow(app);
                var percentDecreased = frozenPercent < initialPercent;
                var visibleWindowChanged = frozenWindowAfterWheel is { } currentWindow && currentWindow != initialWindow;
                freezeAssertion["actual"] = new
                {
                    vertical_percent_before = initialPercent,
                    vertical_percent_after = frozenPercent,
                    visible_window_before = initialWindow,
                    visible_window_after = frozenWindowAfterWheel,
                    vertical_percent_decreased = percentDecreased,
                    visible_window_changed = visibleWindowChanged,
                };
                return percentDecreased && visibleWindowChanged;
            }, "Rendered log viewport did not move upward after the wheel input");
            freezeAssertion["outcome"] = "passed";
        }
        catch
        {
            freezeAssertion["outcome"] = "failed";
            throw;
        }
        var frozenWindow = frozenWindowAfterWheel
            ?? throw new InvalidOperationException("Frozen log viewport did not expose a visible structured record");

        var markerPrefix = fixtureMarkers[0].Split("-row-", StringSplitOptions.None)[0];
        var appendedMarkers = new[] { markerPrefix + "-controlled-append-1", markerPrefix + "-controlled-append-2" };
        var appendedEntries = new List<NativeDiagnostics.Entry>();
        for (var index = 0; index < appendedMarkers.Length; index++)
        {
            AppendLogsFixtureRecord(fixturePath, "logs-freeze-resume", appendedMarkers[index]);
            appendedEntries.Add(ReadFixtureEntry(diagnostics, appendedMarkers[index]));
            DelayLogsCase(deadline, TimeSpan.FromMilliseconds(900), "controlled append refresh interval");
            AssertLogsCase(assertions, $"frozen-visible-anchor-stays-stable-after-append-{index + 1}",
                frozenWindow, FirstVisibleLogWindow(app));
        }
        var rootWhileFrozen = AutomationElement.FromHandle(app.Window);
        foreach (var (entry, index) in appendedEntries.Select((entry, index) => (entry, index)))
            AssertLogsCase(assertions, $"controlled-append-{index + 1}-remains-unrendered-while-frozen", null,
                FindUniqueAutomationId(rootWhileFrozen, LogPresentationState.DetailsAutomationId(entry))?.Current.AutomationId);
        AddScreenshot(app, "logs-freeze-resume", "frozen", deadline, screenshots, screenshotDirectory);

        RequireForeground(app.Window, "resuming log follow with a wheel input over the rendered log viewport");
        var resumeStartPercent = CurrentVerticalScrollPercent(app);
        var resumeStartWindow = FirstVisibleLogWindow(app)
            ?? throw new InvalidOperationException("Frozen log viewport did not expose a visible structured record before resume");
        var resumeAssertion = new Dictionary<string, object?>
        {
            ["id"] = "downward-wheel-resumes-rendered-log-tail",
            ["outcome"] = "not-run",
            ["expected"] = new { vertical_scroll_percent = 100.0, appended_records_rendered = true },
            ["actual"] = new
            {
                vertical_percent_before = resumeStartPercent,
                visible_window_before = resumeStartWindow,
                appended_records_rendered = false,
            },
        };
        assertions.Add(resumeAssertion);
        var resumedPercent = resumeStartPercent;
        VisibleLogWindow? resumedWindow = null;
        var appendedRecordsRendered = false;
        SendLogsViewportWheel(app, -120, "resume log follow", deadline);
        try
        {
            WaitLogsCase(deadline, "log follow resume", () =>
            {
                resumedPercent = CurrentVerticalScrollPercent(app);
                resumedWindow = FirstVisibleLogWindow(app);
                var currentRoot = AutomationElement.FromHandle(app.Window);
                appendedRecordsRendered = appendedEntries.All(entry =>
                    FindUniqueAutomationId(currentRoot, LogPresentationState.DetailsAutomationId(entry)) is not null);
                resumeAssertion["actual"] = new
                {
                    vertical_percent_before = resumeStartPercent,
                    vertical_percent_after = resumedPercent,
                    visible_window_before = resumeStartWindow,
                    visible_window_after = resumedWindow,
                    at_exact_bottom = resumedPercent == 100.0,
                    appended_records_rendered = appendedRecordsRendered,
                };
                return resumedPercent == 100.0 && appendedRecordsRendered;
            }, "Controlled log entries were not rendered after returning to the bottom");
            resumeAssertion["outcome"] = "passed";
        }
        catch
        {
            resumeAssertion["outcome"] = "failed";
            throw;
        }
        foreach (var (entry, index) in appendedEntries.Select((entry, index) => (entry, index)))
            AssertLogsCase(assertions, $"resumed-tail-shows-controlled-append-{index + 1}", entry.Raw,
                FindLogDetailsRawText(app, entry));
        AddScreenshot(app, "logs-freeze-resume", "resumed", deadline, screenshots, screenshotDirectory);
    }

    private static void RunLogsClearCase(
        OwnedLogsApp app, NativeDiagnostics diagnostics, string beforeMarker, string fixturePath,
        LogsCaseDeadline deadline, List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        var before = ReadFixtureEntry(diagnostics, beforeMarker);
        WaitForRenderedDetails(app, before, deadline);
        AssertLogsCase(assertions, "fixture-producer-paused-before-clear", 1, CountFixtureRecords(fixturePath));
        InvokeClear(app);
        WaitLogsCase(deadline, "Clear boundary", () =>
        {
            var current = AutomationElement.FromHandle(app.Window);
            return FindUniqueAutomationId(current, LogPresentationState.DetailsAutomationId(before)) is null;
        }, "Clear left the pre-Clear rendered record visible");
        AssertLogsCase(assertions, "clear-hides-pre-clear-record", null,
            FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), LogPresentationState.DetailsAutomationId(before))?.Current.AutomationId);

        var afterMarker = beforeMarker.Replace("-before-clear", "-after-clear", StringComparison.Ordinal);
        AppendLogsFixtureRecord(fixturePath, "logs-clear", afterMarker);
        var after = ReadFixtureEntry(diagnostics, afterMarker);
        var rendered = WaitForRenderedDetails(app, after, deadline);
        AssertLogsCase(assertions, "clear-shows-first-post-clear-record", LogPresentationState.DetailsAutomationId(after), rendered.Current.AutomationId);
        AssertFixtureRetainsRecords(assertions, fixturePath, before.Raw, after.Raw);
        AddScreenshot(app, "logs-clear", "post-append", deadline, screenshots, screenshotDirectory);
    }

    private static void RunLogsReopenClearBoundaryCase(
        ref OwnedLogsApp? app, string appPath, string fixturePath, NativeDiagnostics diagnostics,
        string beforeMarker, LogsCaseDeadline deadline, List<OwnedLogsApp> launches,
        List<object> assertions, List<string> screenshots, string screenshotDirectory)
    {
        var initialApp = app ?? throw new InvalidOperationException("Reopen boundary case has no initial app process");
        var before = ReadFixtureEntry(diagnostics, beforeMarker);
        AssertLogsCase(assertions, "fixture-producer-paused-at-reopen-clear", 1, CountFixtureRecords(fixturePath));
        WaitForRenderedDetails(initialApp, before, deadline);
        InvokeClear(initialApp);
        WaitLogsCase(deadline, "Clear boundary before reopen", () =>
            FindUniqueAutomationId(AutomationElement.FromHandle(initialApp.Window), LogPresentationState.DetailsAutomationId(before)) is null,
            "Clear left the pre-Clear record visible before frontend reopen");

        var afterMarker = beforeMarker.Replace("-before-clear", "-after-clear", StringComparison.Ordinal);
        AppendLogsFixtureRecord(fixturePath, "logs-reopen-clear-boundary", afterMarker);
        var after = ReadFixtureEntry(diagnostics, afterMarker);
        WaitForRenderedDetails(initialApp, after, deadline);
        var previousPid = initialApp.Process.Id;
        var previousIdentity = initialApp.StartTicks.ToString(CultureInfo.InvariantCulture);
        CloseOwnedLogsApp(initialApp, deadline);
        initialApp.Dispose();
        app = null;

        var reopened = StartOwnedLogsApp(appPath, fixturePath, deadline, launches);
        app = reopened;
        WaitForLogsViewport(reopened, deadline);
        var reopenedRoot = AutomationElement.FromHandle(reopened.Window);
        var reopenedBefore = FindUniqueAutomationId(reopenedRoot, LogPresentationState.DetailsAutomationId(before));
        var reopenedAfter = WaitForRenderedDetails(reopened, after, deadline);
        var reopenedIdentity = reopened.StartTicks.ToString(CultureInfo.InvariantCulture);
        AssertLogsCase(assertions, "reopen-starts-new-process", true,
            reopened.Process.Id != previousPid && reopenedIdentity != previousIdentity);
        AssertLogsCase(assertions, "pre-clear-record-stays-hidden-after-reopen", null, reopenedBefore?.Current.AutomationId);
        AssertLogsCase(assertions, "post-clear-record-survives-reopen", LogPresentationState.DetailsAutomationId(after), reopenedAfter.Current.AutomationId);
        AssertFixtureRetainsRecords(assertions, fixturePath, before.Raw, after.Raw);
        AddScreenshot(reopened, "logs-reopen-clear-boundary", "reopened", deadline, screenshots, screenshotDirectory);
    }

    private static void InvokeClear(OwnedLogsApp app)
    {
        RequireForeground(app.Window, "invoking rendered log Clear");
        var clear = FindRequiredAutomationId(AutomationElement.FromHandle(app.Window), "logs.clear");
        if (!clear.TryGetCurrentPattern(InvokePattern.Pattern, out var pattern))
            throw new InvalidOperationException("Rendered Clear control did not expose InvokePattern");
        ((InvokePattern)pattern).Invoke();
    }

    private static void SendLogsViewportWheel(
        OwnedLogsApp app, int wheelDelta, string operation, LogsCaseDeadline deadline)
    {
        if (wheelDelta is not (120 or -120))
            throw new ArgumentOutOfRangeException(nameof(wheelDelta), "Log wheel input must be one native wheel notch");
        deadline.Check(operation);
        var viewport = RequireLogsViewport(app);
        var bounds = PhysicalBounds(viewport, "Backend logs");
        var point = new NativePoint
        {
            X = checked((int)Math.Round(bounds.Left + bounds.Width / 2, MidpointRounding.AwayFromZero)),
            Y = checked((int)Math.Round(bounds.Top + bounds.Height / 2, MidpointRounding.AwayFromZero)),
        };
        Marshal.SetLastPInvokeError(0);
        if (!SetCursorPos(point.X, point.Y))
            throw new System.ComponentModel.Win32Exception(
                Marshal.GetLastPInvokeError(), $"Could not move the pointer over the log viewport to {operation}");
        Marshal.SetLastPInvokeError(0);
        var cursorRead = GetCursorPos(out var cursor);
        var cursorError = Marshal.GetLastPInvokeError();
        if (!cursorRead || cursor.X != point.X || cursor.Y != point.Y)
            throw new InvalidOperationException(
                $"Pointer did not reach the log viewport for {operation}: expected=({point.X},{point.Y}) " +
                $"actual=({cursor.X},{cursor.Y}) lastError={cursorError}");
        if (GetForegroundWindow() != app.Window)
            throw new InvalidOperationException($"Owned app lost foreground before {operation}");
        if (cursor.X < bounds.Left || cursor.X >= bounds.Right || cursor.Y < bounds.Top || cursor.Y >= bounds.Bottom)
            throw new InvalidOperationException($"Pointer was outside the rendered log viewport for {operation}");
        var windowAtPointer = GetAncestor(WindowFromPoint(cursor), GaRoot);
        if (windowAtPointer != app.Window)
            throw new InvalidOperationException(
                $"Pointer over the log viewport resolved to window 0x{windowAtPointer.ToInt64():X}, " +
                $"not owned app window 0x{app.Window.ToInt64():X}, for {operation}");
        GetWindowThreadProcessId(app.Window, out var ownerPid);
        if (ownerPid != (uint)app.Process.Id)
            throw new InvalidOperationException($"Log wheel input target changed ownership before {operation}");

        var input = CreateMouseInput(MouseEventWheel, unchecked((uint)(short)wheelDelta));
        Marshal.SetLastPInvokeError(0);
        var inserted = SendInput(1, new[] { input }, Marshal.SizeOf<NativeInput>());
        var lastError = Marshal.GetLastPInvokeError();
        if (inserted != 1)
            throw new InvalidOperationException(
                $"SendInput inserted {inserted} of 1 log-wheel event for {operation}; lastError={lastError}");
    }

    private static void AssertFixtureRetainsRecords(
        List<object> assertions, string fixturePath, string beforeRaw, string afterRaw)
    {
        var bytes = File.ReadAllBytes(fixturePath);
        var text = Encoding.UTF8.GetString(bytes);
        AssertLogsCase(assertions, "clear-retains-pre-clear-and-appended-records", true,
            text.Contains(beforeRaw, StringComparison.Ordinal) && text.Contains(afterRaw, StringComparison.Ordinal));
    }

    private static AutomationElement WaitForRenderedDetails(
        OwnedLogsApp app, NativeDiagnostics.Entry entry, LogsCaseDeadline deadline)
    {
        var automationId = LogPresentationState.DetailsAutomationId(entry);
        AutomationElement? found = null;
        var requestedScrollIntoView = false;
        WaitLogsCase(deadline, "rendered log record", () =>
        {
            var viewport = RequireLogsViewport(app);
            var bounds = PhysicalBounds(viewport, "Backend logs");
            found = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), automationId);
            if (found is null) return false;
            var current = found.Current;
            if (!current.IsOffscreen &&
                HasUsableBounds(System.Windows.Rect.Intersect(current.BoundingRectangle, bounds))) return true;
            if (!requestedScrollIntoView)
            {
                if (!found.TryGetCurrentPattern(ScrollItemPattern.Pattern, out var scrollItem))
                    throw new InvalidOperationException(
                        $"Rendered log Details control does not expose ScrollItemPattern while outside the viewport: {automationId}");
                ((ScrollItemPattern)scrollItem).ScrollIntoView();
                requestedScrollIntoView = true;
            }
            return false;
        }, $"Rendered log Details control was not visible for fixture record {entry.Message}");
        return found ?? throw new InvalidOperationException($"Rendered log Details control disappeared for fixture record {entry.Message}");
    }

    private static Dictionary<string, object?> ReadLogsControlFailureMetadata(
        OwnedLogsApp app, string fixturePath, string? marker)
    {
        var metadata = new Dictionary<string, object?>
        {
            ["pid"] = app.Result.GetValueOrDefault("pid"),
            ["process_identity"] = app.Result.GetValueOrDefault("identity"),
            ["window_handle"] = $"0x{app.Window.ToInt64():X}",
            ["window_is_valid"] = IsWindow(app.Window),
        };
        GetWindowThreadProcessId(app.Window, out var ownerPid);
        metadata["window_owner_pid"] = ownerPid;
        metadata["owned_pid"] = app.Process.Id;
        metadata["window_owned_by_process"] = ownerPid == (uint)app.Process.Id;
        if (!IsWindow(app.Window)) return metadata;

        var root = AutomationElement.FromHandle(app.Window);
        var viewport = FindUniqueAutomationId(root, "Backend logs");
        metadata["viewport_found"] = viewport is not null;
        if (viewport is null)
        {
            metadata["viewport"] = null;
            return metadata;
        }

        var viewportCurrent = viewport.Current;
        metadata["viewport"] = new
        {
            automation_id = viewportCurrent.AutomationId,
            name = viewportCurrent.Name,
            control_type = viewportCurrent.ControlType.ProgrammaticName,
            is_offscreen = viewportCurrent.IsOffscreen,
            bounds = RectJson(viewportCurrent.BoundingRectangle),
        };
        if (viewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var scrollPattern))
        {
            var scroll = ((ScrollPattern)scrollPattern).Current;
            metadata["scroll"] = new
            {
                vertical_percent = scroll.VerticalScrollPercent,
                vertical_view_size = scroll.VerticalViewSize,
                horizontally_scrollable = scroll.HorizontallyScrollable,
                vertically_scrollable = scroll.VerticallyScrollable,
            };
        }

        if (!string.IsNullOrWhiteSpace(marker))
        {
            string? expectedAutomationId = null;
            try
            {
                var entry = ReadFixtureEntry(CreateFixtureDiagnostics(fixturePath), marker);
                expectedAutomationId = LogPresentationState.DetailsAutomationId(entry);
                var target = FindUniqueAutomationId(root, expectedAutomationId);
                metadata["expected_details_automation_id"] = expectedAutomationId;
                metadata["expected_details_found"] = target is not null;
                if (target is not null)
                {
                    var current = target.Current;
                    metadata["expected_details"] = new
                    {
                        automation_id = current.AutomationId,
                        accessible_name = current.Name,
                        control_type = current.ControlType.ProgrammaticName,
                        is_offscreen = current.IsOffscreen,
                        bounds = RectJson(current.BoundingRectangle),
                        intersects_viewport = HasUsableBounds(System.Windows.Rect.Intersect(
                            current.BoundingRectangle, viewportCurrent.BoundingRectangle)),
                        has_scroll_item_pattern = target.TryGetCurrentPattern(
                            ScrollItemPattern.Pattern, out _),
                    };
                }
            }
            catch (Exception error)
            {
                metadata["expected_details_metadata_error"] = error.ToString();
            }

            try
            {
                metadata["viewport_descendants"] = ReadLogsViewportDescendantMetadata(
                    viewport, marker, expectedAutomationId);
            }
            catch (Exception error)
            {
                metadata["viewport_descendant_metadata_error"] = error.ToString();
            }
        }
        return metadata;
    }

    private static Dictionary<string, object?> ReadLogsViewportDescendantMetadata(
        AutomationElement viewport, string marker, string? expectedAutomationId)
    {
        const int maximumElements = 192;
        const int maximumSamples = 80;
        var countsByType = new Dictionary<string, int>(StringComparer.Ordinal);
        var samples = new List<object>();
        var markerMatches = new List<string>();
        var expectedIdObserved = false;
        var visited = 0;

        foreach (var element in Walk(viewport).Take(maximumElements + 1))
        {
            visited++;
            if (visited > maximumElements) break;

            try
            {
                var current = element.Current;
                var type = current.ControlType.ProgrammaticName;
                countsByType[type] = countsByType.GetValueOrDefault(type) + 1;
                var automationId = current.AutomationId ?? string.Empty;
                if (automationId == expectedAutomationId) expectedIdObserved = true;

                if (current.ControlType == ControlType.Text)
                {
                    var text = current.Name ?? string.Empty;
                    if (element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern))
                        text = ((TextPattern)pattern).DocumentRange.GetText(4096);
                    var containsMarker = text.Contains(marker, StringComparison.Ordinal);
                    if (containsMarker && !string.IsNullOrWhiteSpace(automationId))
                        markerMatches.Add(automationId);
                    if (samples.Count < maximumSamples &&
                        (containsMarker || !string.IsNullOrWhiteSpace(automationId)))
                    {
                        samples.Add(new
                        {
                            automation_id = automationId,
                            control_type = type,
                            is_offscreen = current.IsOffscreen,
                            bounds = RectJson(current.BoundingRectangle),
                            text_length = text.Length,
                            contains_fixture_marker = containsMarker,
                        });
                    }
                }
                else if (samples.Count < maximumSamples &&
                    (!string.IsNullOrWhiteSpace(automationId) || current.ControlType == ControlType.Button))
                {
                    samples.Add(new
                    {
                        automation_id = automationId,
                        control_type = type,
                        accessible_name = current.Name,
                        is_offscreen = current.IsOffscreen,
                        bounds = RectJson(current.BoundingRectangle),
                    });
                }
            }
            catch (Exception error)
            {
                if (samples.Count < maximumSamples)
                    samples.Add(new { metadata_error = error.ToString() });
            }
        }

        return new Dictionary<string, object?>
        {
            ["sample_limit"] = maximumElements,
            ["visited_count_including_overflow_probe"] = visited,
            ["truncated"] = visited > maximumElements,
            ["counts_by_control_type"] = countsByType,
            ["expected_id_observed"] = expectedIdObserved,
            ["fixture_marker_text_found"] = markerMatches.Count > 0,
            ["fixture_marker_text_automation_ids"] = markerMatches,
            ["controls"] = samples,
        };
    }

    private static AutomationElement RequireLogsViewport(OwnedLogsApp app) =>
        FindRequiredAutomationId(AutomationElement.FromHandle(app.Window), "Backend logs");

    private static VisibleLogWindow? FirstVisibleLogWindow(OwnedLogsApp app)
    {
        var viewport = RequireLogsViewport(app);
        var bounds = PhysicalBounds(viewport, "Backend logs");
        var visible = Walk(viewport)
            .Select(element => (Element: element, Current: element.Current))
            .Where(item => item.Current.AutomationId.StartsWith("Log details ", StringComparison.Ordinal) &&
                !item.Current.IsOffscreen && HasUsableBounds(System.Windows.Rect.Intersect(item.Current.BoundingRectangle, bounds)))
            .Select(item => (item.Current.AutomationId, Top: item.Current.BoundingRectangle.Top))
            .OrderBy(item => item.Top)
            .ToArray();
        if (visible.Length == 0) return null;
        return new VisibleLogWindow(string.Join("\n", visible.Select(item => item.AutomationId)),
            Math.Round(visible[0].Top, 1));
    }

    private static string FindLogDetailsRawText(OwnedLogsApp app, NativeDiagnostics.Entry entry)
    {
        var automationId = LogPresentationState.DetailsAutomationId(entry);
        var details = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), automationId)
            ?? throw new InvalidOperationException($"Rendered log Details control disappeared before raw text check: {automationId}");
        return Walk(details)
            .Where(element => element.Current.ControlType == ControlType.Text)
            .Select(element => element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern)
                ? ((TextPattern)pattern).DocumentRange.GetText(-1).TrimEnd('\r', '\n') : element.Current.Name)
            .FirstOrDefault(value => value == entry.Raw)
            ?? string.Empty;
    }

    private static int CountFixtureRecords(string fixturePath) =>
        File.ReadLines(fixturePath).Count(line => !string.IsNullOrWhiteSpace(line));

    private static double CurrentVerticalScrollPercent(OwnedLogsApp app)
    {
        var viewport = RequireLogsViewport(app);
        if (!viewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var pattern)) return double.NaN;
        return ((ScrollPattern)pattern).Current.VerticalScrollPercent;
    }

    private static AutomationElement? FindUniqueAutomationId(AutomationElement root, string automationId)
    {
        var matches = root.FindAll(TreeScope.Descendants,
            new PropertyCondition(AutomationElement.AutomationIdProperty, automationId));
        if (matches.Count > 1)
            throw new InvalidOperationException($"Multiple native UI elements matched stable automation identifier: {automationId}");
        return matches.Count == 0 ? null : matches[0];
    }

    private static AutomationElement FindRequiredAutomationId(AutomationElement root, string automationId)
    {
        var element = FindUniqueAutomationId(root, automationId)
            ?? throw new InvalidOperationException($"Native UI element was not found by stable automation identifier: {automationId}");
        if (!element.Current.IsEnabled) throw new InvalidOperationException($"Native UI element is disabled: {automationId}");
        return element;
    }

    private static void AssertLogsCase(List<object> assertions, string id, object? expected, object? actual)
    {
        var passed = Equals(expected, actual);
        assertions.Add(new { id, outcome = passed ? "passed" : "failed", expected, actual });
        if (!passed)
            throw new InvalidOperationException(
                $"Windows logs case assertion {id} failed: expected={SerializeNativeCaseJson(expected)} actual={SerializeNativeCaseJson(actual)}");
    }

    private static void AddScreenshot(
        OwnedLogsApp app, string caseName, string stage, LogsCaseDeadline deadline,
        List<string> screenshots, string screenshotDirectory)
    {
        deadline.Check("screenshot capture");
        Directory.CreateDirectory(screenshotDirectory);
        var path = Path.Combine(screenshotDirectory, $"{caseName}-{stage}.png");
        Capture(app.Window, app.Process.Id, path);
        screenshots.Add(path);
    }

    private static void WaitLogsCase(LogsCaseDeadline deadline, string stage, Func<bool> condition, string message)
    {
        while (true)
        {
            deadline.Check(stage);
            try
            {
                if (condition()) return;
            }
            catch (ElementNotAvailableException) { }
            var remaining = deadline.RemainingSeconds;
            if (remaining <= 0) throw new TimeoutException(message);
            Thread.Sleep(Math.Max(1, Math.Min(50, (int)Math.Ceiling(remaining * 1000))));
        }
    }

    private static void DelayLogsCase(LogsCaseDeadline deadline, TimeSpan duration, string stage)
    {
        var until = Stopwatch.GetTimestamp() + (long)(duration.TotalSeconds * Stopwatch.Frequency);
        while (Stopwatch.GetTimestamp() < until)
        {
            deadline.Check(stage);
            var remainingTicks = until - Stopwatch.GetTimestamp();
            var milliseconds = (int)Math.Ceiling(remainingTicks * 1000.0 / Stopwatch.Frequency);
            Thread.Sleep(Math.Max(1, Math.Min(50, milliseconds)));
        }
        deadline.Check(stage);
    }

    private static void CloseOwnedLogsApp(OwnedLogsApp app, LogsCaseDeadline deadline)
    {
        if (app.IsDisposed) return;
        if (app.Process.HasExited)
        {
            TryRecordLogsAppExit(app);
            ClearNativeUiChildMarker(app);
            return;
        }
        if (!app.HasIdentity)
        {
            // Process.Start returned this exact handle. With no verified start identity,
            // avoid window/UI actions and terminate only that owned process object.
            TerminateOwnedLogsAppProcess(app, deadline, "unidentified owned app termination");
            return;
        }
        VerifyOwnedLogsApp(app);
        var closeRequested = false;
        if (app.Window != IntPtr.Zero)
        {
            GetWindowThreadProcessId(app.Window, out var ownerPid);
            if (ownerPid != app.Process.Id)
                throw new InvalidOperationException("Refusing to close a Windows app window without the owned process identity");
            closeRequested = app.Process.CloseMainWindow();
            if (!closeRequested && !app.Process.HasExited)
                closeRequested = PostMessage(app.Window, WmClose, IntPtr.Zero, IntPtr.Zero);
        }

        try
        {
            if (!closeRequested && !app.Process.HasExited)
            {
                VerifyOwnedLogsApp(app);
                app.Process.Kill(entireProcessTree: true);
            }
            WaitLogsCase(deadline, "owned app close", () => app.Process.HasExited,
                $"Owned Windows logs-case app process {app.Process.Id} did not exit after close");
        }
        catch (TimeoutException)
        {
            VerifyOwnedLogsApp(app);
            app.Process.Kill(entireProcessTree: true);
            WaitLogsCase(deadline, "owned app termination", () => app.Process.HasExited,
                $"Owned Windows logs-case app process {app.Process.Id} did not exit after termination");
        }
        TryRecordLogsAppExit(app);
        ClearNativeUiChildMarker(app);
    }

    private static void TerminateOwnedLogsAppProcess(OwnedLogsApp app, LogsCaseDeadline deadline, string stage)
    {
        if (app.IsDisposed) return;
        if (app.Process.HasExited)
        {
            TryRecordLogsAppExit(app);
            return;
        }

        // This Process object came directly from Process.Start for this case. Kill only
        // that process handle; never use an unverified window or process-tree action here.
        try { app.Process.Kill(entireProcessTree: false); }
        catch (InvalidOperationException) when (app.Process.HasExited) { }
        var pid = app.Result.TryGetValue("pid", out var value) ? value : "unknown";
        WaitLogsCase(deadline, stage, () => app.Process.HasExited,
            $"Owned Windows logs-case app process {pid} did not exit after termination");
        TryRecordLogsAppExit(app);
    }

    private static void VerifyOwnedLogsApp(OwnedLogsApp app)
    {
        if (!app.HasIdentity)
            throw new InvalidOperationException("Windows logs-case process identity was not captured; refusing UI actions");
        if (app.Process.HasExited) return;
        app.Process.Refresh();
        var currentPath = app.Process.MainModule?.FileName;
        var currentTicks = app.Process.StartTime.ToUniversalTime().Ticks;
        if (!string.Equals(currentPath, app.Executable, StringComparison.OrdinalIgnoreCase) || currentTicks != app.StartTicks)
            throw new InvalidOperationException("Windows logs-case process identity changed; refusing to close it");
    }

    private static void TryRecordLogsAppExit(OwnedLogsApp app)
    {
        try
        {
            app.Process.Refresh();
            app.Result["alive_after_case"] = !app.Process.HasExited;
            if (app.Process.HasExited) app.Result["exit_code"] = app.Process.ExitCode;
        }
        catch (Exception error)
        {
            app.Result["process_liveness_error"] = error.ToString();
        }
    }

    private static void WriteLogsCaseResult(string resultPath, Dictionary<string, object?> result)
    {
        var json = SerializeNativeCaseJson(result, indented: true) + "\n";
        File.WriteAllText(resultPath, json, new UTF8Encoding(false));
    }
}
