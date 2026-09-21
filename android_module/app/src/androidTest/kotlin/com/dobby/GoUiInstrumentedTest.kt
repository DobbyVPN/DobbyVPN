package com.dobby

import android.content.Intent
import android.graphics.Rect
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.Configurator
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/** Real-renderer smoke against the signed release APK and Android's native input path. */
@RunWith(AndroidJUnit4::class)
class GoUiInstrumentedTest {
    private val connectionActionLabel = "VPN connection action"
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)
    private val packageName = instrumentation.targetContext.packageName

    @Before
    fun configureBoundedSelectorPolling() {
        // UiDevice.findObject() otherwise waits for UiAutomator's global
        // selector timeout even when this class is deliberately polling with
        // its own deadline. On the API 35 image that default wait is several
        // seconds, so a pair of missing selectors can turn a 30-second
        // product timeout into a multi-minute test. Keep discovery
        // non-blocking and let the helpers below own the timing.
        Configurator.getInstance().setWaitForSelectorTimeout(0)
    }

    @Test
    fun releaseUiTypesAndShowsConnectFailureThenReopens() {
        device.pressHome()
        launch()
        device.wait(androidx.test.uiautomator.Until.hasObject(By.pkg(packageName)), 10_000)

        waitForOneOf(arrayOf("Disconnected", "Ready"), 30_000)
        requireObject(connectionActionLabel)

        tapStable("Connection configuration")
        val nativeInput = waitForFocusedNativeInput(10_000)
        nativeInput.setText("invalidprofile")
        // ACTION_SET_TEXT is consumed by Fyne's Go-side Entry and the
        // short-lived EditText may disappear before UiAutomator can read it
        // back.  The later rendered Connect -> Error/Failed transition is
        // the stable product-level proof that this invalid source reached Go.
        device.waitForIdle()
        device.pressBack()
        if (!waitForNativeInputGone(1_000)) {
            // Some IMEs consume the first Back themselves. The second then
            // reaches GoNativeActivity, whose keyboardUp branch hides Fyne's
            // native EditText without finishing the activity.
            device.pressBack()
        }
        if (!waitForNativeInputGone(5_000)) {
            throw AssertionError("ANDROID_UI_INPUT_DISMISS_FAILED")
        }

        // Navigate only after typing so a real control transition proves the
        // Entry focus/IME teardown completed and the entered source survives
        // an in-app screen change before Connect is exercised.
        tapAndWaitForVisible("Settings", "Back")
        tapStable("Back")
        waitForOneOf(arrayOf("Disconnected", "Ready"), 30_000)
        tapAndWaitForFailureOutcome()

        // Exercise the user-visible mobile lifecycle. Fyne's Go runtime owns
        // one NativeActivity window per process, so finishing that Activity
        // from inside the still-running instrumentation process cannot create
        // a second Go window. Process death/restart is covered by the hosted
        // functional scenario; this renderer test backgrounds and reopens it.
        backgroundActivity()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Ready", "Error", "Failed"), 30_000)
        requireObject(connectionActionLabel)
    }

    private fun launch() {
        val launch = instrumentation.targetContext.packageManager
            .getLaunchIntentForPackage(packageName)
            // Match a launcher-icon reopen. CLEAR_TOP can recreate the native
            // Activity while Fyne's live Go runtime still owns the original
            // window, yielding a blank replacement surface.
            ?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            ?: throw IllegalStateException("ANDROID_LAUNCH_ACTIVITY_MISSING")
        instrumentation.targetContext.startActivity(launch)
    }

    private fun backgroundActivity() {
        device.pressHome()
        if (!device.wait(androidx.test.uiautomator.Until.gone(By.pkg(packageName)), 5_000)) {
            throw AssertionError("ANDROID_UI_BACKGROUND_FAILED")
        }
    }

    private fun requireObject(label: String, timeoutMillis: Long = 10_000): UiObject2 {
        val object2 = waitForObject(label, timeoutMillis)
        return object2 ?: throw AssertionError("ANDROID_UI_CONTROL_TIMEOUT")
    }

    private fun waitForObject(label: String, timeoutMillis: Long): UiObject2? {
        val selectors = arrayOf(
            By.text(label).pkg(packageName),
            By.desc(label).pkg(packageName),
        )
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            for (selector in selectors) {
                device.findObject(selector)?.let { return it }
            }
            Thread.sleep(100)
        }
        return null
    }

    private fun waitForFocusedNativeInput(timeoutMillis: Long): UiObject2 {
        val selector = By.clazz("android.widget.EditText").pkg(packageName)
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            device.findObject(selector)?.let { input ->
                if (input.isFocused) return input
            }
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_UI_INPUT_FOCUS_TIMEOUT")
    }

    private fun waitForNativeInputGone(timeoutMillis: Long): Boolean {
        val selector = By.clazz("android.widget.EditText").pkg(packageName)
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            if (device.findObject(selector) == null) return true
            Thread.sleep(100)
        }
        return device.findObject(selector) == null
    }

    private fun waitForStableBounds(label: String, timeoutMillis: Long): Rect {
        val deadline = System.currentTimeMillis() + timeoutMillis
        var previous: Rect? = null
        var stableSamples = 0
        while (System.currentTimeMillis() < deadline) {
            val current = waitForObject(label, 100)?.visibleBounds
            if (current != null && !current.isEmpty) {
                if (current == previous) {
                    stableSamples++
                    if (stableSamples >= 10) return Rect(current)
                } else {
                    previous = Rect(current)
                    stableSamples = 0
                }
            }
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_UI_CONTROL_TIMEOUT")
    }

    private fun tapStable(label: String) {
        val bounds = waitForStableBounds(label, 10_000)
        val x = bounds.centerX()
        val y = bounds.centerY()
        if (!device.click(x, y)) {
            throw AssertionError("ANDROID_UI_TAP_FAILED")
        }
        device.waitForIdle()
    }

    private fun tapAndWaitForFailureOutcome() {
        val outcomes = arrayOf("Error", "Failed")
        tapStable(connectionActionLabel)
        waitForOneOf(outcomes, 10_000)
    }

    private fun tapAndWaitForVisible(control: String, outcome: String) {
        tapStable(control)
        if (waitForObject(outcome, 10_000) == null) {
            throw AssertionError("ANDROID_UI_STATE_TIMEOUT")
        }
    }

    private fun waitForOneOfOrNull(labels: Array<String>, timeoutMillis: Long): UiObject2? {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            for (label in labels) {
                waitForObject(label, 100)?.let { return it }
            }
            Thread.sleep(100)
        }
        return null
    }

    private fun waitForOneOf(labels: Array<String>, timeoutMillis: Long): UiObject2 {
        waitForOneOfOrNull(labels, timeoutMillis)?.let { return it }
        throw AssertionError("ANDROID_UI_STATE_TIMEOUT")
    }

}
