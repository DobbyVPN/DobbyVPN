package com.dobby

import android.app.ActivityManager
import android.content.Intent
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.UiDevice
import com.dobby.ui.MainActivity
import java.io.File
import org.json.JSONObject
import org.junit.Test
import org.junit.runner.RunWith

/** Verifies the user-local Clear boundary after the controller kills the app process. */
@RunWith(AndroidJUnit4::class)
class NativeUiClearProcessRestartTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val context = instrumentation.targetContext
    private val device = UiDevice.getInstance(instrumentation)

    @Test
    fun clearBoundarySurvivesAppProcessDeath() {
        val fixture = File(context.filesDir, "test-clear-process-restart.json")
        check(fixture.isFile) { "ANDROID_CLEAR_PROCESS_RESTART_FIXTURE_MISSING" }
        val expected = JSONObject(fixture.readText())
        val clearedRecord = expected.getString("cleared_record")
        val postClearRecord = expected.getString("post_clear_record")
        val previousProcessId = expected.getInt("original_process_id")
        check(previousProcessId > 0 && clearedRecord.isNotBlank() && postClearRecord.isNotBlank()) {
            "ANDROID_CLEAR_PROCESS_RESTART_FIXTURE_INVALID"
        }

        if (device.currentPackageName != context.packageName) {
            val intent = Intent(context, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            instrumentation.startActivitySync(intent)
        }
        device.waitForIdle()
        waitForLogContaining(postClearRecord)

        val currentProcessId = targetProcessId()
        check(currentProcessId != previousProcessId) {
            "ANDROID_CLEAR_TEST_DID_NOT_CROSS_PROCESS_RESTART old=$previousProcessId new=$currentProcessId"
        }
        val rendered = connectionLogText()
        check(rendered.contains(postClearRecord) && !rendered.contains(clearedRecord)) {
            "ANDROID_CLEAR_BOUNDARY_DID_NOT_SURVIVE_PROCESS_DEATH " +
                "post_clear_visible=${rendered.contains(postClearRecord)} " +
                "cleared_visible=${rendered.contains(clearedRecord)}"
        }
        check(fixture.delete()) { "ANDROID_CLEAR_PROCESS_RESTART_FIXTURE_CLEANUP_FAILED" }
    }

    private fun targetProcessId(): Int {
        val processes = context.getSystemService(ActivityManager::class.java).runningAppProcesses.orEmpty()
        return processes.firstOrNull { it.processName == context.packageName }?.pid
            ?: error("ANDROID_TARGET_APP_PROCESS_ID_UNAVAILABLE_AFTER_RESTART")
    }

    private fun waitForLogContaining(marker: String) {
        val deadline = System.currentTimeMillis() + 15_000
        while (System.currentTimeMillis() < deadline) {
            if (connectionLogText().contains(marker)) return
            Thread.sleep(100)
        }
        throw AssertionError("ANDROID_CLEAR_POST_RESTART_RECORD_NOT_RENDERED marker=$marker")
    }

    private fun connectionLogText(): String {
        val rendered = arrayOf("")
        instrumentation.runOnMainSync {
            fun visit(view: View) {
                if (view.contentDescription == "Connection logs" && view is TextView) {
                    rendered[0] = view.text?.toString().orEmpty()
                }
                if (view is ViewGroup) repeat(view.childCount) { visit(view.getChildAt(it)) }
            }
            MainActivity.current?.window?.decorView?.let(::visit)
        }
        return rendered[0]
    }
}
