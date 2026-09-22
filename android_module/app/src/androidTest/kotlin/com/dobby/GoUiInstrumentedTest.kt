package com.dobby

import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Rect
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.Configurator
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.junit.rules.TestWatcher
import org.junit.runner.Description
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest

/** Real-renderer smoke against the signed release APK and Android's native input path. */
@RunWith(AndroidJUnit4::class)
class GoUiInstrumentedTest {
    private val connectionActionLabel = "VPN connection action"
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)
    private val packageName = instrumentation.targetContext.packageName
    private val screenshotDirectory = File(
        instrumentation.context.cacheDir,
        "dobbyvpn-rendered-screenshots",
    )

    @get:Rule
    val screenshotOnFailure: TestWatcher = object : TestWatcher() {
        override fun failed(error: Throwable?, description: Description?) {
            try {
                captureScreenshot("failure")
            } catch (captureError: Throwable) {
                val failure = AssertionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED label=failure: " +
                        (captureError.message ?: captureError::class.java.simpleName),
                    captureError,
                )
                if (error != null) {
                    error.addSuppressed(failure)
                } else {
                    throw failure
                }
            }
        }
    }

    @Before
    fun configureBoundedSelectorPolling() {
        // UiDevice.findObject() otherwise waits for UiAutomator's global
        // selector timeout even when this class is deliberately polling with
        // its own deadline. On the API 35 image that default wait is several
        // seconds, so a pair of missing selectors can turn a 30-second
        // product timeout into a multi-minute test. Keep discovery
        // non-blocking and let the helpers below own the timing.
        Configurator.getInstance().setWaitForSelectorTimeout(0)
        check(
            screenshotDirectory.deleteRecursively()
                || !screenshotDirectory.exists(),
        ) {
            "ANDROID_UI_SCREENSHOT_DIRECTORY_CLEANUP_FAILED"
        }
        check(screenshotDirectory.mkdirs() || screenshotDirectory.isDirectory()) {
            "ANDROID_UI_SCREENSHOT_DIRECTORY_FAILED"
        }
        check(screenshotDirectory.listFiles()?.isEmpty() == true) {
            "ANDROID_UI_SCREENSHOT_DIRECTORY_NOT_EMPTY"
        }
    }

    @Test
    fun releaseUiTypesAndShowsConnectFailureThenReopens() {
        device.pressHome()
        launch()
        device.wait(androidx.test.uiautomator.Until.hasObject(By.pkg(packageName)), 10_000)

        waitForOneOf(arrayOf("Disconnected", "Ready"), 30_000)
        requireObject(connectionActionLabel)
        captureScreenshot("startup")

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
        captureScreenshot("failure-state")

        // Exercise the user-visible mobile lifecycle. Fyne's Go runtime owns
        // one NativeActivity window per process, so finishing that Activity
        // from inside the still-running instrumentation process cannot create
        // a second Go window. Process death/restart is covered by the hosted
        // functional scenario; this renderer test backgrounds and reopens it.
        backgroundActivity()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Ready", "Error", "Failed"), 30_000)
        requireObject(connectionActionLabel)
        captureScreenshot("reopened")
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

    /** Capture a redacted rendered frame as an extra, integrity-checked artifact. */
    private fun captureScreenshot(label: String) {
        check(label.matches(Regex("[A-Za-z0-9_-]+"))) {
            "ANDROID_UI_SCREENSHOT_LABEL_INVALID"
        }
        var bitmap: Bitmap? = null
        var output: File? = null
        try {
            ensureNativeInputDismissedForScreenshot()
            bitmap = instrumentation.uiAutomation.takeScreenshot()
                ?: throw IllegalStateException("ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY")
            check(bitmap.width > 0 && bitmap.height > 0) {
                "ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY"
            }
            val masks = listOf(
                "Connection configuration",
                "Connection logs",
                "Connection details",
            ).map { requiredLabel ->
                val bounds = waitForStableBounds(requiredLabel, 3_000)
                val clipped = Rect(0, 0, bitmap.width, bitmap.height)
                check(clipped.intersect(bounds)) {
                    "ANDROID_UI_SCREENSHOT_MASK_OUTSIDE_FRAME:$requiredLabel"
                }
                check(!clipped.isEmpty) {
                    "ANDROID_UI_SCREENSHOT_MASK_EMPTY:$requiredLabel"
                }
                clipped
            }
            val canvas = Canvas(bitmap)
            val paint = Paint().apply {
                color = Color.BLACK
                style = Paint.Style.FILL
            }
            for (mask in masks) {
                canvas.drawRect(mask, paint)
            }
            output = File(screenshotDirectory, "$label.png")
            check(!output.exists()) {
                "ANDROID_UI_SCREENSHOT_DUPLICATE_LABEL:$label"
            }
            FileOutputStream(output).use { stream ->
                check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    "ANDROID_UI_SCREENSHOT_PNG_ENCODE_FAILED"
                }
            }
            check(output.isFile && output.length() > 8L) {
                "ANDROID_UI_SCREENSHOT_PNG_INVALID"
            }
            val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(output.absolutePath, options)
            check(options.outWidth == bitmap.width && options.outHeight == bitmap.height) {
                "ANDROID_UI_SCREENSHOT_DIMENSIONS_INVALID"
            }
            val bytes = output.length()
            val hash = sha256(output)
            println(
                "DOBBY_UI_SCREENSHOT label=$label path=${output.absolutePath} " +
                    "bytes=$bytes sha256=$hash width=${options.outWidth} height=${options.outHeight}",
            )
        } catch (error: Throwable) {
            if (output != null && output.exists() && !output.delete()) {
                error.addSuppressed(
                    IllegalStateException("ANDROID_UI_SCREENSHOT_CLEANUP_FAILED:$label"),
                )
            }
            throw AssertionError(
                "ANDROID_UI_SCREENSHOT_CAPTURE_FAILED label=$label: " +
                    (error.message ?: error::class.java.simpleName),
                error,
            )
        } finally {
            bitmap?.recycle()
        }
    }

    /** Do not let a transient native editor or IME put the profile in a frame. */
    private fun ensureNativeInputDismissedForScreenshot() {
        if (waitForNativeInputGone(100)) return
        device.pressBack()
        if (waitForNativeInputGone(1_000)) return
        // Some IMEs consume the first Back. The second reaches Fyne's
        // keyboard bridge; only a proved-absent editor permits capture.
        device.pressBack()
        check(waitForNativeInputGone(3_000)) {
            "ANDROID_UI_SCREENSHOT_UNAVAILABLE_EDITOR_VISIBLE"
        }
    }

    private fun sha256(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(8192)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                if (count > 0) digest.update(buffer, 0, count)
            }
        }
        return digest.digest().joinToString("") { byte ->
            (byte.toInt() and 0xff).toString(16).padStart(2, '0')
        }
    }

}
