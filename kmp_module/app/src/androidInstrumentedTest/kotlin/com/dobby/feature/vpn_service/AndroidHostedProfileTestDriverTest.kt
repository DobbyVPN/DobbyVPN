package com.dobby.feature.vpn_service

import android.content.Context
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.dobby.feature.main.domain.SessionConfiguration
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionObservation
import com.dobby.feature.main.domain.SessionSnapshot
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import kotlinx.coroutines.runBlocking
import org.json.JSONArray
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.nio.file.Files

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
    fun command_validation_rejects_wrong_types_unknown_fields_and_placeholder_sha() {
        val valid = JSONObject(commandJson()).apply {
            put("source_sha", "0".repeat(40))
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

        val wrongTimeoutType = JSONObject(commandJson()).apply {
            getJSONArray("operations").getJSONObject(0).put("timeout_seconds", 30.5)
        }
        assertInputRejected(wrongTimeoutType)

        val wrongOperationType = JSONObject(commandJson()).apply {
            getJSONArray("operations").getJSONObject(0).put("id", 7)
        }
        assertInputRejected(wrongOperationType)

        val endpointExtra = JSONObject(commandJson()).apply {
            getJSONObject("endpoints").put("secret", "do-not-accept")
        }
        assertInputRejected(endpointExtra)
    }

    @Test
    fun private_file_validation_rejects_traversal_and_symlink() {
        try {
            AndroidHostedCommandContract.privateFile(context.filesDir, "../outside.json")
            throw AssertionError("path traversal was accepted")
        } catch (_: AndroidHostedInputException) {
            // Expected.
        }

        val target = writeInput("real-command.json", "{}")
        val link = context.filesDir.resolve("command-link.json")
        Files.createSymbolicLink(link.toPath(), target.toPath())
        files += link
        try {
            AndroidHostedCommandContract.privateFile(context.filesDir, link.name)
            throw AssertionError("symlink input was accepted")
        } catch (_: AndroidHostedInputException) {
            // Expected.
        }
    }

    @Test
    fun command_validation_preserves_supplied_operation_order_without_scenario_catalog() {
        val command = AndroidHostedCommandContract.parse(
            "command.json",
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
    fun output_is_exactly_safe_and_never_contains_profile_endpoints_or_literal_identity() = runBlocking {
        val secret = "PROFILE_SECRET https://private.example.test/path 198.51.100.7"
        val commandFile = writeInput("command-safe.json", commandJson(outputFile = "observation-safe.json"))
        val profileFile = writeInput("profile-8.bin", secret)
        val outputFile = context.filesDir.resolve("observation-safe.json")
        files += outputFile
        val controller = FakeSessionController()
        val platform = FakePlatform()
        AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> platform },
        ).run(commandFile.name)

        val output = outputFile.readText()
        assertFalse(output.contains(secret))
        assertFalse(output.contains("https://private.example.test"))
        assertFalse(output.contains("identity.example.test"))
        assertFalse(output.contains("download.example.test"))
        assertFalse(output.contains("198.51.100.7"))
        val keys = JSONObject(output).keys().asSequence().toSet()
        assertEquals(
            setOf(
                "schema", "kind", "platform", "source_sha", "configured", "connected",
                "tunnel_interface", "routing_identity_changed", "disconnect_clean",
                "restart_verified", "reconnect_bounded", "second_tunnel_interface", "second_routing_identity_changed",
                "stability_verified", "latency_ms", "download_mbps", "upload_mbps", "final_disconnect_clean",
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
        val controller = FakeSessionController(eventLog)
        val platform = FakePlatform(events = eventLog)
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> platform },
        ).run(commandFile.name)

        assertEquals(null, result.errorCode)
        assertTrue(result.tunnelInterface)
        assertTrue(result.routingIdentityChanged)
        assertTrue(result.disconnectClean)
        assertTrue(result.restartVerified)
        assertTrue(result.reconnectBounded)
        assertTrue(result.secondTunnelInterface)
        assertTrue(result.secondRoutingIdentityChanged)
        assertTrue(result.finalDisconnectClean)
        assertEquals(2, controller.stopCalls)
        assertEquals(listOf("configure", "consent", "baseline", "start", "snapshot", "tunnel", "identity", "stability", "throughput", "stop", "disconnected", "consent", "baseline", "start", "snapshot", "tunnel", "identity", "stop", "disconnected", "snapshot", "destroy", "disconnected"), eventLog)
    }

    @Test
    fun first_cycle_false_is_not_masked_by_later_values() = runBlocking {
        val commandFile = writeInput("command-first-false.json", commandJson(operations = listOf("configure", "connect", "observe_tunnel")))
        writeInput("profile-first-false.bin", "opaque-profile")
        val controller = FakeSessionController()
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> FakePlatform(tunnelResults = listOf(false)) },
        ).run(commandFile.name)

        assertEquals("TUNNEL_NOT_OBSERVED", result.errorCode)
        assertFalse(result.tunnelInterface)
        assertFalse(result.secondTunnelInterface)
        assertTrue(result.cleanupVerified)
    }

    @Test
    fun second_cycle_false_is_not_masked_by_first_cycle_success() = runBlocking {
        val operations = listOf(
            "configure", "connect", "observe_tunnel", "disconnect", "reconnect", "observe_tunnel",
        )
        val commandFile = writeInput("command-second-false.json", commandJson(operations = operations))
        writeInput("profile-second-false.bin", "opaque-profile")
        val controller = FakeSessionController()
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> FakePlatform(tunnelResults = listOf(true, false)) },
        ).run(commandFile.name)

        assertEquals("TUNNEL_NOT_OBSERVED", result.errorCode)
        assertTrue(result.tunnelInterface)
        assertFalse(result.secondTunnelInterface)
        assertTrue(result.cleanupVerified)
        assertEquals(2, controller.stopCalls)
    }

    @Test
    fun operation_failure_still_attempts_stop_destroy_service_and_safe_cleanup() = runBlocking {
        val commandFile = writeInput("command-failure.json", commandJson(operations = listOf("configure", "connect", "observe_tunnel")))
        writeInput("profile-3.bin", "opaque-profile")
        val controller = FakeSessionController()
        val platform = FakePlatform(failTunnel = true)
        val outputFile = context.filesDir.resolve("observation-failure.json")
        files += outputFile
        val result = AndroidHostedProfileTestDriver(
            context = context,
            controllerFactory = { controller },
            platformFactory = { _ -> platform },
        ).run(commandFile.name)

        assertEquals("DRIVER_ERROR", result.errorCode)
        assertEquals(1, controller.stopCalls)
        assertTrue(controller.events.contains("destroy"))
        assertTrue(platform.events.contains("disconnected"))
        assertTrue(result.cleanupVerified)
        assertFalse(outputFile.readText().contains("opaque-profile"))
    }

    private fun assertInputRejected(json: JSONObject) {
        try {
            AndroidHostedCommandContract.parse("command.json", json.toString())
            throw AssertionError("invalid command was accepted")
        } catch (_: AndroidHostedInputException) {
            // Expected: invalid input is rejected without exposing the offending value.
        }
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
    ): String {
        val operationArray = JSONArray()
        operations.forEachIndexed { index, operation ->
            operationArray.put(
                JSONObject()
                    .put("id", "step-$index")
                    .put("operation", operation)
                    .put("timeout_seconds", 30),
            )
        }
        return JSONObject()
            .put("schema", 1)
            .put("kind", AndroidHostedCommandContract.COMMAND_KIND)
            .put("platform", "android")
            .put("source_sha", "a".repeat(40))
            .put("profile_file", profileFile)
            .put("output_file", outputFile)
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
        val events: MutableList<String> = mutableListOf(),
    ) : AndroidHostedPlatform {

        private var tunnelIndex = 0

        override suspend fun requestConsent() { events += "consent" }
        override suspend fun captureBaseline() { events += "baseline" }
        override suspend fun observeTunnel(): Boolean {
            events += "tunnel"
            if (failTunnel) error("synthetic platform failure")
            val result = tunnelResults.getOrElse(tunnelIndex) { tunnelResults.last() }
            tunnelIndex += 1
            return result
        }
        override suspend fun observeRoutingIdentity(): Boolean { events += "identity"; return true }
        override suspend fun measureStability(): Boolean { events += "stability"; return true }
        override suspend fun measureThroughput(): AndroidHostedMetrics {
            events += "throughput"
            return AndroidHostedMetrics(12.5, 20.0, 10.0)
        }
        override suspend fun awaitDisconnected(): Boolean { events += "disconnected"; return true }
    }

    private class FakeSessionController(val events: MutableList<String> = mutableListOf()) : SessionController {

        var stopCalls = 0
        private var state = SessionState.IDLE
        override suspend fun configure(rawConfig: ByteArray): SessionControllerResult<SessionConfiguration> {
            events += "configure"
            return SessionControllerResult.Success(SessionConfiguration("digest", emptyList(), emptyList()))
        }
        override suspend fun start(target: SessionStartTarget): SessionControllerResult<ULong> {
            events += "start"
            state = SessionState.CONNECTED
            return SessionControllerResult.Success((stopCalls + 1L).toULong())
        }
        override suspend fun stop(generation: ULong): SessionControllerResult<ULong> {
            events += "stop"
            stopCalls += 1
            state = SessionState.IDLE
            return SessionControllerResult.Success(generation)
        }
        override suspend fun snapshot(): SessionControllerResult<SessionSnapshot> {
            events += "snapshot"
            return SessionControllerResult.Success(
                SessionSnapshot(1u, state, configured = true, cleanupComplete = state == SessionState.IDLE),
            )
        }
        override suspend fun observe(afterSequence: ULong): SessionControllerResult<SessionObservation> =
            SessionControllerResult.Success(SessionObservation(emptyList(), afterSequence))
        override suspend fun destroy(): SessionControllerResult<Unit> {
            events += "destroy"
            return SessionControllerResult.Success(Unit)
        }
    }
}
