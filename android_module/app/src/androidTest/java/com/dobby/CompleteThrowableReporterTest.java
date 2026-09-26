package com.dobby;

import androidx.test.platform.app.InstrumentationRegistry;

import org.junit.Test;
import org.junit.runner.RunWith;

import androidx.test.ext.junit.runners.AndroidJUnit4;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertTrue;

import java.net.UnknownHostException;
import java.util.Base64;

/** On-device regression for complete throwable formatting and transport. */
@RunWith(AndroidJUnit4.class)
public final class CompleteThrowableReporterTest {
    @Test
    public void reportsEveryFrameCauseSuppressedUnicodeAndCycle() {
        IllegalStateException root = new IllegalStateException("root-λ");
        IllegalArgumentException cause = new IllegalArgumentException("cause-message");
        AssertionError suppressed = new AssertionError("suppressed-message");
        root.setStackTrace(new StackTraceElement[]{
                new StackTraceElement("RootType", "rootMethod", "Root.java", 11),
                new StackTraceElement("RootType", "secondMethod", "Root.java", 12),
        });
        cause.setStackTrace(new StackTraceElement[]{
                new StackTraceElement("CauseType", "causeMethod", "Cause.java", 21),
        });
        suppressed.setStackTrace(new StackTraceElement[]{
                new StackTraceElement("SuppressedType", "suppressedMethod", "Suppressed.java", 31),
        });
        root.initCause(cause);
        root.addSuppressed(suppressed);
        cause.initCause(root);

        String formatted = CompleteThrowableReporter.format(root);
        assertTrue(formatted.contains("root-λ"));
        assertTrue(formatted.contains("RootType.rootMethod(Root.java:11)"));
        assertTrue(formatted.contains("RootType.secondMethod(Root.java:12)"));
        assertTrue(formatted.contains("cause-message"));
        assertTrue(formatted.contains("CauseType.causeMethod(Cause.java:21)"));
        assertTrue(formatted.contains("suppressed-message"));
        assertTrue(formatted.contains("SuppressedType.suppressedMethod(Suppressed.java:31)"));
        assertTrue(formatted.contains("CIRCULAR_REFERENCE"));
        assertFalse(formatted.contains("... N more"));
        assertFalse(formatted.contains("trimmed"));

        CompleteThrowableReporter.report(InstrumentationRegistry.getInstrumentation(), root);
    }

    @Test
    public void networkProbeFailureKeepsStableCodeAndOriginalExceptionDetail() throws Exception {
        UnknownHostException original = new UnknownHostException("probe-host.invalid");
        original.setStackTrace(new StackTraceElement[]{
                new StackTraceElement("ProbeResolver", "lookup", "ProbeResolver.java", 47),
        });

        org.json.JSONObject result = AndroidNetworkProbeMain.failureResult(original);

        assertEquals("ANDROID_NETWORK_REQUEST_FAILED", result.getString("error_code"));
        assertTrue(result.getString("error_detail").contains("probe-host.invalid"));
        assertTrue(result.getString("error_detail")
                .contains("ProbeResolver.lookup(ProbeResolver.java:47)"));
    }

    @Test
    public void shellCommandOutputRetainsExactStdoutAndStderrBytes() throws Exception {
        byte[] stdout = new byte[]{0, 10, 13, (byte) 0xff, 65};
        byte[] stderr = new byte[]{68, 68, 32, 111, 117, 116, 10};

        org.json.JSONObject result = NativeUiHostedProfileTest.shellOutputJson(stdout, stderr);

        assertTrue(java.util.Arrays.equals(
                stdout, Base64.getDecoder().decode(result.getString("stdout_base64"))));
        assertTrue(java.util.Arrays.equals(
                stderr, Base64.getDecoder().decode(result.getString("stderr_base64"))));
    }
}
