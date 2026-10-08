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
using System.Threading;
using System.Windows.Automation;
using Microsoft.Win32;
using Microsoft.Win32.SafeHandles;
using Forms = System.Windows.Forms;

// Runs only in the interactive test user's session, outside the shipped app.
internal static class Program
{
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] private static extern IntPtr WindowFromPoint(NativePoint point);
    [DllImport("user32.dll")] private static extern IntPtr GetAncestor(IntPtr window, uint flags);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetWindowRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool SetWindowPos(IntPtr window, IntPtr insertAfter, int x, int y, int width, int height, uint flags);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);
    [DllImport("user32.dll")] private static extern bool ShowWindowAsync(IntPtr window, int command);
    [DllImport("user32.dll")] private static extern IntPtr GetWindow(IntPtr window, uint command);
    [DllImport("user32.dll", SetLastError = true)] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll", SetLastError = true)] private static extern IntPtr GetThreadDesktop(uint threadId);
    [DllImport("user32.dll", SetLastError = true)] private static extern IntPtr OpenInputDesktop(uint flags, bool inherit, uint desiredAccess);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool CloseDesktop(IntPtr desktop);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern bool GetUserObjectInformation(IntPtr handle, int index, StringBuilder information, uint length, out uint needed);
    [DllImport("kernel32.dll")] private static extern uint GetCurrentProcessId();
    [DllImport("kernel32.dll")] private static extern uint GetCurrentThreadId();
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool ProcessIdToSessionId(uint processId, out uint sessionId);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern SafeProcessHandle OpenProcess(uint desiredAccess, bool inheritHandle, uint processId);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool GetProcessTimes(
        SafeProcessHandle process, out NativeFileTime creationTime, out NativeFileTime exitTime,
        out NativeFileTime kernelTime, out NativeFileTime userTime);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern uint GetProcessId(SafeProcessHandle process);
    [DllImport("dbghelp.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool MiniDumpWriteDump(
        SafeProcessHandle process, uint processId, SafeFileHandle file, uint dumpType,
        IntPtr exceptionParam, IntPtr userStreamParam, IntPtr callbackParam);
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
    [DllImport("shell32.dll", CharSet = CharSet.Unicode, ExactSpelling = true, EntryPoint = "ShellExecuteExW", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool ShellExecuteEx(ref ShellExecuteInfo executeInfo);
    [DllImport("kernel32.dll", EntryPoint = "GetProcessId", SetLastError = true)]
    private static extern uint GetProcessIdRaw(IntPtr process);
    [DllImport("kernel32.dll", EntryPoint = "GetProcessTimes", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetProcessTimesRaw(IntPtr process, out NativeFileTime creationTime,
        out NativeFileTime exitTime, out NativeFileTime kernelTime, out NativeFileTime userTime);
    [DllImport("kernel32.dll", EntryPoint = "GetExitCodeProcess", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetExitCodeProcess(IntPtr process, out uint exitCode);
    [DllImport("kernel32.dll", EntryPoint = "QueryFullProcessImageNameW", CharSet = CharSet.Unicode, ExactSpelling = true, SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool QueryFullProcessImageName(IntPtr process, uint flags, StringBuilder imageName, ref uint size);
    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool CloseHandle(IntPtr handle);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool GetCursorPos(out NativePoint point);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetClassNameW", SetLastError = true)]
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
    [StructLayout(LayoutKind.Sequential)] private struct NativeFileTime { public uint Low, High; }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    private struct ShellExecuteInfo
    {
        public int Size;
        public uint Mask;
        public IntPtr Owner;
        [MarshalAs(UnmanagedType.LPWStr)] public string? Verb;
        [MarshalAs(UnmanagedType.LPWStr)] public string? File;
        [MarshalAs(UnmanagedType.LPWStr)] public string? Parameters;
        [MarshalAs(UnmanagedType.LPWStr)] public string? Directory;
        public int ShowCommand;
        public IntPtr Instance;
        public IntPtr IdList;
        [MarshalAs(UnmanagedType.LPWStr)] public string? Class;
        public IntPtr ClassKey;
        public uint HotKey;
        public IntPtr IconOrMonitor;
        public IntPtr Process;
    }
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
    private const uint WmNull = 0x0000;
    private const uint WmNcHitTest = 0x0084;
    private const uint SmtoAbortIfHung = 0x0002;
    private const uint SmtoErrorOnExit = 0x0020;
    private const uint GaRoot = 2;
    private const uint GaRootOwner = 3;
    private const int HtCaption = 2;
    private const uint InputMouse = 0;
    private const uint MouseEventLeftDown = 0x0002;
    private const uint MouseEventLeftUp = 0x0004;
    private const uint ProcessQueryInformation = 0x0400;
    private const uint ProcessVmRead = 0x0010;
    private const uint MiniDumpWithFullMemory = 0x00000002;
    private const uint MiniDumpWithThreadInfo = 0x00001000;

    private const int UiaBoundingRectanglePropertyId = 30001;
    private const int UiaProcessIdPropertyId = 30002;
    private const int UiaControlTypePropertyId = 30003;
    private const int UiaNamePropertyId = 30005;
    private const int UiaAutomationIdPropertyId = 30011;
    private const int UiaIsOffscreenPropertyId = 30022;
    private static readonly Guid CUIAutomationClassId = new("FF48DBA4-60EF-4201-AA87-54103EEF594E");

    // Only the needed methods are callable. The unused declarations preserve the COM vtable order.
    [ComImport]
    [Guid("30CBE57D-D9D0-452A-AB13-7AC5AC4825EE")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IUIAutomationClientCom
    {
        [PreserveSig] int CompareElements(IntPtr element1, IntPtr element2, [MarshalAs(UnmanagedType.Bool)] out bool same);
        [PreserveSig] int CompareRuntimeIds(IntPtr runtimeId1, IntPtr runtimeId2, [MarshalAs(UnmanagedType.Bool)] out bool same);
        [PreserveSig] int GetRootElement(out IntPtr root);
        [PreserveSig] int ElementFromHandle(IntPtr window, out IntPtr element);
        [PreserveSig] int ElementFromPoint(NativePoint point, [MarshalAs(UnmanagedType.Interface)] out IUIAutomationElementCom element);
    }

    [ComImport]
    [Guid("D22108AA-8AC5-49A5-837B-37BBB3D7591E")]
    [InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IUIAutomationElementCom
    {
        [PreserveSig] int SetFocus();
        [PreserveSig] int GetRuntimeId(out IntPtr runtimeId);
        [PreserveSig] int FindFirst(int scope, IntPtr condition, out IntPtr found);
        [PreserveSig] int FindAll(int scope, IntPtr condition, out IntPtr found);
        [PreserveSig] int FindFirstBuildCache(int scope, IntPtr condition, IntPtr cacheRequest, out IntPtr found);
        [PreserveSig] int FindAllBuildCache(int scope, IntPtr condition, IntPtr cacheRequest, out IntPtr found);
        [PreserveSig] int BuildUpdatedCache(IntPtr cacheRequest, out IntPtr updatedElement);
        [PreserveSig]
        int GetCurrentPropertyValue(
            int propertyId,
            [MarshalAs(UnmanagedType.Struct)] out object? value);
    }

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

    private static string ReadUserObjectName(IntPtr handle, string description)
    {
        const int UoiName = 2;
        var name = new StringBuilder(256);
        Marshal.SetLastPInvokeError(0);
        if (!GetUserObjectInformation(handle, UoiName, name, checked((uint)(name.Capacity * sizeof(char))), out _))
            throw new System.ComponentModel.Win32Exception(
                Marshal.GetLastWin32Error(), $"Could not read the {description} name");
        return name.ToString();
    }

    private static Dictionary<string, object?> DescribeThreadDesktop(uint threadId)
    {
        var result = new Dictionary<string, object?> { ["threadId"] = threadId };
        try
        {
            Marshal.SetLastPInvokeError(0);
            var desktop = GetThreadDesktop(threadId);
            result["handle"] = $"0x{desktop.ToInt64():X}";
            if (desktop == IntPtr.Zero)
                throw new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not get the thread desktop");
            result["name"] = ReadUserObjectName(desktop, "thread desktop");
        }
        catch (Exception error)
        {
            result["error"] = error.ToString();
        }
        return result;
    }

    private static Dictionary<string, object?> DescribeInputDesktop()
    {
        var result = new Dictionary<string, object?>();
        var errors = new List<string>();
        var desktop = IntPtr.Zero;
        try
        {
            Marshal.SetLastPInvokeError(0);
            desktop = OpenInputDesktop(0, false, 0x0001); // DESKTOP_READOBJECTS
            result["handle"] = $"0x{desktop.ToInt64():X}";
            if (desktop == IntPtr.Zero)
                throw new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not open the input desktop for a read-only name query");
            result["name"] = ReadUserObjectName(desktop, "input desktop");
        }
        catch (Exception error)
        {
            errors.Add(error.ToString());
        }
        finally
        {
            if (desktop != IntPtr.Zero)
            {
                Marshal.SetLastPInvokeError(0);
                if (!CloseDesktop(desktop))
                    errors.Add(new System.ComponentModel.Win32Exception(
                        Marshal.GetLastWin32Error(), "Could not close the input desktop handle").ToString());
            }
        }
        result["errors"] = errors;
        return result;
    }

    private static Dictionary<string, object?> DescribeWindowContext(
        IntPtr window,
        bool includeThreadDesktop,
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

        if (includeThreadDesktop && threadId != 0)
            result["threadDesktop"] = DescribeThreadDesktop(threadId);

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

    private static Dictionary<string, object?> CapturePointContextBeforeFromPoint(
        IntPtr targetWindow,
        int pointX,
        int pointY)
    {
        return InPerMonitorV2DpiContext(() =>
        {
            var helperThreadId = GetCurrentThreadId();
            var helperProcessId = GetCurrentProcessId();
            var helper = new Dictionary<string, object?>
            {
                ["processId"] = helperProcessId,
                ["threadId"] = helperThreadId,
                ["apartmentState"] = Thread.CurrentThread.GetApartmentState().ToString(),
                ["threadDesktop"] = DescribeThreadDesktop(helperThreadId),
            };
            Marshal.SetLastPInvokeError(0);
            if (ProcessIdToSessionId(helperProcessId, out var helperSessionId))
                helper["sessionId"] = helperSessionId;
            else
                helper["sessionIdError"] = new System.ComponentModel.Win32Exception(
                    Marshal.GetLastWin32Error(), "Could not read the helper process session ID").ToString();

            var inputDesktop = DescribeInputDesktop();
            var targetWindowDescription = DescribeWindowContext(
                targetWindow, includeThreadDesktop: true, includeGeometry: true);
            var result = new Dictionary<string, object?>
            {
                ["coordinateSpace"] = "physical-screen-pixels",
                ["point"] = new { x = pointX, y = pointY },
                ["helper"] = helper,
                ["inputDesktop"] = inputDesktop,
                ["targetWindow"] = targetWindowDescription,
            };

            var foregroundWindow = GetForegroundWindow();
            Marshal.SetLastPInvokeError(0);
            var pointWindowHandle = WindowFromPoint(new NativePoint { X = pointX, Y = pointY });
            var pointWindow = DescribeWindowContext(
                pointWindowHandle, includeThreadDesktop: true, includeGeometry: true);
            try
            {
                Marshal.SetLastPInvokeError(0);
                var rootWindow = GetAncestor(pointWindowHandle, 2); // GA_ROOT
                pointWindow["root"] = DescribeWindowContext(rootWindow, includeThreadDesktop: false, includeGeometry: true);
            }
            catch (Exception error)
            {
                pointWindow["rootError"] = error.ToString();
            }
            try
            {
                var ownerWindow = GetWindow(pointWindowHandle, 4); // GW_OWNER
                pointWindow["ownerWindow"] = DescribeWindowContext(ownerWindow, includeThreadDesktop: false, includeGeometry: true);
            }
            catch (Exception error)
            {
                pointWindow["ownerWindowError"] = error.ToString();
            }
            result["capturedAtUtc"] = UtcTimestamp();
            result["foregroundWindow"] = DescribeWindowContext(
                foregroundWindow, includeThreadDesktop: false, includeGeometry: false);
            result["windowFromPoint"] = pointWindow;
            return result;
        });
    }

    private static Dictionary<string, object?> ProbeWindowMessageResponsiveness(
        IntPtr window,
        int expectedProcessId,
        string expectedProcessIdentity)
    {
        const uint timeoutMs = 1000;
        var started = Stopwatch.GetTimestamp();
        var result = new Dictionary<string, object?>
        {
            ["capturedAtUtc"] = UtcTimestamp(),
            ["message"] = "WM_NULL",
            ["timeoutMs"] = timeoutMs,
            ["flags"] = "SMTO_ABORTIFHUNG|SMTO_ERRORONEXIT",
            ["windowHandle"] = $"0x{window.ToInt64():X}",
            ["expectedProcessId"] = expectedProcessId,
            ["expectedProcessIdentity"] = expectedProcessIdentity,
            ["sendAttempted"] = false,
        };
        try
        {
            var windowContext = DescribeWindowContext(window, includeThreadDesktop: false, includeGeometry: false);
            result["windowContext"] = windowContext;
            var isWindow = IsWindow(window);
            result["isWindow"] = isWindow;
            var threadId = windowContext.GetValueOrDefault("threadId") is uint observedThreadId ? observedThreadId : 0;
            var processId = windowContext.GetValueOrDefault("processId") is uint observedProcessId ? observedProcessId : 0;
            if (!isWindow || threadId == 0 || processId != checked((uint)expectedProcessId))
                throw new InvalidOperationException("The target HWND no longer has the verified window/process owner");

            string actualIdentity;
            try
            {
                using var owner = Process.GetProcessById(checked((int)processId));
                actualIdentity = owner.StartTime.ToUniversalTime().Ticks.ToString(CultureInfo.InvariantCulture);
            }
            catch (Exception error)
            {
                result["ownerProcessIdentityError"] = error.ToString();
                throw;
            }
            result["ownerProcessIdentity"] = actualIdentity;
            if (actualIdentity != expectedProcessIdentity)
                throw new InvalidOperationException("The target HWND PID no longer has the verified process identity");

            Marshal.SetLastPInvokeError(0);
            var sendStarted = Stopwatch.GetTimestamp();
            result["sendAttempted"] = true;
            try
            {
                var messageResult = SendMessageTimeout(window, WmNull, IntPtr.Zero, IntPtr.Zero,
                    SmtoAbortIfHung | SmtoErrorOnExit, timeoutMs, out var returnedResult);
                var lastError = Marshal.GetLastWin32Error();
                result["sendMessageTimeoutSucceeded"] = messageResult != IntPtr.Zero;
                result["windowProcedureResult"] = returnedResult.ToUInt64();
                result["lastError"] = lastError;
                if (messageResult == IntPtr.Zero)
                    result["error"] = lastError == 0
                        ? "SendMessageTimeoutW returned zero without setting a Win32 last error"
                        : new System.ComponentModel.Win32Exception(lastError, "SendMessageTimeoutW failed or timed out").ToString();
            }
            finally { result["sendDurationMs"] = Stopwatch.GetElapsedTime(sendStarted).TotalMilliseconds; }
        }
        catch (Exception error)
        {
            result["error"] = error.ToString();
        }
        result["durationMs"] = Stopwatch.GetElapsedTime(started).TotalMilliseconds;
        return result;
    }

    private static Dictionary<string, object?> CapturePointFailureDump(
        Process process,
        string expectedIdentity,
        JsonElement request,
        string failure,
        string dumpPrefix = "uia-point-failure")
    {
        const uint dumpType = MiniDumpWithFullMemory | MiniDumpWithThreadInfo;
        var result = new Dictionary<string, object?>
        {
            ["attempted"] = true,
            ["failure"] = failure,
            ["processId"] = process.Id,
            ["expectedProcessIdentity"] = expectedIdentity,
            ["dumpTypeFlags"] = dumpType,
            ["dumpType"] = "MiniDumpWithFullMemory|MiniDumpWithThreadInfo",
            ["captureIntent"] = "live process memory and thread state; no exception record supplied",
            ["exceptionInformationIncluded"] = false,
            ["written"] = false,
        };
        string? partialPath = null;
        try
        {
            if (!request.TryGetProperty("dumpDirectory", out var directoryElement) ||
                string.IsNullOrWhiteSpace(directoryElement.GetString()))
                throw new InvalidOperationException("Failure dump directory was not supplied by the Windows test harness");
            var directory = Path.GetFullPath(directoryElement.GetString()!);
            if (!Directory.Exists(directory))
                throw new DirectoryNotFoundException($"Failure dump directory does not exist: {directory}");
            result["dumpDirectory"] = directory;
            try { result["availableDiskBytesBeforeDump"] = new DriveInfo(Path.GetPathRoot(directory)!).AvailableFreeSpace; }
            catch (Exception error) { result["availableDiskBytesError"] = error.ToString(); }
            try
            {
                process.Refresh();
                result["workingSetBytesBeforeDump"] = process.WorkingSet64;
                result["privateMemoryBytesBeforeDump"] = process.PrivateMemorySize64;
            }
            catch (Exception error) { result["processMemorySnapshotError"] = error.ToString(); }

            using var target = OpenProcess(
                ProcessQueryInformation | ProcessVmRead, false, checked((uint)process.Id));
            if (target.IsInvalid)
            {
                var code = Marshal.GetLastWin32Error();
                result["openProcessLastError"] = code;
                throw new System.ComponentModel.Win32Exception(code, "OpenProcess for failure dump failed");
            }
            var actualPid = GetProcessId(target);
            if (actualPid == 0)
            {
                var code = Marshal.GetLastWin32Error();
                result["getProcessIdLastError"] = code;
                throw new System.ComponentModel.Win32Exception(code, "GetProcessId for failure dump handle failed");
            }
            if (actualPid != process.Id)
                throw new InvalidOperationException($"Failure dump handle PID changed: expected {process.Id}, observed {actualPid}");
            if (!GetProcessTimes(target, out var creation, out _, out _, out _))
            {
                var code = Marshal.GetLastWin32Error();
                result["getProcessTimesLastError"] = code;
                throw new System.ComponentModel.Win32Exception(code, "GetProcessTimes for failure dump handle failed");
            }
            var creationFileTime = unchecked(((long)creation.High << 32) | creation.Low);
            var actualIdentity = DateTime.FromFileTimeUtc(creationFileTime).Ticks.ToString(CultureInfo.InvariantCulture);
            result["actualProcessIdentity"] = actualIdentity;
            result["processIdentityVerified"] = actualIdentity == expectedIdentity;
            if (actualIdentity != expectedIdentity)
                throw new InvalidOperationException("Failure dump handle no longer refers to the verified process instance");

            var dumpPath = Path.Combine(directory, $"{dumpPrefix}-{process.Id}-{Guid.NewGuid():N}.dmp");
            partialPath = dumpPath + ".partial";
            result["dumpPath"] = dumpPath;
            result["partialPath"] = partialPath;
            using (var dump = new FileStream(partialPath, FileMode.CreateNew, FileAccess.ReadWrite, FileShare.Read))
            {
                var written = MiniDumpWriteDump(target, actualPid, dump.SafeFileHandle, dumpType,
                    IntPtr.Zero, IntPtr.Zero, IntPtr.Zero);
                if (!written)
                {
                    var code = Marshal.GetLastWin32Error();
                    result["miniDumpWriteDumpLastError"] = code;
                    throw new System.ComponentModel.Win32Exception(code, "MiniDumpWriteDump failed");
                }
                dump.Flush(flushToDisk: true);
                result["bytesWritten"] = dump.Length;
                if (dump.Length == 0) throw new InvalidDataException("MiniDumpWriteDump returned success but wrote an empty file");
            }
            File.Move(partialPath, dumpPath);
            result["partialPath"] = null;
            result["written"] = true;
        }
        catch (Exception error)
        {
            result["error"] = error.ToString();
            if (partialPath is not null && File.Exists(partialPath))
            {
                result["partialPath"] = partialPath;
                try { result["partialBytes"] = new FileInfo(partialPath).Length; }
                catch (Exception sizeError) { result["partialSizeError"] = sizeError.ToString(); }
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

    private readonly record struct NativeActionState(
        AutomationElement? Element,
        string AutomationId,
        string? Name,
        string? ControlType,
        bool? IsControlElement,
        bool? Enabled,
        bool? Offscreen)
    {
        public object ToDiagnostic() => new { automation_id = AutomationId, found = Element is not null,
            name = Name, control_type = ControlType, is_control_element = IsControlElement,
            enabled = Enabled, offscreen = Offscreen };
    }

    private static NativeActionState ReadActionState(AutomationElement scope, string automationId)
    {
        var element = scope.FindFirst(TreeScope.Descendants, new PropertyCondition(AutomationElement.AutomationIdProperty, automationId));
        if (element is null) return new NativeActionState(null, automationId, null, null, null, null, null);
        var current = element.Current;
        return new NativeActionState(element, current.AutomationId, current.Name, current.ControlType.ProgrammaticName,
            current.IsControlElement, current.IsEnabled, current.IsOffscreen);
    }

    private static Dictionary<string, object?> DescribeProtocolRegistration()
    {
        var result = new Dictionary<string, object?> { ["registry_view"] = RegistryView.Default.ToString() };
        try
        {
            using var classes = RegistryKey.OpenBaseKey(RegistryHive.ClassesRoot, RegistryView.Default);
            using var scheme = classes.OpenSubKey("dobbyvpn", writable: false);
            result["scheme_key_found"] = scheme is not null;
            if (scheme is null) return result;
            result["url_protocol_present"] = scheme.GetValueNames().Contains("URL Protocol", StringComparer.OrdinalIgnoreCase);
            result["url_protocol"] = scheme.GetValue("URL Protocol", null, RegistryValueOptions.DoNotExpandEnvironmentNames);
            using var open = scheme.OpenSubKey("shell\\open", writable: false);
            result["open_verb_found"] = open is not null;
            if (open is null) return result;
            using var command = open.OpenSubKey("command", writable: false);
            result["command_key_found"] = command is not null;
            if (command is not null)
            {
                result["command"] = command.GetValue(string.Empty, null, RegistryValueOptions.DoNotExpandEnvironmentNames);
                result["delegate_execute"] = command.GetValue("DelegateExecute", null, RegistryValueOptions.DoNotExpandEnvironmentNames);
            }
            using var dde = open.OpenSubKey("ddeexec", writable: false);
            result["ddeexec_key_found"] = dde is not null;
        }
        catch (Exception error) { result["error"] = error.ToString(); }
        return result;
    }

    private static Dictionary<string, object?> DescribeShellProcess(IntPtr processHandle)
    {
        var result = new Dictionary<string, object?> { ["handle_returned"] = processHandle != IntPtr.Zero };
        if (processHandle == IntPtr.Zero) return result;

        var processId = GetProcessIdRaw(processHandle);
        if (processId == 0)
            result["pid_error"] = new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()).ToString();
        else
            result["pid"] = processId;

        if (GetProcessTimesRaw(processHandle, out var creation, out _, out _, out _))
        {
            var fileTime = unchecked((long)(((ulong)creation.High << 32) | creation.Low));
            result["creation_identity"] = DateTime.FromFileTimeUtc(fileTime).Ticks.ToString(CultureInfo.InvariantCulture);
        }
        else
            result["creation_identity_error"] = new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()).ToString();

        var imagePath = new StringBuilder(32768);
        uint imagePathSize = (uint)imagePath.Capacity;
        if (QueryFullProcessImageName(processHandle, 0, imagePath, ref imagePathSize))
            result["image_path"] = imagePath.ToString();
        else
            result["image_path_error"] = new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()).ToString();

        if (GetExitCodeProcess(processHandle, out var exitCode))
        {
            result["exit_code"] = exitCode;
            result["still_active"] = exitCode == 259;
        }
        else
            result["exit_code_error"] = new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()).ToString();
        return result;
    }

    private static Dictionary<string, object?> ShellDispatchEvidence(
        string protocolUri, IntPtr window, int pid, string identity,
        Dictionary<string, object?> effectiveRegistration, JsonElement request, Func<long, string> timestamp)
    {
        const uint SeeMaskNoCloseProcess = 0x00000040;
        // The STA helper has no shell message loop to finish an asynchronous DDE handoff.
        const uint SeeMaskNoAsync = 0x00000100;
        var executeInfo = new ShellExecuteInfo
        {
            Size = Marshal.SizeOf<ShellExecuteInfo>(),
            Mask = SeeMaskNoCloseProcess | SeeMaskNoAsync,
            Owner = window,
            Verb = "open",
            File = protocolUri,
            ShowCommand = 1,
        };
        var dispatchStarted = Stopwatch.GetTimestamp();
        bool returned = false;
        int? lastError = null;
        Exception? callException = null;
        try
        {
            returned = ShellExecuteEx(ref executeInfo);
            if (!returned) lastError = Marshal.GetLastWin32Error();
        }
        catch (Exception error) { callException = error; }
        var dispatchReturned = Stopwatch.GetTimestamp();
        var shellResult = executeInfo.Instance.ToInt64();

        var processHandle = executeInfo.Process;
        Dictionary<string, object?> processEvidence;
        try { processEvidence = DescribeShellProcess(processHandle); }
        catch (Exception error)
        {
            processEvidence = new Dictionary<string, object?>
            {
                ["handle_returned"] = processHandle != IntPtr.Zero,
                ["observation_error"] = error.ToString(),
            };
        }

        if (protocolUri.Contains("import-during-connect%3D1", StringComparison.OrdinalIgnoreCase))
        {
            if (!returned || shellResult <= 32 || processHandle == IntPtr.Zero)
            {
                processEvidence["live_process_dump"] = new
                {
                    attempted = false,
                    reason = "ShellExecuteEx did not return a successful process handle for the held import",
                };
            }
            else if (processEvidence.GetValueOrDefault("pid") is not uint childPid ||
                processEvidence.GetValueOrDefault("creation_identity") is not string childIdentity ||
                processEvidence.GetValueOrDefault("still_active") is not true)
            {
                processEvidence["live_process_dump"] = new
                {
                    attempted = false,
                    reason = "Returned ShellExecute process identity was unavailable or the process had exited",
                };
            }
            else
            {
                try
                {
                    using var child = Process.GetProcessById(checked((int)childPid));
                    processEvidence["live_process_dump"] = CapturePointFailureDump(
                        child, childIdentity, request,
                        "ShellExecuteEx returned a live handler process for import-during-connect",
                        "shell-import-handler");
                }
                catch (Exception error)
                {
                    processEvidence["live_process_dump"] = new
                    {
                        attempted = true,
                        processId = childPid,
                        expectedProcessIdentity = childIdentity,
                        captureIntent = "live process memory and thread state; no exception record supplied",
                        written = false,
                        error = error.ToString(),
                    };
                }
            }
            if (processHandle != IntPtr.Zero)
            {
                if (GetExitCodeProcess(processHandle, out var exitCodeAfterDump))
                {
                    processEvidence["exit_code_after_dump"] = exitCodeAfterDump;
                    processEvidence["still_active_after_dump"] = exitCodeAfterDump == 259;
                }
                else
                    processEvidence["exit_code_after_dump_error"] =
                        new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error()).ToString();
            }
        }

        bool? handleClosed = null;
        Exception? handleCloseException = null;
        if (processHandle != IntPtr.Zero)
        {
            try
            {
                handleClosed = CloseHandle(processHandle);
                if (handleClosed != true)
                    handleCloseException = new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error());
            }
            catch (Exception error) { handleCloseException = error; }
        }

        var context = $"uri={protocolUri}; pid={pid}; identity={identity}; window=0x{window.ToInt64():X}; " +
            $"dispatch_started={timestamp(dispatchStarted)}; dispatch_returned={timestamp(dispatchReturned)}; " +
            $"shell_execute_returned={returned}; hinstance={shellResult}; last_error={lastError?.ToString(CultureInfo.InvariantCulture) ?? "null"}; " +
            $"registration={JsonSerializer.Serialize(effectiveRegistration)}; " +
            $"launched_process={JsonSerializer.Serialize(processEvidence)}; " +
            $"process_handle_closed={handleClosed?.ToString() ?? "null"}; " +
            $"process_handle_close_error={handleCloseException?.ToString() ?? "null"}";
        Exception? dispatchException = callException;
        if (dispatchException is null && !returned)
        {
            dispatchException = lastError is int code
                ? new System.ComponentModel.Win32Exception(code, "ShellExecuteExW returned false")
                : new InvalidOperationException("ShellExecuteExW returned false without a Win32 error code");
        }
        if (dispatchException is null && shellResult <= 32)
            dispatchException = new InvalidOperationException("ShellExecuteExW returned an error HINSTANCE");
        if (handleCloseException is not null)
        {
            Exception cleanup = dispatchException is null
                ? handleCloseException
                : new AggregateException("Shell dispatch and handle cleanup both failed", dispatchException, handleCloseException);
            throw new InvalidOperationException($"Shell process handle cleanup failed; {context}", cleanup);
        }
        if (dispatchException is not null)
            throw new InvalidOperationException($"ShellExecuteExW failed; {context}", dispatchException);

        return new Dictionary<string, object?>
        {
            ["protocol_dispatch_started_at_utc"] = timestamp(dispatchStarted),
            ["protocol_dispatch_returned_at_utc"] = timestamp(dispatchReturned),
            ["shell_execute_returned"] = returned,
            ["shell_execute_result"] = shellResult,
            ["shell_execute_last_error"] = lastError,
            ["effective_protocol_registration"] = effectiveRegistration,
            ["shell_execute_process"] = processEvidence,
            ["shell_execute_process_handle_closed"] = handleClosed,
        };
    }

    private static Dictionary<string, object?> ProfileSwitchAction(
        AutomationElement root, IntPtr window, Process process, string identity,
        string targetId, string competingId, string? protocolUri = null, bool observeOnly = false,
        JsonElement? request = null)
    {
        if (observeOnly && protocolUri is not null)
            throw new ArgumentException("Profile switch observation cannot dispatch a protocol URI");
        if (!TryGetProfileNumber(targetId, " action", out _) ||
            !TryGetProfileNumber(competingId, " action", out _) || targetId == competingId)
            throw new ArgumentException("Profile switch action requires two distinct profile action identifiers");
        if (protocolUri is not null && !protocolUri.StartsWith(
                "dobbyvpn://import?url=", StringComparison.OrdinalIgnoreCase))
            throw new ArgumentException("Profile switch import requires a DobbyVPN import URI");

        // Keep stable scopes; WinUI recreates profile action peers on each Snapshot.
        var controls = RequireAutomationId(root, "Connection controls");
        var viewport = RequireAutomationId(controls, "Profile list viewport");
        _ = RequireAutomationId(controls, "VPN connection action");
        NativeActionState before = default;
        NativeActionState competingBefore = default;
        Dictionary<string, object?>? lastBaselineSample = null;
        string? latestBaselineException = null;
        bool CanConnect(NativeActionState target, NativeActionState competing) =>
            target.AutomationId == targetId && target.ControlType == ControlType.Button.ProgrammaticName &&
            target.IsControlElement == true && target.Name == "Connect" && target.Enabled == true && target.Offscreen == false &&
            competing.AutomationId == competingId && competing.ControlType == ControlType.Button.ProgrammaticName &&
            competing.IsControlElement == true && competing.Name is ("Connect" or "Disconnect") &&
            competing.Enabled == true && competing.Offscreen == false;
        try
        {
            WaitFor(() =>
            {
                var sampledTarget = default(NativeActionState);
                var sampledCompeting = default(NativeActionState);
                try
                {
                    sampledTarget = ReadActionState(viewport, targetId);
                    sampledCompeting = ReadActionState(viewport, competingId);
                    var ready = CanConnect(sampledTarget, sampledCompeting);
                    if (ready)
                    {
                        before = sampledTarget;
                        competingBefore = sampledCompeting;
                    }
                    lastBaselineSample = new Dictionary<string, object?>
                    {
                        ["ready"] = ready,
                        ["target"] = sampledTarget.ToDiagnostic(),
                        ["competing"] = sampledCompeting.ToDiagnostic(),
                    };
                    Console.Error.WriteLine(
                        $"native-ui-phase=profile-switch-baseline-sample {JsonSerializer.Serialize(lastBaselineSample)}");
                    Console.Error.Flush();
                    return ready;
                }
                catch (ElementNotAvailableException error)
                {
                    latestBaselineException = error.ToString();
                    lastBaselineSample = new Dictionary<string, object?>
                    {
                        ["ready"] = false,
                        ["target"] = sampledTarget.ToDiagnostic(),
                        ["competing"] = sampledCompeting.ToDiagnostic(),
                        ["exception"] = latestBaselineException,
                    };
                    Console.Error.WriteLine(
                        $"native-ui-phase=profile-switch-baseline-element-unavailable {JsonSerializer.Serialize(lastBaselineSample)}");
                    Console.Error.WriteLine(latestBaselineException);
                    Console.Error.Flush();
                    return false;
                }
            }, "Profile actions did not become visible and enabled before Connect", seconds: 15);
        }
        catch (TimeoutException error)
        {
            var failure = new Dictionary<string, object?>
            {
                ["last_sample"] = lastBaselineSample,
                ["latest_element_not_available_exception"] = latestBaselineException,
                ["wait_exception"] = error.ToString(),
            };
            Console.Error.WriteLine(
                $"native-ui-phase=profile-switch-baseline-timeout {JsonSerializer.Serialize(failure)}");
            Console.Error.Flush();
            throw new InvalidOperationException(
                $"Profile actions did not become visible and enabled before Connect: {JsonSerializer.Serialize(failure)}", error);
        }
        bool CanStop(NativeActionState target, NativeActionState competing, NativeActionState connection)
        {
            var connectionName = connection.Name;
            var connectionIsObserved = connection.Element is not null && connection.AutomationId == "VPN connection action" &&
                connection.ControlType == ControlType.Button.ProgrammaticName &&
                connection.IsControlElement == true &&
                (connectionName is "Connect" or "Auto connect" or "Stop" or "Disconnect") &&
                connection.Enabled is not null && connection.Offscreen == false;
            return target.AutomationId == targetId && competing.AutomationId == competingId &&
                target.ControlType == ControlType.Button.ProgrammaticName && target.IsControlElement == true && target.Name == "Stop" &&
                target.Enabled == true && target.Offscreen == false &&
                competing.ControlType == ControlType.Button.ProgrammaticName && competing.IsControlElement == true && competing.Name == "Connect" &&
                competing.Enabled == false && competing.Offscreen == false && connectionIsObserved &&
                (connectionName is not ("Connect" or "Auto connect") || connection.Enabled == false);
        }
        if (before.Element is null || !before.Element.TryGetCurrentPattern(InvokePattern.Pattern, out var connectPattern))
            throw new InvalidOperationException($"Profile action has no native InvokePattern: {targetId}");
        var registration = protocolUri is null ? null : DescribeProtocolRegistration();
        RequireForeground(window, "profile switch Connect");
        var clockStarted = Stopwatch.GetTimestamp();
        var utcStarted = DateTimeOffset.UtcNow;
        string Timestamp(long tick) => utcStarted.Add(Stopwatch.GetElapsedTime(clockStarted, tick))
            .ToString("O", CultureInfo.InvariantCulture);
        var connectedAt = Stopwatch.GetTimestamp();
        var connectedAtUtc = Timestamp(connectedAt);
        Console.Error.WriteLine($"native-ui-phase=profile-switch-connect-invoke-start started_at_utc={connectedAtUtc}");
        Console.Error.Flush();
        var invokeReturned = false;
        try
        {
            ((InvokePattern)connectPattern).Invoke();
            invokeReturned = true;
        }
        finally
        {
            var invokeFinished = Stopwatch.GetTimestamp();
            Console.Error.WriteLine(
                $"native-ui-phase=profile-switch-connect-invoke-finish started_at_utc={connectedAtUtc} " +
                $"finished_at_utc={Timestamp(invokeFinished)} returned={invokeReturned} " +
                $"duration_ms={Stopwatch.GetElapsedTime(connectedAt, invokeFinished).TotalMilliseconds}");
            Console.Error.Flush();
        }

        var lastTarget = before;
        var lastCompeting = competingBefore;
        var lastConnection = default(NativeActionState);
        string? unavailable = null;
        var deadline = Stopwatch.GetTimestamp() + 5 * Stopwatch.Frequency;
        var observation = 0;
        void TraceObservation(Dictionary<string, object?> sample)
        {
            sample["observation"] = ++observation;
            sample["recorded_at_utc"] = Timestamp(Stopwatch.GetTimestamp());
            Console.Error.WriteLine(
                $"native-ui-phase=profile-switch-observation {JsonSerializer.Serialize(sample)}");
            Console.Error.Flush();
        }
        NativeActionState ReadTimed(
            AutomationElement scope, string automationId, Dictionary<string, object?> sample, string label)
        {
            var started = Stopwatch.GetTimestamp();
            sample[$"{label}_read_started_at_utc"] = Timestamp(started);
            try
            {
                var state = ReadActionState(scope, automationId);
                sample[$"{label}_state"] = state.ToDiagnostic();
                return state;
            }
            catch (Exception error)
            {
                sample[$"{label}_exception"] = error.ToString();
                throw;
            }
            finally
            {
                var finished = Stopwatch.GetTimestamp();
                sample[$"{label}_read_finished_at_utc"] = Timestamp(finished);
                sample[$"{label}_read_duration_ms"] = Stopwatch.GetElapsedTime(started, finished).TotalMilliseconds;
            }
        }
        while (Stopwatch.GetTimestamp() < deadline)
        {
            var sample = new Dictionary<string, object?>();
            try
            {
                var target = ReadTimed(viewport, targetId, sample, "poll_target");
                var competing = ReadTimed(viewport, competingId, sample, "poll_competing");
                var connection = ReadTimed(controls, "VPN connection action", sample, "poll_connection");
                lastTarget = target; lastCompeting = competing; lastConnection = connection;
                if (CanStop(target, competing, connection))
                {
                    RequireForeground(window, "profile switch Stop");
                    target = ReadTimed(viewport, targetId, sample, "verify_target");
                    competing = ReadTimed(viewport, competingId, sample, "verify_competing");
                    connection = ReadTimed(controls, "VPN connection action", sample, "verify_connection");
                    lastTarget = target; lastCompeting = competing; lastConnection = connection;
                    if (CanStop(target, competing, connection))
                    {
                        var stopElement = target.Element;
                        if (stopElement is null)
                            throw new InvalidOperationException($"Observed Stop action disappeared before invocation: {targetId}");
                        var current = stopElement.Current;
                        if (current.Name != "Stop" || !current.IsEnabled || current.IsOffscreen)
                            throw new InvalidOperationException("The selected profile action changed from Stop before Invoke");
                        var observedAt = Stopwatch.GetTimestamp();
                        if (observeOnly)
                            return new Dictionary<string, object?>
                            {
                                ["ready"] = true, ["pid"] = process.Id, ["identity"] = identity,
                                ["window_handle"] = $"0x{window.ToInt64():X}",
                                ["target_automation_id"] = targetId, ["competing_automation_id"] = competingId,
                                ["observe_only"] = true,
                                ["connect_invoked_at_utc"] = connectedAtUtc,
                                ["stop_observed_at_utc"] = Timestamp(observedAt),
                                ["target_at_stop"] = target.ToDiagnostic(),
                                ["competing_at_stop"] = competing.ToDiagnostic(),
                                ["connection_action_at_stop"] = connection.ToDiagnostic(),
                            };
                        if (protocolUri is not null)
                        {
                            Dictionary<string, object?> dispatchEvidence;
                            try
                            {
                                dispatchEvidence = ShellDispatchEvidence(
                                    protocolUri, window, process.Id, identity, registration!, request ?? default, Timestamp);
                            }
                            catch (Exception error)
                            {
                                var dispatchFailedAt = Stopwatch.GetTimestamp();
                                throw new InvalidOperationException(
                                    $"ShellExecuteExW failed for the observed profile switch import; " +
                                    $"uri={protocolUri}; pid={process.Id}; identity={identity}; " +
                                    $"window=0x{window.ToInt64():X}; dispatch_failed_at={Timestamp(dispatchFailedAt)}; " +
                                    $"target={JsonSerializer.Serialize(target.ToDiagnostic())}; " +
                                    $"competing={JsonSerializer.Serialize(competing.ToDiagnostic())}; " +
                                    $"connection={JsonSerializer.Serialize(connection.ToDiagnostic())}", error);
                            }
                            var result = new Dictionary<string, object?>
                            {
                                ["ready"] = true, ["pid"] = process.Id, ["identity"] = identity,
                                ["window_handle"] = $"0x{window.ToInt64():X}",
                                ["target_automation_id"] = targetId, ["competing_automation_id"] = competingId,
                                ["protocol_uri"] = protocolUri,
                                ["connect_invoked_at_utc"] = connectedAtUtc,
                                ["stop_observed_at_utc"] = Timestamp(observedAt),
                                ["target_at_stop"] = target.ToDiagnostic(),
                                ["competing_at_stop"] = competing.ToDiagnostic(),
                                ["connection_action_at_stop"] = connection.ToDiagnostic(),
                            };
                            foreach (var field in dispatchEvidence) result[field.Key] = field.Value;
                            return result;
                        }
                        if (!stopElement.TryGetCurrentPattern(InvokePattern.Pattern, out var stopPattern))
                            throw new InvalidOperationException($"Observed Stop action has no native InvokePattern: {targetId}");
                        var invokedAt = Stopwatch.GetTimestamp();
                        ((InvokePattern)stopPattern).Invoke();
                        return new Dictionary<string, object?>
                        {
                            ["ready"] = true, ["pid"] = process.Id, ["identity"] = identity,
                            ["target_automation_id"] = targetId, ["competing_automation_id"] = competingId,
                            ["connect_invoked_at_utc"] = connectedAtUtc,
                            ["stop_observed_at_utc"] = Timestamp(observedAt), ["stop_invoked_at_utc"] = Timestamp(invokedAt),
                            ["target_at_stop"] = target.ToDiagnostic(), ["competing_at_stop"] = competing.ToDiagnostic(),
                            ["connection_action_at_stop"] = connection.ToDiagnostic(),
                        };
                    }
                }
            }
            catch (ElementNotAvailableException error)
            {
                unavailable = error.ToString();
                Console.Error.WriteLine(unavailable);
                Console.Error.Flush();
            }
            finally
            {
                TraceObservation(sample);
            }
            Thread.Sleep(20);
        }
        throw new TimeoutException(
            "Profile switch did not expose an enabled Stop action within five seconds; " +
            $"last_target={JsonSerializer.Serialize(lastTarget.ToDiagnostic())}; " +
            $"last_competing={JsonSerializer.Serialize(lastCompeting.ToDiagnostic())}; " +
            $"last_connection_action={JsonSerializer.Serialize(lastConnection.ToDiagnostic())}; " +
            $"last_element_unavailable_exception={unavailable ?? "none"}");
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

    private static bool TryGetProfileNumber(string automationId, string suffix, out int number)
    {
        const string prefix = "Profile ";
        number = 0;
        if (!automationId.StartsWith(prefix, StringComparison.Ordinal) ||
            !automationId.EndsWith(suffix, StringComparison.Ordinal))
            return false;

        var numberText = automationId.Substring(prefix.Length, automationId.Length - prefix.Length - suffix.Length);
        return int.TryParse(numberText, NumberStyles.None, CultureInfo.InvariantCulture, out number) && number > 0;
    }

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
        var visibleActions = new List<(int Number, string AutomationId)>();
        var profileNames = new Dictionary<int, (string Name, string Protocol)>();
        var profileActions = new Dictionary<int, string>();
        var profileOrder = new List<int>();
        foreach (AutomationElement element in profileElements)
        {
            var automationId = element.Current.AutomationId;
            if (TryGetProfileNumber(automationId, " description", out var descriptionNumber))
            {
                var renderedText = element.Current.Name;
                var separator = renderedText.LastIndexOf(" · ", StringComparison.Ordinal);
                if (separator <= 0 || separator + 3 >= renderedText.Length ||
                    !profileNames.TryAdd(descriptionNumber,
                        (renderedText[..separator], renderedText[(separator + 3)..])))
                    throw new InvalidOperationException(
                        $"Profile {descriptionNumber} has missing, duplicate, or malformed rendered description text: {renderedText}");
                profileOrder.Add(descriptionNumber);
            }
            else if (TryGetProfileNumber(automationId, " action", out var actionNumber))
            {
                if (!profileActions.TryAdd(actionNumber, element.Current.Name))
                    throw new InvalidOperationException($"Profile {actionNumber} has duplicate rendered action controls");

                var actionBounds = element.Current.BoundingRectangle;
                if (element.Current.ControlType == ControlType.Button && HasUsableBounds(actionBounds) &&
                    FullyInside(actionBounds, viewportBounds))
                    visibleActions.Add((actionNumber, automationId));
            }
        }

        if (profileNames.Count != profileActions.Count || profileNames.Keys.Any(index => !profileActions.ContainsKey(index)))
            throw new InvalidOperationException("Rendered profile descriptions and actions did not have matching identifiers");
        var profileRows = profileOrder.Select(index => new Dictionary<string, object?>
        {
            ["index"] = index,
            ["name"] = profileNames[index].Name,
            ["protocol"] = profileNames[index].Protocol,
            ["action"] = profileActions[index],
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
            ["profile_rows"] = profileRows,
            ["visible_profile_actions"] = visibleActions
                .OrderBy(action => action.Number)
                .Select(action => action.AutomationId)
                .ToArray(),
        };
    }

    private static Dictionary<string, object?> ScrollProfileList(
        AutomationElement root,
        IntPtr window,
        string position)
    {
        double targetPosition;
        if (position == "top") targetPosition = 0;
        else if (position == "bottom") targetPosition = 100;
        else if (!double.TryParse(position, NumberStyles.Float, CultureInfo.InvariantCulture, out targetPosition) ||
                 !double.IsFinite(targetPosition) || targetPosition < 0 || targetPosition > 100)
            throw new ArgumentException("Profile list position must be top, bottom, or a finite percentage from 0 to 100");

        var viewport = RequireAutomationId(root, "Profile list viewport");
        if (!viewport.TryGetCurrentPattern(ScrollPattern.Pattern, out var scrollPattern))
            throw new InvalidOperationException("Profile list viewport does not expose ScrollPattern");
        var scroll = (ScrollPattern)scrollPattern;
        if (scroll.Current.VerticalScrollPercent < 0)
            throw new InvalidOperationException("Profile list viewport does not expose a vertical scroll range");

        scroll.SetScrollPercent(ScrollPattern.NoScroll, targetPosition);
        WaitFor(() =>
        {
            var actualPosition = scroll.Current.VerticalScrollPercent;
            return position == "top" ? actualPosition <= 1 : position == "bottom"
                ? actualPosition >= 99 : Math.Abs(actualPosition - targetPosition) <= 1;
        }, $"Profile list viewport did not scroll to {position}", seconds: 3);

        var layout = ProfileListLayout(root, window);
        if (layout["scroll_position"] is not double measuredPosition)
            throw new InvalidOperationException("Profile list viewport did not report its scroll position after scrolling");
        var settled = position == "top" ? measuredPosition <= 1 : position == "bottom"
            ? measuredPosition >= 99 : Math.Abs(measuredPosition - targetPosition) <= 1;
        if (!settled)
            throw new InvalidOperationException(
                $"Profile list viewport did not remain at {position}; position={measuredPosition.ToString(CultureInfo.InvariantCulture)}");
        return layout;
    }

    private static void TracePhase(string name)
    {
        Console.Error.WriteLine($"native-ui-phase={name}");
        Console.Error.Flush();
    }

    private static string UtcTimestamp() =>
        DateTimeOffset.UtcNow.ToString("O", CultureInfo.InvariantCulture);

    private static string ElapsedMilliseconds(long started) =>
        Stopwatch.GetElapsedTime(started).TotalMilliseconds.ToString("F3", CultureInfo.InvariantCulture);

    private static string FormatHresult(int hresult) =>
        $"0x{unchecked((uint)hresult):X8}";

    private static void ReleaseComObject(
        object? value,
        string responseKey,
        Dictionary<string, object?> response)
    {
        if (value is null || !Marshal.IsComObject(value)) return;
        try
        {
            response[responseKey] = Marshal.ReleaseComObject(value);
        }
        catch (Exception error)
        {
            response[$"{responseKey}Exception"] = error.ToString();
        }
    }

    private static void CaptureComPointTargetMetadata(
        IUIAutomationElementCom target,
        JsonElement request,
        Dictionary<string, object?> response)
    {
        var propertiesStarted = Stopwatch.GetTimestamp();
        var hresults = new Dictionary<string, string>();
        var errors = new Dictionary<string, string>();
        object? Read(int propertyId, string propertyName)
        {
            try
            {
                var hresult = target.GetCurrentPropertyValue(propertyId, out var value);
                hresults[propertyName] = FormatHresult(hresult);
                if (hresult < 0)
                    errors[propertyName] = new COMException(
                        $"GetCurrentPropertyValue failed for {propertyName} ({propertyId})", hresult).ToString();
                return hresult < 0 ? null : value;
            }
            catch (Exception error)
            {
                errors[propertyName] = error.ToString();
                return null;
            }
        }

        try
        {
            var automationIdValue = Read(UiaAutomationIdPropertyId, "automationId");
            var nameValue = Read(UiaNamePropertyId, "name");
            var controlTypeValue = Read(UiaControlTypePropertyId, "controlType");
            var processIdValue = Read(UiaProcessIdPropertyId, "processId");
            var boundsValue = Read(UiaBoundingRectanglePropertyId, "bounds");
            var offscreenValue = Read(UiaIsOffscreenPropertyId, "isOffscreen");
            response["targetPropertyHresults"] = hresults;
            if (errors.Count > 0)
            {
                response["targetPropertyExceptions"] = errors;
                response["targetMetadataException"] = string.Join(Environment.NewLine, errors.Values);
                return;
            }

            var automationId = automationIdValue as string ?? string.Empty;
            var name = nameValue as string ?? string.Empty;
            var controlTypeId = Convert.ToInt32(controlTypeValue, CultureInfo.InvariantCulture);
            var controlTypeInformation = ControlType.LookupById(controlTypeId);
            var controlTypeProgrammaticName = controlTypeInformation?.ProgrammaticName
                ?? $"ControlType.{controlTypeId.ToString(CultureInfo.InvariantCulture)}";
            var controlType = controlTypeProgrammaticName.StartsWith("ControlType.", StringComparison.Ordinal)
                ? controlTypeProgrammaticName["ControlType.".Length..]
                : controlTypeProgrammaticName;
            var processId = Convert.ToInt32(processIdValue, CultureInfo.InvariantCulture);
            var isOffscreen = Convert.ToBoolean(offscreenValue, CultureInfo.InvariantCulture);
            System.Windows.Rect? bounds = null;
            if (boundsValue is Array coordinates && coordinates.Rank == 1 && coordinates.Length == 4)
            {
                bounds = new System.Windows.Rect(
                    Convert.ToDouble(coordinates.GetValue(0), CultureInfo.InvariantCulture),
                    Convert.ToDouble(coordinates.GetValue(1), CultureInfo.InvariantCulture),
                    Convert.ToDouble(coordinates.GetValue(2), CultureInfo.InvariantCulture),
                    Convert.ToDouble(coordinates.GetValue(3), CultureInfo.InvariantCulture));
            }
            else if (boundsValue is not null)
            {
                throw new InvalidOperationException("UIA bounding rectangle was not four screen coordinates");
            }
            var expectedAutomationId = request.GetProperty("expectedAutomationId").GetString();
            var expectedName = request.GetProperty("expectedName").GetString();
            var expectedControlType = request.GetProperty("expectedControlType").GetString();
            var expectedProcessId = request.GetProperty("expectedProcessId").GetInt32();
            var matchesExpected = new
            {
                automationId = automationId == expectedAutomationId,
                name = name == expectedName,
                controlType = controlType == expectedControlType,
                processId = processId == expectedProcessId,
                all = automationId == expectedAutomationId && name == expectedName &&
                      controlType == expectedControlType && processId == expectedProcessId,
            };
            response["target"] = new
            {
                automationId,
                name,
                controlType,
                controlTypeId,
                controlTypeProgrammaticName,
                processId,
                bounds = bounds is null ? null : RectJson(bounds.Value),
                boundsCoordinateSpace = "physical-screen-pixels",
                isOffscreen,
            };
            response["expected"] = new
            {
                automationId = expectedAutomationId,
                name = expectedName,
                controlType = expectedControlType,
                processId = expectedProcessId,
            };
            response["matchesExpected"] = matchesExpected;
            response["targetMetadataCompleted"] = true;
        }
        catch (Exception error)
        {
            response["targetPropertyHresults"] = hresults;
            response["targetMetadataException"] = error.ToString();
        }
        finally
        {
            response["targetMetadataDurationMs"] = Stopwatch.GetElapsedTime(propertiesStarted).TotalMilliseconds;
        }
    }

    private static IntPtr ParseWindowHandle(string value)
    {
        var digits = value.StartsWith("0x", StringComparison.OrdinalIgnoreCase) ? value[2..] : value;
        return new IntPtr(unchecked((long)ulong.Parse(digits, NumberStyles.HexNumber, CultureInfo.InvariantCulture)));
    }

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

    private static Process? FindSettingsProcessForSession(int sessionId)
    {
        Process? selected = null;
        foreach (var process in Process.GetProcessesByName("SystemSettings"))
        {
            if (process.SessionId != sessionId) { process.Dispose(); continue; }
            if (selected is not null)
            {
                process.Dispose();
                selected.Dispose();
                throw new InvalidOperationException($"Ambiguous SystemSettings processes in helper session {sessionId}");
            }
            selected = process;
        }
        return selected;
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
        int expectedProcessId,
        long expectedProcessStartTicks,
        int expectedSessionId,
        bool closeMessagePosted)
    {
        var snapshot = new Dictionary<string, object?>
        {
            ["capturedAtUtc"] = UtcTimestamp(),
            ["windowHandle"] = $"0x{window.ToInt64():X}",
            ["closeMessagePosted"] = closeMessagePosted,
            ["expectedProcessId"] = expectedProcessId,
            ["expectedProcessStartUtcTicks"] = expectedProcessStartTicks.ToString(CultureInfo.InvariantCulture),
            ["expectedSessionId"] = expectedSessionId,
        };
        var isWindow = IsWindow(window);
        var windowContext = DescribeWindowContext(window, includeThreadDesktop: false, includeGeometry: false);
        windowContext["isWindow"] = isWindow;
        windowContext["isWindowVisible"] = isWindow && IsWindowVisible(window);
        snapshot["window"] = windowContext;
        var rootOwner = isWindow ? GetAncestor(window, GaRootOwner) : IntPtr.Zero;
        var rootOwnerContext = DescribeWindowContext(
            rootOwner, includeThreadDesktop: false, includeGeometry: false);
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
        Dictionary<string, object?> response)
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

        WaitFor(() => FindSettingsTextSizeControl(
            root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply")?.Current.IsEnabled == true,
            "Settings Apply did not enable after changing Text size", seconds: 5);
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

        WaitFor(() =>
        {
            var currentSlider = FindSettingsTextSizeControl(
                root, WindowsTextSizeSliderAutomationId, ControlType.Slider, "Text size");
            var currentApply = FindSettingsTextSizeControl(
                root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply");
            return currentSlider is not null && currentApply is not null &&
                currentSlider.TryGetCurrentPattern(RangeValuePattern.Pattern, out var currentPattern) &&
                Math.Abs(((RangeValuePattern)currentPattern).Current.Value - target) <= 0.01 &&
                !currentApply.Current.IsEnabled;
        }, "Text size Apply did not settle at the requested value", seconds: 10);

        var finalSlider = FindSettingsTextSizeControl(
            root, WindowsTextSizeSliderAutomationId, ControlType.Slider, "Text size")
            ?? throw new InvalidOperationException("Text size Slider disappeared after Apply");
        var finalApply = FindSettingsTextSizeControl(
            root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply")
            ?? throw new InvalidOperationException("Text size Apply button disappeared after Apply");
        if (!finalSlider.TryGetCurrentPattern(RangeValuePattern.Pattern, out var finalPattern))
            throw new InvalidOperationException("Text size Slider lost RangeValuePattern after Apply");
        response["valueAfterApply"] = ((RangeValuePattern)finalPattern).Current.Value;
        response["applyEnabledAfterApply"] = finalApply.Current.IsEnabled;
        if (Math.Abs(Convert.ToDouble(response["valueAfterApply"], CultureInfo.InvariantCulture) - target) > 0.01 ||
            finalApply.Current.IsEnabled)
            throw new InvalidOperationException("Text size setting did not apply at the requested value");
    }

    private static int OperateWindowsTextSizeSettings(JsonElement request)
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
        IntPtr[] windowsBefore = Array.Empty<IntPtr>();
        IntPtr window = IntPtr.Zero;
        int ownerPid = 0;
        long ownerStart = 0;
        string? primaryError = null;
        var cleanupErrors = new List<string>();
        try
        {
            settings = FindSettingsProcessForSession(sessionId);
            windowsBefore = settings is null ? Array.Empty<IntPtr>() : EnumerateProcessWindows(settings);
            response["settingsBefore"] = settings is null ? null : new { pid = settings.Id, windows = DescribeProcessWindows(settings) };
            activationAttempted = true;
            activation = Process.Start(new ProcessStartInfo(WindowsTextSizeSettingsUri) { UseShellExecute = true });
            WaitFor(() =>
            {
                settings ??= FindSettingsProcessForSession(sessionId);
                if (settings is null) return false;
                var visible = EnumerateProcessWindows(settings).Where(candidate => IsWindowVisible(candidate) && !IsIconic(candidate)).ToArray();
                if (visible.Length > 1) throw new InvalidOperationException($"Ambiguous visible Settings windows in session {sessionId}");
                if (visible.Length == 1) { window = visible[0]; return true; }
                return false;
            }, "Settings did not expose one visible window in the helper session", seconds: 10);
            if (settings is null || window == IntPtr.Zero) throw new InvalidOperationException("Settings window unavailable after URI activation");
            ownerPid = settings.Id;
            ownerStart = settings.StartTime.ToUniversalTime().Ticks;
            var existedBefore = windowsBefore.Contains(window);
            response["settingsAfter"] = new { pid = ownerPid, windows = DescribeProcessWindows(settings) };
            response["window"] = new { hwnd = $"0x{window.ToInt64():X}", existedBefore, description = DescribeWindow(window) };
            if (!existedBefore) TracePhase($"settings-text-size-new-window-owned session={sessionId} pid={ownerPid} hwnd=0x{window.ToInt64():X}");

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
            if (action == "apply")
            {
                if (range is null || apply is null)
                    throw new InvalidOperationException("Text size apply requires RangeValue data and the exact Apply control");
                ApplyWindowsTextSize(root, slider, apply, target!.Value, expectedCurrent!.Value, response);
                slider = FindSettingsTextSizeControl(
                    root, WindowsTextSizeSliderAutomationId, ControlType.Slider, "Text size")
                    ?? throw new InvalidOperationException("Text size Slider disappeared after Apply");
                current = slider.Current;
                if (slider.TryGetCurrentPattern(RangeValuePattern.Pattern, out var refreshedPattern))
                {
                    var refreshed = ((RangeValuePattern)refreshedPattern).Current;
                    range = new { value = refreshed.Value, minimum = refreshed.Minimum, maximum = refreshed.Maximum,
                        smallChange = refreshed.SmallChange, largeChange = refreshed.LargeChange, isReadOnly = refreshed.IsReadOnly };
                }
                apply = FindSettingsTextSizeControl(
                    root, WindowsTextSizeApplyAutomationId, ControlType.Button, "Apply");
                bounds = current.BoundingRectangle;
                response["slider"] = new { identity = DescribeElement(slider), enabled = current.IsEnabled,
                    offscreen = current.IsOffscreen, bounds = HasUsableBounds(bounds) ? RectJson(bounds) : null, range, rangeError };
                response["apply"] = apply is null ? null : new { identity = DescribeElement(apply), enabled = apply.Current.IsEnabled,
                    offscreen = apply.Current.IsOffscreen, bounds = HasUsableBounds(apply.Current.BoundingRectangle) ? RectJson(apply.Current.BoundingRectangle) : null };
            }
            var ready = !current.IsOffscreen && HasUsableBounds(bounds) && range is not null && apply is not null;
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
            if (window != IntPtr.Zero && !windowsBefore.Contains(window))
            {
                var closeMessagePosted = false;
                try
                {
                    GetWindowThreadProcessId(window, out var actualPid);
                    if (actualPid == 0) response["newSettingsWindowClosed"] = true;
                    else
                    {
                        var actualProcessId = checked((int)actualPid);
                        using var owner = Process.GetProcessById(actualProcessId);
                        if (actualProcessId != ownerPid || owner.ProcessName != "SystemSettings" || owner.SessionId != sessionId ||
                            owner.StartTime.ToUniversalTime().Ticks != ownerStart)
                            throw new InvalidOperationException("Refusing to close a Settings window whose ownership changed");
                        if (!PostMessage(window, WmClose, IntPtr.Zero, IntPtr.Zero))
                            throw new System.ComponentModel.Win32Exception(Marshal.GetLastWin32Error(), "Could not close new Settings window");
                        closeMessagePosted = true;
                        WaitFor(() => { GetWindowThreadProcessId(window, out var pid); return pid == 0 || pid != ownerPid; }, "New Settings window did not close", seconds: 5);
                        response["newSettingsWindowClosed"] = true;
                    }
                }
                catch (Exception error) { cleanupErrors.Add(error.ToString()); }
                finally
                {
                    try
                    {
                        response["postCloseWindowSnapshot"] = CaptureSettingsWindowAfterClose(
                            window, ownerPid, ownerStart, sessionId, closeMessagePosted);
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
                    "Settings activation exposed no SystemSettings-owned window; cleanup of a potentially new Settings window cannot be verified");
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
        Console.WriteLine(JsonSerializer.Serialize(response));
        return 0;
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
            RequireForeground(window, "narrow-window screenshot");
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

    private static NativeInput CreateMouseInput(uint flags) => new()
    {
        Type = InputMouse,
        Union = new NativeInputUnion
        {
            Mouse = new NativeMouseInput { Flags = flags, ExtraInfo = UIntPtr.Zero },
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
            ["target"] = DescribeWindowContext(window, includeThreadDesktop: false, includeGeometry: true),
            ["foregroundBefore"] = DescribeWindowContext(
                foregroundBeforeHandle, includeThreadDesktop: false, includeGeometry: true),
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
                foregroundAfterRequestHandle, includeThreadDesktop: false, includeGeometry: true);

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
                foregroundAfterHandle, includeThreadDesktop: false, includeGeometry: true);
            if (!IsWindow(window) || !IsWindowVisible(window) || IsIconic(window) || foregroundAfterHandle != window)
                throw new InvalidOperationException("Candidate UI window did not become the actual foreground window");
            measurement["ready"] = true;
        }
        catch (Exception error)
        {
            measurement["foregroundAtFailure"] = DescribeWindowContext(
                GetForegroundWindow(), includeThreadDesktop: false, includeGeometry: true);
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
            var operation = Text("operation");
            if (operation == "settings-text-size")
            {
                ValidateSettingsTextSizeRequest(request);
                return OperateWindowsTextSizeSettings(request);
            }
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
            var traceAutomationPoint = operation == "uia-point";
            var traceWindow = traceTree || traceWin32Baseline;
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
                if (traceAutomationPoint)
                {
                    window = ParseWindowHandle(request.GetProperty("windowHandle").GetString()
                        ?? throw new InvalidOperationException("UIA point query requires the baseline window handle"));
                    GetWindowThreadProcessId(window, out var requestedOwnerPid);
                    if (window == IntPtr.Zero || requestedOwnerPid != process.Id)
                        throw new InvalidOperationException("UIA point query window does not belong to the verified UI process");
                }
                else
                {
                    window = process.MainWindowHandle;
                }
            }
            if (traceTree) TracePhase($"tree-window-discovery-complete hwnd=0x{window.ToInt64():X}");
            if (traceWin32Baseline)
                TracePhase($"configure-tree-win32-baseline-window-discovery-complete utc={UtcTimestamp()} hwnd=0x{window.ToInt64():X}");
            var visible = window != IntPtr.Zero && IsWindowVisible(window);
            var minimized = window != IntPtr.Zero && IsIconic(window);
            if ((!visible || minimized) && !traceAutomationPoint)
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
            if (traceAutomationPoint)
            {
                var pointQueryResult = 0;
                ExceptionDispatchInfo? pointQueryError = null;
                var pointQueryThread = new Thread(() =>
                {
                    try
                    {
                        pointQueryResult = MeasureAutomationElementFromPoint(process, identity, window, request);
                    }
                    catch (Exception error)
                    {
                        pointQueryError = ExceptionDispatchInfo.Capture(error);
                    }
                });
                pointQueryThread.SetApartmentState(ApartmentState.MTA);
                pointQueryThread.Start();
                pointQueryThread.Join();
                pointQueryError?.Throw();
                return pointQueryResult;
            }
            if (traceTree) TracePhase($"tree-uia-root-start hwnd=0x{window.ToInt64():X}");
            var root = AutomationElement.FromHandle(window);
            if (traceTree) TracePhase("tree-uia-root-complete");
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
            if (operation == "cancel-profile-switch")
            {
                var observeOnly = request.TryGetProperty("observe_only", out var observeOnlyValue) &&
                    observeOnlyValue.GetBoolean();
                var result = ProfileSwitchAction(
                    root, window, process, identity, Text("target"), Text("competing"), observeOnly: observeOnly);
                Console.WriteLine(JsonSerializer.Serialize(result));
                return 0;
            }
            if (operation == "profile-switch-import")
            {
                var result = ProfileSwitchAction(
                    root, window, process, identity, Text("target"), Text("competing"), Text("uri"),
                    request: request);
                Console.WriteLine(JsonSerializer.Serialize(result));
                return 0;
            }
            if (operation == "profile-list-layout")
            {
                Console.WriteLine(JsonSerializer.Serialize(ProfileListLayout(root, window)));
                return 0;
            }
            if (operation == "scroll-profile-list")
            {
                Console.WriteLine(JsonSerializer.Serialize(ScrollProfileList(root, window, Text("position"))));
                return 0;
            }
            if (operation == "logs")
            {
                var logRoot = Find("Backend logs");
                var verifyDetails = request.TryGetProperty("verify_details", out var verifyDetailsValue) &&
                    verifyDetailsValue.GetBoolean();
                var paletteMarker = request.TryGetProperty("marker", out var markerValue)
                    && markerValue.ValueKind == JsonValueKind.String
                        ? markerValue.GetString() : null;
                System.Windows.Rect? logViewport = string.IsNullOrEmpty(paletteMarker)
                    ? null : PhysicalBounds(logRoot, "Backend logs");
                var entries = Walk(logRoot)
                    .Where(element => element.Current.ControlType == ControlType.Text)
                    .Select(element =>
                    {
                        var text = element.Current.Name;
                        int? foreground = null;
                        if (!string.IsNullOrEmpty(paletteMarker) &&
                            text.Contains(paletteMarker, StringComparison.Ordinal) &&
                            element.TryGetCurrentPattern(TextPattern.Pattern, out var pattern))
                        {
                            var value = ((TextPattern)pattern).DocumentRange.GetAttributeValue(TextPattern.ForegroundColorAttribute);
                            if (value is int color) foreground = color;
                        }
                        bool? offscreen = null;
                        bool? visibleInViewport = null;
                        object? bounds = null;
                        if (logViewport is not null && text.Contains(paletteMarker!, StringComparison.Ordinal))
                        {
                            var elementBounds = element.Current.BoundingRectangle;
                            offscreen = element.Current.IsOffscreen;
                            if (HasUsableBounds(elementBounds))
                            {
                                bounds = RectJson(elementBounds);
                                visibleInViewport = !offscreen.Value && FullyInside(elementBounds, logViewport.Value);
                            }
                            else
                            {
                                visibleInViewport = false;
                            }
                        }
                        return new { text, foreground, offscreen, bounds, visible_in_viewport = visibleInViewport };
                    })
                    .Where(entry => entry.text.Length > 0)
                    .ToArray();
                string expandedRecord = "";
                var expansionVerified = false;
                if (verifyDetails)
                {
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
                }
                Console.WriteLine(JsonSerializer.Serialize(new {
                    ready = true,
                    text = string.Join("\n", entries.Select(entry => entry.text)),
                    entries,
                    logs_viewport = logViewport is null ? null : RectJson(logViewport.Value),
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
                    .Select(element => element.Current.Name)
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
                RequireForeground(window, "log scrolling");
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
                string? source_text = null;
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

                    var sourceEditor = visibleControls.FirstOrDefault(element =>
                        element.Current.AutomationId == "Connection configuration");
                    if (sourceEditor is not null &&
                        sourceEditor.TryGetCurrentPattern(ValuePattern.Pattern, out var sourceValue))
                        source_text = ((ValuePattern)sourceValue).Current.Value;

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
                        ready = false, alive = true, pid = process.Id, identity, source_text
                    }));
                    return 0;
                }
                catch (COMException error) when (error.HResult == unchecked((int)0x8000FFFF))
                {
                    Console.WriteLine(JsonSerializer.Serialize(new {
                        ready = false, alive = true, pid = process.Id, identity,
                        uiaError = error.ToString(), source_text
                    }));
                    return 0;
                }
                Console.WriteLine(JsonSerializer.Serialize(new {
                    ready = true, pid = process.Id, identity, labels, enabled_controls, help_texts, source_text
                }));
                return 0;
            }
            long? pasteInvokedAtUnixMs = null;
            Rectangle? capturedScreenBounds = null;
            switch (operation)
            {
                case "focus":
                {
                    RequireForeground(window, "initial focus");
                    break;
                }
                case "click":
                    var element = Find(Text("target"), actionable: true);
                    RequireForeground(window, "UI Automation click");
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
                    RequireForeground(window, "typing");
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
                    var expectedPasteValue = pasteValue;
                    var pasteMismatchMessage = "Native Paste did not place clipboard text in the subscription field";
                    if (request.TryGetProperty("expectedSource", out var expectedSource))
                    {
                        if (expectedSource.ValueKind != JsonValueKind.String)
                            throw new ArgumentException("Expected Paste source must be a string");
                        expectedPasteValue = expectedSource.GetString()!;
                        pasteMismatchMessage = "Native Paste did not preserve the expected subscription field value";
                    }
                    var fieldBeforeClipboard = ((ValuePattern)pasteEditor.GetCurrentPattern(ValuePattern.Pattern)).Current.Value;
                    TracePhase("paste-activate-window");
                    RequireForeground(window, "Paste");
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
                            if (NormalizeLineEndings(pasteObserved).Trim() == NormalizeLineEndings(expectedPasteValue)) break;
                            Thread.Sleep(50);
                        } while (valueWait.Elapsed.TotalSeconds < 5);
                        if (NormalizeLineEndings(pasteObserved).Trim() != NormalizeLineEndings(expectedPasteValue))
                            throw new InvalidOperationException(pasteMismatchMessage);

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
                    capturedScreenBounds = Capture(window, process.Id, Text("path"));
                    break;
                default: throw new ArgumentException($"Unknown operation: {operation}");
            }
            var response = new Dictionary<string, object?>
            {
                ["ready"] = true,
                ["pid"] = process.Id,
                ["identity"] = identity,
                ["labels"] = Array.Empty<string>(),
                ["paste_invoked_at_unix_ms"] = pasteInvokedAtUnixMs,
            };
            if (capturedScreenBounds is { } capturedBounds)
                response["screen_bounds"] = new {
                    x = capturedBounds.X, y = capturedBounds.Y,
                    width = capturedBounds.Width, height = capturedBounds.Height,
                };
            Console.WriteLine(JsonSerializer.Serialize(response));
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

    private static int MeasureAutomationElementFromPoint(
        Process process,
        string identity,
        IntPtr window,
        JsonElement request)
    {
        var response = new Dictionary<string, object?>
        {
            ["completed"] = false,
            ["ready"] = false,
            ["pid"] = process.Id,
            ["identity"] = identity,
            ["diagnosticOnly"] = true,
            ["schema"] = "dobbyvpn.windows-uia-point/v1",
            ["clientApartmentState"] = Thread.CurrentThread.GetApartmentState().ToString(),
        };
        object? comAutomationObject = null;
        IUIAutomationClientCom? comAutomation = null;
        IUIAutomationElementCom? comTarget = null;
        var comPointSucceeded = false;
        var operationStarted = Stopwatch.GetTimestamp();
        try
        {
            var clientApi = request.TryGetProperty("clientApi", out var clientApiElement)
                ? clientApiElement.GetString()
                : "managed";
            response["clientApi"] = clientApi;
            if (clientApi is not ("managed" or "com"))
                throw new ArgumentException("UIA point clientApi must be managed or com");
            if (clientApi == "managed") response["maximumAncestors"] = 8;
            response["fromPointMethod"] = clientApi == "com"
                ? "IUIAutomation::ElementFromPoint"
                : "System.Windows.Automation.AutomationElement.FromPoint";

            var requestedX = request.GetProperty("x").GetInt32();
            var requestedY = request.GetProperty("y").GetInt32();
            var expectedClientX = request.GetProperty("clientOriginX").GetInt32();
            var expectedClientY = request.GetProperty("clientOriginY").GetInt32();
            response["requestedPoint"] = new { x = requestedX, y = requestedY };

            var activationStarted = Stopwatch.GetTimestamp();
            try
            {
                var restoreQueued = ShowWindowAsync(window, 9); // SW_RESTORE
                var foregroundRequested = SetForegroundWindow(window);
                WaitFor(() => IsWindowVisible(window) && !IsIconic(window),
                    "UI window did not restore before the UIA point query", seconds: 3);
                var focusWait = Stopwatch.StartNew();
                while (GetForegroundWindow() != window && focusWait.Elapsed.TotalSeconds < 3)
                    Thread.Sleep(25);
                response["windowActivation"] = new
                {
                    restoreQueued,
                    foregroundRequested,
                    foregroundConfirmed = GetForegroundWindow() == window,
                    visible = IsWindowVisible(window),
                    minimized = IsIconic(window),
                };
            }
            catch (Exception error)
            {
                response["windowActivationException"] = error.ToString();
            }
            response["windowActivationDurationMs"] = Stopwatch.GetElapsedTime(activationStarted).TotalMilliseconds;

            var pointX = requestedX;
            var pointY = requestedY;
            NativePoint currentClientOrigin;
            try
            {
                currentClientOrigin = GetPhysicalClientOrigin(window);
                pointX = checked(requestedX + currentClientOrigin.X - expectedClientX);
                pointY = checked(requestedY + currentClientOrigin.Y - expectedClientY);
                response["currentClientOrigin"] = new { x = currentClientOrigin.X, y = currentClientOrigin.Y };
            }
            catch (Exception error)
            {
                response["clientOriginException"] = error.ToString();
            }
            response["point"] = new
            {
                x = pointX,
                y = pointY,
                coordinateSpace = "physical-screen-pixels",
                adjustedForWindowMove = pointX != requestedX || pointY != requestedY,
            };

            if (clientApi == "com")
            {
                var comClientStarted = Stopwatch.GetTimestamp();
                try
                {
                    var automationType = Type.GetTypeFromCLSID(CUIAutomationClassId, throwOnError: true)
                        ?? throw new InvalidOperationException("CUIAutomation COM class was not registered");
                    comAutomationObject = Activator.CreateInstance(automationType)
                        ?? throw new InvalidOperationException("CUIAutomation COM activation returned null");
                    comAutomation = comAutomationObject as IUIAutomationClientCom
                        ?? throw new InvalidCastException("CUIAutomation did not expose IUIAutomation");
                    response["comClientInitializationCompleted"] = true;
                }
                catch (Exception error)
                {
                    response["comClientInitializationException"] = error.ToString();
                }
                response["comClientInitializationDurationMs"] =
                    Stopwatch.GetElapsedTime(comClientStarted).TotalMilliseconds;
            }

            var pointContextStarted = Stopwatch.GetTimestamp();
            try
            {
                response["pointContextBeforeFromPoint"] =
                    CapturePointContextBeforeFromPoint(window, pointX, pointY);
            }
            catch (Exception error)
            {
                response["pointContextSnapshotException"] = error.ToString();
            }
            response["pointContextSnapshotDurationMs"] =
                Stopwatch.GetElapsedTime(pointContextStarted).TotalMilliseconds;

            AutomationElement? target = null;
            if (clientApi == "com" && comAutomation is null)
            {
                response["fromPointAttempted"] = false;
                response["fromPointSkipped"] = "CUIAutomation initialization failed; no point query was attempted";
            }
            else if (clientApi == "com")
            {
                response["windowMessageBeforeFromPoint"] =
                    ProbeWindowMessageResponsiveness(window, process.Id, identity);
                var fromPointStarted = Stopwatch.GetTimestamp();
                response["fromPointAttempted"] = true;
                int? hresult = null;
                IUIAutomationElementCom? foundTarget = null;
                try
                {
                    hresult = comAutomation!.ElementFromPoint(
                        new NativePoint { X = pointX, Y = pointY }, out foundTarget);
                }
                catch (Exception error)
                {
                    response["fromPointException"] = error.ToString();
                }
                finally
                {
                    response["fromPointDurationMs"] = Stopwatch.GetElapsedTime(fromPointStarted).TotalMilliseconds;
                    response["windowMessageAfterFromPoint"] =
                        ProbeWindowMessageResponsiveness(window, process.Id, identity);
                }
                comTarget = foundTarget;
                if (hresult is int result)
                {
                    response["fromPointHresult"] = FormatHresult(result);
                    response["fromPointCompleted"] = result >= 0;
                    response["targetFound"] = result >= 0 && comTarget is not null;
                    comPointSucceeded = result >= 0;
                    if (result < 0)
                        response["fromPointException"] = new COMException(
                            "IUIAutomation::ElementFromPoint failed", result).ToString();
                }
                var failedHresult = hresult.HasValue && hresult.Value < 0;
                if (failedHresult || response.ContainsKey("fromPointException"))
                {
                    var failure = failedHresult
                        ? $"IUIAutomation::ElementFromPoint returned {FormatHresult(hresult!.Value)}"
                        : "IUIAutomation::ElementFromPoint threw an exception";
                    response["failureDump"] = CapturePointFailureDump(process, identity, request, failure);
                }
            }
            else
            {
                var fromPointStarted = Stopwatch.GetTimestamp();
                response["fromPointAttempted"] = true;
                try
                {
                    target = AutomationElement.FromPoint(new System.Windows.Point(pointX, pointY));
                    response["fromPointCompleted"] = true;
                    response["targetFound"] = target is not null;
                }
                catch (Exception error)
                {
                    response["fromPointException"] = error.ToString();
                }
                response["fromPointDurationMs"] = Stopwatch.GetElapsedTime(fromPointStarted).TotalMilliseconds;
            }

            if (clientApi == "com" && comPointSucceeded && comTarget is not null)
            {
                response["targetMetadataClientApi"] = "com";
                CaptureComPointTargetMetadata(comTarget, request, response);
            }
            else if (clientApi == "managed" && target is not null)
            {
                var propertiesStarted = Stopwatch.GetTimestamp();
                try
                {
                    var current = target.Current;
                    var controlTypeProgrammaticName = current.ControlType.ProgrammaticName;
                    var controlType = controlTypeProgrammaticName.StartsWith("ControlType.", StringComparison.Ordinal)
                        ? controlTypeProgrammaticName["ControlType.".Length..]
                        : controlTypeProgrammaticName;
                    var automationId = current.AutomationId;
                    var name = current.Name;
                    var processId = current.ProcessId;
                    var expectedAutomationId = request.GetProperty("expectedAutomationId").GetString();
                    var expectedName = request.GetProperty("expectedName").GetString();
                    var expectedControlType = request.GetProperty("expectedControlType").GetString();
                    var expectedProcessId = request.GetProperty("expectedProcessId").GetInt32();
                    var matchesExpected = new
                    {
                        automationId = automationId == expectedAutomationId,
                        name = name == expectedName,
                        controlType = controlType == expectedControlType,
                        processId = processId == expectedProcessId,
                        all = automationId == expectedAutomationId && name == expectedName &&
                              controlType == expectedControlType && processId == expectedProcessId,
                    };
                    response["target"] = new
                    {
                        automationId,
                        name,
                        controlType,
                        controlTypeProgrammaticName,
                        processId,
                        isControlElement = current.IsControlElement,
                        isContentElement = current.IsContentElement,
                        isOffscreen = current.IsOffscreen,
                    };
                    response["expected"] = new
                    {
                        automationId = expectedAutomationId,
                        name = expectedName,
                        controlType = expectedControlType,
                        processId = expectedProcessId,
                    };
                    response["matchesExpected"] = matchesExpected;

                    var ancestors = new List<object>();
                    var ancestorStarted = Stopwatch.GetTimestamp();
                    try
                    {
                        var walker = TreeWalker.ControlViewWalker;
                        var ancestor = walker.GetParent(target);
                        for (var depth = 1; ancestor is not null && depth <= 8; depth++)
                        {
                            var properties = ancestor.Current;
                            ancestors.Add(new
                            {
                                depth,
                                automationId = properties.AutomationId,
                                name = properties.Name,
                                controlType = properties.ControlType.ProgrammaticName,
                                processId = properties.ProcessId,
                            });
                            ancestor = depth == 8 ? null : walker.GetParent(ancestor);
                        }
                    }
                    catch (Exception error)
                    {
                        response["ancestorException"] = error.ToString();
                    }
                    response["ancestors"] = ancestors;
                    response["ancestorDurationMs"] = Stopwatch.GetElapsedTime(ancestorStarted).TotalMilliseconds;
                    response["targetMetadataCompleted"] = true;
                }
                catch (Exception error)
                {
                    response["targetMetadataException"] = error.ToString();
                }
                response["targetMetadataDurationMs"] = Stopwatch.GetElapsedTime(propertiesStarted).TotalMilliseconds;
            }
        }
        catch (Exception error)
        {
            response["measurementException"] = error.ToString();
        }
        finally
        {
            ReleaseComObject(comTarget, "comTargetReleaseRemainingReferences", response);
            ReleaseComObject(comAutomationObject, "comClientReleaseRemainingReferences", response);
            response["durationMs"] = Stopwatch.GetElapsedTime(operationStarted).TotalMilliseconds;
            try
            {
                process.Refresh();
                response["processAliveAfterQuery"] = !process.HasExited;
            }
            catch (Exception error)
            {
                response["processLivenessException"] = error.ToString();
            }
        }

        response["completed"] = response.ContainsKey("fromPointDurationMs");
        response["ready"] = response.ContainsKey("targetMetadataCompleted");
        Console.WriteLine(JsonSerializer.Serialize(response));
        return 0;
    }

    private static Rectangle Capture(IntPtr window, int expectedPid, string path) =>
        InPerMonitorV2DpiContext(() => CapturePhysical(window, expectedPid, path));

    private static Rectangle CapturePhysical(IntPtr window, int expectedPid, string path)
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
        return target;
    }
}
