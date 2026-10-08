package com.dobby.nativebridge;

/**
 * Test-APK-only access to the tagged Android recovery Stop seam. The release
 * source set deliberately does not declare these native controls.
 */
public final class NativeRecoveryStopTestSeam {
    private NativeRecoveryStopTestSeam() {}

    public static boolean enable() {
        // Trigger NativeGoSession's library initializer before resolving this
        // test-only class's exported JNI symbol.
        NativeGoSession.snapshot("");
        return enableNative();
    }

    public static boolean arm() {
        NativeGoSession.snapshot("");
        return armNative();
    }

    private static native boolean enableNative();

    private static native boolean armNative();
}
