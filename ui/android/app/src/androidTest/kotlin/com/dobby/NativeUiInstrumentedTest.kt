package com.dobby

import android.app.Instrumentation
import android.app.UiModeManager
import android.content.ClipData
import android.content.ClipboardManager
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Rect
import android.graphics.drawable.AdaptiveIconDrawable
import android.os.Build
import android.os.Bundle
import android.net.Uri
import android.view.WindowInsets
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import android.text.Spanned
import android.text.style.ForegroundColorSpan
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.BySelector
import androidx.test.uiautomator.Configurator
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.UiObject2
import com.dobby.ui.MainActivity
import com.dobby.nativebridge.NativeGoSession
import com.dobby.nativebridge.NativeVpnBridge
import com.dobby.vpn.BuildConfig
import java.util.zip.GZIPInputStream
import java.io.File
import java.io.FileOutputStream
import java.security.MessageDigest
import java.time.Instant
import org.json.JSONObject
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
    private var paletteMarkers: List<String> = emptyList()
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
    fun cellularTransportIsAcceptedOnlyOutsideVpn() {
        check(NativeUiHostedProfileTest.isPhysicalNetworkCandidate(true, false, false, true)) {
            "ANDROID_CELLULAR_PHYSICAL_NETWORK_REJECTED"
        }

        check(!NativeUiHostedProfileTest.isPhysicalNetworkCandidate(false, false, false, true)) {
            "ANDROID_VPN_NETWORK_MISCLASSIFIED_AS_PHYSICAL"
        }
    }

    @Test
    fun releaseUiTypesAndShowsConnectFailureThenReopens() {
        launch()

        waitForOneOf(arrayOf("Disconnected"), 30_000)
        requireObject(connectionActionLabel)
        captureScreenshot("startup")
        captureInstalledLauncherArtwork()
        verifyAboutMetadata()
        verifyResponsiveLayout()
        assertConnectionDisabled("ANDROID_INITIAL_CONNECT_ENABLED")

        // Resolve the app-owned diagnostic paths so the controller can
        // collect the native JSONL file after instrumentation completes.
        requireObject("Share logs")
        waitForTextContaining("Android diagnostic store resolved")
        waitForOneOf(arrayOf("Disconnected"), 10_000)

        verifyClipboardHandling()

        tapStable("Subscription URL")
        val nativeInput = waitForFocusedNativeInput(10_000)
        nativeInput.setText("invalidprofile")
        device.waitForIdle()
        check(waitForImeVisibility(expectedVisible = true, timeoutMillis = 2_000)) {
            "ANDROID_UI_IME_SHOW_TIMEOUT"
        }
        assertLogPaneUsable("ANDROID_LOGS_NOT_VISIBLE_WITH_KEYBOARD")
        val typingMarker = "log-update-while-typing-${System.nanoTime()}"
        NativeVpnBridge.recordDiagnostic(instrumentation.targetContext, "ui.test.typing", typingMarker)
        waitForTextContaining(typingMarker)
        verifyStructuredLogDisplay(typingMarker)
        val logReadPrefix = "log-read-responsiveness-${System.nanoTime()}"
        repeat(384) { index ->
            val message = if (index == 383) "$logReadPrefix-last" else "$logReadPrefix-$index-${"x".repeat(128)}"
            NativeVpnBridge.recordDiagnostic(instrumentation.targetContext, "ui.test.log.read", message)
        }
        waitForTextContaining("$logReadPrefix-last")
        device.waitForIdle()
        check(device.findObject(By.clazz("android.widget.EditText").pkg(packageName))?.isFocused == true) {
            "ANDROID_LOG_READ_STOLE_INPUT_FOCUS"
        }
        check(waitForImeVisibility(expectedVisible = true, timeoutMillis = 2_000)) {
            "ANDROID_LOG_READ_DISMISSED_IME"
        }
        // Incomplete or invalid URLs must leave Connect disabled without fetching.
        dismissNativeInputAfterTextEntry()
        verifyLogThemeColors()

        verifyLogScrollingAndClear()

        // Navigate only after typing so a real control transition proves the
        // Entry focus/IME teardown completed and the entered source survives
        // an in-app screen change before Connect is exercised.
        tapAndWaitForVisible("About", "Back")
        tapStable("Back")
        waitForOneOf(arrayOf("Disconnected"), 30_000)

        // Repeat the UI-only lifecycle that previously exposed an intermittent
        // About lookup timeout. The source is deliberately invalid text, so
        // these cycles exercise editor and Activity state without starting a
        // VPN connection or requiring a live profile.
        for (iteration in 1..20) {
            val suffix = iteration.toString().padStart(2, '0')
            val expectedSource = "invalidprofile-round-trip-$suffix"
            var phase = "open-configuration"
            try {
                tapStable("Subscription URL")
                phase = "enter-source"
                waitForFocusedNativeInput(10_000).setText(expectedSource)
                device.waitForIdle()
                dismissNativeInputAfterTextEntry()
                waitForConfigurationText(expectedSource, 10_000)

                phase = "about"
                tapAndWaitForVisible("About", "Back")
                phase = "back"
                tapStable("Back")
                waitForOneOf(arrayOf("Disconnected"), 10_000)
                waitForConfigurationText(expectedSource, 10_000)

                phase = "background"
                backgroundActivity()
                phase = "reopen"
                launch()
                waitForOneOf(arrayOf("Disconnected"), 10_000)
                requireObject(connectionActionLabel)
                waitForConfigurationText(expectedSource, 10_000)
            } catch (failure: Throwable) {
                throw AssertionError(
                    "ANDROID_ABOUT_ROUND_TRIP_FAILED iteration=$iteration " +
                        "phase=$phase expected_source=$expectedSource",
                    failure,
                )
            }
        }

        verifyInvalidImportOutcome()
        captureScreenshot("failure-state")

        // Exercise the user-visible lifecycle. The VPN service and Go session
        // remain process-owned when the Activity moves to the back.
        backgroundActivity()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Error", "Failed"), 30_000)
        requireObject(connectionActionLabel)
        captureScreenshot("reopened")
        verifyLiveLogsAndExport()
        verifyStreamingDiagnostics(instrumentation.targetContext.cacheDir)
    }

    private fun verifyLiveLogsAndExport() {
        val context = instrumentation.targetContext
        val marker = "live-log-check-${System.nanoTime()}"
        NativeVpnBridge.recordDiagnostic(context, "ui.test.live", marker)
        waitForTextContaining(marker)
        backgroundActivity()
        launch()
        waitForTextContaining(marker)
        val existing = context.cacheDir.listFiles().orEmpty().map { it.name }.toSet()
        val exportMarker = "fresh-export-${System.nanoTime()}"
        NativeVpnBridge.recordDiagnostic(context, "ui.test.export", exportMarker)
        val available = NativeVpnBridge.diagnosticPaths(context).lineSequence()
            .filter(String::isNotBlank).map(::File).filter(File::exists).map { it.readText() }.toList()
        tapStable("Share logs")
        val deadline = System.currentTimeMillis() + 10_000
        var archive: File? = null
        while (archive == null && System.currentTimeMillis() < deadline) {
            archive = context.cacheDir.listFiles().orEmpty().firstOrNull {
                it.name.startsWith("DobbyVPN_logs_") && it.name.endsWith(".jsonl.gz") && it.name !in existing
            }
            if (archive == null) Thread.sleep(100)
        }
        val exported = checkNotNull(archive) { "ANDROID_LOG_EXPORT_NOT_CREATED" }
        // The chooser appears only after compression has closed the file.
        check(device.wait(androidx.test.uiautomator.Until.gone(By.pkg(packageName)), 10_000)) {
            "ANDROID_LOG_SHARE_SHEET_NOT_OPENED"
        }
        val content = GZIPInputStream(exported.inputStream()).bufferedReader().use { it.readText() }
        check(content.contains(exportMarker) && content.contains("app_version") && content.contains("platform")) {
            "ANDROID_LOG_EXPORT_NOT_FRESH"
        }
        check(available.all(content::contains)) { "ANDROID_LOG_EXPORT_INCOMPLETE" }
        device.pressBack()
        check(exported.delete()) { "ANDROID_LOG_EXPORT_CLEANUP_FAILED" }
        launch()
    }

    private fun captureInstalledLauncherArtwork() {
        val context = instrumentation.targetContext
        val application = context.packageManager.getApplicationInfo(packageName, 0)
        check(application.icon != 0) { "ANDROID_LAUNCHER_ICON_MISSING" }
        check(context.resources.getResourceEntryName(application.icon) == "ic_launcher") {
            "ANDROID_INSTALLED_LAUNCHER_ICON_RESOURCE_UNEXPECTED"
        }
        val icon = context.packageManager.getApplicationIcon(packageName)
        check(icon is AdaptiveIconDrawable) { "ANDROID_INSTALLED_LAUNCHER_ICON_NOT_ADAPTIVE" }
        val bitmap = Bitmap.createBitmap(512, 512, Bitmap.Config.ARGB_8888)
        try {
            icon.setBounds(0, 0, bitmap.width, bitmap.height)
            icon.draw(Canvas(bitmap))
            val colors = mutableSetOf<Int>()
            for (y in 0 until bitmap.height step 8) {
                for (x in 0 until bitmap.width step 8) colors.add(bitmap.getPixel(x, y))
            }
            check(colors.size > 2) { "ANDROID_INSTALLED_LAUNCHER_ARTWORK_BLANK" }
            val output = File(screenshotDirectory, "installed-launcher-artwork.png")
            FileOutputStream(output).use { stream ->
                check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    "ANDROID_LAUNCHER_ARTWORK_PNG_ENCODE_FAILED"
                }
            }
            check(output.isFile && output.length() > 8L) { "ANDROID_LAUNCHER_ARTWORK_PNG_INVALID" }
            val marker = "DOBBY_INSTALLED_LAUNCHER_ARTWORK path=${output.absolutePath} " +
                "bytes=${output.length()} sha256=${sha256(output)} width=${bitmap.width} " +
                "height=${bitmap.height} sampled_colors=${colors.size}\n"
            instrumentation.sendStatus(0, Bundle().apply {
                putString(Instrumentation.REPORT_KEY_STREAMRESULT, marker)
            })
        } finally {
            bitmap.recycle()
        }
    }

    private fun verifyAboutMetadata() {
        val context = instrumentation.targetContext
        val installedVersion = context.packageManager.getPackageInfo(packageName, 0).versionName
        check(installedVersion == BuildConfig.VERSION_NAME) { "ANDROID_ABOUT_VERSION_BUILD_MISMATCH" }
        val commit = BuildConfig.PROJECT_REPOSITORY_COMMIT
        val link = BuildConfig.PROJECT_REPOSITORY_COMMIT_LINK
        val expectedLink = "https://github.com/DobbyVPN/DobbyVPN/tree/$commit"
        check(commit.matches(Regex("[0-9a-f]{40}")) && link == expectedLink) {
            "ANDROID_ABOUT_SOURCE_METADATA_MISSING"
        }
        tapAndWaitForVisible("About", "Back")
        waitForTextContaining("Version: ${BuildConfig.VERSION_NAME}")
        waitForTextContaining("Commit: ${commit.take(8)}")
        waitForTextContaining("Source commit: $commit")
        val source = requireObject("Source code $expectedLink")
        var clickable = source
        while (clickable != null && !clickable.isClickable) clickable = clickable.parent
        check(clickable?.isEnabled == true) { "ANDROID_ABOUT_SOURCE_LINK_DISABLED" }
        captureScreenshot("about-metadata")
        tapStable("Back")
        waitForOneOf(arrayOf("Disconnected"), 10_000)
    }

    private fun verifyResponsiveLayout() {
        val originalScale = device.executeShellCommand("settings get system font_scale").trim()
        check(originalScale.toFloatOrNull() != null) { "ANDROID_FONT_SCALE_UNAVAILABLE:$originalScale" }
        val originalDisplaySize = device.executeShellCommand("wm size")
        val overrideSize = Regex("Override size: (\\d+x\\d+)").find(originalDisplaySize)?.groupValues?.get(1)
        try {
            device.setOrientationLeft()
            device.waitForIdle()
            requireObject(connectionActionLabel)
            requireObject("Connection logs")
            device.executeShellCommand("settings put system font_scale 1.5")
            instrumentation.runOnMainSync { MainActivity.current?.recreate() }
            device.waitForIdle()
            waitForOneOf(arrayOf("Disconnected", "Error"), 10_000)
            val connectionAction = requireObject(connectionActionLabel)
            val landscapeWidth = device.displayWidth
            val landscapeHeight = device.displayHeight
            check(landscapeWidth > 0 && landscapeHeight > 0 && landscapeWidth > landscapeHeight
                    && !connectionAction.visibleBounds.isEmpty) {
                "ANDROID_LANDSCAPE_LAYOUT_NOT_USABLE"
            }
            requireObject("Connection logs")
            assertLogPaneUsable("ANDROID_LOGS_NOT_VISIBLE_WITH_LARGE_TEXT")
            captureScreenshot("landscape-large-font")

            device.setOrientationNatural()
            device.waitForIdle()
            val activityBeforeSmallScreenChange = currentMainActivity()
                ?: throw AssertionError("ANDROID_ACTIVITY_MISSING_BEFORE_SMALL_SCREEN_RESIZE")
            device.executeShellCommand("wm size 360x640")
            waitForActivityReplacement(activityBeforeSmallScreenChange, 10_000)
            check(device.displayWidth <= 360 && device.displayHeight <= 640) {
                "ANDROID_SMALL_SCREEN_OVERRIDE_NOT_APPLIED ${device.displayWidth}x${device.displayHeight}"
            }
            // Resolve the controls viewport from the replacement Activity after
            // Android has applied the display-size configuration change.
            scrollControlsToConnectionAction()
            assertLogPaneUsable("ANDROID_LOGS_NOT_VISIBLE_ON_SMALL_SCREEN")
        } finally {
            device.executeShellCommand("settings put system font_scale $originalScale")
            device.executeShellCommand(if (overrideSize == null) "wm size reset" else "wm size $overrideSize")
            device.unfreezeRotation()
            device.setOrientationNatural()
            device.waitForIdle()
        }
    }

    private fun assertLogPaneUsable(message: String) {
        val view = connectionLogTextView()
        val visible = Rect()
        val viewport = view.parent as? View
        check(view.getGlobalVisibleRect(visible) && visible.height() >= 24 && view.height >= 24
                && (viewport?.height ?: 0) >= 24) {
            "$message visible=$visible text_height=${view.height} viewport_height=${viewport?.height}"
        }
    }

    private fun verifyClipboardHandling() {
        val clipboard = instrumentation.targetContext.getSystemService(ClipboardManager::class.java)
        check(Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) { "ANDROID_CLIPBOARD_CLEAR_UNAVAILABLE" }
        try {
            clipboard.clearPrimaryClip()
            check(waitForObject("Paste", 500) == null) { "ANDROID_PASTE_SHOWN_WITHOUT_CLIP" }

            clipboard.setPrimaryClip(ClipData.newPlainText("empty", ""))
            requireObject("Paste")
            tapStable("Paste")
            waitForTextContaining("Clipboard item has no text")

            clipboard.setPrimaryClip(ClipData.newRawUri("non-text", Uri.parse("content://example.invalid/item")))
            check(waitForObject("Paste", 500) == null) { "ANDROID_PASTE_SHOWN_WITHOUT_TEXT_CLIP" }

            val invalidClipboardText = "http://example.invalid/subscription"
            clipboard.setPrimaryClip(ClipData.newPlainText("invalid", invalidClipboardText))
            requireObject("Paste")
            waitForConfigurationText("", 500)
            tapStable("Paste")
            waitForTextContaining("HTTPS subscription URL with a host")
            waitForConfigurationText("", 500)
        } finally {
            clipboard.clearPrimaryClip()
        }
        val clearDeadline = System.currentTimeMillis() + 2_000
        while (System.currentTimeMillis() < clearDeadline && waitForObject("Paste", 100) != null) {
            Thread.sleep(50)
        }
        val remainingPaste = waitForObject("Paste", 100)
        check(remainingPaste == null) {
            val description = clipboard.primaryClipDescription
            val types = description?.let { clipDescription ->
                (0 until clipDescription.getMimeTypeCount()).joinToString(",") { index ->
                    clipDescription.getMimeType(index)
                }
            } ?: "<none>"
            "ANDROID_PASTE_REMAINS_WITHOUT_CLIP has_primary_clip=${clipboard.hasPrimaryClip()} " +
                "advertised_mime_types=$types"
        }
    }

    private fun verifyStructuredLogDisplay(marker: String) {
        val context = instrumentation.targetContext
        val debugMarker = "structured-debug-${System.nanoTime()}"
        val warningMarker = "structured-warning-${System.nanoTime()}"
        val captureRaw = JSONObject()
            .put("timestamp", Instant.now().toString())
            .put("level", "ERROR")
            .put("event", "stderr.capture")
            .put("source", "xray stderr")
            .put("message", "capture fixture raw message")
            .put("fixture_extra", "preserved")
            .toString()
        appendSyntheticDiagnostic(context, debugMarker, "DEBUG")
        appendSyntheticDiagnostic(context, warningMarker, "WARN")
        appendSyntheticDiagnostic(context, captureRaw)
        val errorMarker = "structured-error-${System.nanoTime()}"
        NativeVpnBridge.recordDiagnostic(
            context,
            "ui.test.rendered.error",
            errorMarker,
            IllegalStateException("synthetic rendered-log error"),
        )
        waitForTextContaining(errorMarker)
        waitForTextContaining(debugMarker)
        waitForTextContaining(warningMarker)
        waitForTextContaining("Stderr capture initialized")
        val view = connectionLogTextView()
        check(view.isTextSelectable) { "ANDROID_LOG_TEXT_NOT_SELECTABLE" }
        val content = view.text as? Spanned ?: error("ANDROID_LOG_TEXT_NOT_SPANNED")
        val rendered = content.toString()
        val infoIndex = rendered.indexOf(marker)
        val errorIndex = rendered.indexOf(errorMarker)
        check(infoIndex >= 0 && errorIndex > infoIndex) { "ANDROID_LOG_FIELD_FIDELITY_OR_ORDER_FAILED" }
        check(rendered.substring(0, infoIndex).contains("INFO · Backend · android-native")) {
            "ANDROID_LOG_INFO_LABELS_MISSING"
        }
        check(rendered.substring(0, errorIndex).contains("ERROR · Backend · android-native")) {
            "ANDROID_LOG_ERROR_LABELS_MISSING"
        }
        check(rendered.contains("INFO · Backend · xray stderr\nStderr capture initialized")) {
            "ANDROID_STDERR_CAPTURE_RENDERED_AS_ERROR_OR_LOST_SOURCE"
        }
        val debugIndex = rendered.indexOf(debugMarker)
        val warningIndex = rendered.indexOf(warningMarker)
        check(debugIndex >= 0 && warningIndex >= 0) { "ANDROID_LOG_DEBUG_OR_WARNING_MISSING" }
        val darkTheme = (context.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) ==
            Configuration.UI_MODE_NIGHT_YES
        val expectedWarning = if (darkTheme) android.graphics.Color.rgb(255, 208, 132)
            else android.graphics.Color.rgb(138, 90, 0)
        val expectedError = if (darkTheme) android.graphics.Color.rgb(242, 184, 181)
            else android.graphics.Color.rgb(179, 38, 30)
        val severity = content.getSpans(errorIndex, errorIndex + errorMarker.length, ForegroundColorSpan::class.java)
            .firstOrNull() ?: error("ANDROID_LOG_ERROR_SEVERITY_SPAN_MISSING")
        check(severity.foregroundColor == expectedError) {
            "ANDROID_LOG_ERROR_SEVERITY_COLOR_MISSING"
        }
        val details = content.getSpans(0, content.length, android.text.style.ClickableSpan::class.java)
        check(details.isNotEmpty()) { "ANDROID_LOG_RAW_DETAILS_NOT_CLICKABLE" }
        val normal = content.getSpans(infoIndex, infoIndex + marker.length, ForegroundColorSpan::class.java)
            .firstOrNull() ?: error("ANDROID_LOG_THEME_COLOR_MISSING")
        val debug = content.getSpans(debugIndex, debugIndex + debugMarker.length, ForegroundColorSpan::class.java)
            .firstOrNull() ?: error("ANDROID_LOG_DEBUG_COLOR_MISSING")
        val warning = content.getSpans(warningIndex, warningIndex + warningMarker.length, ForegroundColorSpan::class.java)
            .firstOrNull() ?: error("ANDROID_LOG_WARNING_COLOR_MISSING")
        check(normal.foregroundColor != severity.foregroundColor
                && debug.foregroundColor != normal.foregroundColor
                && warning.foregroundColor == expectedWarning) {
            "ANDROID_LOG_LEVEL_COLORS_COLLAPSED"
        }
        paletteMarkers = listOf(marker, debugMarker, warningMarker, errorMarker)
        val errorDetails = details.minByOrNull { content.getSpanStart(it).takeIf { start -> start >= errorIndex } ?: Int.MAX_VALUE }
            ?.takeIf { content.getSpanStart(it) >= errorIndex }
            ?: error("ANDROID_LOG_ERROR_DETAILS_NOT_FOUND")
        instrumentation.runOnMainSync { errorDetails.onClick(view) }
        device.waitForIdle()
        val rawError = File(context.filesDir, "diagnostics/native_logs.jsonl")
            .readLines().last { it.contains(errorMarker) }
        val record = JSONObject(rawError)
        check(record.getString("message") == errorMarker && record.getString("level") == "ERROR"
                && record.getString("source") == "android-native" && record.has("error_detail")) {
            "ANDROID_LOG_STRUCTURED_RECORD_FIELDS_MISSING"
        }
        check(connectionLogTextView().text.toString().contains(rawError)) {
            "ANDROID_LOG_ORIGINAL_RECORD_NOT_EXPANDED"
        }
    }

    private fun appendSyntheticDiagnostic(context: android.content.Context, message: String, level: String) {
        val raw = JSONObject()
            .put("timestamp", Instant.now().toString())
            .put("level", level)
            .put("source", "android-native")
            .put("message", message)
            .put("event", "ui.test.palette")
            .put("fixture_extra", "palette-detail")
            .toString()
        appendSyntheticDiagnostic(context, raw)
    }

    private fun appendSyntheticDiagnostic(context: android.content.Context, raw: String) {
        val destination = File(context.filesDir, "diagnostics/native_logs.jsonl")
        NativeVpnBridge.storeNativeDiagnostic({ destination }, raw) { _, failure ->
            throw AssertionError("ANDROID_SYNTHETIC_LOG_WRITE_FAILED", failure)
        }
    }

    private fun verifyLogThemeColors() {
        val context = instrumentation.targetContext
        val modeManager = context.getSystemService(UiModeManager::class.java)
        val originalDark = (context.resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) ==
            Configuration.UI_MODE_NIGHT_YES
        val modes = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            listOf(UiModeManager.MODE_NIGHT_NO, UiModeManager.MODE_NIGHT_YES)
        } else {
            listOf(if (originalDark) UiModeManager.MODE_NIGHT_YES else UiModeManager.MODE_NIGHT_NO)
        }
        val restoreMode = if (originalDark) UiModeManager.MODE_NIGHT_YES else UiModeManager.MODE_NIGHT_NO
        val markers = paletteMarkers
        check(markers.size == 4) { "ANDROID_LOG_THEME_MARKERS_MISSING" }
        try {
            for (mode in modes) {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) modeManager.setApplicationNightMode(mode)
                val dark = mode == UiModeManager.MODE_NIGHT_YES
                val expected = listOf(
                    if (dark) android.graphics.Color.rgb(230, 225, 229) else android.graphics.Color.rgb(28, 27, 31),
                    if (dark) android.graphics.Color.rgb(202, 196, 208) else android.graphics.Color.rgb(73, 69, 79),
                    if (dark) android.graphics.Color.rgb(255, 208, 132) else android.graphics.Color.rgb(138, 90, 0),
                    if (dark) android.graphics.Color.rgb(242, 184, 181) else android.graphics.Color.rgb(179, 38, 30),
                )
                markers.zip(expected).forEach { (marker, color) -> waitForRenderedLogColor(marker, color) }
                val surface = if (dark) android.graphics.Color.rgb(20, 18, 24)
                    else android.graphics.Color.rgb(255, 251, 254)
                expected.forEach { color -> check(contrastRatio(color, surface) >= 4.5) {
                    "ANDROID_LOG_THEME_COLOR_NOT_READABLE dark=$dark color=$color"
                } }
            }
        } finally {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                modeManager.setApplicationNightMode(restoreMode)
            }
        }
    }

    private fun waitForRenderedLogColor(marker: String, expected: Int) {
        val deadline = System.currentTimeMillis() + 5_000
        while (System.currentTimeMillis() < deadline) {
            val content = runCatching { connectionLogTextView().text as? Spanned }.getOrNull()
            val start = content?.toString()?.indexOf(marker) ?: -1
            if (start >= 0) {
                val actual = content?.getSpans(start, start + marker.length, ForegroundColorSpan::class.java)
                    ?.firstOrNull()?.foregroundColor
                if (actual == expected) return
            }
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_LOG_THEME_COLOR_TIMEOUT marker=$marker expected=$expected")
    }

    private fun contrastRatio(foreground: Int, background: Int): Double {
        fun luminance(color: Int): Double {
            fun channel(value: Int): Double {
                val normalized = value / 255.0
                return if (normalized <= 0.04045) normalized / 12.92
                    else Math.pow((normalized + 0.055) / 1.055, 2.4)
            }
            return 0.2126 * channel(android.graphics.Color.red(color))
                + 0.7152 * channel(android.graphics.Color.green(color))
                + 0.0722 * channel(android.graphics.Color.blue(color))
        }
        val first = luminance(foreground)
        val second = luminance(background)
        return (maxOf(first, second) + 0.05) / (minOf(first, second) + 0.05)
    }

    private fun connectionLogTextView(): TextView {
        val found = arrayOfNulls<TextView>(1)
        instrumentation.runOnMainSync {
            fun visit(view: View) {
                if (view.contentDescription == "Connection logs" && view is TextView) found[0] = view
                if (view is ViewGroup) repeat(view.childCount) { visit(view.getChildAt(it)) }
            }
            MainActivity.current?.window?.decorView?.let(::visit)
        }
        return checkNotNull(found[0]) { "ANDROID_LOG_VIEW_MISSING" }
    }

    private fun verifyLogScrollingAndClear() {
        val context = instrumentation.targetContext
        val prefix = "scroll-check-${System.nanoTime()}"
        repeat(80) { NativeVpnBridge.recordDiagnostic(context, "ui.test.scroll", "$prefix-$it") }
        waitForTextContaining("$prefix-79")
        val logs = requireObject("Connection logs")
        val viewport = logs.parent
        check(viewport.scroll(androidx.test.uiautomator.Direction.UP, 1f)) { "ANDROID_LOG_SCROLL_UP_FAILED" }
        device.waitForIdle()
        val frozen = requireObject("Connection logs").text
        val frozenScrollY = logScrollY()
        check(frozenScrollY > 0) { "ANDROID_LOG_FREEZE_POSITION_NOT_CAPTURED " + logGeometry() }
        val pending = "$prefix-pending"
        NativeVpnBridge.recordDiagnostic(context, "ui.test.scroll.pending", pending)
        // Two foreground refresh intervals must not change a frozen reader.
        Thread.sleep(1_600)
        check(requireObject("Connection logs").text == frozen
                && kotlin.math.abs(logScrollY() - frozenScrollY) <= 2) {
            "ANDROID_LOG_SCROLL_POSITION_NOT_FROZEN " + logGeometry()
        }

        instrumentation.runOnMainSync { MainActivity.current?.recreate() }
        waitForOneOf(arrayOf("Disconnected", "Error"), 10_000)
        waitForTextContaining("$prefix-79")
        val restored = requireObject("Connection logs").text.orEmpty()
        check(restored == frozen && !restored.contains(pending) && logScrollY() > 0
                && logScrollY() <= frozenScrollY + 32) {
            "ANDROID_LOG_FROZEN_VIEW_DID_NOT_SURVIVE_ACTIVITY_RECREATION " + logGeometry()
        }
        val deadline = System.currentTimeMillis() + 10_000
        while (!requireObject("Connection logs").text.orEmpty().contains(pending) && System.currentTimeMillis() < deadline) {
            requireObject("Connection logs").parent.scroll(androidx.test.uiautomator.Direction.DOWN, 1f)
            device.waitForIdle()
        }
        check(requireObject("Connection logs").text.orEmpty().contains(pending)) {
            "ANDROID_LOG_FOLLOW_NOT_RESUMED " + logGeometry()
        }
        check(!logCanScrollDown()) { "ANDROID_LOG_FOLLOW_DID_NOT_REACH_BOTTOM " + logGeometry() }
        check(requireObject("Connection logs").parent.scroll(androidx.test.uiautomator.Direction.UP, 1f)) {
            "ANDROID_LOG_CLEAR_FREEZE_FAILED"
        }
        device.waitForIdle()
        tapStable("Clear")
        val clearDeadline = System.currentTimeMillis() + 10_000
        while (requireObject("Connection logs").text.orEmpty().contains(prefix) && System.currentTimeMillis() < clearDeadline) Thread.sleep(100)
        check(!requireObject("Connection logs").text.orEmpty().contains(prefix)) { "ANDROID_CLEAR_RESTORED_HISTORY" }
        val afterClear = "$prefix-after-clear"
        NativeVpnBridge.recordDiagnostic(context, "ui.test.after.clear", afterClear)
        waitForTextContaining(afterClear)
        val followDeadline = System.currentTimeMillis() + 5_000
        while (logCanScrollDown() && System.currentTimeMillis() < followDeadline) Thread.sleep(100)
        check(!logCanScrollDown()) { "ANDROID_CLEAR_DID_NOT_RESTORE_LOG_FOLLOW " + logGeometry() }

        finishCurrentActivity()
        launch()
        waitForOneOf(arrayOf("Disconnected", "Error"), 10_000)
        val reopened = requireObject("Connection logs").text.orEmpty()
        check(!reopened.contains("$prefix-0") && reopened.contains(afterClear)) {
            "ANDROID_CLEAR_BOUNDARY_DID_NOT_SURVIVE_ACTIVITY_REOPEN"
        }
    }

    private fun logScrollY(): Int {
        var result = -1
        instrumentation.runOnMainSync {
            fun inspect(view: View) {
                if (view.contentDescription == "Connection logs") {
                    result = (view.parent as? View)?.scrollY ?: -1
                }
                if (view is ViewGroup) repeat(view.childCount) { inspect(view.getChildAt(it)) }
            }
            MainActivity.current?.window?.decorView?.let(::inspect)
        }
        return result
    }

    private fun logCanScrollDown(): Boolean {
        var result = false
        instrumentation.runOnMainSync {
            fun inspect(view: View) {
                if (view.contentDescription == "Connection logs") {
                    result = (view.parent as? View)?.canScrollVertically(1) == true
                }
                if (view is ViewGroup) repeat(view.childCount) { inspect(view.getChildAt(it)) }
            }
            MainActivity.current?.window?.decorView?.let(::inspect)
        }
        return result
    }

    private fun logGeometry(): String {
        var result = "Log view unavailable"
        instrumentation.runOnMainSync {
            fun inspect(view: android.view.View) {
                if (view.contentDescription == "Connection logs") {
                    val parent = view.parent as android.view.View
                    result = "textHeight=${view.height} viewportHeight=${parent.height} " +
                        "scrollY=${parent.scrollY} canScrollDown=${parent.canScrollVertically(1)} " +
                        "text=${(view as android.widget.TextView).text}"
                }
                if (view is android.view.ViewGroup) repeat(view.childCount) { inspect(view.getChildAt(it)) }
            }
            MainActivity.current?.window?.decorView?.let(::inspect)
        }
        return result
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

    private fun waitForConfigurationText(expected: String, timeoutMillis: Long) {
        val selector = By.clazz("android.widget.EditText").pkg(packageName)
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            if (device.findObject(selector)?.text == expected) return
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_UI_SOURCE_RETENTION_TIMEOUT expected=$expected")
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

    private fun assertConnectionDisabled(message: String) {
        // Compose's unmerged tree puts the label and Button role below the
        // clickable node; only that action owner carries the enabled state.
        var button: UiObject2? = requireObject(connectionActionLabel)
        while (button != null && !button.isClickable) button = button.parent
        if (button == null || button.isEnabled) {
            val hierarchy = java.io.ByteArrayOutputStream()
            device.dumpWindowHierarchy(hierarchy)
            throw AssertionError("$message button_found=${button != null}\n" + hierarchy.toString("UTF-8"))
        }
    }

    private fun verifyInvalidImportOutcome() {
        assertConnectionDisabled("ANDROID_INVALID_URL_ENABLED_CONNECT")
        val sourceSelector = By.clazz("android.widget.EditText").pkg(packageName)
        val beforeBareLink = JSONObject(NativeGoSession.snapshot("")).getJSONObject("result")
        val persistedSource = beforeBareLink.optString("source_url")
        launchImport("dobbyvpn://", coldStart = true)
        waitForOneOf(arrayOf("Disconnected"), 10_000)
        assertDeliveredImport("dobbyvpn://")
        waitForConfigurationText(persistedSource, 10_000)
        check(device.findObject(By.text("Error").pkg(packageName)) == null) {
            "ANDROID_BARE_LINK_SHOWED_ERROR"
        }
        check(device.findObject(sourceSelector)?.text == persistedSource) {
            "ANDROID_BARE_LINK_CHANGED_SUBSCRIPTION_SOURCE"
        }
        val afterColdBareLink = JSONObject(NativeGoSession.snapshot("")).getJSONObject("result")
        assertBareLinkPreserved(beforeBareLink, afterColdBareLink, "cold")
        check(!connectionLogTextView().text.toString().contains("Expected authority at index 11")) {
            "ANDROID_BARE_LINK_REPORTED_URI_PARSE_ERROR"
        }
        launchImport("dobbyvpn://", coldStart = false)
        waitForOneOf(arrayOf("Disconnected"), 10_000)
        assertDeliveredImport("dobbyvpn://")
        check(device.findObject(By.text("Error").pkg(packageName)) == null) {
            "ANDROID_WARM_BARE_LINK_SHOWED_ERROR"
        }
        check(device.findObject(sourceSelector)?.text == persistedSource) {
            "ANDROID_WARM_BARE_LINK_CHANGED_SUBSCRIPTION_SOURCE"
        }
        val afterWarmBareLink = JSONObject(NativeGoSession.snapshot("")).getJSONObject("result")
        assertBareLinkPreserved(afterColdBareLink, afterWarmBareLink, "warm")
        val invalid = listOf(
            "dobbyvpn://import?url=http%3A%2F%2Fexample.invalid%2Fsubscription" to "HTTPS subscription URL with a host",
            "dobbyvpn://import" to "Use dobbyvpn://import?url=",
            "dobbyvpn://import?url=" to "Use dobbyvpn://import?url=",
            "dobbyvpn://import?url=https%3A%2F%2F" to "HTTPS subscription URL with a host",
            "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fa&url=https%3A%2F%2Fexample.invalid%2Fb" to "Use dobbyvpn://import?url=",
            "dobbyvpn://import?url=%ZZ" to "Use dobbyvpn://import?url=",
        )
        val cold = invalid.first().first
        launchImport(cold, coldStart = true)
        waitForOneOf(arrayOf("Error"), 10_000)
        waitForTextContaining("HTTPS subscription URL with a host")
        assertDeliveredImport(cold)
        assertConnectionDisabled("ANDROID_COLD_INVALID_IMPORT_ENABLED_CONNECT")

        for ((data, actionableText) in invalid.drop(1)) {
            launchImport(data, coldStart = false)
            waitForOneOf(arrayOf("Error"), 10_000)
            waitForTextContaining(actionableText)
            assertDeliveredImport(data)
            assertConnectionDisabled("ANDROID_INVALID_IMPORT_ENABLED_CONNECT")
        }
    }

    private fun launchImport(data: String, coldStart: Boolean) {
        if (coldStart) finishCurrentActivity()
        val output = device.executeShellCommand(
            "am start -W -a android.intent.action.VIEW -d '$data' $packageName",
        )
        check(output.contains("Status: ok")) { "ANDROID_IMPORT_ACTIVATION_FAILED:$output" }
    }

    private fun finishCurrentActivity() {
        instrumentation.runOnMainSync { MainActivity.current?.finishAndRemoveTask() }
        val deadline = System.currentTimeMillis() + 5_000
        while (System.currentTimeMillis() < deadline) {
            val current = booleanArrayOf(false)
            instrumentation.runOnMainSync { current[0] = MainActivity.current != null }
            if (!current[0]) return
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_ACTIVITY_DID_NOT_FINISH_FOR_COLD_IMPORT")
    }

    private fun assertDeliveredImport(expected: String) {
        val deadline = System.currentTimeMillis() + 5_000
        while (System.currentTimeMillis() < deadline) {
            var delivered = false
            instrumentation.runOnMainSync {
                delivered = MainActivity.current?.intent?.dataString == expected
            }
            if (delivered) return
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_IMPORT_INTENT_NOT_DELIVERED")
    }

    private fun assertBareLinkPreserved(before: JSONObject, after: JSONObject, delivery: String) {
        check(before.optString("session_id") == after.optString("session_id")
                && before.optLong("sequence") == after.optLong("sequence")
                && before.optLong("generation") == after.optLong("generation")
                && before.optString("state") == after.optString("state")
                && before.optString("source_url") == after.optString("source_url")
                && before.optString("digest") == after.optString("digest")) {
            "ANDROID_${delivery.uppercase()}_BARE_LINK_CHANGED_SESSION_OR_SOURCE"
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

    private fun currentMainActivity(): MainActivity? {
        var activity: MainActivity? = null
        instrumentation.runOnMainSync { activity = MainActivity.current }
        return activity
    }

    private fun waitForActivityReplacement(previous: MainActivity, timeoutMillis: Long) {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            val current = currentMainActivity()
            if (current != null && current !== previous) {
                device.waitForIdle()
                return
            }
            Thread.sleep(50)
        }
        throw AssertionError("ANDROID_CONFIGURATION_RELAUNCH_TIMEOUT")
    }

    private fun scrollControlsToConnectionAction() {
        var viewport = currentControlsScrollViewport()
        // rememberScrollState restores its offset across configuration changes.
        // A failed UP means this viewport is already at the top.
        viewport.scroll(androidx.test.uiautomator.Direction.UP, 1f)
        device.waitForIdle()
        viewport = currentControlsScrollViewport()

        var reachedStatus: UiObject2? = null
        var reachedAction: UiObject2? = null
        val scrollAttempts = mutableListOf<String>()
        for (attempt in 0..8) {
            reachedStatus = waitForOneOfOrNull(arrayOf("Disconnected", "Error"), 100)
            reachedAction = waitForObject(connectionActionLabel, 100)
            if (reachedStatus != null && reachedAction != null) break
            if (attempt == 8) break
            val scrolled = viewport.scroll(androidx.test.uiautomator.Direction.DOWN, 0.8f)
            device.waitForIdle()
            var swiped = false
            if (!scrolled) {
                // Compose can move on touch without UiAutomator observing its
                // scroll-finished accessibility event. Try the same drag a
                // user can make, then re-read the rendered nodes.
                val bounds = runCatching { viewport.visibleBounds }.getOrNull()
                if (bounds != null && bounds.height() > 24) {
                    swiped = device.swipe(
                        bounds.centerX(), bounds.bottom - 12,
                        bounds.centerX(), bounds.top + 12,
                        12,
                    )
                    device.waitForIdle()
                }
            }
            val viewportBounds = runCatching { viewport.visibleBounds.toString() }
                .getOrElse { "unavailable(${it.javaClass.simpleName})" }
            scrollAttempts += "attempt=$attempt ui_scroll=$scrolled " +
                "coordinate_swipe=$swiped viewport=$viewportBounds"
        }

        val scrollDetails = scrollAttempts.joinToString("; ")
        var button = reachedAction
        while (button != null && !button.isClickable) button = button.parent
        if (reachedStatus?.visibleBounds?.isEmpty != false) {
            failSmallScreenReachability(
                "ANDROID_SMALL_SCREEN_STATUS_NOT_REACHABLE_AFTER_SCROLL",
                viewport,
                reachedStatus,
                reachedAction,
                button,
                scrollDetails,
            )
        }
        if (button?.let { it.isClickable && !it.visibleBounds.isEmpty } != true) {
            failSmallScreenReachability(
                "ANDROID_SMALL_SCREEN_CONNECTION_ACTION_NOT_REACHABLE",
                viewport,
                reachedStatus,
                reachedAction,
                button,
                scrollDetails,
            )
        }
    }

    private fun currentControlsScrollViewport(): UiObject2 {
        val width = device.displayWidth
        val height = device.displayHeight
        var ancestor: UiObject2? = requireObject("Subscription URL")
        val scrollableViewports = mutableListOf<UiObject2>()
        while (ancestor != null) {
            val bounds = ancestor.visibleBounds
            if (ancestor.isScrollable && bounds.width() >= width * 8 / 10 && bounds.height() >= height / 5) {
                scrollableViewports += ancestor
            }
            ancestor = ancestor.parent
        }
        return scrollableViewports.maxByOrNull { it.visibleBounds.height() }
            ?: throw AssertionError("ANDROID_CONTROLS_SCROLL_VIEWPORT_MISSING ${width}x$height")
    }

    private fun failSmallScreenReachability(
        reason: String,
        viewport: UiObject2,
        status: UiObject2?,
        action: UiObject2?,
        clickableAction: UiObject2?,
        scrollDetails: String,
    ): Nothing {
        fun bounds(node: UiObject2?): String {
            if (node == null) return "missing"
            return runCatching { node.visibleBounds.toString() }
                .getOrElse { "unavailable(${it.javaClass.simpleName})" }
        }

        val screenshotFailure = runCatching {
            captureScreenshot("small-screen-scroll-failure")
        }.exceptionOrNull()
        val screenshotDiagnostic = screenshotFailure?.let {
            " screenshot_error=${it.javaClass.simpleName}:${it.message}"
        }.orEmpty()
        val statusCandidates = arrayOf("Disconnected", "Error").joinToString(";") { debugNodeBounds(it) }
        throw AssertionError(
            "$reason viewport=${bounds(viewport)} status=${bounds(status)} " +
                "action=${bounds(action)} clickable_action=${bounds(clickableAction)} " +
                "$scrollDetails status_candidates=[$statusCandidates] " +
                "action_candidates=[${debugNodeBounds(connectionActionLabel)}]$screenshotDiagnostic"
        )
    }

    private fun debugNodeBounds(label: String): String {
        fun candidates(selector: BySelector): String = runCatching {
            device.findObjects(selector).joinToString(",") { node ->
                runCatching { node.visibleBounds.toString() }
                    .getOrElse { "unavailable(${it.javaClass.simpleName})" }
            }
        }.getOrElse { "query-error(${it.javaClass.simpleName})" }

        return "$label{text=${candidates(By.text(label).pkg(packageName))}," +
            "desc=${candidates(By.desc(label).pkg(packageName))}}"
    }

    private fun waitForTextContaining(text: String, timeoutMillis: Long = 10_000) {
        val selector = By.textContains(text).pkg(packageName)
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            if (device.findObject(selector) != null) return
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_UI_TEXT_TIMEOUT:$text")
    }

    /** Capture a complete rendered frame as an extra, integrity-checked artifact. */
    private fun captureScreenshot(label: String) {
        check(label.matches(Regex("[A-Za-z0-9_-]+"))) {
            "ANDROID_UI_SCREENSHOT_LABEL_INVALID"
        }
        var sourceBitmap: Bitmap? = null
        var output: File? = null
        try {
            if (label != "failure") dismissNativeInputIfVisible()
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

    /** Finish URL input without a Back event that could close the Activity. */
    private fun dismissNativeInputIfVisible() {
        if (isImeVisible()) dismissNativeInputAfterTextEntry()
        device.waitForIdle()
    }

    /** Wait for the keyboard opened by the focused field, then close and verify it. */
    private fun dismissNativeInputAfterTextEntry() {
        check(waitForImeVisibility(expectedVisible = true, timeoutMillis = 5_000)) {
            "ANDROID_UI_IME_SHOW_TIMEOUT"
        }
        check(device.pressEnter()) { "ANDROID_UI_IME_DONE_INJECTION_FAILED" }
        check(waitForImeVisibility(expectedVisible = false, timeoutMillis = 5_000)) {
            "ANDROID_UI_IME_DISMISS_TIMEOUT"
        }
        device.waitForIdle()
        check(device.currentPackageName == packageName) {
            "ANDROID_UI_IME_DISMISS_LEFT_ACTIVITY:${device.currentPackageName}"
        }
    }

    private fun waitForImeVisibility(expectedVisible: Boolean, timeoutMillis: Long): Boolean {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (System.currentTimeMillis() < deadline) {
            if (isImeVisible() == expectedVisible) return true
            Thread.sleep(50)
        }
        return isImeVisible() == expectedVisible
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
