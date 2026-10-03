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
        String sourcePath = NativeVpnBridge.sourceURLPath(context);
        String attachFailure = attachNative(context.getApplicationContext(), sourcePath);
        if (attachFailure == null || !attachFailure.isEmpty()) {
            IllegalStateException error = new IllegalStateException(
                "Go backend saved configuration storage could not be attached"
                    + (attachFailure == null ? "" : ": " + attachFailure)
            );
            NativeVpnBridge.recordNativeFailure(context, "go.saved_source_attach_failed", error);
            throw error;
        }
    }

    private static native String initializeLogger(String path);

    private static native String attachNative(Context context, String sourcePath);

    public static native String configure(String sessionId, long expectedSequence, byte[] rawConfig);

    public static native String start(
        String sessionId,
        long expectedSequence,
        String mode,
        int index,
        byte[] rawConfig
    );

    public static native String stop(String sessionId, long generation);

    static native String stopAndWait();

    static native String resume();

    public static native String snapshot(String sessionId);
}
