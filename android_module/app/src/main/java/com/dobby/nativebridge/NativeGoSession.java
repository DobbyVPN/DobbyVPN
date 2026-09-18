package com.dobby.nativebridge;

import android.content.Context;

/**
 * Narrow JNI surface used by the Android instrumentation contract. Production
 * UI calls the same Go binding through Fyne; this class exists so the real
 * Android service tests can drive Go session policy without coordinates or a
 * second VPN implementation.
 */
public final class NativeGoSession {
    static {
        // GoNativeActivity normally loads this library. Instrumentation may
        // call the binding before the activity has reached its first frame,
        // so make the same production payload available explicitly.
        System.loadLibrary("dobby_vpn");
    }

    private NativeGoSession() {}

    public static native void attach(Context context);

    public static native String configure(String sessionId, long expectedSequence, byte[] rawConfig);

    public static native String start(String sessionId, long expectedSequence, String mode, int index);

    public static native String stop(String sessionId, long generation);

    public static native String snapshot(String sessionId);

    public static native String reset(String sessionId, long expectedSequence);
}
