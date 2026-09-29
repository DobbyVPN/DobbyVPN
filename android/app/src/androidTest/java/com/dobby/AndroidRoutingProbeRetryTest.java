package com.dobby;

import static org.junit.Assert.assertEquals;
import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertTrue;

import org.json.JSONObject;
import org.junit.Test;

import java.util.ArrayList;
import java.util.List;

public final class AndroidRoutingProbeRetryTest {
    @Test
    public void retriesNetworkFailureAndRetainsCompleteAttemptOnSuccess() throws Exception {
        long[] now = {100L};
        int[] calls = {0};
        List<Integer> timeouts = new ArrayList<>();

        JSONObject result = AndroidRoutingProbeRetry.run(
                20_000L,
                timeoutMillis -> {
                    timeouts.add(timeoutMillis);
                    if (calls[0]++ == 0) {
                        return new JSONObject()
                                .put("error_code", AndroidRoutingProbeRetry.NETWORK_REQUEST_FAILED)
                                .put("error_detail", "complete first attempt stack trace");
                    }
                    return new JSONObject().put("status", 200).put("body", "203.0.113.8");
                },
                () -> now[0],
                millis -> now[0] += millis);

        assertEquals(2, calls[0]);
        assertEquals(2, timeouts.size());
        assertTrue(timeouts.get(0) > 0 && timeouts.get(0) <= 8_000);
        assertEquals("203.0.113.8", result.getString("body"));
        assertEquals(
                "complete first attempt stack trace",
                result.getJSONArray("attempt_errors").getJSONObject(0).getString("error_detail"));
    }

    @Test
    public void doesNotRetryNonNetworkFailure() throws Exception {
        int[] calls = {0};
        JSONObject result = AndroidRoutingProbeRetry.run(
                20_000L,
                timeoutMillis -> {
                    calls[0]++;
                    return new JSONObject().put(
                            "error_code", "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN");
                },
                () -> 100L,
                millis -> { throw new AssertionError("non-network failure must not retry"); });

        assertEquals(1, calls[0]);
        assertEquals("ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN", result.getString("error_code"));
        assertFalse(result.has("attempt_errors"));
    }

    @Test
    public void stopsAtDeadlineAndPreservesEveryFailedAttempt() throws Exception {
        long[] now = {100L};
        int[] calls = {0};
        JSONObject result = AndroidRoutingProbeRetry.run(
                500L,
                timeoutMillis -> {
                    calls[0]++;
                    return new JSONObject()
                            .put("error_code", AndroidRoutingProbeRetry.NETWORK_REQUEST_FAILED)
                            .put("error_detail", "attempt " + calls[0]);
                },
                () -> now[0],
                millis -> now[0] += millis);

        assertEquals(1, calls[0]);
        assertEquals(AndroidRoutingProbeRetry.NETWORK_REQUEST_FAILED,
                result.getString("error_code"));
        assertTrue(result.getString("error_detail").contains("attempt 1"));
        assertEquals(1, result.getJSONArray("attempt_errors").length());
    }
}
