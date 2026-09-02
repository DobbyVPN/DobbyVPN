package com.dobby.feature.vpn_service

import android.app.Activity
import android.net.VpnService
import android.os.Bundle
import androidx.test.platform.app.InstrumentationRegistry

/**
 * Test-only host for Android's real VPN consent UI.
 *
 * This class is compiled into the instrumentation APK, never the production
 * APK.  The consent request still uses the target application context so that
 * Android grants the production VpnService permission rather than the test
 * package's permission.
 */
class VpnConsentTestActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val targetContext = InstrumentationRegistry.getInstrumentation().targetContext
        val consentIntent = VpnService.prepare(targetContext)
        if (consentIntent == null) {
            finish()
        } else {
            startActivityForResult(consentIntent, VPN_CONSENT_REQUEST)
        }
    }

    @Suppress("DEPRECATION")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: android.content.Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == VPN_CONSENT_REQUEST) finish()
    }

    private companion object {
        private const val VPN_CONSENT_REQUEST = 1
    }
}
