package com.dobby.feature.vpn_service

import android.content.Context
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionEvent
import com.dobby.feature.main.domain.SessionFailureCode
import com.dobby.feature.main.domain.SessionObservation
import com.dobby.feature.main.domain.SessionProfile
import com.dobby.feature.main.domain.SessionProtocol
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.yield
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/** Unit-style contract tests for the candidate-owned Android hosted seam. */
@RunWith(AndroidJUnit4::class)
class AndroidHostedProfileTestDriverTest {
    private val context: Context
        get() = InstrumentationRegistry.getInstrumentation().targetContext
    private val files = mutableListOf<File>()

    @After
    fun cleanupFiles() {
        files.forEach(File::delete)
        files.clear()
    }

    @Test
    fun command_validation_rejects_missing_or_invalid_required_values() {
        val valid = JSONObject(commandJson()).apply {
            put("source_sha", "not-a-sha")
        }
        assertInputRejected(valid)

        val wrongType = JSONObject(commandJson()).apply {
            put("profile_file", 17)
        }
        assertInputRejected(wrongType)

        val wrongSourceType = JSONObject(commandJson()).apply {
            put("source_sha", 17)
        }
        assertInputRejected(wrongSourceType)

        val wrongPreserveType = JSONObject(commandJson()).apply {
            put("preserve_active", "true")
        }
        assertInputRejected(wrongPreserveType)

        val wrongProfileIndex = JSONObject(commandJson()).apply {
            put("profile_index", "0")
        }
        assertInputRejected(wrongProfileIndex)

        val negativeProfileIndex = JSONObject(commandJson()).apply {
            put("profile_index", -1)
        }
        assertInputRejected(negativeProfileIndex)

        val wrongTimeoutType = JSONObject(commandJson()).apply {
            getJSONArray("operations").getJSONObject(0).put("timeout_seconds", 30.5)
        }
        assertInputRejected(wrongTimeoutType)

        val wrongOperationType = JSONObject(commandJson()).apply {
            getJSONArray("operations").getJSONObject(0).put("id", 7)
        }
        assertInputRejected(wrongOperationType)

        val missingControl = JSONObject(commandJson(operations = listOf("network_transition"))).apply {
            getJSONArray("operations").getJSONObject(0).remove("control_file")
        }
        assertInputRejected(missingControl)

        val unknownOperation = JSONObject(commandJson()).apply {
            getJSONArray("operations").getJSONObject(0).put("operation", "unknown")
        }
        assertInputRejected(unknownOperation)
    }

    @Test
    fun non_success_measurement_http_status_has_stable_service_error_code() {
        assertEquals(null, measurementServiceFailureCode(200))
        assertEquals(null, measurementServiceFailureCode(299))
        assertEquals("MEASUREMENT_SERVICE_UNAVAILABLE", measurementServiceFailureCode(429))
        assertEquals("MEASUREMENT_SERVICE_UNAVAILABLE", measurementServiceFailureCode(503))
    }

    @Test
    fun command_parser_ignores_extra_fields_and_leaves_scenario_policy_to_torturer() {
        val json = JSONObject(commandJson(preserveActive = true)).apply {
            put("diagnostic", "retained")
            getJSONObject("endpoints").put("diagnostic", "retained")
            getJSONArray("operations").getJSONObject(0).put("diagnostic", "retained")
        }
        val command = AndroidHostedCommandContract.parse(json.toString())
        assertTrue(command.preserveActive)
    }

    @Test
    fun command_and_observation_allow_absent_source_sha_but_keep_supplied_identity_optional() {
        val commandJson = JSONObject(commandJson()).apply { remove("source_sha") }
        val command = AndroidHostedCommandContract.parse(commandJson.toString())

        assertEquals(null, command.sourceSha)
        assertFalse(AndroidHostedObservation(null).toJson().has("source_sha"))
    }

    @Test
    fun command_validation_preserves_supplied_operation_order_without_test_set() {
        val command = AndroidHostedCommandContract.parse(
            commandJson(
                operations = listOf(
                    "measure_throughput", "configure", "connect", "disconnect", "inspect_cleanup",
                ),
            ),
        )
        assertEquals(
            listOf("measure_throughput", "configure", "connect", "disconnect", "inspect_cleanup"),
            command.operations.map(AndroidHostedOperation::operation),
        )
    }

    @Test
    fun output_contains_the_declared_observation_fields_and_cleans_up_inputs() = runBlocking {
        val profileData = "synthetic profile https://private.example.test/path 198.51.100.7"
        val commandFile = writeInput("command-observation.json", commandJson(outputFile = "observation.json"))
        val profileFile = writeInput("profile-8.bin", profileData)
        val outputFile = context.filesDir.resolve("observation.json")
        files += outputFile
        val controller = FakeSessionController()
        val platform = FakePlatform()
        AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> platform },
        ).run(commandFile.name)

        val output = outputFile.readText()
        val keys = JSONObject(output).keys().asSequence().toSet()
        assertEquals(
            setOf(
                "source_sha", "configured", "connected",
                "connections", "connection",
                "tunnel_interface", "routing_verified", "disconnect_clean",
                "restart_verified", "reconnect_completed", "second_tunnel_interface", "second_routing_verified",
                "stability_verified", "stability_sample_count", "stability_sample_interval_seconds",
                "latency_ms", "download_mbps", "upload_mbps", "final_disconnect_clean",
                "network_transition_verified", "process_loss_verified",
                "cleanup_verified",
            ),
            keys,
        )
        assertFalse(profileFile.exists())
        assertFalse(commandFile.exists())
    }

    @Test
    fun ordered_operations_and_reconnect_use_one_generation_at_a_time() = runBlocking {
        val operations = listOf(
            "configure", "connect", "observe_tunnel", "observe_routing_identity", "measure_stability",
            "measure_throughput", "disconnect", "reconnect", "observe_tunnel", "observe_routing_identity",
            "disconnect", "inspect_cleanup",
        )
        val commandFile = writeInput("command-order.json", commandJson(operations = operations))
        writeInput("profile-12.bin", "opaque-profile")
        val eventLog = mutableListOf<String>()
        val controller = FakeSessionController(eventLog, staleIdleReplay = true)
        val platform = FakePlatform(events = eventLog)
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> platform },
        ).run(commandFile.name)

        assertEquals(null, result.errorCode)
        assertTrue(result.tunnelInterface)
        assertTrue(result.routingVerified)
        assertTrue(result.disconnectClean)
        assertTrue(result.restartVerified)
        assertTrue(result.reconnectCompleted)
        assertTrue(result.secondTunnelInterface)
        assertTrue(result.secondRoutingVerified)
        assertTrue(result.finalDisconnectClean)
        assertEquals(2, controller.stopCalls)
        assertEquals(4, controller.emittedWatchEvents)
        assertTrue(controller.startTargets.all { it == SessionStartTarget.ProfileIndex(0) })
        assertEquals(listOf("configure", "disconnected", "consent", "start", "watch", "tunnel", "identity", "stability", "throughput", "stop", "disconnected", "disconnected", "consent", "start", "watch", "tunnel", "identity", "stop", "disconnected", "snapshot", "watch", "snapshot", "disconnected", "snapshot", "destroy", "disconnected"), eventLog)
    }

    @Test
    fun selected_profile_index_and_complete_dynamic_inventory_are_preserved() = runBlocking {
        val profiles = listOf(
            SessionProfile(0, SessionProtocol.OUTLINE, "Outline"),
            SessionProfile(1, SessionProtocol.XRAY, "Xray"),
        )
        val commandFile = writeInput(
            "command-profile-one.json",
            commandJson(
                operations = listOf("configure", "connect", "disconnect"),
                profileIndex = 1,
            ),
        )
        writeInput("profile-3.bin", "opaque-profile")
        val controller = FakeSessionController(profiles = profiles)
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> FakePlatform() },
        ).run(commandFile.name)

        assertEquals(profiles, result.connections)
        assertEquals(profiles[1], result.selectedConnection)
        assertEquals(listOf(SessionStartTarget.ProfileIndex(1)), controller.startTargets)
    }

    @Test
    fun first_cycle_false_is_not_masked_by_later_values() = runBlocking {
        val commandFile = writeInput("command-first-false.json", commandJson(operations = listOf("configure", "connect", "observe_tunnel")))
        writeInput("profile-3.bin", "opaque-profile")
        val controller = FakeSessionController()
        val outputFile = context.filesDir.resolve("observation-3.json").also(files::add)
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = { controller },
                platformFactory = { _ -> FakePlatform(tunnelResults = listOf(false)) },
            ).run(commandFile.name)
        }
        val result = JSONObject(outputFile.readText())

        assertEquals("TUNNEL_NOT_OBSERVED", failure.message)
        assertEquals("TUNNEL_NOT_OBSERVED", result.getString("error_code"))
        assertFalse(result.getBoolean("tunnel_interface"))
        assertFalse(result.getBoolean("second_tunnel_interface"))
        assertTrue(result.getBoolean("cleanup_verified"))
    }

    @Test
    fun second_cycle_false_is_not_masked_by_first_cycle_success() = runBlocking {
        val operations = listOf(
            "configure", "connect", "observe_tunnel", "disconnect", "reconnect", "observe_tunnel",
        )
        val commandFile = writeInput("command-second-false.json", commandJson(operations = operations))
        writeInput("profile-6.bin", "opaque-profile")
        val controller = FakeSessionController()
        val outputFile = context.filesDir.resolve("observation-6.json").also(files::add)
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = { controller },
                platformFactory = { _ -> FakePlatform(tunnelResults = listOf(true, false)) },
            ).run(commandFile.name)
        }
        val result = JSONObject(outputFile.readText())

        assertEquals("TUNNEL_NOT_OBSERVED", failure.message)
        assertEquals("TUNNEL_NOT_OBSERVED", result.getString("error_code"))
        assertTrue(result.getBoolean("tunnel_interface"))
        assertFalse(result.getBoolean("second_tunnel_interface"))
        assertTrue(result.getBoolean("cleanup_verified"))
        assertEquals(2, controller.stopCalls)
    }

    @Test
    fun operation_failure_still_attempts_stop_destroy_service_and_cleanup() = runBlocking {
        val commandFile = writeInput("command-failure.json", commandJson(operations = listOf("configure", "connect", "observe_tunnel")))
        writeInput("profile-3.bin", "opaque-profile")
        val controller = FakeSessionController()
        val platform = FakePlatform(failTunnel = true)
        val diagnostics = mutableListOf<String>()
        val outputFile = context.filesDir.resolve("observation-3.json")
        files += outputFile
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = { controller },
                platformFactory = { _ -> platform },
                diagnosticLog = diagnostics::add,
            ).run(commandFile.name)
        }
        val result = JSONObject(outputFile.readText())

        assertTrue(failure is IllegalStateException)
        assertEquals("synthetic platform failure", failure.message)
        assertEquals("DRIVER_ERROR", result.getString("error_code"))
        assertEquals(1, controller.stopCalls)
        assertTrue(controller.events.contains("destroy"))
        assertTrue(platform.events.contains("disconnected"))
        assertTrue(result.getBoolean("cleanup_verified"))
        assertTrue(
            "diagnostics:\n${diagnostics.joinToString(separator = "\n")}",
            diagnostics.any {
                it.contains("stage=observe_tunnel") &&
                    it.contains("failureTypes=IllegalStateException") &&
                    it.contains("failureCode=DRIVER_ERROR")
            },
        )
        assertTrue(diagnostics.any { it.contains("cleanupVerified=true") })
    }

    @Test
    fun cleanup_failure_is_suppressed_on_the_original_operation_failure() = runBlocking {
        val commandFile = writeInput(
            "command-primary-secondary.json",
            commandJson(operations = listOf("configure", "connect", "observe_tunnel")),
        )
        writeInput("profile-3.bin", "opaque-profile")
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = {
                    FakeSessionController(
                        destroyFailure = IllegalArgumentException("synthetic cleanup failure"),
                    )
                },
                platformFactory = { _ -> FakePlatform(failTunnel = true) },
            ).run(commandFile.name)
        }

        assertTrue(failure is IllegalStateException)
        assertEquals("synthetic platform failure", failure.message)
        assertTrue(
            failure.suppressed.any {
                it.message == "synthetic cleanup failure"
            },
        )
    }

    @Test
    fun controller_failure_preserves_its_exact_code_and_message() = runBlocking {
        val commandFile = writeInput(
            "command-controller-failure.json",
            commandJson(operations = listOf("configure", "connect", "disconnect")),
        )
        writeInput("profile-3.bin", "opaque-profile")
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = {
                    FakeSessionController(
                        stopFailure = SessionControllerResult.Failure(
                            "synthetic stop failure",
                            SessionFailureCode.PLATFORM_FAILED,
                        ),
                    )
                },
                platformFactory = { _ -> FakePlatform() },
            ).run(commandFile.name)
        }

        assertEquals("DISCONNECT_FAILED", failure.message)
        assertEquals(
            "session controller stop failed: code=PLATFORM_FAILED; message=synthetic stop failure",
            failure.cause?.message,
        )
    }

    @Test
    fun failed_connection_records_the_last_product_state_and_failure_code() = runBlocking {
        val outputName = "observation-connect-failure.json"
        val commandFile = writeInput(
            "command-connect-failure.json",
            commandJson(
                operations = listOf("configure", "connect"),
                outputFile = outputName,
            ),
        )
        writeInput("profile-2.bin", "opaque-profile")
        files += context.filesDir.resolve(outputName)
        val diagnostics = mutableListOf<String>()
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = {
                    FakeSessionController(
                        stateAfterStart = SessionState.FAILED,
                        failureAfterStart = SessionFailureCode.PLATFORM_FAILED,
                    )
                },
                platformFactory = { _ -> FakePlatform() },
                diagnosticLog = diagnostics::add,
            ).run(commandFile.name)
        }
        val result = JSONObject(context.filesDir.resolve(outputName).readText())

        assertEquals("CONNECT_FAILED", failure.message)
        assertEquals("CONNECT_FAILED", result.getString("error_code"))
        assertTrue(
            diagnostics.any {
                it.contains("stage=await_connected") &&
                    it.contains("lastState=FAILED") &&
                    it.contains("lastFailureCode=PLATFORM_FAILED")
            },
        )
    }

    @Test
    fun network_transition_control_is_observed() = runBlocking {
        val externalOperations = listOf("network_transition")
        val operations = listOf("configure", "connect") + externalOperations + listOf("disconnect", "inspect_cleanup")
        val commandFile = writeInput(
            "command-external.json",
            commandJson(operations = operations, profileFile = "profile-external.bin"),
        )
        writeInput("profile-external.bin", "opaque-profile")
        val outputFile = context.filesDir.resolve("observation-${operations.size}.json")
        files += outputFile
        val command = JSONObject(commandFile.readText())
        val responder = Thread {
            externalOperations.forEach { operation ->
                val operationJson = command.getJSONArray("operations").let { items ->
                    (0 until items.length()).asSequence()
                        .map(items::getJSONObject)
                        .first { it.getString("operation") == operation }
                }
                val controlFile = context.filesDir.resolve(operationJson.getString("control_file"))
                val ready = context.filesDir.resolve("${controlFile.name}.ready")
                val deadline = System.currentTimeMillis() + 10_000L
                while (!ready.exists() && System.currentTimeMillis() < deadline) Thread.sleep(10)
                check(ready.exists())
                while (controlFile.exists() && System.currentTimeMillis() < deadline) Thread.sleep(10)
                check(!controlFile.exists())
                val temporary = context.filesDir.resolve("${controlFile.name}.tmp")
                temporary.writeText(JSONObject().put("operation", operation).toString())
                check(temporary.renameTo(controlFile))
                while (controlFile.exists() && System.currentTimeMillis() < deadline) Thread.sleep(10)
                check(!controlFile.exists())
                while (ready.exists() && System.currentTimeMillis() < deadline) Thread.sleep(10)
                check(!ready.exists())
            }
        }
        responder.isDaemon = true
        responder.start()
        val controller = FakeSessionController()
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> FakePlatform() },
        ).run(commandFile.name)
        responder.join(2_000)

        assertFalse("external control responder did not finish", responder.isAlive)
        assertEquals(null, result.errorCode)
        assertTrue(result.networkTransitionVerified)
        assertTrue(result.cleanupVerified)
        externalOperations.forEach { operation ->
            val operationJson = command.getJSONArray("operations").let { items ->
                (0 until items.length()).asSequence()
                    .map(items::getJSONObject)
                    .first { it.getString("operation") == operation }
            }
            val controlFile = context.filesDir.resolve(operationJson.getString("control_file"))
            assertFalse(operationJson.has("control_token"))
            assertFalse(controlFile.exists())
            assertFalse(context.filesDir.resolve("${controlFile.name}.ready").exists())
        }
    }

    @Test
    fun successful_preserve_active_phase_leaves_the_started_generation_for_external_loss() = runBlocking {
        val operations = listOf("configure", "connect", "observe_tunnel", "observe_routing_identity")
        val commandFile = writeInput(
            "command-preserve-active.json",
            commandJson(
                operations = operations,
                profileFile = "profile-preserve-active.bin",
                preserveActive = true,
            ),
        )
        writeInput("profile-preserve-active.bin", "opaque-profile")
        val controller = FakeSessionController()
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> FakePlatform() },
        ).run(commandFile.name)

        assertEquals(null, result.errorCode)
        assertTrue(result.connected)
        assertTrue(result.tunnelInterface)
        assertTrue(result.routingVerified)
        assertFalse(result.cleanupVerified)
        assertEquals(1, controller.startCalls)
        assertEquals(0, controller.stopCalls)
        assertFalse(controller.events.contains("destroy"))
    }

    @Test
    fun failed_preserve_active_phase_still_cleans_up() = runBlocking {
        val operations = listOf("configure", "connect", "observe_tunnel")
        val commandFile = writeInput(
            "command-preserve-failure.json",
            commandJson(
                operations = operations,
                profileFile = "profile-preserve-failure.bin",
                preserveActive = true,
            ),
        )
        writeInput("profile-preserve-failure.bin", "opaque-profile")
        val controller = FakeSessionController()
        val outputFile = context.filesDir.resolve("observation-3.json").also(files::add)
        val failure = captureFailure {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = { controller },
                platformFactory = { _ -> FakePlatform(tunnelResults = listOf(false)) },
            ).run(commandFile.name)
        }
        val result = JSONObject(outputFile.readText())

        assertEquals("TUNNEL_NOT_OBSERVED", failure.message)
        assertEquals("TUNNEL_NOT_OBSERVED", result.getString("error_code"))
        assertTrue(result.getBoolean("cleanup_verified"))
        assertEquals(1, controller.startCalls)
        assertEquals(1, controller.stopCalls)
        assertTrue(controller.events.contains("destroy"))
    }

    @Test
    fun cancellation_still_completes_session_and_file_cleanup() = runBlocking {
        val outputName = "observation-cancelled.json"
        val profileName = "profile-cancelled.bin"
        val commandFile = writeInput(
            "command-cancelled.json",
            commandJson(
                operations = listOf("configure", "connect", "measure_stability"),
                outputFile = outputName,
                profileFile = profileName,
            ),
        )
        val profileFile = writeInput(profileName, "opaque-profile")
        val outputFile = context.filesDir.resolve(outputName).also(files::add)
        val controller = FakeSessionController()
        val platform = FakePlatform(cancelStability = true)
        val job = launch {
            AndroidHostedProfileTestDriver(
                context = context,
                controllerFactory = { controller },
                platformFactory = { _ -> platform },
            ).run(commandFile.name)
        }
        while ("stability-wait" !in platform.events) yield()
        job.cancelAndJoin()

        assertEquals(1, controller.stopCalls)
        assertTrue(controller.events.contains("destroy"))
        assertFalse(profileFile.exists())
        assertTrue(outputFile.isFile)
    }

    private fun assertInputRejected(json: JSONObject) {
        try {
            AndroidHostedCommandContract.parse(json.toString())
            throw AssertionError("invalid command was accepted")
        } catch (_: AndroidHostedInputException) {
            // Expected.
        }
    }

    private suspend fun captureFailure(action: suspend () -> Unit): Throwable {
        try {
            action()
        } catch (failure: Throwable) {
            return failure
        }
        throw AssertionError("operation unexpectedly succeeded")
    }

    private fun writeInput(name: String, contents: String): File {
        val file = context.filesDir.resolve(name)
        file.writeText(contents)
        files += file
        return file
    }

    private fun commandJson(
        operations: List<String> = listOf(
            "configure", "connect", "observe_tunnel", "observe_routing_identity", "measure_stability",
            "measure_throughput", "disconnect", "inspect_cleanup",
        ),
        outputFile: String = "observation-${operations.size}.json",
        profileFile: String = "profile-${operations.size}.bin",
        preserveActive: Boolean = false,
        profileIndex: Int? = 0,
    ): String {
        val operationArray = JSONArray()
        operations.forEachIndexed { index, operation ->
            operationArray.put(
                JSONObject()
                    .put("id", "step-$index")
                    .put("operation", operation)
                    .put("timeout_seconds", 30)
                    .apply {
                        if (operation in AndroidHostedCommandContract.EXTERNAL_CONTROL_OPERATIONS) {
                            put("control_file", "control-${operations.size}-$index.json")
                        }
                    },
            )
        }
        return JSONObject()
            .put("source_sha", "a".repeat(40))
            .put("profile_file", profileFile)
            .put("output_file", outputFile)
            .apply { profileIndex?.let { put("profile_index", it) } }
            .apply { if (preserveActive) put("preserve_active", true) }
            .put(
                "endpoints",
                JSONObject()
                    .put("identity_url", "https://identity.example.test/ip")
                    .put("latency_url", "https://latency.example.test/trace")
                    .put("download_url", "https://download.example.test/blob")
                    .put("upload_url", "https://upload.example.test/blob"),
            )
            .put("operations", operationArray)
            .toString()
    }

    private class FakePlatform(
        private val failTunnel: Boolean = false,
        private val tunnelResults: List<Boolean> = listOf(true),
        private val cancelStability: Boolean = false,
        val events: MutableList<String> = mutableListOf(),
    ) : AndroidHostedPlatform {

        private var tunnelIndex = 0
        override suspend fun requestConsent() { events += "consent" }
        override suspend fun observeTunnel(): Boolean {
            events += "tunnel"
            if (failTunnel) error("synthetic platform failure")
            val result = tunnelResults.getOrElse(tunnelIndex) { tunnelResults.last() }
            tunnelIndex += 1
            return result
        }
        override suspend fun observeRoutingProof(controlFile: String): Boolean { events += "identity"; return true }
        override suspend fun measureStability(): Boolean {
            if (cancelStability) {
                events += "stability-wait"
                awaitCancellation()
            }
            events += "stability"
            return true
        }
        override suspend fun measureThroughput(): AndroidHostedMetrics {
            events += "throughput"
            return AndroidHostedMetrics(12.5, 20.0, 10.0)
        }
        override suspend fun awaitDisconnected(): Boolean { events += "disconnected"; return true }
    }

    private class FakeSessionController(
        val events: MutableList<String> = mutableListOf(),
        private val profiles: List<SessionProfile> = listOf(
            SessionProfile(0, SessionProtocol.OUTLINE, "Outline"),
        ),
        private val stateAfterStart: SessionState = SessionState.CONNECTED,
        private val failureAfterStart: SessionFailureCode? = null,
        private val destroyFailure: Throwable? = null,
        private val stopFailure: SessionControllerResult.Failure? = null,
        private val staleIdleReplay: Boolean = false,
    ) : SessionController {

        var stopCalls = 0
        var startCalls = 0
        var emittedWatchEvents = 0
        val startTargets = mutableListOf<SessionStartTarget>()
        val startGenerations = mutableListOf<ULong>()
        private var state = SessionState.IDLE
        override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> {
            events += "configure"
            return SessionControllerResult.Success(
                SessionConfiguration(
                    "digest",
                    profiles,
                    emptyList(),
                ),
            )
        }
        override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> {
            events += "start"
            startTargets += target
            startCalls += 1
            state = stateAfterStart
            val generation = startCalls.toULong()
            startGenerations += generation
            return SessionControllerResult.Success(generation)
        }
        override suspend fun stop(generation: ULong): SessionControllerResult<ULong> {
            events += "stop"
            stopCalls += 1
            if (stopCalls == 1) stopFailure?.let { return it }
            state = SessionState.IDLE
            return SessionControllerResult.Success(generation)
        }
        override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> {
            events += "snapshot"
            return SessionControllerResult.Success(
                SessionSnapshot(
                    startCalls.toULong(),
                    state,
                    configured = true,
                    cleanupComplete = state == SessionState.IDLE,
                    lastFailureCode = failureAfterStart.takeIf { state == SessionState.FAILED },
                    sessionId = "test-session",
                ),
            )
        }
        override fun watch(afterSequence: ULong): Flow<SessionEvent> {
            events += "watch"
            val current = SessionEvent(
                generation = startCalls.toULong(),
                sequence = afterSequence + 1uL,
                state = state,
                failureCode = failureAfterStart.takeIf { state == SessionState.FAILED },
                sessionId = "test-session",
            )
            val replay = if (staleIdleReplay && state == SessionState.IDLE && current.generation > 1uL) {
                listOf(current.copy(generation = current.generation - 1uL), current)
            } else {
                listOf(current)
            }
            return flowOf(*replay.toTypedArray()).onEach { emittedWatchEvents += 1 }
        }
        override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> =
            SessionControllerResult.Success(SessionObservation(emptyList(), afterSequence))
        override suspend fun destroy(): SessionControllerResult<Unit> {
            events += "destroy"
            destroyFailure?.let { throw it }
            return SessionControllerResult.Success(Unit)
        }
    }
}
