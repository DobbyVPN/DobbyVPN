package com.dobby.nativebridge;

import android.content.Context;

/**
 * Narrow JNI surface used by the Compose frontend and Android functional
 * tests to call the process-owned Go session manager.
 */
public final class NativeGoSession {
    static {
        // Load the process-owned Go backend before exposing session commands.
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
