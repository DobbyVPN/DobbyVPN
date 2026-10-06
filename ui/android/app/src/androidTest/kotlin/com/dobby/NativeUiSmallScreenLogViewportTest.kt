package com.dobby

import android.app.Instrumentation
import android.content.Intent
import android.graphics.Bitmap
import android.os.Bundle
import android.graphics.Rect
import android.view.View
import android.view.ViewGroup
import android.widget.ScrollView
import android.widget.TextView
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.Configurator
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import com.dobby.nativebridge.NativeVpnBridge
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

/** Focused rendered check for the connection log viewport at a compact display size. */
@RunWith(AndroidJUnit4::class)
class NativeUiSmallScreenLogViewportTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val device = UiDevice.getInstance(instrumentation)
    private val packageName = instrumentation.targetContext.packageName
    private val screenshotDirectory = File(
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
                if (finalFailure != null) finalFailure.addSuppressed(failure)
                else finalFailure = failure
            }
            if (finalFailure != null) {
                CompleteThrowableReporter.report(instrumentation, finalFailure)
            }
        }
    }

    @Before
    fun prepareScreenshotDirectory() {
        Configurator.getInstance().setWaitForSelectorTimeout(0)
        check(screenshotDirectory.deleteRecursively() || !screenshotDirectory.exists()) {
            "ANDROID_UI_SCREENSHOT_DIRECTORY_CLEANUP_FAILED"
        }
        check(screenshotDirectory.mkdirs() || screenshotDirectory.isDirectory) {
            "ANDROID_UI_SCREENSHOT_DIRECTORY_FAILED"
        }
    }

    @Test
    fun smallScreenLogViewportIsUsable() {
        device.unfreezeRotation()
        device.setOrientationNatural()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Error"), 30_000)
        requireObject("Connection logs")

        val originalDisplaySize = device.executeShellCommand("wm size")
        val originalOverride = Regex("Override size: (\\d+x\\d+)")
            .find(originalDisplaySize)?.groupValues?.get(1)
        try {
            val before = MainActivity.current
                ?: throw AssertionError("ANDROID_ACTIVITY_MISSING_BEFORE_SMALL_SCREEN_RESIZE")
            device.executeShellCommand("wm size 360x640")
            waitForActivityReplacement(before, 10_000)
            check(MainActivity.current?.intent?.getBooleanExtra("dobbyvpn.traceComposeLayout", false) == true) {
                "ANDROID_LAYOUT_TRACE_EXTRA_LOST_AFTER_CONFIGURATION_CHANGE"
            }
            check(device.displayWidth <= 360 && device.displayHeight <= 640) {
                "ANDROID_SMALL_SCREEN_OVERRIDE_NOT_APPLIED ${device.displayWidth}x${device.displayHeight}"
            }
            val layoutTrace = device.executeShellCommand("logcat -d -s DobbyComposeLayout:I")
            check(layoutTrace.contains("logs-android-view incoming=")) {
                "ANDROID_LAYOUT_TRACE_MEASUREMENTS_MISSING trace=$layoutTrace"
            }
            val controlsMeasurement = layoutTrace.lineSequence().lastOrNull {
                it.contains("connection-controls incoming=")
            }
            check(controlsMeasurement?.contains("measured=") == true) {
                "ANDROID_CONNECTION_CONTROLS_MEASUREMENT_MISSING trace=$layoutTrace"
            }
            check(
                layoutTrace.contains("logs-pane incoming=") &&
                    layoutTrace.contains("logs-content-column incoming="),
            ) {
                "ANDROID_LOGS_PANE_MEASUREMENTS_MISSING trace=$layoutTrace"
            }
            val visibleMessage = seedDiagnosticRows()
            assertLogPaneUsable(visibleMessage)
            assertPrimaryConnectionActionReachable()
            captureScreenshot("small-screen-log-viewport")
            requireObject("Clear").click()
            waitForLogMessageAbsent(visibleMessage)
        } finally {
            device.executeShellCommand(
                if (originalOverride == null) "wm size reset" else "wm size $originalOverride",
            )
            device.unfreezeRotation()
            device.setOrientationNatural()
            device.waitForIdle()
        }
    }

    private fun launch() {
        val intent = Intent(instrumentation.targetContext, MainActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            .putExtra("dobbyvpn.traceComposeLayout", true)
        val launched = instrumentation.startActivitySync(intent)
        check(launched.intent.getBooleanExtra("dobbyvpn.traceComposeLayout", false)) {
            "ANDROID_LAYOUT_TRACE_EXTRA_NOT_DELIVERED_TO_ACTIVITY"
        }
        val deadline = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < deadline) {
            if (device.currentPackageName == packageName && MainActivity.current?.hasWindowFocus() == true) {
                device.waitForIdle()
                return
            }
            Thread.sleep(100)
        }
        throw AssertionError(
            "ANDROID_LAUNCH_ACTIVITY_FOREGROUND_TIMEOUT extra=" +
                launched.intent.getBooleanExtra("dobbyvpn.traceComposeLayout", false),
        )
    }

    private fun waitForOneOf(labels: Array<String>, timeoutMillis: Long) {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            if (labels.any { waitForObject(it, 100) != null }) return
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_UI_STATE_TIMEOUT")
    }

    private fun requireObject(label: String): UiObject2 =
        waitForObject(label, 10_000) ?: throw AssertionError("ANDROID_UI_CONTROL_TIMEOUT:$label")

    private fun waitForObject(label: String, timeoutMillis: Long): UiObject2? {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            for (selector in arrayOf(By.text(label).pkg(packageName), By.desc(label).pkg(packageName))) {
                device.findObject(selector)?.takeIf { !it.visibleBounds.isEmpty }?.let { return it }
            }
            Thread.sleep(50)
        }
        return null
    }

    private fun waitForActivityReplacement(previous: MainActivity, timeoutMillis: Long) {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            val current = MainActivity.current
            if (current != null && current !== previous) {
                device.waitForIdle()
                return
            }
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_CONFIGURATION_RELAUNCH_TIMEOUT")
    }

    private fun seedDiagnosticRows(): String {
        val clearMarker = "small-screen-log-viewport-clear-${System.nanoTime()}"
        NativeVpnBridge.recordDiagnostic(
            instrumentation.targetContext,
            "ui.test.small_screen.clear",
            clearMarker,
        )
        waitForLogMessage(clearMarker)
        requireObject("Clear").click()
        waitForLogMessageAbsent(clearMarker)

        val prefix = "small-screen-log-viewport-row"
        val messages = (0 until 8).map { index -> "$prefix-${index.toString().padStart(2, '0')}" }
        messages.forEach { message ->
            NativeVpnBridge.recordDiagnostic(
                instrumentation.targetContext,
                "ui.test.small_screen.row",
                message,
            )
        }
        waitForLogMessage(messages.last())
        return messages.last()
    }

    private fun waitForLogMessage(message: String) {
        val deadline = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < deadline) {
            if (waitForObject("Connection logs", 100)?.text.orEmpty().contains(message)) return
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_SMALL_SCREEN_LOG_MESSAGE_NOT_RENDERED message=$message")
    }

    private fun waitForLogMessageAbsent(message: String) {
        val deadline = System.currentTimeMillis() + 10_000
        while (System.currentTimeMillis() < deadline) {
            if (!waitForObject("Connection logs", 100)?.text.orEmpty().contains(message)) return
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_SMALL_SCREEN_LOG_MESSAGE_NOT_CLEARED message=$message")
    }

    private fun assertLogPaneUsable(expectedVisibleMessage: String) {
        var lastLayout = "log view not found"
        val deadline = System.nanoTime() + 5_000_000_000L
        while (System.nanoTime() < deadline) {
            var ready = false
            instrumentation.runOnMainSync {
                var logView: TextView? = null
                fun visit(view: View) {
                    if (view.contentDescription == "Connection logs" && view is TextView) logView = view
                    if (view is ViewGroup) repeat(view.childCount) { visit(view.getChildAt(it)) }
                }
                MainActivity.current?.window?.decorView?.let(::visit)
                val view = logView
                val viewport = view?.parent as? ScrollView
                val textVisible = Rect()
                val viewportVisible = Rect()
                ready = view != null && viewport != null
                        && view.height >= 24
                        && view.getGlobalVisibleRect(textVisible) && textVisible.height() >= 24
                        && viewport.height >= 24
                        && viewport.getGlobalVisibleRect(viewportVisible) && viewportVisible.height() >= 24
                var messageVisible = false
                if (ready && view != null && viewport != null
                    && textVisible.intersect(viewportVisible)
                ) {
                    val text = view.text?.toString().orEmpty()
                    val messageStart = text.indexOf(expectedVisibleMessage)
                    val layout = view.layout
                    if (messageStart >= 0 && layout != null) {
                        val firstLine = layout.getLineForOffset(messageStart)
                        val lastLine = layout.getLineForOffset(messageStart + expectedVisibleMessage.length - 1)
                        val viewportLocation = IntArray(2)
                        viewport.getLocationOnScreen(viewportLocation)
                        val visibleContentTop = viewport.scrollY + textVisible.top - viewportLocation[1]
                        val visibleContentBottom = viewport.scrollY + textVisible.bottom - viewportLocation[1]
                        val textTop = view.top + view.extendedPaddingTop - view.scrollY
                        messageVisible = (firstLine..lastLine).all { line ->
                            val lineTop = textTop + layout.getLineTop(line)
                            val lineBottom = textTop + layout.getLineBottom(line)
                            lineTop >= visibleContentTop && lineBottom <= visibleContentBottom
                        }
                    }
                }
                ready = ready && messageVisible
                lastLayout = if (view == null) "log view not found" else
                    "${describeViewChain(view)} expected=$expectedVisibleMessage " +
                        "rendered=${view.text?.toString()?.contains(expectedVisibleMessage)} " +
                        "fully_visible=$messageVisible"
            }
            if (ready) return
            Thread.sleep(100)
        }
        val layoutTrace = device.executeShellCommand("logcat -d -s DobbyComposeLayout:I")
        throw AssertionError(
            "ANDROID_LOGS_NOT_VISIBLE_ON_SMALL_SCREEN display=${device.displayWidth}x${device.displayHeight} " +
                "ancestor_chain=$lastLayout layout_trace=$layoutTrace",
        )
    }

    private fun assertPrimaryConnectionActionReachable() {
        // This case has no subscription fixture, so it verifies that the
        // primary action remains fully visible and accessible without starting a VPN.
        val action = requireObject("VPN connection action")
        val autoConnectLabel = requireObject("Auto connect")
        val title = requireObject("DobbyVPN")
        val screen = Rect(0, 0, device.displayWidth, device.displayHeight)
        val minimumActionHeight = (40 * instrumentation.targetContext.resources.displayMetrics.density).toInt()
        check(screen.contains(action.visibleBounds)
                && action.visibleBounds.height() >= minimumActionHeight
                && screen.contains(autoConnectLabel.visibleBounds)
                && action.visibleBounds.contains(autoConnectLabel.visibleBounds)) {
            "ANDROID_PRIMARY_CONNECTION_ACTION_NOT_REACHABLE_ON_SMALL_SCREEN " +
                "action=${action.visibleBounds} label=${autoConnectLabel.visibleBounds} screen=$screen " +
                "minimum_action_height=$minimumActionHeight"
        }
        val maximumTitleHeight = (40 * instrumentation.targetContext.resources.displayMetrics.density).toInt()
        check(!title.visibleBounds.isEmpty && title.visibleBounds.height() <= maximumTitleHeight) {
            "ANDROID_APP_TITLE_WRAPS_ON_SMALL_SCREEN bounds=${title.visibleBounds} " +
                "maximum_height=$maximumTitleHeight"
        }
    }

    private fun describeViewChain(view: View): String {
        val chain = mutableListOf<String>()
        var current: View? = view
        while (current != null) {
            val visible = Rect()
            val hasVisibleRect = current.getGlobalVisibleRect(visible)
            chain += "${current.javaClass.simpleName}[${current.width}x${current.height}," +
                "visible=$hasVisibleRect:$visible,shown=${current.isShown}]"
            current = current.parent as? View
        }
        return chain.joinToString(" <- ")
    }

    private fun captureScreenshot(label: String) {
        check(label.matches(Regex("[A-Za-z0-9_-]+"))) { "ANDROID_UI_SCREENSHOT_LABEL_INVALID" }
        var bitmap: Bitmap? = null
        var output: File? = null
        try {
            bitmap = instrumentation.uiAutomation.takeScreenshot()
                ?: throw IllegalStateException("ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY")
            output = File(screenshotDirectory, "$label.png")
            check(!output.exists()) { "ANDROID_UI_SCREENSHOT_DUPLICATE_LABEL:$label" }
            FileOutputStream(output).use { stream ->
                check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    "ANDROID_UI_SCREENSHOT_PNG_ENCODE_FAILED"
                }
            }
            val bytes = output.length()
            val hash = MessageDigest.getInstance("SHA-256").digest(output.readBytes())
                .joinToString("") { "%02x".format(it) }
            val marker = "DOBBY_UI_SCREENSHOT label=$label path=${output.absolutePath} " +
                "bytes=$bytes sha256=$hash width=${bitmap.width} height=${bitmap.height}\n"
            instrumentation.sendStatus(0, Bundle().apply {
                putString(Instrumentation.REPORT_KEY_STREAMRESULT, marker)
            })
        } catch (error: Throwable) {
            if (output?.exists() == true) output.delete()
            throw AssertionError("ANDROID_UI_SCREENSHOT_CAPTURE_FAILED label=$label", error)
        } finally {
            bitmap?.recycle()
        }
    }
}
