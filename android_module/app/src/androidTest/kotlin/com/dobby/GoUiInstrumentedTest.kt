package com.dobby

import android.content.Intent
import android.view.KeyEvent
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import java.io.File

/** Real-renderer smoke against the signed release APK, including VPN consent. */
@RunWith(AndroidJUnit4::class)
class GoUiInstrumentedTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)
    private val packageName = instrumentation.targetContext.packageName

    @Test
    fun releaseUiTypesProfileDeniesAndApprovesVpnReopensAndDisconnects() {
        device.pressHome()
        launch()
        device.wait(androidx.test.uiautomator.Until.hasObject(By.pkg(packageName)), 10_000)

        requireObject("Disconnected")
        requireObject("Connect")
        requireObject("Settings").click()
        requireObject("Back")
        val version = device.findObjects(By.textStartsWith("Version:"))
            .mapNotNull { it.text }
            .firstOrNull { it.matches(Regex("Version: [0-9]+\\.[0-9]+\\.[0-9]+(?:[-+].*)?")) }
        assertTrue("release Settings view did not expose a numeric version", version != null)
        requireObject("Back").click()

        val profilePath = InstrumentationRegistry.getArguments().getString(PROFILE_ARGUMENT)
            ?: throw IllegalArgumentException("ANDROID_UI_PROFILE_ARGUMENT_MISSING")
        val profile = File(profilePath).takeIf { it.isFile }?.readText()
            ?: throw IllegalArgumentException("ANDROID_UI_PROFILE_UNAVAILABLE:$profilePath")
        require(profile.isNotBlank()) { "ANDROID_UI_PROFILE_EMPTY" }
        val input = requireObject("Connection configuration")
        input.click()
        enterProfile(profile)

        requireObject("Connect").click()
        val denial = waitForOneOf(CONSENT_DENY, 15_000)
        denial.click()
        requireObject("Connect", timeoutMillis = 15_000)

        // A second visible Connect action must retry the native permission
        // boundary. Only the system consent button below authorizes the VPN;
        // network/routing observations remain in the hosted functional lane.
        requireObject("Connect").click()
        waitForOneOf(CONSENT_ALLOW, 15_000).click()
        requireObject("Connected", timeoutMillis = 60_000)
        requireObject("Disconnect")

        // Finish and relaunch only the UI activity; the service-owned session
        // must remain visible after reopen.
        leaveActivity()
        launch()
        requireObject("Connected", timeoutMillis = 30_000)
        requireObject("Disconnect").click()
        requireObject("Disconnected", timeoutMillis = 60_000)
    }

    private fun launch() {
        val launch = instrumentation.targetContext.packageManager
            .getLaunchIntentForPackage(packageName)
            ?.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_NEW_TASK)
            ?: throw IllegalStateException("Go/Fyne launcher activity is missing")
        instrumentation.targetContext.startActivity(launch)
    }

    private fun leaveActivity() {
        // The first Back may only dismiss the software keyboard opened by the
        // real profile Entry. Finish the activity only after the keyboard has
        // had a chance to close, then require the app's accessibility tree to
        // disappear before launching a genuinely fresh activity instance.
        device.pressBack()
        if (!device.wait(androidx.test.uiautomator.Until.gone(By.pkg(packageName)), 1_000)) {
            device.pressBack()
        }
        if (!device.wait(androidx.test.uiautomator.Until.gone(By.pkg(packageName)), 5_000)) {
            throw AssertionError("Android Go/Fyne activity did not finish before reopen")
        }
    }

    private fun requireObject(label: String, timeoutMillis: Long = 10_000): UiObject2 {
        val object2 = waitForObject(label, timeoutMillis)
        return object2 ?: throw AssertionError("Android UI did not expose $label")
    }

    private fun waitForObject(label: String, timeoutMillis: Long): UiObject2? {
        val selectors = arrayOf(By.text(label), By.desc(label))
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            for (selector in selectors) {
                device.findObject(selector)?.let { return it }
            }
            Thread.sleep(100)
        }
        return null
    }

    private fun waitForOneOf(labels: Array<String>, timeoutMillis: Long): UiObject2 {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            for (label in labels) {
                waitForObject(label, 100)?.let { return it }
            }
            Thread.sleep(100)
        }
        throw AssertionError("Android UI did not expose any of ${labels.joinToString()}")
    }

    private fun enterProfile(profile: String) {
        // Fyne exposes the Entry through a transparent accessibility node, so
        // UiObject2.setText is not available. After the native tap focuses the
        // real Entry, send the profile via Android's input channel instead.
        device.pressKeyCode(KeyEvent.KEYCODE_A, KeyEvent.META_CTRL_ON)
        device.pressKeyCode(KeyEvent.KEYCODE_DEL)
        val lines = profile.split("\n")
        lines.forEachIndexed { index, line ->
            if (line.isNotEmpty()) {
                device.executeShellCommand("input text ${shellQuote(encodeInputText(line))}")
            }
            if (index + 1 < lines.size) {
                device.pressEnter()
            }
        }
    }

    private fun encodeInputText(value: String): String =
        value.replace("%", "%25").replace(" ", "%s")

    private fun shellQuote(value: String): String =
        "'${value.replace("'", "'\\''")}'"

    companion object {
        private const val PROFILE_ARGUMENT = "dobby.ui_profile"
        private val CONSENT_DENY = arrayOf("Cancel", "Deny", "Don't allow", "Don’t allow", "NO")
        private val CONSENT_ALLOW = arrayOf("Allow", "OK", "Start now", "Allow VPN")
    }
}
