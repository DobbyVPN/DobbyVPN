using System;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using DobbyVPN.Windows;

internal static class NativeDiagnosticsTests
{
    public static async Task RunAsync()
    {
        var directory = Path.Combine(Path.GetTempPath(), "dobby-diagnostics-" + Guid.NewGuid());
        Directory.CreateDirectory(directory);
        try
        {
            var backend = Path.Combine(directory, "backend.jsonl");
            var ui = Path.Combine(directory, "ui.jsonl");
            var destination = Path.Combine(directory, "export.gz");
            var activationUi = Path.Combine(directory, "activation-ui.jsonl");
            var activationDiagnostics = new NativeDiagnostics(backend, activationUi);
            activationDiagnostics.Record("same failure", "ui.failure");
            activationDiagnostics.Record("same failure", "ui.failure");
            var activationFields = new
            {
                kind = "Protocol",
                runtimeDataType = "ProtocolActivatedEventArgs",
                protocolUri = "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fsubscription",
            };
            activationDiagnostics.RecordInfo("ui.failure", "same failure", activationFields);
            activationDiagnostics.RecordInfo("ui.failure", "same failure", activationFields);
            activationDiagnostics.Record("same failure", "ui.failure");
            activationDiagnostics.Record("", "ui.failure");
            activationDiagnostics.Record("same failure", "ui.failure");
            var activationLines = File.ReadAllLines(activationUi);
            Require(activationLines.Length == 4, "INFO activation records were deduplicated or ERROR reset behavior changed");
            using (var errorRecord = JsonDocument.Parse(activationLines[0]))
            using (var firstInfo = JsonDocument.Parse(activationLines[1]))
            using (var secondInfo = JsonDocument.Parse(activationLines[2]))
            using (var resetError = JsonDocument.Parse(activationLines[3]))
            {
                Require(errorRecord.RootElement.GetProperty("level").GetString() == "ERROR" &&
                    firstInfo.RootElement.GetProperty("level").GetString() == "INFO" &&
                    firstInfo.RootElement.GetProperty("process_sequence").GetInt64() == 2 &&
                    secondInfo.RootElement.GetProperty("process_sequence").GetInt64() == 3,
                    "INFO records changed error severity or did not share the process sequence");
                Require(firstInfo.RootElement.GetProperty("fields").GetProperty("protocolUri").GetString() ==
                    secondInfo.RootElement.GetProperty("fields").GetProperty("protocolUri").GetString(),
                    "repeated activation payload fields were not retained");
                Require(resetError.RootElement.GetProperty("level").GetString() == "ERROR" &&
                    resetError.RootElement.GetProperty("process_sequence").GetInt64() == 4,
                    "ERROR deduplication did not preserve its empty-message reset behavior");
            }
            var block = Enumerable.Range(0, 65536).Select(i => (byte)i).ToArray();
            await using (var source = File.Create(backend))
                for (var index = 0; index < 1024; index++) await source.WriteAsync(block);
            var diagnostics = new NativeDiagnostics(backend, ui);
            for (var index = 0; index < 100; index++)
                diagnostics.Record($"error-{index}:" + new string('x', 10000), "ui.failure");
            // A new owner must read the complete persisted UI history.
            diagnostics = new NativeDiagnostics(backend, ui);
            var previewEntries = await diagnostics.EntriesAsync();
            Require(previewEntries.Error == "", previewEntries.Error);
            var preview = string.Join("\n", previewEntries.Entries.Select(entry => entry.Message));
            Require(preview.Length < 530000, "preview is not bounded");
            Require(!preview.Contains("error-0:") && preview.Contains("error-99:"), "preview is not the recent tail");
            await diagnostics.SaveAsync(destination, "test metadata\n");
            await using (var compressed = File.OpenRead(destination))
            await using (var exported = new GZipStream(compressed, CompressionMode.Decompress))
            {
                var prefix = Encoding.UTF8.GetBytes("test metadata\n\n--- backend.jsonl ---\n");
                var actualPrefix = new byte[prefix.Length];
                await exported.ReadExactlyAsync(actualPrefix);
                Require(prefix.SequenceEqual(actualPrefix), "metadata missing");
                var actual = new byte[block.Length];
                for (var index = 0; index < 1024; index++)
                {
                    await exported.ReadExactlyAsync(actual);
                    Require(block.SequenceEqual(actual), "export changed diagnostic bytes");
                }
                using var reader = new StreamReader(exported);
                var history = await reader.ReadToEndAsync();
                Require(history.Contains("error-0:") && history.Contains("error-99:"), "persisted errors missing");
            }
            try
            {
                await diagnostics.SaveAsync(backend, "overwrite");
                throw new InvalidOperationException("export overwrote diagnostic input");
            }
            catch (IOException) { Require(new FileInfo(backend).Length == 67108864, "source changed"); }
            // A failed replacement must preserve the old destination and remove the temporary file.
            await using (var locked = new FileStream(destination, FileMode.Open, FileAccess.Read, FileShare.None))
            {
                try
                {
                    await diagnostics.SaveAsync(destination, "replacement");
                    throw new InvalidOperationException("locked destination was overwritten");
                }
                catch (Exception failure) when (failure is IOException or UnauthorizedAccessException) { }
            }
            Require(!Directory.EnumerateFiles(directory, ".dobby-export-*.tmp").Any(), "partial export left behind");
            var native = Path.Combine(directory, "rotation.jsonl");
            using (var legacy = File.Create(native))
            {
                legacy.SetLength(NativeLogFiles.Threshold - 1);
                legacy.Position = NativeLogFiles.Threshold - 1;
                legacy.Write(Encoding.UTF8.GetBytes("\nlegacy-tail\n"));
            }
            NativeLogFiles.Append(native, Encoding.UTF8.GetBytes("first\n"));
            Require(new FileInfo(native + ".previous").Length == NativeLogFiles.Threshold, "migration split a record");
            Require(File.ReadAllText(native) == "legacy-tail\nfirst\n", "migration lost the retained tail");
            var clearBoundary = Path.Combine(directory, "clear-boundary.jsonl");
            var clearUi = Path.Combine(directory, "clear-boundary-ui.jsonl");
            var completeRows = Encoding.UTF8.GetBytes("{\"message\":\"pre-clear first\"}\n{\"message\":\"pre-clear second\"}\n");
            var crossingRow = Encoding.UTF8.GetBytes("{\"message\":\"pre-clear final crossing\"}\n");
            var sparseLength = NativeLogFiles.Threshold - completeRows.Length - crossingRow.Length / 2;
            using (var sparse = File.Create(clearBoundary))
            {
                sparse.SetLength(sparseLength);
                sparse.Position = sparseLength;
                sparse.Write(completeRows);
                sparse.Write(crossingRow);
            }
            var clearFileLength = new FileInfo(clearBoundary).Length;
            var clearDiagnostics = new NativeDiagnostics(clearBoundary, clearUi);
            await clearDiagnostics.ClearViewAsync();
            NativeLogFiles.Append(clearBoundary, Encoding.UTF8.GetBytes("{\"message\":\"after clear restart\"}\n"));
            Require(new FileInfo(clearBoundary + ".previous").Length == clearFileLength,
                "startup migration rewrote the final threshold-crossing record");
            var restartedDiagnostics = new NativeDiagnostics(clearBoundary, clearUi);
            var afterClearEntries = await restartedDiagnostics.EntriesAsync();
            Require(afterClearEntries.Error == "" &&
                afterClearEntries.Entries.Select(entry => entry.Message).SequenceEqual(new[] { "after clear restart" }),
                "startup migration exposed pre-Clear rows or hid the post-Clear row");
            await restartedDiagnostics.SaveAsync(destination, "Clear retention\n");
            Require(await ExportContainsAsync(destination, "pre-clear final crossing"),
                "Clear or startup migration removed old rows from export");
            await using (var held = new FileStream(native, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
            {
                foreach (var record in new[] {"second\n", "third\n"})
                {
                    using (var growing = new FileStream(native, FileMode.Open, FileAccess.Write, FileShare.ReadWrite | FileShare.Delete))
                        growing.SetLength(NativeLogFiles.Threshold);
                    NativeLogFiles.Append(native, Encoding.UTF8.GetBytes(record));
                }
                var retained = new byte[18];
                await held.ReadExactlyAsync(retained);
                Require(Encoding.UTF8.GetString(retained) == "legacy-tail\nfirst\n", "rotation lost captured bytes");
            }
            File.WriteAllText(native + ".previous", "retained prior\n");
            await new NativeDiagnostics(native, ui).SaveAsync(destination, "both generations\n");
            using (var compressed = File.OpenRead(destination))
            using (var gzip = new GZipStream(compressed, CompressionMode.Decompress))
            using (var reader = new StreamReader(gzip))
            {
                var both = await reader.ReadToEndAsync();
                Require(both.Contains("retained prior") && both.Contains("third"), "export lost a generation");
            }
            using var partial = new MemoryStream();
            await new NativeDiagnostics(directory, ui).ExportAsync(partial, "read failure\n");
            partial.Position = 0;
            using var uncompressed = new GZipStream(partial, CompressionMode.Decompress);
            using var text = new StreamReader(uncompressed);
            var details = await text.ReadToEndAsync();
            Require(details.Contains(directory) && details.Contains("error-0:"), "read failure hid available logs");
            // L1/L2/L4/L5: preserve structured fields and original records,
            // keep equal-time records stable across streams, leave raw output
            // untimed, and classify stderr capture as information.
            var presentationBackend = Path.Combine(directory, "presentation.jsonl");
            var presentationStderr = presentationBackend + ".stderr";
            var presentationUI = Path.Combine(directory, "presentation-ui.jsonl");
            const string tied = "2026-01-01T00:00:03Z";
            const string backendTie = "{\"timestamp\":\"" + tied + "\",\"level\":\"INFO\",\"source\":\"backend-test\",\"message\":\"backend tie\",\"extra\":42}";
            const string capture = "{\"timestamp\":\"" + tied + "\",\"level\":\"ERROR\",\"source\":\"stdio\",\"event\":\"stderr.capture\",\"message\":\"raw capture detail\"}";
            const string uiTie = "{\"timestamp\":\"" + tied + "\",\"level\":\"WARN\",\"source\":\"ui-test\",\"message\":\"warning detail λ\",\"extra\":{\"attempt\":2}}";
            const string jsonLikeRaw = "{\"timestamp\":not-json raw output";
            File.WriteAllText(presentationBackend, backendTie + "\n" + jsonLikeRaw + "\ntrace line 1\ntrace line 2\n");
            File.WriteAllText(presentationStderr, capture + "\n");
            File.WriteAllText(presentationUI, uiTie + "\n");
            var presentation = new NativeDiagnostics(presentationBackend, presentationUI);
            var presented = await presentation.EntriesAsync();
            Require(presented.Error == "", presented.Error);
            var structuredEntry = presented.Entries.Single(entry => entry.Message == "warning detail λ");
            Require(structuredEntry.Timestamp == tied && structuredEntry.Level == "WARN" &&
                structuredEntry.Source == "App · ui-test" && structuredEntry.Raw == uiTie,
                "structured timestamp, severity, source, message, or original record changed");
            Require(structuredEntry.Raw.Contains("\"attempt\":2"), "extra structured fields were lost");
            var ties = presented.Entries.Where(entry => entry.Time == DateTimeOffset.Parse(tied)).Select(entry => entry.Message).ToArray();
            Require(ties.SequenceEqual(new[] { "backend tie", "Stderr capture initialized", "warning detail λ" }),
                "equal-timestamp stream ordering was not stable");
            var repeated = await presentation.EntriesAsync();
            Require(repeated.Entries.Select(entry => entry.Id).SequenceEqual(presented.Entries.Select(entry => entry.Id)),
                "repeated reads changed stable diagnostic ordering");
            var rawEntries = presented.Entries.Where(entry => entry.Time is null).ToArray();
            Require(rawEntries.Select(entry => entry.Message).SequenceEqual(new[] { jsonLikeRaw, "trace line 1", "trace line 2" }) &&
                rawEntries.All(entry => entry.Timestamp == "" && entry.Level == "RAW" && entry.Source == "Backend" && entry.Raw == entry.Message),
                "raw JSON-like output or multiline traces gained fabricated event metadata or changed order");
            var captureEntry = presented.Entries.Single(entry => entry.Message == "Stderr capture initialized");
            Require(captureEntry.Level == "INFO" && captureEntry.Source == "Backend stderr · stdio" &&
                captureEntry.Timestamp == tied && captureEntry.Raw == capture,
                "stderr.capture was presented as an error or lost its source record");
            var structured = Path.Combine(directory, "structured.jsonl");
            var structuredUI = Path.Combine(directory, "structured-ui.jsonl");
            const string earlier = "{\"timestamp\":\"2026-01-01T00:00:01Z\",\"level\":\"WARN\",\"message\":\"earlier λ\",\"extra\":42}\n";
            const string later = "{\"timestamp\":\"2026-01-01T00:00:02Z\",\"message\":\"later\"}\n";
            File.WriteAllText(structured, later + "trace line 1\ntrace line 2\n");
            File.WriteAllText(structuredUI, earlier + "{\"message\":\"incomplete");
            var view = new NativeDiagnostics(structured, structuredUI);
            var parsed = await view.EntriesAsync();
            Require(parsed.Error == "", parsed.Error);
            Require(parsed.Entries.Select(e => e.Message).SequenceEqual(new[] { "earlier λ", "trace line 1", "trace line 2", "later" }), "structured ordering or partial record failed");
            Require(parsed.Entries[0].Level == "WARN" && parsed.Entries[0].Raw.Contains("extra"), "structured details lost");
            File.AppendAllText(structuredUI, " record\"}\n");
            parsed = await view.EntriesAsync();
            var completedJson = parsed.Entries.Single(entry => entry.Message == "incomplete record");
            Require(completedJson.Timestamp == "" && completedJson.Source == "App" &&
                completedJson.Raw == "{\"message\":\"incomplete record\"}",
                "an incomplete JSON record did not become readable when completed on the same stream");
            await view.ClearViewAsync();
            File.Move(structured, structured + ".previous");
            File.WriteAllText(structured, "new after rotation\n");
            view = new NativeDiagnostics(structured, structuredUI);
            parsed = await view.EntriesAsync();
            Require(parsed.Error == "" && parsed.Entries.Select(e => e.Message).SequenceEqual(new[] { "new after rotation" }), "Clear did not survive rotation/restart or partial boundary");
            Require(File.ReadAllText(structured + ".previous").StartsWith(later), "Clear modified retained bytes");
            File.WriteAllBytes(structured, Encoding.UTF8.GetBytes("partial ").Concat(new byte[] { 0xCE }).ToArray());
            Require((await view.EntriesAsync()).Entries.Single().Message == "partial ", "Incomplete UTF-8 fabricated text");
            await using (var completed = new FileStream(structured, FileMode.Append, FileAccess.Write, FileShare.ReadWrite))
                await completed.WriteAsync(new byte[] { 0xBB, (byte)'\n' });
            Require((await view.EntriesAsync()).Entries.Single().Message == "partial λ", "Completed UTF-8 was lost");
            VerifyLogPresentationState();
            VerifySubscriptionLoadState();
            VerifyProfileAutomationIds();
            await VerifyStaActivationRedirectAsync();
            Console.WriteLine("Native diagnostics: 64 MiB exact export, bounded preview, persisted history, structured ordering, raw streams, capture classification, same-stream incomplete record/UTF-8 completion, rotation, input protection, failure cleanup, production log presentation, subscription load state, profile automation identifiers, and production STA activation wait passed");
        }
        finally { Directory.Delete(directory, recursive: true); }
    }

    private static async Task VerifyStaActivationRedirectAsync()
    {
        var completed = new TaskCompletionSource<Exception?>(TaskCreationOptions.RunContinuationsAsynchronously);
        var sta = new Thread(() =>
        {
            try
            {
                var workFinished = false;
                StaActivationRedirect.WaitForRedirect(async () =>
                {
                    await Task.Yield();
                    workFinished = true;
                });
                Require(workFinished, "STA activation wait returned before asynchronous redirection completed");

                var expected = new InvalidOperationException("activation redirect failure sentinel");
                try
                {
                    StaActivationRedirect.WaitForRedirect(() => Task.FromException(expected));
                    throw new InvalidOperationException("STA activation wait swallowed a redirect failure");
                }
                catch (InvalidOperationException actual) when (ReferenceEquals(actual, expected)) { }

                completed.SetResult(null);
            }
            catch (Exception error)
            {
                completed.SetResult(error);
            }
        });
        sta.IsBackground = true;
        sta.SetApartmentState(ApartmentState.STA);
        sta.Start();
        var failure = await completed.Task.WaitAsync(TimeSpan.FromSeconds(10));
        sta.Join();
        if (failure is not null)
            System.Runtime.ExceptionServices.ExceptionDispatchInfo.Capture(failure).Throw();
    }

    private static void VerifyLogPresentationState()
    {
        var first = new NativeDiagnostics.Entry(
            "volume:17:0", "2026-01-01T00:00:01Z", "INFO", "Backend · fixture",
            "first marker", "{\"message\":\"first marker\"}", DateTimeOffset.Parse("2026-01-01T00:00:01Z"));
        var second = new NativeDiagnostics.Entry(
            "volume:17:32", "2026-01-01T00:00:02Z", "WARN", "Backend · fixture",
            "second marker", "{\"message\":\"second marker\"}", DateTimeOffset.Parse("2026-01-01T00:00:02Z"));
        var appended = new NativeDiagnostics.Entry(
            "volume:17:64", "2026-01-01T00:00:03Z", "INFO", "Backend · fixture",
            "append marker", "{\"message\":\"append marker\"}", DateTimeOffset.Parse("2026-01-01T00:00:03Z"));
        var state = new LogPresentationState();
        state.ReplaceEntries([first, second]);
        state.SetExpanded(first.Id, true);
        Require(state.FollowsTail && state.VisibleEntries.Select(entry => entry.Id).SequenceEqual(new[] { first.Id, second.Id }) &&
            state.LatestEntries.Select(entry => entry.Id).SequenceEqual(new[] { first.Id, second.Id }),
            "initial log presentation did not follow the current entries");
        var automationId = LogPresentationState.DetailsAutomationId(first);
        var accessibleName = LogPresentationState.DetailsAccessibleName(first);
        Require(automationId == "Log details " + first.Id && accessibleName == "Details" && accessibleName != automationId,
            "log Details identity replaced or omitted its user-facing accessible name");

        state.Freeze();
        state.ReplaceEntries([first, second, appended]);
        Require(!state.FollowsTail && state.LatestEntries[^1].Id == appended.Id &&
            state.VisibleEntries.Select(entry => entry.Id).SequenceEqual(new[] { first.Id, second.Id }) && state.IsExpanded(first.Id),
            "a controlled append changed the frozen visible snapshot or discarded its expanded row");
        state.ObserveUserScroll(isAtBottom: true);
        Require(state.FollowsTail && state.VisibleEntries.Select(entry => entry.Id).SequenceEqual(new[] { first.Id, second.Id, appended.Id }),
            "resuming at the bottom did not show the latest entries");
        state.ClearAfterBoundary();
        Require(state.FollowsTail && state.VisibleEntries.Count == 0 && state.LatestEntries.Count == 0 && !state.IsExpanded(first.Id),
            "Clear did not reset the production presentation state");
        state.ReplaceEntries([appended]);
        Require(state.VisibleEntries.Select(entry => entry.Id).SequenceEqual(new[] { appended.Id }),
            "the first post-Clear entry was not presented after its boundary");
    }

    private static void VerifySubscriptionLoadState()
    {
        var typed = new SubscriptionLoadState();
        typed.Edit("https://example.test/first", valid: true, kind: SubscriptionInputKind.Typed, now: TimeSpan.Zero);
        Require(typed.BeginReady(TimeSpan.FromMilliseconds(399)) is null,
            "typed URL dispatched before its 400 ms debounce elapsed");
        var typedRequest = typed.BeginReady(TimeSpan.FromMilliseconds(400));
        Require(typedRequest?.Source == "https://example.test/first",
            "typed URL did not dispatch when its 400 ms debounce elapsed");
        _ = typed.Finish(typedRequest!, TimeSpan.FromMilliseconds(401));

        var reset = new SubscriptionLoadState();
        reset.Edit("https://example.test/old", valid: true, kind: SubscriptionInputKind.Typed, now: TimeSpan.Zero);
        reset.Edit("https://example.test/latest", valid: true, kind: SubscriptionInputKind.Typed, now: TimeSpan.FromMilliseconds(200));
        Require(reset.BeginReady(TimeSpan.FromMilliseconds(599)) is null,
            "a later typed edit did not reset the debounce deadline");
        var resetRequest = reset.BeginReady(TimeSpan.FromMilliseconds(600));
        Require(resetRequest?.Source == "https://example.test/latest",
            "debounce reset dispatched a stale URL rather than the latest edit");
        _ = reset.Finish(resetRequest!, TimeSpan.FromMilliseconds(601));

        foreach (var trigger in new[] { SubscriptionInputKind.Paste, SubscriptionInputKind.Import, SubscriptionInputKind.Retry })
        {
            var immediate = new SubscriptionLoadState();
            immediate.Edit("https://example.test/immediate", valid: true, kind: trigger, now: TimeSpan.Zero);
            Require(immediate.BeginReady(TimeSpan.Zero)?.Source == "https://example.test/immediate",
                $"{trigger} incorrectly waited for the typed-input debounce");
        }

        var serialized = new SubscriptionLoadState();
        serialized.Edit("https://example.test/first", valid: true, kind: SubscriptionInputKind.Paste, now: TimeSpan.Zero);
        var first = serialized.BeginReady(TimeSpan.Zero)!;
        serialized.Edit("https://example.test/obsolete", valid: true, kind: SubscriptionInputKind.Typed, now: TimeSpan.FromMilliseconds(10));
        serialized.Edit("https://example.test/latest", valid: true, kind: SubscriptionInputKind.Import, now: TimeSpan.FromMilliseconds(20));
        Require(serialized.BeginReady(TimeSpan.FromMilliseconds(20)) is null && serialized.IsLoading,
            "a new input started a second Configure while an earlier load was held");
        Require(serialized.CanAct(isStopTarget: true, stopInFlight: false, busy: true, hasLoadError: true,
                sequenceCurrent: false, configured: false, canStartOrSwitch: false) &&
            !serialized.CanAct(isStopTarget: false, stopInFlight: false, busy: false, hasLoadError: false,
                sequenceCurrent: true, configured: true, canStartOrSwitch: true),
            "Stop became unresponsive or a connection action remained enabled during a pending edit");
        Require(!serialized.MarkAccepted(first), "a stale completion marked the newest source accepted");
        var latest = serialized.Finish(first, TimeSpan.FromMilliseconds(30));
        Require(latest?.Source == "https://example.test/latest" && latest.Revision > first.Revision,
            "finishing a held load did not dispatch the latest immediate request");
        Require(serialized.MarkAccepted(latest!) && !serialized.SourceDirty,
            "the current successful completion did not accept and clear the newest source");
        _ = serialized.Finish(latest!, TimeSpan.FromMilliseconds(40));

        var heldTyped = new SubscriptionLoadState();
        heldTyped.Edit("https://example.test/first", valid: true, kind: SubscriptionInputKind.Retry, now: TimeSpan.Zero);
        var heldFirst = heldTyped.BeginReady(TimeSpan.Zero)!;
        heldTyped.Edit("https://example.test/latest", valid: true, kind: SubscriptionInputKind.Typed, now: TimeSpan.FromMilliseconds(10));
        Require(heldTyped.Finish(heldFirst, TimeSpan.FromMilliseconds(409)) is null,
            "a held Configure ignored the typed input's 400 ms deadline");
        var heldLatest = heldTyped.BeginReady(TimeSpan.FromMilliseconds(410));
        Require(heldLatest?.Source == "https://example.test/latest",
            "the newest typed request was not dispatched when its deadline elapsed after completion");
    }

    private static void VerifyProfileAutomationIds()
    {
        const string digest = "accepted-go-digest";
        Require(ProfileAutomationIds.Create(digest, 0, "description") == "profile.accepted-go-digest.0.description" &&
            ProfileAutomationIds.Create(digest, 0, "protocol") == "profile.accepted-go-digest.0.protocol" &&
            ProfileAutomationIds.Create(digest, 0, "action") == "profile.accepted-go-digest.0.action",
            "profile role identifiers did not use the accepted digest and zero-based inventory index");
        Require(ProfileAutomationIds.TryParse("profile.accepted-go-digest.0.action", out var parsedDigest, out var index, out var role) &&
            parsedDigest == digest && index == 0 && role == "action" &&
            !ProfileAutomationIds.TryParse("Profile 1 action", out _, out _, out _),
            "profile automation ID parsing accepted an unstable legacy selector or lost its semantic fields");
    }

    private static void Require(bool condition, string message)
    {
        if (!condition) throw new InvalidOperationException(message);
    }

    private static async Task<bool> ExportContainsAsync(string path, string value)
    {
        var needle = Encoding.UTF8.GetBytes(value);
        var buffer = new byte[65536 + needle.Length - 1];
        await using var source = File.OpenRead(path);
        await using var gzip = new GZipStream(source, CompressionMode.Decompress);
        var carry = 0;
        while (true)
        {
            var count = await gzip.ReadAsync(buffer.AsMemory(carry, 65536));
            if (count == 0) return false;
            var length = carry + count;
            if (buffer.AsSpan(0, length).IndexOf(needle) >= 0) return true;
            carry = Math.Min(needle.Length - 1, length);
            buffer.AsSpan(length - carry, carry).CopyTo(buffer);
        }
    }
}
