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

    /**
     * Initializes complete Go diagnostics before attaching platform callbacks.
     * All UI and instrumentation entry points use this method before issuing
     * session commands.
     */
    public static void attach(Context context) {
        String logPath = NativeVpnBridge.goDiagnosticPath(context);
        String failure = initializeLogger(logPath);
        if (failure == null || !failure.isEmpty()) {
            IllegalStateException error = new IllegalStateException(
                "Go backend diagnostics could not be initialized"
                    + (failure == null ? "" : ": " + failure)
            );
            NativeVpnBridge.recordNativeFailure(context, "go.logger_init_failed", error);
            throw error;
        }
        attachNative(context);
    }

    private static native String initializeLogger(String path);

    private static native void attachNative(Context context);

    public static native String configure(String sessionId, long expectedSequence, byte[] rawConfig);

    public static native String start(String sessionId, long expectedSequence, String mode, int index);

    public static native String stop(String sessionId, long generation);

    public static native String snapshot(String sessionId);

    public static native String reset(String sessionId, long expectedSequence);
}
