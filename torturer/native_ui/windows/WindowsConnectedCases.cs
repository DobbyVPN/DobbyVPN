using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Net.Http;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Windows.Automation;
using DobbyVPN.Windows;

internal static partial class Program
{
    private const string NativeObserverPrefix = "DOBBY_NATIVE_OBSERVE ";

    private readonly record struct ConnectedActionSample(
        string AutomationId, string Name, string ControlType, bool Enabled, bool Offscreen);

    private static void RunConnectedLayoutCase(
        OwnedLogsApp app, JsonElement request, string executable, NativeDiagnostics diagnostics,
        string subscriptionUrl,
        string controlUrl, string controlKey, string originalProfilePath,
        SubscriptionFixtureStats fixtureStart, LogsCaseDeadline deadline,
        List<OwnedLogsApp> launches, List<object> assertions, List<string> cleanupErrors,
        List<object> observerResults, List<string> screenshots, string screenshotDirectory)
    {
        if (!request.TryGetProperty("observer_enabled", out var observerEnabled) ||
            observerEnabled.ValueKind != JsonValueKind.True)
            throw new ArgumentException("connected-layout requires the native observer callback channel");

        var originalBytes = File.ReadAllBytes(originalProfilePath);
        if (originalBytes.Length == 0 || originalBytes.Length > 1 << 20)
            throw new InvalidDataException("Connected-layout source profile must contain 1 byte through 1 MiB");
        var fixtureGets = fixtureStart.SubscriptionGets;
        ApplySubscriptionThroughRenderedControl(app, subscriptionUrl, paste: false, deadline);
        var originalLayout = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 1);
        var originalDigest = originalLayout.GetValueOrDefault("profile_digest") as string
            ?? throw new InvalidOperationException("Connected-layout source inventory has no rendered configuration digest");
        var originalRows = ProfileRows(originalLayout);
        AssertLogsCase(assertions, "connected-layout-fixture-starts-with-no-subscription-get", 0,
            fixtureStart.SubscriptionGets);
        AssertProfileRowsHaveSemanticIds(assertions, originalLayout);
        var originalFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(300));
        AssertSubscriptionFixtureRequest(assertions, "connected-layout-source-configure", fixtureStart, originalFixture);
        AssertLogsCase(assertions, "connected-layout-source-is-the-restorable-two-profile-inventory", 2,
            originalRows.Length);
        AssertLogsCase(assertions, "connected-layout-source-editor-retains-requested-url", subscriptionUrl,
            ReadSourceEditor(app));
        fixtureGets = originalFixture.SubscriptionGets;

        var originalSnapshot = WaitNativeSnapshot(request, deadline, "connected-layout source inventory", snapshot =>
            JsonBoolean(snapshot, "configured") && JsonString(snapshot, "source_url") == subscriptionUrl &&
            JsonString(snapshot, "digest") == originalDigest && JsonArrayLength(snapshot, "profiles") > 0 &&
            IsDisconnectedInventory(snapshot));
        AssertLogsCase(assertions, "connected-layout-source-Go-and-rendered-digests-match", originalDigest,
            JsonString(originalSnapshot, "digest"));

        RequestNativeObserver(request, deadline, "prepare-connect");
        WaitNativeAction(app, "VPN connection action", "Auto connect", deadline);
        InvokeRenderedAction(app, "VPN connection action", "Auto connect", deadline);
        var connected = WaitNativeSnapshot(request, deadline, "connected-layout initial Auto session", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "active_mode") == "AUTO_SELECT" &&
            JsonObject(snapshot, "active_profile").ValueKind == JsonValueKind.Object &&
            JsonString(snapshot, "digest") == originalDigest);
        var initialGeneration = JsonInt64(connected, "generation");
        AssertLogsCase(assertions, "connected-layout-initial-Auto-keeps-original-inventory", originalDigest,
            JsonString(connected, "digest"));
        WaitRenderedStatus(app, "Connected", deadline);
        RecordNativeObservations(request, deadline, "auto", observerResults, assertions);
        AddScreenshot(app, "connected-layout", "initial-connected", deadline, screenshots, screenshotDirectory);

        var synthetic = CreateConnectedLayoutInventory();
        ReplaceSubscriptionFixtureProfile(controlUrl, controlKey, synthetic, deadline);
        var layoutUrl = AppendQuery(subscriptionUrl, "connected-layout=24");
        var beforeLayoutStats = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets, deadline, TimeSpan.Zero);
        var appPidBeforeLayoutImport = app.Process.Id;
        var appIdentityBeforeLayoutImport = app.StartTicks;
        var appWindowBeforeLayoutImport = app.Window;
        TrackWarmProtocolActivation(app, layoutUrl, deadline, launches, assertions);
        WaitForProtocolImportEvent(diagnostics, layoutUrl, deadline);
        VerifyOwnedLogsApp(app);
        GetWindowThreadProcessId(appWindowBeforeLayoutImport, out var layoutWindowOwner);
        AssertLogsCase(assertions, "connected-layout-24-import-retains-exact-app-process", appPidBeforeLayoutImport,
            app.Process.Id);
        AssertLogsCase(assertions, "connected-layout-24-import-retains-process-identity", appIdentityBeforeLayoutImport,
            app.StartTicks);
        AssertLogsCase(assertions, "connected-layout-24-import-retains-owned-window", true,
            app.Window == appWindowBeforeLayoutImport && layoutWindowOwner == (uint)appPidBeforeLayoutImport);
        var layout = WaitForAcceptedProfiles(app, layoutUrl, deadline, minimumRows: 24);
        AssertLogsCase(assertions, "connected-layout-24-os-import-renders-requested-url", layoutUrl,
            ReadSourceEditor(app));
        AssertProfileRowsHaveSemanticIds(assertions, layout);
        var layoutDigest = layout.GetValueOrDefault("profile_digest") as string
            ?? throw new InvalidOperationException("Connected 24-profile inventory has no digest");
        var afterLayoutStats = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(800));
        AssertSubscriptionFixtureRequest(assertions, "connected-layout-replacement", beforeLayoutStats, afterLayoutStats);
        fixtureGets = afterLayoutStats.SubscriptionGets;
        var largeSnapshot = WaitNativeSnapshot(request, deadline, "connected 24-profile inventory replacement", snapshot =>
            JsonString(snapshot, "source_url") == layoutUrl && JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "digest") == layoutDigest && JsonArrayLength(snapshot, "profiles") == 24);
        AssertLogsCase(assertions, "connected-layout-replacement-loads-24-Go-profiles", 24,
            JsonArrayLength(largeSnapshot, "profiles"));
        AssertLogsCase(assertions, "connected-layout-replacement-changes-inventory-digest", true,
            JsonString(largeSnapshot, "digest") != originalDigest && layoutDigest == JsonString(largeSnapshot, "digest"));
        AssertLogsCase(assertions, "connected-layout-replacement-retains-connected-generation", initialGeneration,
            JsonInt64(largeSnapshot, "generation"));
        AssertLogsCase(assertions, "connected-layout-replacement-preserves-active-profile-and-generation", true,
            SameActiveGeneration(largeSnapshot, connected));
        AssertLogsCase(assertions, "connected-layout-replacement-retains-original-active-digest", originalDigest,
            JsonString(largeSnapshot, "active_digest"));
        AssertLogsCase(assertions, "connected-layout-replacement-preserves-active-profile-fields", true,
            SameActiveProfile(largeSnapshot, connected));
        AssertLogsCase(assertions, "connected-layout-replacement-keeps-rendered-connected-status", "Connected",
            ReadRenderedStatus(app));
        AddScreenshot(app, "connected-layout", "24-profile-inventory", deadline, screenshots, screenshotDirectory);
        RunRenderedProfileLayoutRows(app, layout, layoutDigest, "connected-layout-24", "connected-layout-24",
            deadline, assertions, screenshots, screenshotDirectory);

        RunConnectedTextSize150Layout(app, request, largeSnapshot, executable, layoutDigest, deadline, assertions,
            cleanupErrors, screenshots, screenshotDirectory);
        var connectedAtRestoredTextSize = RequestNativeObserver(request, deadline, "snapshot");
        AssertLogsCase(assertions, "connected-layout-150-restore-keeps-live-connection-generation", true,
            SameActiveGeneration(connectedAtRestoredTextSize, connected) &&
            JsonString(connectedAtRestoredTextSize, "state") == "CONNECTED");
        AssertLogsCase(assertions, "connected-layout-text-size-restore-keeps-24-profile-inventory-digest",
            layoutDigest, JsonString(connectedAtRestoredTextSize, "digest"));
        AssertLogsCase(assertions, "connected-layout-text-size-restore-keeps-24-profile-inventory", 24,
            JsonArrayLength(connectedAtRestoredTextSize, "profiles"));
        AssertLogsCase(assertions, "connected-layout-150-restore-keeps-rendered-Connected", "Connected",
            ReadRenderedStatus(app));

        ReplaceSubscriptionFixtureProfile(controlUrl, controlKey, originalBytes, deadline);
        var restoreUrl = AppendQuery(subscriptionUrl, "connected-layout=restore");
        var beforeRestoreStats = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets, deadline, TimeSpan.Zero);
        var appPidBeforeRestore = app.Process.Id;
        var appIdentityBeforeRestore = app.StartTicks;
        var appWindowBeforeRestore = app.Window;
        TrackWarmProtocolActivation(app, restoreUrl, deadline, launches, assertions);
        WaitForProtocolImportEvent(diagnostics, restoreUrl, deadline);
        VerifyOwnedLogsApp(app);
        GetWindowThreadProcessId(appWindowBeforeRestore, out var restoreWindowOwner);
        AssertLogsCase(assertions, "connected-layout-restore-import-retains-exact-app-process", appPidBeforeRestore,
            app.Process.Id);
        AssertLogsCase(assertions, "connected-layout-restore-import-retains-process-identity", appIdentityBeforeRestore,
            app.StartTicks);
        AssertLogsCase(assertions, "connected-layout-restore-import-retains-owned-window", true,
            app.Window == appWindowBeforeRestore && restoreWindowOwner == (uint)appPidBeforeRestore);
        var restoredLayout = WaitForAcceptedProfiles(app, restoreUrl, deadline, minimumRows: originalRows.Length);
        AssertLogsCase(assertions, "connected-layout-restore-os-import-renders-requested-url", restoreUrl,
            ReadSourceEditor(app));
        AssertProfileRowsHaveSemanticIds(assertions, restoredLayout);
        var restoredDigest = restoredLayout.GetValueOrDefault("profile_digest") as string
            ?? throw new InvalidOperationException("Restored original profile inventory has no digest");
        var afterRestoreStats = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(800));
        AssertSubscriptionFixtureRequest(assertions, "connected-layout-original-inventory-restore",
            beforeRestoreStats, afterRestoreStats);
        fixtureGets = afterRestoreStats.SubscriptionGets;
        var restored = WaitNativeSnapshot(request, deadline, "restore original connected profile inventory", snapshot =>
            JsonString(snapshot, "source_url") == restoreUrl && JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "digest") == originalDigest && JsonArrayLength(snapshot, "profiles") == originalRows.Length);
        AssertLogsCase(assertions, "connected-layout-restores-original-inventory-digest", originalDigest,
            restoredDigest);
        AssertLogsCase(assertions, "connected-layout-restores-original-inventory-count", originalRows.Length,
            JsonArrayLength(restored, "profiles"));
        AssertLogsCase(assertions, "connected-layout-restore-retains-connected-generation", initialGeneration,
            JsonInt64(restored, "generation"));
        AssertLogsCase(assertions, "connected-layout-restore-preserves-active-profile-and-generation", true,
            SameActiveGeneration(restored, connected));
        AssertLogsCase(assertions, "connected-layout-restore-preserves-active-profile-fields", true,
            SameActiveProfile(restored, connected));
        AssertLogsCase(assertions, "connected-layout-restores-original-semantic-row-ids", true,
            SameProfileAutomationIds(originalLayout, restoredLayout));
        AssertLogsCase(assertions, "connected-layout-restore-keeps-rendered-connected-status", "Connected",
            ReadRenderedStatus(app));
        AddScreenshot(app, "connected-layout", "restored-original-inventory", deadline, screenshots, screenshotDirectory);

        DisconnectRenderedAndVerifyCleanup(request, app, restored, deadline,
            observerResults, assertions, "connected-layout-final-disconnect");
        AddScreenshot(app, "connected-layout", "disconnected", deadline, screenshots, screenshotDirectory);
        AssertLogsCase(assertions, "connected-layout-completes-three-fixture-requests", fixtureStart.SubscriptionGets + 3,
            fixtureGets);
    }

    private static byte[] CreateConnectedLayoutInventory()
    {
        var profiles = Enumerable.Range(0, 24).Select(index =>
            "[[Outline]]\nDescription = \"" + (index == 0 ? "" : $"Layout profile {index + 1}") +
            "\"\nServer = \"127.0.0.1\"\nPort = 9\nPassword = \"never-connect-layout-" +
            (index + 1).ToString(CultureInfo.InvariantCulture) + "\"");
        return Encoding.UTF8.GetBytes(string.Join("\n\n", profiles));
    }

    private static void ReplaceSubscriptionFixtureProfile(
        string controlUrl, string controlKey, byte[] profile, LogsCaseDeadline deadline)
    {
        if (profile.Length == 0 || profile.Length > 1 << 20)
            throw new ArgumentOutOfRangeException(nameof(profile), "Subscription fixture profile must contain 1 byte through 1 MiB");
        deadline.Check("replace subscription fixture profile");
        using var client = new HttpClient
        {
            Timeout = TimeSpan.FromSeconds(Math.Min(15, deadline.RemainingSeconds)),
        };
        using var request = new HttpRequestMessage(HttpMethod.Post,
            new Uri(controlUrl.TrimEnd('/') + "/profile", UriKind.Absolute));
        request.Headers.Add("X-DobbyVPN-Torturer-Key", controlKey);
        request.Content = new ByteArrayContent(profile);
        request.Content.Headers.ContentType = new System.Net.Http.Headers.MediaTypeHeaderValue("application/octet-stream");
        using var response = client.Send(request);
        deadline.Check("subscription fixture profile replacement response");
        if ((int)response.StatusCode != 204)
        {
            var body = response.Content.ReadAsStringAsync().GetAwaiter().GetResult();
            throw new HttpRequestException(
                $"Subscription fixture profile replacement returned {(int)response.StatusCode} {response.ReasonPhrase}: {body}");
        }
    }

    private static void RunConnectedTextSize150Layout(
        OwnedLogsApp app, JsonElement request, JsonElement expectedConnected,
        string executable, string digest, LogsCaseDeadline deadline,
        List<object> assertions, List<string> cleanupErrors,
        List<string> screenshots, string screenshotDirectory)
    {
        var inspect = RunTextSizeSettingsAction(executable, "inspect",
            caseDeadline: deadline, cleanupDeadline: new LogsCaseDeadline(30));
        RecordSettingsObservation(assertions, "connected-layout-original-text-size-is-available", inspect);
        var originalValue = SettingsTextSizeValue(inspect);
        var original = (int)Math.Round(originalValue);
        if (Math.Abs(originalValue - original) > 0.01)
            throw new InvalidOperationException($"Windows Text size is not an integer percentage: {originalValue.ToString(CultureInfo.InvariantCulture)}");
        var range = SettingsTextSizeRange(inspect);
        if (range.Minimum > 150 || range.Maximum < 150)
            throw new InvalidOperationException($"Windows Text size range does not include 150%: {range.Minimum}..{range.Maximum}");

        try
        {
            var current = original;
            if (current == 150)
            {
                var intermediate = range.Minimum <= 125 && range.Maximum >= 125 ? 125
                    : range.Minimum <= 100 && range.Maximum >= 100 ? 100
                    : throw new InvalidOperationException($"Windows Text size range has no setting away from 150%: {range.Minimum}..{range.Maximum}");
                var stepped = RunTextSizeSettingsAction(executable, "apply", intermediate, current,
                    deadline, new LogsCaseDeadline(30));
                RecordSettingsObservation(assertions, "connected-layout-text-size-leaves-150-before-reapply", stepped);
                AssertLogsCase(assertions, "connected-layout-text-size-intermediate-setting", (double)intermediate,
                    TextSizeResponseValueAfterAction(stepped));
                current = intermediate;
            }

            var applied = RunTextSizeSettingsAction(executable, "apply", 150, current,
                deadline, new LogsCaseDeadline(30));
            RecordSettingsObservation(assertions, "connected-layout-applies-native-text-size-150", applied);
            AssertLogsCase(assertions, "connected-layout-text-size-apply-result-is-150", 150.0,
                TextSizeResponseValueAfterAction(applied));
            var verified = RunTextSizeSettingsAction(executable, "inspect",
                caseDeadline: deadline, cleanupDeadline: new LogsCaseDeadline(30));
            RecordSettingsObservation(assertions, "connected-layout-text-size-150-setting-is-available", verified);
            AssertLogsCase(assertions, "connected-layout-os-text-size-is-exactly-150", 150.0,
                SettingsTextSizeValue(verified));

            Dictionary<string, object?>? layout = null;
            WaitLogsCase(deadline, "connected window reflow at 150% Text size", () =>
            {
                var currentLayout = ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window);
                if (!Equals(currentLayout.GetValueOrDefault("profile_digest"), digest) ||
                    !ProfileLayoutGeometryValid(currentLayout, requireReadableLogs: true)) return false;
                layout = currentLayout;
                return true;
            }, "Connected native window did not retain readable, non-overlapping layout at 150% Text size");
            var at150 = layout ?? throw new InvalidOperationException("Connected 150% reflow did not capture native geometry");
            RunRenderedProfileLayoutRows(app, at150, digest, "connected-layout-150", "connected-layout-150",
                deadline, assertions, screenshots, screenshotDirectory);
            var activeAt150 = RequestNativeObserver(request, deadline, "snapshot");
            AssertLogsCase(assertions, "connected-layout-150-preserves-live-generation", true,
                SameActiveGeneration(activeAt150, expectedConnected) &&
                SameActiveProfile(activeAt150, expectedConnected));
            AssertLogsCase(assertions, "connected-layout-150-keeps-Go-connected", "CONNECTED",
                JsonString(activeAt150, "state"));
            AssertLogsCase(assertions, "connected-layout-150-keeps-24-profile-inventory-digest", digest,
                JsonString(activeAt150, "digest"));
            AssertLogsCase(assertions, "connected-layout-150-keeps-24-profile-inventory", 24,
                JsonArrayLength(activeAt150, "profiles"));
            AssertLogsCase(assertions, "connected-layout-150-keeps-rendered-Connected-status", "Connected",
                ReadRenderedStatus(app));
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
                var current = (int)Math.Round(currentValue);
                if (Math.Abs(currentValue - original) > 0.01)
                {
                    if (Math.Abs(currentValue - current) > 0.01)
                        throw new InvalidOperationException($"Windows Text size became a non-integer value: {currentValue.ToString(CultureInfo.InvariantCulture)}");
                    var restored = RunTextSizeSettingsAction(executable, "apply", original, current,
                        restoreDeadline, restoreCleanupDeadline);
                    RecordSettingsObservation(assertions, "connected-layout-restores-original-os-text-size", restored);
                    AssertLogsCase(assertions, "connected-layout-original-setting-restore-result", (double)original,
                        TextSizeResponseValueAfterAction(restored));
                }
                var final = RunTextSizeSettingsAction(executable, "inspect",
                    caseDeadline: restoreDeadline, cleanupDeadline: restoreCleanupDeadline);
                var finalValue = SettingsTextSizeValue(final);
                AssertLogsCase(assertions, "connected-layout-os-text-size-is-restored", (double)original, finalValue);
                WaitLogsCase(deadline, "connected window reflow after Text size restore", () =>
                {
                    var restoredLayout = ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window);
                    return Equals(restoredLayout.GetValueOrDefault("profile_digest"), digest) &&
                        ProfileLayoutGeometryValid(restoredLayout, requireReadableLogs: true);
                }, "Connected native window did not restore readable layout with the original Text size");
                AddScreenshot(app, "connected-layout", "restored-text-size", deadline,
                    screenshots, screenshotDirectory);
            }
            catch (Exception restoreError)
            {
                cleanupErrors.Add("Windows connected-layout Text size restoration: " + restoreError);
                assertions.Add(new
                {
                    id = "connected-layout-os-text-size-restored",
                    outcome = "failed",
                    expected = original,
                    actual = restoreError.ToString(),
                });
            }
        }
    }

    private static void RunConnectedSessionCase(
        OwnedLogsApp initialApp, JsonElement request, string executable, string fixturePath,
        NativeDiagnostics diagnostics, SubscriptionFixtureStats fixtureBefore,
        string subscriptionUrl, string controlUrl, string controlKey,
        LogsCaseDeadline deadline, List<OwnedLogsApp> launches, List<object> assertions,
        List<object> observerResults, List<string> screenshots, string screenshotDirectory,
        Action<OwnedLogsApp?> publishActiveApp)
    {
        if (!request.TryGetProperty("observer_enabled", out var observerEnabled) ||
            observerEnabled.ValueKind != JsonValueKind.True)
            throw new ArgumentException("connected-session requires the native observer callback channel");

        var app = initialApp;
        publishActiveApp(app);
        var fixtureGets = fixtureBefore.SubscriptionGets;

        // Load the supplied inventory through the real editor and let the production
        // debounce/Configure path finish before starting Auto from its rendered action.
        ApplySubscriptionThroughRenderedControl(app, subscriptionUrl, paste: false, deadline);
        var initialLayout = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 2);
        AssertLogsCase(assertions, "connected-session-configure-field-keeps-requested-url",
            subscriptionUrl, ReadSourceEditor(app));
        AssertProfileRowsHaveSemanticIds(assertions, initialLayout);
        var initialFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(300));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-initial-configure",
            fixtureBefore, initialFixture);
        fixtureGets = initialFixture.SubscriptionGets;

        var configured = WaitNativeSnapshot(request, deadline, "connected-session-configure", snapshot =>
            JsonBoolean(snapshot, "configured") && JsonString(snapshot, "source_url") == subscriptionUrl &&
            JsonArrayLength(snapshot, "profiles") >= 2);
        AssertLogsCase(assertions, "connected-session-configures-idle-inventory", true,
            IsDisconnectedInventory(configured));
        var profileDigest = JsonString(configured, "digest");
        AssertLogsCase(assertions, "connected-session-rendered-digest-matches-Go-inventory",
            profileDigest, initialLayout.GetValueOrDefault("profile_digest"));

        RequestNativeObserver(request, deadline, "prepare-connect");
        WaitNativeAction(app, "VPN connection action", "Auto connect", deadline);
        InvokeRenderedAction(app, "VPN connection action", "Auto connect", deadline);
        var auto = WaitNativeSnapshot(request, deadline, "initial Auto connection", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "active_mode") == "AUTO_SELECT" &&
            JsonObject(snapshot, "active_profile").ValueKind == JsonValueKind.Object);
        AssertLogsCase(assertions, "initial-Auto-connection-retains-requested-inventory",
            profileDigest, JsonString(auto, "digest"));
        WaitRenderedStatus(app, "Connected", deadline);
        RecordNativeObservations(request, deadline, "auto", observerResults, assertions);
        AddScreenshot(app, "connected-session", "auto-connected", deadline, screenshots, screenshotDirectory);

        var activeProfile = JsonObject(auto, "active_profile");
        var activeIndex = JsonInt32(activeProfile, "index");
        var cancelIndex = activeIndex == 1 ? 0 : 1;
        var cancelActionId = ProfileActionId(initialLayout, cancelIndex);
        var cancelCompetingId = ProfileActionId(initialLayout, 1 - cancelIndex);
        var cancelBaseline = GetAction(app, cancelActionId);
        AssertLogsCase(assertions, "profile-switch-cancel-target-starts-as-enabled-Connect", true,
            cancelBaseline is { Name: "Connect", Enabled: true, Offscreen: false });

        var beforeCancel = auto;
        InvokeRenderedAction(app, cancelActionId, "Connect", deadline);
        _ = WaitForProfileStopTransition(
            request, app, deadline, beforeCancel, profileDigest, cancelIndex,
            cancelActionId, cancelCompetingId, "profile-switch-cancel", assertions);
        AddScreenshot(app, "connected-session", "profile-switch-stop-visible", deadline, screenshots, screenshotDirectory);
        InvokeRenderedAction(app, cancelActionId, "Stop", deadline);
        var canceled = WaitNativeSnapshot(request, deadline, "cancel selected profile connection", snapshot =>
            IsDisconnectedInventory(snapshot));
        AssertLogsCase(assertions, "profile-switch-cancel-does-not-move-generation-backward",
            true, JsonInt64(canceled, "generation") >= JsonInt64(beforeCancel, "generation"));
        WaitRenderedStatus(app, "Disconnected", deadline);
        RecordCleanupObservation(request, deadline, "profile-switch-cancel", observerResults, assertions);
        AssertLogsCase(assertions, "profile-switch-cancel-clears-pending-selection", true,
            HasNoPendingOrActiveProfile(canceled));
        AddScreenshot(app, "connected-session", "profile-switch-cancelled", deadline, screenshots, screenshotDirectory);

        var profileTwoId = ProfileActionId(initialLayout, 1);
        var profileOneId = ProfileActionId(initialLayout, 0);
        WaitNativeAction(app, profileTwoId, "Connect", deadline);
        var profileTwoBaseline = GetAction(app, profileTwoId);
        AssertLogsCase(assertions, "Profile-2-action-is-enabled-after-cancellation", true,
            profileTwoBaseline is { Name: "Connect", Enabled: true, Offscreen: false });
        var beforeProfileTwo = canceled;
        InvokeRenderedAction(app, profileTwoId, "Connect", deadline);
        _ = WaitForProfileStopTransition(
            request, app, deadline, beforeProfileTwo, profileDigest, 1,
            profileTwoId, profileOneId, "profile-2-selection", assertions);
        var selected = WaitNativeSnapshot(request, deadline, "Profile 2 connection", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "active_mode") == "PROFILE_INDEX" &&
            JsonInt32(snapshot, "active_index") == 1 &&
            JsonInt64(snapshot, "generation") > JsonInt64(beforeProfileTwo, "generation") &&
            JsonInt32(JsonObject(snapshot, "active_profile"), "index") == 1);
        AssertLogsCase(assertions, "Profile-2-selection-preserves-accepted-digest",
            profileDigest, JsonString(selected, "digest"));
        WaitRenderedStatus(app, "Connected", deadline);
        AddScreenshot(app, "connected-session", "profile-2-connected", deadline, screenshots, screenshotDirectory);

        // The failure-once URL is unique to this attempt. Its first GET must fail,
        // leave the current tunnel and accepted source untouched, and remain idle
        // until the rendered Retry is invoked on that same URL.
        var failureUrl = AppendQuery(RequiredSubscriptionFailureUrl(request),
            "connected-retry=" + Guid.NewGuid().ToString("N"));
        ApplySubscriptionThroughRenderedControl(app, failureUrl, paste: false, deadline);
        WaitNativeAction(app, "Retry", "Retry", deadline);
        var failedFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromSeconds(1.6));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-failed-load",
            new SubscriptionFixtureStats(fixtureGets, failedFixture.InFlightGets, 0), failedFixture);
        fixtureGets = failedFixture.SubscriptionGets;
        var failedSource = WaitNativeSnapshot(request, deadline, "failed Configure preserves active session", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "source_url") == JsonString(selected, "source_url") &&
            SameActiveGeneration(snapshot, selected));
        AssertLogsCase(assertions, "failed-subscription-input-keeps-Profile-2-generation", true,
            SameActiveGeneration(failedSource, selected));
        AssertLogsCase(assertions, "failed-subscription-input-keeps-rendered-Connected-status", true,
            ReadRenderedStatus(app) == "Connected");
        AssertLogsCase(assertions, "failed-subscription-exposes-enabled-Retry", true,
            GetAction(app, "Retry") is { Name: "Retry", Enabled: true, Offscreen: false });
        AssertLogsCase(assertions, "failed-subscription-keeps-rendered-Disconnect-enabled", true,
            FindRenderedDisconnect(app, failedSource) is not null);
        AddScreenshot(app, "connected-session", "failed-load-retry-visible", deadline, screenshots, screenshotDirectory);

        InvokeRenderedAction(app, "Retry", "Retry", deadline);
        var retryLayout = WaitForAcceptedProfiles(app, failureUrl, deadline, minimumRows: 2);
        AssertLogsCase(assertions, "Retry-keeps-the-failed-attempts-exact-URL", failureUrl,
            ReadSourceEditor(app));
        var retriedFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(300));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-retry",
            new SubscriptionFixtureStats(fixtureGets, retriedFixture.InFlightGets, 0), retriedFixture);
        fixtureGets = retriedFixture.SubscriptionGets;
        var retried = WaitNativeSnapshot(request, deadline, "Retry accepts the exact failed URL", snapshot =>
            JsonString(snapshot, "source_url") == failureUrl && SameActiveGeneration(snapshot, selected));
        AssertLogsCase(assertions, "Retry-keeps-the-same-profile-inventory-digest",
            profileDigest, JsonString(retried, "digest"));
        AssertLogsCase(assertions, "Retry-keeps-semantic-profile-row-identifiers", true,
            SameProfileAutomationIds(initialLayout, retryLayout));

        var warmUrl = AppendQuery(subscriptionUrl, "connected-warm-import=" + Guid.NewGuid().ToString("N"));
        var warmFixtureBefore = fixtureGets;
        var initialWarmPid = app.Process.Id;
        var initialWarmStartTicks = app.StartTicks;
        var initialWarmWindow = app.Window;
        TrackWarmProtocolActivation(app, warmUrl, deadline, launches, assertions);
        WaitForProtocolImportEvent(diagnostics, warmUrl, deadline);
        var warmLayout = WaitForAcceptedProfiles(app, warmUrl, deadline, minimumRows: 2);
        var warmFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, warmFixtureBefore + 1, deadline, TimeSpan.FromMilliseconds(800));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-warm-import",
            new SubscriptionFixtureStats(warmFixtureBefore, warmFixture.InFlightGets, 0), warmFixture);
        fixtureGets = warmFixture.SubscriptionGets;
        VerifyOwnedLogsApp(app);
        GetWindowThreadProcessId(initialWarmWindow, out var warmWindowOwner);
        AssertLogsCase(assertions, "warm-import-retains-exact-app-process", initialWarmPid, app.Process.Id);
        AssertLogsCase(assertions, "warm-import-retains-process-creation-identity", initialWarmStartTicks, app.StartTicks);
        AssertLogsCase(assertions, "warm-import-retains-owned-window", true,
            initialWarmWindow == app.Window && warmWindowOwner == (uint)initialWarmPid);
        AssertLogsCase(assertions, "warm-import-preserves-inventory-digest", profileDigest,
            warmLayout.GetValueOrDefault("profile_digest"));
        var warmed = WaitNativeSnapshot(request, deadline, "warm OS import keeps Profile 2 active", snapshot =>
            JsonString(snapshot, "source_url") == warmUrl && SameActiveGeneration(snapshot, selected) &&
            JsonString(snapshot, "digest") == profileDigest);
        AssertLogsCase(assertions, "warm-import-preserves-Profile-2-generation", true,
            SameActiveGeneration(warmed, selected));
        AddScreenshot(app, "connected-session", "warm-import-connected", deadline, screenshots, screenshotDirectory);

        var savedUrl = JsonString(warmed, "source_url");
        DisconnectRenderedAndVerifyCleanup(request, app, warmed, deadline,
            observerResults, assertions, "connected-session-first-disconnect");
        AddScreenshot(app, "connected-session", "disconnected", deadline, screenshots, screenshotDirectory);
        CloseOwnedLogsApp(app, deadline);
        app.Window = IntPtr.Zero;
        publishActiveApp(null);

        var restart = RequestNativeObserver(request, deadline, "restart-service");
        AssertLogsCase(assertions, "connected-session-restart-confirms-process-loss", true,
            JsonBoolean(restart, "process_loss_verified"));
        var empty = WaitNativeSnapshot(request, deadline, "backend restart preserves only saved URL", snapshot =>
            !JsonBoolean(snapshot, "configured") && JsonArrayLength(snapshot, "profiles") == 0 &&
            JsonString(snapshot, "source_url") == savedUrl &&
            (JsonString(snapshot, "state") is "IDLE" or "CONFIGURED"));
        AssertLogsCase(assertions, "backend-restart-keeps-saved-URL-without-inventory", savedUrl,
            JsonString(empty, "source_url"));
        AssertLogsCase(assertions, "backend-restart-starts-with-no-active-profile", true,
            HasNoPendingOrActiveProfile(empty));

        app = StartOwnedLogsApp(executable, fixturePath, deadline, launches);
        publishActiveApp(app);
        WaitForLogsViewport(app, deadline);
        var restoreLayout = WaitForAcceptedProfiles(app, savedUrl, deadline, minimumRows: 2);
        var restoredFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(800));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-saved-url-restore",
            new SubscriptionFixtureStats(fixtureGets, restoredFixture.InFlightGets, 0), restoredFixture);
        fixtureGets = restoredFixture.SubscriptionGets;
        AssertLogsCase(assertions, "saved-URL-reopen-restores-same-profile-digest", profileDigest,
            restoreLayout.GetValueOrDefault("profile_digest"));
        var restoredIdle = WaitNativeSnapshot(request, deadline, "saved inventory restores without connection", snapshot =>
            JsonBoolean(snapshot, "configured") && JsonString(snapshot, "source_url") == savedUrl &&
            JsonArrayLength(snapshot, "profiles") >= 2 && IsDisconnectedInventory(snapshot));
        AssertLogsCase(assertions, "saved-URL-reopen-remains-disconnected", true,
            IsDisconnectedInventory(restoredIdle));
        WaitRenderedStatus(app, "Disconnected", deadline);
        AddScreenshot(app, "connected-session", "saved-inventory-restored", deadline, screenshots, screenshotDirectory);

        RequestNativeObserver(request, deadline, "prepare-connect");
        WaitNativeAction(app, "VPN connection action", "Auto connect", deadline);
        InvokeRenderedAction(app, "VPN connection action", "Auto connect", deadline);
        var reconnected = WaitNativeSnapshot(request, deadline, "Auto reconnect after saved inventory restore", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "active_mode") == "AUTO_SELECT" &&
            JsonObject(snapshot, "active_profile").ValueKind == JsonValueKind.Object);
        AssertLogsCase(assertions, "saved-inventory-reconnect-retains-digest", profileDigest,
            JsonString(reconnected, "digest"));
        WaitRenderedStatus(app, "Connected", deadline);
        RecordNativeObservations(request, deadline, "reconnect", observerResults, assertions);

        var coldUrl = AppendQuery(subscriptionUrl, "connected-cold-import=" + Guid.NewGuid().ToString("N"));
        var beforeCold = reconnected;
        var coldFixtureBefore = fixtureGets;
        var oldColdPid = app.Process.Id;
        var oldColdStartTicks = app.StartTicks;
        CloseOwnedLogsApp(app, deadline);
        app.Window = IntPtr.Zero;
        publishActiveApp(null);
        app = StartOwnedProtocolApp(executable, fixturePath, coldUrl, deadline, launches);
        publishActiveApp(app);
        WaitForLogsViewport(app, deadline);
        WaitForProtocolImportEvent(diagnostics, coldUrl, deadline);
        var coldLayout = WaitForAcceptedProfiles(app, coldUrl, deadline, minimumRows: 2);
        var coldFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, coldFixtureBefore + 1, deadline, TimeSpan.FromMilliseconds(800));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-cold-import",
            new SubscriptionFixtureStats(coldFixtureBefore, coldFixture.InFlightGets, 0), coldFixture);
        fixtureGets = coldFixture.SubscriptionGets;
        var coldImported = WaitNativeSnapshot(request, deadline, "cold OS import retains the active Auto generation", snapshot =>
            JsonString(snapshot, "source_url") == coldUrl && SameActiveGeneration(snapshot, beforeCold));
        AssertLogsCase(assertions, "cold-import-retains-existing-app-process-is-replaced", true,
            app.Process.Id != oldColdPid && app.StartTicks != oldColdStartTicks);
        AssertLogsCase(assertions, "cold-import-retains-profile-digest", profileDigest,
            coldLayout.GetValueOrDefault("profile_digest"));
        AssertLogsCase(assertions, "cold-import-does-not-interrupt-active-generation", true,
            SameActiveGeneration(coldImported, beforeCold));
        AddScreenshot(app, "connected-session", "cold-import-connected", deadline, screenshots, screenshotDirectory);

        CloseOwnedLogsApp(app, deadline);
        app.Window = IntPtr.Zero;
        publishActiveApp(null);
        app = StartOwnedLogsApp(executable, fixturePath, deadline, launches);
        publishActiveApp(app);
        WaitForLogsViewport(app, deadline);
        var reopenedLayout = WaitForAcceptedProfiles(app, coldUrl, deadline, minimumRows: 2);
        var reusedFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets, deadline, TimeSpan.FromMilliseconds(1600));
        AssertLogsCase(assertions, "cold-import-reopen-does-not-refetch-inventory", fixtureGets,
            reusedFixture.SubscriptionGets);
        AssertLogsCase(assertions, "cold-import-reopen-has-no-request-in-flight", 0,
            reusedFixture.InFlightGets);
        AssertLogsCase(assertions, "cold-import-reopen-reuses-identical-digest", profileDigest,
            reopenedLayout.GetValueOrDefault("profile_digest"));
        var reused = WaitNativeSnapshot(request, deadline, "reopened app reuses active inventory", snapshot =>
            JsonString(snapshot, "source_url") == coldUrl && SameActiveGeneration(snapshot, beforeCold) &&
            JsonString(snapshot, "digest") == profileDigest);
        AssertLogsCase(assertions, "cold-import-reopen-keeps-active-generation", true,
            SameActiveGeneration(reused, beforeCold));
        AddScreenshot(app, "connected-session", "reopened-inventory-reused", deadline, screenshots, screenshotDirectory);

        var loss = RequestNativeObserver(request, deadline, "restart-service");
        AssertLogsCase(assertions, "process-loss-restart-confirms-service-disappearance", true,
            JsonBoolean(loss, "process_loss_verified"));
        var restoredAfterLoss = WaitForAcceptedProfiles(app, coldUrl, deadline, minimumRows: 2);
        var processLossFixture = WaitForSubscriptionFixtureGets(
            controlUrl, controlKey, fixtureGets + 1, deadline, TimeSpan.FromMilliseconds(800));
        AssertSubscriptionFixtureRequest(assertions, "connected-session-process-loss-inventory-reload",
            new SubscriptionFixtureStats(fixtureGets, processLossFixture.InFlightGets, 0), processLossFixture);
        fixtureGets = processLossFixture.SubscriptionGets;
        AssertLogsCase(assertions, "process-loss-inventory-reload-retains-digest", profileDigest,
            restoredAfterLoss.GetValueOrDefault("profile_digest"));
        var processLossIdle = WaitNativeSnapshot(request, deadline, "replacement service reloads saved URL before reconnect", snapshot =>
            JsonBoolean(snapshot, "configured") && JsonString(snapshot, "source_url") == coldUrl &&
            (JsonString(snapshot, "state") is "IDLE" or "CONFIGURED") &&
            JsonArrayLength(snapshot, "profiles") >= 2);
        AssertLogsCase(assertions, "process-loss-inventory-reload-does-not-auto-connect", true,
            IsDisconnectedInventory(processLossIdle));
        RequestNativeObserver(request, deadline, "prepare-connect");
        WaitNativeAction(app, "VPN connection action", "Auto connect", deadline);
        InvokeRenderedAction(app, "VPN connection action", "Auto connect", deadline);
        var processRecovered = WaitNativeSnapshot(request, deadline, "native reconnect after Go process loss", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "active_mode") == "AUTO_SELECT" &&
            JsonObject(snapshot, "active_profile").ValueKind == JsonValueKind.Object);
        AssertLogsCase(assertions, "process-loss-reconnect-retains-inventory-digest", profileDigest,
            JsonString(processRecovered, "digest"));
        WaitRenderedStatus(app, "Connected", deadline);
        RecordNativeObservations(request, deadline, "process-loss", observerResults, assertions);
        AddScreenshot(app, "connected-session", "process-loss-recovered", deadline, screenshots, screenshotDirectory);

        DisconnectRenderedAndVerifyCleanup(request, app, processRecovered, deadline,
            observerResults, assertions, "connected-session-final-disconnect");
        AddScreenshot(app, "connected-session", "final-disconnect", deadline, screenshots, screenshotDirectory);
    }

    private static void RunAutoRecoveryStopCase(
        OwnedLogsApp app, JsonElement request, string subscriptionUrl,
        string controlUrl, string controlKey, LogsCaseDeadline deadline,
        List<object> assertions, List<object> observerResults,
        List<string> screenshots, string screenshotDirectory)
    {
        if (!request.TryGetProperty("observer_enabled", out var observerEnabled) ||
            observerEnabled.ValueKind != JsonValueKind.True)
            throw new ArgumentException("auto-recovery-stop requires the native observer callback channel");

        var layout = WaitForAcceptedProfiles(app, subscriptionUrl, deadline, minimumRows: 2);
        AssertProfileRowsHaveSemanticIds(assertions, layout);
        var configured = WaitNativeSnapshot(request, deadline, "auto-recovery-stop Configure", snapshot =>
            JsonBoolean(snapshot, "configured") && JsonString(snapshot, "source_url") == subscriptionUrl &&
            JsonArrayLength(snapshot, "profiles") >= 2 && IsDisconnectedInventory(snapshot));
        var digest = JsonString(configured, "digest");
        AssertLogsCase(assertions, "auto-recovery-stop-configures-idle-inventory", true,
            JsonBoolean(configured, "configured") && IsDisconnectedInventory(configured));
        AssertLogsCase(assertions, "auto-recovery-stop-rendered-digest-matches-Go", digest,
            layout.GetValueOrDefault("profile_digest"));

        RequestNativeObserver(request, deadline, "prepare-connect");
        WaitNativeAction(app, "VPN connection action", "Auto connect", deadline);
        InvokeRenderedAction(app, "VPN connection action", "Auto connect", deadline);
        var connected = WaitNativeSnapshot(request, deadline, "auto-recovery-stop initial Auto connection", snapshot =>
            JsonString(snapshot, "state") == "CONNECTED" &&
            JsonString(snapshot, "active_mode") == "AUTO_SELECT" &&
            JsonObject(snapshot, "active_profile").ValueKind == JsonValueKind.Object);
        var initialGeneration = JsonInt64(connected, "generation");
        AssertLogsCase(assertions, "auto-recovery-stop-connection-retains-inventory-digest", digest,
            JsonString(connected, "digest"));
        WaitRenderedStatus(app, "Connected", deadline);
        RecordNativeObservations(request, deadline, "auto", observerResults, assertions);

        var markerValue = request.TryGetProperty("auto_recovery_marker", out var markerElement) &&
            markerElement.ValueKind == JsonValueKind.String ? markerElement.GetString() : null;
        if (string.IsNullOrWhiteSpace(markerValue))
            throw new ArgumentException("auto-recovery-stop requires its build-local auto_recovery_marker path");
        var markerPath = Path.GetFullPath(markerValue);
        if (File.Exists(markerPath))
            throw new IOException($"Auto-recovery fault marker already exists before the verified Auto connection: {markerPath}");
        Directory.CreateDirectory(Path.GetDirectoryName(markerPath)!);
        using (var output = new FileStream(markerPath, FileMode.CreateNew, FileAccess.Write, FileShare.Read))
        {
            var markerBytes = new System.Text.UTF8Encoding(encoderShouldEmitUTF8Identifier: false)
                .GetBytes("armed after real route and traffic\n");
            output.Write(markerBytes);
            output.Flush(true);
        }
        AssertLogsCase(assertions, "auto-recovery-marker-is-written-after-independent-observations", true,
            File.ReadAllText(markerPath) == "armed after real route and traffic\n");

        var recovery = WaitNativeSnapshot(request, deadline, "health fault enters a later recovery generation", snapshot =>
            JsonBoolean(snapshot, "recovering") && JsonInt64(snapshot, "generation") > initialGeneration);
        var recoveryGeneration = JsonInt64(recovery, "generation");
        AssertLogsCase(assertions, "fault-enters-a-later-generation", true,
            JsonBoolean(recovery, "recovering") && recoveryGeneration > initialGeneration);
        WaitNativeAction(app, "VPN connection action", "Stop", deadline);
        var profileActionIds = ProfileRows(layout)
            .Select(row => row.GetValueOrDefault("action_automation_id") as string)
            .Where(id => !string.IsNullOrWhiteSpace(id))
            .Cast<string>()
            .ToArray();
        WaitLogsCase(deadline, "profile Connect controls disable during Auto recovery", () =>
        {
            var main = GetAction(app, "VPN connection action");
            return main is { Name: "Stop", Enabled: true, Offscreen: false } &&
                profileActionIds.Length > 0 && profileActionIds.All(id =>
                    GetAction(app, id) is { Enabled: false, Offscreen: false });
        }, "Auto recovery did not render Stop while disabling competing profile actions");
        foreach (var (id, index) in profileActionIds.Select((id, index) => (id, index)))
            AssertLogsCase(assertions, $"recovery-disables-profile-action-{index + 1}", true,
                GetAction(app, id) is { Enabled: false, Offscreen: false });
        AddScreenshot(app, "auto-recovery-stop", "recovery-stop-enabled", deadline,
            screenshots, screenshotDirectory);

        InvokeRenderedAction(app, "VPN connection action", "Stop", deadline);
        var stopped = WaitNativeSnapshot(request, deadline, "rendered Stop cancels Auto recovery", snapshot =>
            (JsonString(snapshot, "state") is "IDLE" or "CONFIGURED") &&
            JsonBoolean(snapshot, "cleanup_complete") &&
            !JsonBoolean(snapshot, "recovering") &&
            HasNoPendingOrActiveProfile(snapshot));
        var stoppedGeneration = JsonInt64(stopped, "generation");
        AssertLogsCase(assertions, "Stop-clears-recovery-pending-and-active-targets", true,
            !JsonBoolean(stopped, "recovering") && HasNoPendingOrActiveProfile(stopped));
        AssertLogsCase(assertions, "Stop-completes-Go-cleanup", true,
            JsonBoolean(stopped, "cleanup_complete"));
        WaitRenderedStatus(app, "Disconnected", deadline);
        RecordCleanupObservation(request, deadline, "auto-recovery-stop", observerResults, assertions);

        var stabilityStarted = Stopwatch.GetTimestamp();
        var stabilitySamples = 0;
        while (Stopwatch.GetElapsedTime(stabilityStarted) < TimeSpan.FromSeconds(3))
        {
            var latest = RequestNativeObserver(request, deadline, "snapshot");
            stabilitySamples++;
            AssertLogsCase(assertions, $"idle-after-recovery-keeps-generation-sample-{stabilitySamples}",
                stoppedGeneration, JsonInt64(latest, "generation"));
            AssertLogsCase(assertions, $"idle-after-recovery-does-not-resume-sample-{stabilitySamples}", true,
                (JsonString(latest, "state") is "IDLE" or "CONFIGURED") &&
                !JsonBoolean(latest, "recovering") && JsonBoolean(latest, "cleanup_complete") &&
                HasNoPendingOrActiveProfile(latest));
            Thread.Sleep(250);
        }
        AssertLogsCase(assertions, "recovery-remains-idle-for-at-least-three-seconds", true,
            Stopwatch.GetElapsedTime(stabilityStarted) >= TimeSpan.FromSeconds(3) && stabilitySamples >= 3);
        AddScreenshot(app, "auto-recovery-stop", "idle-after-recovery", deadline,
            screenshots, screenshotDirectory);
    }

    private static string RequiredSubscriptionFailureUrl(JsonElement request)
    {
        var value = request.TryGetProperty("subscription_failure_url", out var failureValue) &&
            failureValue.ValueKind == JsonValueKind.String ? failureValue.GetString() : null;
        if (string.IsNullOrWhiteSpace(value) || !Uri.TryCreate(value, UriKind.Absolute, out var uri) ||
            uri.Scheme != Uri.UriSchemeHttps || string.IsNullOrWhiteSpace(uri.Host))
            throw new ArgumentException("connected-session requires an absolute HTTPS subscription_failure_url");
        return value;
    }

    private static JsonElement RequestNativeObserver(
        JsonElement request, LogsCaseDeadline deadline, string operation, string? phase = null)
    {
        deadline.Check("native observer " + operation);
        if (!request.TryGetProperty("observer_enabled", out var enabled) || enabled.ValueKind != JsonValueKind.True)
            throw new InvalidOperationException("Native observer callback was requested without an enabled duplex channel");
        var remaining = deadline.RemainingSeconds;
        if (remaining <= 0) throw new TimeoutException("Native observer callback started after the case deadline");
        var observation = new Dictionary<string, object?>
        {
            ["operation"] = operation,
            ["timeout_seconds"] = Math.Min(30.0, remaining),
        };
        if (!string.IsNullOrWhiteSpace(phase)) observation["phase"] = phase;
        Console.Out.WriteLine(NativeObserverPrefix + JsonSerializer.Serialize(observation));
        Console.Out.Flush();

        var responseLine = Console.In.ReadLine()
            ?? throw new EndOfStreamException($"Native observer channel closed before replying to {operation}");
        using var response = JsonDocument.Parse(responseLine);
        var root = response.RootElement;
        if (!root.TryGetProperty("ok", out var ok) || ok.ValueKind != JsonValueKind.True)
        {
            var error = root.TryGetProperty("error", out var errorValue) && errorValue.ValueKind == JsonValueKind.String
                ? errorValue.GetString() : "observer response omitted the original error";
            throw new InvalidOperationException($"Native observer {operation} failed: {error}");
        }
        if (!root.TryGetProperty("value", out var value) || value.ValueKind != JsonValueKind.Object)
            throw new InvalidDataException($"Native observer {operation} returned no object value");
        deadline.Check("native observer " + operation + " response");
        return value.Clone();
    }

    private static void RecordNativeObservations(
        JsonElement request, LogsCaseDeadline deadline, string phase,
        List<object> observerResults, List<object> assertions)
    {
        var observed = RequestNativeObserver(request, deadline, "observe", phase);
        foreach (var field in new[] { "tunnel_interface", "routing_verified", "stability_verified", "throughput_positive" })
            AssertLogsCase(assertions, $"{phase}-independent-{field}", true, JsonBoolean(observed, field));
        var metrics = new Dictionary<string, object?> { ["phase"] = phase };
        foreach (var field in new[] { "latency_ms", "download_mbps", "upload_mbps" })
        {
            var value = observed.GetProperty(field);
            var valid = value.ValueKind == JsonValueKind.Number && value.TryGetDouble(out var number) &&
                double.IsFinite(number) && number > 0;
            AssertLogsCase(assertions, $"{phase}-independent-{field}-is-positive-finite", true, valid);
            metrics[field] = value.Clone();
        }
        if (observed.TryGetProperty("stability_sample_count", out var sampleCount))
        {
            var count = sampleCount.GetInt32();
            AssertLogsCase(assertions, $"{phase}-independent-stability-has-samples", true, count > 0);
            metrics["stability_sample_count"] = count;
        }
        foreach (var field in new[] { "tunnel_interface", "routing_verified", "stability_verified", "throughput_positive" })
            metrics[field] = JsonBoolean(observed, field);
        observerResults.Add(metrics);
    }

    private static void RecordCleanupObservation(
        JsonElement request, LogsCaseDeadline deadline, string phase,
        List<object> observerResults, List<object> assertions)
    {
        var observed = RequestNativeObserver(request, deadline, "cleanup");
        var connected = JsonBoolean(observed, "connected");
        var verified = JsonBoolean(observed, "cleanup_verified");
        AssertLogsCase(assertions, $"{phase}-no-live-tunnel-after-cleanup", false, connected);
        AssertLogsCase(assertions, $"{phase}-adapter-confirms-cleanup", true, verified);
        observerResults.Add(new { phase, operation = "cleanup", connected, cleanup_verified = verified });
    }

    private static void DisconnectRenderedAndVerifyCleanup(
        JsonElement request, OwnedLogsApp app, JsonElement before,
        LogsCaseDeadline deadline, List<object> observerResults, List<object> assertions, string phase)
    {
        var actionId = ActiveDisconnectActionId(app, before);
        WaitNativeAction(app, actionId, "Disconnect", deadline);
        InvokeRenderedAction(app, actionId, "Disconnect", deadline);
        var disconnected = WaitNativeSnapshot(request, deadline, phase, IsDisconnectedInventory);
        AssertLogsCase(assertions, phase + "-clears-active-profile-and-pending-target", true,
            HasNoPendingOrActiveProfile(disconnected));
        AssertLogsCase(assertions, phase + "-marks-Go-cleanup-complete", true,
            JsonBoolean(disconnected, "cleanup_complete"));
        WaitRenderedStatus(app, "Disconnected", deadline);
        RecordCleanupObservation(request, deadline, phase, observerResults, assertions);
    }

    private static string ActiveDisconnectActionId(OwnedLogsApp app, JsonElement snapshot)
    {
        var mode = JsonString(snapshot, "active_mode");
        if (mode == "AUTO_SELECT") return "VPN connection action";
        if (mode != "PROFILE_INDEX")
            throw new InvalidOperationException($"Connected session has unsupported active mode: {mode}");
        var index = JsonInt32(snapshot, "active_index");
        var layout = ProfileListLayout(AutomationElement.FromHandle(app.Window), app.Window);
        if (!string.Equals(JsonString(snapshot, "digest"),
                layout.GetValueOrDefault("profile_digest") as string, StringComparison.Ordinal))
            throw new InvalidOperationException("Active Go digest differs from the rendered profile inventory before Disconnect");
        return ProfileActionId(layout, index);
    }

    private static JsonElement WaitForProfileStopTransition(
        JsonElement request, OwnedLogsApp app, LogsCaseDeadline deadline,
        JsonElement previous, string digest, int index, string targetId, string competingId, string stage,
        List<object> assertions)
    {
        JsonElement latest = default;
        var stopObserved = false;
        while (deadline.RemainingSeconds > 0)
        {
            latest = RequestNativeObserver(request, deadline, "snapshot");
            var pending = JsonObject(latest, "pending_target");
            var hasPending = pending.ValueKind == JsonValueKind.Object &&
                JsonString(pending, "mode") == "PROFILE_INDEX" &&
                JsonInt32(pending, "index") == index && JsonString(pending, "digest") == digest;
            var activeAttempt = (JsonString(latest, "state") is "PROBING" or "PREPARING") &&
                JsonString(latest, "active_mode") == "PROFILE_INDEX" &&
                JsonInt32(latest, "active_index") == index && JsonString(latest, "active_digest") == digest;
            if (hasPending || activeAttempt)
            {
                try
                {
                    var target = GetAction(app, targetId);
                    var competing = GetAction(app, competingId);
                    var main = GetAction(app, "VPN connection action");
                    if (target is { Name: "Stop", Enabled: true, Offscreen: false } &&
                        competing is { Name: "Connect", Enabled: false, Offscreen: false } && main is not null)
                    {
                        stopObserved = true;
                        AssertLogsCase(assertions, stage + "-target-is-rendered-enabled-Stop",
                            true, target.Value.Enabled && target.Value.Name == "Stop");
                        AssertLogsCase(assertions, stage + "-competing-profile-Connect-is-disabled",
                            true, !competing.Value.Enabled && competing.Value.Name == "Connect");
                        return latest;
                    }
                }
                catch (ElementNotAvailableException) { }
            }
            if (JsonString(latest, "state") == "FAILED")
                throw new InvalidOperationException($"{stage} failed while waiting for its rendered Stop: {JsonSerializer.Serialize(latest)}");
            if (JsonString(latest, "state") == "CONNECTED" &&
                JsonInt64(latest, "generation") > JsonInt64(previous, "generation") &&
                JsonString(latest, "active_mode") == "PROFILE_INDEX" && JsonInt32(latest, "active_index") == index)
                throw new InvalidOperationException($"{stage} completed before the rendered Stop transition could be canceled");
            Thread.Sleep(Math.Max(1, Math.Min(100, (int)Math.Ceiling(deadline.RemainingSeconds * 1000))));
        }
        throw new TimeoutException(
            $"{stage} did not render an enabled Stop and disabled competing profile action before the case deadline; " +
            $"stop_observed={stopObserved}; latest_snapshot={JsonSerializer.Serialize(latest)}");
    }

    private static JsonElement WaitNativeSnapshot(
        JsonElement request, LogsCaseDeadline deadline, string stage, Func<JsonElement, bool> ready)
    {
        JsonElement latest = default;
        while (deadline.RemainingSeconds > 0)
        {
            latest = RequestNativeObserver(request, deadline, "snapshot");
            if (ready(latest)) return latest;
            if (JsonString(latest, "state") == "FAILED")
                throw new InvalidOperationException($"{stage} observed a failed Go session: {JsonSerializer.Serialize(latest)}");
            Thread.Sleep(Math.Max(1, Math.Min(100, (int)Math.Ceiling(deadline.RemainingSeconds * 1000))));
        }
        throw new TimeoutException($"{stage} did not reach its Go snapshot condition: {JsonSerializer.Serialize(latest)}");
    }

    private static void WaitRenderedStatus(OwnedLogsApp app, string expected, LogsCaseDeadline deadline) =>
        WaitLogsCase(deadline, "rendered connection status " + expected,
            () => ReadRenderedStatus(app) == expected,
            $"Rendered connection.status did not become {expected}");

    private static string ReadRenderedStatus(OwnedLogsApp app)
    {
        var status = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), "connection.status")
            ?? throw new InvalidOperationException("Native UI omitted semantic connection.status");
        var current = status.Current;
        if (current.ControlType != ControlType.Text || !current.IsControlElement)
            throw new InvalidOperationException("connection.status is not the rendered status text control");
        return current.Name;
    }

    private static void WaitNativeAction(
        OwnedLogsApp app, string automationId, string name, LogsCaseDeadline deadline) =>
        WaitLogsCase(deadline, "rendered action " + automationId,
            () => GetAction(app, automationId) is { Name: var actualName, Enabled: true, Offscreen: false } && actualName == name,
            $"Rendered action {automationId} did not become visible and enabled as {name}");

    private static ConnectedActionSample? GetAction(OwnedLogsApp app, string automationId)
    {
        var element = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), automationId);
        if (element is null) return null;
        var current = element.Current;
        return new ConnectedActionSample(current.AutomationId, current.Name,
            current.ControlType.ProgrammaticName, current.IsEnabled, current.IsOffscreen);
    }

    private static AutomationElement InvokeRenderedAction(
        OwnedLogsApp app, string automationId, string expectedName, LogsCaseDeadline deadline)
    {
        deadline.Check("invoke rendered action " + automationId);
        var element = FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), automationId)
            ?? throw new InvalidOperationException($"Native UI omitted rendered action {automationId}");
        var current = element.Current;
        if (!current.IsControlElement || current.ControlType != ControlType.Button ||
            current.Name != expectedName || !current.IsEnabled || current.IsOffscreen)
            throw new InvalidOperationException(
                $"Rendered action {automationId} was not a visible, enabled {expectedName} button: " +
                JsonSerializer.Serialize(new
                {
                    automation_id = current.AutomationId,
                    name = current.Name,
                    control_type = current.ControlType.ProgrammaticName,
                    enabled = current.IsEnabled,
                    offscreen = current.IsOffscreen,
                    bounds = RectJson(current.BoundingRectangle),
                }));
        RequireForeground(app.Window, "native connected-session action " + automationId);
        if (!element.TryGetCurrentPattern(InvokePattern.Pattern, out var pattern))
            throw new InvalidOperationException($"Rendered action {automationId} does not expose InvokePattern");
        ((InvokePattern)pattern).Invoke();
        return element;
    }

    private static AutomationElement? FindRenderedDisconnect(OwnedLogsApp app, JsonElement snapshot)
    {
        var id = ActiveDisconnectActionId(app, snapshot);
        var action = GetAction(app, id);
        if (action is not { Name: "Disconnect", Enabled: true, Offscreen: false, ControlType: "ControlType.Button" })
            return null;
        return FindUniqueAutomationId(AutomationElement.FromHandle(app.Window), id);
    }

    private static string ProfileActionId(Dictionary<string, object?> layout, int index)
    {
        var row = ProfileRows(layout).SingleOrDefault(candidate =>
            candidate.GetValueOrDefault("profile_index") is int candidateIndex && candidateIndex == index)
            ?? throw new InvalidOperationException($"Rendered profile index {index} is not currently realized");
        var id = row.GetValueOrDefault("action_automation_id") as string;
        var digest = layout.GetValueOrDefault("profile_digest") as string;
        var expected = string.IsNullOrWhiteSpace(digest) ? "" : ProfileAutomationIds.Create(digest, index, "action");
        if (string.IsNullOrWhiteSpace(id) || id != expected)
            throw new InvalidOperationException($"Rendered Profile {index + 1} action identifier was not canonical: {id}");
        return id;
    }

    private static string AppendQuery(string url, string query) =>
        url + (url.Contains('?') ? "&" : "?") + query;

    private static bool SameActiveGeneration(JsonElement current, JsonElement previous) =>
        JsonString(current, "state") == "CONNECTED" &&
        JsonInt64(current, "generation") == JsonInt64(previous, "generation") &&
        JsonString(current, "active_digest") == JsonString(previous, "active_digest") &&
        JsonString(current, "active_mode") == JsonString(previous, "active_mode") &&
        JsonInt32(current, "active_index") == JsonInt32(previous, "active_index");

    private static bool SameActiveProfile(JsonElement current, JsonElement previous)
    {
        var currentProfile = JsonObject(current, "active_profile");
        var previousProfile = JsonObject(previous, "active_profile");
        return currentProfile.ValueKind == JsonValueKind.Object &&
            previousProfile.ValueKind == JsonValueKind.Object &&
            JsonInt32(currentProfile, "index") == JsonInt32(previousProfile, "index") &&
            JsonString(currentProfile, "protocol") == JsonString(previousProfile, "protocol") &&
            JsonString(currentProfile, "description") == JsonString(previousProfile, "description");
    }

    private static bool IsDisconnectedInventory(JsonElement snapshot) =>
        (JsonString(snapshot, "state") is "IDLE" or "CONFIGURED") && HasNoPendingOrActiveProfile(snapshot);

    private static bool HasNoPendingOrActiveProfile(JsonElement snapshot) =>
        (JsonObject(snapshot, "pending_target").ValueKind is JsonValueKind.Undefined or JsonValueKind.Null) &&
        (JsonObject(snapshot, "active_profile").ValueKind is JsonValueKind.Undefined or JsonValueKind.Null);

    private static int JsonArrayLength(JsonElement value, string name) =>
        value.TryGetProperty(name, out var property) && property.ValueKind == JsonValueKind.Array
            ? property.GetArrayLength() : 0;

    private static JsonElement JsonObject(JsonElement value, string name) =>
        value.TryGetProperty(name, out var property) ? property : default;

    private static string JsonString(JsonElement value, string name) =>
        value.TryGetProperty(name, out var property) && property.ValueKind == JsonValueKind.String
            ? property.GetString() ?? "" : "";

    private static bool JsonBoolean(JsonElement value, string name) =>
        value.TryGetProperty(name, out var property) && property.ValueKind == JsonValueKind.True;

    private static long JsonInt64(JsonElement value, string name) =>
        value.TryGetProperty(name, out var property) && property.TryGetInt64(out var number)
            ? number : throw new InvalidDataException($"Go snapshot omitted integer field {name}");

    private static int JsonInt32(JsonElement value, string name) =>
        value.TryGetProperty(name, out var property) && property.TryGetInt32(out var number)
            ? number : throw new InvalidDataException($"Go snapshot omitted integer field {name}");
}
