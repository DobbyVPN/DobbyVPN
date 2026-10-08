import Combine
@testable import DobbyNativeUI
import Foundation
import XCTest

final class SessionViewModelTests: XCTestCase {
    @MainActor
    func testTypedEditsWaitForDebounceAndOnlyConfigureLatestURL() async throws {
        let fixture = try ViewModelFixture()
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sessionID == "test-session" }

        let first = "https://example.invalid/first"
        let latest = "https://example.invalid/latest"
        model.sourceChanged(first)
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertTrue(fixture.client.startedConfigureSources.isEmpty, "Typed input must still be debounced")

        model.sourceChanged(latest)
        let latestEditAt = Date()
        try await Task.sleep(nanoseconds: 350_000_000)
        XCTAssertTrue(fixture.client.startedConfigureSources.isEmpty, "A later edit restarts the debounce interval")
        let configured = await waitUntil(timeout: 2) { fixture.client.startedConfigureSources.count == 1 }
        XCTAssertTrue(configured)
        XCTAssertGreaterThanOrEqual(Date().timeIntervalSince(latestEditAt), 0.38)
        XCTAssertEqual(fixture.client.startedConfigureSources, [latest])
        await waitForSnapshot(model) { $0.sourceURL == latest }
        XCTAssertEqual(model.sourceText, latest)
        XCTAssertTrue(model.inventoryReady)
    }

    @MainActor
    func testRepeatedUnchangedTypedValueDoesNotConfigureAgain() async throws {
        let fixture = try ViewModelFixture()
        let source = "https://example.invalid/held-typed-source"
        fixture.client.holdConfigure(source)
        defer {
            fixture.client.releaseConfigure(source)
            try? FileManager.default.removeItem(at: fixture.directory)
        }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sessionID == "test-session" }

        model.sourceChanged(source)
        let firstStarted = await waitUntil(timeout: 2) {
            fixture.client.startedConfigureSources == [source]
        }
        XCTAssertTrue(firstStarted)

        model.sourceChanged(source)
        try await Task.sleep(nanoseconds: 450_000_000)
        XCTAssertTrue(model.loading, "The original Configure should remain held during the repeated edit")
        XCTAssertEqual(fixture.client.startedConfigureSources, [source],
                       "An unchanged edit must not queue a second Configure behind the held request")
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)

        fixture.client.releaseConfigure(source)
        let accepted = await waitUntil(timeout: 2) {
            model.inventoryReady && model.snapshot.sourceURL == source
        }
        XCTAssertTrue(accepted, "The original typed Configure should still be accepted")
        try await Task.sleep(nanoseconds: 450_000_000)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source])
        XCTAssertEqual(fixture.client.completedConfigureSources, [source])
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)

        model.sourceChanged(source)
        try await Task.sleep(nanoseconds: 450_000_000)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source],
                       "Typing the already accepted URL again must not refetch its inventory")
        XCTAssertTrue(model.inventoryReady)
    }

    @MainActor
    func testPasteIsImmediateFencesOldResultAndLeavesStopAndLogsResponsive() async throws {
        let fixture = try ViewModelFixture()
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sessionID == "test-session" }

        let oldSource = "https://example.invalid/old"
        let pastedSource = "https://example.invalid/pasted"
        fixture.client.holdConfigure(oldSource)
        fixture.client.holdConfigure(pastedSource)
        model.sourceChanged(oldSource, immediate: true)
        let oldStarted = await waitUntil(timeout: 2) { fixture.client.startedConfigureSources == [oldSource] }
        XCTAssertTrue(oldStarted)

        model.paste(pastedSource)
        try await Task.sleep(nanoseconds: 50_000_000)
        XCTAssertEqual(model.sourceText, pastedSource)
        model.stop()
        let stopped = await waitUntil(timeout: 1) { fixture.client.stopCount == 1 }
        XCTAssertTrue(stopped, "Stop must use its session worker while Configure is held")

        let logsLoaded = expectation(description: "diagnostics refresh while Configure is held")
        let logsSubscription = model.$logEntries.first { $0.contains { $0.message == "during configure" } }
            .sink { _ in logsLoaded.fulfill() }
        model.refreshLogs()
        await fulfillment(of: [logsLoaded], timeout: 2)
        logsSubscription.cancel()

        fixture.client.releaseConfigure(oldSource)
        let pastedStarted = await waitUntil(timeout: 0.35) {
            fixture.client.startedConfigureSources == [oldSource, pastedSource]
        }
        XCTAssertTrue(pastedStarted, "Paste must start the next serialized Configure without a typing debounce")
        XCTAssertEqual(model.sourceText, pastedSource, "The old Configure result must not restore its superseded URL")
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)

        fixture.client.releaseConfigure(pastedSource)
        await waitForSnapshot(model) { $0.sourceURL == pastedSource }
        XCTAssertEqual(model.sourceText, pastedSource)
        XCTAssertTrue(model.inventoryReady)
        XCTAssertEqual(fixture.client.completedConfigureSources, [oldSource, pastedSource])
    }

    @MainActor
    func testRestoredURLLoadsOnceWhenSnapshotHasNoInventory() async throws {
        let source = "https://example.invalid/restored"
        let fixture = try ViewModelFixture(initialSource: source, inventoryConfigured: false)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)

        await waitForSnapshot(model) { $0.configured && $0.sourceURL == source }
        XCTAssertEqual(fixture.client.startedConfigureSources, [source])
        XCTAssertEqual(model.sourceText, source)

        model.refreshSnapshot()
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source], "Unchanged Snapshot polling must not fetch the restored URL again")
    }

    @MainActor
    func testConfiguredInventoryIsReusedAcrossSnapshotRefreshes() async throws {
        let source = "https://example.invalid/saved"
        let fixture = try ViewModelFixture(initialSource: source, inventoryConfigured: true)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)

        await waitForSnapshot(model) { $0.configured && $0.sourceURL == source }
        XCTAssertEqual(model.sourceText, source)
        XCTAssertTrue(model.inventoryReady)
        XCTAssertTrue(fixture.client.startedConfigureSources.isEmpty)

        model.refreshSnapshot()
        try await Task.sleep(nanoseconds: 200_000_000)
        XCTAssertEqual(fixture.client.startedConfigureSources, [], "A configured saved inventory must be reused without another Configure")
    }

    @MainActor
    func testFailedLoadWaitsForExplicitRetryAndRetryLoadsProfiles() async throws {
        let fixture = try ViewModelFixture()
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sessionID == "test-session" }

        let source = "https://example.invalid/retry"
        fixture.client.holdConfigure(source)
        fixture.client.failNextConfigure(source)
        model.sourceChanged(source, immediate: true)
        let firstAttemptStarted = await waitUntil(timeout: 2) {
            fixture.client.startedConfigureSources == [source]
        }
        XCTAssertTrue(firstAttemptStarted)
        XCTAssertTrue(model.loading, "A held request should expose the loading state")
        fixture.client.releaseConfigure(source)

        let failureShown = await waitUntil(timeout: 2) { !model.loadError.isEmpty }
        XCTAssertTrue(failureShown, "The failed request should expose actionable load feedback")
        XCTAssertFalse(model.loading)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source])
        try await Task.sleep(nanoseconds: 900_000_000)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source], "Snapshot polling must not retry a failed load")

        model.retryLoad()
        let retried = await waitUntil(timeout: 2) { model.inventoryReady && model.snapshot.sourceURL == source }
        XCTAssertTrue(retried, "Explicit Retry should accept the repaired subscription")
        XCTAssertTrue(model.loadError.isEmpty)
        XCTAssertEqual(fixture.client.startedConfigureSources, [source, source])
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)
    }

    @MainActor
    func testLinkImportLoadsOnceWithoutStartingOrStoppingTheActiveSession() async throws {
        let original = "https://example.invalid/original"
        let imported = "https://example.invalid/imported"
        let fixture = try ViewModelFixture(initialSource: original, inventoryConfigured: true)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.configured && $0.sourceURL == original }
        let generation = model.snapshot.generation

        model.importLink(URL(string: "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fimported")!)
        await waitForSnapshot(model) { $0.sourceURL == imported }
        model.importLink(URL(string: "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fimported")!)
        model.importLink(URL(string: "dobbyvpn://")!)
        try await Task.sleep(nanoseconds: 150_000_000)

        XCTAssertEqual(model.snapshot.sourceURL, imported)
        XCTAssertEqual(model.snapshot.generation, generation)
        XCTAssertEqual(fixture.client.startedConfigureSources, [imported])
        XCTAssertEqual(fixture.client.startCount, 0)
        XCTAssertEqual(fixture.client.stopCount, 0)
    }

    @MainActor
    func testInvalidImportGuidanceSurvivesConnectionSnapshotError() async throws {
        let fixture = try ViewModelFixture(initialSource: "https://example.invalid/original", inventoryConfigured: true)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.configured && $0.sourceURL == "https://example.invalid/original" }

        model.importLink(URL(string: "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fa&url=duplicate")!)
        let guidance = model.importError
        XCTAssertTrue(guidance.contains("Use dobbyvpn://import?url="))
        model.importLink(URL(string: "dobbyvpn://")!)
        XCTAssertEqual(model.importError, guidance,
                       "A bare link opens the app and must not clear guidance for the rejected import")

        fixture.client.setSnapshotOverrides(["source_error": "Simulator provider IPC unavailable"])
        model.refreshSnapshot()
        let refreshed = await waitUntil(timeout: 2) { model.snapshot.sourceError == "Simulator provider IPC unavailable" }
        XCTAssertTrue(refreshed)
        XCTAssertEqual(model.importError, guidance,
                       "A connection/provider snapshot error must not replace actionable invalid-link guidance")
    }

    @MainActor
    func testLinkImportWhileConnectingKeepsPendingTargetWithoutStartingOrStopping() async throws {
        let original = "https://example.invalid/original"
        let imported = "https://example.invalid/imported-while-connecting"
        let fixture = try ViewModelFixture(initialSource: original, inventoryConfigured: true)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        fixture.client.setSnapshotOverrides([
            "state": "PROBING",
            "primary_action": "STOP",
            "pending_target": ["digest": "digest", "mode": "PROFILE_INDEX", "index": 1],
            "can_switch": false,
        ])
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.configured && $0.sourceURL == original && $0.state == "PROBING" }
        let generation = model.snapshot.generation

        model.importLink(URL(string: "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fimported-while-connecting")!)
        await waitForSnapshot(model) {
            $0.sourceURL == imported && $0.pendingTarget?.mode == "PROFILE_INDEX" && $0.pendingTarget?.index == 1
        }

        XCTAssertEqual(model.snapshot.state, "PROBING")
        XCTAssertEqual(model.snapshot.generation, generation)
        XCTAssertEqual(fixture.client.startedConfigureSources, [imported])
        XCTAssertEqual(fixture.client.startCount, 0)
        XCTAssertEqual(fixture.client.stopCount, 0)
    }

    @MainActor
    func testPendingProfileSelectionShowsStopAndRejectsCompetingActions() async throws {
        let fixture = try ViewModelFixture(initialSource: "https://example.invalid/profiles")
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        fixture.client.setSnapshotOverrides([
            "digest": "profiles-v1",
            "active_digest": "profiles-v1",
            "active_mode": "AUTO_SELECT",
            "active_profile": ["index": 0, "protocol": "Outline", "description": "Active profile"],
            "profiles": [
                ["index": 0, "protocol": "Outline", "description": "Active profile"],
                ["index": 1, "protocol": "Xray", "description": "Second profile"],
            ],
            "can_switch": true,
        ])
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.configured && $0.profiles.count == 2 }

        XCTAssertEqual(model.actionTitle(0), "Disconnect")
        XCTAssertTrue(model.canAct(0))
        XCTAssertEqual(model.actionTitle(1), "Connect")
        XCTAssertTrue(model.canAct(1))

        fixture.client.holdStarts()
        model.performPrimaryAction(1)
        XCTAssertTrue(model.busy)
        let requestStarted = await waitUntil(timeout: 2) { fixture.client.startCount == 1 }
        XCTAssertTrue(requestStarted)
        XCTAssertFalse(model.canAct(0), "A competing action must be disabled while the first request is in flight")
        XCTAssertFalse(model.canAct(1), "The in-flight target must not submit a duplicate request")
        model.performPrimaryAction(0)
        model.performPrimaryAction(1)
        fixture.client.releaseStarts()

        await waitForSnapshot(model) { $0.pendingTarget?.mode == "PROFILE_INDEX" && $0.pendingTarget?.index == 1 }
        XCTAssertEqual(fixture.client.startCount, 1, "The first selected profile must own the transition")
        XCTAssertEqual(model.actionTitle(1), "Stop")
        XCTAssertTrue(model.canAct(1), "The selected target must remain cancellable")
        XCTAssertEqual(model.actionTitle(0), "Connect")
        XCTAssertFalse(model.canAct(0), "Other profile actions stay disabled during the transition")
        XCTAssertEqual(model.actionTitle(), "Auto connect")
        XCTAssertFalse(model.canAct())
        XCTAssertEqual(fixture.client.startRequests.first?["mode"] as? String, "PROFILE_INDEX")
        XCTAssertEqual(fixture.client.startRequests.first?["index"] as? Int, 1)

        fixture.client.setSnapshotOverrides([
            "state": "PROBING",
            "recovering": true,
            "pending_target": ["digest": "profiles-v1", "mode": "AUTO_SELECT", "index": 0],
            "can_switch": false,
        ])
        model.refreshSnapshot()
        await waitForSnapshot(model) { $0.pendingTarget?.mode == "AUTO_SELECT" && $0.recovering }
        XCTAssertEqual(model.status, "Reconnecting")
        XCTAssertEqual(model.actionTitle(), "Stop")
        XCTAssertTrue(model.canAct(), "The Auto recovery target must stay cancellable")
        XCTAssertFalse(model.canAct(0), "Profile Connect actions stay disabled during Auto recovery")
        model.performPrimaryAction()
        let stoppedRecovery = await waitUntil(timeout: 2) { fixture.client.stopCount == 1 }
        XCTAssertTrue(stoppedRecovery, "The Auto Stop action must reach the session client")
        XCTAssertEqual(fixture.client.startCount, 1, "Stopping recovery must not submit another Start")
    }

    @MainActor
    func testEditingAndReplacingInventoryKeepsOldActiveProfileDisconnectable() async throws {
        let original = "https://example.invalid/original"
        let replacement = "https://example.invalid/replacement"
        let fixture = try ViewModelFixture(initialSource: original)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        fixture.client.setSnapshotOverrides([
            "digest": "old-inventory",
            "active_digest": "old-inventory",
            "active_mode": "PROFILE_INDEX",
            "active_index": 0,
            "active_profile": ["index": 0, "protocol": "Outline", "description": "Old active profile"],
            "profiles": [
                ["index": 0, "protocol": "Outline", "description": "Old active profile"],
                ["index": 1, "protocol": "Xray", "description": "Old second profile"],
            ],
            "can_switch": true,
        ])
        fixture.client.setConfigureSnapshotOverrides(replacement, values: [
            "digest": "new-inventory",
            "profiles": [["index": 0, "protocol": "TrustTunnel", "description": "New profile"]],
            "active_profile": ["index": 0, "protocol": "Outline", "description": "Old active profile"],
            "active_digest": "old-inventory",
            "active_mode": "PROFILE_INDEX",
            "active_index": 0,
            "can_switch": true,
        ])
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.configured && $0.sourceURL == original && $0.profiles.count == 2 }

        XCTAssertEqual(model.actionTitle(0), "Disconnect")
        fixture.client.holdConfigure(replacement)
        model.sourceChanged(replacement, immediate: true)
        let replacementStarted = await waitUntil(timeout: 2) {
            fixture.client.startedConfigureSources == [replacement]
        }
        XCTAssertTrue(replacementStarted)
        XCTAssertFalse(model.inventoryReady)
        XCTAssertTrue(model.canAct(0), "Editing the URL must retain Disconnect for the active profile")
        XCTAssertFalse(model.canAct(1), "Editing the URL must disable other old-inventory actions")

        fixture.client.releaseConfigure(replacement)
        await waitForSnapshot(model) { $0.sourceURL == replacement && $0.digest == "new-inventory" }
        XCTAssertTrue(model.inventoryReady)
        XCTAssertEqual(model.snapshot.profiles.map(\.name), ["New profile"])
        XCTAssertEqual(model.snapshot.activeProfile?.name, "Old active profile",
                       "The active profile must remain visible after it disappears from the loaded inventory")
        XCTAssertNotEqual(model.snapshot.activeDigest, model.snapshot.digest)
        XCTAssertEqual(model.snapshot.primaryAction, "STOP")
        XCTAssertFalse(model.isStopTarget(nil), "The old profile must not be mistaken for the new Auto inventory target")
        XCTAssertFalse(model.snapshot.profiles.contains { model.isStopTarget($0.index) })

        model.stop()
        let stopped = await waitUntil(timeout: 2) { fixture.client.stopCount == 1 }
        XCTAssertTrue(stopped, "The active profile outside the new inventory must still be disconnectable")
    }

    @MainActor
    func testHeldSupersededLoadKeepsDisplayedInventoryUntilNewestLoadSucceeds() async throws {
        let original = "https://example.invalid/original"
        let stale = "https://example.invalid/stale"
        let newest = "https://example.invalid/newest"
        let originalProfiles: [[String: Any]] = [
            ["index": 0, "protocol": "Outline", "description": "Original one"],
            ["index": 1, "protocol": "Xray", "description": "Original active"],
        ]
        let fixture = try ViewModelFixture(initialSource: original)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        fixture.client.setSnapshotOverrides([
            "configured": true,
            "source_url": original,
            "digest": "original-digest",
            "profiles": originalProfiles,
            "state": "CONNECTED",
            "primary_action": "STOP",
            "active_profile": originalProfiles[1],
            "active_digest": "original-digest",
            "active_mode": "PROFILE_INDEX",
            "active_index": 1,
            "can_switch": true,
        ])
        fixture.client.setConfigureSnapshotOverrides(stale, values: [
            "configured": true,
            "source_url": stale,
            "digest": "stale-digest",
            "profiles": [["index": 0, "protocol": "Outline", "description": "Stale response"]],
            "sequence": 2,
            "generation": 8,
            "state": "CONNECTED",
            "primary_action": "STOP",
            "active_profile": originalProfiles[1],
            "active_digest": "original-digest",
            "active_mode": "PROFILE_INDEX",
            "active_index": 1,
            "pending_target": ["digest": "original-digest", "mode": "PROFILE_INDEX", "index": 1],
            "last_failure": ["code": "RECOVERY_PROBE_FAILED", "message": "previous probe failed"],
            "recovering": true,
            "can_switch": false,
        ])
        fixture.client.setConfigureSnapshotOverrides(newest, values: [
            "configured": true,
            "source_url": newest,
            "digest": "newest-digest",
            "profiles": [["index": 0, "protocol": "TrustTunnel", "description": "Newest profile"]],
            "sequence": 3,
            "generation": 9,
            "state": "CONNECTED",
            "primary_action": "STOP",
            "active_profile": originalProfiles[1],
            "active_digest": "original-digest",
            "active_mode": "PROFILE_INDEX",
            "active_index": 1,
            "pending_target": NSNull(),
            "last_failure": NSNull(),
            "recovering": false,
            "can_switch": true,
        ])
        fixture.client.holdConfigure(stale)
        fixture.client.holdConfigure(newest)
        fixture.client.failNextConfigure(newest)
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sourceURL == original && $0.digest == "original-digest" }

        model.sourceChanged(stale, immediate: true)
        let staleStarted = await waitUntil(timeout: 2) { fixture.client.startedConfigureSources == [stale] }
        XCTAssertTrue(staleStarted)
        model.sourceChanged(newest, immediate: true)
        fixture.client.releaseConfigure(stale)
        let newestStarted = await waitUntil(timeout: 2) {
            fixture.client.startedConfigureSources == [stale, newest]
        }
        XCTAssertTrue(newestStarted)

        let staleSnapshotPolled = await waitUntil(timeout: 2) { model.snapshot.sequence == 2 }
        XCTAssertTrue(staleSnapshotPolled, "The stale completion should still refresh live session state")
        XCTAssertTrue(model.loading, "The newest held request must remain visible as loading")
        XCTAssertEqual(model.sourceText, newest)
        XCTAssertEqual(model.snapshot.sourceURL, original)
        XCTAssertEqual(model.snapshot.digest, "original-digest")
        XCTAssertEqual(model.snapshot.profiles.map(\.name), ["Original one", "Original active"])
        XCTAssertTrue(model.snapshot.configured)
        XCTAssertEqual(model.snapshot.generation, 8)
        XCTAssertEqual(model.snapshot.state, "CONNECTED")
        XCTAssertEqual(model.snapshot.primaryAction, "STOP")
        XCTAssertEqual(model.snapshot.activeProfile?.name, "Original active")
        XCTAssertEqual(model.snapshot.activeDigest, "original-digest")
        XCTAssertEqual(model.snapshot.activeMode, "PROFILE_INDEX")
        XCTAssertEqual(model.snapshot.activeIndex, 1)
        XCTAssertEqual(model.snapshot.pendingTarget?.mode, "PROFILE_INDEX")
        XCTAssertEqual(model.snapshot.pendingTarget?.index, 1)
        XCTAssertEqual(model.snapshot.lastFailure?.code, "RECOVERY_PROBE_FAILED")
        XCTAssertEqual(model.snapshot.lastFailure?.message, "previous probe failed")
        XCTAssertTrue(model.snapshot.recovering)
        XCTAssertFalse(model.snapshot.canSwitch)
        XCTAssertEqual(model.status, "Reconnecting")
        XCTAssertFalse(model.inventoryReady)
        XCTAssertTrue(model.canAct(1), "The active Disconnect action must stay available during loading")
        XCTAssertFalse(model.canAct(0), "A competing Connect action must stay disabled during loading")
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)

        fixture.client.releaseConfigure(newest)
        let newestFailed = await waitUntil(timeout: 2) { !model.loading && !model.loadError.isEmpty }
        XCTAssertTrue(newestFailed, "The newest Configure failure should be reported")
        XCTAssertEqual(model.sourceText, newest)
        XCTAssertEqual(model.snapshot.sourceURL, original)
        XCTAssertEqual(model.snapshot.digest, "original-digest")
        XCTAssertEqual(model.snapshot.profiles.map(\.name), ["Original one", "Original active"])
        XCTAssertEqual(model.snapshot.generation, 8)
        XCTAssertTrue(model.snapshot.recovering)
        XCTAssertEqual(model.snapshot.pendingTarget?.index, 1)
        XCTAssertEqual(model.snapshot.lastFailure?.code, "RECOVERY_PROBE_FAILED")
        XCTAssertFalse(model.snapshot.canSwitch)
        XCTAssertTrue(model.canAct(1))
        XCTAssertEqual(fixture.client.startedConfigureSources, [stale, newest])

        model.retryLoad()
        let retried = await waitUntil(timeout: 2) {
            model.inventoryReady && model.snapshot.sourceURL == newest && model.snapshot.digest == "newest-digest"
        }
        XCTAssertTrue(retried, "Retry should admit the newest inventory after its receipt is accepted")
        XCTAssertEqual(model.sourceText, newest)
        XCTAssertEqual(model.snapshot.profiles.map(\.name), ["Newest profile"])
        XCTAssertEqual(model.snapshot.generation, 9)
        XCTAssertEqual(model.snapshot.activeProfile?.name, "Original active")
        XCTAssertEqual(model.snapshot.activeDigest, "original-digest")
        XCTAssertEqual(model.snapshot.activeMode, "PROFILE_INDEX")
        XCTAssertEqual(model.snapshot.activeIndex, 1)
        XCTAssertNil(model.snapshot.pendingTarget)
        XCTAssertNil(model.snapshot.lastFailure)
        XCTAssertFalse(model.snapshot.recovering)
        XCTAssertTrue(model.snapshot.canSwitch)
        XCTAssertEqual(fixture.client.startedConfigureSources, [stale, newest, newest])
        XCTAssertEqual(fixture.client.maximumConcurrentConfigures, 1)
    }

    @MainActor
    func testNewSessionInventoryReplacesDisplayedInventoryWhileSourceIsDirty() async throws {
        let original = "https://example.invalid/original-session"
        let replacement = "https://example.invalid/replacement-session"
        let fixture = try ViewModelFixture(initialSource: original)
        defer { try? FileManager.default.removeItem(at: fixture.directory) }
        fixture.client.setSnapshotOverrides([
            "configured": true,
            "source_url": original,
            "digest": "original-session-digest",
            "profiles": [["index": 0, "protocol": "Outline", "description": "Original session profile"]],
        ])
        let model = DobbySessionViewModel(client: fixture.client)
        await waitForSnapshot(model) { $0.sourceURL == original && $0.digest == "original-session-digest" }
        model.sourceChanged("not a subscription URL")

        fixture.client.replaceSession(
            id: "replacement-session",
            source: replacement,
            digest: "replacement-session-digest",
            profiles: [["index": 0, "protocol": "Xray", "description": "Replacement session profile"]]
        )
        model.refreshSnapshot()
        await waitForSnapshot(model) { $0.sessionID == "replacement-session" }

        XCTAssertEqual(model.sourceText, "not a subscription URL", "An edited field stays visible while its request is invalid")
        XCTAssertEqual(model.snapshot.sourceURL, replacement)
        XCTAssertEqual(model.snapshot.digest, "replacement-session-digest")
        XCTAssertEqual(model.snapshot.profiles.map(\.name), ["Replacement session profile"])
        XCTAssertTrue(fixture.client.startedConfigureSources.isEmpty)
    }

    func testProfileMetadataKeepsOrderProtocolAndEmptyDescriptionFallback() throws {
        let data = Data(#"[{"index":0,"protocol":"Outline","description":""},{"index":1,"protocol":"Xray","description":"Second profile"}]"#.utf8)
        let profiles = try JSONDecoder().decode([DobbyProfile].self, from: data)
        XCTAssertEqual(profiles.map(\.index), [0, 1])
        XCTAssertEqual(profiles.map(\.name), ["Profile 1", "Second profile"])
        XCTAssertEqual(profiles.map(\.protocolName), ["Outline", "Xray"])
        XCTAssertEqual(profiles.map(\.description), ["", "Second profile"])
    }

    @MainActor
    private func waitForSnapshot(
        _ model: DobbySessionViewModel,
        matching predicate: @escaping (DobbySessionSnapshot) -> Bool
    ) async {
        let received = expectation(description: "matching snapshot")
        let subscription = model.$snapshot.first(where: predicate).sink { _ in received.fulfill() }
        await fulfillment(of: [received], timeout: 5)
        subscription.cancel()
    }
}

@MainActor
private func waitUntil(timeout: TimeInterval, condition: @escaping () -> Bool) async -> Bool {
    let deadline = Date().addingTimeInterval(timeout)
    while Date() < deadline {
        if condition() { return true }
        try? await Task.sleep(nanoseconds: 10_000_000)
    }
    return condition()
}

private struct ViewModelFixture {
    let directory: URL
    let client: ViewModelTestClient

    init(initialSource: String = "", inventoryConfigured: Bool = true) throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        self.directory = directory
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let backend = directory.appendingPathComponent("backend.jsonl")
        try Data("during configure\n".utf8).write(to: backend)
        self.client = ViewModelTestClient(
            diagnosticPaths: [backend],
            uiDiagnosticPath: directory.appendingPathComponent("ui.jsonl"),
            initialSource: initialSource,
            inventoryConfigured: inventoryConfigured
        )
    }
}

private final class ViewModelTestClient: DobbySessionClient, @unchecked Sendable {
    let diagnosticPaths: [URL]
    let uiDiagnosticPath: URL
    let version = "test"
    let sourceCommit = String(repeating: "a", count: 40)

    private let condition = NSCondition()
    private var sequence: Int64 = 1
    private var sessionID = "test-session"
    private var configuredSource: String
    private var inventoryConfigured: Bool
    private var heldSources = Set<String>()
    private var releasedSources = Set<String>()
    private var failingSources = Set<String>()
    private var startedSources: [String] = []
    private var completedSources: [String] = []
    private var activeConfigures = 0
    private var maxConcurrentConfigures = 0
    private var stops = 0
    private var starts = 0
    private var recordedStartRequests: [[String: Any]] = []
    private var startsHeld = false
    private var startsReleased = false
    private var snapshotOverrides: [String: Any] = [:]
    private var configureSnapshotOverrides: [String: [String: Any]] = [:]

    init(diagnosticPaths: [URL], uiDiagnosticPath: URL, initialSource: String, inventoryConfigured: Bool) {
        self.diagnosticPaths = diagnosticPaths
        self.uiDiagnosticPath = uiDiagnosticPath
        self.configuredSource = initialSource
        self.inventoryConfigured = inventoryConfigured
    }

    var startedConfigureSources: [String] {
        condition.lock(); defer { condition.unlock() }
        return startedSources
    }

    var completedConfigureSources: [String] {
        condition.lock(); defer { condition.unlock() }
        return completedSources
    }

    var maximumConcurrentConfigures: Int {
        condition.lock(); defer { condition.unlock() }
        return maxConcurrentConfigures
    }

    var stopCount: Int {
        condition.lock(); defer { condition.unlock() }
        return stops
    }

    var startCount: Int {
        condition.lock(); defer { condition.unlock() }
        return starts
    }

    var startRequests: [[String: Any]] {
        condition.lock(); defer { condition.unlock() }
        return recordedStartRequests
    }

    func setSnapshotOverrides(_ values: [String: Any]) {
        condition.lock(); defer { condition.unlock() }
        snapshotOverrides.merge(values) { _, new in new }
    }

    func setConfigureSnapshotOverrides(_ source: String, values: [String: Any]) {
        condition.lock(); defer { condition.unlock() }
        configureSnapshotOverrides[source] = values
    }

    func replaceSession(id: String, source: String, digest: String, profiles: [[String: Any]]) {
        condition.lock(); defer { condition.unlock() }
        sessionID = id
        sequence = 1
        configuredSource = source
        inventoryConfigured = true
        snapshotOverrides = ["digest": digest, "profiles": profiles]
    }

    func holdStarts() {
        condition.lock(); defer { condition.unlock() }
        startsHeld = true
        startsReleased = false
    }

    func releaseStarts() {
        condition.lock(); defer { condition.unlock() }
        startsReleased = true
        condition.broadcast()
    }

    func holdConfigure(_ source: String) {
        condition.lock(); defer { condition.unlock() }
        heldSources.insert(source)
    }

    func releaseConfigure(_ source: String) {
        condition.lock(); defer { condition.unlock() }
        releasedSources.insert(source)
        condition.broadcast()
    }

    func failNextConfigure(_ source: String) {
        condition.lock(); defer { condition.unlock() }
        failingSources.insert(source)
    }

    func call(_ method: String, parameters: [String: Any]) -> String {
        switch method {
        case "Snapshot":
            condition.lock()
            let currentSequence = sequence
            let source = configuredSource
            let configured = inventoryConfigured
            var result: [String: Any] = [
                "session_id": sessionID, "sequence": currentSequence, "generation": 1,
                "state": "CONNECTED", "primary_action": "STOP", "configured": configured,
                "source_url": source, "source_error": "", "digest": "digest",
                "active_digest": "digest", "active_mode": "AUTO_SELECT", "can_switch": true,
            ]
            result.merge(snapshotOverrides) { _, new in new }
            condition.unlock()
            return response(["ok": true, "result": result])
        case "Configure":
            let source = parameters["source"] as? String ?? ""
            condition.lock()
            startedSources.append(source)
            activeConfigures += 1
            maxConcurrentConfigures = max(maxConcurrentConfigures, activeConfigures)
            condition.broadcast()
            while heldSources.contains(source) && !releasedSources.contains(source) {
                condition.wait()
            }
            let shouldFail = failingSources.remove(source) != nil
            activeConfigures -= 1
            completedSources.append(source)
            if shouldFail {
                condition.broadcast()
                condition.unlock()
                return response(["ok": false, "error": ["code": "FETCH_FAILED", "message": "fixture failure"]])
            }
            configuredSource = source
            inventoryConfigured = true
            sequence += 1
            let resultSequence = sequence
            if let overrides = configureSnapshotOverrides[source] {
                snapshotOverrides.merge(overrides) { _, new in new }
            }
            condition.broadcast()
            condition.unlock()
            return response(["ok": true, "result": ["sequence": resultSequence]])
        case "Stop":
            condition.lock(); stops += 1; condition.broadcast(); condition.unlock()
            return response(["ok": true, "result": [:] as [String: Any]])
        case "Start":
            condition.lock()
            starts += 1
            recordedStartRequests.append(parameters)
            condition.broadcast()
            while startsHeld && !startsReleased { condition.wait() }
            let mode = parameters["mode"] as? String ?? "AUTO_SELECT"
            let index = parameters["index"] as? Int ?? 0
            let digest = parameters["digest"] as? String ?? ""
            snapshotOverrides.merge([
                "state": "PROBING",
                "primary_action": "STOP",
                "pending_target": ["digest": digest, "mode": mode, "index": index],
                "can_switch": false,
            ]) { _, new in new }
            condition.broadcast()
            condition.unlock()
            return response(["ok": true, "result": [:] as [String: Any]])
        default:
            return response(["ok": true, "result": [:] as [String: Any]])
        }
    }
}

private func response(_ value: [String: Any]) -> String {
    let data = try! JSONSerialization.data(withJSONObject: value)
    return String(decoding: data, as: UTF8.self)
}
