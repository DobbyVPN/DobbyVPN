using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Linq;
using System.Globalization;
using System.Runtime.InteropServices;
using System.Text;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using System.Threading;
using System.Windows.Automation;
using Forms = System.Windows.Forms;

// Runs only in the interactive test user's session, outside the shipped app.
internal static class Program
{
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool GetWindowRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool SetWindowPos(IntPtr window, IntPtr insertAfter, int x, int y, int width, int height, uint flags);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);
    [DllImport("user32.dll")] private static extern bool ShowWindowAsync(IntPtr window, int command);
    [DllImport("user32.dll")] private static extern IntPtr GetWindow(IntPtr window, uint command);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetClientRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool ClientToScreen(IntPtr window, ref NativePoint point);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetClassNameW")]
    private static extern int GetClassName(IntPtr window, StringBuilder className, int maxCount);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetWindowTextLengthW")]
    private static extern int GetWindowTextLength(IntPtr window);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetWindowTextW")]
    private static extern int GetWindowText(IntPtr window, StringBuilder text, int maxCount);
    private delegate bool EnumThreadWindowsCallback(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern bool EnumThreadWindows(uint threadId, EnumThreadWindowsCallback callback, IntPtr parameter);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] private static extern bool SetProcessDpiAwarenessContext(IntPtr context);
    [StructLayout(LayoutKind.Sequential)] private struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] private struct NativePoint { public int X, Y; }

    private static void TracePhase(string name)
    {
        Console.Error.WriteLine($"native-ui-phase={name}");
        Console.Error.Flush();
    }

    private static T ReadUiaProbeProperty<T>(string property, Func<T> read)
    {
        TracePhase($"configure-tree-uia-target-property-start property={property}");
        try
        {
            var value = read();
            TracePhase(
                $"configure-tree-uia-target-property-complete property={property} " +
                $"value={JsonSerializer.Serialize(value)}");
            return value;
        }
        catch (Exception error)
        {
            TracePhase(
                $"configure-tree-uia-target-property-failed property={property} " +
                $"exception_type={error.GetType().FullName} message={JsonSerializer.Serialize(error.Message)}");
            throw;
        }
    }

    private static AutomationElement? GetUiaProbeFirstChild(
        AutomationElement parent,
        string parentPath,
        string childPath)
    {
        TracePhase($"configure-tree-uia-path-edge-start parent={parentPath} child={childPath}");
        try
        {
            var child = TreeWalker.ControlViewWalker.GetFirstChild(parent);
            TracePhase(
                $"configure-tree-uia-path-edge-complete child={childPath} " +
                $"has_child={child is not null}");
            return child;
        }
        catch (Exception error)
        {
            TracePhase(
                $"configure-tree-uia-path-edge-failed child={childPath} " +
                $"exception_type={error.GetType().FullName} message={JsonSerializer.Serialize(error.Message)}");
            throw;
        }
    }

    private static int ReportUiaProbeMissingPath(
        Process process,
        string identity,
        IntPtr window,
        string missingPath)
    {
        Console.WriteLine(JsonSerializer.Serialize(new
        {
            ready = true,
            pid = process.Id,
            identity,
            windowHandle = $"0x{window.ToInt64():X}",
            pathResolved = false,
            missingPath,
            expansionAttempted = false,
        }));
        return 0;
    }

    private static string UtcTimestamp() =>
        DateTimeOffset.UtcNow.ToString("O", CultureInfo.InvariantCulture);

    private static string ElapsedMilliseconds(long started) =>
        Stopwatch.GetElapsedTime(started).TotalMilliseconds.ToString("F3", CultureInfo.InvariantCulture);

    private static string NormalizeLineEndings(string value) =>
        value.Replace("\r\n", "\n").Replace('\r', '\n');

    private static string DescribeWindow(IntPtr window)
    {
        GetWindowThreadProcessId(window, out var pid);
        var hasBounds = GetWindowRect(window, out var bounds);
        var classBuffer = new StringBuilder(256);
        var classLength = GetClassName(window, classBuffer, classBuffer.Capacity);
        var textBuffer = new StringBuilder(Math.Max(1, GetWindowTextLength(window) + 1));
        var textLength = GetWindowText(window, textBuffer, textBuffer.Capacity);
        var boundsText = hasBounds
            ? $"{bounds.Left},{bounds.Top} {bounds.Right - bounds.Left}x{bounds.Bottom - bounds.Top}"
            : "unavailable";
        var classText = classLength > 0 ? classBuffer.ToString() : "unavailable";
        var windowText = textLength > 0 ? textBuffer.ToString() : "unavailable";
        return $"HWND=0x{window.ToInt64():X} PID={pid} bounds=[{boundsText}] class=\"{classText}\" text=\"{windowText}\"";
    }

    private static string[] DescribeProcessWindows(Process process)
    {
        return EnumerateProcessWindows(process).Select(window =>
            DescribeWindow(window) +
            $" visible={IsWindowVisible(window)} minimized={IsIconic(window)}").ToArray();
    }

    private static IntPtr[] EnumerateProcessWindows(Process process)
    {
        var windows = new List<IntPtr>();
        EnumThreadWindowsCallback callback = (window, _) =>
        {
            GetWindowThreadProcessId(window, out var ownerPid);
            if (ownerPid == process.Id) windows.Add(window);
            return true;
        };
        try
        {
            foreach (ProcessThread thread in process.Threads)
                EnumThreadWindows(unchecked((uint)thread.Id), callback, IntPtr.Zero);
        }
        catch (InvalidOperationException)
        {
            // The caller will retry on its next bounded poll if the thread
            // list changes while the window is being created.
        }
        GC.KeepAlive(callback);
        return windows.ToArray();
    }

    private static string DescribeElement(AutomationElement element)
    {
        var current = element.Current;
        var invoke = element.TryGetCurrentPattern(InvokePattern.Pattern, out _);
        var selection = element.TryGetCurrentPattern(SelectionItemPattern.Pattern, out _);
        var value = element.TryGetCurrentPattern(ValuePattern.Pattern, out _);
        return $"type={current.ControlType.ProgrammaticName} id=\"{current.AutomationId}\" " +
               $"name=\"{current.Name}\" patterns[invoke={invoke},selectionItem={selection},value={value}]";
    }

    private static AutomationElement? ByAutomationId(AutomationElement root, string id, Action<string>? trace = null)
    {
        return Walk(root, trace).FirstOrDefault(element =>
        {
            var current = element.Current;
            return current.IsControlElement && current.AutomationId == id;
        });
    }

    private static void WaitFor(Func<bool> condition, string message, double seconds = 15)
    {
        var limit = Stopwatch.StartNew();
        do
        {
            try { if (condition()) return; }
            catch (ElementNotAvailableException) { }
            Thread.Sleep(50);
        } while (limit.Elapsed.TotalSeconds < seconds);
        throw new TimeoutException(message);
    }

    private static void VerifyNarrowWindow(AutomationElement root, IntPtr window, int processId, string sourcePath)
    {
        var fixtureDirectory = Path.GetDirectoryName(Path.GetFullPath(sourcePath))
            ?? throw new InvalidOperationException("Subscription fixture source path has no directory");
        var marker = Path.Combine(fixtureDirectory, ".windows-narrow-window-rendered-passed");
        if (File.Exists(marker)) return;

        var originalBounds = new Rect();
        if (!GetWindowRect(window, out originalBounds))
            throw new InvalidOperationException("Could not capture the original window bounds for narrow-window test");
        var windowElement = AutomationElement.FromHandle(window);
        if (!windowElement.TryGetCurrentPattern(WindowPattern.Pattern, out var windowPattern) ||
            !((WindowPattern)windowPattern).Current.CanMaximize)
            throw new InvalidOperationException("Native window does not expose a maximizable WindowPattern");

        var screenshot = Path.Combine(Path.GetTempPath(), "dobby-narrow-window-" + Guid.NewGuid().ToString("N") + ".png");
        Exception? operationFailure = null;
        try
        {
            WaitFor(() =>
            {
                var first = ByAutomationId(root, "Profile 1 action");
                var second = ByAutomationId(root, "Profile 2 action");
                return first is not null && second is not null && !first.Current.IsOffscreen && !second.Current.IsOffscreen;
            }, "Two profile actions did not render before the narrow-window check");

            var display = Forms.Screen.FromHandle(window).WorkingArea;
            if (display.Width < 680 || display.Height < 680)
                throw new InvalidOperationException($"Display is too small for the narrow-window interaction: {display.Width}x{display.Height}");
            if (!SetWindowPos(window, IntPtr.Zero, display.Left + 20, display.Top + 20, 640, 640, 0x0004 | 0x0010))
                throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "Could not resize the native window to the narrow test size");
            WaitFor(() => GetWindowRect(window, out var bounds) && bounds.Right - bounds.Left <= 660 && bounds.Bottom - bounds.Top <= 660,
                "Native window did not render at the narrow test size");

            foreach (var id in new[] { "Connection configuration", "VPN connection action", "Profile 1 action", "Profile 2 action", "Backend logs" })
            {
                var element = ByAutomationId(root, id);
                if (element is null || element.Current.IsOffscreen)
                    throw new InvalidOperationException($"Native control was not rendered in the narrow window: {id}");
            }
            if (!SetForegroundWindow(window)) throw new InvalidOperationException("Could not activate the native window for narrow capture");
            Capture(window, processId, screenshot);
        }
        catch (Exception error) { operationFailure = error; throw; }
        finally
        {
            var cleanupFailures = new List<Exception>();
            try
            {
                if (!SetWindowPos(window, IntPtr.Zero, originalBounds.Left, originalBounds.Top,
                        originalBounds.Right - originalBounds.Left, originalBounds.Bottom - originalBounds.Top, 0x0004 | 0x0010))
                    throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "Could not restore native window bounds after narrow-window test");
            }
            catch (Exception error) { cleanupFailures.Add(error); }
            try { if (File.Exists(screenshot)) File.Delete(screenshot); }
            catch (Exception error) { cleanupFailures.Add(error); }
            if (cleanupFailures.Count > 0)
            {
                if (operationFailure is not null) cleanupFailures.Insert(0, operationFailure);
                throw new AggregateException("Narrow-window interaction or cleanup failed", cleanupFailures);
            }
        }
        File.WriteAllText(marker, "rendered narrow-window two-profile interaction passed\n", Encoding.ASCII);
        Console.Error.WriteLine("Windows narrow-window rendered two-profile interaction passed");
        Console.Error.Flush();
    }

    private static Rectangle ClientScreenBounds(IntPtr window, Rectangle target)
    {
        if (!GetClientRect(window, out var client) || client.Right <= client.Left || client.Bottom <= client.Top)
            throw new InvalidOperationException("UI client bounds unavailable");
        var topLeft = new NativePoint { X = client.Left, Y = client.Top };
        var bottomRight = new NativePoint { X = client.Right, Y = client.Bottom };
        if (!ClientToScreen(window, ref topLeft) || !ClientToScreen(window, ref bottomRight))
            throw new InvalidOperationException("UI client screen coordinates unavailable");
        var bounds = Rectangle.FromLTRB(topLeft.X, topLeft.Y, bottomRight.X, bottomRight.Y);
        if (bounds.Width <= 0 || bounds.Height <= 0 || !target.Contains(bounds))
            throw new InvalidOperationException("UI client bounds fall outside the native window");
        return bounds;
    }

    private static void PrepareCaptureCursor(IntPtr window, Rectangle target, Rectangle client, Rectangle display)
    {
        if (!SetForegroundWindow(window))
            throw new InvalidOperationException("Could not activate UI before screenshot");
        // CTRL dismisses keyboard-focus tooltips without changing page or input.
        Forms.SendKeys.SendWait("^");
        var corners = new[]
        {
            new Point(display.Left, display.Top),
            new Point(display.Right - 1, display.Top),
            new Point(display.Left, display.Bottom - 1),
            new Point(display.Right - 1, display.Bottom - 1),
        };
        // A full-display window has no outside corner; use the client margin then.
        var destination = new Point(client.Right - 2, client.Bottom - 2);
        foreach (var corner in corners)
        {
            if (target.Contains(corner)) continue;
            destination = corner;
            break;
        }
        if (!SetCursorPos(destination.X, destination.Y))
            throw new InvalidOperationException("Could not move pointer before screenshot");
        Thread.Sleep(200);
    }

    private static bool HasNonuniformClientPixels(
        Bitmap bitmap, Rectangle clientInBitmap, Stopwatch renderWait)
    {
        var row = new byte[clientInBitmap.Width * 4];
        var data = bitmap.LockBits(
            clientInBitmap, ImageLockMode.ReadOnly, PixelFormat.Format32bppArgb
        );
        try
        {
            Marshal.Copy(data.Scan0, row, 0, row.Length);
            var firstBlue = row[0];
            var firstGreen = row[1];
            var firstRed = row[2];
            for (var y = 0; y < clientInBitmap.Height; y++)
            {
                if (renderWait.Elapsed.TotalSeconds >= 5) return false;
                Marshal.Copy(IntPtr.Add(data.Scan0, y * data.Stride), row, 0, row.Length);
                for (var offset = 0; offset < row.Length; offset += 4)
                {
                    if (row[offset] != firstBlue || row[offset + 1] != firstGreen ||
                        row[offset + 2] != firstRed)
                        return true;
                }
            }
            return false;
        }
        finally
        {
            bitmap.UnlockBits(data);
        }
    }

    [STAThread]
    private static int Main(string[] args)
    {
        try
        {
            if (args.Length == 2 && args[0] == "--subscription-certificate")
            {
                using var key = RSA.Create(2048);
                var certificateRequest = new CertificateRequest("CN=DobbyVPN Torturer " + Guid.NewGuid().ToString("N"), key,
                    HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
                certificateRequest.CertificateExtensions.Add(new X509BasicConstraintsExtension(true, false, 0, true));
                var names = new SubjectAlternativeNameBuilder();
                names.AddIpAddress(System.Net.IPAddress.Loopback);
                certificateRequest.CertificateExtensions.Add(names.Build());
                using var certificate = certificateRequest.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-1), DateTimeOffset.UtcNow.AddDays(1));
                File.WriteAllText(Path.Combine(args[1], "ca.pem"), certificate.ExportCertificatePem());
                File.WriteAllText(Path.Combine(args[1], "key.pem"), key.ExportPkcs8PrivateKeyPem());
                return 0;
            }
            if (args.Length == 1 && args[0] == "--diagnostics-test")
            {
                NativeDiagnosticsTests.RunAsync().GetAwaiter().GetResult();
                return 0;
            }
            SetProcessDpiAwarenessContext(new IntPtr(-4));
            using var input = JsonDocument.Parse(Console.In.ReadToEnd());
            var request = input.RootElement;
            string Text(string key) => request.GetProperty(key).GetString()!;
            var expected = Path.GetFullPath(Text("executable"));
            Process? found = null;
            if (request.TryGetProperty("pid", out var requestedPid))
            {
                try { found = Process.GetProcessById(requestedPid.GetInt32()); }
                catch (ArgumentException) { Console.WriteLine("{\"ready\":false,\"alive\":false}"); return 0; }
            }
            else
            {
                foreach (var candidate in Process.GetProcessesByName(Path.GetFileNameWithoutExtension(expected)))
                {
                    try
                    {
                        if (!string.Equals(candidate.MainModule?.FileName, expected, StringComparison.OrdinalIgnoreCase))
                        {
                            candidate.Dispose();
                            continue;
                        }
                        if (found is not null)
                        {
                            candidate.Dispose();
                            found.Dispose();
                            throw new InvalidOperationException("More than one candidate UI process matches the executable");
                        }
                        found = candidate;
                    }
                    catch (ArgumentException)
                    {
                        candidate.Dispose();
                    }
                }
            }
            if (found is null) { Console.WriteLine("{\"ready\":false,\"alive\":false}"); return 0; }
            using var process = found;
            if (process.HasExited)
            {
                Console.WriteLine("{\"ready\":false,\"alive\":false}");
                return 0;
            }
            if (!string.Equals(process.MainModule!.FileName, expected, StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("UI process executable changed");
            var identity = process.StartTime.ToUniversalTime().Ticks.ToString(CultureInfo.InvariantCulture);
            if (request.TryGetProperty("identity", out var prior) && prior.GetString() != identity)
                throw new InvalidOperationException("UI process creation time changed");
            var operation = Text("operation");
            if (operation == "probe")
            {
                process.Refresh();
                var probeWindowHandle = process.MainWindowHandle;
                Console.WriteLine(JsonSerializer.Serialize(new {
                    alive = true, pid = process.Id, identity,
                    windowHandle = probeWindowHandle == IntPtr.Zero ? null : $"0x{probeWindowHandle.ToInt64():X}"
                }));
                return 0;
            }
            if (operation == "close")
            {
                if (!process.CloseMainWindow()) throw new InvalidOperationException("Native window close request failed");
                Console.WriteLine("{}");
                return 0;
            }
            if (operation == "kill")
            {
                process.Kill(entireProcessTree: true);
                if (!process.WaitForExit(5000)) throw new TimeoutException("UI process did not exit");
                Console.WriteLine("{}");
                return 0;
            }
            var traceTree = operation == "tree";
            var traceWin32Baseline = operation == "windows-baseline";
            var traceUiaProbe = operation == "uia-connection-configuration";
            var traceWindow = traceTree || traceWin32Baseline || traceUiaProbe;
            var activateAndDiscoverWindow = traceWindow || operation == "resize-window";
            var baselineStarted = traceWin32Baseline ? Stopwatch.GetTimestamp() : 0;
            if (traceTree) TracePhase($"tree-window-discovery-start pid={process.Id}");
            if (traceWin32Baseline)
                TracePhase($"configure-tree-win32-baseline-start utc={UtcTimestamp()} pid={process.Id}");
            IntPtr window = IntPtr.Zero;
            if (activateAndDiscoverWindow)
            {
                string[] lastWindows = Array.Empty<string>();
                string lastTitle = "unavailable";
                IntPtr activationRequestedFor = IntPtr.Zero;
                try
                {
                    WaitFor(() =>
                    {
                        process.Refresh();
                        window = process.MainWindowHandle;
                        GetWindowThreadProcessId(window, out var ownerPid);
                        if (window == IntPtr.Zero || ownerPid != process.Id)
                        {
                            var ownedWindows = EnumerateProcessWindows(process).Where(candidate =>
                            {
                                GetWindowThreadProcessId(candidate, out var candidatePid);
                                return candidatePid == process.Id;
                            }).ToArray();
                            window = ownedWindows.FirstOrDefault(candidate =>
                                IsWindowVisible(candidate) && !IsIconic(candidate));
                            if (window == IntPtr.Zero)
                                window = ownedWindows.FirstOrDefault();
                        }
                        if (window != IntPtr.Zero && (!IsWindowVisible(window) || IsIconic(window)) && window != activationRequestedFor)
                        {
                            activationRequestedFor = window;
                            var showCommand = IsIconic(window) ? 9 : 5; // SW_RESTORE or SW_SHOW
                            var showQueued = ShowWindowAsync(window, showCommand);
                            var foregroundRequested = SetForegroundWindow(window);
                            TracePhase(
                                $"tree-window-activation hwnd=0x{window.ToInt64():X} " +
                                $"showCommand={showCommand} showQueued={showQueued} foregroundRequested={foregroundRequested}");
                        }
                        lastWindows = DescribeProcessWindows(process);
                        lastTitle = process.MainWindowTitle;
                        return window != IntPtr.Zero && IsWindowVisible(window) && !IsIconic(window);
                    }, "UI process did not expose a visible, non-minimized window for the tree snapshot", seconds: 20.0);
                }
                catch (TimeoutException error)
                {
                    throw new TimeoutException(
                        $"{error.Message}; pid={process.Id}; session={process.SessionId}; " +
                        $"mainWindowTitle=\"{lastTitle}\"; processTopLevelWindows=[{string.Join(" || ", lastWindows)}]",
                        error);
                }
            }
            else
            {
                process.Refresh();
                window = process.MainWindowHandle;
            }
            if (traceTree) TracePhase($"tree-window-discovery-complete hwnd=0x{window.ToInt64():X}");
            if (traceWin32Baseline)
                TracePhase($"configure-tree-win32-baseline-window-discovery-complete utc={UtcTimestamp()} hwnd=0x{window.ToInt64():X}");
            var visible = window != IntPtr.Zero && IsWindowVisible(window);
            var minimized = window != IntPtr.Zero && IsIconic(window);
            if (!visible || minimized)
            {
                uint windowOwnerPid = 0;
                if (window != IntPtr.Zero) GetWindowThreadProcessId(window, out windowOwnerPid);
                Console.WriteLine(JsonSerializer.Serialize(new
                {
                    ready = false,
                    pid = process.Id,
                    identity,
                    windowHandle = $"0x{window.ToInt64():X}",
                    visible,
                    minimized,
                    ownerPid = windowOwnerPid,
                    candidateSessionId = process.SessionId,
                    helperSessionId = Process.GetCurrentProcess().SessionId,
                    mainWindowTitle = process.MainWindowTitle,
                    windowDescription = window == IntPtr.Zero ? "unavailable" : DescribeWindow(window),
                    processTopLevelWindows = DescribeProcessWindows(process),
                    executablePath = process.MainModule?.FileName,
                    processStartUtc = process.StartTime.ToUniversalTime().ToString("O", CultureInfo.InvariantCulture),
                }));
                if (traceWin32Baseline)
                    TracePhase($"configure-tree-win32-baseline-incomplete utc={UtcTimestamp()} elapsed_ms={ElapsedMilliseconds(baselineStarted)}");
                return 0;
            }
            var windowThreadId = GetWindowThreadProcessId(window, out var owner);
            if (owner != process.Id) throw new InvalidOperationException("UI window ownership changed");
            if (traceWin32Baseline)
            {
                var boundsAvailable = GetWindowRect(window, out var bounds);
                var boundsText = boundsAvailable
                    ? $"{bounds.Left},{bounds.Top} {bounds.Right - bounds.Left}x{bounds.Bottom - bounds.Top}"
                    : "unavailable";
                Console.WriteLine(JsonSerializer.Serialize(new
                {
                    ready = true,
                    pid = process.Id,
                    identity,
                    executablePath = process.MainModule?.FileName,
                    processStartUtc = process.StartTime.ToUniversalTime().ToString("O", CultureInfo.InvariantCulture),
                    sessionId = process.SessionId,
                    threadCount = process.Threads.Count,
                    workingSetBytes = process.WorkingSet64,
                    windowHandle = $"0x{window.ToInt64():X}",
                    windowThreadId,
                    ownerPid = owner,
                    visible,
                    minimized,
                    mainWindowTitle = process.MainWindowTitle,
                    windowBounds = boundsText,
                    windowDescription = DescribeWindow(window),
                    processTopLevelWindows = DescribeProcessWindows(process),
                }));
                TracePhase($"configure-tree-win32-baseline-complete utc={UtcTimestamp()} elapsed_ms={ElapsedMilliseconds(baselineStarted)} hwnd=0x{window.ToInt64():X}");
                return 0;
            }
            if (traceUiaProbe)
                TracePhase($"configure-tree-uia-root-start utc={UtcTimestamp()} hwnd=0x{window.ToInt64():X}");
            if (traceTree) TracePhase($"tree-uia-root-start hwnd=0x{window.ToInt64():X}");
            var root = AutomationElement.FromHandle(window);
            if (traceUiaProbe)
                TracePhase($"configure-tree-uia-root-complete utc={UtcTimestamp()}");
            if (traceTree) TracePhase("tree-uia-root-complete");
            if (traceUiaProbe)
            {
                var queryStarted = Stopwatch.GetTimestamp();
                TracePhase($"configure-tree-uia-connection-configuration-start utc={UtcTimestamp()}");
                const string firstPanePath = "window/ControlType.Pane#1";
                const string targetPanePath = firstPanePath + "/ControlType.Pane#1";
                var firstPane = GetUiaProbeFirstChild(root, "window", firstPanePath);
                if (firstPane is null)
                {
                    TracePhase($"configure-tree-uia-path-incomplete missing={firstPanePath}");
                    return ReportUiaProbeMissingPath(process, identity, window, firstPanePath);
                }
                var firstPaneControlType = ReadUiaProbeProperty(
                    "FirstPane.ControlType", () => firstPane.Current.ControlType.ProgrammaticName);
                if (firstPaneControlType != ControlType.Pane.ProgrammaticName)
                    throw new InvalidOperationException(
                        $"Expected {firstPanePath} to be a Pane, found {firstPaneControlType}");
                var targetPane = GetUiaProbeFirstChild(firstPane, firstPanePath, targetPanePath);
                if (targetPane is null)
                {
                    TracePhase($"configure-tree-uia-path-incomplete missing={targetPanePath}");
                    return ReportUiaProbeMissingPath(process, identity, window, targetPanePath);
                }

                var targetControlType = ReadUiaProbeProperty(
                    "ControlType", () => targetPane.Current.ControlType.ProgrammaticName);
                if (targetControlType != ControlType.Pane.ProgrammaticName)
                    throw new InvalidOperationException(
                        $"Expected {targetPanePath} to be a Pane, found {targetControlType}");
                var targetName = ReadUiaProbeProperty("Name", () => targetPane.Current.Name);
                var targetAutomationId = ReadUiaProbeProperty(
                    "AutomationId", () => targetPane.Current.AutomationId);
                var targetClassName = ReadUiaProbeProperty(
                    "ClassName", () => targetPane.Current.ClassName);
                var targetFrameworkId = ReadUiaProbeProperty(
                    "FrameworkId", () => targetPane.Current.FrameworkId);
                var targetIsControlElement = ReadUiaProbeProperty(
                    "IsControlElement", () => targetPane.Current.IsControlElement);
                var targetIsContentElement = ReadUiaProbeProperty(
                    "IsContentElement", () => targetPane.Current.IsContentElement);
                var targetBounds = ReadUiaProbeProperty("BoundingRectangle", () =>
                {
                    var bounds = targetPane.Current.BoundingRectangle;
                    return new
                    {
                        left = bounds.Left,
                        top = bounds.Top,
                        right = bounds.Right,
                        bottom = bounds.Bottom,
                    };
                });

                TracePhase($"configure-tree-uia-target-expansion-start path={targetPanePath}");
                AutomationElement? targetChild;
                try
                {
                    targetChild = TreeWalker.ControlViewWalker.GetFirstChild(targetPane);
                    TracePhase(
                        $"configure-tree-uia-target-expansion-complete path={targetPanePath} " +
                        $"has_child={targetChild is not null}");
                }
                catch (Exception error)
                {
                    TracePhase(
                        $"configure-tree-uia-target-expansion-failed path={targetPanePath} " +
                        $"exception_type={error.GetType().FullName} message={JsonSerializer.Serialize(error.Message)}");
                    throw;
                }
                TracePhase(
                    $"configure-tree-uia-connection-configuration-complete utc={UtcTimestamp()} " +
                    $"elapsed_ms={ElapsedMilliseconds(queryStarted)} expansion_completed=true");
                Console.WriteLine(JsonSerializer.Serialize(new
                {
                    ready = true,
                    pid = process.Id,
                    identity,
                    windowHandle = $"0x{window.ToInt64():X}",
                    query = "ControlView.GetFirstChild(targetPane)",
                    targetPane = new
                    {
                        path = targetPanePath,
                        controlType = targetControlType,
                        name = targetName,
                        automationId = targetAutomationId,
                        className = targetClassName,
                        frameworkId = targetFrameworkId,
                        isControlElement = targetIsControlElement,
                        isContentElement = targetIsContentElement,
                        boundingRectangle = targetBounds,
                    },
                    expansionCompleted = true,
                    targetChildPresent = targetChild is not null,
                }));
                return 0;
            }
            if (operation == "resize-window")
            {
                if (!GetWindowRect(window, out var original))
                    throw new InvalidOperationException("Could not read native window bounds before resize");
                var width = request.TryGetProperty("width", out var requestedWidth) ? requestedWidth.GetInt32() : 0;
                var height = request.TryGetProperty("height", out var requestedHeight) ? requestedHeight.GetInt32() : 0;
                if (width < 560 || height < 460)
                    throw new ArgumentOutOfRangeException("width", "Requested native window is below its supported minimum");
                var left = request.TryGetProperty("left", out var requestedLeft) ? requestedLeft.GetInt32() : original.Left;
                var top = request.TryGetProperty("top", out var requestedTop) ? requestedTop.GetInt32() : original.Top;
                if (!SetWindowPos(window, IntPtr.Zero, left, top, width, height, 0x0004 | 0x0010))
                    throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "Could not resize the native window");
                Rect resized = default;
                WaitFor(() => GetWindowRect(window, out resized) &&
                    Math.Abs((resized.Right - resized.Left) - width) <= 20 &&
                    Math.Abs((resized.Bottom - resized.Top) - height) <= 20,
                    $"Native window did not settle at the requested {width}x{height} size");
                Console.WriteLine(JsonSerializer.Serialize(new
                {
                    ready = true,
                    left = original.Left,
                    top = original.Top,
                    width = original.Right - original.Left,
                    height = original.Bottom - original.Top,
                    resized_width = resized.Right - resized.Left,
                    resized_height = resized.Bottom - resized.Top,
                }));
                return 0;
            }
            AutomationElement Find(string name, bool editor = false, bool actionable = false)
            {
                AutomationElement? FindBy(bool automationId)
                {
                    foreach (var candidate in Walk(root,
                                 traceTree ? message => TracePhase($"tree-action-{message}") : null))
                    {
                        var current = candidate.Current;
                        if (!current.IsControlElement || current.IsOffscreen ||
                            (automationId ? current.AutomationId : current.Name) != name ||
                            (editor && current.ControlType != ControlType.Edit))
                            continue;
                        if (actionable &&
                            !candidate.TryGetCurrentPattern(InvokePattern.Pattern, out _) &&
                            !candidate.TryGetCurrentPattern(SelectionItemPattern.Pattern, out _))
                            continue;
                        return candidate;
                    }
                    return null;
                }

                // Resolve stable identifiers before user-facing labels to avoid matching a tab and its label.
                var element = FindBy(automationId: true) ?? FindBy(automationId: false);
                if (element is null)
                    throw new InvalidOperationException($"Expected one visible {name}, found no matching control");
                if (!element.Current.IsEnabled) throw new InvalidOperationException($"Control disabled: {name}");
                return element;
            }
            if (operation == "logs")
            {
                var logRoot = Find("Backend logs");
                var entries = Walk(logRoot)
                    .Where(element => element.Current.ControlType == ControlType.Text)
                    .Select(element =>
                    {
                        var text = element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern)
                            ? ((TextPattern)pattern).DocumentRange.GetText(-1) : element.Current.Name;
                        int? foreground = null;
                        if (element.TryGetCurrentPattern(TextPattern.Pattern, out pattern))
                        {
                            var value = ((TextPattern)pattern).DocumentRange.GetAttributeValue(TextPattern.ForegroundColorAttribute);
                            if (value is int color) foreground = color;
                        }
                        return new { text, foreground };
                    })
                    .Where(entry => entry.text.Length > 0)
                    .ToArray();
                string expandedRecord = "";
                var expansionVerified = false;
                var details = Walk(logRoot).FirstOrDefault(element =>
                    element.Current.Name == "Details" &&
                    element.TryGetCurrentPattern(ExpandCollapsePattern.Pattern, out _));
                if (details is not null && details.TryGetCurrentPattern(ExpandCollapsePattern.Pattern, out var expand))
                {
                    var control = (ExpandCollapsePattern)expand;
                    var wasExpanded = control.Current.ExpandCollapseState == ExpandCollapseState.Expanded;
                    try
                    {
                        if (!wasExpanded) control.Expand();
                        expandedRecord = Walk(logRoot)
                            .Where(element => element.Current.ControlType == ControlType.Text)
                            .Select(element => element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern)
                                ? ((TextPattern)pattern).DocumentRange.GetText(-1) : element.Current.Name)
                            .FirstOrDefault(value => value.TrimStart().StartsWith("{", StringComparison.Ordinal)) ?? "";
                        expansionVerified = expandedRecord.Length > 0;
                    }
                    finally
                    {
                        if (!wasExpanded) control.Collapse();
                    }
                }
                Console.WriteLine(JsonSerializer.Serialize(new {
                    ready = true,
                    text = string.Join("\n", entries.Select(entry => entry.text)),
                    entries,
                    expanded_record = expandedRecord,
                    expansion_verified = expansionVerified
                }));
                return 0;
            }
            if (operation == "select-log-text")
            {
                var entry = Walk(Find("Backend logs"))
                    .Where(element => element.Current.ControlType == ControlType.Text && !element.Current.IsOffscreen)
                    .FirstOrDefault(element => element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern) &&
                        ((TextPattern)pattern).DocumentRange.GetText(-1).Contains(" · ", StringComparison.Ordinal));
                if (entry is null || !entry.TryGetCurrentPattern(TextPattern.Pattern, out var entryPattern))
                    throw new InvalidOperationException("No structured log entry is available for text selection");
                var range = ((TextPattern)entryPattern).DocumentRange;
                range.Select();
                var selected = ((TextPattern)entryPattern).GetSelection()
                    .Select(selection => selection.GetText(-1)).FirstOrDefault() ?? "";
                if (selected.Length == 0)
                    throw new InvalidOperationException("Native log text selection returned no selected text");
                Console.WriteLine(JsonSerializer.Serialize(new { ready = true, selected }));
                return 0;
            }
            if (operation == "log-position")
            {
                var logRoot = Find("Backend logs");
                if (!logRoot.TryGetCurrentPattern(ScrollPattern.Pattern, out var scrollPattern))
                    throw new InvalidOperationException("Native log viewer does not expose scrolling");
                var scroll = (ScrollPattern)scrollPattern;
                var position = scroll.Current.VerticalScrollPercent;
                if (position < 0)
                    throw new InvalidOperationException("Native log viewer does not expose a vertical scroll position");
                var firstVisibleRecord = Walk(logRoot)
                    .Where(element => element.Current.ControlType == ControlType.Text && !element.Current.IsOffscreen)
                    .Select(element => element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern)
                        ? ((TextPattern)pattern).DocumentRange.GetText(-1) : element.Current.Name)
                    .FirstOrDefault(text => text.Contains(" · ", StringComparison.Ordinal)) ?? "";
                Console.WriteLine(JsonSerializer.Serialize(new {
                    ready = true, vertical_scroll_percent = position,
                    visible_first_record = firstVisibleRecord
                }));
                return 0;
            }
            if (operation == "scroll-logs")
            {
                var position = Text("position");
                if (position is not ("top" or "bottom")) throw new ArgumentException("Log scroll position must be top or bottom");
                var logRoot = Find("Backend logs");
                if (!logRoot.TryGetCurrentPattern(ScrollPattern.Pattern, out var scrollPattern))
                    throw new InvalidOperationException("Native log viewer does not expose scrolling");
                var scroll = (ScrollPattern)scrollPattern;
                if (scroll.Current.VerticalScrollPercent < 0)
                    throw new InvalidOperationException("Native log viewer does not expose a vertical scroll range");
                if (!SetForegroundWindow(window)) throw new InvalidOperationException("Could not activate UI before log scrolling");
                logRoot.SetFocus();
                Forms.SendKeys.SendWait(position == "top" ? "{HOME}" : "{END}");
                scroll.SetScrollPercent(ScrollPattern.NoScroll, position == "top" ? 0 : 100);
                Thread.Sleep(100);
                var actual = scroll.Current.VerticalScrollPercent;
                var atRequestedEnd = position == "top" ? actual <= 1 : actual >= 99;
                if (!atRequestedEnd) throw new InvalidOperationException($"Log viewer did not scroll to {position}; position={actual}");
                Console.WriteLine(JsonSerializer.Serialize(new { ready = true, position = actual }));
                return 0;
            }
            if (operation == "tree")
            {
                string[] labels;
                string[] enabled_controls;
                string[] help_texts;
                try
                {
                    TracePhase("tree-uia-targeted-start");
                    var visibleControls = Walk(root, message => TracePhase($"tree-uia-{message}"))
                        .Where(element =>
                        {
                            var current = element.Current;
                            return current.IsControlElement && !current.IsOffscreen;
                        })
                        .ToList();
                    var profileCount = visibleControls.Count(element =>
                    {
                        var id = element.Current.AutomationId;
                        return id.StartsWith("Profile ", StringComparison.Ordinal) &&
                               id.EndsWith(" action", StringComparison.Ordinal);
                    });
                    if (profileCount >= 8192)
                        throw new InvalidOperationException("Visible profile actions exceed 8192 controls");

                    labels = visibleControls.SelectMany(e => new[] { e.Current.AutomationId, e.Current.Name })
                        .Where(value => value.Length > 0).Distinct(StringComparer.Ordinal).ToArray();
                    enabled_controls = visibleControls.Where(e => e.Current.IsEnabled)
                        .SelectMany(e => new[] { e.Current.AutomationId, e.Current.Name })
                        .Where(value => value.Length > 0).Distinct(StringComparer.Ordinal).ToArray();
                    help_texts = visibleControls.Select(e => e.Current.HelpText)
                        .Where(value => value.Length > 0).Distinct(StringComparer.Ordinal).ToArray();
                    TracePhase($"tree-uia-targeted-complete controls={visibleControls.Count} profiles={profileCount}");
                }
                catch (ElementNotAvailableException error)
                {
                    Console.Error.WriteLine(error.ToString());
                    Console.Error.Flush();
                    Console.WriteLine(JsonSerializer.Serialize(new {
                        ready = false, alive = true, pid = process.Id, identity
                    }));
                    return 0;
                }
                catch (COMException error) when (error.HResult == unchecked((int)0x8000FFFF))
                {
                    Console.WriteLine(JsonSerializer.Serialize(new {
                        ready = false, alive = true, pid = process.Id, identity,
                        uiaError = error.ToString()
                    }));
                    return 0;
                }
                Console.WriteLine(JsonSerializer.Serialize(new {
                    ready = true, pid = process.Id, identity, labels, enabled_controls, help_texts
                }));
                return 0;
            }
            long? pasteInvokedAtUnixMs = null;
            switch (operation)
            {
                case "focus":
                    if (!SetForegroundWindow(window)) throw new InvalidOperationException("Could not activate UI window");
                    break;
                case "click":
                    var element = Find(Text("target"), actionable: true);
                    if (!SetForegroundWindow(window)) throw new InvalidOperationException("Could not activate UI window");
                    if (element.TryGetCurrentPattern(InvokePattern.Pattern, out var invoke))
                        ((InvokePattern)invoke).Invoke();
                    else if (element.TryGetCurrentPattern(SelectionItemPattern.Pattern, out var select))
                        ((SelectionItemPattern)select).Select();
                    else throw new InvalidOperationException("Control has no native invoke or selection action");
                    break;
                case "type":
                    TracePhase("type-find-editor");
                    var editor = Find("Connection configuration", editor: true);
                    TracePhase("type-read-profile");
                    var value = File.ReadAllText(Text("source"));
                    TracePhase("type-activate-window");
                    if (!SetForegroundWindow(window)) throw new InvalidOperationException("Could not activate UI window");
                    TracePhase("type-focus-editor");
                    editor.SetFocus();
                    TracePhase("type-read-clipboard");
                    var previous = Forms.Clipboard.GetDataObject();
                    var clipboard = new Forms.DataObject();
                    if (previous is not null)
                    {
                        TracePhase("type-enumerate-clipboard-formats");
                        var formats = previous.GetFormats(false);
                        for (var index = 0; index < formats.Length; index++)
                        {
                            TracePhase($"type-copy-clipboard-format-{index + 1}");
                            var format = formats[index];
                            clipboard.SetData(format, false, previous.GetData(format, false));
                        }
                    }
                    Exception? primary = null;
                    try
                    {
                        TracePhase("type-set-clipboard-text");
                        // Windows text clipboard data requires CRLF line endings.
                        Forms.Clipboard.SetText(
                            NormalizeLineEndings(value).Replace("\n", "\r\n"),
                            Forms.TextDataFormat.UnicodeText);
                        TracePhase("type-send-select-all");
                        Forms.SendKeys.SendWait("^a");
                        TracePhase("type-send-paste");
                        Forms.SendKeys.SendWait("^v");
                        TracePhase("type-verify-pasted-value");
                        var limit = Stopwatch.StartNew();
                        string observed = "";
                        do
                        {
                            observed = ((ValuePattern)editor.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;
                            if (NormalizeLineEndings(observed) == NormalizeLineEndings(value)) break;
                            Thread.Sleep(50);
                        } while (limit.Elapsed.TotalSeconds < 5);
                        if (NormalizeLineEndings(observed) != NormalizeLineEndings(value))
                            throw new InvalidOperationException("Native pasted configuration does not match the source");
                    }
                    catch (Exception error) { primary = error; throw; }
                    finally
                    {
                        try
                        {
                            if (previous is null)
                            {
                                TracePhase("type-clear-clipboard");
                                Forms.Clipboard.Clear();
                            }
                            else
                            {
                                TracePhase("type-restore-clipboard");
                                Forms.Clipboard.SetDataObject(clipboard, true);
                            }
                        }
                        catch (Exception cleanup)
                        {
                            if (primary is null) throw;
                            throw new AggregateException(primary, cleanup);
                        }
                    }
                    break;
                case "paste":
                    TracePhase("paste-find-editor");
                    var pasteEditor = Find("Connection configuration", editor: true);
                    var pasteValue = File.ReadAllText(Text("source")).Trim();
                    var fieldBeforeClipboard = ((ValuePattern)pasteEditor.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;
                    TracePhase("paste-activate-window");
                    if (!SetForegroundWindow(window)) throw new InvalidOperationException("Could not activate UI window");
                    TracePhase("paste-read-clipboard");
                    var previousPaste = Forms.Clipboard.GetDataObject();
                    var savedClipboard = new Forms.DataObject();
                    if (previousPaste is not null)
                    {
                        foreach (var format in previousPaste.GetFormats(false))
                            savedClipboard.SetData(format, false, previousPaste.GetData(format, false));
                    }
                    Exception? pasteFailure = null;
                    try
                    {
                        bool PasteAvailable()
                        {
                            var pasteButton = Walk(root).FirstOrDefault(element =>
                            {
                                var current = element.Current;
                                return current.Name == "Paste" &&
                                       current.ControlType == ControlType.Button &&
                                       current.IsControlElement &&
                                       !current.IsOffscreen &&
                                       current.IsEnabled &&
                                       element.TryGetCurrentPattern(InvokePattern.Pattern, out _);
                            });
                            return pasteButton is not null;
                        }
                        void WaitForPasteAvailability(bool expected, string clipboardDescription)
                        {
                            var availabilityWait = Stopwatch.StartNew();
                            while (PasteAvailable() != expected)
                            {
                                if (availabilityWait.Elapsed.TotalSeconds >= 5)
                                    throw new TimeoutException($"Native Paste availability did not match {clipboardDescription} clipboard content");
                                Thread.Sleep(50);
                            }
                        }
                        void RequireEditorUnchanged(string stage)
                        {
                            var observed = ((ValuePattern)pasteEditor.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;
                            if (NormalizeLineEndings(observed) != NormalizeLineEndings(fieldBeforeClipboard))
                                throw new InvalidOperationException($"Clipboard {stage} changed the subscription field before Paste was tapped");
                        }

                        TracePhase("paste-clear-clipboard");
                        Forms.Clipboard.Clear();
                        WaitForPasteAvailability(false, "empty");
                        RequireEditorUnchanged("empty availability check");

                        TracePhase("paste-set-bitmap-only-clipboard");
                        using (var bitmap = new Bitmap(2, 2))
                        {
                            var nonText = new Forms.DataObject();
                            nonText.SetData(Forms.DataFormats.Bitmap, false, bitmap);
                            Forms.Clipboard.SetDataObject(nonText, true);
                        }
                        WaitForPasteAvailability(false, "non-text");
                        RequireEditorUnchanged("non-text availability check");

                        TracePhase("paste-set-clipboard-text");
                        Forms.Clipboard.SetText(pasteValue, Forms.TextDataFormat.UnicodeText);
                        TracePhase("paste-wait-for-button");
                        WaitForPasteAvailability(true, "text");
                        AutomationElement pasteButton;
                        pasteButton = Find("Paste", actionable: true);
                        var fieldAtAvailability = ((ValuePattern)pasteEditor.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;
                        if (NormalizeLineEndings(fieldAtAvailability) != NormalizeLineEndings(fieldBeforeClipboard))
                            throw new InvalidOperationException("Clipboard availability inspection changed the configuration before Paste was tapped");
                        if (!pasteButton.TryGetCurrentPattern(InvokePattern.Pattern, out var pasteInvoke))
                            throw new InvalidOperationException("Native Paste button has no invoke action");
                        TracePhase("paste-invoke-button");
                        pasteInvokedAtUnixMs = DateTimeOffset.UtcNow.ToUnixTimeMilliseconds();
                        ((InvokePattern)pasteInvoke).Invoke();
                        TracePhase("paste-verify-field");
                        var valueWait = Stopwatch.StartNew();
                        string pasteObserved = "";
                        do
                        {
                            pasteObserved = ((ValuePattern)pasteEditor.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;
                            if (NormalizeLineEndings(pasteObserved).Trim() == NormalizeLineEndings(pasteValue)) break;
                            Thread.Sleep(50);
                        } while (valueWait.Elapsed.TotalSeconds < 5);
                        if (NormalizeLineEndings(pasteObserved).Trim() != NormalizeLineEndings(pasteValue))
                            throw new InvalidOperationException("Native Paste did not place clipboard text in the subscription field");

                        TracePhase("paste-verify-narrow-window-layout");
                        VerifyNarrowWindow(root, window, process.Id, Text("source"));
                    }
                    catch (Exception error) { pasteFailure = error; throw; }
                    finally
                    {
                        try
                        {
                            if (previousPaste is null)
                            {
                                TracePhase("paste-clear-clipboard");
                                Forms.Clipboard.Clear();
                            }
                            else
                            {
                                TracePhase("paste-restore-clipboard");
                                Forms.Clipboard.SetDataObject(savedClipboard, true);
                            }
                        }
                        catch (Exception cleanup)
                        {
                            if (pasteFailure is null) throw;
                            throw new AggregateException(pasteFailure, cleanup);
                        }
                    }
                    break;
                case "capture":
                    Capture(window, process.Id, Text("path"));
                    break;
                default: throw new ArgumentException($"Unknown operation: {operation}");
            }
            Console.WriteLine(JsonSerializer.Serialize(new {
                ready = true, pid = process.Id, identity,
                labels = Array.Empty<string>(),
                paste_invoked_at_unix_ms = pasteInvokedAtUnixMs
            }));
            return 0;
        }
        catch (Exception error) { Console.Error.WriteLine(error); return 1; }
    }

    private static IEnumerable<AutomationElement> Walk(AutomationElement root, Action<string>? trace = null)
    {
        const int maximumDepth = 64;
        var queue = new Queue<(AutomationElement Element, int Depth, string Path)>();
        queue.Enqueue((root, 0, "window"));
        int count = 0;
        var walker = TreeWalker.ControlViewWalker;
        while (queue.Count > 0)
        {
            var (element, depth, path) = queue.Dequeue();
            if (++count > 8192)
                throw new InvalidOperationException($"Accessibility tree exceeds 8192 elements at {path}");
            yield return element;
            trace?.Invoke($"children-start node={count} depth={depth} path={path}");
            var child = walker.GetFirstChild(element);
            trace?.Invoke($"first-child-complete node={count} has_child={child is not null}");
            if (child is not null && depth >= maximumDepth)
                throw new InvalidOperationException($"Accessibility tree exceeds depth {maximumDepth} at {path}");
            var siblingIndex = 0;
            while (child is not null)
            {
                siblingIndex++;
                var current = child.Current;
                var segment = $"{current.ControlType.ProgrammaticName}#{siblingIndex}";
                if (!string.IsNullOrWhiteSpace(current.AutomationId))
                    segment += $"[{JsonSerializer.Serialize(current.AutomationId)}]";
                var childPath = $"{path}/{segment}";
                trace?.Invoke($"child-observed parent_node={count} child_index={siblingIndex} path={childPath}");
                queue.Enqueue((child, depth + 1, childPath));
                trace?.Invoke($"next-sibling-start parent_node={count} child_index={siblingIndex} path={childPath}");
                child = walker.GetNextSibling(child);
                trace?.Invoke($"next-sibling-complete parent_node={count} child_index={siblingIndex} has_sibling={child is not null}");
            }
            trace?.Invoke($"children-complete node={count} depth={depth} child_count={siblingIndex} path={path}");
        }
    }

    private static void Capture(IntPtr window, int expectedPid, string path)
    {
        GetWindowThreadProcessId(window, out var owner);
        if (owner != expectedPid) throw new InvalidOperationException("UI window ownership changed before screenshot");
        if (!GetWindowRect(window, out var r) || r.Right - r.Left < 300 || r.Bottom - r.Top < 300)
            throw new InvalidOperationException("UI window bounds unavailable or too small");
        var target = Rectangle.FromLTRB(r.Left, r.Top, r.Right, r.Bottom);
        var display = Forms.Screen.AllScreens.FirstOrDefault(screen => screen.Bounds.Contains(target));
        if (display is null)
            throw new InvalidOperationException("UI window is partly offscreen or crosses a display gap");
        var client = ClientScreenBounds(window, target);
        var clientInBitmap = new Rectangle(
            client.Left - target.Left,
            client.Top - target.Top,
            client.Width,
            client.Height
        );
        PrepareCaptureCursor(window, target, client, display.Bounds);
        void RequireUnobstructed()
        {
            for (var other = GetWindow(window, 3); other != IntPtr.Zero; other = GetWindow(other, 3))
            {
                if (!IsWindowVisible(other) || IsIconic(other) || !GetWindowRect(other, out var bounds)) continue;
                if (target.IntersectsWith(Rectangle.FromLTRB(bounds.Left, bounds.Top, bounds.Right, bounds.Bottom)))
                    throw new InvalidOperationException($"UI screenshot obstructed by {DescribeWindow(other)}");
            }
        }
        using var bitmap = new Bitmap(target.Width, target.Height, PixelFormat.Format32bppArgb);
        var clientRendered = false;
        var renderWait = Stopwatch.StartNew();
        var frameCount = 0;
        do
        {
            if (renderWait.Elapsed.TotalSeconds >= 5) break;
            Thread.Sleep(50);
            RequireUnobstructed();
            using (var graphics = Graphics.FromImage(bitmap))
                graphics.CopyFromScreen(target.Location, Point.Empty, target.Size);
            frameCount++;
            clientRendered = HasNonuniformClientPixels(bitmap, clientInBitmap, renderWait);
        } while (!clientRendered && renderWait.Elapsed.TotalSeconds < 5);
        if (!clientRendered)
            throw new InvalidOperationException(
                $"UI client area did not render within 5 seconds ({frameCount} frames captured)"
            );
        bitmap.Save(path, ImageFormat.Png);
        RequireUnobstructed();
        GetWindowThreadProcessId(window, out var afterOwner);
        if (afterOwner != expectedPid) throw new InvalidOperationException("UI window ownership changed during screenshot");
        if (!GetWindowRect(window, out var after) || !r.Equals(after))
            throw new InvalidOperationException("Native window changed during screenshot");
    }
}
