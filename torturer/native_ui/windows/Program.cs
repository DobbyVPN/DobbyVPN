using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Linq;
using System.Globalization;
using System.Runtime.InteropServices;
using System.Runtime.ExceptionServices;
using System.Text;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Threading;
using System.Windows.Automation;
using Forms = System.Windows.Forms;

// Runs only in the interactive test user's session, outside the shipped app.
internal static partial class Program
{
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] private static extern IntPtr WindowFromPoint(NativePoint point);
    [DllImport("user32.dll")] private static extern IntPtr GetAncestor(IntPtr window, uint flags);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetWindowRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);
    [DllImport("user32.dll")] private static extern IntPtr GetWindow(IntPtr window, uint command);
    [DllImport("user32.dll", SetLastError = true)] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("kernel32.dll")] private static extern uint GetCurrentThreadId();
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool ProcessIdToSessionId(uint processId, out uint sessionId);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetClientRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool ClientToScreen(IntPtr window, ref NativePoint point);
    [DllImport("user32.dll", SetLastError = true)] private static extern IntPtr SetThreadDpiAwarenessContext(IntPtr dpiContext);
    [DllImport("user32.dll", SetLastError = true, EntryPoint = "PostMessageW")]
    private static extern bool PostMessage(IntPtr window, uint message, IntPtr wParam, IntPtr lParam);
    [DllImport("user32.dll", SetLastError = true, EntryPoint = "SendMessageTimeoutW")]
    private static extern IntPtr SendMessageTimeout(
        IntPtr window, uint message, IntPtr wParam, IntPtr lParam, uint flags, uint timeoutMs, out UIntPtr result);
    [DllImport("user32.dll", SetLastError = true)]
    private static extern uint SendInput(uint numberOfInputs, NativeInput[] inputs, int size);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetCursorPos(out NativePoint point);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetClassNameW", SetLastError = true)]
    private static extern int GetClassName(IntPtr window, StringBuilder className, int maxCount);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetWindowTextLengthW")]
    private static extern int GetWindowTextLength(IntPtr window);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetWindowTextW")]
    private static extern int GetWindowText(IntPtr window, StringBuilder text, int maxCount);
    private delegate bool EnumThreadWindowsCallback(IntPtr window, IntPtr parameter);
    private delegate bool EnumChildWindowsCallback(IntPtr window, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern bool EnumThreadWindows(uint threadId, EnumThreadWindowsCallback callback, IntPtr parameter);
    [DllImport("user32.dll")]
    private static extern bool EnumChildWindows(IntPtr parent, EnumChildWindowsCallback callback, IntPtr parameter);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] private static extern bool SetProcessDpiAwarenessContext(IntPtr context);
    [StructLayout(LayoutKind.Sequential)] private struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] private struct NativePoint { public int X, Y; }
    // INPUT and MOUSEINPUT use a pointer-sized dwExtraInfo on both architectures.
    [StructLayout(LayoutKind.Sequential)]
    private struct NativeInput
    {
        public uint Type;
        public NativeInputUnion Union;
    }
    [StructLayout(LayoutKind.Explicit)]
    private struct NativeInputUnion
    {
        [FieldOffset(0)] public NativeMouseInput Mouse;
    }
    [StructLayout(LayoutKind.Sequential)]
    private struct NativeMouseInput
    {
        public int Dx, Dy;
        public uint MouseData, Flags, Time;
        public UIntPtr ExtraInfo;
    }

    private const string WindowsTextSizeSettingsUri = "ms-settings:easeofaccess-display";
    private const string WindowsTextSizeSliderAutomationId = "SystemSettings_EaseOfAccess_Experience_TextScalingDesktop_Slider";
    private const string WindowsTextSizeApplyAutomationId = "SystemSettings_EaseOfAccess_Experience_TextScalingDesktop_ButtonRemove";
    private const uint WmClose = 0x0010;
    private const uint WmNcHitTest = 0x0084;
    private const uint SmtoAbortIfHung = 0x0002;
    private const uint SmtoErrorOnExit = 0x0020;
    private const uint GaRoot = 2;
    private const uint GaRootOwner = 3;
    private const uint GwOwner = 4;
    private const int HtCaption = 2;
    private const uint InputMouse = 0;
    private const uint MouseEventLeftDown = 0x0002;
    private const uint MouseEventLeftUp = 0x0004;
    private const uint MouseEventWheel = 0x0800;

    private readonly record struct SettingsWindowCandidate(
        IntPtr Window,
        int OwnerProcessId,
        string OwnerProcessName,
        int OwnerSessionId,
        long OwnerStartTimeUtcTicks,
        string WindowClassName,
        bool IsApplicationFrameHost);

    private static NativePoint GetPhysicalClientOrigin(IntPtr window) =>
        InPerMonitorV2DpiContext(() =>
        {
            var point = new NativePoint();
            if (!ClientToScreen(window, ref point))
                throw new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not map the HWND client origin to screen coordinates");
            return point;
        });

    private static System.Windows.Rect GetPhysicalWindowRect(IntPtr window) =>
        InPerMonitorV2DpiContext(() =>
        {
            if (!GetWindowRect(window, out var bounds))
                throw new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not read the HWND rectangle in physical pixels");
            return new System.Windows.Rect(
                bounds.Left, bounds.Top, bounds.Right - bounds.Left, bounds.Bottom - bounds.Top);
        });

    private static T InPerMonitorV2DpiContext<T>(Func<T> operation)
    {
        var previousContext = SetThreadDpiAwarenessContext(new IntPtr(-4)); // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
        if (previousContext == IntPtr.Zero)
            throw new System.ComponentModel.Win32Exception(
                Marshal.GetLastWin32Error(), "Could not enter the physical-pixel DPI context for the point snapshot");

        T result = default!;
        Exception? failure = null;
        try
        {
            result = operation();
        }
        catch (Exception error)
        {
            failure = error;
        }

        try
        {
            if (SetThreadDpiAwarenessContext(previousContext) == IntPtr.Zero)
                throw new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not restore the native UI thread DPI context after the point snapshot");
        }
        catch (Exception restoreFailure)
        {
            failure = failure is null ? restoreFailure : new AggregateException(failure, restoreFailure);
        }

        if (failure is not null)
            ExceptionDispatchInfo.Capture(failure).Throw();
        return result;
    }

    private static Dictionary<string, object?> DescribeWindowContext(
        IntPtr window,
        bool includeGeometry)
    {
        var result = new Dictionary<string, object?>
        {
            ["handle"] = $"0x{window.ToInt64():X}",
            ["found"] = window != IntPtr.Zero,
        };
        if (window == IntPtr.Zero) return result;

        Marshal.SetLastPInvokeError(0);
        var threadId = GetWindowThreadProcessId(window, out var processId);
        result["threadId"] = threadId;
        result["processId"] = processId;
        if (threadId == 0 || processId == 0)
            result["identityError"] = "GetWindowThreadProcessId returned a zero thread or process ID";

        if (processId != 0)
        {
            Marshal.SetLastPInvokeError(0);
            if (ProcessIdToSessionId(processId, out var sessionId))
                result["sessionId"] = sessionId;
            else
                result["sessionIdError"] = new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), $"Could not read session ID for process {processId}").ToString();
        }

        if (includeGeometry)
        {
            try
            {
                Marshal.SetLastPInvokeError(0);
                if (!GetWindowRect(window, out var rect))
                    throw new System.ComponentModel.Win32Exception(
                        Marshal.GetLastWin32Error(), "Could not read the window rectangle in physical pixels");
                result["rect"] = new
                {
                    left = rect.Left,
                    top = rect.Top,
                    right = rect.Right,
                    bottom = rect.Bottom,
                    width = rect.Right - rect.Left,
                    height = rect.Bottom - rect.Top,
                };
            }
            catch (Exception error)
            {
                result["rectError"] = error.ToString();
            }

            try
            {
                var className = new StringBuilder(256);
                Marshal.SetLastPInvokeError(0);
                var length = GetClassName(window, className, className.Capacity);
                if (length == 0)
                    throw new System.ComponentModel.Win32Exception(
                        Marshal.GetLastWin32Error(), "Could not read the window class name");
                result["className"] = className.ToString();
            }
            catch (Exception error)
            {
                result["classNameError"] = error.ToString();
            }
        }
        return result;
    }

    private static AutomationElement RequireAutomationId(AutomationElement root, string automationId)
    {
        var condition = new PropertyCondition(AutomationElement.AutomationIdProperty, automationId);
        return root.FindFirst(TreeScope.Descendants, condition)
            ?? throw new InvalidOperationException($"Native UI element was not found: {automationId}");
    }

    private static System.Windows.Rect PhysicalBounds(AutomationElement element, string description)
    {
        var bounds = element.Current.BoundingRectangle;
        if (!HasUsableBounds(bounds))
            throw new InvalidOperationException($"Native UI element has no usable screen rectangle: {description}");
        return bounds;
    }

    private static bool HasUsableBounds(System.Windows.Rect bounds) =>
        !bounds.IsEmpty && double.IsFinite(bounds.Left) && double.IsFinite(bounds.Top) &&
        double.IsFinite(bounds.Width) && double.IsFinite(bounds.Height) &&
        bounds.Width > 0 && bounds.Height > 0;

    private static object RectJson(System.Windows.Rect bounds) => new
    {
        x = bounds.Left,
        y = bounds.Top,
        width = bounds.Width,
        height = bounds.Height,
    };

    private static bool FullyInside(System.Windows.Rect element, System.Windows.Rect viewport) =>
        element.Left >= viewport.Left && element.Top >= viewport.Top &&
        element.Right <= viewport.Right && element.Bottom <= viewport.Bottom;

    private static Dictionary<string, object?> ProfileListLayout(AutomationElement root, IntPtr window)
    {
        var controls = RequireAutomationId(root, "Connection controls");
        var profileViewport = RequireAutomationId(root, "Profile list viewport");
        var connectionAction = RequireAutomationId(root, "VPN connection action");
        var logs = RequireAutomationId(root, "Backend logs");
        double? scrollPosition = null;
        if (profileViewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var scrollPattern))
        {
            var verticalPosition = ((ScrollPattern)scrollPattern).Current.VerticalScrollPercent;
            if (verticalPosition >= 0) scrollPosition = verticalPosition;
        }

        var viewportBounds = PhysicalBounds(profileViewport, "Profile list viewport");
        var profileElements = profileViewport.FindAll(TreeScope.Descendants, new OrCondition(
            new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Text),
            new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Button)));
        var visibleActions = new List<(int Index, string AutomationId)>();
        var profileNames = new Dictionary<int, (string Name, string AutomationId)>();
        var profileProtocols = new Dictionary<int, (string Protocol, string AutomationId)>();
        var profileActions = new Dictionary<int, (string Action, string AutomationId)>();
        var profileOrder = new List<int>();
        string? profileDigest = null;
        foreach (AutomationElement element in profileElements)
        {
            var automationId = element.Current.AutomationId;
            if (DobbyVPN.Windows.ProfileAutomationIds.TryParse(automationId, out var digest, out var profileIndex, out var role))
            {
                if (profileDigest is not null && profileDigest != digest)
                    throw new InvalidOperationException("Rendered profile rows did not share one accepted configuration digest");
                profileDigest = digest;
                var renderedText = element.Current.Name;
                switch (role)
                {
                    case "description":
                        if (string.IsNullOrWhiteSpace(renderedText) ||
                            !profileNames.TryAdd(profileIndex, (renderedText, automationId)))
                            throw new InvalidOperationException(
                                $"Profile {profileIndex} has missing or duplicate rendered description text: {renderedText}");
                        profileOrder.Add(profileIndex);
                        break;
                    case "protocol":
                        if (string.IsNullOrWhiteSpace(renderedText) ||
                            !profileProtocols.TryAdd(profileIndex, (renderedText, automationId)))
                            throw new InvalidOperationException(
                                $"Profile {profileIndex} has missing or duplicate rendered protocol text: {renderedText}");
                        break;
                    case "action":
                        if (!profileActions.TryAdd(profileIndex, (renderedText, automationId)))
                            throw new InvalidOperationException($"Profile {profileIndex} has duplicate rendered action controls");

                        var actionBounds = element.Current.BoundingRectangle;
                        if (element.Current.ControlType == ControlType.Button && HasUsableBounds(actionBounds) &&
                            FullyInside(actionBounds, viewportBounds))
                            visibleActions.Add((profileIndex, automationId));
                        break;
                }
            }
        }

        if (profileNames.Count != profileProtocols.Count || profileNames.Count != profileActions.Count ||
            profileNames.Keys.Any(index => !profileProtocols.ContainsKey(index) || !profileActions.ContainsKey(index)))
            throw new InvalidOperationException("Rendered profile descriptions, protocols, and actions did not have matching identifiers");
        var profileRows = profileOrder.Select(index => new Dictionary<string, object?>
        {
            ["index"] = index + 1,
            ["profile_index"] = index,
            ["profile_digest"] = profileDigest!,
            ["name"] = profileNames[index].Name,
            ["protocol"] = profileProtocols[index].Protocol,
            ["action"] = profileActions[index].Action,
            ["description_automation_id"] = profileNames[index].AutomationId,
            ["protocol_automation_id"] = profileProtocols[index].AutomationId,
            ["action_automation_id"] = profileActions[index].AutomationId,
        }).ToArray();

        return new Dictionary<string, object?>
        {
            ["ready"] = true,
            ["window"] = RectJson(GetPhysicalWindowRect(window)),
            ["controls"] = RectJson(PhysicalBounds(controls, "Connection controls")),
            ["profile_viewport"] = RectJson(viewportBounds),
            ["connection_action"] = RectJson(PhysicalBounds(connectionAction, "VPN connection action")),
            ["logs"] = RectJson(PhysicalBounds(logs, "Backend logs")),
            ["scroll_position"] = scrollPosition,
            ["profile_digest"] = profileDigest ?? "",
            ["profile_rows"] = profileRows,
            ["visible_profile_actions"] = visibleActions
                .OrderBy(action => action.Index)
                .Select(action => action.AutomationId)
                .ToArray(),
        };
    }

    private static void TracePhase(string name)
    {
        Console.Error.WriteLine($"native-ui-phase={name}");
        Console.Error.Flush();
    }

    private static string UtcTimestamp() =>
        DateTimeOffset.UtcNow.ToString("O", CultureInfo.InvariantCulture);

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

    private static Dictionary<string, object?> DescribeProbeCandidate(
        Process process, int processId, string? executablePath, Exception? pathError)
    {
        var candidate = new Dictionary<string, object?>
        {
            ["pid"] = processId,
            ["path"] = executablePath,
        };
        if (pathError is not null) candidate["path_error"] = pathError.ToString();
        try
        {
            candidate["identity"] = process.StartTime.ToUniversalTime().Ticks.ToString(CultureInfo.InvariantCulture);
        }
        catch (Exception error) { candidate["identity_error"] = error.ToString(); }
        try
        {
            var mainWindow = process.MainWindowHandle;
            candidate["main_window_handle"] = mainWindow == IntPtr.Zero
                ? null : $"0x{mainWindow.ToInt64():X}";
        }
        catch (Exception error) { candidate["main_window_error"] = error.ToString(); }
        try { candidate["top_level_windows"] = DescribeProcessWindows(process); }
        catch (Exception error) { candidate["top_level_windows_error"] = error.ToString(); }
        return candidate;
    }

    private static Process FindSettledProbeProcess(string expected)
    {
        Process? selected = null;
        var latestInventory = "[]";
        var previousInventory = (string?)null;

        bool FindUniqueCandidate()
        {
            selected = null;
            var exactMatches = new List<Process>();
            var unknownPaths = new List<Process>();
            var inventory = new List<Dictionary<string, object?>>();
            var processes = Process.GetProcessesByName(Path.GetFileNameWithoutExtension(expected));
            try
            {
                foreach (var candidate in processes)
                {
                    var processId = candidate.Id;
                    string? candidatePath = null;
                    Exception? pathError = null;
                    try
                    {
                        candidatePath = candidate.MainModule?.FileName;
                        if (candidatePath is null)
                            pathError = new InvalidOperationException("Process.MainModule.FileName returned null");
                    }
                    catch (Exception error)
                    {
                        pathError = error;
                    }

                    var exactPathMatch = pathError is null && string.Equals(
                        candidatePath, expected, StringComparison.OrdinalIgnoreCase);
                    var description = DescribeProbeCandidate(candidate, processId, candidatePath, pathError);
                    description["exact_path_match"] = exactPathMatch;
                    description["blocks_unique_probe"] = exactPathMatch || pathError is not null;
                    inventory.Add(description);

                    if (exactPathMatch)
                        exactMatches.Add(candidate);
                    else if (pathError is not null)
                        unknownPaths.Add(candidate);
                }

                latestInventory = JsonSerializer.Serialize(
                    inventory.OrderBy(candidate => (int)candidate["pid"]!));
                if (latestInventory != previousInventory)
                {
                    TracePhase(
                        $"probe-process-settle-inventory utc={UtcTimestamp()} settle_redirect_workers=true inventory={latestInventory}");
                    previousInventory = latestInventory;
                }

                if (exactMatches.Count != 1 || unknownPaths.Count != 0)
                    return false;

                selected = exactMatches[0];
                return true;
            }
            finally
            {
                foreach (var candidate in processes)
                    if (!ReferenceEquals(candidate, selected)) candidate.Dispose();
            }
        }

        try
        {
            WaitFor(
                FindUniqueCandidate,
                "Windows warm activation did not settle to one exact-path UI process within five seconds",
                seconds: 5);
        }
        catch (TimeoutException error)
        {
            TracePhase(
                $"probe-process-settle-timeout utc={UtcTimestamp()} settle_redirect_workers=true inventory={latestInventory} error={JsonSerializer.Serialize(error.ToString())}");
            throw new TimeoutException(
                "Windows warm activation did not settle to one exact-path UI process within five seconds; " +
                $"latest candidate inventory={latestInventory}; wait exception={error}",
                error);
        }

        return selected ?? throw new InvalidOperationException(
            "Windows warm activation probe completed without selecting a UI process");
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
            process.Refresh();
            foreach (ProcessThread thread in process.Threads)
                EnumThreadWindows(unchecked((uint)thread.Id), callback, IntPtr.Zero);
        }
        finally
        {
            GC.KeepAlive(callback);
        }
        return windows.ToArray();
    }

    private static Process? FindSettingsProcessForSession(int sessionId)
    {
        Process? selected = null;
        foreach (var process in Process.GetProcessesByName("SystemSettings"))
        {
            bool belongsToSession;
            try
            {
                process.Refresh();
                belongsToSession = !process.HasExited && process.SessionId == sessionId;
            }
            catch (InvalidOperationException)
            {
                process.Dispose();
                continue;
            }
            if (!belongsToSession) { process.Dispose(); continue; }
            if (selected is not null)
            {
                process.Dispose();
                selected.Dispose();
                throw new InvalidOperationException(
                    $"Ambiguous SystemSettings processes in helper session {sessionId}");
            }
            selected = process;
        }
        return selected;
    }

    private static long ProcessStartTimeUtcTicks(Process process)
    {
        process.Refresh();
        if (process.HasExited) throw new InvalidOperationException("Settings process exited while reading its identity");
        return process.StartTime.ToUniversalTime().Ticks;
    }

    private static string GetWindowClassName(IntPtr window)
    {
        if (!IsWindow(window)) throw new InvalidOperationException("Could not inspect a vanished window class");
        var className = new StringBuilder(256);
        Marshal.SetLastPInvokeError(0);
        var length = GetClassName(window, className, className.Capacity);
        if (length == 0)
            throw new System.ComponentModel.Win32Exception(
                Marshal.GetLastWin32Error(), "Could not read a Settings window class name");
        return className.ToString();
    }

    private static bool IsMainRootWindow(IntPtr window)
    {
        return IsWindow(window) && GetWindow(window, GwOwner) == IntPtr.Zero &&
            GetAncestor(window, GaRoot) == window;
    }

    private static bool IsApplicationFrameWindowClass(string className) =>
        string.Equals(className, "ApplicationFrameWindow", StringComparison.Ordinal);

    private static HashSet<IntPtr> CaptureSettingsWindowBaseline(int sessionId, Process? settings)
    {
        var windows = new HashSet<IntPtr>();
        if (settings is not null)
        {
            settings.Refresh();
            if (!settings.HasExited && settings.SessionId == sessionId)
            {
                foreach (var window in EnumerateProcessWindows(settings))
                    if (IsMainRootWindow(window)) windows.Add(window);
            }
        }

        foreach (var host in Process.GetProcessesByName("ApplicationFrameHost"))
        {
            using (host)
            {
                int hostSession;
                try
                {
                    host.Refresh();
                    if (host.HasExited) continue;
                    hostSession = host.SessionId;
                }
                catch (InvalidOperationException) { continue; }
                if (hostSession == sessionId)
                {
                    foreach (var window in EnumerateProcessWindows(host))
                        if (IsMainRootWindow(window)) windows.Add(window);
                }
            }
        }
        return windows;
    }

    private static bool HasSettingsOwnedDescendant(IntPtr frameWindow, int settingsProcessId)
    {
        var found = false;
        EnumChildWindowsCallback callback = (child, _) =>
        {
            GetWindowThreadProcessId(child, out var processId);
            if (processId == settingsProcessId) found = true;
            return true;
        };
        _ = EnumChildWindows(frameWindow, callback, IntPtr.Zero);
        GC.KeepAlive(callback);
        return found;
    }

    private static bool IsSettingsAssociatedFrameWindow(IntPtr frameWindow, Process settings)
    {
        if (HasSettingsOwnedDescendant(frameWindow, settings.Id)) return true;
        foreach (var settingsWindow in EnumerateProcessWindows(settings))
        {
            GetWindowThreadProcessId(settingsWindow, out var processId);
            if (processId == settings.Id && GetWindow(settingsWindow, GwOwner) != IntPtr.Zero &&
                GetAncestor(settingsWindow, GaRootOwner) == frameWindow)
                return true;
        }
        return false;
    }

    private static SettingsWindowCandidate DescribeSettingsWindowCandidate(
        IntPtr window, Process owner, int sessionId, bool isApplicationFrameHost)
    {
        owner.Refresh();
        if (owner.HasExited || owner.SessionId != sessionId)
            throw new InvalidOperationException("Settings window owner exited or changed session during inspection");
        GetWindowThreadProcessId(window, out var actualProcessId);
        if (!IsWindow(window) || actualProcessId != owner.Id)
            throw new InvalidOperationException("Settings window ownership changed during inspection");
        return new SettingsWindowCandidate(
            window,
            owner.Id,
            owner.ProcessName,
            owner.SessionId,
            owner.StartTime.ToUniversalTime().Ticks,
            GetWindowClassName(window),
            isApplicationFrameHost);
    }

    private static List<SettingsWindowCandidate> FindSettingsWindowCandidates(
        Process settings, int sessionId)
    {
        settings.Refresh();
        if (settings.HasExited || settings.ProcessName != "SystemSettings" || settings.SessionId != sessionId)
            throw new InvalidOperationException("Selected SystemSettings process identity changed");

        var candidates = new List<SettingsWindowCandidate>();
        foreach (var window in EnumerateProcessWindows(settings))
        {
            if (!IsMainRootWindow(window) || !IsWindowVisible(window) || IsIconic(window))
                continue;
            GetWindowThreadProcessId(window, out var ownerPid);
            if (ownerPid == settings.Id)
                candidates.Add(DescribeSettingsWindowCandidate(window, settings, sessionId, isApplicationFrameHost: false));
        }

        foreach (var host in Process.GetProcessesByName("ApplicationFrameHost"))
        {
            using (host)
            {
                int hostSession;
                try
                {
                    host.Refresh();
                    if (host.HasExited) continue;
                    hostSession = host.SessionId;
                }
                catch (InvalidOperationException) { continue; }
                if (hostSession != sessionId) continue;
                foreach (var window in EnumerateProcessWindows(host))
                {
                    if (!IsMainRootWindow(window) || !IsWindowVisible(window) || IsIconic(window))
                        continue;
                    var className = GetWindowClassName(window);
                    if (!IsApplicationFrameWindowClass(className) ||
                        !IsSettingsAssociatedFrameWindow(window, settings))
                        continue;
                    candidates.Add(DescribeSettingsWindowCandidate(
                        window, host, sessionId, isApplicationFrameHost: true));
                }
            }
        }
        return candidates;
    }

    private static object DescribeSettingsProcess(Process process)
    {
        process.Refresh();
        return new
        {
            processId = process.Id,
            processName = process.ProcessName,
            sessionId = process.SessionId,
            startTimeUtcTicks = ProcessStartTimeUtcTicks(process).ToString(CultureInfo.InvariantCulture),
            windows = DescribeProcessWindows(process),
        };
    }

    private static object DescribeSettingsWindow(SettingsWindowCandidate candidate, bool existedBefore)
    {
        return new
        {
            hwnd = $"0x{candidate.Window.ToInt64():X}",
            existedBefore,
            description = DescribeWindow(candidate.Window),
            owner = new
            {
                processId = candidate.OwnerProcessId,
                processName = candidate.OwnerProcessName,
                sessionId = candidate.OwnerSessionId,
                startTimeUtcTicks = candidate.OwnerStartTimeUtcTicks.ToString(CultureInfo.InvariantCulture),
            },
            isApplicationFrameHost = candidate.IsApplicationFrameHost,
        };
    }

    private static bool SameSettingsWindowOwner(
        SettingsWindowCandidate expected, SettingsWindowCandidate actual)
    {
        return expected.Window == actual.Window &&
            expected.OwnerProcessId == actual.OwnerProcessId &&
            string.Equals(expected.OwnerProcessName, actual.OwnerProcessName, StringComparison.Ordinal) &&
            expected.OwnerSessionId == actual.OwnerSessionId &&
            expected.OwnerStartTimeUtcTicks == actual.OwnerStartTimeUtcTicks &&
            string.Equals(expected.WindowClassName, actual.WindowClassName, StringComparison.Ordinal) &&
            expected.IsApplicationFrameHost == actual.IsApplicationFrameHost;
    }

    private static bool ReverifySettingsWindowAssociation(
        SettingsWindowCandidate expectedWindow,
        int settingsProcessId,
        long settingsProcessStartTimeUtcTicks,
        int sessionId)
    {
        using var currentSettings = FindSettingsProcessForSession(sessionId);
        if (currentSettings is null || currentSettings.Id != settingsProcessId ||
            ProcessStartTimeUtcTicks(currentSettings) != settingsProcessStartTimeUtcTicks)
            return false;
        if (!IsMainRootWindow(expectedWindow.Window) ||
            !string.Equals(GetWindowClassName(expectedWindow.Window), expectedWindow.WindowClassName,
                StringComparison.Ordinal))
            return false;

        GetWindowThreadProcessId(expectedWindow.Window, out var ownerProcessId);
        if (ownerProcessId != expectedWindow.OwnerProcessId) return false;
        if (!expectedWindow.IsApplicationFrameHost)
        {
            var current = DescribeSettingsWindowCandidate(
                expectedWindow.Window, currentSettings, sessionId, isApplicationFrameHost: false);
            return SameSettingsWindowOwner(expectedWindow, current);
        }

        using var currentHost = Process.GetProcessById(expectedWindow.OwnerProcessId);
        currentHost.Refresh();
        if (currentHost.HasExited || currentHost.ProcessName != "ApplicationFrameHost" ||
            currentHost.SessionId != sessionId ||
            ProcessStartTimeUtcTicks(currentHost) != expectedWindow.OwnerStartTimeUtcTicks ||
            !IsApplicationFrameWindowClass(expectedWindow.WindowClassName) ||
            !IsSettingsAssociatedFrameWindow(expectedWindow.Window, currentSettings))
            return false;
        var currentFrame = DescribeSettingsWindowCandidate(
            expectedWindow.Window, currentHost, sessionId, isApplicationFrameHost: true);
        return SameSettingsWindowOwner(expectedWindow, currentFrame);
    }

    private static void VerifySettingsWindowOwner(
        IntPtr window, SettingsWindowCandidate expectedWindow)
    {
        if (!IsWindow(window))
            throw new InvalidOperationException("Refusing to close a Settings window that no longer exists");
        GetWindowThreadProcessId(window, out var actualProcessId);
        if (actualProcessId != expectedWindow.OwnerProcessId)
            throw new InvalidOperationException("Refusing to close a Settings window whose owner PID changed");
        using var owner = Process.GetProcessById(expectedWindow.OwnerProcessId);
        owner.Refresh();
        if (owner.HasExited || owner.ProcessName != expectedWindow.OwnerProcessName ||
            owner.SessionId != expectedWindow.OwnerSessionId ||
            ProcessStartTimeUtcTicks(owner) != expectedWindow.OwnerStartTimeUtcTicks)
            throw new InvalidOperationException("Refusing to close a Settings window whose owner identity changed");
        if (!string.Equals(GetWindowClassName(window), expectedWindow.WindowClassName, StringComparison.Ordinal))
            throw new InvalidOperationException("Refusing to close a Settings window whose HWND class changed");
    }

    private static void ValidateSettingsTextSizeRequest(JsonElement request)
    {
        var unexpected = request.EnumerateObject().Select(property => property.Name)
            .Where(name => name is not ("operation" or "executable" or "action" or "uri" or "target" or "expectedCurrent")).ToArray();
        if (unexpected.Length > 0) throw new ArgumentException("Unexpected Settings inspection fields: " + string.Join(", ", unexpected));
        if (string.IsNullOrWhiteSpace(request.GetProperty("executable").GetString()))
            throw new ArgumentException("Settings Text size operation requires the helper executable field");
        if (request.GetProperty("uri").GetString() != WindowsTextSizeSettingsUri)
            throw new ArgumentException("Settings helper supports only the fixed Text size URI");

        var action = request.GetProperty("action").GetString();
        if (action == "inspect")
        {
            if (request.TryGetProperty("target", out _) || request.TryGetProperty("expectedCurrent", out _))
                throw new ArgumentException("Settings inspection does not accept mutation fields");
            return;
        }
        if (action != "apply") throw new ArgumentException("Settings Text size action must be inspect or apply");
        if (!request.TryGetProperty("target", out var target) || target.ValueKind != JsonValueKind.Number ||
            !target.TryGetInt32(out var targetValue) || targetValue <= 0 ||
            !request.TryGetProperty("expectedCurrent", out var expected) || expected.ValueKind != JsonValueKind.Number ||
            !expected.TryGetInt32(out var expectedValue) || expectedValue <= 0)
            throw new ArgumentException("Settings apply requires positive integer target and expectedCurrent values");
    }

    private static Dictionary<string, object?> CaptureSettingsWindowAfterClose(
        IntPtr window,
        SettingsWindowCandidate expectedWindow,
        int expectedSettingsProcessId,
        long expectedSettingsProcessStartTicks,
        int expectedSessionId,
        bool closeMessagePosted)
    {
        var snapshot = new Dictionary<string, object?>
        {
            ["capturedAtUtc"] = UtcTimestamp(),
            ["windowHandle"] = $"0x{window.ToInt64():X}",
            ["closeMessagePosted"] = closeMessagePosted,
            ["expectedWindowOwner"] = new
            {
                processId = expectedWindow.OwnerProcessId,
                processName = expectedWindow.OwnerProcessName,
                sessionId = expectedWindow.OwnerSessionId,
                startTimeUtcTicks = expectedWindow.OwnerStartTimeUtcTicks.ToString(CultureInfo.InvariantCulture),
            },
            ["expectedSettingsProcess"] = new
            {
                processId = expectedSettingsProcessId,
                startTimeUtcTicks = expectedSettingsProcessStartTicks.ToString(CultureInfo.InvariantCulture),
                sessionId = expectedSessionId,
            },
        };
        var isWindow = IsWindow(window);
        var windowContext = DescribeWindowContext(window, includeGeometry: false);
        windowContext["isWindow"] = isWindow;
        windowContext["isWindowVisible"] = isWindow && IsWindowVisible(window);
        if (isWindow)
        {
            GetWindowThreadProcessId(window, out var actualProcessId);
            if (actualProcessId != 0)
            {
                using var owner = Process.GetProcessById(checked((int)actualProcessId));
                owner.Refresh();
                windowContext["ownerProcessName"] = owner.ProcessName;
                windowContext["ownerSessionId"] = owner.SessionId;
                windowContext["ownerStartTimeUtcTicks"] = ProcessStartTimeUtcTicks(owner)
                    .ToString(CultureInfo.InvariantCulture);
            }
        }
        snapshot["window"] = windowContext;
        var rootOwner = isWindow ? GetAncestor(window, GaRootOwner) : IntPtr.Zero;
        var rootOwnerContext = DescribeWindowContext(
            rootOwner, includeGeometry: false);
        rootOwnerContext["isWindow"] = IsWindow(rootOwner);
        rootOwnerContext["isWindowVisible"] = IsWindow(rootOwner) && IsWindowVisible(rootOwner);
        snapshot["rootOwner"] = rootOwnerContext;
        return snapshot;
    }

    private static AutomationElement? FindSettingsTextSizeControl(
        AutomationElement root,
        string automationId,
        ControlType controlType,
        string name)
    {
        var element = root.FindFirst(TreeScope.Descendants,
            new PropertyCondition(AutomationElement.AutomationIdProperty, automationId));
        if (element is null) return null;
        var current = element.Current;
        if (current.ControlType != controlType || !string.Equals(current.Name, name, StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException(
                $"Windows Settings control ID changed identity: expected={automationId}/{controlType.ProgrammaticName}/{name} " +
                $"actual={current.AutomationId}/{current.ControlType.ProgrammaticName}/{current.Name}");
        return element;
    }

    private static void ApplyWindowsTextSize(
        AutomationElement root,
        AutomationElement slider,
        AutomationElement apply,
        int target,
        int expectedCurrent,
        Dictionary<string, object?> response,
        LogsCaseDeadline? caseDeadline = null)
    {
        if (apply.Current.AutomationId != WindowsTextSizeApplyAutomationId)
            throw new InvalidOperationException("Refusing to apply Text size through an unverified Settings control");
        if (!slider.TryGetCurrentPattern(RangeValuePattern.Pattern, out var sliderPattern))
            throw new InvalidOperationException("Text size Slider no longer exposes RangeValuePattern");
        var range = (RangeValuePattern)sliderPattern;
        var before = range.Current;
        response["rangeBeforeApply"] = new
        {
            value = before.Value, minimum = before.Minimum, maximum = before.Maximum,
            smallChange = before.SmallChange, largeChange = before.LargeChange, isReadOnly = before.IsReadOnly,
        };
        if (before.IsReadOnly) throw new InvalidOperationException("Text size Slider RangeValuePattern is read-only");
        if (Math.Abs(before.Value - expectedCurrent) > 0.01)
            throw new InvalidOperationException(
                $"Text size changed from expected {expectedCurrent}% to {before.Value.ToString(CultureInfo.InvariantCulture)}% before apply");
        if (target < before.Minimum || target > before.Maximum)
            throw new InvalidOperationException(
                $"Text size target {target}% is outside the writable RangeValue bounds {before.Minimum}..{before.Maximum}");
        if (!slider.Current.IsEnabled || slider.Current.IsOffscreen || !HasUsableBounds(slider.Current.BoundingRectangle))
            throw new InvalidOperationException("Text size Slider is not enabled and visible");

        response["setValueAttempted"] = true;
        range.SetValue(target);
        var setValueReadback = range.Current.Value;
        response["setValueReadback"] = setValueReadback;
        if (Math.Abs(setValueReadback - target) > 0.01)
            throw new InvalidOperationException(
                $"Text size Slider read back {setValueReadback.ToString(CultureInfo.InvariantCulture)}% after setting {target}%");

        WaitTextSizeSettings(() => FindSettingsTextSizeControl(
            root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply")?.Current.IsEnabled == true,
            "Settings Apply did not enable after changing Text size", caseDeadline, seconds: 5);
        apply = FindSettingsTextSizeControl(
            root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply")
            ?? throw new InvalidOperationException("Exact Text size Apply button disappeared after changing the slider");
        if (!apply.Current.IsEnabled || apply.Current.IsOffscreen || !HasUsableBounds(apply.Current.BoundingRectangle))
            throw new InvalidOperationException("Text size Apply button is not enabled and visible after changing the slider");
        if (!apply.TryGetCurrentPattern(InvokePattern.Pattern, out var invokePattern))
            throw new InvalidOperationException("Text size Apply button does not expose InvokePattern");
        response["applyInvokeAttempted"] = true;
        ((InvokePattern)invokePattern).Invoke();
        response["applyInvoked"] = true;

        WaitTextSizeSettings(() =>
        {
            var currentSlider = FindSettingsTextSizeControl(
                root, WindowsTextSizeSliderAutomationId, ControlType.Slider, "Text size");
            var currentApply = FindSettingsTextSizeControl(
                root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply");
            if (currentSlider is null || currentApply is null ||
                !currentSlider.TryGetCurrentPattern(RangeValuePattern.Pattern, out var currentPattern))
                return false;

            var rangeAfterApply = ((RangeValuePattern)currentPattern).Current;
            var sliderAfterApply = currentSlider.Current;
            var applyAfterApply = currentApply.Current;
            var sliderBounds = sliderAfterApply.BoundingRectangle;
            var applyBounds = applyAfterApply.BoundingRectangle;
            if (Math.Abs(rangeAfterApply.Value - target) > 0.01 || rangeAfterApply.IsReadOnly ||
                applyAfterApply.IsEnabled || !sliderAfterApply.IsEnabled || sliderAfterApply.IsOffscreen ||
                !HasUsableBounds(sliderBounds) || applyAfterApply.IsOffscreen || !HasUsableBounds(applyBounds))
                return false;

            var sliderIdentity = DescribeElement(currentSlider);
            var applyIdentity = DescribeElement(currentApply);
            var sliderObservation = new
            {
                identity = sliderIdentity, enabled = sliderAfterApply.IsEnabled, offscreen = sliderAfterApply.IsOffscreen,
                bounds = RectJson(sliderBounds),
                range = new
                {
                    value = rangeAfterApply.Value, minimum = rangeAfterApply.Minimum, maximum = rangeAfterApply.Maximum,
                    smallChange = rangeAfterApply.SmallChange, largeChange = rangeAfterApply.LargeChange,
                    isReadOnly = rangeAfterApply.IsReadOnly,
                },
                rangeError = (string?)null,
            };
            var applyObservation = new
            {
                identity = applyIdentity, enabled = applyAfterApply.IsEnabled, offscreen = applyAfterApply.IsOffscreen,
                bounds = RectJson(applyBounds),
            };
            response["valueAfterApply"] = rangeAfterApply.Value;
            response["applyEnabledAfterApply"] = applyAfterApply.IsEnabled;
            response["slider"] = sliderObservation;
            response["apply"] = applyObservation;
            return true;
        }, "Text size Apply did not settle at the requested value", caseDeadline, seconds: 10);
    }

    private static Dictionary<string, object?> ReadWindowsTextSizeSettings(
        JsonElement request, LogsCaseDeadline? caseDeadline = null,
        LogsCaseDeadline? cleanupDeadline = null)
    {
        var action = request.GetProperty("action").GetString()!;
        var target = action == "apply" ? request.GetProperty("target").GetInt32() : (int?)null;
        var expectedCurrent = action == "apply" ? request.GetProperty("expectedCurrent").GetInt32() : (int?)null;
        using var helper = Process.GetCurrentProcess();
        var sessionId = helper.SessionId;
        var response = new Dictionary<string, object?>
        {
            ["schema"] = "dobbyvpn.windows-text-size-settings/v1", ["operation"] = "settings-text-size",
            ["action"] = action, ["uri"] = WindowsTextSizeSettingsUri,
            ["helper"] = new { processId = helper.Id, sessionId, threadId = GetCurrentThreadId(), userName = Environment.UserName },
            ["ready"] = false, ["available"] = false, ["newSettingsWindowClosed"] = null,
        };
        if (target is not null)
        {
            response["target"] = target.Value;
            response["expectedCurrent"] = expectedCurrent!.Value;
            response["setValueAttempted"] = false;
            response["applyInvokeAttempted"] = false;
        }
        Process? settings = null;
        Process? activation = null;
        var activationAttempted = false;
        var windowsBefore = new HashSet<IntPtr>();
        IntPtr window = IntPtr.Zero;
        int settingsProcessId = 0;
        long settingsProcessStartTimeUtcTicks = 0;
        SettingsWindowCandidate? selectedWindow = null;
        string? primaryError = null;
        var cleanupErrors = new List<string>();
        try
        {
            settings = FindSettingsProcessForSession(sessionId);
            windowsBefore = CaptureSettingsWindowBaseline(sessionId, settings);
            response["settingsBefore"] = settings is null ? null : DescribeSettingsProcess(settings);
            response["baselineMainWindowsBefore"] = windowsBefore
                .OrderBy(candidate => candidate.ToInt64())
                .Select(candidate => DescribeWindow(candidate))
                .ToArray();
            activationAttempted = true;
            activation = Process.Start(new ProcessStartInfo(WindowsTextSizeSettingsUri) { UseShellExecute = true });
            WaitTextSizeSettings(() =>
            {
                var refreshedSettings = FindSettingsProcessForSession(sessionId);
                settings?.Dispose();
                settings = refreshedSettings;
                if (settings is null) return false;
                settingsProcessId = settings.Id;
                settingsProcessStartTimeUtcTicks = ProcessStartTimeUtcTicks(settings);
                var candidates = FindSettingsWindowCandidates(settings, sessionId);
                if (candidates.Count > 1)
                    throw new InvalidOperationException(
                        $"Ambiguous visible Settings main windows in session {sessionId}: " +
                        string.Join(", ", candidates.Select(candidate => $"0x{candidate.Window.ToInt64():X}")));
                if (candidates.Count == 1)
                {
                    selectedWindow = candidates[0];
                    window = candidates[0].Window;
                    return true;
                }
                return false;
            }, "Settings did not expose one visible window in the helper session", caseDeadline, seconds: 10);
            if (settings is null || selectedWindow is null || window == IntPtr.Zero)
                throw new InvalidOperationException("Settings window unavailable after URI activation");
            var candidateWindow = selectedWindow.Value;
            var existedBefore = windowsBefore.Contains(window);
            response["settingsAfter"] = DescribeSettingsProcess(settings);
            response["window"] = DescribeSettingsWindow(candidateWindow, existedBefore);
            response["settingsAssociation"] = candidateWindow.IsApplicationFrameHost
                ? "same-session ApplicationFrameHost with SystemSettings-owned descendant or validated RootOwner"
                : "visible unowned SystemSettings root window";
            if (!existedBefore)
                TracePhase(
                    $"settings-text-size-new-window-owned session={sessionId} core_pid={settingsProcessId} " +
                    $"owner_pid={candidateWindow.OwnerProcessId} hwnd=0x{window.ToInt64():X}");

            var root = AutomationElement.FromHandle(window);
            var slider = FindSettingsTextSizeControl(
                root, WindowsTextSizeSliderAutomationId, ControlType.Slider, "Text size");
            if (slider is null) throw new InvalidOperationException("Exact Text size Slider was not found in the Settings window");
            var current = slider.Current;
            object? range = null;
            string? rangeError = null;
            try
            {
                if (!slider.TryGetCurrentPattern(RangeValuePattern.Pattern, out var pattern))
                    rangeError = "RangeValuePattern unavailable";
                else
                {
                    var value = ((RangeValuePattern)pattern).Current;
                    range = new { value = value.Value, minimum = value.Minimum, maximum = value.Maximum,
                        smallChange = value.SmallChange, largeChange = value.LargeChange, isReadOnly = value.IsReadOnly };
                }
            }
            catch (Exception error) { rangeError = error.ToString(); }
            var apply = FindSettingsTextSizeControl(
                root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply");
            var bounds = current.BoundingRectangle;
            response["slider"] = new { identity = DescribeElement(slider), enabled = current.IsEnabled,
                offscreen = current.IsOffscreen, bounds = HasUsableBounds(bounds) ? RectJson(bounds) : null, range, rangeError };
            response["apply"] = apply is null ? null : new { identity = DescribeElement(apply), enabled = apply.Current.IsEnabled,
                offscreen = apply.Current.IsOffscreen, bounds = HasUsableBounds(apply.Current.BoundingRectangle) ? RectJson(apply.Current.BoundingRectangle) : null };
            var ready = !current.IsOffscreen && HasUsableBounds(bounds) && range is not null && apply is not null;
            if (action == "apply")
            {
                if (range is null || apply is null)
                    throw new InvalidOperationException("Text size apply requires RangeValue data and the exact Apply control");
                ApplyWindowsTextSize(root, slider, apply, target!.Value, expectedCurrent!.Value, response, caseDeadline);
                ready = true;
            }
            response["ready"] = ready;
            response["available"] = ready;
            if (!ready) response["reason"] = "Text size slider, RangeValue data, or Apply control was unavailable or not visible";
            if (action == "apply" && response.GetValueOrDefault("applyInvoked") is not true)
                throw new InvalidOperationException("Text size Apply did not complete its guarded mutation");
        }
        catch (Exception error)
        {
            primaryError = error.ToString();
            response["ready"] = false;
            response["available"] = false;
        }
        finally
        {
            if (window != IntPtr.Zero && selectedWindow is not null && !windowsBefore.Contains(window))
            {
                var closeMessagePosted = false;
                try
                {
                    var candidateWindow = selectedWindow.Value;
                    GetWindowThreadProcessId(window, out var actualPid);
                    if (!IsWindow(window) || actualPid == 0) response["newSettingsWindowClosed"] = true;
                    else
                    {
                        VerifySettingsWindowOwner(window, candidateWindow);
                        if (!ReverifySettingsWindowAssociation(
                                candidateWindow, settingsProcessId, settingsProcessStartTimeUtcTicks, sessionId))
                            throw new InvalidOperationException(
                                "Refusing to close a Settings window whose SystemSettings association changed");
                        VerifySettingsWindowOwner(window, candidateWindow);
                        if (!PostMessage(window, WmClose, IntPtr.Zero, IntPtr.Zero))
                            throw new System.ComponentModel.Win32Exception(
                                Marshal.GetLastWin32Error(), "Could not close new Settings window");
                        closeMessagePosted = true;
                        WaitTextSizeSettings(() =>
                        {
                            if (!IsWindow(window)) return true;
                            GetWindowThreadProcessId(window, out var pid);
                            return pid == 0 || pid != candidateWindow.OwnerProcessId;
                        }, "New Settings window did not close", cleanupDeadline, seconds: 5);
                        response["newSettingsWindowClosed"] = true;
                    }
                }
                catch (Exception error) { cleanupErrors.Add(error.ToString()); }
                finally
                {
                    try
                    {
                        response["postCloseWindowSnapshot"] = CaptureSettingsWindowAfterClose(
                            window, selectedWindow.Value, settingsProcessId,
                            settingsProcessStartTimeUtcTicks, sessionId, closeMessagePosted);
                    }
                    catch (Exception error)
                    {
                        cleanupErrors.Add("Settings post-close HWND snapshot: " + error);
                    }
                }
            }
            else if (activationAttempted && window == IntPtr.Zero)
            {
                response["newSettingsWindowClosed"] = false;
                cleanupErrors.Add(
                    "Settings activation exposed no associated main window; cleanup of a potentially new Settings window cannot be verified");
            }
            foreach (var process in new[] { settings, activation })
                try { process?.Dispose(); } catch (Exception error) { cleanupErrors.Add(error.ToString()); }
        }
        if (primaryError is not null) response["error"] = primaryError;
        if (cleanupErrors.Count > 0)
        {
            response["cleanupErrors"] = cleanupErrors;
            response["ready"] = false;
            response["available"] = false;
        }
        response["finishedAtUtc"] = UtcTimestamp();
        return response;
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

    private static NativeInput CreateMouseInput(uint flags, uint mouseData = 0) => new()
    {
        Type = InputMouse,
        Union = new NativeInputUnion
        {
            Mouse = new NativeMouseInput { MouseData = mouseData, Flags = flags, ExtraInfo = UIntPtr.Zero },
        },
    };

    private static void RequireForeground(IntPtr window, string operation)
    {
        var targetUsable = window != IntPtr.Zero && IsWindow(window) && IsWindowVisible(window) && !IsIconic(window);
        var foregroundBeforeHandle = GetForegroundWindow();
        if (targetUsable && foregroundBeforeHandle == window)
            return;

        var measurement = new Dictionary<string, object?>
        {
            ["operation"] = operation,
            ["target"] = DescribeWindowContext(window, includeGeometry: true),
            ["foregroundBefore"] = DescribeWindowContext(
                foregroundBeforeHandle, includeGeometry: true),
        };

        try
        {
            if (!targetUsable)
                throw new InvalidOperationException(
                    "Candidate UI window is not visible and usable for foreground activation");

            var foregroundRequested = SetForegroundWindow(window);
            var foregroundAfterRequestHandle = GetForegroundWindow();
            measurement["setForegroundWindowResult"] = foregroundRequested;
            measurement["foregroundAfterRequest"] = DescribeWindowContext(
                foregroundAfterRequestHandle, includeGeometry: true);

            if (foregroundAfterRequestHandle != window)
            {
                var captionClick = new Dictionary<string, object?>();
                measurement["captionClick"] = captionClick;
                var bounds = GetPhysicalWindowRect(window);
                var clientOrigin = GetPhysicalClientOrigin(window);
                var captionHeight = clientOrigin.Y - bounds.Top;
                if (bounds.Width <= 0 || bounds.Height <= 0 || captionHeight <= 0 || captionHeight >= bounds.Height)
                    throw new InvalidOperationException("Native window has no measurable nonclient caption band");
                var point = new NativePoint { X = checked((int)Math.Round(bounds.Left + bounds.Width / 2, MidpointRounding.AwayFromZero)),
                    Y = checked((int)Math.Round(bounds.Top + captionHeight / 2.0, MidpointRounding.AwayFromZero)) };
                captionClick["screenPoint"] = new { x = point.X, y = point.Y };

                if (point.X < short.MinValue || point.X > short.MaxValue || point.Y < short.MinValue || point.Y > short.MaxValue)
                    throw new InvalidOperationException($"Caption point is outside WM_NCHITTEST range: ({point.X},{point.Y})");
                Marshal.SetLastPInvokeError(0);
                if (!SetCursorPos(point.X, point.Y))
                    throw new System.ComponentModel.Win32Exception(
                        Marshal.GetLastPInvokeError(), "Could not move the pointer to the candidate caption");
                Marshal.SetLastPInvokeError(0);
                var cursorRead = GetCursorPos(out var cursor);
                var cursorError = Marshal.GetLastPInvokeError();
                if (!cursorRead || cursor.X != point.X || cursor.Y != point.Y)
                    throw new InvalidOperationException($"Cursor did not reach caption point: expected=({point.X},{point.Y}) " +
                        $"actual=({cursor.X},{cursor.Y}) lastError={cursorError}");

                var packedPoint = new IntPtr(unchecked((int)((uint)(ushort)cursor.X | ((uint)(ushort)cursor.Y << 16))));
                Marshal.SetLastPInvokeError(0);
                var hitTestDelivered = SendMessageTimeout(window, WmNcHitTest, IntPtr.Zero, packedPoint,
                    SmtoAbortIfHung | SmtoErrorOnExit, 1000, out var hitTestResult) != IntPtr.Zero;
                var hitTestError = Marshal.GetLastPInvokeError();
                var hitTestCode = unchecked((int)hitTestResult.ToUInt64());
                captionClick["wmNcHitTestResult"] = hitTestCode;
                if (!hitTestDelivered || hitTestCode != HtCaption)
                    throw new InvalidOperationException($"Refusing caption click: WM_NCHITTEST result={hitTestCode}, " +
                        $"lastError={hitTestError}, expected HTCAPTION={HtCaption}");

                var pointRoot = GetAncestor(WindowFromPoint(cursor), GaRoot);
                captionClick["windowFromPointRoot"] = $"0x{pointRoot.ToInt64():X}";
                if (pointRoot != window) throw new InvalidOperationException(
                    $"Refusing caption click: WindowFromPoint root 0x{pointRoot.ToInt64():X} " +
                    $"does not match candidate 0x{window.ToInt64():X}");

                var inputSize = Marshal.SizeOf<NativeInput>();
                captionClick["sendInputStructSize"] = inputSize;
                var clickInputs = new[] { CreateMouseInput(MouseEventLeftDown), CreateMouseInput(MouseEventLeftUp) };
                Marshal.SetLastPInvokeError(0);
                var inserted = SendInput((uint)clickInputs.Length, clickInputs, inputSize);
                var sendInputError = Marshal.GetLastPInvokeError();
                captionClick["sendInputInserted"] = inserted;
                captionClick["sendInputLastError"] = sendInputError;
                if (inserted == 1)
                {
                    Marshal.SetLastPInvokeError(0);
                    captionClick["partialClickReleaseInserted"] = SendInput(
                        1, new[] { CreateMouseInput(MouseEventLeftUp) }, inputSize);
                    captionClick["partialClickReleaseLastError"] = Marshal.GetLastPInvokeError();
                }
                if (inserted != (uint)clickInputs.Length) throw new InvalidOperationException(
                    $"SendInput inserted {inserted} of {clickInputs.Length} caption-click events; lastError={sendInputError}");

                var focusWait = Stopwatch.StartNew();
                while (GetForegroundWindow() != window && focusWait.Elapsed.TotalSeconds < 3) Thread.Sleep(25);
            }
            var foregroundAfterHandle = GetForegroundWindow();
            measurement["foregroundAfter"] = DescribeWindowContext(
                foregroundAfterHandle, includeGeometry: true);
            if (!IsWindow(window) || !IsWindowVisible(window) || IsIconic(window) || foregroundAfterHandle != window)
                throw new InvalidOperationException("Candidate UI window did not become the actual foreground window");
            measurement["ready"] = true;
        }
        catch (Exception error)
        {
            measurement["foregroundAtFailure"] = DescribeWindowContext(
                GetForegroundWindow(), includeGeometry: true);
            measurement["ready"] = false;
            measurement["error"] = error.ToString();
            var serializedFailure = JsonSerializer.Serialize(measurement);
            TracePhase($"require-foreground {serializedFailure}");
            throw new InvalidOperationException(
                $"Could not activate UI window; focus measurement={serializedFailure}", error);
        }

        TracePhase($"require-foreground {JsonSerializer.Serialize(measurement)}");
    }

    private static void PrepareCaptureCursor(IntPtr window, Rectangle target, Rectangle client, Rectangle display)
    {
        RequireForeground(window, "screenshot");
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
        string? mainOperation = null;
        try
        {
            if (args.Length == 0) TracePhase($"main-entry utc={UtcTimestamp()}");
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
            var requestLine = Console.In.ReadLine()
                ?? throw new EndOfStreamException("Native UI helper did not receive its initial JSON request line");
            using var input = JsonDocument.Parse(requestLine);
            var request = input.RootElement;
            string Text(string key) => request.GetProperty(key).GetString()!;
            var operation = Text("operation");
            mainOperation = operation;
            TracePhase($"main-operation-start utc={UtcTimestamp()} operation={JsonSerializer.Serialize(operation)}");
            if (operation == "native-case") return RunNativeCase(request);
            if (operation != "probe") throw new ArgumentException($"Unknown operation: {operation}");

            var settleRedirectWorkers = false;
            if (request.TryGetProperty("settle_redirect_workers", out var settleValue))
            {
                if (settleValue.ValueKind is not (JsonValueKind.True or JsonValueKind.False))
                    throw new ArgumentException("settle_redirect_workers must be a JSON boolean");
                settleRedirectWorkers = settleValue.GetBoolean();
            }
            if (settleRedirectWorkers && request.TryGetProperty("pid", out _))
                throw new ArgumentException("settle_redirect_workers cannot be combined with a PID-bound probe");
            if (settleRedirectWorkers && request.TryGetProperty("identity", out _))
                throw new ArgumentException("settle_redirect_workers cannot be combined with an identity-bound probe");

            var expected = Path.GetFullPath(Text("executable"));
            Process? found = null;
            if (settleRedirectWorkers)
            {
                found = FindSettledProbeProcess(expected);
            }
            else if (request.TryGetProperty("pid", out var requestedPid))
            {
                try { found = Process.GetProcessById(requestedPid.GetInt32()); }
                catch (ArgumentException)
                {
                    Console.WriteLine("{\"ready\":false,\"alive\":false}");
                    return 0;
                }
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

            if (found is null)
            {
                Console.WriteLine("{\"ready\":false,\"alive\":false}");
                return 0;
            }
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
            process.Refresh();
            var window = process.MainWindowHandle;
            Console.WriteLine(JsonSerializer.Serialize(new
            {
                alive = true,
                pid = process.Id,
                identity,
                windowHandle = window == IntPtr.Zero ? null : $"0x{window.ToInt64():X}",
            }));
            return 0;
        }
        catch (Exception error)
        {
            Console.Error.WriteLine(error);
            Console.Error.Flush();
            return 1;
        }
        finally
        {
            TracePhase($"main-finally-enter utc={UtcTimestamp()} operation={JsonSerializer.Serialize(mainOperation)}");
            TracePhase($"main-finally-exit utc={UtcTimestamp()} operation={JsonSerializer.Serialize(mainOperation)}");
        }
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
            if (depth > 0 && element.Current.AutomationId == "Backend logs")
                continue;
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

    private static Rectangle Capture(IntPtr window, int expectedPid, string path) =>
        InPerMonitorV2DpiContext(() => CapturePhysical(window, expectedPid, path));

    private static Rectangle CapturePhysical(IntPtr window, int expectedPid, string path)
    {
        var captureStarted = Stopwatch.GetTimestamp();
        TracePhase($"capture-physical-start utc={UtcTimestamp()} pid={expectedPid} hwnd=0x{window.ToInt64():X} path={JsonSerializer.Serialize(path)}");
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
        TracePhase($"capture-cursor-prepare-start utc={UtcTimestamp()} path={JsonSerializer.Serialize(path)}");
        PrepareCaptureCursor(window, target, client, display.Bounds);
        TracePhase($"capture-cursor-prepare-complete utc={UtcTimestamp()} path={JsonSerializer.Serialize(path)}");
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
        TracePhase($"capture-render-wait-start utc={UtcTimestamp()} client={clientInBitmap} path={JsonSerializer.Serialize(path)}");
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
        TracePhase($"capture-render-wait-complete utc={UtcTimestamp()} elapsed_ms={renderWait.Elapsed.TotalMilliseconds:F3} frames={frameCount} rendered={clientRendered} path={JsonSerializer.Serialize(path)}");
        if (!clientRendered)
            throw new InvalidOperationException(
                $"UI client area did not render within 5 seconds ({frameCount} frames captured)"
            );
        TracePhase($"capture-image-save-start utc={UtcTimestamp()} path={JsonSerializer.Serialize(path)}");
        bitmap.Save(path, ImageFormat.Png);
        TracePhase($"capture-image-save-complete utc={UtcTimestamp()} path={JsonSerializer.Serialize(path)}");
        RequireUnobstructed();
        GetWindowThreadProcessId(window, out var afterOwner);
        if (afterOwner != expectedPid) throw new InvalidOperationException("UI window ownership changed during screenshot");
        if (!GetWindowRect(window, out var after) || !r.Equals(after))
            throw new InvalidOperationException("Native window changed during screenshot");
        TracePhase($"capture-physical-complete utc={UtcTimestamp()} elapsed_ms={Stopwatch.GetElapsedTime(captureStarted).TotalMilliseconds:F3} bounds={target} path={JsonSerializer.Serialize(path)}");
        return target;
    }
}
