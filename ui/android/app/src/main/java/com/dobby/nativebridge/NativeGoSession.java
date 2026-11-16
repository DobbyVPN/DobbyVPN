package com.dobby.nativebridge;

import android.content.Context;

/**
 * Narrow JNI surface used by the Compose frontend and Android functional
 * tests to call the process-owned Go session manager.
 */
public final class NativeGoSession {
    private static volatile boolean loaded;
    private static final NativeStartup startup = new NativeStartup();
    private static volatile Throwable loadFailure;

    static {
        // Load the process-owned Go backend before exposing session commands.
        // Keep load failures catchable so the basic UI and ordinary
        // diagnostics can still start when the native library is unavailable.
        try {
            System.loadLibrary("dobby_vpn");
            loaded = true;
        } catch (Throwable failure) {
            loadFailure = failure;
        }
    }

    private NativeGoSession() {}

    public static boolean isAvailable() {
        return loaded && startup.isAttached();
    }

    static boolean isLoggerReady() {
        return loaded && startup.isLoggerReady();
    }

    public static Throwable loadFailure() {
        return loadFailure;
    }

    /**
     * Initializes complete Go diagnostics before attaching platform callbacks.
     * All UI and instrumentation entry points use this method before issuing
     * session commands.
     */
    public static synchronized void attach(Context context) {
        if (!loaded) {
            Throwable failure = loadFailure;
            if (failure == null) {
                failure = new UnsatisfiedLinkError("dobby_vpn could not be loaded");
            }
            NativeVpnBridge.recordNativeFailure(context, "go.library_load_failed", failure);
            throw new IllegalStateException("Go backend native library is unavailable", failure);
        }
        startup.attach(() -> {
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
        }, () -> {
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
        });
    }

    private static native String initializeLogger(String path);

    static native String appendServiceDiagnostic(String path, String record);

    private static native String attachNative(Context context, String sourcePath);

    public static native String configure(String sessionId, long expectedSequence, byte[] rawConfig);

    public static native String start(
        String sessionId,
        long expectedSequence,
        String mode,
        int index,
        byte[] rawConfig
    );

    public static native String startSelection(String sessionId, long expectedSequence, String mode, int index, String digest, boolean replaceCurrent);

    public static native String stop(String sessionId, long generation);

    static native String stopAndWait();

    static native String resume();

    static native String restore(boolean onlyPreviouslyEnabled);

    public static native String snapshot(String sessionId);

    public static native String prepareLogUpload(String sessionId, String digest);
}