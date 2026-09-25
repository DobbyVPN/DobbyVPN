package com.dobby

import android.app.Instrumentation
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Rect
import android.os.Build
import android.os.Bundle
import android.view.WindowInsets
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.Configurator
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import com.dobby.ui.MainActivity
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TestWatcher
import org.junit.runner.Description
import org.junit.runner.RunWith

/** Rendered Compose smoke against the signed release APK and Android's native input path. */
@RunWith(AndroidJUnit4::class)
class NativeUiInstrumentedTest {
    private val connectionActionLabel = "VPN connection action"
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)
    private val packageName = instrumentation.targetContext.packageName
    private val screenshotDirectory = File(
        // Instrumentation executes in the target application's UID. The
        // instrumentation APK's Context points at a different sandbox, which
        // is not writable from this process. Keep rendered frames in the
        // target app's cache, where the test process can create and pull them.
        instrumentation.targetContext.cacheDir,
        "dobbyvpn-rendered-screenshots",
    )

    @get:Rule
    val screenshotOnFailure: TestWatcher = object : TestWatcher() {
        override fun failed(error: Throwable?, description: Description?) {
            var finalFailure = error
            try {
                captureScreenshot("failure")
            } catch (captureError: Throwable) {
                val failure = AssertionError(
                    "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED label=failure: " +
                        (captureError.message ?: captureError::class.java.simpleName),
                    captureError,
                )
                if (finalFailure != null) {
                    finalFailure.addSuppressed(failure)
                } else {
                    finalFailure = failure
                }
            }
            if (finalFailure != null) {
                try {
                    CompleteThrowableReporter.report(instrumentation, finalFailure)
                } catch (reportError: Throwable) {
                    finalFailure.addSuppressed(
                        AssertionError("ANDROID_COMPLETE_THROWABLE_REPORT_FAILED", reportError),
                    )
                }
            }
            if (error == null && finalFailure != null) throw finalFailure
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
        launch()

        waitForOneOf(arrayOf("Disconnected"), 30_000)
        requireObject(connectionActionLabel)
        captureScreenshot("startup")

        tapStable("Connection configuration")
        val nativeInput = waitForFocusedNativeInput(10_000)
        nativeInput.setText("invalidprofile")
        device.waitForIdle()
        // The backend rejects this deliberately invalid source. The visible
        // Error state proves the Compose input reached the production binding.
        dismissNativeInputIfVisible()

        // Navigate only after typing so a real control transition proves the
        // Entry focus/IME teardown completed and the entered source survives
        // an in-app screen change before Connect is exercised.
        tapAndWaitForVisible("Settings", "Back")
        tapStable("Back")
        waitForOneOf(arrayOf("Disconnected"), 30_000)
        tapAndWaitForFailureOutcome()
        captureScreenshot("failure-state")

        // Exercise the user-visible lifecycle. The VPN service and Go session
        // remain process-owned when the Activity moves to the back.
        backgroundActivity()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Error", "Failed"), 30_000)
        requireObject(connectionActionLabel)
        captureScreenshot("reopened")
    }

    private fun launch() {
        // Instrumentation runs while Android has the test runner in front.
        // Start the real Activity through UiAutomation so Android does not
        // treat this as a blocked background Activity launch.
        val output = device.executeShellCommand(
            "am start -W -n $packageName/com.dobby.ui.MainActivity",
        )
        check(output.contains("Status: ok") && output.contains("Complete")) {
            "ANDROID_LAUNCH_ACTIVITY_FAILED"
        }
        val deadline = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < deadline) {
            if (device.currentPackageName == packageName && targetActivityHasWindowFocus()) {
                device.waitForIdle()
                return
            }
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_LAUNCH_ACTIVITY_FOREGROUND_TIMEOUT launch_output=$output")
    }

    private fun targetActivityHasWindowFocus(): Boolean {
        val focused = booleanArrayOf(false)
        instrumentation.runOnMainSync {
            val activity = MainActivity.current
            focused[0] = activity != null && !activity.isFinishing &&
                !activity.isDestroyed && activity.hasWindowFocus()
        }
        return focused[0]
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
                for (candidate in device.findObjects(selector)) {
                    if (!candidate.visibleBounds.isEmpty) return candidate
                }
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
        throw AssertionError("ANDROID_UI_CONTROL_TIMEOUT:$label")
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
        check(waitForObject("Enter an HTTPS connection URL or inline configuration", 1_000) == null) {
            "ANDROID_UI_SOURCE_WAS_EMPTY"
        }
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

    /** Capture a complete rendered frame as an extra, integrity-checked artifact. */
    private fun captureScreenshot(label: String) {
        check(label.matches(Regex("[A-Za-z0-9_-]+"))) {
            "ANDROID_UI_SCREENSHOT_LABEL_INVALID"
        }
        var sourceBitmap: Bitmap? = null
        var output: File? = null
        try {
            dismissNativeInputIfVisible()
            sourceBitmap = instrumentation.uiAutomation.takeScreenshot()
                ?: throw IllegalStateException("ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY")
            val source = sourceBitmap ?: throw IllegalStateException(
                "ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY",
            )
            check(source.width > 0 && source.height > 0) {
                "ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY"
            }
            output = File(screenshotDirectory, "$label.png")
            check(!output.exists()) {
                "ANDROID_UI_SCREENSHOT_DUPLICATE_LABEL:$label"
            }
            FileOutputStream(output).use { stream ->
                check(source.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    "ANDROID_UI_SCREENSHOT_PNG_ENCODE_FAILED"
                }
            }
            check(output.isFile && output.length() > 8L) {
                "ANDROID_UI_SCREENSHOT_PNG_INVALID"
            }
            val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(output.absolutePath, options)
            check(options.outWidth == source.width && options.outHeight == source.height) {
                "ANDROID_UI_SCREENSHOT_DIMENSIONS_INVALID"
            }
            val bytes = output.length()
            val hash = sha256(output)
            val marker =
                "DOBBY_UI_SCREENSHOT label=$label path=${output.absolutePath} " +
                    "bytes=$bytes sha256=$hash width=${options.outWidth} height=${options.outHeight}\n"
            val status = Bundle().apply {
                putString(Instrumentation.REPORT_KEY_STREAMRESULT, marker)
            }
            instrumentation.sendStatus(0, status)
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
            sourceBitmap?.recycle()
        }
    }

    /** Send Back only when the Compose keyboard is visible. */
    private fun dismissNativeInputIfVisible() {
        if (isImeVisible()) device.pressBack()
        device.waitForIdle()
    }

    private fun isImeVisible(): Boolean {
        val visible = booleanArrayOf(false)
        instrumentation.runOnMainSync {
            val decor = MainActivity.current?.window?.decorView
            if (decor != null) {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    visible[0] = decor.rootWindowInsets
                        ?.isVisible(WindowInsets.Type.ime()) == true
                } else {
                    val frame = Rect()
                    decor.getWindowVisibleDisplayFrame(frame)
                    val rootHeight = decor.rootView.height
                    visible[0] = rootHeight > 0 &&
                        rootHeight - frame.bottom > rootHeight * 0.15f
                }
            }
        }
        return visible[0]
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
