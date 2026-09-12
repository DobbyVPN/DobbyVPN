package com.dobby.feature.vpn_service

import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.SystemClock
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.uiautomator.By
import androidx.test.uiautomator.UiDevice
import androidx.test.uiautomator.Until
import com.dobby.feature.main.ui.MainActivity
import java.util.regex.Pattern

/** Test-only access to Android's real VPN consent dialog through the production activity process. */
internal object VpnConsentTestHelper {
    internal class Failure(val code: String) : Exception(code)

    @Suppress("DEPRECATION")
    fun grant(context: Context, timeoutMillis: Long, pollIntervalMillis: Long) {
        if (VpnService.prepare(context) == null) return

        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val targetContext = instrumentation.targetContext
        val activity = instrumentation.startActivitySync(
            Intent(targetContext, MainActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        ) as? MainActivity ?: throw Failure("CONSENT_ACTIVITY_UNAVAILABLE")

        var primaryFailure: Throwable? = null
        try {
            var consentIntent: Intent? = null
            instrumentation.runOnMainSync {
                consentIntent = VpnService.prepare(targetContext)
                consentIntent?.let { activity.startActivityForResult(it, VPN_CONSENT_REQUEST) }
            }
            if (consentIntent == null) return

            val approval = UiDevice.getInstance(instrumentation).wait(
                Until.findObject(By.res(Pattern.compile(".+:id/button1"))),
                timeoutMillis,
            ) ?: throw Failure("CONSENT_UNAVAILABLE")
            approval.click()

            val deadline = SystemClock.elapsedRealtime() + timeoutMillis
            while (SystemClock.elapsedRealtime() < deadline) {
                if (VpnService.prepare(context) == null) return
                Thread.sleep(pollIntervalMillis)
            }
            throw Failure("CONSENT_REJECTED")
        } catch (failure: Throwable) {
            primaryFailure = failure
            throw failure
        } finally {
            try {
                instrumentation.runOnMainSync { activity.finish() }
            } catch (cleanupFailure: Throwable) {
                if (primaryFailure == null) {
                    throw cleanupFailure
                }
                primaryFailure.addSuppressed(cleanupFailure)
            }
        }
    }

    private const val VPN_CONSENT_REQUEST = 0xD0BB
}
