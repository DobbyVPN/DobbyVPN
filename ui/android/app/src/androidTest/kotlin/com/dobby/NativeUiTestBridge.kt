package com.dobby

import android.content.Context
import com.dobby.nativebridge.NativeVpnBridge

/** Test-only access to native diagnostics for Java instrumentation cases. */
object NativeUiTestBridge {
    @JvmStatic
    fun recordDiagnostic(context: Context, event: String, message: String) {
        NativeVpnBridge.recordDiagnostic(context, event, message)
    }
}
