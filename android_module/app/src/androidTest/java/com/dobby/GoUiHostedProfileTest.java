package com.dobby;

import android.app.Activity;
import android.app.Instrumentation;
import android.app.UiAutomation;
import android.content.Context;
import android.content.Intent;
import android.content.ContentResolver;
import android.graphics.Rect;
import android.content.pm.ApplicationInfo;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.Uri;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.os.SystemClock;
import android.view.View;
import android.view.ViewGroup;
import android.view.accessibility.AccessibilityNodeInfo;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputConnection;
import android.view.inputmethod.InputMethodManager;
import android.widget.EditText;

import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.runner.lifecycle.ActivityLifecycleMonitor;
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry;
import androidx.test.runner.lifecycle.Stage;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.Configurator;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.UiObject2;

import com.dobby.nativebridge.NativeGoSession;
import com.dobby.nativebridge.NativeVpnBridge;

import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.lang.reflect.Field;
import java.security.MessageDigest;
import java.net.HttpURLConnection;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Enumeration;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.atomic.AtomicReference;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;

/**
 * Android's real functional seam for the Go/Fyne application.
 *
 * The external Torturer runner supplies only an ordered command file and an
 * opaque profile. The protocol-matrix lane drives the native Go binding for
 * per-profile coverage; the gui-auto lane drives the production Fyne
 * controls with Android's native input/accessibility path. Both lanes keep
 * Kotlin/Java responsible only for Android permission, Network/VpnService
 * observations, and disposable test files.
 */
@RunWith(AndroidJUnit4.class)
public final class GoUiHostedProfileTest {
    private static final String COMMAND_ARGUMENT = "dobby.hosted_command_file";
    private static final String REAL_PROFILE_ARGUMENT = "dobby.real_profile";
    private static final String GUI_AUTO_MODE = "gui-auto";
    private static final String BINDING_MODE = "protocol-matrix";
    private static final String GUI_AUTO_PROTOCOL = "AUTO";
    private static final String CONNECTION_ACTION_LABEL = "VPN connection action";
    private static final String START_MODE_PROFILE_INDEX = "PROFILE_INDEX";
    private static final Class<GoUiNetworkProbeMain> NETWORK_PROBE_CLASS =
            GoUiNetworkProbeMain.class;
    private static final long POLL_MILLIS = 100L;
    private static final long ERROR_CATEGORY_TIMEOUT_MILLIS = 2_000L;
    private static final long DEFAULT_TIMEOUT_MILLIS = 60_000L;
    private static final long NETWORK_RECOVERY_TIMEOUT_MILLIS = 10_000L;
    private static final long ACTIVITY_RESUME_TIMEOUT_MILLIS = 15_000L;
    private static final long INPUT_DISMISS_INITIAL_WAIT_MILLIS = 1_000L;
    private static final long INPUT_REACQUIRE_TIMEOUT_MILLIS = 5_000L;
    private static final int PROFILE_INPUT_CHUNK_CODE_UNITS = 4_096;
    private static final int UI_STABILITY_SAMPLES = 10;
    private static final int STABILITY_SAMPLES = 5;
    private static final String FALLBACK_ERROR_CODE = "ANDROID_HOSTED_DRIVER_FAILED";
    private static final String[] FIXED_ERROR_CODES = new String[]{
            "ANDROID_COMMAND_FILE_MISSING",
            "ANDROID_GUI_AUTO_PROFILE_INDEX_FORBIDDEN",
            "ANDROID_UI_CONFIGURE_TIMEOUT",
            "ANDROID_UI_CONFIGURE_DISCONNECT_FAILED",
            "ANDROID_CONNECT_BEFORE_CONFIGURE",
            "ANDROID_TUNNEL_NOT_PRESENT",
            "ANDROID_DISCONNECT_WITHOUT_GENERATION",
            "ANDROID_RECONNECT_TUNNEL_NOT_PRESENT",
            "ANDROID_OPERATION_UNSUPPORTED",
            "ANDROID_HOSTED_DRIVER_FAILED",
            "ANDROID_COVERAGE_LANE_INVALID",
            "ANDROID_COVERAGE_LANE_MISMATCH",
            "ANDROID_NO_PROFILES",
            "ANDROID_PROFILE_INDEX_INVALID",
            "ANDROID_PROFILE_TEXT_EMPTY",
            "ANDROID_PROFILE_TEXT_INVALID",
            "ANDROID_UI_CONNECT_TIMEOUT",
            "ANDROID_STALE_VPN_NETWORK",
            "ANDROID_UI_CONNECT_FAILED",
            "ANDROID_UI_DISCONNECT_TIMEOUT",
            "ANDROID_UI_BACKGROUND_FAILED",
            "ANDROID_UI_REOPEN_STATE_INVALID",
            "ANDROID_UI_CONTROL_TIMEOUT",
            "ANDROID_UI_TAP_FAILED",
            "ANDROID_UI_SURFACE_TIMEOUT",
            "ANDROID_UI_DISCONNECT_FAILED",
            "ANDROID_UI_STATE_TIMEOUT",
            "ANDROID_UI_INPUT_FOCUS_TIMEOUT",
            "ANDROID_UI_INPUT_DISMISS_FAILED",
            "ANDROID_UI_INPUT_FOCUS_LOST",
            "ANDROID_UI_INPUT_COMMIT_FAILED",
            "ANDROID_SESSION_FAILED",
            "ANDROID_SESSION_STATE_TIMEOUT",
            "ANDROID_VPN_PERMISSION_OR_SERVICE_FAILED",
            "ANDROID_LAUNCH_ACTIVITY_MISSING",
            "ANDROID_LAUNCH_ACTIVITY_FAILED",
            "ANDROID_LAUNCH_ACTIVITY_INTERRUPTED",
            "ANDROID_LAUNCH_ACTIVITY_RESUME_TIMEOUT",
            "ANDROID_VPN_CONSENT_TIMEOUT",
            "ANDROID_NETWORK_IDENTITY_UNAVAILABLE",
            "ANDROID_NETWORK_INTERFACE_UNAVAILABLE",
            "ANDROID_ROUTING_PROOF_FAILED",
            "ANDROID_NETWORK_TRANSITION_REQUEST_INVALID",
            "ANDROID_PHYSICAL_NETWORK_NOT_VALIDATED",
            "ANDROID_IDENTITY_IPV4_UNAVAILABLE",
            "ANDROID_NETWORK_REQUEST_FAILED",
            "ANDROID_NETWORK_PROBE_SHELL_UID_INVALID",
            "ANDROID_NETWORK_PROBE_OPERATION_INVALID",
            "ANDROID_NETWORK_PROBE_OUTPUT_INVALID",
            "ANDROID_NETWORK_PROBE_IDENTITY_INVALID",
            "ANDROID_NETWORK_PROBE_REQUIRES_API_34",
            "ANDROID_NETWORK_PROBE_DIRECTORY_FAILED",
            "ANDROID_NETWORK_PROBE_DEX_MISSING",
            "ANDROID_NETWORK_PROBE_CLASS_DEX_MISSING",
            "ANDROID_NETWORK_PROBE_PERMISSIONS_FAILED",
            "ANDROID_NETWORK_PROBE_PIPE_FAILED",
            "ANDROID_NETWORK_PROBE_STAGE_FAILED",
            "ANDROID_NETWORK_PROBE_STAGE_SIZE_INVALID",
            "ANDROID_NETWORK_PROBE_FAILED",
            "ANDROID_NETWORK_PROBE_PROVIDER_ACCESS_DENIED",
            "ANDROID_NETWORK_PROBE_PROVIDER_FAILED",
            "ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID",
            "ANDROID_NETWORK_PROBE_PROVIDER_IDENTITY_INVALID",
            "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN",
            "ANDROID_VPN_NETWORK_UNAVAILABLE",
            "ANDROID_STABILITY_HTTP_STATUS",
            "ANDROID_STABILITY_TIMEOUT",
            "ANDROID_DOWNLOAD_INVALID",
            "ANDROID_UPLOAD_STATUS",
            "ANDROID_OPERATION_REQUIRES_CONNECTION",
            "ANDROID_GO_OPERATION_FAILED",
            "ANDROID_FILE_NAME_INVALID",
            "ANDROID_CONTROL_TIMEOUT",
            "ANDROID_ATOMIC_WRITE_FAILED",
            "ANDROID_CONTROL_STALE_FILE",
            "INVALID_ARGUMENT",
            "MALFORMED_CONFIG",
            "STALE_REVISION",
            "PLATFORM_PERMISSION_REQUIRED",
            "PLATFORM_FAILED",
            "INTERNAL",
            "SESSION_NOT_FOUND",
            "STALE_SESSION",
    };

    private final Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
    private final Context testContext = InstrumentationRegistry.getInstrumentation().getContext();
    private final ConnectivityManager connectivity =
            context.getSystemService(ConnectivityManager.class);
    private Activity foregroundActivity;
    private File progressFile;
    private JSONObject progressObservation;
    private String progressOperation = "command";
    private String progressStage = "start";
    private String progressPostTapState = "";
    private long progressSequence;
    private boolean consentTimeoutDiagnosed;
    private final JSONArray screenshotHistory = new JSONArray();
    private final File screenshotDirectory = new File(
            // Instrumentation executes in the target application's UID. The
            // instrumentation APK's cache is a different sandbox and is not
            // writable from this process.
            context.getCacheDir(), "dobbyvpn-rendered-screenshots");

    @Before
    public void configureBoundedSelectorPolling() {
        // findObject() otherwise applies UiAutomator's global selector wait
        // to every probe. Hosted UI helpers already own their deadlines; on
        // API 35 a missing selector can otherwise block for several seconds
        // and make a bounded surface timeout run far beyond its command
        // budget.
        Configurator.getInstance().setWaitForSelectorTimeout(0);
        if (screenshotDirectory.exists() && !deleteScreenshotTree(screenshotDirectory)) {
            throw new IllegalStateException("ANDROID_UI_SCREENSHOT_DIRECTORY_CLEANUP_FAILED");
        }
        if (!screenshotDirectory.mkdirs() && !screenshotDirectory.isDirectory()) {
            throw new IllegalStateException("ANDROID_UI_SCREENSHOT_DIRECTORY_FAILED");
        }
        File[] remainingScreenshots = screenshotDirectory.listFiles();
        if (remainingScreenshots == null || remainingScreenshots.length != 0) {
            throw new IllegalStateException("ANDROID_UI_SCREENSHOT_DIRECTORY_NOT_EMPTY");
        }
        while (screenshotHistory.length() > 0) screenshotHistory.remove(0);
    }

    @Test
    public void runHostedCommand() throws Exception {
        Bundle arguments = InstrumentationRegistry.getArguments();
        String commandName = arguments.getString(COMMAND_ARGUMENT);
        if (commandName == null || commandName.isEmpty()) {
            throw new IllegalArgumentException("ANDROID_COMMAND_FILE_MISSING");
        }
        File commandFile = safeFile(commandName);
        JSONObject command = readJson(commandFile);
        File profileFile = safeFile(command.getString("profile_file"));
        File outputFile = safeFile(command.getString("output_file"));
        JSONObject observation = baseObservation(command);
        String progressName = command.optString("progress_file", "");
        progressFile = progressName.isEmpty() ? null : safeFile(progressName);
        progressObservation = observation;
        markProgress("command", "start", "started");
        boolean guiAuto = GUI_AUTO_MODE.equals(command.optString(
                "ui_mode", command.optString("coverage_lane", "")));
        if (guiAuto && command.has("profile_index")) {
            throw new IllegalArgumentException("ANDROID_GUI_AUTO_PROFILE_INDEX_FORBIDDEN");
        }
        String sessionID = "";
        long sequence = 0L;
        long generation = 0L;
        boolean configured = false;
        boolean connected = false;
        boolean disconnectClean = false;

        try {
            if (!guiAuto) {
                NativeGoSession.attach(context);
                JSONObject initial = snapshotResult("");
                sessionID = initial.getString("session_id");
                sequence = initial.getLong("sequence");
            }
            byte[] profile = readBytes(profileFile);
            JSONArray operations = command.getJSONArray("operations");
            for (int i = 0; i < operations.length(); i++) {
                JSONObject operation = operations.getJSONObject(i);
                String name = operation.getString("operation");
                String operationID = operation.getString("id");
                markProgress(name, "start", "started");
                switch (name) {
                    case "configure": {
                        if (guiAuto) {
                            long configureDeadline = System.currentTimeMillis()
                                    + operationTimeout(operation);
                            configureThroughRenderedUI(
                                    profile,
                                    remainingTimeout(configureDeadline,
                                            "ANDROID_UI_CONFIGURE_TIMEOUT"));
                            // The one-step configure scenario must prove that
                            // the rendered profile was accepted by Go.  A
                            // later connect/reconnect owns that visible start
                            // action, so do not consume it here and double
                            // connect the same scenario.
                            boolean startsLater = hasFollowingConnectionStart(
                                    operations, i + 1);
                            boolean consentHandled = false;
                            if (!startsLater) {
                                consentHandled = connectThroughRenderedUI(
                                        remainingTimeout(configureDeadline,
                                                "ANDROID_UI_CONFIGURE_TIMEOUT"));
                                disconnectThroughRenderedUI(
                                        remainingTimeout(configureDeadline,
                                                "ANDROID_UI_CONFIGURE_TIMEOUT"));
                                boolean noVpn = awaitVpnNetwork(
                                        false,
                                        Math.min(
                                                NETWORK_RECOVERY_TIMEOUT_MILLIS,
                                                remainingTimeout(configureDeadline,
                                                        "ANDROID_UI_CONFIGURE_TIMEOUT"))) == null;
                                if (!noVpn) {
                                    throw new IllegalStateException(
                                            "ANDROID_UI_CONFIGURE_DISCONNECT_FAILED");
                                }
                                observation.put("disconnect_clean", true);
                                observation.put("final_disconnect_clean", true);
                                observation.put("cleanup_verified", true);
                            }
                            setGuiAutoConnection(observation);
                            configured = true;
                            observation.put("configured", true);
                            observation.put("gui_auto_verified", true);
                            observation.put("vpn_consent_handled", consentHandled);
                        } else {
                            JSONObject configuredEnvelope = requireOK(
                                    NativeGoSession.configure(sessionID, sequence, profile));
                            JSONObject configuredResult = configuredEnvelope.getJSONObject("result");
                            copyProfiles(observation, configuredResult.optJSONArray("profiles"));
                            if (command.has("profile_index")) {
                                observation.put("connection", selectedConnection(
                                        observation, command.getInt("profile_index")));
                            }
                            configured = true;
                            JSONObject snapshot = snapshotResult(sessionID);
                            sessionID = snapshot.optString("session_id", sessionID);
                            sequence = snapshot.optLong("sequence", sequence);
                            observation.put("configured", true);
                        }
                        break;
                    }
                    case "connect": {
                        if (!configured) throw new IllegalStateException("ANDROID_CONNECT_BEFORE_CONFIGURE");
                        if (guiAuto) {
                            boolean consentHandled = connectThroughRenderedUI(
                                    operationTimeout(operation));
                            connected = true;
                            observation.put("connected", true);
                            observation.put("gui_auto_verified", true);
                            observation.put("vpn_consent_handled", consentHandled);
                        } else {
                            ensureVpnReady();
                            int index = command.has("profile_index")
                                    ? command.getInt("profile_index") : 0;
                            JSONObject started = requireOK(NativeGoSession.start(
                                    sessionID, sequence, START_MODE_PROFILE_INDEX, index));
                            JSONObject startedResult = started.getJSONObject("result");
                            generation = startedResult.getLong("generation");
                            sequence = startedResult.getLong("sequence");
                            JSONObject ready = awaitState(sessionID, "CONNECTED", operationTimeout(operation));
                            connected = "CONNECTED".equals(ready.optString("state"));
                            observation.put("connected", connected);
                            observation.put("connection", selectedConnection(observation, index));
                        }
                        break;
                    }
                    case "observe_tunnel":
                        requireConnected(connected);
                        if (awaitVpnNetwork(true, operationTimeout(operation)) == null) {
                            throw new IllegalStateException("ANDROID_TUNNEL_NOT_PRESENT");
                        }
                        observation.put("second-tunnel".equals(operationID)
                                ? "second_tunnel_interface" : "tunnel_interface", true);
                        break;
                    case "observe_routing_identity":
                        requireConnected(connected);
                        runRoutingProof(operation.getString("control_file"),
                                command.getJSONObject("endpoints").getString("identity_url"),
                                SystemClock.elapsedRealtime() + operationTimeout(operation));
                        observation.put("second-routing".equals(operationID)
                                ? "second_routing_verified" : "routing_verified", true);
                        break;
                    case "measure_stability":
                        requireConnected(connected);
                        measureStability(command.getJSONObject("endpoints").getString("latency_url"),
                                operationTimeout(operation));
                        observation.put("stability_verified", true);
                        observation.put("stability_sample_count", STABILITY_SAMPLES);
                        observation.put("stability_sample_interval_seconds", 1.0);
                        break;
                    case "measure_throughput":
                        requireConnected(connected);
                        JSONObject metrics = measureThroughput(
                                command.getJSONObject("endpoints").getString("download_url"),
                                command.getJSONObject("endpoints").getString("upload_url"),
                                operationTimeout(operation));
                        observation.put("latency_ms", metrics.getDouble("latency_ms"));
                        observation.put("download_mbps", metrics.getDouble("download_mbps"));
                        observation.put("upload_mbps", metrics.getDouble("upload_mbps"));
                        break;
                    case "network_transition":
                        requireConnected(connected);
                        runNetworkTransition(operation.getString("control_file"),
                                command.getJSONObject("endpoints").getString("identity_url"),
                                SystemClock.elapsedRealtime() + operationTimeout(operation));
                        observation.put("network_transition_verified", true);
                        break;
                    case "disconnect": {
                        if (guiAuto) {
                            disconnectThroughRenderedUI(operationTimeout(operation));
                            boolean vpnRemoved = awaitVpnNetwork(
                                    false, operationTimeout(operation)) == null;
                            disconnectClean = vpnRemoved;
                            connected = false;
                            observation.put("disconnect_clean", disconnectClean);
                            observation.put("gui_auto_verified", true);
                        } else {
                            if (generation <= 0) throw new IllegalStateException("ANDROID_DISCONNECT_WITHOUT_GENERATION");
                            requireOK(NativeGoSession.stop(sessionID, generation));
                            JSONObject idle = awaitState(sessionID, "IDLE", operationTimeout(operation));
                            sequence = idle.optLong("sequence", sequence);
                            boolean vpnRemoved = awaitVpnNetwork(
                                    false, operationTimeout(operation)) == null;
                            disconnectClean = "IDLE".equals(idle.optString("state")) && vpnRemoved;
                            connected = false;
                            observation.put("disconnect_clean", disconnectClean);
                        }
                        break;
                    }
                    case "reconnect": {
                        if (guiAuto) {
                            boolean consentHandled = connectThroughRenderedUI(
                                    operationTimeout(operation), "reconnect");
                            if (awaitVpnNetwork(true, operationTimeout(operation)) == null) {
                                throw new IllegalStateException("ANDROID_RECONNECT_TUNNEL_NOT_PRESENT");
                            }
                            connected = true;
                            observation.put("restart_verified", true);
                            observation.put("reconnect_completed", true);
                            observation.put("gui_auto_verified", true);
                            observation.put("vpn_consent_handled",
                                    observation.optBoolean("vpn_consent_handled") || consentHandled);
                        } else {
                            ensureVpnReady();
                            int index = command.has("profile_index") ? command.getInt("profile_index") : 0;
                            JSONObject started = requireOK(NativeGoSession.start(
                                    sessionID, sequence, START_MODE_PROFILE_INDEX, index));
                            JSONObject startedResult = started.getJSONObject("result");
                            generation = startedResult.getLong("generation");
                            sequence = startedResult.getLong("sequence");
                            awaitState(sessionID, "CONNECTED", operationTimeout(operation));
                            if (awaitVpnNetwork(true, operationTimeout(operation)) == null) {
                                throw new IllegalStateException("ANDROID_RECONNECT_TUNNEL_NOT_PRESENT");
                            }
                            connected = true;
                            observation.put("restart_verified", true);
                            observation.put("reconnect_completed", true);
                        }
                        break;
                    }
                    case "inspect_cleanup": {
                        if (guiAuto) {
                            boolean noVpn = awaitVpnNetwork(false, operationTimeout(operation)) == null;
                            boolean reopened = reopenRenderedUI(operationTimeout(operation));
                            observation.put("cleanup_verified", reopened && noVpn);
                            observation.put("final_disconnect_clean", reopened && noVpn);
                            observation.put("ui_reopen_verified", reopened);
                            observation.put("gui_auto_verified", true);
                        } else {
                            JSONObject finalSnapshot = snapshotResult(sessionID);
                            boolean idle = "IDLE".equals(finalSnapshot.optString("state"));
                            boolean noVpn = awaitVpnNetwork(false, operationTimeout(operation)) == null;
                            observation.put("cleanup_verified", idle && noVpn);
                            observation.put("final_disconnect_clean", idle && noVpn);
                        }
                        break;
                    }
                    case "process_loss":
                        // The owner kills/restarts the target process around
                        // this marker. The recovery phase is a fresh test
                        // invocation and will execute the same Go calls.
                        break;
                    default:
                        throw new IllegalArgumentException("ANDROID_OPERATION_UNSUPPORTED");
                }
                markProgress(name, "complete", "completed");
            }
        } catch (Throwable failure) {
            if (!consentTimeoutDiagnosed) {
                try {
                    markProgress(progressOperation, progressStage, "failed");
                } catch (Throwable screenshotFailure) {
                    // Keep the product assertion/error primary while making
                    // mandatory rendered-artifact collection failure
                    // explicit and attached.
                    failure.addSuppressed(screenshotFailure);
                    observation.put(
                            "screenshot_error_code",
                            "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED");
                    try {
                        publishScreenshotFailureMarker();
                    } catch (Throwable markerFailure) {
                        failure.addSuppressed(markerFailure);
                    }
                }
            }
            observation.put("error_code", fixedFailureCode(failure));
            try {
                CompleteThrowableReporter.report(
                        InstrumentationRegistry.getInstrumentation(), failure);
            } catch (Throwable reportError) {
                failure.addSuppressed(new IllegalStateException(
                        "ANDROID_COMPLETE_THROWABLE_REPORT_FAILED", reportError));
            }
        } finally {
            String cleanupError = null;
            try {
                deleteIfPresent(commandFile);
            } catch (Throwable error) {
                cleanupError = "ANDROID_COMMAND_FILE_CLEANUP_FAILED";
            }
            try {
                deleteIfPresent(profileFile);
            } catch (Throwable error) {
                cleanupError = cleanupError == null
                        ? "ANDROID_PROFILE_FILE_CLEANUP_FAILED"
                        : cleanupError + ",ANDROID_PROFILE_FILE_CLEANUP_FAILED";
            }
            if (cleanupError != null) {
                observation.put("cleanup_error", cleanupError);
                if (!observation.has("error_code")) {
                    observation.put("error_code", "ANDROID_CONTROL_CLEANUP_FAILED");
                }
            }
            writeJson(outputFile, observation);
            progressFile = null;
            progressObservation = null;
        }
    }

    private JSONObject baseObservation(JSONObject command) throws Exception {
        JSONObject output = new JSONObject();
        if (command.has("source_sha")) output.put("source_sha", command.getString("source_sha"));
        String lane = command.optString(
                "coverage_lane", command.optString("ui_mode", BINDING_MODE));
        String uiMode = command.optString("ui_mode", lane);
        if (!BINDING_MODE.equals(lane) && !GUI_AUTO_MODE.equals(lane)) {
            throw new IllegalArgumentException("ANDROID_COVERAGE_LANE_INVALID");
        }
        if (!lane.equals(uiMode)) {
            throw new IllegalArgumentException("ANDROID_COVERAGE_LANE_MISMATCH");
        }
        output.put("coverage_lane", lane);
        output.put("connections", new JSONArray());
        output.put("gui_auto_verified", false);
        output.put("ui_reopen_verified", false);
        output.put("vpn_consent_handled", false);
        output.put("configured", false);
        output.put("connected", false);
        output.put("tunnel_interface", false);
        output.put("routing_verified", false);
        output.put("stability_verified", false);
        output.put("stability_sample_count", STABILITY_SAMPLES);
        output.put("stability_sample_interval_seconds", 1.0);
        output.put("network_transition_verified", false);
        output.put("process_loss_verified", false);
        output.put("latency_ms", 0.0);
        output.put("download_mbps", 0.0);
        output.put("upload_mbps", 0.0);
        output.put("disconnect_clean", false);
        output.put("restart_verified", false);
        output.put("reconnect_completed", false);
        output.put("second_tunnel_interface", false);
        output.put("second_routing_verified", false);
        output.put("final_disconnect_clean", false);
        output.put("cleanup_verified", false);
        return output;
    }

    /**
     * Keep the app-to-host failure boundary deliberately small.  Exception
     * messages can contain profile text, endpoint values, file names, system
     * paths, or framework details; only the fixed contract vocabulary crosses
     * into the observation consumed by the adapter.
     */
    private String fixedFailureCode(Throwable failure) {
        if (failure == null) return FALLBACK_ERROR_CODE;
        String description = failure.toString();
        int separator = description.indexOf(": ");
        return fixedFailureCode(
                separator < 0 ? description : description.substring(separator + 2));
    }

    private String fixedFailureCode(String message) {
        if (message == null || message.isEmpty()) return FALLBACK_ERROR_CODE;
        int separator = message.indexOf(':');
        String candidate = separator < 0 ? message : message.substring(0, separator);
        for (String code : FIXED_ERROR_CODES) {
            if (code.equals(candidate)) return code;
        }
        return FALLBACK_ERROR_CODE;
    }

    private void setGuiAutoConnection(JSONObject output) throws Exception {
        JSONArray values = new JSONArray();
        JSONObject auto = new JSONObject()
                .put("index", 0)
                .put("protocol", GUI_AUTO_PROTOCOL);
        values.put(auto);
        output.put("connections", values);
        output.put("connection", auto);
    }

    private void copyProfiles(JSONObject output, JSONArray profiles) throws Exception {
        if (profiles == null || profiles.length() == 0) throw new IllegalArgumentException("ANDROID_NO_PROFILES");
        JSONArray copy = new JSONArray();
        for (int i = 0; i < profiles.length(); i++) {
            JSONObject profile = profiles.getJSONObject(i);
            copy.put(new JSONObject()
                    .put("index", profile.getInt("index"))
                    .put("protocol", profile.getString("protocol")));
        }
        output.put("connections", copy);
    }

    private JSONObject selectedConnection(JSONObject output, int index) throws Exception {
        JSONArray values = output.getJSONArray("connections");
        for (int i = 0; i < values.length(); i++) {
            JSONObject value = values.getJSONObject(i);
            if (value.getInt("index") == index) return value;
        }
        throw new IllegalArgumentException("ANDROID_PROFILE_INDEX_INVALID");
    }

    /**
     * Enter one fresh profile through the production Fyne renderer.
     *
     * Fyne exposes its multiline entry through a short-lived native
     * android.widget.EditText while the field has focus.  UiAutomator is used
     * only for that real input bridge and for visible controls; no session
     * binding call is made by this lane.  The profile bytes stay in the
     * instrumentation process and are never included in diagnostics.
     */
    private void configureThroughRenderedUI(byte[] profile, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress("configure", "surface", "started");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        // The controller may have launched the production Activity before
        // instrumentation started. Bind that already-rendered Activity
        // before opening the configuration editor; otherwise the lifecycle
        // refresh after the first focus sample can replace the focused editor
        // and report a false ANDROID_UI_INPUT_FOCUS_LOST. The resolver first
        // adopts Fyne's live Activity, so this does not relaunch a usable
        // NativeActivity merely because AndroidX did not observe its birth.
        foregroundActivity = ensureForegroundActivity();
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        markProgress("configure", "surface", "completed");
        markProgress("configure", "configuration-control", "started");
        tapUiControl(
                "Connection configuration",
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        markProgress("configure", "configuration-control", "completed");
        markProgress("configure", "input-focus", "started");
        waitForFocusedNativeInput(
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        markProgress("configure", "input-focus", "completed");
        // Preserve the downloaded source, including line boundaries and a
        // trailing LF. The production Connect callback trims the complete
        // source before parsing; the test must still send every line through
        // the focused editor's native IME boundary below. It never calls
        // UiObject2.setText for the opaque profile.
        String text = new String(profile, StandardCharsets.UTF_8);
        if (text.trim().isEmpty()) {
            throw new IllegalArgumentException("ANDROID_PROFILE_TEXT_EMPTY");
        }
        if (text.indexOf('\u0000') >= 0) {
            throw new IllegalArgumentException("ANDROID_PROFILE_TEXT_INVALID");
        }
        markProgress("configure", "profile-entry", "started");
        injectProfileThroughNativeInput(text, deadline);
        markProgress("configure", "profile-entry", "completed");
        // injectProfileThroughNativeInput waits for a short bounded native/Go
        // event settle before returning. The following rendered Connect
        // action plus independent tunnel/routing checks are the authoritative
        // proof that the complete configuration arrived; the transient hidden
        // editor is not used as a qualification gate because accessibility
        // text can be cached or normalize Fyne's sentinel.
        hideNativeInput(
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        markProgress("configure", "input-dismiss", "completed");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        // Match the standalone renderer contract before the first Connect
        // action.  Returning through a real Settings screen transition gives
        // Fyne time to retire the native EditText/focus bridge and rebuild its
        // rendered accessibility snapshot.  Without this settle point the
        // hosted runner can discover the visible Connect node while the next
        // coordinate touch is still swallowed by the just-dismissed editor;
        // that is not a valid rendered Connect proof.
        markProgress("configure", "rendered-navigation", "started");
        tapUiControl(
                "Settings",
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        waitForUiControl(
                "Back",
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        tapUiControl(
                "Back",
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        waitForUiState(
                "Disconnected",
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        markProgress("configure", "rendered-navigation", "completed");
    }

    private boolean hasFollowingConnectionStart(JSONArray operations, int start)
            throws Exception {
        for (int index = start; index < operations.length(); index++) {
            String operation = operations.getJSONObject(index).getString("operation");
            if ("connect".equals(operation) || "reconnect".equals(operation)) {
                return true;
            }
        }
        return false;
    }

    private long remainingTimeout(long deadline, String errorCode) {
        long remaining = deadline - System.currentTimeMillis();
        if (remaining <= 0) throw new IllegalStateException(errorCode);
        return remaining;
    }

    /** Tap the rendered Connect button, completing Android VPN consent once. */
    private boolean connectThroughRenderedUI(long timeout) throws Exception {
        return connectThroughRenderedUI(timeout, "connect");
    }

    private boolean connectThroughRenderedUI(long timeout, String operation) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress(operation, "surface", "started");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
        markProgress(operation, "surface", "completed");
        markProgress(operation, "vpn-network-clear", "started");
        if (awaitVpnNetwork(
                false,
                Math.min(
                        NETWORK_RECOVERY_TIMEOUT_MILLIS,
                remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"))) != null) {
            throw new IllegalStateException("ANDROID_STALE_VPN_NETWORK");
        }
        markProgress(operation, "vpn-network-clear", "completed");
        markProgress(operation, "physical-network", "started");
        awaitValidatedPhysicalNetwork(
                remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
        markProgress(operation, "physical-network", "completed");
        boolean consentNeeded = VpnService.prepare(context) != null;
        markProgress(operation, "connect-control", "started");
        tapUiControl(
                CONNECTION_ACTION_LABEL,
                remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
        markProgress(operation, "connect-control", "completed");
        // Record only the small, non-sensitive status vocabulary after the
        // rendered tap.  If Android never shows consent, this distinguishes a
        // swallowed touch (still Disconnected) from profile rejection
        // (Error/Failed) without exposing the entered configuration.
        String postTapState = awaitVisibleConnectionState(
                Math.min(2_000L,
                        remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT")));
        String postTapErrorCategory = "";
        if ("Error".equals(postTapState) || "Failed".equals(postTapState)) {
            // Error becomes accessible before its Details/category node on
            // some Android/Fyne frames. Give the fixed, redacted category
            // vocabulary a short bounded poll, still owned by the command's
            // overall deadline, before classifying the rendered failure.
            postTapErrorCategory = visibleErrorCategory(
                    Math.min(
                            ERROR_CATEGORY_TIMEOUT_MILLIS,
                            remainingTimeout(deadline,
                                    "ANDROID_UI_CONNECT_TIMEOUT")));
            postTapState += "/" + postTapErrorCategory;
        }
        progressPostTapState = postTapState;
        if (progressObservation != null) {
            progressObservation.put("post_tap_state", postTapState);
        }
        markProgress(operation, "post-tap-" + postTapState.toLowerCase(Locale.ROOT),
                "observed");
        boolean knownNonPermissionFailure = isKnownNonPermissionErrorCategory(
                postTapErrorCategory);
        boolean unclassifiedWithoutConsent = !consentNeeded
                && "UNCLASSIFIED".equals(postTapErrorCategory);
        if (knownNonPermissionFailure || unclassifiedWithoutConsent) {
            // A known non-permission category proves that Go rejected the
            // entered source before Android's VPN permission boundary. Do not
            // open or wait on consent: it would hide the product error and
            // turn the real cause into a misleading consent timeout. An
            // unclassified Error is inconclusive while consent is pending,
            // because the Details node can lag the status node; let the real
            // consent flow resolve that case. Once permission is already
            // granted, the same unclassified Error is terminal.
            // The category is selected from the fixed allowlist in
            // visibleErrorCategory(); no profile text or free-form detail
            // crosses this error boundary.
            throw new IllegalStateException(
                    "ANDROID_UI_CONNECT_FAILED" + ":" + postTapErrorCategory);
        }
        if (consentNeeded) {
            markProgress(operation, "consent", "started");
            acceptVpnConsent(
                    remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"), operation);
            markProgress(operation, "consent", "completed");
            // The first production click correctly reports the permission
            // boundary as an error.  Once the system grant is durable, the
            // next visible Connect click is the real Go/Fyne start action.
            markProgress(operation, "connect-retry", "started");
            waitForUiControl(
                    CONNECTION_ACTION_LABEL,
                    remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
            tapUiControl(
                CONNECTION_ACTION_LABEL,
                    remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
            markProgress(operation, "connect-retry", "completed");
        }
        markProgress(operation, "connected-state", "started");
        waitForUiState(
                "Connected",
                remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
        markProgress(operation, "connected-state", "completed");
        return consentNeeded;
    }

    private void disconnectThroughRenderedUI(long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress("disconnect", "surface", "started");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        markProgress("disconnect", "surface", "completed");
        markProgress("disconnect", "disconnect-control", "started");
        tapUiControl(
                CONNECTION_ACTION_LABEL,
                remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        markProgress("disconnect", "disconnect-control", "completed");
        markProgress("disconnect", "disconnected-state", "started");
        waitForUiState(
                "Disconnected",
                remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        markProgress("disconnect", "disconnected-state", "completed");
    }

    /**
     * Reopen the actual Go/Fyne activity and verify that the visible state is
     * usable again.  The VPN service is intentionally observed separately by
     * the caller, so a rendered reopen cannot manufacture tunnel evidence.
     */
    private boolean reopenRenderedUI(long timeout) throws Exception {
        markProgress("inspect_cleanup", "reopen", "started");
        UiDevice device = uiDevice();
        device.pressHome();
        long waitMillis = Math.max(1L, Math.min(5_000L, timeout));
        if (!device.wait(androidx.test.uiautomator.Until.gone(
                By.pkg(context.getPackageName())), waitMillis)) {
            throw new IllegalStateException("ANDROID_UI_BACKGROUND_FAILED");
        }
        foregroundActivity = null;
        ensureForegroundActivity();
        ensureUiSurface(timeout);
        boolean hasConnect = findUiObject(CONNECTION_ACTION_LABEL) != null;
        boolean hasStatus = findUiObject("Disconnected") != null
                || findUiObject("Ready") != null
                || findUiObject("Error") != null
                || findUiObject("Failed") != null;
        if (!hasConnect || !hasStatus) {
            throw new IllegalStateException("ANDROID_UI_REOPEN_STATE_INVALID");
        }
        markProgress("inspect_cleanup", "reopen", "completed");
        return true;
    }

    private UiDevice uiDevice() {
        return UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
    }

    private UiObject2 findUiObject(String label) {
        UiDevice device = uiDevice();
        UiObject2 value = device.findObject(By.text(label).pkg(context.getPackageName()));
        if (value != null) return value;
        return device.findObject(By.desc(label).pkg(context.getPackageName()));
    }

    private UiObject2 waitForUiControl(String label, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            UiObject2 value = findUiObject(label);
            if (value != null && value.isEnabled() && !value.getVisibleBounds().isEmpty()) {
                return value;
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_CONTROL_TIMEOUT");
    }

    private void tapUiControl(String label, long timeout) throws Exception {
        UiDevice device = uiDevice();
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        Rect previous = null;
        int stable = 0;
        while (System.currentTimeMillis() < deadline) {
            UiObject2 value = findUiObject(label);
            if (value != null && value.isEnabled()) {
                Rect bounds = value.getVisibleBounds();
                if (!bounds.isEmpty()) {
                    if (bounds.equals(previous)) {
                        stable++;
                    } else {
                        previous = new Rect(bounds);
                        stable = 0;
                    }
                    if (stable >= UI_STABILITY_SAMPLES) {
                        if (!device.click(bounds.centerX(), bounds.centerY())) {
                            throw new IllegalStateException("ANDROID_UI_TAP_FAILED");
                        }
                        waitForIdleBounded(device, deadline);
                        return;
                    }
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_CONTROL_TIMEOUT");
    }

    private void ensureUiSurface(long timeout) throws Exception {
        UiDevice device = uiDevice();
        if (findUiObject(CONNECTION_ACTION_LABEL) == null) {
            ensureForegroundActivity();
        }
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            boolean button = findUiObject(CONNECTION_ACTION_LABEL) != null;
            boolean status = findUiObject("Disconnected") != null
                    || findUiObject("Ready") != null
                    || findUiObject("Connecting") != null
                    || findUiObject("Connected") != null
                    || findUiObject("Disconnecting") != null
                    || findUiObject("Reconnecting") != null
                    || findUiObject("Error") != null
                    || findUiObject("Failed") != null;
            if (button && status) return;
            waitForIdleBounded(device, deadline);
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_SURFACE_TIMEOUT");
    }

    private void waitForIdleBounded(UiDevice device, long deadline) {
        long remaining = deadline - System.currentTimeMillis();
        if (remaining > 0) {
            device.waitForIdle(Math.min(1_000L, remaining));
        }
    }

    private void waitForUiState(String expected, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            if (findUiObject(expected) != null) return;
            if ("Connected".equals(expected)
                    && (findUiObject("Error") != null || findUiObject("Failed") != null)) {
                // The rendered error state can appear before its Details
                // category node. Poll only the fixed redacted vocabulary and
                // retain the operation deadline; never expose the visible
                // error text or any profile content in the failure code.
                String category = visibleErrorCategory(
                        Math.min(
                                ERROR_CATEGORY_TIMEOUT_MILLIS,
                                remainingTimeout(
                                        deadline, "ANDROID_UI_CONNECT_TIMEOUT")));
                throw new IllegalStateException("ANDROID_UI_CONNECT_FAILED");
            }
            if ("Disconnected".equals(expected)
                    && (findUiObject("Error") != null || findUiObject("Failed") != null)) {
                throw new IllegalStateException("ANDROID_UI_DISCONNECT_FAILED");
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_STATE_TIMEOUT");
    }

    private String awaitVisibleConnectionState(long timeout) throws Exception {
        String[] states = new String[]{
                "Connecting", "Connected", "Error", "Failed", "Ready", "Disconnected",
        };
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        String fallback = "Unknown";
        while (System.currentTimeMillis() < deadline) {
            for (String state : states) {
                if (findUiObject(state) != null) {
                    fallback = state;
                    if (!"Disconnected".equals(state) && !"Ready".equals(state)) {
                        return state;
                    }
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        return fallback;
    }

    private String visibleErrorCategory(long timeout) throws Exception {
        UiDevice device = uiDevice();
        String[] categories = new String[]{
                "MALFORMED_CONFIG",
                "INVALID_ARGUMENT",
                "STALE_REVISION",
                "PLATFORM_FAILED",
                "PLATFORM_PERMISSION_REQUIRED",
                "INTERNAL",
        };
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            for (String category : categories) {
                if (device.findObject(By.textContains(category)
                        .pkg(context.getPackageName())) != null
                        || device.findObject(By.descContains(category)
                                .pkg(context.getPackageName())) != null) {
                    return category;
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        return "UNCLASSIFIED";
    }

    private boolean isKnownNonPermissionErrorCategory(String category) {
        return "MALFORMED_CONFIG".equals(category)
                || "INVALID_ARGUMENT".equals(category)
                || "STALE_REVISION".equals(category)
                || "PLATFORM_FAILED".equals(category)
                || "INTERNAL".equals(category);
    }

    private UiObject2 waitForFocusedNativeInput(long timeout) throws Exception {
        UiDevice device = uiDevice();
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            UiObject2 input = device.findObject(
                    By.clazz("android.widget.EditText").pkg(context.getPackageName()));
            if (input != null && input.isFocused()) return input;
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_INPUT_FOCUS_TIMEOUT");
    }

    private void hideNativeInput(long timeout) throws Exception {
        UiDevice device = uiDevice();
        long deadline = System.currentTimeMillis()
                + Math.max(1L, Math.min(timeout, 5_000L));

        // Back is the same user action that Fyne's Android driver handles in
        // production.  Some IMEs consume that first Back themselves, however,
        // and a second sequential GUI scenario can leave Fyne's transient
        // editor visible even though the keyboard has already disappeared.
        device.pressBack();
        long initialDeadline = Math.min(
                deadline,
                System.currentTimeMillis() + INPUT_DISMISS_INITIAL_WAIT_MILLIS);
        if (waitForNativeInputGone(device, initialDeadline)) return;

        // Keep the assertion on the real production input bridge, but make
        // dismissal independent of the IME's Back-event timing. This runs on
        // the target Activity's UI thread and operates only on Fyne's actual
        // transient EditText; it never edits, replaces, or validates the
        // entered profile.
        forceHideNativeInput();
        if (waitForNativeInputGone(device, deadline)) return;
        throw new IllegalStateException("ANDROID_UI_INPUT_DISMISS_FAILED");
    }

    private boolean waitForNativeInputGone(UiDevice device, long deadline) throws Exception {
        while (System.currentTimeMillis() < deadline) {
            if (device.findObject(By.clazz("android.widget.EditText")
                    .pkg(context.getPackageName())) == null) return true;
            Thread.sleep(POLL_MILLIS);
        }
        return device.findObject(By.clazz("android.widget.EditText")
                .pkg(context.getPackageName())) == null;
    }

    private void forceHideNativeInput() throws Exception {
        String[] failure = new String[]{null};
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        try {
            instrumentation.runOnMainSync(() -> {
                try {
                    Activity activity = foregroundActivity;
                    if (activity == null || activity.isFinishing()) {
                        failure[0] = "ANDROID_UI_INPUT_DISMISS_FAILED";
                        return;
                    }
                    EditText editor = findCurrentNativeInput(activity);
                    if (editor == null) {
                        editor = findNativeInputView(activity.getWindow().getDecorView());
                    }
                    if (editor == null) return;
                    InputMethodManager manager = (InputMethodManager) activity.getSystemService(
                            Context.INPUT_METHOD_SERVICE);
                    if (manager != null) {
                        manager.hideSoftInputFromWindow(editor.getWindowToken(), 0);
                    }
                    editor.clearFocus();
                    editor.setVisibility(View.GONE);
                } catch (Throwable ignored) {
                    failure[0] = "ANDROID_UI_INPUT_DISMISS_FAILED";
                }
            });
        } catch (Throwable ignored) {
            failure[0] = "ANDROID_UI_INPUT_DISMISS_FAILED";
        }
        if (failure[0] != null) {
            throw new IllegalStateException(failure[0]);
        }
    }

    private boolean isEligibleNativeInput(EditText editor) {
        return editor != null
                && editor.getVisibility() == View.VISIBLE
                && editor.isShown()
                && editor.isEnabled()
                && editor.isFocusable()
                && context.getPackageName().equals(editor.getContext().getPackageName());
    }

    private EditText findCurrentNativeInput(Activity activity) {
        if (activity == null) return null;
        View focused = activity.getCurrentFocus();
        if (focused instanceof EditText) {
            EditText editor = (EditText) focused;
            if (editor.isFocused() && isEligibleNativeInput(editor)) return editor;
        }
        return null;
    }

    private EditText findNativeInputView(View root) {
        if (root instanceof EditText) {
            EditText editor = (EditText) root;
            return isEligibleNativeInput(editor) ? editor : null;
        }
        if (!(root instanceof ViewGroup)) return null;
        ViewGroup group = (ViewGroup) root;
        for (int index = 0; index < group.getChildCount(); index++) {
            EditText editor = findNativeInputView(group.getChildAt(index));
            if (editor != null) return editor;
        }
        return null;
    }

    /** Commit the opaque profile through the focused editor's real IME seam. */
    private void injectProfileThroughNativeInput(String text, long deadline)
            throws Exception {
        // The process-loss lane launches the production Activity before it
        // starts instrumentation. In that valid preserve-active state the
        // rendered controls are already visible, so ensureUiSurface() may not
        // have needed to launch/discover the Activity for this test instance.
        // Refresh the lifecycle handle before reading the focused native view.
        // Do not retain or re-query the UiObject2 returned by the polling
        // helper here.  Android may invalidate that accessibility wrapper
        // when Fyne rebuilds its transient editor between the final focus
        // sample and this commit (especially after a process-loss launch).
        // Resolve the currently resumed Activity and its current focus on the
        // main thread below; that is the real native-input seam and already
        // reports the fixed focus/commit codes when it is no longer usable.
        foregroundActivity = ensureForegroundActivity();

        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        // Commit the complete source in bounded chunks through one focused
        // production InputConnection. Android's native editor rejects a
        // single very large commit, while chunking preserves every newline
        // and the exact source order. Fyne's Android keyboard path maps LF to
        // Return; no synthetic key events are needed. The rendered Connect
        // result and subsequent consent, tunnel, routing, and traffic checks
        // are the authoritative proof that Go received the complete source.
        commitAndVerifyNativeInput(instrumentation, text, deadline);

        waitForIdleBounded(uiDevice(), deadline);
    }

    private void commitAndVerifyNativeInput(
            Instrumentation instrumentation, String text, long deadline) throws Exception {
        remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT");
        long reacquireDeadline = Math.min(
                deadline,
                System.currentTimeMillis() + INPUT_REACQUIRE_TIMEOUT_MILLIS);
        while (true) {
            String[] failure = new String[]{null};
            try {
                instrumentation.runOnMainSync(() -> {
                    try {
                        Activity activity = foregroundActivity;
                        EditText editor = resolveFocusedNativeInput(activity);
                        if (editor == null) {
                            // Accessibility can observe the newly visible Fyne
                            // editor one frame before Activity.currentFocus is
                            // updated after a cold process-loss launch.  Keep
                            // this a real native-focus assertion and let the
                            // bounded outer loop reacquire that same editor.
                            failure[0] = "ANDROID_UI_INPUT_FOCUS_LOST";
                            return;
                        }
                        View focused = editor;
                        EditorInfo editorInfo = new EditorInfo();
                        InputConnection connection = focused.onCreateInputConnection(editorInfo);
                        if (connection == null) {
                            failure[0] = "ANDROID_UI_INPUT_COMMIT_FAILED";
                            return;
                        }
                        int start = 0;
                        while (start < text.length()) {
                            if (System.currentTimeMillis() >= deadline) {
                                failure[0] = "ANDROID_UI_CONFIGURE_TIMEOUT";
                                return;
                            }
                            editor.setSelection(editor.length());
                            int end = Math.min(
                                    text.length(), start + PROFILE_INPUT_CHUNK_CODE_UNITS);
                            if (end < text.length()
                                    && end > start
                                    && Character.isHighSurrogate(text.charAt(end - 1))
                                    && Character.isLowSurrogate(text.charAt(end))) {
                                end--;
                            }
                            String chunk = text.substring(start, end);
                            if (!connection.commitText(chunk, 1)) {
                                failure[0] = "ANDROID_UI_INPUT_COMMIT_FAILED";
                                return;
                            }
                            if (!connection.finishComposingText()) {
                                failure[0] = "ANDROID_UI_INPUT_COMMIT_FAILED";
                                return;
                            }
                            if (System.currentTimeMillis() >= deadline) {
                                failure[0] = "ANDROID_UI_CONFIGURE_TIMEOUT";
                                return;
                            }
                            start = end;
                        }
                    } catch (Throwable ignored) {
                        failure[0] = "ANDROID_UI_INPUT_COMMIT_FAILED";
                    }
                });
            } catch (Throwable ignored) {
                failure[0] = "ANDROID_UI_INPUT_COMMIT_FAILED";
            }
            if (failure[0] == null) break;
            if (!"ANDROID_UI_INPUT_FOCUS_LOST".equals(failure[0])
                    || System.currentTimeMillis() >= reacquireDeadline) {
                throw new IllegalStateException(failure[0]);
            }
            Thread.sleep(POLL_MILLIS);
        }
        remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT");
    }

    /**
     * Resolve the currently rendered Fyne editor on the Activity UI thread.
     *
     * The accessibility tree and Activity.currentFocus are updated by
     * different Android queues.  During a process-loss cold start the former
     * can report a focused EditText while the latter is still null.  Reusing
     * the production editor and requesting focus on the UI thread closes that
     * short handoff window without replacing the editor or injecting text by
     * any other seam.
     */
    private EditText resolveFocusedNativeInput(Activity activity) {
        if (activity == null || activity.isFinishing()) return null;
        // Prefer the Activity's actual focused production editor.  Fyne can
        // leave an older hidden EditText in the decor tree for one render
        // cycle after process loss; a first-child traversal would select it
        // and report a false focus loss even though the new editor is ready.
        EditText editor = findCurrentNativeInput(activity);
        if (editor == null) {
            editor = findNativeInputView(activity.getWindow().getDecorView());
        }
        if (editor == null) return null;
        View focused = activity.getCurrentFocus();
        if (focused != editor || !editor.isFocused()) {
            if (!editor.requestFocus()) return null;
        }
        focused = activity.getCurrentFocus();
        if (!(focused instanceof EditText) || focused != editor || !editor.isFocused()) return null;
        return editor;
    }

    private JSONObject snapshotResult(String sessionID) throws Exception {
        return requireOK(NativeGoSession.snapshot(sessionID)).getJSONObject("result");
    }

    private JSONObject awaitState(String sessionID, String expected, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        JSONObject latest = snapshotResult(sessionID);
        while (System.currentTimeMillis() < deadline) {
            latest = snapshotResult(sessionID);
            String state = latest.optString("state");
            if (expected.equals(state)) return latest;
            if ("FAILED".equals(state)) throw new IllegalStateException(
                    "ANDROID_SESSION_FAILED");
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_SESSION_STATE_TIMEOUT");
    }

    private void ensureVpnReady() throws Exception {
        // A force-stopped VPN process can disappear before ConnectivityService
        // has removed its Network. Starting the replacement session in that
        // gap leaves Go's bootstrap DNS lookup bound to the dead VPN. Require
        // both teardown and a revalidated physical path before every start.
        if (awaitVpnNetwork(false, NETWORK_RECOVERY_TIMEOUT_MILLIS) != null) {
            throw new IllegalStateException("ANDROID_STALE_VPN_NETWORK");
        }
        awaitValidatedPhysicalNetwork();
        // VpnService.prepare() returns a system consent activity. Launch it
        // from the product Activity rather than the instrumentation's
        // application context: Android may reject a background task launch
        // without showing any dialog when the hosted test starts before the
        // Go/Fyne window has been created.
        Activity activity = ensureForegroundActivity();
        int result = NativeVpnBridge.prepare(activity);
        if (result == 0) {
            acceptVpnConsent();
            result = NativeVpnBridge.prepare(activity);
        }
        if (result != 1) throw new IllegalStateException("ANDROID_VPN_PERMISSION_OR_SERVICE_FAILED");
    }

    private Activity ensureForegroundActivity() {
        Activity resumed = findResumedTargetActivity();
        if (resumed != null) {
            foregroundActivity = resumed;
            return resumed;
        }
        Activity live = findLiveFyneActivity();
        if (live != null) {
            foregroundActivity = live;
            return live;
        }
        foregroundActivity = null;
        Intent launch = context.getPackageManager()
                .getLaunchIntentForPackage(context.getPackageName());
        if (launch == null) throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_MISSING");
        // The controller may have launched the production app immediately
        // before --no-restart instrumentation. That Activity was resumed
        // before AndroidX's lifecycle monitor was installed, so it may not be
        // tracked even though it owns the rendered task. Bring the existing
        // task forward with NEW_TASK; clearing it can terminate Fyne's native
        // window while its Go event loop is still starting and leave a blank
        // replacement surface. The Fyne singleton resolver below and the
        // lifecycle monitor both get a chance to observe the same Activity.
        launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        try {
            // startActivitySync waits for the main queue to become idle. Fyne
            // continuously renders, so that wait can time out even though
            // the Activity is already usable. Schedule only the launch call;
            // lifecycle monitoring below observes the real resumed Activity.
            instrumentation.runOnMainSync(() -> context.startActivity(launch));
        } catch (RuntimeException error) {
            throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_FAILED", error);
        }
        long deadline = System.currentTimeMillis() + ACTIVITY_RESUME_TIMEOUT_MILLIS;
        while (System.currentTimeMillis() < deadline) {
            resumed = findResumedTargetActivity();
            if (resumed != null) {
                foregroundActivity = resumed;
                return resumed;
            }
            try {
                Thread.sleep(POLL_MILLIS);
            } catch (InterruptedException error) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_INTERRUPTED", error);
            }
        }
        throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_RESUME_TIMEOUT");
    }

    /**
     * Recover the already-running Fyne NativeActivity when it predates the
     * lifecycle monitor. Fyne keeps the current activity in its own process
     * singleton because the native keyboard bridge needs it; using that live
     * instance avoids replacing a rendered window just to obtain a Java
     * reference. If the test ever runs in a separate process, reflection
     * simply yields no value and the normal monitored launch path remains the
     * fallback.
     */
    private Activity findLiveFyneActivity() {
        AtomicReference<Activity> live = new AtomicReference<>();
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        instrumentation.runOnMainSync(() -> {
            try {
                Class<?> type;
                try {
                    // Instrumentation has both the test and target APKs on
                    // its class path. Resolve through the target Context's
                    // loader so this field belongs to the running Fyne app
                    // class loader rather than a duplicate test-side class.
                    type = context.getClassLoader().loadClass(
                            "org.golang.app.GoNativeActivity");
                } catch (ClassNotFoundException ignored) {
                    type = Class.forName("org.golang.app.GoNativeActivity");
                }
                Field field = type.getDeclaredField("goNativeActivity");
                field.setAccessible(true);
                Object value = field.get(null);
                if (!(value instanceof Activity)) return;
                Activity activity = (Activity) value;
                if (!activity.isFinishing()
                        && !activity.isDestroyed()
                        && activity.hasWindowFocus()
                        && context.getPackageName().equals(activity.getPackageName())) {
                    live.set(activity);
                }
            } catch (Throwable ignored) {
                // The generated Fyne activity is an implementation detail;
                // monitored discovery and a fresh launch are still valid
                // fallbacks when it is unavailable to this test process.
            }
        });
        return live.get();
    }

    private Activity findResumedTargetActivity() {
        AtomicReference<Activity> resumed = new AtomicReference<>();
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        instrumentation.runOnMainSync(() -> {
            ActivityLifecycleMonitor monitor = ActivityLifecycleMonitorRegistry.getInstance();
            for (Activity activity : monitor.getActivitiesInStage(Stage.RESUMED)) {
                if (activity != null
                        && !activity.isFinishing()
                        && context.getPackageName().equals(activity.getPackageName())) {
                    resumed.set(activity);
                    return;
                }
            }
        });
        return resumed.get();
    }

    private void acceptVpnConsent() throws Exception {
        acceptVpnConsent(15_000L);
    }

    private void acceptVpnConsent(long timeout) throws Exception {
        acceptVpnConsent(timeout, "connect");
    }

    private void acceptVpnConsent(long timeout, String operation) throws Exception {
        if (VpnService.prepare(context) == null) {
            return;
        }
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress(operation, "consent-dialog", "waiting");
        while (System.currentTimeMillis() < deadline) {
            androidx.test.uiautomator.UiObject2 button = findVpnConsentButton(device);
            if (button != null && button.isEnabled()) {
                markProgress(operation, "consent-action", "started");
                button.click();
                // A UI Automator click returns before the system has
                // persisted the VPN grant. Poll briefly, then rediscover the
                // consent button and retry until the one overall deadline.
                long grantDeadline = Math.min(deadline,
                        System.currentTimeMillis() + 1_500L);
                while (System.currentTimeMillis() < grantDeadline) {
                    if (VpnService.prepare(context) == null) {
                        markProgress(operation, "consent-action", "completed");
                        return;
                    }
                    Thread.sleep(POLL_MILLIS);
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        try {
            markConsentTimeoutDiagnostic(device);
        } catch (Throwable diagnosticFailure) {
            IllegalStateException timeoutFailure =
                    new IllegalStateException("ANDROID_VPN_CONSENT_TIMEOUT");
            timeoutFailure.addSuppressed(diagnosticFailure);
            throw timeoutFailure;
        }
        throw new IllegalStateException("ANDROID_VPN_CONSENT_TIMEOUT");
    }

    private androidx.test.uiautomator.UiObject2 findVpnConsentButton(UiDevice device) {
        // The VPN consent dialog is owned by Android, not by the Go/Fyne
        // Activity. Prefer stable system resource IDs, then the small set of
        // platform labels observed across API levels and emulator images.
        // System-owned buttons can report an unreliable accessibility
        // isClickable flag on some API levels even though UiObject2.click()
        // is the supported action; enabled plus the explicit selector is
        // sufficient.
        for (String resource : new String[]{
                "android:id/button1",
                "com.android.vpndialogs:id/button1",
                "com.android.settings:id/button1",
                "com.android.systemui:id/button1"}) {
            androidx.test.uiautomator.UiObject2 button = device.findObject(By.res(resource));
            if (button != null && button.isEnabled()) {
                return button;
            }
        }
        for (String label : new String[]{"Allow", "OK", "Start now", "Allow VPN"}) {
            androidx.test.uiautomator.UiObject2 button = device.findObject(By.text(label));
            if (button != null && button.isEnabled()) {
                return button;
            }
        }
        return null;
    }

    /**
     * Record only fixed vocabulary at the consent boundary.  This is used to
     * distinguish an absent/foreign system window from a disabled standard
     * action and a pre-bridge rendered failure without retaining a hierarchy,
     * button text, profile, or raw system output.
     */
    private void markConsentTimeoutDiagnostic(UiDevice device) throws Exception {
        consentTimeoutDiagnosed = true;
        JSONObject marker = new JSONObject();
        progressStage = "consent-diagnosis";
        progressSequence++;
        JSONObject diagnosis = new JSONObject()
                .put("foreground", foregroundCategory())
                .put("button1", consentButton1State(device))
                .put("vpn_permission", vpnPermissionState())
                .put("launch_state", NativeVpnBridge.consentLaunchStateForTest())
                .put("post_tap_state", postTapState());
        marker.put("operation", progressOperation)
                .put("stage", progressStage)
                .put("state", "observed")
                .put("sequence", progressSequence)
                .put("consent_diagnostic", diagnosis);
        RenderedScreenshot screenshot = captureRenderedScreenshot(
                progressOperation, progressStage, "observed");
        if (screenshot != null) {
            marker.put("screenshot_path", screenshot.path)
                    .put("screenshot_label", screenshot.label)
                    .put("screenshot_bytes", screenshot.bytes)
                    .put("screenshot_sha256", screenshot.sha256)
                    .put("screenshot_width", screenshot.width)
                    .put("screenshot_height", screenshot.height);
            screenshotHistory.put(new JSONObject()
                    .put("path", screenshot.path)
                    .put("label", screenshot.label)
                    .put("bytes", screenshot.bytes)
                    .put("sha256", screenshot.sha256)
                    .put("width", screenshot.width)
                    .put("height", screenshot.height));
        }
        marker.put("screenshots", new JSONArray(screenshotHistory.toString()));
        if (progressFile != null) writeJson(progressFile, marker);
    }

    private void publishScreenshotFailureMarker() throws Exception {
        if (progressFile == null) return;
        JSONObject marker = new JSONObject()
                .put("operation", progressOperation)
                .put("stage", progressStage)
                .put("state", "failed")
                .put("sequence", progressSequence)
                .put("screenshot_error_code", "ANDROID_UI_SCREENSHOT_COLLECTION_FAILED")
                .put("screenshots", new JSONArray(screenshotHistory.toString()));
        writeJson(progressFile, marker);
    }

    private String postTapState() {
        String value = progressPostTapState;
        int separator = value.indexOf('/');
        if (separator >= 0) value = value.substring(0, separator);
        switch (value) {
            case "Connecting":
            case "Connected":
            case "Error":
            case "Failed":
            case "Ready":
            case "Disconnected":
                return value;
            default:
                return "UNKNOWN";
        }
    }

    private String foregroundCategory() {
        try {
            UiAutomation automation = InstrumentationRegistry.getInstrumentation()
                    .getUiAutomation();
            AccessibilityNodeInfo root = automation.getRootInActiveWindow();
            if (root == null) return "NONE";
            CharSequence packageName = root.getPackageName();
            String packageValue = packageName == null ? "" : packageName.toString();
            if ("com.android.vpndialogs".equals(packageValue)) {
                return "VPN_DIALOG";
            }
            if (context.getPackageName().equals(packageValue)) return "PRODUCT";
            if (packageValue.isEmpty()) return "NONE";
            return "OTHER";
        } catch (Throwable ignored) {
            return "NONE";
        }
    }

    private String consentButton1State(UiDevice device) {
        try {
            UiObject2 button = device.findObject(By.res("android:id/button1"));
            if (button == null) return "ABSENT";
            return button.isEnabled() ? "ENABLED" : "DISABLED";
        } catch (Throwable ignored) {
            return "UNAVAILABLE";
        }
    }

    private String vpnPermissionState() {
        try {
            return VpnService.prepare(context) == null ? "GRANTED" : "PENDING";
        } catch (Throwable ignored) {
            return "UNAVAILABLE";
        }
    }

    private Network awaitVpnNetwork(boolean present, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        while (System.currentTimeMillis() < deadline) {
            Network vpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
            if ((vpn != null) == present) return vpn;
            Thread.sleep(POLL_MILLIS);
        }
        return findNetwork(NetworkCapabilities.TRANSPORT_VPN);
    }

    private Network findNetwork(int transport) {
        if (connectivity == null) return null;
        for (Network network : connectivity.getAllNetworks()) {
            NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
            if (capabilities != null && capabilities.hasTransport(transport)) return network;
        }
        return null;
    }

    private void runRoutingProof(
            String controlName, String identityUrl, long deadlineElapsedRealtime)
            throws Exception {
        File control = safeFile(controlName);
        deleteIfPresent(control);
        deleteIfPresent(new File(control.getPath() + ".ready"));
        Network vpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
        Network physical = awaitValidatedPhysicalNetwork();
        if (vpn == null || physical == null) throw new IllegalStateException("ANDROID_NETWORK_IDENTITY_UNAVAILABLE");
        LinkProperties physicalProperties = connectivity.getLinkProperties(physical);
        LinkProperties vpnProperties = connectivity.getLinkProperties(vpn);
        String physicalInterface = physicalProperties == null ? null : physicalProperties.getInterfaceName();
        String vpnInterface = vpnProperties == null ? null : vpnProperties.getInterfaceName();
        if (physicalInterface == null || vpnInterface == null) throw new IllegalStateException("ANDROID_NETWORK_INTERFACE_UNAVAILABLE");
        URL identity = new URL(identityUrl);
        JSONArray identityAddresses = resolveIdentityIpv4s(physical, identity.getHost());
        JSONObject providerReady = awaitProviderDefaultVpn(deadlineElapsedRealtime);
        JSONObject ready = new JSONObject()
                .put("phase", "ready")
                .put("physical_interface", physicalInterface)
                .put("physical_transport", physicalTransport(physical))
                .put("vpn_interface", vpnInterface)
                .put("provider_uid", providerReady.getInt("probe_uid"))
                .put("provider_network_binding", providerReady.getString("network_binding"))
                .put("provider_network_transport", providerReady.getString("network_transport"))
                // Resolve on the selected physical network. Resolving through
                // the process default can incorrectly follow the just-created
                // VPN route and was the source of intermittent emulator
                // UnknownHostException failures during the routing proof.
                // Keep the complete IPv4 set: api.ipify.org rotates among
                // several addresses, and blocking only one permits the
                // physical request to escape through another address.
                .put("ipv4s", identityAddresses)
                .put("port", identity.getPort() > 0 ? identity.getPort() : 443);
        writeJson(new File(control.getPath() + ".ready"), ready);
        waitForFile(control, DEFAULT_TIMEOUT_MILLIS);
        String handledPhase = "";
        while (true) {
            JSONObject request = readJson(control);
            String phase = request.optString("phase", "");
            if (phase.equals(handledPhase)) {
                Thread.sleep(POLL_MILLIS);
                continue;
            }
            handledPhase = phase;
            if ("finish".equals(phase)) {
                if (!request.optBoolean("passed", false)) {
                    throw new IllegalStateException("ANDROID_ROUTING_PROOF_FAILED");
                }
                return;
            }
            if (!"blocked".equals(phase) && !"unblocked".equals(phase)) {
                Thread.sleep(POLL_MILLIS);
                continue;
            }
            Network phaseVpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
            Network phasePhysical = findPhysicalNetwork();
            if (phaseVpn == null || phasePhysical == null) {
                throw new IllegalStateException("ANDROID_NETWORK_IDENTITY_UNAVAILABLE");
            }
            boolean directRequired = "unblocked".equals(phase);
            JSONObject response = new JSONObject().put("phase", phase)
                    .put("direct", networkRequest(
                            phasePhysical, identity.toString(), directRequired))
                    // Run the positive oracle in the ordinary test-APK
                    // provider process. Its default route is deliberately
                    // unbound, and its UID is checked against the test APK
                    // before the result crosses this process boundary.
                    .put("vpn", routingProviderRequest(identity.toString()));
            writeJson(new File(control.getPath() + ".ready"), response);
        }
    }

    private JSONObject awaitProviderDefaultVpn(long deadlineElapsedRealtime) throws Exception {
        Bundle request = new Bundle();
        request.putLong(
                GoUiRoutingProbeProvider.KEY_DEADLINE_ELAPSED_REALTIME,
                deadlineElapsedRealtime);
        Bundle response;
        try {
            ContentResolver resolver = testContext.getContentResolver();
            response = resolver.call(
                    Uri.parse("content://" + GoUiRoutingProbeProvider.AUTHORITY),
                    GoUiRoutingProbeProvider.METHOD_AWAIT_DEFAULT_VPN,
                    null,
                    request);
        } catch (SecurityException failure) {
            throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_ACCESS_DENIED", failure);
        } catch (Throwable failure) {
            throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_FAILED", failure);
        }
        if (response == null) {
            throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
        }
        try {
            int probeUid = response.getInt(GoUiRoutingProbeProvider.KEY_PROBE_UID, -1);
            int testUid = testContext.getApplicationInfo().uid;
            int targetUid = context.getApplicationInfo().uid;
            String binding = response.getString(
                    GoUiRoutingProbeProvider.KEY_NETWORK_BINDING, "");
            String transport = response.getString(
                    GoUiRoutingProbeProvider.KEY_NETWORK_TRANSPORT, "");
            if (probeUid != testUid || probeUid == targetUid || !"default".equals(binding)
                    || !("vpn".equals(transport)
                    || "non_vpn".equals(transport)
                    || "none".equals(transport))) {
                throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_IDENTITY_INVALID");
            }
            String errorCode = response.getString(GoUiRoutingProbeProvider.KEY_ERROR_CODE);
            if (errorCode != null && !errorCode.isEmpty()) {
                if (!"ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN".equals(errorCode)) {
                    throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
                }
                throw new IOException(errorCode);
            }
            if (!"vpn".equals(transport)) {
                throw new IOException("ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN");
            }
            return new JSONObject()
                    .put("probe_uid", probeUid)
                    .put("network_binding", binding)
                    .put("network_transport", transport);
        } catch (IOException failure) {
            throw failure;
        } catch (Throwable failure) {
            throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID", failure);
        }
    }

    private JSONObject routingProviderRequest(String endpoint) throws Exception {
        Bundle response;
        try {
            ContentResolver resolver = testContext.getContentResolver();
            response = resolver.call(
                    Uri.parse("content://" + GoUiRoutingProbeProvider.AUTHORITY),
                    GoUiRoutingProbeProvider.METHOD_PROBE,
                    endpoint,
                    null);
        } catch (SecurityException failure) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_ACCESS_DENIED");
        } catch (Throwable failure) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_FAILED");
        }
        if (response == null) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
        }

        try {
            int probeUid = response.getInt(GoUiRoutingProbeProvider.KEY_PROBE_UID, -1);
            int testUid = testContext.getApplicationInfo().uid;
            int targetUid = context.getApplicationInfo().uid;
            String binding = response.getString(
                    GoUiRoutingProbeProvider.KEY_NETWORK_BINDING, "");
            String transport = response.getString(
                    GoUiRoutingProbeProvider.KEY_NETWORK_TRANSPORT, "");
            if (probeUid != testUid || probeUid == targetUid || !"default".equals(binding)
                    || !("vpn".equals(transport)
                    || "non_vpn".equals(transport)
                    || "none".equals(transport))) {
                return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_IDENTITY_INVALID");
            }

            JSONObject result = new JSONObject()
                    .put("probe_uid", probeUid)
                    .put("network_binding", binding)
                    .put("network_transport", transport);
            String errorCode = response.getString(GoUiRoutingProbeProvider.KEY_ERROR_CODE);
            if (errorCode != null && !errorCode.isEmpty()) {
                if (!("ANDROID_NETWORK_REQUEST_FAILED".equals(errorCode)
                        || "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN".equals(errorCode))) {
                    return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
                }
                return result.put("error_code", errorCode);
            }
            if (!response.containsKey(GoUiRoutingProbeProvider.KEY_STATUS)
                    || !response.containsKey(GoUiRoutingProbeProvider.KEY_BODY)) {
                return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
            }
            return result
                    .put("status", response.getInt(GoUiRoutingProbeProvider.KEY_STATUS))
                    .put("body", response.getString(GoUiRoutingProbeProvider.KEY_BODY, ""));
        } catch (Throwable failure) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
        }
    }

    private JSONObject routingProviderFailure(String code) throws Exception {
        return new JSONObject().put("error_code", code);
    }

    /**
     * A transition has two host/app handshakes: first acknowledge the
     * transition request, then run the routing proof on the dedicated
     * .routing control file after the host has toggled and restored the
     * physical uplink. Keeping these files separate prevents a stale ready
     * response from being mistaken for the proof response.
     */
    private void runNetworkTransition(
            String controlName, String identityUrl, long deadlineElapsedRealtime)
            throws Exception {
        File control = safeFile(controlName);
        deleteIfPresent(control);
        deleteIfPresent(new File(control.getPath() + ".ready"));
        deleteIfPresent(new File(control.getPath() + ".routing"));
        deleteIfPresent(new File(control.getPath() + ".routing.ready"));
        JSONObject ready = new JSONObject()
                .put("operation", "network_transition")
                .put("phase", "ready");
        writeJson(new File(control.getPath() + ".ready"), ready);
        waitForFile(control, DEFAULT_TIMEOUT_MILLIS);
        JSONObject request = readJson(control);
        if (!"network_transition".equals(request.optString("operation"))) {
            throw new IllegalStateException("ANDROID_NETWORK_TRANSITION_REQUEST_INVALID");
        }
        runRoutingProof(controlName + ".routing", identityUrl, deadlineElapsedRealtime);
    }

    private Network findPhysicalNetwork() {
        if (connectivity == null) return null;
        Network fallback = null;
        for (Network network : connectivity.getAllNetworks()) {
            NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
            if (capabilities == null
                    || !capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    || !(capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                    ^ capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET))) continue;
            if (capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    && capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED)) {
                return network;
            }
            if (fallback == null) fallback = network;
        }
        return fallback;
    }

    private Network awaitValidatedPhysicalNetwork() throws Exception {
        return awaitValidatedPhysicalNetwork(NETWORK_RECOVERY_TIMEOUT_MILLIS);
    }

    private Network awaitValidatedPhysicalNetwork(long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        Network stable = null;
        int stableSamples = 0;
        while (System.currentTimeMillis() < deadline) {
            Network candidate = findPhysicalNetwork();
            NetworkCapabilities capabilities = candidate == null
                    ? null : connectivity.getNetworkCapabilities(candidate);
            boolean validated = capabilities != null
                    && capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    && capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED);
            if (validated) {
                if (candidate.equals(stable)) {
                    stableSamples++;
                } else {
                    stable = candidate;
                    stableSamples = 1;
                }
                if (stableSamples >= 2) return candidate;
            } else {
                stable = null;
                stableSamples = 0;
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_PHYSICAL_NETWORK_NOT_VALIDATED");
    }

    private String physicalTransport(Network network) {
        NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
        if (capabilities != null && capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) return "wifi";
        if (capabilities != null && capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return "ethernet";
        return "unknown";
    }

    private JSONArray resolveIdentityIpv4s(Network network, String host) throws Exception {
        InetAddress[] addresses = network.getAllByName(host);
        JSONArray result = new JSONArray();
        java.util.HashSet<String> seen = new java.util.HashSet<>();
        for (InetAddress address : addresses) {
            if (address instanceof Inet4Address
                    && seen.add(address.getHostAddress())) {
                result.put(address.getHostAddress());
            }
        }
        if (result.length() == 0) {
            throw new IOException("ANDROID_IDENTITY_IPV4_UNAVAILABLE");
        }
        return result;
    }

    private JSONObject networkRequest(Network network, String endpoint, boolean required) throws Exception {
        HttpURLConnection connection = null;
        URL url = new URL(endpoint);
        try {
            // The negative leg is always explicitly bound to the selected
            // physical Network. The positive leg uses the separate ordinary
            // test-APK provider process above.
            connection = (HttpURLConnection) network.openConnection(url);
            connection.setConnectTimeout(8_000);
            connection.setReadTimeout(8_000);
            // Keep the Android probe equivalent to the previous hosted
            // driver: no redirects, a stable request identity, and no
            // content encoding that would make the returned identity opaque.
            connection.setInstanceFollowRedirects(false);
            connection.setRequestProperty("User-Agent", "DobbyVPN-Harness/1");
            connection.setRequestProperty("Accept-Encoding", "identity");
            // Do not let HttpURLConnection reuse a connection across the
            // explicitly selected physical and VPN networks.
            connection.setRequestProperty("Connection", "close");
            int status = connection.getResponseCode();
            InputStream response = status >= 400
                    ? connection.getErrorStream() : connection.getInputStream();
            String body = response == null ? "" : readStream(response);
            return new JSONObject()
                    .put("status", status)
                    .put("body", body);
        } catch (Throwable failure) {
            JSONObject detail = networkRequestFailure();
            if (required) {
                throw new IOException("ANDROID_NETWORK_REQUEST_FAILED", failure);
            }
            return detail;
        } finally {
            if (connection != null) connection.disconnect();
        }
    }

    private JSONObject networkRequestFailure() throws Exception {
        return new JSONObject().put("error_code", "ANDROID_NETWORK_REQUEST_FAILED");
    }

    private JSONObject shellNetworkRequest(String operation, String endpoint, int value)
            throws Exception {
        ApplicationInfo probeApplication = InstrumentationRegistry.getInstrumentation()
                .getContext().getApplicationInfo();
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        String shellUid = device.executeShellCommand("su 2000 id -u").trim();
        if (!"2000".equals(shellUid)) {
            throw new IOException("ANDROID_NETWORK_PROBE_SHELL_UID_INVALID");
        }
        String probeRoot = "/data/local/tmp/dobbyvpn-probe-"
                + android.os.Process.myPid() + "-" + System.nanoTime();
        String outputPath = probeRoot + "/result.json";
        String output = "";
        try {
            // A raw APK class path exposes only its primary classes.dex to a
            // standalone app_process. The Android test companion is
            // multidex. Stream all dex members through UiAutomation's stdin
            // pipe so the shell owns the files without depending on access to
            // the package manager's private /data/app path.
            List<String> dexPaths = stageProbeDex(probeApplication, probeRoot);
            if (!"get".equals(operation) && !"upload".equals(operation)) {
                throw new IllegalArgumentException("ANDROID_NETWORK_PROBE_OPERATION_INVALID");
            }
            String encodedEndpoint = Base64.getUrlEncoder().withoutPadding()
                    .encodeToString(endpoint.getBytes("UTF-8"));
            String command = "su 2000 app_process -cp "
                    + String.join(File.pathSeparator, dexPaths)
                    + " /system/bin " + NETWORK_PROBE_CLASS.getName()
                    + " " + operation
                    + " " + encodedEndpoint
                    + " " + value
                    + " " + outputPath;
            String launchOutput = device.executeShellCommand(command).trim();
            output = device.executeShellCommand("cat " + outputPath).trim();
            if (output.isEmpty() && launchOutput.startsWith("{")) output = launchOutput;
            if (output.isEmpty()) {
                throw new IOException("ANDROID_NETWORK_PROBE_OUTPUT_INVALID");
            }
        } finally {
            device.executeShellCommand("rm -rf " + probeRoot);
        }
        JSONObject result;
        try {
            result = new JSONObject(output);
        } catch (Throwable failure) {
            throw new IOException("ANDROID_NETWORK_PROBE_OUTPUT_INVALID", failure);
        }
        int reportedUid = result.optInt("probe_uid", -1);
        if (reportedUid != 2000 || reportedUid == context.getApplicationInfo().uid
                || !"default".equals(result.optString("network_binding"))) {
            throw new IOException("ANDROID_NETWORK_PROBE_IDENTITY_INVALID");
        }
        return result;
    }

    private List<String> stageProbeDex(ApplicationInfo probeApplication, String probeRoot)
            throws Exception {
        if (Build.VERSION.SDK_INT < 34) {
            throw new IOException("ANDROID_NETWORK_PROBE_REQUIRES_API_34");
        }
        UiAutomation automation = InstrumentationRegistry.getInstrumentation().getUiAutomation();
        String mkdirOutput = automationShell(
                automation, "mkdir " + probeRoot).trim();
        if (!mkdirOutput.isEmpty()) {
            throw new IOException("ANDROID_NETWORK_PROBE_DIRECTORY_FAILED");
        }
        automationShell(automation, "chmod 0700 " + probeRoot);

        List<String> paths = new ArrayList<>();
        try (ZipFile archive = new ZipFile(probeApplication.sourceDir)) {
            Enumeration<? extends ZipEntry> entries = archive.entries();
            while (entries.hasMoreElements()) {
                ZipEntry entry = entries.nextElement();
                if (entry.isDirectory() || !entry.getName().matches("classes[0-9]*\\.dex")) {
                    continue;
                }
                String path = probeRoot + "/" + entry.getName();
                try (InputStream source = archive.getInputStream(entry)) {
                    writeShellFile(automation, path, source, entry.getSize());
                }
                paths.add(path);
            }
        }
        if (paths.isEmpty()) {
            throw new IOException("ANDROID_NETWORK_PROBE_DEX_MISSING");
        }
        paths.sort(String::compareTo);
        StringBuilder classLookup = new StringBuilder(
                "grep -a -l GoUiNetworkProbeMain");
        for (String path : paths) classLookup.append(" ").append(path);
        String classDex = automationShell(automation, classLookup.toString()).trim();
        if (classDex.isEmpty()) {
            throw new IOException("ANDROID_NETWORK_PROBE_CLASS_DEX_MISSING");
        }
        StringBuilder chmod = new StringBuilder("chmod 0444");
        for (String path : paths) chmod.append(" ").append(path);
        String chmodOutput = automationShell(automation, chmod.toString()).trim();
        if (!chmodOutput.isEmpty()) {
            throw new IOException("ANDROID_NETWORK_PROBE_PERMISSIONS_FAILED");
        }
        automationShell(automation, "chmod 0755 " + probeRoot);
        return paths;
    }

    private String automationShell(UiAutomation automation, String command) throws IOException {
        ParcelFileDescriptor descriptor = automation.executeShellCommand(command);
        return readStream(new ParcelFileDescriptor.AutoCloseInputStream(descriptor));
    }

    private void writeShellFile(
            UiAutomation automation, String path, InputStream source, long expectedBytes)
            throws Exception {
        ParcelFileDescriptor[] descriptors = automation.executeShellCommandRwe(
                "dd of=" + path + " bs=65536");
        if (descriptors == null || descriptors.length != 3) {
            throw new IOException("ANDROID_NETWORK_PROBE_PIPE_FAILED");
        }
        InputStream commandOutput = new ParcelFileDescriptor.AutoCloseInputStream(descriptors[0]);
        InputStream commandError = new ParcelFileDescriptor.AutoCloseInputStream(descriptors[2]);
        IOException writeFailure = null;
        try {
            try (OutputStream destination =
                         new ParcelFileDescriptor.AutoCloseOutputStream(descriptors[1])) {
                byte[] buffer = new byte[64 * 1024];
                int count;
                while ((count = source.read(buffer)) >= 0) {
                    destination.write(buffer, 0, count);
                }
            }
        } catch (IOException failure) {
            writeFailure = failure;
        }
        readStream(commandOutput);
        readStream(commandError);
        if (writeFailure != null) {
            throw new IOException("ANDROID_NETWORK_PROBE_STAGE_FAILED", writeFailure);
        }
        String stagedBytes = automationShell(automation, "stat -c %s " + path).trim();
        if (!Long.toString(expectedBytes).equals(stagedBytes)) {
            throw new IOException("ANDROID_NETWORK_PROBE_STAGE_SIZE_INVALID");
        }
    }

    private JSONObject requiredShellNetworkRequest(String operation, String endpoint, int value)
            throws Exception {
        JSONObject result = shellNetworkRequest(operation, endpoint, value);
        if (result.has("error_code")) {
            throw new IOException("ANDROID_NETWORK_PROBE_FAILED");
        }
        return result;
    }

    private void measureStability(String endpoint, long timeout) throws Exception {
        // The routing proof immediately before this operation already proves
        // real VPN traffic through the tunnel.  Metrics intentionally use the
        // shell-UID probe (whose default route follows that VPN); repeating a
        // ConnectivityManager VPN lookup here is a stale duplicate gate and
        // can race the just-completed routing handshake on the emulator.
        long deadline = System.currentTimeMillis() + timeout;
        for (int i = 0; i < STABILITY_SAMPLES; i++) {
            JSONObject sample = requiredShellNetworkRequest("get", endpoint, 0);
            int status = sample.optInt("status", 0);
            if (status < 200 || status >= 300) {
                // A probe response is only a successful stability sample when
                // the HTTPS endpoint returned a 2xx response.  The shell
                // helper already rejects transport/probe failures; keep this
                // separate fixed code for an HTTP rejection without copying
                // response text or endpoint details into the test result.
                throw new IllegalStateException("ANDROID_STABILITY_HTTP_STATUS");
            }
            if (i + 1 < STABILITY_SAMPLES) Thread.sleep(1_000L);
            if (System.currentTimeMillis() > deadline) throw new IllegalStateException("ANDROID_STABILITY_TIMEOUT");
        }
    }

    private JSONObject measureThroughput(String download, String upload, long timeout) throws Exception {
        // See measureStability(): the preceding routing proof is the
        // authoritative tunnel/traffic check, while these shell-UID probes
        // provide the metric observations without a second stale lookup.
        JSONObject downloadResult = requiredShellNetworkRequest("get", download, 0);
        int downloadStatus = downloadResult.optInt("status", 0);
        long downloadBytes = downloadResult.optLong("body_bytes", 0L);
        double downloadSeconds = Math.max(0.001,
                downloadResult.optDouble("elapsed_ms", 0.0) / 1_000.0);
        if (downloadStatus < 200 || downloadStatus >= 300 || downloadBytes <= 0) {
            throw new IOException("ANDROID_DOWNLOAD_INVALID");
        }
        double downloadMbps = downloadBytes * 8.0 / downloadSeconds / 1_000_000.0;
        JSONObject uploadResult = requiredShellNetworkRequest(
                "upload", upload, 64 * 1024);
        int status = uploadResult.optInt("status", 0);
        if (status < 200 || status >= 300) throw new IOException("ANDROID_UPLOAD_STATUS");
        double uploadSeconds = Math.max(0.001,
                uploadResult.optDouble("elapsed_ms", 0.0) / 1_000.0);
        double uploadMbps = 64 * 1024 * 8.0 / uploadSeconds / 1_000_000.0;
        return new JSONObject().put("latency_ms", downloadSeconds * 1000.0)
                .put("download_mbps", downloadMbps).put("upload_mbps", uploadMbps);
    }

    private long operationTimeout(JSONObject operation) {
        return Math.max(1, operation.optLong("timeout_seconds", 60L)) * 1000L;
    }

    private void requireConnected(boolean connected) {
        if (!connected) throw new IllegalStateException("ANDROID_OPERATION_REQUIRES_CONNECTION");
    }

    private JSONObject requireOK(String encoded) throws Exception {
        JSONObject value = new JSONObject(encoded);
        if (!value.optBoolean("ok", false)) {
            String code = value.optJSONObject("error") == null
                    ? "ANDROID_GO_OPERATION_FAILED"
                    : value.getJSONObject("error").optString(
                            "code", "ANDROID_GO_OPERATION_FAILED");
            throw new IllegalStateException(fixedFailureCode(code));
        }
        return value;
    }

    private File safeFile(String name) throws IOException {
        if (name.contains("/") || name.contains("\\") || name.contains("..")) throw new IOException("ANDROID_FILE_NAME_INVALID");
        return new File(context.getFilesDir(), name);
    }

    private JSONObject readJson(File file) throws Exception { return new JSONObject(new String(readBytes(file), "UTF-8")); }

    private byte[] readBytes(File file) throws IOException {
        try (InputStream input = new FileInputStream(file); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[8192];
            int count;
            while ((count = input.read(buffer)) >= 0) output.write(buffer, 0, count);
            return output.toByteArray();
        }
    }

    private String readStream(InputStream input) throws IOException {
        try (InputStream source = input; ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[8192];
            int count;
            while ((count = source.read(buffer)) >= 0) output.write(buffer, 0, count);
            return output.toString("UTF-8");
        }
    }

    private void waitForFile(File file, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        while (!file.isFile() && System.currentTimeMillis() < deadline) Thread.sleep(POLL_MILLIS);
        if (!file.isFile()) throw new IOException("ANDROID_CONTROL_TIMEOUT");
    }

    private void writeJson(File file, JSONObject value) throws IOException {
        File temporary = new File(file.getPath() + ".tmp");
        try (FileOutputStream output = new FileOutputStream(temporary, false)) {
            output.write(value.toString().getBytes("UTF-8"));
            output.getFD().sync();
        }
        if (!temporary.renameTo(file)) {
            throw new IOException("ANDROID_ATOMIC_WRITE_FAILED");
        }
    }

    /** Publish one redacted UI phase and its required rendered artifact. */
    private void markProgress(String operation, String stage, String state) throws Exception {
        progressOperation = operation;
        progressStage = stage;
        progressSequence++;
        if (progressObservation != null) {
            progressObservation.put("ui_operation", operation);
            progressObservation.put("ui_phase", stage);
            progressObservation.put("ui_phase_state", state);
        }
        if (progressFile == null) return;
        RenderedScreenshot screenshot = captureRenderedScreenshot(operation, stage, state);
        JSONObject marker = new JSONObject()
                .put("operation", operation)
                .put("stage", stage)
                .put("state", state)
                .put("sequence", progressSequence);
        if (!progressPostTapState.isEmpty()) {
            marker.put("post_tap_state", progressPostTapState);
        }
        if (screenshot != null) {
            marker.put("screenshot_path", screenshot.path)
                    .put("screenshot_label", screenshot.label)
                    .put("screenshot_bytes", screenshot.bytes)
                    .put("screenshot_sha256", screenshot.sha256)
                    .put("screenshot_width", screenshot.width)
                    .put("screenshot_height", screenshot.height);
        }
        if (screenshot != null) {
            screenshotHistory.put(new JSONObject()
                    .put("path", screenshot.path)
                    .put("label", screenshot.label)
                    .put("bytes", screenshot.bytes)
                    .put("sha256", screenshot.sha256)
                    .put("width", screenshot.width)
                    .put("height", screenshot.height));
        }
        marker.put("screenshots", new JSONArray(screenshotHistory.toString()));
        writeJson(progressFile, marker);
    }

    /**
     * Capture selected rendered milestones as required, redacted PNGs. The
     * profile/editor and diagnostics regions are painted black before the
     * frame leaves the instrumentation cache. A screenshot remains an extra
     * artifact and never replaces the complete instrumentation streams or
     * observation JSON.
     */
    private RenderedScreenshot captureRenderedScreenshot(
            String operation, String stage, String state) throws Exception {
        if (!("completed".equals(state) || "observed".equals(state) || "failed".equals(state))) {
            return null;
        }
        if (!("failed".equals(state)
                || "surface".equals(stage)
                || stage.startsWith("post-tap-")
                || stage.endsWith("-state")
                || "consent-diagnosis".equals(stage))) {
            return null;
        }
        Bitmap sourceBitmap = null;
        Bitmap bitmap = null;
        File output = null;
        try {
            ensureNativeInputDismissedForScreenshot();
            List<Rect> masks = new ArrayList<>();
            for (String label : new String[]{
                    "Connection configuration", "Connection logs", "Connection details"}) {
                // These panels are sensitive when visible, but the rendered
                // status screen after Connect intentionally has none of
                // them. A missing panel therefore means there is nothing to
                // redact on this frame, not a failed screenshot.
                Rect bounds = stableRenderedBoundsOrNull(label);
                if (bounds != null) masks.add(bounds);
            }
            sourceBitmap = InstrumentationRegistry.getInstrumentation()
                    .getUiAutomation().takeScreenshot();
            if (sourceBitmap == null
                    || sourceBitmap.getWidth() <= 0 || sourceBitmap.getHeight() <= 0) {
                throw new IllegalStateException("ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY");
            }
            // UiAutomation.takeScreenshot() returns an immutable bitmap on
            // current Android images. Copy it before applying redaction.
            bitmap = sourceBitmap.copy(Bitmap.Config.ARGB_8888, true);
            if (bitmap == null) {
                throw new IllegalStateException("ANDROID_UI_SCREENSHOT_COPY_FAILED");
            }
            sourceBitmap.recycle();
            sourceBitmap = null;
            Canvas canvas = new Canvas(bitmap);
            Paint paint = new Paint();
            paint.setColor(Color.BLACK);
            paint.setStyle(Paint.Style.FILL);
            for (Rect mask : masks) {
                Rect clipped = new Rect(0, 0, bitmap.getWidth(), bitmap.getHeight());
                if (!clipped.intersect(mask) || clipped.isEmpty()) {
                    throw new IllegalStateException(
                            "ANDROID_UI_SCREENSHOT_MASK_OUTSIDE_FRAME");
                }
                canvas.drawRect(clipped, paint);
            }
            if (!screenshotDirectory.exists() && !screenshotDirectory.mkdirs()) {
                throw new IOException("ANDROID_UI_SCREENSHOT_DIRECTORY_FAILED");
            }
            String safeOperation = operation.replaceAll("[^A-Za-z0-9_-]", "_");
            String safeStage = stage.replaceAll("[^A-Za-z0-9_-]", "_");
            output = new File(screenshotDirectory,
                    String.format(Locale.ROOT, "%04d-%s-%s.png",
                            progressSequence, safeOperation, safeStage));
            if (output.exists()) {
                throw new IOException("ANDROID_UI_SCREENSHOT_DUPLICATE_LABEL:" + output.getName());
            }
            try (FileOutputStream stream = new FileOutputStream(output, false)) {
                if (!bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    throw new IOException("ANDROID_UI_SCREENSHOT_PNG_ENCODE_FAILED");
                }
            }
            if (!output.isFile() || output.length() <= 8L) {
                throw new IOException("ANDROID_UI_SCREENSHOT_PNG_INVALID");
            }
            BitmapFactory.Options options = new BitmapFactory.Options();
            options.inJustDecodeBounds = true;
            BitmapFactory.decodeFile(output.getAbsolutePath(), options);
            if (options.outWidth != bitmap.getWidth() || options.outHeight != bitmap.getHeight()) {
                throw new IOException("ANDROID_UI_SCREENSHOT_DIMENSIONS_INVALID");
            }
            return new RenderedScreenshot(
                    output.getAbsolutePath(), output.getName(), output.length(),
                    sha256(output), options.outWidth, options.outHeight);
        } catch (Exception failure) {
            if (output != null && output.exists() && !output.delete()) {
                failure.addSuppressed(new IOException(
                        "ANDROID_UI_SCREENSHOT_CLEANUP_FAILED:" + output.getName()));
            }
            throw failure;
        } catch (Error failure) {
            if (output != null && output.exists() && !output.delete()) {
                failure.addSuppressed(new IOException(
                        "ANDROID_UI_SCREENSHOT_CLEANUP_FAILED:" + output.getName()));
            }
            throw failure;
        } finally {
            if (bitmap != null) bitmap.recycle();
            if (sourceBitmap != null) sourceBitmap.recycle();
        }
    }

    private static final class RenderedScreenshot {
        final String path;
        final String label;
        final long bytes;
        final String sha256;
        final int width;
        final int height;

        RenderedScreenshot(String path, String label, long bytes, String sha256,
                           int width, int height) {
            this.path = path;
            this.label = label;
            this.bytes = bytes;
            this.sha256 = sha256;
            this.width = width;
            this.height = height;
        }
    }

    private String sha256(File file) throws Exception {
        MessageDigest digest = MessageDigest.getInstance("SHA-256");
        try (InputStream input = new FileInputStream(file)) {
            byte[] buffer = new byte[8192];
            int count;
            while ((count = input.read(buffer)) >= 0) {
                if (count > 0) digest.update(buffer, 0, count);
            }
        }
        byte[] value = digest.digest();
        StringBuilder encoded = new StringBuilder(value.length * 2);
        for (byte item : value) {
            encoded.append(String.format(Locale.ROOT, "%02x", item & 0xff));
        }
        return encoded.toString();
    }

    private UiObject2 findRenderedObject(String label) {
        UiDevice device = uiDevice();
        UiObject2 object = device.findObject(By.text(label).pkg(context.getPackageName()));
        if (object == null) {
            object = device.findObject(By.desc(label).pkg(context.getPackageName()));
        }
        return object;
    }

    private Rect stableRenderedBounds(String label) throws Exception {
        long deadline = System.currentTimeMillis() + 3_000L;
        Rect previous = null;
        int stableSamples = 0;
        while (System.currentTimeMillis() < deadline) {
            UiObject2 object = findRenderedObject(label);
            Rect current = object == null ? null : object.getVisibleBounds();
            if (current != null && !current.isEmpty()) {
                if (current.equals(previous)) {
                    stableSamples++;
                    if (stableSamples >= UI_STABILITY_SAMPLES) {
                        return new Rect(current);
                    }
                } else {
                    previous = new Rect(current);
                    stableSamples = 0;
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_SCREENSHOT_MASK_MISSING:" + label);
    }

    private Rect stableRenderedBoundsOrNull(String label) throws Exception {
        try {
            return stableRenderedBounds(label);
        } catch (IllegalStateException error) {
            if (error.getMessage() != null
                    && error.getMessage().startsWith("ANDROID_UI_SCREENSHOT_MASK_MISSING:")) {
                return null;
            }
            throw error;
        }
    }

    /** A required frame must prove the profile editor is no longer visible. */
    private void ensureNativeInputDismissedForScreenshot() throws Exception {
        UiDevice device = uiDevice();
        if (device.findObject(By.clazz("android.widget.EditText")
                .pkg(context.getPackageName())) == null) {
            return;
        }
        hideNativeInput(3_000L);
        if (device.findObject(By.clazz("android.widget.EditText")
                .pkg(context.getPackageName())) != null) {
            throw new IllegalStateException(
                    "ANDROID_UI_SCREENSHOT_UNAVAILABLE_EDITOR_VISIBLE");
        }
    }

    private void deleteIfPresent(File file) {
        if (file.exists() && !file.delete()) {
            throw new IllegalStateException("ANDROID_CONTROL_STALE_FILE");
        }
    }

    private boolean deleteScreenshotTree(File file) {
        if (file.isDirectory()) {
            File[] children = file.listFiles();
            if (children == null) return false;
            for (File child : children) {
                if (!deleteScreenshotTree(child)) return false;
            }
        }
        return !file.exists() || file.delete();
    }

}
