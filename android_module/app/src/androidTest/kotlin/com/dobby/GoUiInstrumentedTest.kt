package com.dobby

import android.content.Intent
import android.graphics.Rect
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TestWatcher
import org.junit.runner.Description
import org.junit.runner.RunWith
import java.io.File

/** Real-renderer smoke against the signed release APK and Android's native input path. */
@RunWith(AndroidJUnit4::class)
class GoUiInstrumentedTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)
    private val packageName = instrumentation.targetContext.packageName
    private var lastTapDiagnostic = "none"

    @get:Rule
    val failureDiagnostics = object : TestWatcher() {
        override fun failed(error: Throwable?, description: Description?) {
            captureFailureDiagnostics()
        }
    }

    @Test
    fun releaseUiTypesAndShowsConnectFailureThenReopens() {
        failureScreenshot().delete()
        failureTree().delete()
        device.pressHome()
        launch()
        device.wait(androidx.test.uiautomator.Until.hasObject(By.pkg(packageName)), 10_000)

        requireObject("Disconnected")
        requireObject("Connect")

        tapStable("Connection configuration")
        val nativeInput = waitForFocusedNativeInput(10_000)
        nativeInput.setText("invalidprofile")
        device.waitForIdle()
        waitForNativeInputText("invalidprofile", 10_000)
        nativeInput.setText("")
        device.waitForIdle()
        waitForNativeInputCleared(10_000)
        device.pressBack()
        if (!waitForNativeInputGone(1_000)) {
            // Some IMEs consume the first Back themselves. The second then
            // reaches GoNativeActivity, whose keyboardUp branch hides Fyne's
            // native EditText without finishing the activity.
            device.pressBack()
        }
        if (!waitForNativeInputGone(5_000)) {
            throw AssertionError("Android did not dismiss Fyne's native input view")
        }

        // Navigate only after typing so a real control transition proves the
        // Entry focus/IME teardown completed and the entered source survives
        // an in-app screen change before Connect is exercised.
        tapAndWaitForVisible("Settings", "Back")
        tapAndWaitForVisible("Back", "Disconnected")
        tapAndWaitForFailureOutcome()

        // Exercise the user-visible mobile lifecycle. Fyne's Go runtime owns
        // one NativeActivity window per process, so finishing that Activity
        // from inside the still-running instrumentation process cannot create
        // a second Go window. Process death/restart is covered by the hosted
        // functional scenario; this renderer test backgrounds and reopens it.
        backgroundActivity()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Error", "Failed"), 30_000)
        requireObject("Connect")
    }

    private fun launch() {
        val launch = instrumentation.targetContext.packageManager
            .getLaunchIntentForPackage(packageName)
            // Match a launcher-icon reopen. CLEAR_TOP can recreate the native
            // Activity while Fyne's live Go runtime still owns the original
            // window, yielding a blank replacement surface.
            ?.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            ?: throw IllegalStateException("Go/Fyne launcher activity is missing")
        instrumentation.targetContext.startActivity(launch)
    }

    private fun backgroundActivity() {
        device.pressHome()
        if (!device.wait(androidx.test.uiautomator.Until.gone(By.pkg(packageName)), 5_000)) {
            throw AssertionError("Android Go/Fyne activity did not background before reopen")
        }
    }

    private fun requireObject(label: String, timeoutMillis: Long = 10_000): UiObject2 {
        val object2 = waitForObject(label, timeoutMillis)
        return object2 ?: throw AssertionError("Android UI did not expose $label")
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
        throw AssertionError("Android tap did not focus Fyne's native input view")
    }

    private fun waitForNativeInputText(expected: String, timeoutMillis: Long) {
        val selector = By.clazz("android.widget.EditText").pkg(packageName)
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            val value = device.findObject(selector)?.text.orEmpty()
            if (expected in value) return
            Thread.sleep(100)
        }
        throw AssertionError("Android native input did not deliver text to Fyne")
    }

    private fun waitForNativeInputCleared(timeoutMillis: Long) {
        val selector = By.clazz("android.widget.EditText").pkg(packageName)
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            val value = device.findObject(selector)?.text
            if (value != null && value.trim().isEmpty()) return
            Thread.sleep(100)
        }
        throw AssertionError("Android native input did not clear Fyne's text bridge")
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
        throw AssertionError("Android $label control did not reach stable tappable bounds")
    }

    private fun tapStable(label: String) {
        val bounds = waitForStableBounds(label, 10_000)
        val x = bounds.centerX()
        val y = bounds.centerY()
        lastTapDiagnostic = "$label:${bounds.flattenToString()}@$x,$y"
        if (!device.click(x, y)) {
            throw AssertionError("Android touch injection failed for $label")
        }
        device.waitForIdle()
    }

    private fun tapAndWaitForFailureOutcome() {
        val outcomes = arrayOf("Error", "Failed")
        tapStable("Connect")
        waitForOneOf(outcomes, 10_000)
    }

    private fun tapAndWaitForVisible(control: String, outcome: String) {
        tapStable(control)
        if (waitForObject(outcome, 10_000) == null) {
            throw AssertionError("Android $control control did not expose $outcome")
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
        val known = arrayOf(
            "Disconnected", "Connecting", "Connected", "Error", "Failed",
            "Connect", "Disconnect",
        ).filter { label ->
            device.findObject(By.text(label).pkg(packageName)) != null ||
                device.findObject(By.desc(label).pkg(packageName)) != null
        }
        val nativeInputPresent = device.findObject(
            By.clazz("android.widget.EditText").pkg(packageName)
        ) != null
        throw AssertionError(
            "Android UI did not expose any of ${labels.joinToString()}; " +
                "visible states=${known.joinToString()}; " +
                "native input present=$nativeInputPresent; " +
                "last tap=$lastTapDiagnostic; display=${device.displayWidth}x${device.displayHeight}"
        )
    }

    private fun captureFailureDiagnostics() {
        try {
            device.takeScreenshot(failureScreenshot())
        } catch (_: Throwable) {
            // The assertion remains authoritative when optional diagnostics
            // cannot be written by a particular device image.
        }
        try {
            device.dumpWindowHierarchy(failureTree())
        } catch (_: Throwable) {
            // Keep the original UI failure rather than replacing it.
        }
    }

    private fun failureScreenshot(): File =
        File(instrumentation.targetContext.filesDir, "dobbyvpn-ui-failure.png")

    private fun failureTree(): File =
        File(instrumentation.targetContext.filesDir, "dobbyvpn-ui-failure.xml")

}
