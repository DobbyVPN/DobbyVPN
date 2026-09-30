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
using System.Text.Json;
using System.Threading;
using System.Windows.Automation;
using Forms = System.Windows.Forms;

// Runs only in the interactive test user's session, outside the shipped app.
internal static class Program
{
    [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] private static extern bool GetWindowRect(IntPtr window, out Rect rect);
    [DllImport("user32.dll")] private static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);
    [DllImport("user32.dll")] private static extern IntPtr GetWindow(IntPtr window, uint command);
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetClassNameW")]
    private static extern int GetClassName(IntPtr window, StringBuilder className, int maxCount);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetWindowTextLengthW")]
    private static extern int GetWindowTextLength(IntPtr window);
    [DllImport("user32.dll", CharSet = CharSet.Unicode, EntryPoint = "GetWindowTextW")]
    private static extern int GetWindowText(IntPtr window, StringBuilder text, int maxCount);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool SetCursorPos(int x, int y);
    [DllImport("user32.dll")] private static extern bool SetProcessDpiAwarenessContext(IntPtr context);
    [StructLayout(LayoutKind.Sequential)] private struct Rect { public int Left, Top, Right, Bottom; }

    private static void TracePhase(string name)
    {
        Console.Error.WriteLine($"native-ui-phase={name}");
        Console.Error.Flush();
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

    private static string DescribeElement(AutomationElement element)
    {
        var current = element.Current;
        var invoke = element.TryGetCurrentPattern(InvokePattern.Pattern, out _);
        var selection = element.TryGetCurrentPattern(SelectionItemPattern.Pattern, out _);
        var value = element.TryGetCurrentPattern(ValuePattern.Pattern, out _);
        return $"type={current.ControlType.ProgrammaticName} id=\"{current.AutomationId}\" " +
               $"name=\"{current.Name}\" patterns[invoke={invoke},selectionItem={selection},value={value}]";
    }

    private static void PrepareCaptureCursor(Rectangle target)
    {
        // Moving to the inert title bar dismisses tooltips from hovered controls.
        var destination = new Point(target.Left + target.Width / 2,
            target.Top + Math.Max(1, Forms.SystemInformation.CaptionHeight / 2));
        if (!SetCursorPos(destination.X, destination.Y))
            throw new InvalidOperationException("Could not move pointer before screenshot");
        Thread.Sleep(200);
    }

    [STAThread]
    private static int Main()
    {
        try
        {
            SetProcessDpiAwarenessContext(new IntPtr(-4));
            using var input = JsonDocument.Parse(Console.In.ReadToEnd());
            var request = input.RootElement;
            string Text(string key) => request.GetProperty(key).GetString()!;
            Process found;
            try { found = Process.GetProcessById(request.GetProperty("pid").GetInt32()); }
            catch (ArgumentException) { Console.WriteLine("{\"ready\":false,\"alive\":false}"); return 0; }
            using var process = found;
            if (process.HasExited)
            {
                Console.WriteLine("{\"ready\":false,\"alive\":false}");
                return 0;
            }
            var expected = Path.GetFullPath(Text("executable"));
            if (!string.Equals(process.MainModule!.FileName, expected, StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("UI process executable changed");
            var identity = process.StartTime.ToUniversalTime().Ticks.ToString(CultureInfo.InvariantCulture);
            if (request.TryGetProperty("identity", out var prior) && prior.GetString() != identity)
                throw new InvalidOperationException("UI process creation time changed");
            var operation = Text("operation");
            if (operation == "probe")
            {
                Console.WriteLine(JsonSerializer.Serialize(new { alive = true, pid = process.Id, identity }));
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
            process.Refresh();
            var window = process.MainWindowHandle;
            if (window == IntPtr.Zero || !IsWindowVisible(window) || IsIconic(window))
            {
                Console.WriteLine(JsonSerializer.Serialize(new { ready = false, pid = process.Id, identity }));
                return 0;
            }
            GetWindowThreadProcessId(window, out var owner);
            if (owner != process.Id) throw new InvalidOperationException("UI window ownership changed");
            var root = AutomationElement.FromHandle(window);
            AutomationElement Find(string name, bool editor = false, bool actionable = false)
            {
                AutomationElementCollection FindBy(AutomationProperty property)
                {
                    var conditions = new List<Condition>
                    {
                        new PropertyCondition(property, name),
                        new PropertyCondition(AutomationElement.IsOffscreenProperty, false),
                        // Keep targeted lookup within the same UIA control view as Walk.
                        new PropertyCondition(AutomationElement.IsControlElementProperty, true),
                    };
                    if (editor)
                        conditions.Add(new PropertyCondition(AutomationElement.ControlTypeProperty, ControlType.Edit));
                    if (actionable)
                    {
                        conditions.Add(new OrCondition(
                            new PropertyCondition(AutomationElement.IsInvokePatternAvailableProperty, true),
                            new PropertyCondition(AutomationElement.IsSelectionItemPatternAvailableProperty, true)
                        ));
                    }
                    return root.FindAll(TreeScope.Subtree, new AndCondition(conditions.ToArray()));
                }

                // Resolve stable identifiers before user-facing labels to avoid matching a tab and its label.
                var matches = FindBy(AutomationElement.AutomationIdProperty);
                if (matches.Count == 0) matches = FindBy(AutomationElement.NameProperty);
                if (matches.Count != 1)
                {
                    var details = string.Join("; ", matches.Cast<AutomationElement>().Select(DescribeElement));
                    throw new InvalidOperationException(
                        $"Expected one visible {name}, found {matches.Count}; matches=[{details}]"
                    );
                }
                var element = matches[0];
                if (!element.Current.IsEnabled) throw new InvalidOperationException($"Control disabled: {name}");
                return element;
            }
            if (operation == "tree")
            {
                string[] labels;
                try
                {
                    var elements = Walk(root).Where(e => !e.Current.IsOffscreen).ToList();
                    labels = elements.SelectMany(e => new[] { e.Current.AutomationId, e.Current.Name })
                        .Where(s => s.Length > 0).Distinct().ToArray();
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
                Console.WriteLine(JsonSerializer.Serialize(new {
                    ready = true, pid = process.Id, identity, labels
                }));
                return 0;
            }
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
                case "capture":
                    Capture(window, process.Id, Text("path"));
                    break;
                default: throw new ArgumentException($"Unknown operation: {operation}");
            }
            Console.WriteLine(JsonSerializer.Serialize(new {
                ready = true, pid = process.Id, identity,
                labels = Array.Empty<string>()
            }));
            return 0;
        }
        catch (Exception error) { Console.Error.WriteLine(error); return 1; }
    }

    private static IEnumerable<AutomationElement> Walk(AutomationElement root)
    {
        var queue = new Queue<AutomationElement>();
        queue.Enqueue(root);
        int count = 0;
        while (queue.Count > 0)
        {
            if (++count > 8192) throw new InvalidOperationException("Accessibility tree exceeds 8192 elements");
            var element = queue.Dequeue();
            yield return element;
            for (var child = TreeWalker.ControlViewWalker.GetFirstChild(element); child is not null;
                 child = TreeWalker.ControlViewWalker.GetNextSibling(child)) queue.Enqueue(child);
        }
    }

    private static void Capture(IntPtr window, int expectedPid, string path)
    {
        GetWindowThreadProcessId(window, out var owner);
        if (owner != expectedPid) throw new InvalidOperationException("UI window ownership changed before screenshot");
        if (!GetWindowRect(window, out var r) || r.Right - r.Left < 300 || r.Bottom - r.Top < 300)
            throw new InvalidOperationException("UI window bounds unavailable or too small");
        var target = Rectangle.FromLTRB(r.Left, r.Top, r.Right, r.Bottom);
        if (!Forms.Screen.AllScreens.Any(screen => screen.Bounds.Contains(target)))
            throw new InvalidOperationException("UI window is partly offscreen or crosses a display gap");
        PrepareCaptureCursor(target);
        void RequireUnobstructed()
        {
            for (var other = GetWindow(window, 3); other != IntPtr.Zero; other = GetWindow(other, 3))
            {
                if (!IsWindowVisible(other) || IsIconic(other) || !GetWindowRect(other, out var bounds)) continue;
                if (target.IntersectsWith(Rectangle.FromLTRB(bounds.Left, bounds.Top, bounds.Right, bounds.Bottom)))
                    throw new InvalidOperationException($"UI screenshot obstructed by {DescribeWindow(other)}");
            }
        }
        RequireUnobstructed();
        using var bitmap = new Bitmap(target.Width, target.Height);
        using (var graphics = Graphics.FromImage(bitmap))
            graphics.CopyFromScreen(target.Location, Point.Empty, target.Size);
        bitmap.Save(path, ImageFormat.Png);
        RequireUnobstructed();
        GetWindowThreadProcessId(window, out var afterOwner);
        if (afterOwner != expectedPid) throw new InvalidOperationException("UI window ownership changed during screenshot");
        if (!GetWindowRect(window, out var after) || !r.Equals(after))
            throw new InvalidOperationException("Native window changed during screenshot");
    }
}
