package com.dobby;

import android.os.SystemClock;

import org.json.JSONArray;
import org.json.JSONObject;

/** Bounded retry for transient failures in the positive blocked-window route probe. */
final class AndroidRoutingProbeRetry {
    static final String NETWORK_REQUEST_FAILED = "ANDROID_NETWORK_REQUEST_FAILED";
    private static final int MAX_ATTEMPTS = 3;
    private static final int MAX_REQUEST_TIMEOUT_MILLIS = 8_000;
    private static final long RETRY_DELAY_MILLIS = 250L;
    private static final long MIN_RETRY_BUDGET_MILLIS = 1_500L;

    interface Probe {
        JSONObject run(int timeoutMillis) throws Exception;
    }

    interface Clock {
        long elapsedRealtime();
    }

    interface Sleeper {
        void sleep(long millis) throws InterruptedException;
    }

    private AndroidRoutingProbeRetry() { }

    static JSONObject run(long deadlineElapsedRealtime, Probe probe) throws Exception {
        return run(
                deadlineElapsedRealtime,
                probe,
                SystemClock::elapsedRealtime,
                Thread::sleep);
    }

    static JSONObject run(
            long deadlineElapsedRealtime, Probe probe, Clock clock, Sleeper sleeper)
            throws Exception {
        JSONArray failedAttempts = new JSONArray();
        JSONObject lastResult = null;
        int attempts = 0;

        while (attempts < MAX_ATTEMPTS) {
            long remainingMillis = deadlineElapsedRealtime - clock.elapsedRealtime();
            if (remainingMillis <= 0) break;

            // Connect, response headers, and body reads each use this bound.
            // Reserve one third of the remaining scenario budget for each.
            int timeoutMillis = (int) Math.min(
                    MAX_REQUEST_TIMEOUT_MILLIS,
                    Math.max(1L, remainingMillis / 3L));
            lastResult = probe.run(timeoutMillis);
            attempts++;

            if (!NETWORK_REQUEST_FAILED.equals(lastResult.optString("error_code", ""))) {
                if (failedAttempts.length() > 0) {
                    if (lastResult.has("error_code")) {
                        failedAttempts.put(new JSONObject(lastResult.toString()));
                        lastResult.put("error_detail", failedAttempts.toString());
                    }
                    lastResult.put("attempt_errors", failedAttempts);
                }
                return lastResult;
            }

            failedAttempts.put(new JSONObject(lastResult.toString()));
            remainingMillis = deadlineElapsedRealtime - clock.elapsedRealtime();
            if (attempts >= MAX_ATTEMPTS
                    || remainingMillis <= Math.max(
                    RETRY_DELAY_MILLIS, MIN_RETRY_BUDGET_MILLIS)) break;
            sleeper.sleep(Math.min(RETRY_DELAY_MILLIS, remainingMillis));
        }

        if (lastResult == null) {
            lastResult = new JSONObject().put(
                    "error_code", "ANDROID_NETWORK_PROBE_DEADLINE_EXPIRED");
        }
        if (failedAttempts.length() > 0) {
            lastResult.put("attempt_errors", failedAttempts);
            // Keep every complete failure in the diagnostic string consumed
            // by the hosted adapter, including a failure from the last try.
            lastResult.put("error_detail", failedAttempts.toString());
        }
        return lastResult;
    }
}
