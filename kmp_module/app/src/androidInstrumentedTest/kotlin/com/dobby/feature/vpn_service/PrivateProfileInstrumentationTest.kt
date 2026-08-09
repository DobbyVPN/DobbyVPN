package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import com.dobby.feature.main.domain.AndroidSessionController
import com.dobby.feature.main.domain.SessionController
import com.dobby.feature.main.domain.SessionControllerResult
import com.dobby.feature.main.domain.SessionStartTarget
import com.dobby.feature.main.domain.SessionState
import com.dobby.feature.main.domain.SessionSnapshot
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File
import java.io.FileOutputStream
import java.net.HttpURLConnection
import java.net.Inet4Address
import java.net.InetAddress
import java.net.URL
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.concurrent.TimeUnit
import java.util.regex.Pattern
import org.json.JSONObject

/**
 * Private, owner-injected qualification for a real Android Go session. The host runner streams
 * the profile into the debug app's private directory and passes only a fixed file name to
 * instrumentation. It records fixed-source external-identity observations solely in an
 * app-private file which the local Harness retrieves before clearing app state. This class is
 * never part of public CI.
 */
@RunWith(AndroidJUnit4::class)
class PrivateProfileInstrumentationTest {
    private val context: Context
        get() = InstrumentationRegistry.getInstrumentation().targetContext
    private var runningSession: RunningSession? = null

    @After
    fun cleanup() = runBlocking {
        stopRunningSession()
        context.stopService(DobbyVpnService.createStopIntent(context, 0, false))
        assertNull(awaitVpnNetwork(present = false))
    }

    @Test
    fun owner_injected_profile_proves_external_identity_stability_and_cleanup(): Unit = runBlocking {
        val privateFiles = requirePrivateFiles()
        try {
            grantVpnConsentThroughSystemUi()
            resetIdentityEvidence()
            val baselineIdentity = observeIdentities()
            appendIdentityEvidence(privateFiles, phase = "baseline", observations = baselineIdentity)

            val controller = AndroidSessionController(context)
            val config = privateFiles.config.readBytes()
            val configured = try {
                controller.configure(config)
            } finally {
                config.fill(0)
                privateFiles.config.delete()
            }
            assertTrue("owner-injected profile was not accepted", configured is SessionControllerResult.Success)

            val started = controller.start(SessionStartTarget.AutoSelect)
            val generation = (started as? SessionControllerResult.Success)?.value
            assertNotNull("owner-injected profile did not start", generation)
            runningSession = RunningSession(controller, requireNotNull(generation))

            val connectionSnapshot = awaitSessionState(controller, SessionState.CONNECTED)
            assertTrue(
                "owner-injected profile did not reach CONNECTED " +
                    "state=${connectionSnapshot?.state} failure=${connectionSnapshot?.lastFailureCode}",
                connectionSnapshot?.state == SessionState.CONNECTED,
            )
            val tunnelIdentity = observeIdentities()
            appendIdentityEvidence(privateFiles, phase = "tunneled", observations = tunnelIdentity)
            repeat(STABILITY_PROBE_COUNT - 1) {
                assertTrue(
                    "external identity changed during bounded tunnel stability check",
                    tunnelIdentity == observeIdentities(),
                )
            }
            IDENTITY_SOURCES.forEach { source ->
                assertFalse(
                    "$source external identity did not change after the real tunnel connected",
                    baselineIdentity.getValue(source) == tunnelIdentity.getValue(source),
                )
            }
            assertTrue("bounded tunnel throughput check failed", downloadBoundedMegabyte())
            assertTrue("Go session did not stop with complete cleanup", stopRunningSession())
            assertNull(awaitVpnNetwork(present = false))
        } finally {
            privateFiles.config.delete()
            stopRunningSession()
            context.stopService(DobbyVpnService.createStopIntent(context, 0, false))
            assertNull(awaitVpnNetwork(present = false))
        }
    }

    private fun requirePrivateFiles(): PrivateFiles {
        val arguments = InstrumentationRegistry.getArguments()
        val configName = arguments.getString(PRIVATE_PROFILE_CONFIG_ARGUMENT).orEmpty()
        val sourceSha = arguments.getString(SOURCE_SHA_ARGUMENT).orEmpty()
        val scenarioIndex = arguments.getString(SCENARIO_INDEX_ARGUMENT).orEmpty().toIntOrNull()
        assertTrue(
            "private-profile arguments were not supplied",
            configName.matches(PRIVATE_FILE_NAME) &&
                sourceSha.matches(SOURCE_SHA) &&
                scenarioIndex != null &&
                scenarioIndex in 1..MAX_SCENARIOS,
        )
        val config = File(context.filesDir, configName)
        assertTrue("private-profile file was not injected", config.isFile)
        return PrivateFiles(config, sourceSha, requireNotNull(scenarioIndex))
    }

    private fun grantVpnConsentThroughSystemUi() {
        if (VpnService.prepare(context) == null) return
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        instrumentation.startActivitySync(
            Intent(context, VpnConsentTestActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
        val device = UiDevice.getInstance(instrumentation)
        val approval = device.wait(
            Until.findObject(By.res(Pattern.compile(".+:id/button1"))),
            CONSENT_TIMEOUT_MILLIS,
        )
        assertNotNull("Android VPN consent dialog did not expose its approval button", approval)
        requireNotNull(approval).click()

        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(CONSENT_TIMEOUT_MILLIS)
        while (System.nanoTime() < deadline) {
            if (VpnService.prepare(context) == null) return
            Thread.sleep(POLL_INTERVAL_MILLIS)
        }
        assertNull("Android did not grant VPN consent after the system dialog was approved", VpnService.prepare(context))
    }

    private suspend fun awaitSessionState(
        controller: SessionController,
        expected: SessionState,
    ): SessionSnapshot? {
        var latest: SessionSnapshot? = null
        repeat(SESSION_STATE_POLL_COUNT) {
            val snapshot = controller.snapshot()
            if (snapshot is SessionControllerResult.Success) {
                latest = snapshot.value
                if (snapshot.value.state == expected || snapshot.value.state == SessionState.FAILED) return snapshot.value
            }
            delay(SESSION_STATE_POLL_MILLIS)
        }
        return latest
    }

    private suspend fun stopRunningSession(): Boolean {
        val session = runningSession ?: return true
        runningSession = null
        val stopSucceeded = try {
            session.controller.stop(session.generation) is SessionControllerResult.Success
        } catch (_: Exception) {
            // Destroy and the Android service stop below are still required cleanup attempts.
            false
        }
        val disconnected = awaitSessionState(session.controller, SessionState.IDLE)?.state == SessionState.IDLE
        val snapshot = try {
            session.controller.snapshot()
        } catch (_: Exception) {
            null
        }
        val cleanupComplete =
            snapshot is SessionControllerResult.Success &&
                snapshot.value.state == SessionState.IDLE &&
                snapshot.value.cleanupComplete
        try {
            session.controller.destroy()
        } catch (_: Exception) {
            // The test must proceed to Android cleanup without exposing private failure details.
        }
        return stopSucceeded && disconnected && cleanupComplete
    }

    private fun awaitVpnNetwork(present: Boolean): Network? {
        val manager = requireNotNull(context.getSystemService(ConnectivityManager::class.java))
        return manager.awaitVpnNetwork(
            present = present,
            timeoutMillis = NETWORK_TIMEOUT_MILLIS,
            pollIntervalMillis = POLL_INTERVAL_MILLIS
        )
    }

    private fun observeIdentities(): Map<String, String> = mapOf(
        "yandex" to fetchYandexIdentity(),
        "independent" to fetchIndependentIdentity(),
    )

    private fun fetchIndependentIdentity(): String = fetchBytes(INDEPENDENT_IDENTITY_URL, MAX_INDEPENDENT_IDENTITY_BYTES)
        .toString(Charsets.US_ASCII)
        .trim()
        .let(::canonicalPublicIpv4)

    private fun fetchYandexIdentity(): String {
        val body = fetchBytes(YANDEX_IDENTITY_URL, MAX_IDENTITY_BYTES).toString(Charsets.UTF_8)
        val occurrences = YANDEX_IPV4_PATTERN.findAll(body)
            .mapNotNull { match -> runCatching { canonicalPublicIpv4(match.value) }.getOrNull() }
            .groupingBy { it }
            .eachCount()
            .filterValues { count -> count >= 2 }
            .keys
        check(occurrences.size == 1) { "Yandex external-identity response was ambiguous" }
        return occurrences.single()
    }

    private fun fetchBytes(rawUrl: String, maximumBytes: Int): ByteArray = try {
        val connection = URL(rawUrl).openConnection() as HttpURLConnection
        connection.connectTimeout = EXTERNAL_PROBE_TIMEOUT_MILLIS
        connection.readTimeout = EXTERNAL_PROBE_TIMEOUT_MILLIS
        connection.instanceFollowRedirects = false
        connection.setRequestProperty("User-Agent", "DobbyVPN-Harness/1")
        try {
            check(connection.responseCode == HttpURLConnection.HTTP_OK) { "identity provider status" }
            connection.inputStream.use { input ->
                val output = ByteArray(maximumBytes + 1)
                var offset = 0
                while (offset < output.size) {
                    val count = input.read(output, offset, output.size - offset)
                    if (count < 0) break
                    offset += count
                }
                check(offset in 1..maximumBytes) { "identity response size" }
                output.copyOf(offset)
            }
        } finally {
            connection.disconnect()
        }
    } catch (_: Exception) {
        throw IllegalStateException("fixed external-identity probe failed")
    }

    private fun canonicalPublicIpv4(value: String): String {
        val trimmed = value.trim()
        check(IPV4_TEXT.matches(trimmed)) { "external identity was not IPv4" }
        val address = InetAddress.getByName(trimmed) as? Inet4Address
            ?: throw IllegalStateException("external identity was not IPv4")
        val canonical = address.hostAddress
        check(canonical == trimmed) { "external identity was not canonical IPv4" }
        val bytes = address.address.map { it.toInt() and 0xff }
        check(
            bytes[0] !in setOf(0, 10, 127) &&
                !(bytes[0] == 100 && bytes[1] in 64..127) &&
                !(bytes[0] == 169 && bytes[1] == 254) &&
                !(bytes[0] == 172 && bytes[1] in 16..31) &&
                !(bytes[0] == 192 && bytes[1] == 0) &&
                !(bytes[0] == 192 && bytes[1] == 168) &&
                !(bytes[0] == 198 && bytes[1] in 18..19) &&
                !(bytes[0] == 198 && bytes[1] == 51 && bytes[2] == 100) &&
                !(bytes[0] == 203 && bytes[1] == 0 && bytes[2] == 113) &&
                bytes[0] !in 224..255,
        ) { "external identity was not public IPv4" }
        return canonical
    }

    private fun resetIdentityEvidence() {
        val evidence = identityEvidenceFile()
        check(!evidence.exists() || evidence.delete()) { "could not reset app-private identity evidence" }
    }

    private fun appendIdentityEvidence(privateFiles: PrivateFiles, phase: String, observations: Map<String, String>) {
        check(phase in IDENTITY_PHASES && observations.keys == IDENTITY_SOURCES) { "invalid identity evidence" }
        val evidence = identityEvidenceFile()
        FileOutputStream(evidence, true).bufferedWriter(Charsets.UTF_8).use { output ->
            IDENTITY_SOURCES.forEach { source ->
                val record = JSONObject()
                    .put("schema", 1)
                    .put("source_sha", privateFiles.sourceSha)
                    .put("platform", "android")
                    .put("scenario", "profile-${privateFiles.scenarioIndex}")
                    .put("source", source)
                    .put("phase", phase)
                    .put("observed_at", utcTimestamp())
                    .put("observed_ipv4", observations.getValue(source))
                output.write(record.toString())
                output.newLine()
            }
            output.flush()
        }
        evidence.setReadable(false, false)
        evidence.setWritable(false, false)
        evidence.setReadable(true, true)
        evidence.setWritable(true, true)
    }

    private fun identityEvidenceFile(): File {
        val output = File(context.filesDir, IDENTITY_EVIDENCE_FILE)
        check(output.parentFile?.canonicalFile == context.filesDir.canonicalFile) { "identity evidence path was unsafe" }
        return output
    }

    private fun utcTimestamp(): String = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss'Z'", Locale.US).apply {
        timeZone = TimeZone.getTimeZone("UTC")
    }.format(Date())

    private fun downloadBoundedMegabyte(): Boolean {
        return try {
            val started = System.nanoTime()
            val connection = URL(THROUGHPUT_URL).openConnection() as HttpURLConnection
            connection.connectTimeout = THROUGHPUT_TIMEOUT_MILLIS
            connection.readTimeout = THROUGHPUT_TIMEOUT_MILLIS
            connection.instanceFollowRedirects = false
            try {
                if (connection.responseCode != HttpURLConnection.HTTP_OK) return false
                connection.inputStream.use { input ->
                    val buffer = ByteArray(16 * 1024)
                    var total = 0
                    while (total < THROUGHPUT_BYTES) {
                        val count = input.read(buffer, 0, minOf(buffer.size, THROUGHPUT_BYTES - total))
                        if (count < 0) return false
                        total += count
                    }
                    total == THROUGHPUT_BYTES &&
                        TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started) <= THROUGHPUT_TIMEOUT_MILLIS
                }
            } finally {
                connection.disconnect()
            }
        } catch (_: Exception) {
            false
        }
    }

    private data class PrivateFiles(val config: File, val sourceSha: String, val scenarioIndex: Int)
    private data class RunningSession(val controller: SessionController, val generation: ULong)

    private companion object {
        const val CONSENT_TIMEOUT_MILLIS = 10_000L
        const val NETWORK_TIMEOUT_MILLIS = 10_000L
        const val EXTERNAL_PROBE_TIMEOUT_MILLIS = 20_000
        const val THROUGHPUT_TIMEOUT_MILLIS = 30_000
        const val POLL_INTERVAL_MILLIS = 100L
        const val SESSION_STATE_POLL_COUNT = 80
        const val SESSION_STATE_POLL_MILLIS = 250L
        const val STABILITY_PROBE_COUNT = 3
        const val MAX_IDENTITY_BYTES = 1024 * 1024
        const val MAX_INDEPENDENT_IDENTITY_BYTES = 128
        const val THROUGHPUT_BYTES = 1024 * 1024
        const val MAX_SCENARIOS = 64
        const val PRIVATE_PROFILE_CONFIG_ARGUMENT = "dobby.private_profile_config_file"
        const val SOURCE_SHA_ARGUMENT = "dobby.source_sha"
        const val SCENARIO_INDEX_ARGUMENT = "dobby.real_profile_index"
        const val IDENTITY_EVIDENCE_FILE = "dobby-private-identity-evidence.jsonl"
        const val YANDEX_IDENTITY_URL = "https://yandex.ru/internet/"
        const val INDEPENDENT_IDENTITY_URL = "https://api.ipify.org"
        const val THROUGHPUT_URL = "https://speed.cloudflare.com/__down?bytes=1048576"
        val PRIVATE_FILE_NAME = Regex("[A-Za-z0-9][A-Za-z0-9._-]{0,79}")
        val SOURCE_SHA = Regex("[0-9a-f]{40}")
        val IPV4_TEXT = Regex("(?:[0-9]{1,3}\\.){3}[0-9]{1,3}")
        val YANDEX_IPV4_PATTERN = Regex("(?<![0-9.])(?:[0-9]{1,3}\\.){3}[0-9]{1,3}(?![0-9.])")
        val IDENTITY_SOURCES = linkedSetOf("yandex", "independent")
        val IDENTITY_PHASES = setOf("baseline", "tunneled")
    }
}
