package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
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
import java.net.HttpURLConnection
import java.net.URL
import java.util.concurrent.TimeUnit
import java.util.regex.Pattern

/**
 * Private, owner-injected qualification for a real Android Go session. The host runner streams
 * the profile and external-identity probe URL into the debug app's private directory and passes
 * only these fixed file names to instrumentation. This class is never part of public CI.
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
            val baselineIdentity = fetchIdentity(privateFiles.probeUrlText)
            privateFiles.probeUrl.delete()

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
            val tunnelIdentity = fetchIdentity(privateFiles.probeUrlText)
            repeat(STABILITY_PROBE_COUNT - 1) {
                assertTrue(
                    "external identity changed during bounded tunnel stability check",
                    tunnelIdentity == fetchIdentity(privateFiles.probeUrlText),
                )
            }
            assertFalse("external identity did not change after the real tunnel connected", baselineIdentity == tunnelIdentity)
            assertTrue("Go session did not stop with complete cleanup", stopRunningSession())
            assertNull(awaitVpnNetwork(present = false))
        } finally {
            privateFiles.config.delete()
            privateFiles.probeUrl.delete()
            stopRunningSession()
            context.stopService(DobbyVpnService.createStopIntent(context, 0, false))
            assertNull(awaitVpnNetwork(present = false))
        }
    }

    private fun requirePrivateFiles(): PrivateFiles {
        val arguments = InstrumentationRegistry.getArguments()
        val configName = arguments.getString(PRIVATE_PROFILE_CONFIG_ARGUMENT).orEmpty()
        val probeName = arguments.getString(PRIVATE_PROFILE_PROBE_ARGUMENT).orEmpty()
        assertTrue(
            "private-profile arguments were not supplied",
            configName.matches(PRIVATE_FILE_NAME) && probeName.matches(PRIVATE_FILE_NAME),
        )
        val config = File(context.filesDir, configName)
        val probeUrl = File(context.filesDir, probeName)
        assertTrue("private-profile files were not injected", config.isFile && probeUrl.isFile)
        val probeUrlText = probeUrl.readText().trim()
        assertTrue("private probe URL was empty", probeUrlText.isNotEmpty())
        return PrivateFiles(config, probeUrl, probeUrlText)
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
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(NETWORK_TIMEOUT_MILLIS)
        while (System.nanoTime() < deadline) {
            val vpn = manager.allNetworks.firstOrNull { network ->
                manager.getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
            }
            if ((vpn != null) == present) return vpn
            Thread.sleep(POLL_INTERVAL_MILLIS)
        }
        return manager.allNetworks.firstOrNull { network ->
            manager.getNetworkCapabilities(network)?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
        }
    }

    private fun fetchIdentity(rawUrl: String): String = try {
        val connection = URL(rawUrl).openConnection() as HttpURLConnection
        connection.connectTimeout = EXTERNAL_PROBE_TIMEOUT_MILLIS
        connection.readTimeout = EXTERNAL_PROBE_TIMEOUT_MILLIS
        try {
            connection.inputStream.use { input ->
                input.readNBytes(MAX_IDENTITY_BYTES).toString(Charsets.UTF_8).trim().also { identity ->
                    require(identity.isNotEmpty()) { "empty" }
                }
            }
        } finally {
            connection.disconnect()
        }
    } catch (_: Exception) {
        throw IllegalStateException("private external-identity probe failed")
    }

    private data class PrivateFiles(val config: File, val probeUrl: File, val probeUrlText: String)
    private data class RunningSession(val controller: SessionController, val generation: ULong)

    private companion object {
        const val CONSENT_TIMEOUT_MILLIS = 10_000L
        const val NETWORK_TIMEOUT_MILLIS = 10_000L
        const val EXTERNAL_PROBE_TIMEOUT_MILLIS = 10_000
        const val POLL_INTERVAL_MILLIS = 100L
        const val SESSION_STATE_POLL_COUNT = 80
        const val SESSION_STATE_POLL_MILLIS = 250L
        const val STABILITY_PROBE_COUNT = 3
        const val MAX_IDENTITY_BYTES = 1024
        const val PRIVATE_PROFILE_CONFIG_ARGUMENT = "dobby.private_profile_config_file"
        const val PRIVATE_PROFILE_PROBE_ARGUMENT = "dobby.private_probe_url_file"
        val PRIVATE_FILE_NAME = Regex("[A-Za-z0-9][A-Za-z0-9._-]{0,79}")
    }
}
