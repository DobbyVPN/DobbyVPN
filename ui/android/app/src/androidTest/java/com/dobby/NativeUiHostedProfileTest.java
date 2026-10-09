package com.dobby;

import android.app.Activity;
import android.app.Instrumentation;
import android.app.UiAutomation;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.ContentResolver;
import android.graphics.Rect;
import android.content.pm.ApplicationInfo;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.Uri;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.os.SystemClock;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.accessibility.AccessibilityNodeInfo;
import android.view.accessibility.AccessibilityWindowInfo;

import androidx.compose.ui.platform.ViewRootForTest;
import androidx.compose.ui.semantics.SemanticsNode;
import androidx.compose.ui.semantics.SemanticsProperties;
import androidx.compose.ui.semantics.SemanticsPropertyKey;
import androidx.compose.ui.text.AnnotatedString;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.runner.lifecycle.ActivityLifecycleMonitor;
import androidx.test.runner.lifecycle.ActivityLifecycleMonitorRegistry;
import androidx.test.runner.lifecycle.Stage;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.Configurator;
import androidx.test.uiautomator.StaleObjectException;
import androidx.test.uiautomator.UiDevice;
import androidx.test.uiautomator.UiObject2;

import com.dobby.nativebridge.NativeGoSession;
import com.dobby.nativebridge.NativeRecoveryStopTestSeam;
import com.dobby.nativebridge.DobbyVpnService;
import com.dobby.nativebridge.NativeVpnBridge;
import com.dobby.ui.MainActivity;

import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.security.KeyStore;
import java.security.MessageDigest;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.net.HttpURLConnection;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Enumeration;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicReference;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;
import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.TrustManagerFactory;

/**
 * Android's functional seam for the Compose frontend and shared Go backend.
 *
 * The external Torturer runner supplies only an ordered command file and an
 * opaque profile. The protocol-matrix lane drives the native Go binding for
 * per-profile coverage; the gui-auto lane drives the production Compose
 * controls with Android's native input/accessibility path. Both lanes keep
 * Kotlin/Java responsible only for Android permission, Network/VpnService
 * observations, and disposable test files.
 */
@RunWith(AndroidJUnit4.class)
public final class NativeUiHostedProfileTest {
    private static final String COMMAND_ARGUMENT = "dobby.hosted_command_file";
    private static final String REAL_PROFILE_ARGUMENT = "dobby.real_profile";
    private static final String GUI_AUTO_MODE = "gui-auto";
    private static final String BINDING_MODE = "protocol-matrix";
    private static final String GUI_AUTO_PROTOCOL = "AUTO";
    private static final String CONNECTION_ACTION_LABEL = "VPN connection action";
    private static final String START_MODE_PROFILE_INDEX = "PROFILE_INDEX";
    private static final String CONSENT_GRANT_SELECTION_OPERATION = "consent_grant_selection";
    private static final String AUTO_RECOVERY_STOP_TEST_CASE = "android:auto-recovery-stop";
    private static final String AUTO_RECOVERY_STOP_HEALTH_FAULT =
            "test recovery Stop health fault";
    private static final String AUTO_RECOVERY_STOP_FAILURE_MESSAGE =
            "connected health check failed: " + AUTO_RECOVERY_STOP_HEALTH_FAULT;
    private static final String AUTO_RECOVERY_STOP_FAILURE_CODE = "RUNTIME_FAILED";
    private static final Class<AndroidNetworkProbeMain> NETWORK_PROBE_CLASS =
            AndroidNetworkProbeMain.class;
    private static final long POLL_MILLIS = 100L;
    private static final long ERROR_CATEGORY_TIMEOUT_MILLIS = 2_000L;
    private static final long DEFAULT_TIMEOUT_MILLIS = 60_000L;
    private static final long NETWORK_RECOVERY_TIMEOUT_MILLIS = 10_000L;
    private static final long ACTIVITY_RESUME_TIMEOUT_MILLIS = 15_000L;
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
            "ANDROID_NATIVE_CASE_UNSUPPORTED",
            "ANDROID_TEST_RECOVERY_STOP_CASE_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_REQUIRES_GUI_AUTO",
            "ANDROID_TEST_RECOVERY_STOP_JNI_UNAVAILABLE",
            "ANDROID_TEST_RECOVERY_STOP_ENABLE_FAILED",
            "ANDROID_TEST_RECOVERY_STOP_ARM_FAILED",
            "ANDROID_TEST_RECOVERY_STOP_CONSENT_NOT_FRESH",
            "ANDROID_TEST_RECOVERY_STOP_INITIAL_STATE_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_PROFILE_INVENTORY_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_ARM_PRECONDITION_FAILED",
            "ANDROID_TEST_RECOVERY_STOP_RECOVERY_TIMEOUT",
            "ANDROID_TEST_RECOVERY_STOP_RECOVERY_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_UI_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_ENABLED",
            "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_UNAVAILABLE",
            "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_NOT_RESTORED",
            "ANDROID_TEST_RECOVERY_STOP_RENDERED_STATUS_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_RENDERED_FAILURE_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_RENDER_TIMEOUT",
            "ANDROID_TEST_RECOVERY_STOP_SCREENSHOT_MISSING",
            "ANDROID_TEST_RECOVERY_STOP_TAP_FAILED",
            "ANDROID_TEST_RECOVERY_STOP_CLEANUP_TIMEOUT",
            "ANDROID_TEST_RECOVERY_STOP_FINAL_STATE_INVALID",
            "ANDROID_TEST_RECOVERY_STOP_GENERATION_ADVANCED",
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
            "ANDROID_UI_ACCEPTED_SOURCE_NOT_VISIBLE",
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
            "ANDROID_VPN_CONSENT_NOT_FRESH_AFTER_APK_REINSTALL",
            "ANDROID_VPN_CONSENT_NOT_FRESH_BEFORE_DENY_STALE_CASE",
            "ANDROID_VPN_CONSENT_NONDEFAULT_PROFILE_UNAVAILABLE",
            "ANDROID_VPN_CONSENT_SOURCE_CHANGED",
            "ANDROID_VPN_CONSENT_DIGEST_CHANGED",
            "ANDROID_VPN_CONSENT_PROFILE_IDENTITY_MISMATCH",
            "ANDROID_VPN_CONSENT_SELECTION_MODE_MISMATCH",
            "ANDROID_VPN_CONSENT_GENERATION_INVALID",
            "ANDROID_VPN_CONSENT_TUNNEL_MISSING",
            "ANDROID_VPN_CONSENT_DISCONNECT_FAILED",
            "ANDROID_NETWORK_IDENTITY_UNAVAILABLE",
            "ANDROID_NETWORK_INTERFACE_UNAVAILABLE",
            "ANDROID_VPN_DNS_UNSUPPORTED_ADDRESS_FAMILY",
            "ANDROID_ROUTING_PROOF_FAILED",
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
    private final Set<Network> observedNetworks = ConcurrentHashMap.newKeySet();
    private ConnectivityManager.NetworkCallback networkCallback;
    private Activity foregroundActivity;
    private File progressFile;
    private JSONObject progressObservation;
    private String progressOperation = "command";
    private String progressStage = "start";
    private String progressPostTapState = "";
    private long progressSequence;
    private boolean consentTimeoutDiagnosed;
    private final JSONArray screenshotHistory = new JSONArray();
    private final JSONArray commandOutputDiagnostics = new JSONArray();
    private final JSONArray consentDiagnosticFailures = new JSONArray();
    private String expectedRenderedSource = "";
    private String launchSubscriptionURL = "";
    private String subscriptionControlURL = "";
    private String subscriptionControlKey = "";
    private String subscriptionControlCAPem = "";
    private SSLSocketFactory subscriptionControlSocketFactory;
    private int savedSourceInitialGets = -1;
    private boolean savedSourceRestoreStarted;
    private boolean savedSourceRestoreAttempted;
    private boolean processColdImportPending;
    private int processColdImportInitialGets = -1;
    private final File screenshotDirectory = new File(
            // Instrumentation executes in the target application's UID. The
            // instrumentation APK's files directory is a different sandbox and
            // is not writable from this process. The target app's files survive
            // cache eviction until the host pulls the screenshots.
            context.getFilesDir(), "dobbyvpn-rendered-screenshots");

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
        while (commandOutputDiagnostics.length() > 0) commandOutputDiagnostics.remove(0);
        while (consentDiagnosticFailures.length() > 0) consentDiagnosticFailures.remove(0);
        observedNetworks.clear();
        if (connectivity != null) {
            NetworkRequest request = new NetworkRequest.Builder()
                    .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    .build();
            ConnectivityManager.NetworkCallback callback =
                    new ConnectivityManager.NetworkCallback() {
                        @Override
                        public void onAvailable(Network network) {
                            observedNetworks.add(network);
                        }

                        @Override
                        public void onLost(Network network) {
                            observedNetworks.remove(network);
                        }
                    };
            connectivity.registerNetworkCallback(request, callback);
            networkCallback = callback;
        }
    }

    @After
    public void unregisterObservedNetworks() {
        ConnectivityManager.NetworkCallback callback = networkCallback;
        networkCallback = null;
        if (callback == null) {
            observedNetworks.clear();
            return;
        }
        try {
            connectivity.unregisterNetworkCallback(callback);
        } catch (RuntimeException failure) {
            throw new IllegalStateException("ANDROID_NETWORK_CALLBACK_CLEANUP_FAILED", failure);
        } finally {
            observedNetworks.clear();
        }
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
        File profileFile = command.has("profile_file") ? safeFile(command.getString("profile_file")) : null;
        File outputFile = safeFile(command.getString("output_file"));
        JSONObject observation = baseObservation(command);
        String progressName = command.optString("progress_file", "");
        progressFile = progressName.isEmpty() ? null : safeFile(progressName);
        progressObservation = observation;
        markProgress("command", "start", "started");
        boolean guiAuto = GUI_AUTO_MODE.equals(command.optString(
                "ui_mode", command.optString("coverage_lane", "")));
        String testCase = command.optString("test_case", "");
        boolean autoRecoveryStopCase = AUTO_RECOVERY_STOP_TEST_CASE.equals(testCase);
        boolean savedSourceRestoreOnly = command.optBoolean("saved_source_restore_only", false);
        boolean savedSourceRestorePreverified = command.optBoolean(
                "saved_source_restore_preverified", false);
        launchSubscriptionURL = guiAuto ? command.getString("subscription_url") : "";
        subscriptionControlURL = guiAuto ? command.getString("subscription_control_url") : "";
        subscriptionControlKey = guiAuto ? command.getString("subscription_control_key") : "";
        subscriptionControlCAPem = guiAuto ? command.getString("subscription_control_ca_pem") : "";
        processColdImportPending = guiAuto && command.optBoolean("process_cold_import", false);
        processColdImportInitialGets = command.optInt("process_cold_import_request_count", -1);
        if (guiAuto && (subscriptionControlURL.isEmpty() || subscriptionControlKey.isEmpty()
                || subscriptionControlCAPem.isEmpty())) {
            throw new IllegalArgumentException("ANDROID_SUBSCRIPTION_CONTROL_MISSING");
        }
        if ((savedSourceRestoreOnly || savedSourceRestorePreverified)
                && (!guiAuto || !testCase.isEmpty()
                || savedSourceRestoreOnly && savedSourceRestorePreverified)) {
            throw new IllegalArgumentException("ANDROID_SAVED_SOURCE_RESTORE_PHASE_INVALID");
        }
        subscriptionControlSocketFactory = guiAuto
                ? createSubscriptionControlSocketFactory(subscriptionControlCAPem) : null;
        if (guiAuto && command.has("profile_index")) {
            throw new IllegalArgumentException("ANDROID_GUI_AUTO_PROFILE_INDEX_FORBIDDEN");
        }
        String sessionID = "";
        long sequence = 0L;
        long generation = 0L;
        boolean configured = false;
        boolean connected = false;
        boolean disconnectClean = false;
        long autoRecoveryInitialGeneration = 0L;

        try {
            if (!testCase.isEmpty() && !autoRecoveryStopCase) {
                throw new IllegalArgumentException("ANDROID_TEST_RECOVERY_STOP_CASE_INVALID");
            }
            if (autoRecoveryStopCase && !guiAuto) {
                throw new IllegalArgumentException("ANDROID_TEST_RECOVERY_STOP_REQUIRES_GUI_AUTO");
            }
            if (autoRecoveryStopCase) observation.put("test_case", testCase);
            if (!guiAuto) {
                NativeGoSession.attach(context);
                JSONObject initial = snapshotResult("");
                sessionID = initial.getString("session_id");
                sequence = initial.getLong("sequence");
            }
            byte[] profile = guiAuto ? null : readBytes(profileFile);
            JSONArray operations = command.getJSONArray("operations");
            for (int i = 0; i < operations.length(); i++) {
                JSONObject operation = operations.getJSONObject(i);
                String name = operation.getString("operation");
                String operationID = operation.getString("id");
                markProgress(name, "start", "started");
                switch (name) {
                    case "configure": {
                        if (guiAuto) {
                            configureThroughRenderedUI(
                                    command.getString("subscription_url"),
                                    operationTimeout(operation),
                                    autoRecoveryStopCase || savedSourceRestorePreverified);
                            // The one-step configure scenario must prove that
                            // the rendered profile was accepted by Go.  A
                            // later connect/reconnect owns that visible start
                            // action, so do not consume it here and double
                            // connect the same scenario.
                            boolean startsLater = hasFollowingConnectionStart(
                                    operations, i + 1);
                            boolean consentHandled = false;
                            if (!startsLater && !savedSourceRestoreOnly) {
                                consentHandled = verifyManualConsent(operationTimeout(operation));
                                consentHandled |= connectThroughRenderedUI(operationTimeout(operation));
                                assertRenderedSourceRetained(2_000L);
                                boolean nativeShutdownVerified = verifySubscriptionControls(
                                        expectedRenderedSource,
                                        operationTimeout(operation));
                                observation.put("native_os_shutdown_cancels_pending_switch",
                                        nativeShutdownVerified);
                                ensureRenderedDisconnected(
                                        operationTimeout(operation));
                                boolean noVpn = awaitVpnNetwork(
                                        false,
                                        NETWORK_RECOVERY_TIMEOUT_MILLIS) == null;
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
                                    operationTimeout(operation),
                                    "connect",
                                    autoRecoveryStopCase);
                            assertRenderedSourceRetained(2_000L);
                            if (autoRecoveryStopCase) {
                                if (!consentHandled) {
                                    throw new IllegalStateException(
                                            "ANDROID_TEST_RECOVERY_STOP_CONSENT_NOT_FRESH");
                                }
                                JSONObject initial = snapshotResult("");
                                JSONArray profiles = initial.optJSONArray("profiles");
                                if (!"CONNECTED".equals(initial.optString("state"))
                                        || initial.optBoolean("recovering")
                                        || !"AUTO_SELECT".equals(
                                                initial.optString("active_mode"))
                                        || initial.optLong("generation") <= 0) {
                                    throw new IllegalStateException(
                                            "ANDROID_TEST_RECOVERY_STOP_INITIAL_STATE_INVALID");
                                }
                                if (profiles == null || profiles.length() < 2) {
                                    throw new IllegalStateException(
                                            "ANDROID_TEST_RECOVERY_STOP_PROFILE_INVENTORY_INVALID");
                                }
                                autoRecoveryInitialGeneration = initial.getLong("generation");
                            }
                            connected = true;
                            observation.put("connected", true);
                            observation.put("gui_auto_verified", true);
                            observation.put("vpn_consent_handled", consentHandled);
                        } else {
                            ensureVpnReady();
                            int index = command.has("profile_index")
                                    ? command.getInt("profile_index") : 0;
                            JSONObject started = requireOK(NativeGoSession.start(
                                    sessionID, sequence, START_MODE_PROFILE_INDEX, index, null));
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
                    case CONSENT_GRANT_SELECTION_OPERATION: {
                        if (!guiAuto) {
                            throw new IllegalStateException("ANDROID_CONSENT_N11_REQUIRES_GUI_AUTO");
                        }
                        if (!configured) {
                            throw new IllegalStateException("ANDROID_CONNECT_BEFORE_CONFIGURE");
                        }
                        JSONObject selection = verifyUnchangedConsentProfileSelection(
                                operationTimeout(operation), name);
                        observation.put("consent_grant_selection", selection);
                        observation.put("connected", true);
                        observation.put("disconnect_clean", selection.getBoolean("disconnect_clean"));
                        observation.put("final_disconnect_clean", selection.getBoolean("disconnect_clean"));
                        observation.put("cleanup_verified", selection.getBoolean("disconnect_clean"));
                        observation.put("gui_auto_verified", true);
                        observation.put("vpn_consent_handled", true);
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
                    case "disconnect": {
                        if (guiAuto) {
                            if (autoRecoveryStopCase) {
                                JSONObject nativeCaseFacts = runAutoRecoveryStop(
                                        operationTimeout(operation),
                                        autoRecoveryInitialGeneration,
                                        observation);
                                observation.put("native_case_facts", nativeCaseFacts);
                                disconnectClean = nativeCaseFacts.getBoolean("final_idle")
                                        && nativeCaseFacts.getBoolean("vpn_network_absent");
                            } else {
                                disconnectThroughRenderedUI(operationTimeout(operation));
                                boolean vpnRemoved = awaitVpnNetwork(
                                        false, operationTimeout(operation)) == null;
                                disconnectClean = vpnRemoved;
                            }
                            connected = false;
                            observation.put("disconnect_clean", disconnectClean);
                            observation.put("final_disconnect_clean", disconnectClean);
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
                                    sessionID, sequence, START_MODE_PROFILE_INDEX, index, null));
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
            if (commandOutputDiagnostics.length() > 0) {
                observation.put("command_output", commandOutputDiagnostics);
            }
            if (consentDiagnosticFailures.length() > 0) {
                observation.put("consent_diagnostic_errors", consentDiagnosticFailures);
            }
            String cleanupError = null;
            JSONArray cleanupDetails = new JSONArray();
            try {
                deleteIfPresent(commandFile);
            } catch (Throwable failure) {
                cleanupError = "ANDROID_COMMAND_FILE_CLEANUP_FAILED";
                cleanupDetails.put(cleanupError + "\n"
                        + CompleteThrowableReporter.format(failure));
            }
            try {
                if (profileFile != null) deleteIfPresent(profileFile);
            } catch (Throwable failure) {
                cleanupError = cleanupError == null
                        ? "ANDROID_PROFILE_FILE_CLEANUP_FAILED"
                        : cleanupError + ",ANDROID_PROFILE_FILE_CLEANUP_FAILED";
                cleanupDetails.put("ANDROID_PROFILE_FILE_CLEANUP_FAILED\n"
                        + CompleteThrowableReporter.format(failure));
            }
            if (cleanupError != null) {
                observation.put("cleanup_error", cleanupError);
                observation.put("cleanup_error_detail", cleanupDetails);
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
        output.put("native_os_shutdown_cancels_pending_switch", false);
        output.put("ui_reopen_verified", false);
        output.put("vpn_consent_handled", false);
        output.put("configured", false);
        output.put("connected", false);
        output.put("tunnel_interface", false);
        output.put("routing_verified", false);
        output.put("stability_verified", false);
        output.put("stability_sample_count", STABILITY_SAMPLES);
        output.put("stability_sample_interval_seconds", 1.0);
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
     * Enter the subscription URL through the production Compose text field.
     * UiAutomator supplies the text through the visible control; Connect
     * still calls the production Go binding.
     */
    private void configureThroughRenderedUI(String subscriptionURL, long timeout) throws Exception {
        configureThroughRenderedUI(subscriptionURL, timeout, false);
    }

    private void configureThroughRenderedUI(
            String subscriptionURL, long timeout, boolean skipColdSavedSourceRestore)
            throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress("configure", "surface", "started");
        boolean processColdImportVerified = processColdImportPending;
        if (processColdImportPending) {
            verifyProcessColdImport(subscriptionURL,
                    remainingTimeout(deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT"));
            processColdImportPending = false;
        }
        int recoveryImportInitialGets = skipColdSavedSourceRestore && !processColdImportVerified
                ? subscriptionFixtureState().getInt("subscription_gets") : -1;
        int expectedNavigationGets = -1;
        // Keep the restore GET distinct so it cannot satisfy the separate
        // successful cold deep-link import assertion below.
        String restoredURL = urlWithQuery(subscriptionURL, "android-saved-source", "1");
        if (!skipColdSavedSourceRestore && !savedSourceRestoreAttempted) {
            launchActivityColdSavedSourceRestore(restoredURL,
                    remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        }
        String savedLaunchURL = launchSubscriptionURL;
        if (savedSourceRestoreStarted) launchSubscriptionURL = "";
        try {
            ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
            foregroundActivity = ensureForegroundActivity();
        } finally {
            launchSubscriptionURL = savedLaunchURL;
        }
        markProgress("configure", "surface", "completed");
        if (savedSourceRestoreStarted) {
            expectedRenderedSource = restoredURL;
            verifySavedURLRestoration(restoredURL,
                    remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
            JSONObject restored = snapshotResult("");
            if (!"CONFIGURED".equals(restored.getString("state"))
                    || !restoredURL.equals(restored.optString("source_url"))) {
                throw new AssertionError("Saved URL restore did not remain configured and disconnected: " + restored);
            }
            savedSourceRestoreStarted = false;
            markProgress("configure", "cold-source-restored", "completed");

            int beforeColdImport = subscriptionFixtureState().getInt("subscription_gets");
            verifyColdDeepLinkImport(subscriptionURL, beforeColdImport,
                    remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
            verifyInvalidDeepLinksDoNotFetch(subscriptionURL,
                    remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
            expectedNavigationGets = savedSourceInitialGets + 2;
        } else if (skipColdSavedSourceRestore) {
            if (processColdImportVerified) {
                expectedRenderedSource = subscriptionURL;
                JSONObject importedGets = waitForInFlightGets(0,
                        remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
                expectedNavigationGets = importedGets.getInt("subscription_gets");
            } else {
                expectedRenderedSource = subscriptionURL;
                waitForUiControl("Profile 1 action",
                        remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
                JSONObject importedGets = waitForInFlightGets(0,
                        remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
                JSONObject imported = waitForSessionSource(subscriptionURL,
                        remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
                JSONArray profiles = imported.optJSONArray("profiles");
                if (importedGets.getInt("subscription_gets") != recoveryImportInitialGets + 1
                        || !imported.optBoolean("configured")
                        || !subscriptionURL.equals(imported.optString("source_url"))
                        || profiles == null || profiles.length() == 0
                        || imported.optJSONObject("active_profile") != null
                        || imported.optJSONObject("pending_target") != null
                        || !"CONFIGURED".equals(imported.optString("state"))) {
                    throw new AssertionError("Rendered cold import did not load one disconnected inventory: "
                            + imported);
                }
                expectedNavigationGets = importedGets.getInt("subscription_gets");
            }
        }
        markProgress("configure", "configuration-control", "started");
        tapUiControl("Subscription URL", remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        UiObject2 input = waitForFocusedNativeInput(
                remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        markProgress("configure", "configuration-control", "completed");

        String text = subscriptionURL;
        if (text.trim().isEmpty() || text.indexOf('\u0000') >= 0) {
            throw new IllegalArgumentException("ANDROID_PROFILE_TEXT_INVALID");
        }
        expectedRenderedSource = text.trim();
        markProgress("configure", "profile-entry", "started");
        input.setText(expectedRenderedSource);
        waitForIdleBounded(uiDevice(), deadline);
        assertRenderedSourceRetained(
                Math.min(2_000L, remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT")));
        markProgress("configure", "profile-entry", "completed");
        dismissNativeInputIfVisible();
        markProgress("configure", "input-dismiss", "completed");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));

        markProgress("configure", "rendered-navigation", "started");
        tapUiControl("About", remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        waitForUiControl("Back", remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        tapUiControl("Back", remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        assertRenderedSourceRetained(
                Math.min(2_000L, remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT")));
        waitForUiControl("Profile 1 action", remainingTimeout(deadline, "ANDROID_UI_CONFIGURE_TIMEOUT"));
        JSONObject unchanged = subscriptionFixtureState();
        if (expectedNavigationGets < 0
                || unchanged.getInt("subscription_gets") != expectedNavigationGets) {
            throw new AssertionError("Entering the already accepted URL triggered another fetch");
        }
        markProgress("configure", "rendered-navigation", "completed");
    }

    private void verifyProcessColdImport(String subscriptionURL, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        if (processColdImportInitialGets < 0) {
            throw new IllegalStateException("ANDROID_PROCESS_COLD_IMPORT_BASELINE_MISSING");
        }
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT"));
        Activity activity = MainActivity.current;
        if (activity == null) {
            throw new IllegalStateException("ANDROID_PROCESS_COLD_IMPORT_ACTIVITY_MISSING");
        }
        String[] deliveredLink = new String[1];
        InstrumentationRegistry.getInstrumentation().runOnMainSync(() ->
                deliveredLink[0] = activity.getIntent().getDataString());
        Uri expectedLink = new Uri.Builder().scheme("dobbyvpn").authority("import")
                .appendQueryParameter("url", subscriptionURL).build();
        if (!expectedLink.toString().equals(deliveredLink[0])) {
            throw new AssertionError("Process-cold OS intent was not delivered to the rendered Activity: "
                    + deliveredLink[0]);
        }
        expectedRenderedSource = subscriptionURL;
        waitForUiControl("Profile 1 action",
                remainingTimeout(deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT"));
        JSONObject requested = waitForSubscriptionGets(processColdImportInitialGets + 1,
                remainingTimeout(deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT"));
        requested = waitForInFlightGets(0,
                remainingTimeout(deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT"));
        JSONObject imported = waitForSessionSource(subscriptionURL,
                remainingTimeout(deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT"));
        if (requested.getInt("subscription_gets") != processColdImportInitialGets + 1
                || !imported.optBoolean("configured")
                || !subscriptionURL.equals(imported.optString("source_url"))
                || imported.optJSONArray("profiles") == null
                || imported.getJSONArray("profiles").length() == 0
                || !"CONFIGURED".equals(imported.optString("state"))
                || imported.optJSONObject("active_profile") != null
                || imported.optJSONObject("pending_target") != null) {
            throw new AssertionError("Process-cold deep link did not load exactly one disconnected inventory: "
                    + imported);
        }
        if (awaitVpnNetwork(false, remainingTimeout(
                deadline, "ANDROID_PROCESS_COLD_IMPORT_TIMEOUT")) != null) {
            throw new AssertionError("Process-cold import started a VPN connection");
        }
        markProgress("configure", "process-cold-import-loaded", "completed");
    }

    private void launchActivityColdSavedSourceRestore(String subscriptionURL, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        savedSourceRestoreAttempted = true;
        savedSourceRestoreStarted = true;
        expectedRenderedSource = subscriptionURL;
        Activity previous = MainActivity.current;
        if (previous != null) {
            InstrumentationRegistry.getInstrumentation().runOnMainSync(previous::finishAndRemoveTask);
            long finishDeadline = Math.min(deadline, System.currentTimeMillis() + 5_000L);
            while (MainActivity.current != null && System.currentTimeMillis() < finishDeadline) {
                SystemClock.sleep(25L);
            }
            if (MainActivity.current != null) {
                throw new IllegalStateException("ANDROID_SAVED_SOURCE_RESTORE_ACTIVITY_DID_NOT_FINISH");
            }
        }
        remainingTimeout(deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT");
        JSONObject drained = waitForInFlightGets(0,
                remainingTimeout(deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
        savedSourceInitialGets = drained.getInt("subscription_gets");

        File savedSource = new File(NativeVpnBridge.sourceURLPath(context));
        File savedSourceDirectory = savedSource.getParentFile();
        if (savedSourceDirectory == null
                || (!savedSourceDirectory.isDirectory() && !savedSourceDirectory.mkdirs())) {
            throw new IllegalStateException("ANDROID_SAVED_SOURCE_DIRECTORY_FAILED");
        }
        try (FileOutputStream output = new FileOutputStream(savedSource, false)) {
            output.write(subscriptionURL.getBytes(StandardCharsets.UTF_8));
            output.getFD().sync();
        }

        subscriptionFixturePost("/hold", new byte[0]);
        try {
            // The runner may leave a deep-link Activity open before
            // instrumentation. Finish it while retaining the instrumentation
            // process, then launch a bare link so the seeded saved URL is the
            // only source for the production UI's automatic-load path.
            String output = launchBareLink();
            if (!output.contains("Status: ok")) {
                throw new IllegalStateException("ANDROID_SAVED_SOURCE_RESTORE_LAUNCH_FAILED");
            }
        } catch (Exception failure) {
            try {
                subscriptionFixturePost("/release", new byte[0]);
            } catch (Exception releaseFailure) {
                failure.addSuppressed(releaseFailure);
            }
            throw failure;
        }
        markProgress("configure", "cold-saved-source-launched", "completed");
    }

    private void verifySavedURLRestoration(String subscriptionURL, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        boolean releaseNeeded = true;
        Throwable primaryFailure = null;
        try {
            waitForUiControl("Loading profiles…", remainingTimeout(
                    deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
            assertRenderedSourceRetained(remainingTimeout(
                    deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
            JSONObject held = waitForSubscriptionGets(savedSourceInitialGets + 1,
                    remainingTimeout(deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
            held = waitForInFlightGets(1,
                    remainingTimeout(deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
            if (held.getInt("subscription_gets") != savedSourceInitialGets + 1
                    || held.getInt("max_in_flight_gets") > 1) {
                throw new AssertionError("Saved URL restoration duplicated or overlapped its HTTPS request");
            }

            JSONObject emptyInventory = snapshotResult("");
            JSONArray profiles = emptyInventory.optJSONArray("profiles");
            UiObject2 connectLabel = findUiObject(CONNECTION_ACTION_LABEL);
            UiObject2 connect = connectLabel;
            while (connect != null && !connect.isClickable()) connect = connect.getParent();
            boolean sourceMatches = subscriptionURL.equals(emptyInventory.optString("source_url"));
            boolean backendUnconfigured = !emptyInventory.optBoolean("configured");
            boolean inventoryEmpty = profiles == null || profiles.length() == 0;
            UiObject2 retry = findUiObject("Retry");
            UiObject2 profile1Action = findUiObject("Profile 1 action");
            boolean retryAbsent = retry == null;
            boolean profile1ActionAbsent = profile1Action == null;
            Boolean connectEnabled = connect == null ? null : connect.isEnabled();
            boolean connectPresentAndDisabled = connectLabel != null
                    && connect != null && Boolean.FALSE.equals(connectEnabled);
            ArrayList<String> failedPredicates = new ArrayList<>();
            if (!sourceMatches) failedPredicates.add("sourceMatches");
            if (!backendUnconfigured) failedPredicates.add("backendUnconfigured");
            if (!inventoryEmpty) failedPredicates.add("inventoryEmpty");
            if (!retryAbsent) failedPredicates.add("retryAbsent");
            if (!profile1ActionAbsent) failedPredicates.add("profile1ActionAbsent");
            if (!connectPresentAndDisabled) failedPredicates.add("connectPresentAndDisabled");
            if (!failedPredicates.isEmpty()) {
                AssertionError failure = new AssertionError(
                        "Held saved URL did not render an empty, loading inventory: " + emptyInventory
                                + "; failed_predicates=" + failedPredicates
                                + "; predicate_values={sourceMatches=" + sourceMatches
                                + ", backendUnconfigured=" + backendUnconfigured
                                + ", inventoryEmpty=" + inventoryEmpty
                                + ", retryAbsent=" + retryAbsent
                                + ", profile1ActionAbsent=" + profile1ActionAbsent
                                + ", connectPresent=" + (connectLabel != null)
                                + ", connectActionFound=" + (connect != null)
                                + ", connectEnabled=" + connectEnabled + "}");
                try {
                    StringBuilder uiDetails = new StringBuilder("Held saved URL matching UI nodes:")
                            .append("\nretryFound=").append(retry != null)
                            .append("\nprofile1ActionFound=").append(profile1Action != null);
                    appendHeldSavedSourceActionDetails(uiDetails, failure, connect, connectEnabled);
                    System.err.println("DOBBY_SAVED_SOURCE_UI_NODES_BEGIN");
                    System.err.println(uiDetails);
                    System.err.println("DOBBY_SAVED_SOURCE_UI_NODES_END");
                    System.err.flush();
                } catch (Throwable diagnosticFailure) {
                    failure.addSuppressed(new IllegalStateException(
                            "ANDROID_SAVED_SOURCE_UI_NODE_DIAGNOSTICS_FAILED", diagnosticFailure));
                }
                try {
                    System.err.println("DOBBY_SAVED_SOURCE_UI_HIERARCHY_BEGIN");
                    System.err.println(dumpUiHierarchy());
                    System.err.println("DOBBY_SAVED_SOURCE_UI_HIERARCHY_END");
                    System.err.flush();
                } catch (Throwable hierarchyFailure) {
                    failure.addSuppressed(new IllegalStateException(
                            "ANDROID_SAVED_SOURCE_UI_HIERARCHY_FAILED", hierarchyFailure));
                }
                throw failure;
            }

            subscriptionFixturePost("/release", new byte[0]);
            releaseNeeded = false;
            waitForUiControl("Profile 1 action", remainingTimeout(
                    deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
            JSONObject completed = waitForInFlightGets(0,
                    remainingTimeout(deadline, "ANDROID_SAVED_SOURCE_RESTORE_TIMEOUT"));
            JSONObject restored = snapshotResult("");
            if (completed.getInt("subscription_gets") != savedSourceInitialGets + 1
                    || !restored.optBoolean("configured")
                    || !subscriptionURL.equals(restored.optString("source_url"))
                    || restored.optJSONArray("profiles") == null
                    || restored.getJSONArray("profiles").length() == 0) {
                throw new AssertionError("Saved URL did not restore its rendered profile inventory: " + restored);
            }
        } catch (Exception | Error failure) {
            primaryFailure = failure;
            throw failure;
        } finally {
            // Also release the response on assertion failures so fixture and
            // Activity teardown do not depend on the outer adapter timeout.
            if (releaseNeeded) {
                try {
                    subscriptionFixturePost("/release", new byte[0]);
                } catch (Exception releaseFailure) {
                    if (primaryFailure != null) primaryFailure.addSuppressed(releaseFailure);
                    else throw releaseFailure;
                }
            }
        }
    }

    private void appendHeldSavedSourceActionDetails(
            StringBuilder details, AssertionError failure, UiObject2 connect, Boolean enabled) {
        details.append("\nconnectionActionFound=").append(connect != null);
        if (connect == null) return;
        details.append("\nconnectionAction.enabled=").append(enabled);
        try {
            details.append("\nconnectionAction.text=").append(connect.getText());
        } catch (Throwable diagnosticFailure) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_SAVED_SOURCE_ACTION_TEXT_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        try {
            details.append("\nconnectionAction.contentDescription=").append(connect.getContentDescription());
        } catch (Throwable diagnosticFailure) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_SAVED_SOURCE_ACTION_DESCRIPTION_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        try {
            details.append("\nconnectionAction.class=").append(connect.getClassName());
        } catch (Throwable diagnosticFailure) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_SAVED_SOURCE_ACTION_CLASS_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        try {
            details.append("\nconnectionAction.clickable=").append(connect.isClickable());
        } catch (Throwable diagnosticFailure) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_SAVED_SOURCE_ACTION_CLICKABLE_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        try {
            details.append("\nconnectionAction.visibleBounds=").append(connect.getVisibleBounds());
        } catch (Throwable diagnosticFailure) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_SAVED_SOURCE_ACTION_BOUNDS_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
    }

    private void verifyColdDeepLinkImport(String subscriptionURL, int before, long timeout)
            throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        Activity previous = MainActivity.current;
        if (previous != null) {
            InstrumentationRegistry.getInstrumentation().runOnMainSync(previous::finishAndRemoveTask);
            long finishDeadline = Math.min(deadline, System.currentTimeMillis() + 5_000L);
            while (MainActivity.current != null && System.currentTimeMillis() < finishDeadline) {
                SystemClock.sleep(25L);
            }
            if (MainActivity.current != null) {
                throw new IllegalStateException("ANDROID_COLD_IMPORT_ACTIVITY_DID_NOT_FINISH");
            }
        }

        expectedRenderedSource = subscriptionURL;
        String link = "dobbyvpn://import?url=" + java.net.URLEncoder.encode(subscriptionURL, "UTF-8");
        String output = uiDevice().executeShellCommand(
                "am start -W -a android.intent.action.VIEW -d " + link + " " + context.getPackageName());
        if (!output.contains("Status: ok")) {
            throw new IllegalStateException("ANDROID_COLD_IMPORT_LAUNCH_FAILED");
        }
        markProgress("configure", "cold-deeplink-launched", "completed");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));
        foregroundActivity = ensureForegroundActivity();
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));
        waitForUiControl("Profile 1 action", remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));

        JSONObject requested = waitForSubscriptionGets(before + 1,
                remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));
        requested = waitForInFlightGets(0,
                remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));
        JSONObject imported = waitForSessionSource(subscriptionURL,
                remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));
        if (requested.getInt("subscription_gets") != before + 1
                || !imported.optBoolean("configured")
                || !subscriptionURL.equals(imported.optString("source_url"))
                || imported.optJSONArray("profiles") == null
                || imported.getJSONArray("profiles").length() == 0) {
            throw new AssertionError("Cold deep-link import did not load one rendered inventory: " + imported);
        }
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_COLD_IMPORT_TIMEOUT"));
        markProgress("configure", "cold-import-loaded", "completed");
    }

    private void verifyInvalidDeepLinksDoNotFetch(String subscriptionURL, long timeout)
            throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        expectedRenderedSource = subscriptionURL;
        Activity activity = MainActivity.current;
        if (activity == null) throw new AssertionError("Android Activity missing before invalid imports");
        JSONObject before = snapshotResult("");
        int requests = waitForInFlightGets(0,
                remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"))
                .getInt("subscription_gets");
        String[][] invalidLinks = {
                {
                        "dobbyvpn://import?url="
                                + java.net.URLEncoder.encode("http://example.invalid/subscription", "UTF-8"),
                        "Paste an HTTPS subscription URL with a host",
                },
                {
                        "dobbyvpn://import?url=" + java.net.URLEncoder.encode("https://", "UTF-8"),
                        "Paste an HTTPS subscription URL with a host",
                },
                {
                        "dobbyvpn://import?url=%ZZ",
                        "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL",
                },
                {
                        "dobbyvpn://import",
                        "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL",
                },
                {
                        "dobbyvpn://import?url=",
                        "Paste an HTTPS subscription URL with a host",
                },
                {
                        "dobbyvpn://import?url=https%3A%2F%2Fexample.invalid%2Fa"
                                + "&url=https%3A%2F%2Fexample.invalid%2Fb",
                        "Use dobbyvpn://import?url= followed by an encoded HTTPS subscription URL",
                },
        };
        for (int index = 0; index < invalidLinks.length; index++) {
            String link = invalidLinks[index][0];
            String actionableFeedback = invalidLinks[index][1];
            if (index > 0) {
                // Clear the previous error through the accepted URL without
                // fetching it again, so every malformed link must render its
                // own new feedback before the unchanged state is checked.
                deliverWarmImport(subscriptionURL);
                waitForPreviousInvalidImportFeedbackToClear(
                        invalidLinks[index - 1][1], deadline);
                waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
                if (findUiObject("Error") != null) {
                    throw new AssertionError("Accepted URL did not clear the preceding deep-link error");
                }
            }
            String output = uiDevice().executeShellCommand("am start -W -a android.intent.action.VIEW -d "
                    + link + " " + context.getPackageName());
            if (!output.contains("Status: ok")) {
                throw new AssertionError("ANDROID_INVALID_IMPORT_LAUNCH_FAILED");
            }
            waitForUiState("Error", remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
            waitForUiControl(actionableFeedback,
                    remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
            assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
            JSONObject after = snapshotResult("");
            JSONObject counts = waitForInFlightGets(0,
                    remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
            if (MainActivity.current != activity
                    || counts.getInt("subscription_gets") != requests
                    || !before.optString("source_url").equals(after.optString("source_url"))
                    || !before.optString("digest").equals(after.optString("digest"))
                    || before.optLong("generation") != after.optLong("generation")
                    || !before.optString("state").equals(after.optString("state"))) {
                throw new AssertionError("Invalid deep link fetched or changed the accepted session ("
                        + link + "): " + after);
            }
        }

        String finalFeedback = invalidLinks[invalidLinks.length - 1][1];
        deliverWarmImport(subscriptionURL);
        waitForPreviousInvalidImportFeedbackToClear(finalFeedback, deadline);
        waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
        if (findUiObject("Error") != null) {
            throw new AssertionError("Accepted URL did not clear the final deep-link error");
        }
        JSONObject clearedCounts = waitForInFlightGets(0,
                remainingTimeout(deadline, "ANDROID_INVALID_IMPORT_TIMEOUT"));
        JSONObject cleared = snapshotResult("");
        if (MainActivity.current != activity
                || clearedCounts.getInt("subscription_gets") != requests
                || !before.optString("source_url").equals(cleared.optString("source_url"))
                || !before.optString("digest").equals(cleared.optString("digest"))
                || before.optLong("generation") != cleared.optLong("generation")
                || !before.optString("state").equals(cleared.optString("state"))) {
            throw new AssertionError("Accepted URL fetched or changed the accepted session: " + cleared);
        }
    }

    private void waitForPreviousInvalidImportFeedbackToClear(String previousFeedback, long deadline)
            throws Exception {
        UiDevice device = uiDevice();
        while (System.currentTimeMillis() < deadline) {
            UiObject2 error = findUiObject("Error");
            UiObject2 failed = findUiObject("Failed");
            if (error == null && failed == null) return;
            if (failed != null) throw new IllegalStateException("ANDROID_UI_DISCONNECT_FAILED");
            if (findUiObject(previousFeedback) == null) {
                // Do not wait through a different Error state. The second
                // state read avoids treating a just-cleared prior node as a
                // new failure while the rendered tree is changing.
                if (findUiObject("Error") == null && findUiObject("Failed") == null) return;
                throw new IllegalStateException("ANDROID_UI_DISCONNECT_FAILED");
            }
            waitForIdleBounded(device, deadline);
            long remaining = deadline - System.currentTimeMillis();
            if (remaining > 0) Thread.sleep(Math.min(POLL_MILLIS, remaining));
        }
        throw new IllegalStateException("ANDROID_UI_DISCONNECT_FAILED");
    }

    private SSLSocketFactory createSubscriptionControlSocketFactory(String caPem) throws Exception {
        CertificateFactory certificateFactory = CertificateFactory.getInstance("X.509");
        X509Certificate certificate;
        try (ByteArrayInputStream input = new ByteArrayInputStream(
                caPem.getBytes(StandardCharsets.US_ASCII))) {
            certificate = (X509Certificate) certificateFactory.generateCertificate(input);
        }
        certificate.checkValidity();
        KeyStore trustStore = KeyStore.getInstance(KeyStore.getDefaultType());
        trustStore.load(null, null);
        trustStore.setCertificateEntry("android-subscription-fixture", certificate);
        TrustManagerFactory trustManagers = TrustManagerFactory.getInstance(
                TrustManagerFactory.getDefaultAlgorithm());
        trustManagers.init(trustStore);
        SSLContext tls = SSLContext.getInstance("TLS");
        tls.init(null, trustManagers.getTrustManagers(), null);
        return tls.getSocketFactory();
    }

    private HttpURLConnection subscriptionControl(String suffix, String method, byte[] body) throws Exception {
        HttpURLConnection connection = (HttpURLConnection)
                new URL(subscriptionControlURL + suffix).openConnection();
        if (!(connection instanceof HttpsURLConnection) || subscriptionControlSocketFactory == null) {
            throw new IllegalStateException("ANDROID_SUBSCRIPTION_CONTROL_TLS_UNAVAILABLE");
        }
        ((HttpsURLConnection) connection).setSSLSocketFactory(subscriptionControlSocketFactory);
        connection.setConnectTimeout(5_000);
        connection.setReadTimeout(5_000);
        connection.setRequestMethod(method);
        connection.setRequestProperty("X-DobbyVPN-Torturer-Key", subscriptionControlKey);
        if (body != null) {
            connection.setDoOutput(true);
            connection.setFixedLengthStreamingMode(body.length);
            connection.setRequestProperty("Content-Type", "application/octet-stream");
            try (OutputStream output = connection.getOutputStream()) {
                output.write(body);
            }
        }
        return connection;
    }

    private JSONObject subscriptionFixtureState() throws Exception {
        HttpURLConnection connection = subscriptionControl("", "GET", null);
        try {
            if (connection.getResponseCode() != 200) throw new IOException("ANDROID_FIXTURE_CONTROL_READ_FAILED");
            try (InputStream input = connection.getInputStream()) {
                ByteArrayOutputStream bytes = new ByteArrayOutputStream();
                byte[] buffer = new byte[4_096];
                int count;
                while ((count = input.read(buffer)) >= 0) {
                    if (count > 0) bytes.write(buffer, 0, count);
                }
                return new JSONObject(new String(bytes.toByteArray(), StandardCharsets.UTF_8));
            }
        } finally {
            connection.disconnect();
        }
    }

    private void subscriptionFixturePost(String suffix, byte[] body) throws Exception {
        HttpURLConnection connection = subscriptionControl(suffix, "POST", body);
        try {
            if (connection.getResponseCode() != 204) throw new IOException("ANDROID_FIXTURE_CONTROL_WRITE_FAILED");
        } finally {
            connection.disconnect();
        }
    }

    private JSONObject waitForSubscriptionGets(int count, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        while (System.currentTimeMillis() < deadline) {
            JSONObject state = subscriptionFixtureState();
            if (state.optInt("subscription_gets") >= count) return state;
            SystemClock.sleep(25L);
        }
        throw new IllegalStateException("ANDROID_FIXTURE_GET_COUNT_TIMEOUT");
    }

    private JSONObject waitForInFlightGets(int count, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        while (System.currentTimeMillis() < deadline) {
            JSONObject state = subscriptionFixtureState();
            if (state.optInt("in_flight_gets") == count) return state;
            SystemClock.sleep(25L);
        }
        throw new IllegalStateException("ANDROID_FIXTURE_IN_FLIGHT_TIMEOUT");
    }

    private JSONObject waitForSessionSource(String source, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        while (System.currentTimeMillis() < deadline) {
            JSONObject snapshot = snapshotResult("");
            if (source.equals(snapshot.optString("source_url"))) return snapshot;
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_SOURCE_ACCEPTANCE_TIMEOUT");
    }

    private boolean hasFollowingConnectionStart(JSONArray operations, int start)
            throws Exception {
        for (int index = start; index < operations.length(); index++) {
            String operation = operations.getJSONObject(index).getString("operation");
            if ("connect".equals(operation) || "reconnect".equals(operation)
                    || CONSENT_GRANT_SELECTION_OPERATION.equals(operation)) {
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
        return connectThroughRenderedUI(timeout, operation, false);
    }

    private boolean connectThroughRenderedUI(
            long timeout, String operation, boolean enableRecoveryStopSeam) throws Exception {
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
        if (enableRecoveryStopSeam && !consentNeeded) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_CONSENT_NOT_FRESH");
        }
        markProgress(operation, "connect-control", "started");
        if (enableRecoveryStopSeam) enableTestRecoveryStopSeam();
        tapUiControl(
                CONNECTION_ACTION_LABEL,
                remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
        markProgress(operation, "connect-control", "completed");
        // Observe the rendered state after the tap to distinguish a missed
        // action from a rendered failure while awaiting consent.
        String postTapState = awaitVisibleConnectionState(
                Math.min(2_000L,
                        remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT")));
        String postTapErrorCategory = "";
        if ("Error".equals(postTapState) || "Failed".equals(postTapState)) {
            // Error becomes accessible before its Details/category node on
            // some Android frames. Poll for that category within the
            // command's overall deadline before classifying the failure.
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

        }
        markProgress(operation, "connected-state", "started");
        waitForUiState(
                "Connected",
                remainingTimeout(deadline, "ANDROID_UI_CONNECT_TIMEOUT"));
        markProgress(operation, "connected-state", "completed");
        return consentNeeded;
    }

    private boolean verifyManualConsent(long timeout) throws Exception {
        if (VpnService.prepare(context) == null) {
            throw new IllegalStateException(
                    "ANDROID_VPN_CONSENT_NOT_FRESH_BEFORE_DENY_STALE_CASE: "
                            + "the run APKs must be reinstalled before deny/stale assertions");
        }
        long deadline = System.currentTimeMillis() + timeout;
        JSONObject initial = snapshotResult("");
        int count = initial.getJSONArray("profiles").length();
        int target = count > 1 ? 1 : 0;
        String control = "Profile " + (target + 1) + " action";
        if (initial.optJSONObject("active_profile") != null
                || initial.optJSONObject("pending_target") != null
                || awaitVpnNetwork(false, remainingTimeout(
                        deadline, "ANDROID_VPN_CONSENT_STALE_NETWORK")) != null) {
            throw new AssertionError("Manual consent requires an idle, tunnel-free baseline: " + initial);
        }
        tapEnabledControl(control, deadline);
        UiObject2 cancel = uiDevice().wait(androidx.test.uiautomator.Until.findObject(
                By.res("android:id/button2")), remainingTimeout(deadline, "ANDROID_VPN_CONSENT_TIMEOUT"));
        if (cancel == null || !cancel.isEnabled()) throw new AssertionError("VPN consent cancel button unavailable");
        cancel.click();
        waitForUiControl("VPN permission was not granted", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject denied = snapshotResult("");
        if (VpnService.prepare(context) == null || denied.getLong("generation") != initial.getLong("generation")
                || !initial.getString("state").equals(denied.getString("state"))
                || !initial.optString("active_digest").equals(denied.optString("active_digest"))) {
            throw new AssertionError("Denied consent started a connection: " + denied);
        }
        tapEnabledControl(control, deadline);
        UiDevice device = uiDevice();
        boolean secondPromptVisible = false;
        while (System.currentTimeMillis() < deadline) {
            UiObject2 prompt = findVpnConsentButton(device);
            secondPromptVisible = prompt != null && !prompt.getVisibleBounds().isEmpty();
            if (secondPromptVisible) break;
            SystemClock.sleep(POLL_MILLIS);
        }
        if (!secondPromptVisible || VpnService.prepare(context) == null) {
            throw new AssertionError("Second VPN consent prompt did not open before the warm import");
        }
        Uri changedSource = Uri.parse(launchSubscriptionURL).buildUpon()
                .appendQueryParameter("consent-currentness", "1").build();
        if (MainActivity.current == null) throw new IllegalStateException("ANDROID_CONSENT_ACTIVITY_MISSING");
        String currentSource = changedSource.toString();
        expectedRenderedSource = currentSource;
        deliverWarmImport(currentSource);
        waitForSessionSource(
                currentSource, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        boolean returnedToProduct = false;
        String cancellationObservation = "no observation";
        while (System.currentTimeMillis() < deadline) {
            // The instrumentation monitor may miss an Activity resumed before enrollment.
            // Keep it diagnostic and use the live Activity's LifecycleOwner as authority.
            Activity monitorResumedActivity = findResumedTargetActivity();
            AtomicReference<Boolean> nativeActivityReady = new AtomicReference<>(false);
            AtomicReference<String> nativeActivityState = new AtomicReference<>("activity=null");
            InstrumentationRegistry.getInstrumentation().runOnMainSync(() -> {
                MainActivity activity = MainActivity.current;
                if (activity == null) return;
                boolean targetActivity = !activity.isFinishing()
                        && !activity.isDestroyed()
                        && context.getPackageName().equals(activity.getPackageName());
                androidx.lifecycle.Lifecycle.State lifecycleState =
                        activity.getLifecycle().getCurrentState();
                boolean lifecycleResumed = lifecycleState
                        == androidx.lifecycle.Lifecycle.State.RESUMED;
                boolean windowFocused = activity.hasWindowFocus();
                nativeActivityReady.set(targetActivity && lifecycleResumed && windowFocused);
                nativeActivityState.set("activity=" + activity
                        + ", target=" + targetActivity
                        + ", lifecycle=" + lifecycleState
                        + ", windowFocused=" + windowFocused);
            });
            String foreground = foregroundCategory();
            boolean consentPromptAbsent = findVpnConsentButton(device) == null;
            boolean permissionPending = VpnService.prepare(context) != null;
            returnedToProduct = nativeActivityReady.get()
                    && "PRODUCT".equals(foreground)
                    && consentPromptAbsent
                    && permissionPending;
            cancellationObservation = "monitorResumed=" + (monitorResumedActivity != null)
                    + (monitorResumedActivity == null ? "" : " (" + monitorResumedActivity + ")")
                    + ", nativeActivity={" + nativeActivityState.get() + "}"
                    + ", foreground=" + foreground
                    + ", consentPromptAbsent=" + consentPromptAbsent
                    + ", permissionPending=" + permissionPending;
            if (returnedToProduct) break;
            SystemClock.sleep(POLL_MILLIS);
        }
        if (progressObservation != null) {
            progressObservation.put("consent_cancellation_observation", cancellationObservation);
        }
        if (!returnedToProduct) {
            AssertionError failure = new AssertionError(
                    "Cancelled consent did not return to the resumed product with permission pending: "
                            + cancellationObservation);
            try {
                failure.addSuppressed(new IllegalStateException(
                        "ANDROID_CONSENT_CANCELLATION_UI_HIERARCHY\n"
                                + dumpUiTimeoutContext("VPN consent cancellation")));
            } catch (Throwable diagnosticFailure) {
                failure.addSuppressed(new IllegalStateException(
                        "ANDROID_CONSENT_CANCELLATION_UI_HIERARCHY_COLLECTION_FAILED",
                        diagnosticFailure));
            }
            throw failure;
        }
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject current = snapshotResult("");
        if (!currentSource.equals(current.optString("source_url"))
                || !initial.getString("digest").equals(current.optString("digest"))
                || current.getLong("generation") != initial.getLong("generation")
                || !initial.getString("state").equals(current.optString("state"))
                || !initial.optString("active_digest").equals(current.optString("active_digest"))
                || current.optJSONObject("active_profile") != null
                || current.optJSONObject("pending_target") != null
                || current.getJSONArray("profiles").length() != count
                || findVpnConsentButton(device) != null
                || VpnService.prepare(context) == null) {
            throw new AssertionError("Warm import changed the denied consent baseline: " + current);
        }
        if (awaitVpnNetwork(false, remainingTimeout(
                deadline, "ANDROID_VPN_CONSENT_STALE_NETWORK")) != null) {
            throw new AssertionError("Warm import created a VPN before a fresh consent request");
        }

        tapEnabledControl(control, deadline);
        acceptVpnConsent(
                remainingTimeout(deadline, "ANDROID_VPN_CONSENT_TIMEOUT"),
                "manual-consent-current-source");
        if (VpnService.prepare(context) != null) {
            throw new AssertionError("Fresh current-source consent did not grant VPN permission");
        }
        JSONObject selected = awaitSelection(
                current.getLong("generation"), "PROFILE_INDEX", target, deadline);
        JSONObject expectedProfile = current.getJSONArray("profiles").getJSONObject(target);
        JSONObject activeProfile = selected.getJSONObject("active_profile");
        String currentDigest = current.getString("digest");
        if (!"CONNECTED".equals(selected.optString("state"))
                || !currentSource.equals(selected.optString("source_url"))
                || !currentDigest.equals(selected.optString("digest"))
                || !currentDigest.equals(selected.optString("active_digest"))
                || !"PROFILE_INDEX".equals(selected.optString("active_mode"))
                || activeProfile.getInt("index") != target
                || !expectedProfile.optString("protocol").equals(activeProfile.optString("protocol"))
                || !expectedProfile.optString("description").equals(activeProfile.optString("description"))
                || selected.getLong("generation") <= current.getLong("generation")) {
            throw new AssertionError("Fresh consent selected a different source or profile: " + selected);
        }
        if (awaitVpnNetwork(true, remainingTimeout(
                deadline, "ANDROID_VPN_CONSENT_TUNNEL_MISSING")) == null) {
            throw new AssertionError("Fresh current-source consent did not create a VPN tunnel");
        }

        disconnectThroughRenderedUI(
                remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        if (awaitVpnNetwork(false, remainingTimeout(
                deadline, "ANDROID_UI_DISCONNECT_TIMEOUT")) != null) {
            throw new AssertionError("Fresh current-source consent tunnel remained after Disconnect");
        }
        JSONObject stopped = snapshotResult("");
        if (stopped.getLong("generation") != selected.getLong("generation")
                || !"IDLE".equals(stopped.optString("state"))
                || !currentSource.equals(stopped.optString("source_url"))
                || !currentDigest.equals(stopped.optString("digest"))
                || stopped.optJSONObject("active_profile") != null
                || stopped.optJSONObject("pending_target") != null) {
            throw new AssertionError("Nondefault consent target did not disconnect cleanly");
        }
        markProgress("configure", "manual-consent-current-source-granted", "completed");
        return true;
    }

    private JSONObject verifyUnchangedConsentProfileSelection(long timeout, String operation)
            throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress(operation, "fresh-permission-check", "started");
        if (VpnService.prepare(context) == null) {
            throw new IllegalStateException(
                    "ANDROID_VPN_CONSENT_NOT_FRESH_AFTER_APK_REINSTALL: "
                            + "VpnService.prepare returned null after reinstalling the run APKs; "
                            + "refusing to substitute another permission path");
        }
        JSONObject before = snapshotResult("");
        String source = before.optString("source_url");
        if (!launchSubscriptionURL.equals(source)) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_SOURCE_CHANGED");
        }
        if (awaitVpnNetwork(false, NETWORK_RECOVERY_TIMEOUT_MILLIS) != null) {
            throw new IllegalStateException("ANDROID_STALE_VPN_NETWORK");
        }
        awaitValidatedPhysicalNetwork(
                remainingTimeout(deadline, "ANDROID_PHYSICAL_NETWORK_NOT_VALIDATED"));
        JSONArray profiles = before.getJSONArray("profiles");
        if (profiles.length() < 2) {
            throw new IllegalStateException(
                    "ANDROID_VPN_CONSENT_NONDEFAULT_PROFILE_UNAVAILABLE: "
                            + "the adapter-prepared profile inventory has fewer than two entries");
        }
        int target = 1;
        JSONObject expectedProfile = profiles.getJSONObject(target);
        String control = "Profile " + (target + 1) + " action";
        markProgress(operation, "fresh-permission-check", "completed");
        markProgress(operation, "profile-selection", "started");
        tapEnabledControl(control, deadline);
        markProgress(operation, "profile-selection", "completed");
        if (VpnService.prepare(context) == null) {
            throw new IllegalStateException(
                    "ANDROID_VPN_CONSENT_NOT_FRESH_AFTER_APK_REINSTALL: "
                            + "VPN permission changed before the consent action");
        }
        markProgress(operation, "consent", "started");
        acceptVpnConsent(
                remainingTimeout(deadline, "ANDROID_VPN_CONSENT_TIMEOUT"), operation);
        if (VpnService.prepare(context) != null) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_TIMEOUT");
        }
        markProgress(operation, "consent", "completed");
        markProgress(operation, "connected-state", "started");
        JSONObject connected = awaitSelection(
                before.getLong("generation"), "PROFILE_INDEX", target, deadline);
        markProgress(operation, "connected-state", "completed");
        if (!"CONNECTED".equals(connected.optString("state"))
                || !source.equals(connected.optString("source_url"))) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_SOURCE_CHANGED");
        }
        String digest = before.getString("digest");
        if (!digest.equals(connected.optString("digest"))
                || !digest.equals(connected.optString("active_digest"))) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_DIGEST_CHANGED");
        }
        if (!"PROFILE_INDEX".equals(connected.optString("active_mode"))) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_SELECTION_MODE_MISMATCH");
        }
        JSONObject activeProfile = connected.getJSONObject("active_profile");
        if (activeProfile.getInt("index") != target
                || activeProfile.getInt("index") != expectedProfile.getInt("index")
                || !activeProfile.getString("protocol").equals(
                        expectedProfile.getString("protocol"))
                || !activeProfile.getString("description").equals(
                        expectedProfile.getString("description"))) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_PROFILE_IDENTITY_MISMATCH");
        }
        long generation = connected.getLong("generation");
        if (generation <= before.getLong("generation")) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_GENERATION_INVALID");
        }
        if (awaitVpnNetwork(true, remainingTimeout(
                deadline, "ANDROID_VPN_CONSENT_TUNNEL_MISSING")) == null) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_TUNNEL_MISSING");
        }
        markProgress(operation, "disconnect", "started");
        disconnectThroughRenderedUI(
                remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        boolean vpnRemoved = awaitVpnNetwork(
                false, remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT")) == null;
        JSONObject stopped = snapshotResult("");
        if (!vpnRemoved || !"IDLE".equals(stopped.optString("state"))
                || stopped.getLong("generation") != generation
                || !source.equals(stopped.optString("source_url"))
                || !digest.equals(stopped.optString("digest"))) {
            throw new IllegalStateException("ANDROID_VPN_CONSENT_DISCONNECT_FAILED");
        }
        markProgress(operation, "disconnect", "completed");
        return new JSONObject()
                .put("source_verified", true)
                .put("digest_verified", true)
                .put("profile_identity_verified", true)
                .put("mode", "PROFILE_INDEX")
                .put("index", target)
                .put("protocol", expectedProfile.getString("protocol"))
                .put("generation", generation)
                .put("generation_advanced", true)
                .put("disconnect_clean", true);
    }

    private boolean verifySubscriptionControls(String subscriptionURL, long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + timeout;
        JSONObject initial = snapshotResult("");
        verifyAcceptedInventoryAfterActivityReopen(subscriptionURL, deadline);
        initial = snapshotResult("");
        int count = initial.getJSONArray("profiles").length();
        boolean nativeShutdownVerified = false;
        int first = count > 1 && initial.getJSONObject("active_profile").getInt("index") == 0 ? 1 : 0;
        if (count > 1) {
            verifyRenderedStopCancelsPendingSwitch(first, deadline);
            initial = snapshotResult("");
        }
        if (count == 1) disconnectThroughRenderedUI(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (count > 1) scrollControlsToProfileAction(first, count, deadline);
        tapEnabledControl("Profile " + (first + 1) + " action", deadline);
        if (count > 1) verifyPendingProfileTransition(first, first == 0 ? 1 : 0,
                subscriptionURL, deadline);
        JSONObject manual = awaitSelection(initial.getLong("generation"), "PROFILE_INDEX", first, deadline);
        if (count > 1) {
            int second = first == 0 ? 1 : 0;
            scrollControlsToProfileAction(second, count, deadline);
            tapEnabledControl("Profile " + (second + 1) + " action", deadline);
            verifyPendingProfileTransition(second, first, subscriptionURL, deadline);
            manual = awaitSelection(manual.getLong("generation"), "PROFILE_INDEX", second, deadline);
            manual = verifyNativeRevokeCancelsPendingSwitch(manual, deadline);
            nativeShutdownVerified = true;
        }
        scrollControlsToTop(count, deadline);
        tapUiControl("Subscription URL", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        int beforeFailedLoad = subscriptionFixtureState().getInt("subscription_gets");
        subscriptionFixturePost("/fail-next", new byte[0]);
        String failedURL = urlWithQuery(subscriptionURL, "android-retry", "1");
        expectedRenderedSource = failedURL;
        long debounceStarted = SystemClock.elapsedRealtime();
        waitForFocusedNativeInput(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT")).setText(failedURL);
        long debounceProbeDeadline = debounceStarted + 300L;
        while (SystemClock.elapsedRealtime() < debounceProbeDeadline) {
            JSONObject beforeDebounce = subscriptionFixtureState();
            if (beforeDebounce.getInt("subscription_gets") != beforeFailedLoad) {
                throw new AssertionError("Subscription text edit bypassed the 400 ms debounce");
            }
            SystemClock.sleep(20L);
        }
        dismissNativeInputIfVisible();
        waitForUiControl("Retry", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (findUiObject("Load") != null) {
            throw new AssertionError("Subscription failure exposed an explicit Load action");
        }
        JSONObject failedRequest = waitForSubscriptionGets(beforeFailedLoad + 1,
                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        failedRequest = waitForInFlightGets(0, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (failedRequest.getInt("subscription_gets") != beforeFailedLoad + 1
                || failedRequest.getInt("max_in_flight_gets") > 1
                || SystemClock.elapsedRealtime() - debounceStarted < 350L) {
            throw new AssertionError("Debounced subscription load was premature, duplicated, or overlapped");
        }
        JSONObject failedLoad = snapshotResult("");
        if (!"CONNECTED".equals(failedLoad.getString("state"))
                || failedLoad.getLong("generation") != manual.getLong("generation")
                || !failedLoad.getString("active_digest").equals(manual.getString("active_digest"))) {
            throw new AssertionError("Failed subscription load interrupted the tunnel");
        }
        long retryStarted = SystemClock.elapsedRealtime();
        tapEnabledControl("Retry", deadline);
        JSONObject retryResult = waitForSubscriptionGets(beforeFailedLoad + 2,
                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (SystemClock.elapsedRealtime() - retryStarted >= 350L) {
            throw new AssertionError("Retry waited for the text-field debounce");
        }
        retryResult = waitForInFlightGets(0, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject retried = waitForSessionSource(failedURL, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (retryResult.getInt("subscription_gets") != beforeFailedLoad + 2
                || !"CONNECTED".equals(retried.getString("state"))
                || retried.getLong("generation") != manual.getLong("generation")
                || !retried.getString("active_digest").equals(manual.getString("active_digest"))) {
            throw new AssertionError("Immediate Retry did not recover while preserving the active connection");
        }
        if (findUiObject("Retry") != null || findUiObject("Load") != null) {
            throw new AssertionError("Successful Retry left a load action or failure prompt visible");
        }
        SystemClock.sleep(600L);
        if (subscriptionFixtureState().getInt("subscription_gets") != beforeFailedLoad + 2) {
            throw new AssertionError("Successful Retry triggered an automatic subscription reload");
        }
        int beforeWarmLinks = retryResult.getInt("subscription_gets");
        Activity warmActivity = MainActivity.current;
        if (warmActivity == null) throw new AssertionError("Android Activity missing before warm import");
        expectedRenderedSource = subscriptionURL;
        long importStarted = SystemClock.elapsedRealtime();
        deliverWarmImport(subscriptionURL);
        JSONObject immediateImport = waitForSubscriptionGets(beforeWarmLinks + 1,
                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (SystemClock.elapsedRealtime() - importStarted >= 350L) {
            throw new AssertionError("Deep-link import waited for the text-field debounce");
        }
        deliverWarmImport(subscriptionURL);
        if (MainActivity.current != warmActivity) {
            throw new AssertionError("Warm deep link replaced the existing Activity");
        }
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject warmCounts = waitForInFlightGets(0, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject warmSnapshot = waitForSessionSource(subscriptionURL,
                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (immediateImport.getInt("subscription_gets") != beforeWarmLinks + 1
                || warmCounts.getInt("subscription_gets") != beforeWarmLinks + 1
                || !"CONNECTED".equals(warmSnapshot.getString("state"))
                || warmSnapshot.getLong("generation") != manual.getLong("generation")
                || !warmSnapshot.getString("active_digest").equals(manual.getString("active_digest"))) {
            throw new AssertionError("Repeated warm links duplicated a load or changed the active connection");
        }
        if (MainActivity.current != warmActivity) {
            throw new AssertionError("Repeated warm deep link replaced the existing Activity");
        }
        tapEnabledControl(CONNECTION_ACTION_LABEL, deadline);
        verifyPendingAutoTransition(subscriptionURL, deadline);
        JSONObject auto = awaitSelection(manual.getLong("generation"), "AUTO_SELECT", -1, deadline);
        tapUiControl("Clear", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        verifyHeldLoadKeepsControlsResponsive(subscriptionURL, auto, deadline);
        verifyTypedURLChangesWhileLoadHeld(subscriptionURL, deadline);
        verifyReplacementInventoryWhileOldProfileIsActive(subscriptionURL, deadline);
        verifyLongListAndValidPaste(subscriptionURL, deadline);
        markProgress("configure", "manual-switch-failed-load-import-clear", "completed");
        return nativeShutdownVerified;
    }

    private void verifyAcceptedInventoryAfterActivityReopen(String subscriptionURL, long deadline)
            throws Exception {
        JSONObject before = snapshotResult("");
        int requests = subscriptionFixtureState().getInt("subscription_gets");
        expectedRenderedSource = subscriptionURL;
        Activity current = MainActivity.current;
        if (current == null) throw new AssertionError("Activity missing before inventory reopen");
        InstrumentationRegistry.getInstrumentation().runOnMainSync(current::finishAndRemoveTask);
        long closeDeadline = Math.min(deadline, System.currentTimeMillis() + 5_000L);
        while (MainActivity.current != null && System.currentTimeMillis() < closeDeadline) {
            SystemClock.sleep(25L);
        }
        if (MainActivity.current != null) {
            throw new AssertionError("Activity did not close before inventory reopen");
        }
        String output = uiDevice().executeShellCommand(
                "am start -W -n " + context.getPackageName() + "/com.dobby.ui.MainActivity");
        if (!output.contains("Status: ok")) {
            throw new AssertionError("Activity reopen failed: " + output);
        }
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_REOPEN_STATE_INVALID"));
        waitForUiControl("Profile 1 action", remainingTimeout(deadline, "ANDROID_UI_REOPEN_STATE_INVALID"));
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_REOPEN_STATE_INVALID"));
        SystemClock.sleep(1_100L); // Covers multiple foreground Snapshot polls.
        JSONObject after = snapshotResult("");
        int finalRequests = subscriptionFixtureState().getInt("subscription_gets");
        if (!subscriptionURL.equals(after.optString("source_url"))
                || !before.optString("digest").equals(after.optString("digest"))
                || before.optLong("generation") != after.optLong("generation")
                || !before.optString("state").equals(after.optString("state"))
                || !before.optString("active_digest").equals(after.optString("active_digest"))
                || before.getJSONArray("profiles").length() != after.getJSONArray("profiles").length()
                || finalRequests != requests) {
            throw new AssertionError("Activity reopen or unchanged Snapshot polling reloaded/changed the accepted inventory");
        }
    }

    private String urlWithQuery(String url, String key, String value) {
        return Uri.parse(url).buildUpon().appendQueryParameter(key, value).build().toString();
    }

    private void deliverWarmImport(String subscriptionURL) throws Exception {
        String link = "dobbyvpn://import?url=" + java.net.URLEncoder.encode(subscriptionURL, "UTF-8");
        String output = uiDevice().executeShellCommand("am start -W -a android.intent.action.VIEW -d "
                + link + " " + context.getPackageName());
        if (!output.contains("Status: ok")) throw new AssertionError("ANDROID_IMPORT_ACTIVATION_FAILED");
    }

    private void verifyHeldLoadKeepsControlsResponsive(String subscriptionURL, JSONObject active,
            long deadline) throws Exception {
        int before = subscriptionFixtureState().getInt("subscription_gets");
        subscriptionFixturePost("/hold", new byte[0]);
        String heldURL = urlWithQuery(subscriptionURL, "android-held", "1");
        String intermediateURL = urlWithQuery(subscriptionURL, "android-intermediate", "1");
        String newestURL = urlWithQuery(subscriptionURL, "android-newest", "1");
        expectedRenderedSource = newestURL;
        deliverWarmImport(heldURL);
        waitForSubscriptionGets(before + 1, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject held = waitForInFlightGets(1, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        waitForUiControl("Loading profiles…", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (findUiObject("Retry") != null) {
            throw new AssertionError("Retry appeared before the held subscription load failed");
        }
        deliverWarmImport(heldURL);
        deliverWarmImport(intermediateURL);
        deliverWarmImport(newestURL);
        SystemClock.sleep(200L);
        JSONObject whileHeld = subscriptionFixtureState();
        if (whileHeld.getInt("subscription_gets") != before + 1
                || whileHeld.getInt("in_flight_gets") != 1
                || held.getInt("max_in_flight_gets") > 1) {
            throw new AssertionError("Held request allowed a duplicate or concurrent subscription load");
        }
        assertOldConnectActionsDisabled(active);
        String marker = "held-load-log-" + System.nanoTime();
        NativeUiTestBridge.recordDiagnostic(context, "ui.test.held.load", marker);
        waitForUiControl(marker, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        tapEnabledControl(CONNECTION_ACTION_LABEL, deadline);
        waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject stopped = snapshotResult("");
        if (!"IDLE".equals(stopped.optString("state"))
                || stopped.getLong("generation") != active.getLong("generation")) {
            throw new AssertionError("Stop did not complete while subscription loading was held");
        }
        subscriptionFixturePost("/release", new byte[0]);
        waitForSubscriptionGets(before + 2, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject completed = waitForInFlightGets(0, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject latest = waitForSessionSource(newestURL,
                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        if (completed.getInt("subscription_gets") != before + 2
                || completed.getInt("max_in_flight_gets") != 1
                || !"IDLE".equals(latest.optString("state"))
                || latest.getLong("generation") != stopped.getLong("generation")) {
            throw new AssertionError("Newest request did not win after the held response completed");
        }
    }

    private void verifyTypedURLChangesWhileLoadHeld(String subscriptionURL, long deadline)
            throws Exception {
        int before = subscriptionFixtureState().getInt("subscription_gets");
        String heldURL = urlWithQuery(subscriptionURL, "android-typed-held", "1");
        String intermediateURL = urlWithQuery(subscriptionURL, "android-typed-intermediate", "1");
        String newestURL = urlWithQuery(subscriptionURL, "android-typed-newest", "1");
        expectedRenderedSource = newestURL;
        subscriptionFixturePost("/hold", new byte[0]);
        boolean releaseNeeded = true;
        Throwable primaryFailure = null;
        try {
            tapUiControl("Subscription URL", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            waitForFocusedNativeInput(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"))
                    .setText(heldURL);
            waitForSubscriptionGets(before + 1,
                    remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            JSONObject held = waitForInFlightGets(1,
                    remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            if (held.getInt("subscription_gets") != before + 1
                    || held.getInt("max_in_flight_gets") > 1) {
                throw new AssertionError("Typed edit did not produce one held subscription request");
            }

            waitForFocusedNativeInput(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"))
                    .setText(intermediateURL);
            waitForFocusedNativeInput(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"))
                    .setText(newestURL);
            SystemClock.sleep(500L);
            JSONObject whileHeld = subscriptionFixtureState();
            if (whileHeld.getInt("subscription_gets") != before + 1
                    || whileHeld.getInt("in_flight_gets") != 1
                    || whileHeld.getInt("max_in_flight_gets") > 1) {
                throw new AssertionError("Overlapping typed edits duplicated or overlapped a held request");
            }
            dismissNativeInputIfVisible();
            assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));

            subscriptionFixturePost("/release", new byte[0]);
            releaseNeeded = false;
            JSONObject completed = waitForInFlightGets(0,
                    remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            JSONObject latest = waitForSessionSource(newestURL,
                    remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            if (completed.getInt("subscription_gets") != before + 2
                    || completed.getInt("max_in_flight_gets") != 1
                    || !newestURL.equals(latest.optString("source_url"))
                    || !latest.optBoolean("configured")) {
                throw new AssertionError("Latest typed edit did not win after the held response: " + latest);
            }
        } catch (Exception | Error failure) {
            primaryFailure = failure;
            throw failure;
        } finally {
            if (releaseNeeded) {
                try {
                    subscriptionFixturePost("/release", new byte[0]);
                } catch (Exception releaseFailure) {
                    if (primaryFailure != null) primaryFailure.addSuppressed(releaseFailure);
                    else throw releaseFailure;
                }
            }
        }
    }

    private void verifyRenderedStopCancelsPendingSwitch(int targetIndex, long deadline)
            throws Exception {
        JSONObject before = snapshotResult("");
        if (!"CONNECTED".equals(before.optString("state"))) {
            throw new AssertionError("Pending-switch Stop scenario did not start connected: " + before);
        }
        scrollControlsToProfileAction(
                targetIndex, before.getJSONArray("profiles").length(), deadline);
        tapEnabledControl("Profile " + (targetIndex + 1) + " action", deadline);
        String digest = before.optString("digest");
        while (System.currentTimeMillis() < deadline) {
            JSONObject pendingState = snapshotResult("");
            JSONObject pending = pendingState.optJSONObject("pending_target");
            if (pending != null && "PROFILE_INDEX".equals(pending.optString("mode"))
                    && pending.optInt("index", -1) == targetIndex
                    && digest.equals(pending.optString("digest"))) {
                tapEnabledControl("Stop", deadline);
                waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_SWITCH_STOP_TIMEOUT"));
                JSONObject stopped = snapshotResult("");
                if (!"IDLE".equals(stopped.optString("state"))
                        || stopped.optJSONObject("pending_target") != null
                        || stopped.getLong("generation") <= before.getLong("generation")) {
                    throw new AssertionError("Rendered Stop did not cancel the pending profile switch: " + stopped);
                }
                Thread.sleep(300L);
                JSONObject settled = snapshotResult("");
                if (!"IDLE".equals(settled.optString("state"))
                        || settled.optJSONObject("pending_target") != null
                        || settled.getLong("generation") != stopped.getLong("generation")) {
                    throw new AssertionError("Canceled switch connected after rendered Stop: " + settled);
                }
                return;
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new AssertionError("Profile switch did not expose a pending target for Stop cancellation");
    }

    private JSONObject verifyNativeRevokeCancelsPendingSwitch(JSONObject connected, long deadline)
            throws Exception {
        if (!"CONNECTED".equals(connected.optString("state"))) {
            throw new AssertionError("Native revoke scenario did not start connected: " + connected);
        }
        int activeIndex = connected.getJSONObject("active_profile").getInt("index");
        int targetIndex = activeIndex == 0 ? 1 : 0;
        int profileCount = connected.getJSONArray("profiles").length();
        String digest = connected.optString("digest");
        JSONObject pendingState = null;
        Object service = currentNativeVpnService();
        // The real service's synchronized release callback holds cleanup at the
        // pending-target boundary until the OS lifecycle callback has fenced it.
        scrollControlsToProfileAction(targetIndex, profileCount, deadline);
        synchronized (service) {
            tapEnabledControl("Profile " + (targetIndex + 1) + " action", deadline);
            while (System.currentTimeMillis() < deadline) {
                JSONObject current = snapshotResult("");
                JSONObject pending = current.optJSONObject("pending_target");
                if (pending != null && "PROFILE_INDEX".equals(pending.optString("mode"))
                        && pending.optInt("index", -1) == targetIndex
                        && digest.equals(pending.optString("digest"))) {
                    pendingState = current;
                    break;
                }
                Thread.sleep(10L);
            }
            if (pendingState == null) {
                throw new AssertionError("Profile switch did not expose a pending target before native revoke");
            }
            // Invoke the same callback body while holding the service monitor;
            // the owner then cancels before the held release can let a new
            // profile attempt proceed.
            ((DobbyVpnService) service).onRevoke();
        }

        JSONObject cancelled = null;
        while (System.currentTimeMillis() < deadline) {
            JSONObject current = snapshotResult("");
            if ("FAILED".equals(current.optString("state"))) {
                throw new AssertionError("Native revoke failed while cancelling the pending switch: " + current);
            }
            if ("IDLE".equals(current.optString("state"))
                    && current.optJSONObject("pending_target") == null) {
                cancelled = current;
                break;
            }
            Thread.sleep(POLL_MILLIS);
        }
        if (cancelled == null || cancelled.getLong("generation") <= connected.getLong("generation")) {
            throw new AssertionError("Native revoke did not clear the pending switch: " + snapshotResult(""));
        }
        waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_NATIVE_REVOKE_TIMEOUT"));
        SystemClock.sleep(300L);
        JSONObject settled = snapshotResult("");
        if (!"IDLE".equals(settled.optString("state"))
                || settled.optJSONObject("pending_target") != null
                || settled.getLong("generation") != cancelled.getLong("generation")
                || awaitVpnNetwork(false, remainingTimeout(deadline, "ANDROID_NATIVE_REVOKE_TIMEOUT")) != null) {
            throw new AssertionError("Canceled replacement started after native revoke: " + settled);
        }

        scrollControlsToProfileAction(activeIndex, profileCount, deadline);
        tapEnabledControl("Profile " + (activeIndex + 1) + " action", deadline);
        JSONObject resumed = awaitSelection(
                settled.getLong("generation"), "PROFILE_INDEX", activeIndex, deadline);
        markProgress("configure", "native-revoke-canceled-pending-switch", "completed");
        return resumed;
    }

    private Object currentNativeVpnService() throws Exception {
        Object bridge = NativeVpnBridge.class.getField("INSTANCE").get(null);
        java.lang.reflect.Field field = NativeVpnBridge.class.getDeclaredField("service");
        field.setAccessible(true);
        Object service = field.get(bridge);
        if (!(service instanceof DobbyVpnService)) {
            throw new AssertionError("ANDROID_NATIVE_REVOKE_SERVICE_UNAVAILABLE");
        }
        return service;
    }

    private void verifyReplacementInventoryWhileOldProfileIsActive(String subscriptionURL,
            long deadline) throws Exception {
        JSONObject before = snapshotResult("");
        tapEnabledControl("Profile 1 action", deadline);
        JSONObject active = awaitSelection(before.getLong("generation"), "PROFILE_INDEX", 0, deadline);
        String oldDescription = active.getJSONObject("active_profile").optString("description");
        byte[] replacement = syntheticOutlineInventory(1, "Replacement inventory only");
        subscriptionFixturePost("/profile", replacement);
        int requests = subscriptionFixtureState().getInt("subscription_gets");
        String replacementURL = urlWithQuery(subscriptionURL, "android-inventory", "replacement");
        expectedRenderedSource = replacementURL;
        deliverWarmImport(replacementURL);
        waitForSubscriptionGets(requests + 1, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        waitForInFlightGets(0, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject changed = waitForSessionSource(replacementURL,
                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONArray profiles = changed.getJSONArray("profiles");
        if (!"CONNECTED".equals(changed.optString("state"))
                || changed.getLong("generation") != active.getLong("generation")
                || changed.getString("active_digest").equals(changed.getString("digest"))
                || profiles.length() != 1
                || !"Replacement inventory only".equals(profiles.getJSONObject(0).optString("description"))
                || oldDescription.equals(profiles.getJSONObject(0).optString("description"))) {
            throw new AssertionError("New inventory displaced or hid the still-active profile");
        }
        if (!oldDescription.isEmpty()) waitForUiControl(oldDescription, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        waitForUiControl("Replacement inventory only", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        tapEnabledControl("Disconnect", deadline);
        waitForUiState("Disconnected", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject stopped = snapshotResult("");
        if (!"IDLE".equals(stopped.optString("state"))
                || stopped.getLong("generation") != active.getLong("generation")) {
            throw new AssertionError("Visible Stop control could not end an absent-profile connection");
        }
    }

    private void verifyLongListAndValidPaste(String subscriptionURL, long deadline) throws Exception {
        byte[] longList = syntheticOutlineInventory(24, "long-list");
        subscriptionFixturePost("/profile", longList);
        String pastedURL = urlWithQuery(subscriptionURL, "android-paste", "valid");
        expectedRenderedSource = pastedURL;
        ClipboardManager clipboard = context.getSystemService(ClipboardManager.class);
        try {
            clipboard.setPrimaryClip(ClipData.newPlainText("subscription", pastedURL));
            waitForUiControl("Paste", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            int before = subscriptionFixtureState().getInt("subscription_gets");
            long pasteStarted = SystemClock.elapsedRealtime();
            tapUiControl("Paste", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            JSONObject pasteRequest = waitForSubscriptionGets(before + 1,
                    remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            if (pasteRequest.getInt("subscription_gets") != before + 1
                    || SystemClock.elapsedRealtime() - pasteStarted >= 350L) {
                throw new AssertionError("Paste did not request its URL immediately");
            }
            waitForInFlightGets(0, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            JSONObject loaded = waitForSessionSource(pastedURL,
                    remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            JSONArray profiles = loaded.getJSONArray("profiles");
            if (profiles.length() != 24 || !"Profile 2 long-list".equals(profiles.getJSONObject(1).optString("description"))
                    || !"OUTLINE".equals(profiles.getJSONObject(0).optString("protocol"))
                    || !"".equals(profiles.getJSONObject(0).optString("description"))
                    || !"Profile 24 long-list".equals(profiles.getJSONObject(23).optString("description"))) {
                throw new AssertionError("Ordered profile description, protocol, or fallback inventory was not retained");
            }
            assertRenderedProfileInventory(profiles);
            verifyBareLinkPreservesSource(deadline);
            scrollControlsToLastProfile(deadline);
            waitForUiControl("Profile 24 long-list", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
            waitForEnabledControl("Profile 24 action", deadline);
            assertLongListLeavesLogsUsable(deadline);
        } finally {
            clipboard.clearPrimaryClip();
        }
    }

    private void assertLongListLeavesLogsUsable(long deadline) throws Exception {
        UiDevice device = uiDevice();
        int width = device.getDisplayWidth();
        int height = device.getDisplayHeight();
        while (System.currentTimeMillis() < deadline) {
            UiObject2 logs = findUiObject("Connection logs");
            if (logs != null && logs.getVisibleBounds().height() >= 24) return;
            device.swipe(width / 2, height * 2 / 3, width / 2, height / 4, 12);
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new AssertionError("Long profile inventory left no usable log viewport");
    }

    private void verifyBareLinkPreservesSource(long deadline) throws Exception {
        JSONObject before = snapshotResult("");
        expectedRenderedSource = before.optString("source_url");
        int requestsBefore = subscriptionFixtureState().getInt("subscription_gets");
        Activity activity = MainActivity.current;
        if (activity == null) throw new AssertionError("Android Activity missing before bare-link delivery");
        String output = launchBareLink();
        if (!output.contains("Status: ok")) {
            throw new AssertionError("Bare deep-link activation failed: " + output);
        }
        if (MainActivity.current != activity) {
            throw new AssertionError("Bare deep link replaced the existing Activity");
        }
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        JSONObject after = snapshotResult("");
        if (!before.optString("source_url").equals(after.optString("source_url"))
                || !before.optString("digest").equals(after.optString("digest"))
                || before.optLong("generation") != after.optLong("generation")
                || !before.optString("state").equals(after.optString("state"))) {
            throw new AssertionError("Bare deep link changed the accepted source or session");
        }
        if (findUiObject("Error") != null) {
            throw new AssertionError("Bare deep link showed an import error");
        }

        InstrumentationRegistry.getInstrumentation().runOnMainSync(activity::finishAndRemoveTask);
        long closeDeadline = Math.min(deadline, System.currentTimeMillis() + 5_000L);
        while (MainActivity.current != null && System.currentTimeMillis() < closeDeadline) {
            SystemClock.sleep(25L);
        }
        if (MainActivity.current != null) {
            throw new AssertionError("Activity did not close before cold bare-link delivery");
        }
        output = launchBareLink();
        if (!output.contains("Status: ok")) {
            throw new AssertionError("Cold bare deep-link activation failed: " + output);
        }
        foregroundActivity = ensureForegroundActivity();
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        Activity reopened = MainActivity.current;
        if (reopened == null || reopened == activity) {
            throw new AssertionError("Cold bare deep link did not open a new Activity");
        }
        assertRenderedSourceRetained(remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
        SystemClock.sleep(1_100L);
        JSONObject coldAfter = snapshotResult("");
        int requestsAfter = subscriptionFixtureState().getInt("subscription_gets");
        if (!before.optString("source_url").equals(coldAfter.optString("source_url"))
                || !before.optString("digest").equals(coldAfter.optString("digest"))
                || before.optLong("generation") != coldAfter.optLong("generation")
                || !before.optString("state").equals(coldAfter.optString("state"))
                || requestsBefore != requestsAfter
                || findUiObject("Error") != null) {
            throw new AssertionError("Cold bare deep link changed the source/session or triggered an import");
        }
    }

    private String launchBareLink() throws Exception {
        return uiDevice().executeShellCommand("am start -W -a android.intent.action.VIEW -d dobbyvpn:// "
                + context.getPackageName());
    }

    private void assertRenderedProfileInventory(JSONArray profiles) throws Exception {
        ArrayList<String> renderedTexts = new ArrayList<>();
        ArrayList<String> renderedDescriptions = new ArrayList<>();
        AtomicReference<String> failure = new AtomicReference<>();
        String lastName = profiles.getJSONObject(profiles.length() - 1).optString("description");
        String lastAction = "Profile " + profiles.length() + " action";
        long deadline = System.currentTimeMillis() + 5_000L;
        while (System.currentTimeMillis() < deadline) {
            renderedTexts.clear();
            renderedDescriptions.clear();
            failure.set(null);
            InstrumentationRegistry.getInstrumentation().runOnMainSync(() -> {
                Activity activity = MainActivity.current;
                View content = activity == null ? null : activity.findViewById(android.R.id.content);
                SemanticsNode root = content == null ? null : findComposeRootSemantics(content);
                if (root == null) {
                    failure.set("Compose semantics tree is unavailable");
                    return;
                }
                collectRenderedSemantics(root, renderedTexts, renderedDescriptions);
            });
            if (failure.get() == null
                    && renderedTexts.contains(lastName)
                    && renderedDescriptions.contains(lastAction)) {
                break;
            }
            Thread.sleep(POLL_MILLIS);
        }
        if (failure.get() != null || !renderedTexts.contains(lastName)
                || !renderedDescriptions.contains(lastAction)) {
            throw new AssertionError("Rendered profile inventory could not be inspected: "
                    + (failure.get() == null ? "last row did not appear" : failure.get()));
        }

        int textCursor = 0;
        int actionCursor = 0;
        int connectLabels = 0;
        for (int index = 0; index < profiles.length(); index++) {
            JSONObject profile = profiles.getJSONObject(index);
            String description = profile.optString("description");
            String expectedDescription = index == 0 ? "" : "Profile " + (index + 1) + " long-list";
            String expectedName = description.isEmpty() ? "Profile " + (index + 1) : description;
            if (profile.optInt("index", -1) != index
                    || !expectedDescription.equals(description)
                    || !"OUTLINE".equals(profile.optString("protocol"))) {
                throw new AssertionError("Synthetic profile metadata/order changed at index " + index);
            }
            textCursor = requireRenderedValue(
                    renderedTexts, expectedName, textCursor, "profile name/order");
            textCursor = requireRenderedValue(
                    renderedTexts, profile.optString("protocol"), textCursor, "profile protocol/order");
            actionCursor = requireRenderedValue(
                    renderedDescriptions, "Profile " + (index + 1) + " action", actionCursor,
                    "numbered profile action/order");
        }
        for (String text : renderedTexts) {
            if ("Connect".equals(text)) connectLabels++;
        }
        if (connectLabels != profiles.length()) {
            throw new AssertionError("Rendered Connect actions missing: expected "
                    + profiles.length() + " labels, found " + connectLabels);
        }
    }

    private int requireRenderedValue(List<String> values, String expected, int start, String field) {
        for (int index = start; index < values.size(); index++) {
            if (expected.equals(values.get(index))) return index + 1;
        }
        throw new AssertionError("Rendered " + field + " missing or out of source order: " + expected);
    }

    private static SemanticsNode findComposeRootSemantics(View view) {
        if (view instanceof ViewRootForTest) {
            return ((ViewRootForTest) view)
                    .getSemanticsOwner()
                    .getUnmergedRootSemanticsNode();
        }
        if (view instanceof ViewGroup) {
            ViewGroup group = (ViewGroup) view;
            for (int index = 0; index < group.getChildCount(); index++) {
                SemanticsNode root = findComposeRootSemantics(group.getChildAt(index));
                if (root != null) return root;
            }
        }
        return null;
    }

    private static void collectRenderedSemantics(
            SemanticsNode node, List<String> texts, List<String> descriptions) {
        SemanticsPropertyKey<List<AnnotatedString>> textKey = SemanticsProperties.INSTANCE.getText();
        if (node.getConfig().contains(textKey)) {
            for (AnnotatedString text : node.getConfig().get(textKey)) {
                texts.add(text.getText());
            }
        }
        SemanticsPropertyKey<List<String>> descriptionKey =
                SemanticsProperties.INSTANCE.getContentDescription();
        if (node.getConfig().contains(descriptionKey)) {
            descriptions.addAll(node.getConfig().get(descriptionKey));
        }
        for (SemanticsNode child : node.getChildren()) {
            collectRenderedSemantics(child, texts, descriptions);
        }
    }

    private byte[] syntheticOutlineInventory(int count, String descriptionPrefix) {
        StringBuilder profile = new StringBuilder();
        for (int index = 0; index < count; index++) {
            String description = index == 0 && count > 1 ? "" :
                    (count == 1 ? descriptionPrefix : "Profile " + (index + 1) + " " + descriptionPrefix);
            profile.append("[[Outline]]\nDescription = \"").append(description).append("\"\n")
                    .append("Server = \"198.51.100.10\"\nPort = 443\nPassword = \"synthetic-password\"\n\n");
        }
        return profile.toString().getBytes(StandardCharsets.UTF_8);
    }

    private String dumpUiHierarchy() throws Exception {
        ByteArrayOutputStream hierarchy = new ByteArrayOutputStream();
        uiDevice().dumpWindowHierarchy(hierarchy);
        return hierarchy.toString(StandardCharsets.UTF_8.name());
    }

    private void scrollControlsToLastProfile(long deadline) throws Exception {
        scrollControlsToProfileAction(23, 24, deadline);
    }

    private void scrollControlsToProfileAction(int targetIndex, int profileCount, long deadline)
            throws Exception {
        UiDevice device = uiDevice();
        int firstVisible = -1;
        int lastVisible = -1;
        int swipes = 0;
        UiLookupCounters firstTargetLookup = new UiLookupCounters();
        String firstTargetNodeDiagnostics = null;
        List<Throwable> firstTargetDiagnosticFailures = new ArrayList<>();
        UiObject2 enumeratedTarget = null;
        UiObject2 enumeratedTargetAncestor = null;
        String targetLabel = "Profile " + (targetIndex + 1) + " action";
        while (swipes <= profileCount + 2 && System.currentTimeMillis() < deadline) {
            UiObject2 viewport = findUiObject("Connection controls");
            Rect viewportBounds = viewport == null ? new Rect() : viewport.getVisibleBounds();
            if (viewportBounds.isEmpty()) {
                SystemClock.sleep(POLL_MILLIS);
                continue;
            }
            UiLookupCounters targetLookup = new UiLookupCounters();
            targetLookup.attempts++;
            UiObject2 target = findUiObject(targetLabel, targetLookup);
            UiObject2 targetButton = target;
            while (targetButton != null && !targetButton.isClickable()) {
                targetButton = targetButton.getParent();
            }
            if (firstTargetLookup.attempts == 0) {
                firstTargetLookup = targetLookup;
                StringBuilder firstNodes = new StringBuilder();
                appendStoppedRecoveryNodeFields(
                        firstNodes, "first_target_node", target, firstTargetDiagnosticFailures);
                appendStoppedRecoveryNodeFields(
                        firstNodes, "first_clickable_ancestor", targetButton,
                        firstTargetDiagnosticFailures);
                firstTargetNodeDiagnostics = firstNodes.toString();
            }
            if (targetButton != null
                    && Rect.intersects(viewportBounds, targetButton.getVisibleBounds())) {
                return;
            }

            firstVisible = -1;
            lastVisible = -1;
            enumeratedTarget = null;
            enumeratedTargetAncestor = null;
            for (int index = 0; index < profileCount; index++) {
                UiObject2 visibleAction = findUiObject("Profile " + (index + 1) + " action");
                if (index == targetIndex) {
                    enumeratedTarget = visibleAction;
                    enumeratedTargetAncestor = visibleAction;
                    while (enumeratedTargetAncestor != null
                            && !enumeratedTargetAncestor.isClickable()) {
                        enumeratedTargetAncestor = enumeratedTargetAncestor.getParent();
                    }
                }
                if (visibleAction == null
                        || !Rect.intersects(viewportBounds, visibleAction.getVisibleBounds())) {
                    continue;
                }
                if (firstVisible < 0) firstVisible = index;
                lastVisible = index;
            }
            boolean targetCandidateVisible = enumeratedTarget != null
                    && Rect.intersects(viewportBounds, enumeratedTarget.getVisibleBounds());
            if (targetCandidateVisible) {
                if (enumeratedTargetAncestor != null
                        && Rect.intersects(
                                viewportBounds, enumeratedTargetAncestor.getVisibleBounds())) {
                    return;
                }
                throw profileActionLookupFailure(
                        "visible target candidate has no intersecting clickable action",
                        targetIndex, viewportBounds, firstVisible, lastVisible,
                        firstTargetLookup, firstTargetNodeDiagnostics,
                        firstTargetDiagnosticFailures,
                        enumeratedTarget, enumeratedTargetAncestor);
            }
            boolean targetWithinVisibleRange = firstVisible >= 0
                    && targetIndex >= firstVisible && targetIndex <= lastVisible;
            if (targetWithinVisibleRange) {
                waitForIdleBounded(device, deadline);
                SystemClock.sleep(POLL_MILLIS);
                continue;
            }
            boolean towardLaterProfiles = firstVisible < 0 || targetIndex > lastVisible;
            if (swipes == profileCount + 2
                    || !swipeControlsViewport(device, viewportBounds, towardLaterProfiles)) {
                throw profileActionLookupFailure(
                        "bounded controls-viewport swipe could not reveal the target row",
                        targetIndex, viewportBounds, firstVisible, lastVisible,
                        firstTargetLookup, firstTargetNodeDiagnostics,
                        firstTargetDiagnosticFailures,
                        enumeratedTarget, enumeratedTargetAncestor);
            }
            swipes++;
            waitForIdleBounded(device, deadline);
            SystemClock.sleep(POLL_MILLIS);
        }
        AssertionError failure = profileActionVisibilityFailure(
                "target action did not become visible before the existing deadline\n"
                        + firstTargetLookupDiagnostics(
                                firstTargetLookup, firstTargetNodeDiagnostics),
                targetIndex, new Rect(), firstVisible, lastVisible);
        for (Throwable diagnosticFailure : firstTargetDiagnosticFailures) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_PROFILE_ACTION_LOOKUP_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        throw failure;
    }

    private boolean swipeControlsViewport(
            UiDevice device, Rect viewportBounds, boolean towardLaterProfiles) {
        int margin = Math.min(12, Math.max(1, viewportBounds.height() / 10));
        int top = viewportBounds.top + margin;
        int bottom = viewportBounds.bottom - margin;
        if (viewportBounds.isEmpty() || bottom <= top || viewportBounds.width() <= 0) return false;
        int x = viewportBounds.centerX();
        int startY = towardLaterProfiles ? bottom : top;
        int endY = towardLaterProfiles ? top : bottom;
        return device.swipe(x, startY, x, endY, 12);
    }

    private void scrollControlsToTop(int profileCount, long deadline) throws Exception {
        UiDevice device = uiDevice();
        for (int swipes = 0; swipes <= profileCount + 2
                && System.currentTimeMillis() < deadline; swipes++) {
            UiObject2 viewport = findUiObject("Connection controls");
            Rect viewportBounds = viewport == null ? new Rect() : viewport.getVisibleBounds();
            UiObject2 sourceField = findUiObject("Subscription URL");
            if (sourceField != null
                    && !viewportBounds.isEmpty()
                    && Rect.intersects(viewportBounds, sourceField.getVisibleBounds())) {
                return;
            }
            if (swipes == profileCount + 2 || viewportBounds.isEmpty()
                    || !swipeControlsViewport(device, viewportBounds, false)) {
                throw profileActionVisibilityFailure(
                        "could not return the controls viewport to its rendered source field",
                        -1,
                        viewportBounds,
                        -1,
                        -1);
            }
            waitForIdleBounded(device, deadline);
            SystemClock.sleep(POLL_MILLIS);
        }
        throw profileActionVisibilityFailure(
                "source field did not return before the existing deadline",
                -1,
                new Rect(),
                -1,
                -1);
    }

    private int activeProfileIndexInLoadedInventory(JSONObject snapshot) throws Exception {
        if (!"PROFILE_INDEX".equals(snapshot.optString("active_mode"))) return -1;
        String loadedDigest = snapshot.optString("digest");
        if (loadedDigest.isEmpty() || !loadedDigest.equals(snapshot.optString("active_digest"))) {
            return -1;
        }
        int activeIndex = snapshot.optInt("active_index", -1);
        JSONObject activeProfile = snapshot.optJSONObject("active_profile");
        JSONArray profiles = snapshot.optJSONArray("profiles");
        if (activeIndex < 0 || activeProfile == null || profiles == null
                || activeIndex >= profiles.length()) {
            return -1;
        }
        return activeProfile.optInt("index", -1) == activeIndex ? activeIndex : -1;
    }

    private void verifyVisibleProfileDisconnectAction(
            int targetIndex, Rect viewportBounds) throws Exception {
        String profileLabel = "Profile " + (targetIndex + 1) + " action";
        UiObject2 profileAction = findUiObject(profileLabel);
        while (profileAction != null && !profileAction.isClickable()) {
            profileAction = profileAction.getParent();
        }
        UiObject2 disconnectLabel = findUiObject("Disconnect");
        UiObject2 disconnectAction = disconnectLabel;
        while (disconnectAction != null && !disconnectAction.isClickable()) {
            disconnectAction = disconnectAction.getParent();
        }
        Rect profileBounds = profileAction == null ? new Rect() : profileAction.getVisibleBounds();
        Rect labelBounds = disconnectLabel == null ? new Rect() : disconnectLabel.getVisibleBounds();
        Rect disconnectBounds = disconnectAction == null ? new Rect() : disconnectAction.getVisibleBounds();
        if (profileAction == null
                || disconnectLabel == null
                || disconnectAction == null
                || !profileAction.isEnabled()
                || !disconnectAction.isEnabled()
                || viewportBounds.isEmpty()
                || !Rect.intersects(viewportBounds, profileBounds)
                || !Rect.intersects(viewportBounds, labelBounds)
                || !Rect.intersects(viewportBounds, disconnectBounds)
                || !profileBounds.contains(labelBounds)
                || !profileBounds.contains(disconnectBounds)) {
            throw profileActionVisibilityFailure(
                    "Disconnect is not the enabled rendered action for the active inventory row",
                    targetIndex,
                    viewportBounds,
                    -1,
                    -1);
        }
    }

    private AssertionError profileActionVisibilityFailure(
            String reason,
            int targetIndex,
            Rect viewportBounds,
            int minimumVisibleIndex,
            int maximumVisibleIndex) {
        String targetLabel = targetIndex < 0 ? "none" : "Profile " + (targetIndex + 1) + " action";
        String message = "ANDROID_PROFILE_ACTION_VISIBILITY_FAILED: " + reason
                + ", target=" + targetLabel
                + ", viewport=" + viewportBounds.toShortString()
                + ", visible_profile_range=" + minimumVisibleIndex + ".." + maximumVisibleIndex;
        try {
            return new AssertionError(message + "\nui_hierarchy_xml:\n" + dumpUiHierarchy());
        } catch (Exception diagnosticFailure) {
            AssertionError failure = new AssertionError(message);
            failure.addSuppressed(diagnosticFailure);
            return failure;
        }
    }

    private AssertionError profileActionLookupFailure(String reason, int targetIndex,
            Rect viewportBounds, int minimumVisibleIndex, int maximumVisibleIndex,
            UiLookupCounters firstLookup, String firstTargetDiagnostics,
            List<Throwable> firstLookupDiagnosticFailures,
            UiObject2 enumeratedTarget, UiObject2 enumeratedAncestor) {
        StringBuilder diagnostic = new StringBuilder();
        List<Throwable> diagnosticFailures = new ArrayList<>();
        diagnostic.append(firstTargetLookupDiagnostics(firstLookup, firstTargetDiagnostics));
        appendStoppedRecoveryNodeFields(diagnostic, "enumerated_target_node", enumeratedTarget, diagnosticFailures);
        appendStoppedRecoveryNodeFields(diagnostic, "enumerated_clickable_ancestor", enumeratedAncestor, diagnosticFailures);
        AssertionError failure = profileActionVisibilityFailure(
                reason + "\n" + diagnostic, targetIndex, viewportBounds,
                minimumVisibleIndex, maximumVisibleIndex);
        for (Throwable diagnosticFailure : diagnosticFailures) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_PROFILE_ACTION_LOOKUP_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        for (Throwable diagnosticFailure : firstLookupDiagnosticFailures) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_PROFILE_ACTION_LOOKUP_DIAGNOSTIC_FAILED", diagnosticFailure));
        }
        return failure;
    }

    private String firstTargetLookupDiagnostics(UiLookupCounters lookup, String nodeDiagnostics) {
        return "first_target_lookup attempts=" + lookup.attempts
                + " description_candidates=" + lookup.descriptionCandidates
                + " text_candidates=" + lookup.textCandidates
                + " visible_matches=" + lookup.visibleMatches
                + " empty_bounds=" + lookup.emptyBounds
                + " stale_objects=" + lookup.staleObjects + '\n'
                + (nodeDiagnostics == null ? "first_target_nodes=not_observed\n" : nodeDiagnostics);
    }

    private void tapEnabledControl(String label, long deadline) throws Exception {
        while (System.currentTimeMillis() < deadline) {
            UiObject2 control = findUiObject(label);
            while (control != null && !control.isClickable()) control = control.getParent();
            if (control != null && control.isEnabled()) {
                tapUiControl(label, remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
                return;
            }
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new AssertionError("Control did not become enabled: " + label);
    }

    private UiObject2 waitForEnabledControl(String label, long deadline) throws Exception {
        while (System.currentTimeMillis() < deadline) {
            UiObject2 control = findUiObject(label);
            while (control != null && !control.isClickable()) control = control.getParent();
            if (control != null && control.isEnabled()) return control;
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new AssertionError("Control did not become enabled: " + label);
    }

    private JSONObject awaitSelection(long previous, String mode, int index, long deadline) throws Exception {
        while (System.currentTimeMillis() < deadline) {
            JSONObject value = snapshotResult("");
            if ("FAILED".equals(value.optString("state"))) throw new AssertionError("Native selection failed: " + value);
            if ("CONNECTED".equals(value.optString("state")) && value.optLong("generation") > previous
                    && mode.equals(value.optString("active_mode"))
                    && (index < 0 || value.getJSONObject("active_profile").getInt("index") == index)) {
                waitForUiState("Connected", remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
                return value;
            }
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new AssertionError("Native selection did not reach the requested profile: " + mode + "/" + index);
    }

    private void verifyPendingProfileTransition(int targetIndex, int competingIndex,
            String subscriptionURL, long deadline) throws Exception {
        String targetLabel = "Profile " + (targetIndex + 1) + " action";
        String competingLabel = "Profile " + (competingIndex + 1) + " action";
        UiDevice device = uiDevice();
        boolean pendingObserved = false;
        boolean stopEnabled = false;
        boolean targetEnabled = false;
        boolean competingDisabled = false;
        Rect competingBounds = new Rect();
        while (System.currentTimeMillis() < deadline) {
            JSONObject state = snapshotResult("");
            JSONObject pending = state.optJSONObject("pending_target");
            boolean pendingMatches = pending != null && "PROFILE_INDEX".equals(pending.optString("mode"))
                    && pending.optInt("index", -1) == targetIndex
                    && state.optString("digest").equals(pending.optString("digest"));
            if (pendingMatches) {
                pendingObserved = true;
                UiObject2 stop = findUiObject("Stop");
                while (stop != null && !stop.isClickable()) stop = stop.getParent();
                stopEnabled = stop != null && stop.isEnabled();

                UiObject2 target = findUiObject(targetLabel);
                while (target != null && !target.isClickable()) target = target.getParent();
                targetEnabled = target != null && target.isEnabled();

                UiObject2 competing = findUiObject(competingLabel);
                while (competing != null && !competing.isClickable()) competing = competing.getParent();
                competingDisabled = competing != null && !competing.isEnabled();
                competingBounds = competing == null ? new Rect() : competing.getVisibleBounds();

                if (stopEnabled && targetEnabled && competingDisabled && !competingBounds.isEmpty()) {
                    if (!device.click(competingBounds.centerX(), competingBounds.centerY())) {
                        throw new AssertionError("Could not inject a competing tap during profile switching");
                    }
                    JSONObject afterCompetingTap = snapshotResult("");
                    if (!selectionRemainsTarget(afterCompetingTap, targetIndex, state.optString("digest"))) {
                        throw new AssertionError("Competing tap displaced the authoritative profile target");
                    }
                    Activity activity = MainActivity.current;
                    if (activity == null) throw new AssertionError("Android Activity missing during profile selection");
                    int requests = subscriptionFixtureState().getInt("subscription_gets");
                    deliverWarmImport(subscriptionURL);
                    JSONObject afterImport = snapshotResult("");
                    if (MainActivity.current != activity
                            || subscriptionFixtureState().getInt("subscription_gets") != requests
                            || !state.optString("digest").equals(afterImport.optString("digest"))) {
                        throw new AssertionError("Warm import changed the in-flight selection or reloaded its inventory");
                    }
                    if (!selectionRemainsTarget(afterImport, targetIndex, state.optString("digest"))) {
                        throw new AssertionError("Warm import superseded the pending profile selection");
                    }
                    return;
                }
            }
            long remaining = deadline - System.currentTimeMillis();
            if (remaining > 0) SystemClock.sleep(Math.min(20L, remaining));
        }
        if (pendingObserved) {
            if (!stopEnabled) {
                throw new AssertionError("Pending profile selection did not retain an enabled Stop action");
            }
            if (!targetEnabled) {
                throw new AssertionError("Pending profile target did not retain its Stop action");
            }
            if (!competingDisabled) {
                throw new AssertionError("Competing profile Connect remained enabled during switching");
            }
            if (competingBounds.isEmpty()) {
                throw new AssertionError("Could not inject a competing tap during profile switching");
            }
        }
        throw new AssertionError("Pending profile transition was not rendered before selection completed");
    }

    private boolean selectionRemainsTarget(JSONObject snapshot, int targetIndex, String digest)
            throws Exception {
        if (!digest.equals(snapshot.optString("digest"))) return false;
        JSONObject pending = snapshot.optJSONObject("pending_target");
        if (pending != null) {
            return "PROFILE_INDEX".equals(pending.optString("mode"))
                    && pending.optInt("index", -1) == targetIndex
                    && digest.equals(pending.optString("digest"));
        }
        JSONObject selected = snapshot.optJSONObject("active_profile");
        return "CONNECTED".equals(snapshot.optString("state"))
                && selected != null && selected.optInt("index", -1) == targetIndex;
    }

    private void verifyPendingAutoTransition(String subscriptionURL, long deadline) throws Exception {
        boolean pendingObserved = false;
        boolean stopEnabled = false;
        boolean profileActionsDisabled = false;
        while (System.currentTimeMillis() < deadline) {
            JSONObject state = snapshotResult("");
            JSONObject pending = state.optJSONObject("pending_target");
            boolean pendingMatches = pending != null && "AUTO_SELECT".equals(pending.optString("mode"))
                    && state.optString("digest").equals(pending.optString("digest"));
            if (pendingMatches) {
                pendingObserved = true;
                UiObject2 stop = findUiObject("Stop");
                while (stop != null && !stop.isClickable()) stop = stop.getParent();
                stopEnabled = stop != null && stop.isEnabled();
                profileActionsDisabled = true;
                for (int index = 0; index < state.getJSONArray("profiles").length(); index++) {
                    UiObject2 profile = findUiObject("Profile " + (index + 1) + " action");
                    while (profile != null && !profile.isClickable()) profile = profile.getParent();
                    if (profile == null || profile.isEnabled()) {
                        profileActionsDisabled = false;
                        break;
                    }
                }

                if (stopEnabled && profileActionsDisabled) {
                    int requests = subscriptionFixtureState().getInt("subscription_gets");
                    String importedURL = urlWithQuery(subscriptionURL, "android-pending-auto", "1");
                    expectedRenderedSource = importedURL;
                    subscriptionFixturePost("/hold", new byte[0]);
                    long generationDuringLoad = state.optLong("generation");
                    try {
                        deliverWarmImport(importedURL);
                        waitForSubscriptionGets(requests + 1,
                                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
                        JSONObject heldRequest = waitForInFlightGets(1,
                                remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
                        JSONObject duringLoad = snapshotResult("");
                        generationDuringLoad = duringLoad.optLong("generation");
                        if (!autoSelectionRemainsAuthoritative(duringLoad, state.optString("digest"))
                                || heldRequest.getInt("subscription_gets") != requests + 1
                                || heldRequest.getInt("max_in_flight_gets") > 1) {
                            throw new AssertionError("Import changed the pending Auto selection or duplicated its load");
                        }
                    } finally {
                        subscriptionFixturePost("/release", new byte[0]);
                    }
                    JSONObject completed = waitForInFlightGets(0,
                            remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
                    JSONObject afterLoad = waitForSessionSource(importedURL,
                            remainingTimeout(deadline, "ANDROID_UI_STATE_TIMEOUT"));
                    if (completed.getInt("subscription_gets") != requests + 1
                            || generationDuringLoad != afterLoad.optLong("generation")
                            || !autoSelectionRemainsAuthoritative(afterLoad, state.optString("digest"))) {
                        throw new AssertionError("Import interrupted or replaced the authoritative Auto connection");
                    }
                    return;
                }
            }
            long remaining = deadline - System.currentTimeMillis();
            if (remaining > 0) SystemClock.sleep(Math.min(20L, remaining));
        }
        if (pendingObserved) {
            if (!stopEnabled) {
                throw new AssertionError("Auto selection did not expose its enabled Stop action");
            }
            if (!profileActionsDisabled) {
                throw new AssertionError("Profile Connect remained enabled during Auto selection");
            }
        }
        throw new AssertionError("Pending Auto selection was not rendered before connection completed");
    }

    private boolean autoSelectionRemainsAuthoritative(JSONObject snapshot, String digest)
            throws Exception {
        if (!digest.equals(snapshot.optString("digest"))) return false;
        JSONObject pending = snapshot.optJSONObject("pending_target");
        if (pending != null) {
            return "AUTO_SELECT".equals(pending.optString("mode"))
                    && digest.equals(pending.optString("digest"));
        }
        return "CONNECTED".equals(snapshot.optString("state"))
                && "AUTO_SELECT".equals(snapshot.optString("active_mode"));
    }

    private void assertOldConnectActionsDisabled(JSONObject active) throws Exception {
        int activeIndex = active.getJSONObject("active_profile").getInt("index");
        JSONArray profiles = active.getJSONArray("profiles");
        for (int index = 0; index < profiles.length(); index++) {
            UiObject2 button = findUiObject("Profile " + (index + 1) + " action");
            while (button != null && !button.isClickable()) button = button.getParent();
            if (button == null) throw new AssertionError("Old inventory action disappeared during loading");
            if (index == activeIndex) {
                UiObject2 disconnect = findUiObject("Disconnect");
                while (disconnect != null && !disconnect.isClickable()) disconnect = disconnect.getParent();
                if (!button.isEnabled() || disconnect == null || !disconnect.isEnabled()) {
                    throw new AssertionError("Active old profile did not retain only its Disconnect action");
                }
            } else if (button.isEnabled()) {
                throw new AssertionError("A stale old-inventory Connect action remained enabled during loading");
            }
        }
    }

    private void assertRenderedSourceRetained(long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            UiObject2 input = findNativeInput();
            UiObject2 label = findUiObject("Subscription URL");
            String visibleText = input == null ? null : input.getText();
            if (input != null
                    && label != null
                    && visibleText != null
                    && !visibleText.isEmpty()
                    && expectedRenderedSource.equals(readComposeEditableText())) {
                return;
            }
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_ACCEPTED_SOURCE_NOT_VISIBLE");
    }

    // Compose truncates accessibility text to 100,000 characters. Read the
    // editor's complete semantics value instead of accepting a partial match.
    private String readComposeEditableText() {
        AtomicReference<String> renderedText = new AtomicReference<>();
        InstrumentationRegistry.getInstrumentation().runOnMainSync(() -> {
            if (foregroundActivity == null) return;
            View content = foregroundActivity.findViewById(android.R.id.content);
            if (content != null) renderedText.set(findComposeEditableText(content));
        });
        return renderedText.get();
    }

    private static String findComposeEditableText(View view) {
        if (view instanceof ViewRootForTest) {
            SemanticsNode root = ((ViewRootForTest) view)
                    .getSemanticsOwner()
                    .getUnmergedRootSemanticsNode();
            SemanticsPropertyKey<AnnotatedString> editableTextKey =
                    SemanticsProperties.INSTANCE.getEditableText();
            ArrayList<SemanticsNode> pending = new ArrayList<>();
            pending.add(root);
            String result = null;
            int matches = 0;
            for (int index = 0; index < pending.size(); index++) {
                SemanticsNode node = pending.get(index);
                if (node.getConfig().contains(editableTextKey)) {
                    matches++;
                    result = node.getConfig().get(editableTextKey).getText();
                }
                pending.addAll(node.getChildren());
            }
            return matches == 1 ? result : null;
        }
        if (view instanceof ViewGroup) {
            ViewGroup group = (ViewGroup) view;
            for (int index = 0; index < group.getChildCount(); index++) {
                String result = findComposeEditableText(group.getChildAt(index));
                if (result != null) return result;
            }
        }
        return null;
    }

    private UiObject2 findNativeInput() {
        return findVisibleUiObject(uiDevice().findObjects(
                By.clazz("android.widget.EditText").pkg(context.getPackageName())));
    }

    private UiObject2 waitForFocusedNativeInput(long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        while (System.currentTimeMillis() < deadline) {
            UiObject2 input = findNativeInput();
            if (input != null && input.isFocused()) return input;
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_UI_INPUT_FOCUS_TIMEOUT");
    }

    private void disconnectThroughRenderedUI(long timeout) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        markProgress("disconnect", "surface", "started");
        ensureUiSurface(remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        markProgress("disconnect", "surface", "completed");
        markProgress("disconnect", "disconnect-control", "started");
        JSONObject expectedConnection = snapshotResult("");
        JSONArray profiles = expectedConnection.optJSONArray("profiles");
        int profileCount = profiles == null ? 0 : profiles.length();
        if ("CONNECTED".equals(expectedConnection.optString("state"))) {
            int activeIndex = activeProfileIndexInLoadedInventory(expectedConnection);
            UiObject2 controls = null;
            Rect controlsBounds = new Rect();
            if (activeIndex >= 0) {
                scrollControlsToProfileAction(
                        activeIndex,
                        profileCount,
                        deadline);
                controls = findUiObject("Connection controls");
                controlsBounds = controls == null ? new Rect() : controls.getVisibleBounds();
                verifyVisibleProfileDisconnectAction(activeIndex, controlsBounds);
            } else {
                // Auto uses the main Disconnect action; an active selection from
                // another inventory uses the standalone action near the top.
                scrollControlsToTop(profileCount, deadline);
            }
            JSONObject current = snapshotResult("");
            if (!sameRenderedDisconnectTarget(expectedConnection, current)) {
                throw profileActionVisibilityFailure(
                        "backend selection changed before the rendered Disconnect action; before="
                                + expectedConnection + ", current=" + current,
                        activeIndex,
                        controlsBounds,
                        -1,
                        -1);
            }
        }
        tapEnabledControl("Disconnect", deadline);
        markProgress("disconnect", "disconnect-control", "completed");
        if ("CONNECTED".equals(expectedConnection.optString("state"))) {
            scrollControlsToTop(profileCount, deadline);
        }
        markProgress("disconnect", "disconnected-state", "started");
        waitForUiState(
                "Disconnected",
                remainingTimeout(deadline, "ANDROID_UI_DISCONNECT_TIMEOUT"));
        markProgress("disconnect", "disconnected-state", "completed");
    }

    private boolean sameRenderedDisconnectTarget(JSONObject expected, JSONObject current) {
        return "CONNECTED".equals(current.optString("state"))
                && expected.optLong("generation", -1L) == current.optLong("generation", -2L)
                && expected.optString("digest").equals(current.optString("digest"))
                && expected.optString("active_digest").equals(current.optString("active_digest"))
                && expected.optString("active_mode").equals(current.optString("active_mode"))
                && expected.optInt("active_index", -1) == current.optInt("active_index", -2)
                && current.optJSONObject("pending_target") == null;
    }

    private JSONObject runAutoRecoveryStop(
            long timeout, long initialGeneration, JSONObject observation) throws Exception {
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        if (initialGeneration <= 0
                || !observation.optBoolean("connected")
                || !observation.optBoolean("tunnel_interface")
                || !observation.optBoolean("routing_verified")
                || !observation.optBoolean("stability_verified")
                || observation.optDouble("latency_ms") <= 0.0
                || observation.optDouble("download_mbps") <= 0.0
                || observation.optDouble("upload_mbps") <= 0.0
                || !observation.optBoolean("vpn_consent_handled")) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_ARM_PRECONDITION_FAILED");
        }
        JSONObject beforeArm = snapshotResult("");
        if (beforeArm.getLong("generation") != initialGeneration
                || !"CONNECTED".equals(beforeArm.optString("state"))
                || beforeArm.optBoolean("recovering")
                || !"AUTO_SELECT".equals(beforeArm.optString("active_mode"))) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_ARM_PRECONDITION_FAILED");
        }

        markProgress("disconnect", "recovery-arm", "started");
        armTestRecoveryStopSeam();
        markProgress("disconnect", "recovery-arm", "completed");
        RecoveryStopUiState recoveryState = awaitRenderedRecoveryStop(
                initialGeneration, deadline);
        JSONObject recovery = recoveryState.snapshot;
        long recoveryGeneration = recovery.getLong("generation");
        int profileActionCount = recoveryState.profileActionCount;

        markProgress("disconnect", "recovery-stop-state", "observed");
        String stopScreenshot = latestScreenshotLabel();
        UiObject2 mainAction = findUiObject(CONNECTION_ACTION_LABEL);
        while (mainAction != null && !mainAction.isClickable()) mainAction = mainAction.getParent();
        if (mainAction == null || !mainAction.isEnabled()) {
            throw new IllegalStateException("ANDROID_TEST_RECOVERY_STOP_UI_INVALID");
        }
        markProgress("disconnect", "recovery-stop-tap", "started");
        try {
            tapUiControl(
                    CONNECTION_ACTION_LABEL,
                    remainingTimeout(
                            deadline, "ANDROID_TEST_RECOVERY_STOP_TAP_FAILED"));
        } catch (Throwable failure) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_TAP_FAILED", failure);
        }
        markProgress("disconnect", "recovery-stop-tap", "completed");

        JSONObject idle = awaitRecoveryStopCleanup(
                recoveryGeneration, deadline);
        markProgress("disconnect", "stopped-state", "started");
        JSONObject renderedStop = verifyStoppedRecoveryRendering(idle, deadline);
        markProgress("disconnect", "stopped-state", "completed");
        String stoppedScreenshot = latestScreenshotLabel();
        int restoredProfileActionCount = verifyRecoveryProfileActions(
                idle.optJSONArray("profiles"), deadline, true);
        if (restoredProfileActionCount != profileActionCount) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_NOT_RESTORED");
        }
        renderedStop = returnToStoppedRecoverySurface(recoveryGeneration, deadline);
        for (int sample = 0; sample < UI_STABILITY_SAMPLES; sample++) {
            idle = snapshotResult("");
            if (!isStoppedRecoverySnapshot(idle, recoveryGeneration)) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_GENERATION_ADVANCED");
            }
            if (awaitVpnNetwork(
                            false,
                            Math.min(
                                    NETWORK_RECOVERY_TIMEOUT_MILLIS,
                                    remainingTimeout(
                                            deadline,
                                            "ANDROID_TEST_RECOVERY_STOP_CLEANUP_TIMEOUT")))
                    != null) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_FINAL_STATE_INVALID");
            }
            if (sample + 1 < UI_STABILITY_SAMPLES) {
                SystemClock.sleep(POLL_MILLIS);
            }
        }
        if (stopScreenshot.isEmpty() || stoppedScreenshot.isEmpty()) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_SCREENSHOT_MISSING");
        }

        JSONArray screenshotLabels = new JSONArray();
        for (int i = 0; i < screenshotHistory.length(); i++) {
            screenshotLabels.put(screenshotHistory.getJSONObject(i).getString("label"));
        }
        return new JSONObject()
                .put("case_id", AUTO_RECOVERY_STOP_TEST_CASE)
                .put("passed", true)
                .put("seam_enabled", true)
                .put("seam_armed", true)
                .put("arm_after_tunnel_route_and_traffic", true)
                .put("initial_generation", initialGeneration)
                .put("recovery_generation", recoveryGeneration)
                .put("recovery_observed", true)
                .put("recovery_hold_sample_count", UI_STABILITY_SAMPLES)
                .put("recovery_poll_interval_ms", POLL_MILLIS)
                .put("recovery_ui_reconnecting", true)
                .put("main_stop_visible", true)
                .put("main_stop_enabled", true)
                .put("competing_profile_action_count", profileActionCount)
                .put("competing_profile_actions_disabled", true)
                .put("profile_actions_verified_while_visible", true)
                .put("competing_profile_actions_enabled_after_stop", true)
                .put("primary_auto_connect_enabled", true)
                .put("rendered_status", renderedStop.getString("status"))
                .put("rendered_failure_code", renderedStop.getString("failure_code"))
                .put("rendered_failure_message", renderedStop.getString("failure_message"))
                .put("stop_clicked", true)
                .put("final_generation", idle.getLong("generation"))
                .put("final_idle", "IDLE".equals(idle.optString("state")))
                .put("cleanup_complete", idle.optBoolean("cleanup_complete"))
                .put("recovering_cleared", !idle.optBoolean("recovering"))
                .put("pending_cleared", idle.optJSONObject("pending_target") == null)
                .put("active_profile_cleared", idle.optJSONObject("active_profile") == null)
                .put("vpn_network_absent", true)
                .put("no_later_generation", true)
                .put("final_stable_sample_count", UI_STABILITY_SAMPLES)
                .put("stop_screenshot_label", stopScreenshot)
                .put("stopped_screenshot_label", stoppedScreenshot)
                .put("screenshot_labels", screenshotLabels);
    }

    private JSONObject verifyStoppedRecoveryRendering(JSONObject stopped, long deadline)
            throws Exception {
        long generation = stopped.optLong("generation");
        Exception backendReadFailure = null;
        while (System.currentTimeMillis() < deadline) {
            JSONObject snapshot;
            try {
                snapshot = snapshotResult("");
            } catch (Exception failure) {
                backendReadFailure = failure;
                SystemClock.sleep(POLL_MILLIS);
                continue;
            }
            backendReadFailure = null;
            if (snapshot.optLong("generation") > generation) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_GENERATION_ADVANCED");
            }
            if ("FAILED".equals(snapshot.optString("state"))) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_FINAL_STATE_INVALID");
            }
            JSONObject rendered = null;
            try {
                rendered = stoppedRecoveryRendering(snapshot, generation);
            } catch (StaleObjectException replacedUi) {
                // Retry the full terminal-state predicate against the new tree.
            }
            if (rendered != null && System.currentTimeMillis() < deadline) return rendered;
            SystemClock.sleep(POLL_MILLIS);
        }

        IllegalStateException failure = new IllegalStateException(
                "ANDROID_TEST_RECOVERY_STOP_RENDER_TIMEOUT");
        StringBuilder diagnostic = new StringBuilder()
                .append("expected_generation=").append(generation).append('\n');
        List<Throwable> diagnosticFailures = new ArrayList<>();
        JSONObject diagnosticSnapshot = null;
        try {
            diagnosticSnapshot = snapshotResult("");
            diagnostic.append("backend_snapshot=")
                    .append(diagnosticSnapshot.toString()).append('\n');
        } catch (Throwable collectionFailure) {
            diagnostic.append("backend_snapshot=unavailable\n");
            diagnosticFailures.add(collectionFailure);
        }
        if (backendReadFailure != null) diagnosticFailures.add(backendReadFailure);
        try {
            appendStoppedRecoveryRenderDiagnostics(
                    diagnostic, diagnosticFailures, diagnosticSnapshot);
        } catch (Throwable collectionFailure) {
            diagnostic.append("rendered_ui_metadata=unavailable\n");
            diagnosticFailures.add(collectionFailure);
        }
        try {
            diagnostic.append("ui_hierarchy_xml_begin\n")
                    .append(dumpUiHierarchy())
                    .append("\nui_hierarchy_xml_end\n");
        } catch (Throwable collectionFailure) {
            diagnostic.append("ui_hierarchy_xml=unavailable\n");
            diagnosticFailures.add(collectionFailure);
        }
        failure.addSuppressed(new IllegalStateException(
                "ANDROID_TEST_RECOVERY_STOP_RENDER_DIAGNOSTICS\n" + diagnostic));
        for (Throwable diagnosticFailure : diagnosticFailures) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_RENDER_DIAGNOSTIC_FAILED",
                    diagnosticFailure));
        }
        throw failure;
    }

    private JSONObject stoppedRecoveryRendering(JSONObject snapshot, long generation)
            throws Exception {
        if (!isStoppedRecoverySnapshot(snapshot, generation)) return null;
        UiObject2 failed = findUiObject("Failed");
        UiObject2 disconnected = findUiObject("Disconnected");
        if ((failed == null) == (disconnected == null)) return null;

        JSONObject lastFailure = snapshot.optJSONObject("last_failure");
        String code = lastFailure == null ? "" : lastFailure.optString("code", "");
        String message = lastFailure == null ? "" : lastFailure.optString("message", "");
        String status;
        if (failed != null) {
            if (!AUTO_RECOVERY_STOP_FAILURE_CODE.equals(code)
                    || !AUTO_RECOVERY_STOP_FAILURE_MESSAGE.equals(message)
                    || findUiObject(message + " (" + code + ")") == null) return null;
            status = "Failed";
        } else {
            if (!code.isEmpty() || !message.isEmpty()) return null;
            status = "Disconnected";
        }

        UiObject2 autoConnect = findUiObject("Auto connect");
        UiObject2 action = findUiObject(CONNECTION_ACTION_LABEL);
        while (action != null && !action.isClickable()) action = action.getParent();
        if (autoConnect == null || action == null || !action.isEnabled()) return null;
        Rect actionBounds = action.getVisibleBounds();
        Rect labelBounds = autoConnect.getVisibleBounds();
        if (actionBounds.isEmpty() || labelBounds.isEmpty()
                || !actionBounds.contains(labelBounds)) return null;
        return new JSONObject()
                .put("status", status)
                .put("failure_code", code)
                .put("failure_message", message);
    }

    private void appendStoppedRecoveryRenderDiagnostics(
            StringBuilder diagnostic,
            List<Throwable> diagnosticFailures,
            JSONObject snapshot) {
        List<String> visibleStatuses = new ArrayList<>();
        for (String status : new String[]{
                "Failed", "Disconnected", "Stopping", "Reconnecting",
                "Connecting", "Connected", "Error"}) {
            if (findUiObject(status) != null) visibleStatuses.add(status);
        }
        UiObject2 autoConnect = findUiObject("Auto connect");
        UiObject2 actionLabel = findUiObject(CONNECTION_ACTION_LABEL);
        JSONObject lastFailure = snapshot == null ? null : snapshot.optJSONObject("last_failure");
        String code = lastFailure == null ? "" : lastFailure.optString("code", "");
        String message = lastFailure == null ? "" : lastFailure.optString("message", "");
        UiObject2 renderedFailure = code.isEmpty() && message.isEmpty()
                ? null : findUiObject(message + " (" + code + ")");
        UiObject2 action = actionLabel;
        try {
            while (action != null && !action.isClickable()) action = action.getParent();
        } catch (Throwable readFailure) {
            diagnosticFailures.add(readFailure);
            action = null;
        }
        diagnostic.append("rendered_status=")
                .append(visibleStatuses.size() == 1 ? visibleStatuses.get(0)
                        : visibleStatuses.isEmpty() ? "none" : "ambiguous " + visibleStatuses)
                .append('\n');
        diagnostic.append("failure_code=").append(JSONObject.quote(code)).append('\n')
                .append("failure_message=").append(JSONObject.quote(message)).append('\n')
                .append("expected_injected_failure=")
                .append(AUTO_RECOVERY_STOP_FAILURE_CODE.equals(code)
                        && AUTO_RECOVERY_STOP_FAILURE_MESSAGE.equals(message)).append('\n');
        appendRecoveryActionField(
                diagnostic, "failed_status_visible",
                () -> visibleStatuses.contains("Failed"),
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "disconnected_status_visible",
                () -> visibleStatuses.contains("Disconnected"),
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "rendered_failure_text_visible", () -> renderedFailure != null,
                diagnosticFailures);
        appendStoppedRecoveryNodeFields(
                diagnostic, "auto_connect_label", autoConnect, diagnosticFailures);
        appendStoppedRecoveryNodeFields(
                diagnostic, "action_description_node", actionLabel, diagnosticFailures);
        final UiObject2 actionNode = action;
        final UiObject2 autoConnectNode = autoConnect;
        appendStoppedRecoveryNodeFields(
                diagnostic, "clickable_action_ancestor", actionNode, diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "action_bounds_nonempty",
                () -> actionNode != null && !actionNode.getVisibleBounds().isEmpty(),
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "label_bounds_nonempty",
                () -> autoConnectNode != null
                        && !autoConnectNode.getVisibleBounds().isEmpty(),
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "action_bounds_contain_label",
                () -> actionNode != null && autoConnectNode != null
                        && actionNode.getVisibleBounds()
                                .contains(autoConnectNode.getVisibleBounds()),
                diagnosticFailures);
    }

    private void appendStoppedRecoveryNodeFields(
            StringBuilder diagnostic,
            String prefix,
            UiObject2 node,
            List<Throwable> diagnosticFailures) {
        appendRecoveryActionField(
                diagnostic, prefix + "_present", () -> node != null, diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_bounds",
                () -> node == null ? "null" : node.getVisibleBounds().toShortString(),
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_text",
                () -> node == null ? "null" : node.getText(), diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_description",
                () -> node == null ? "null" : node.getContentDescription(),
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_class",
                () -> node == null ? "null" : node.getClassName(), diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_resource",
                () -> node == null ? "null" : node.getResourceName(), diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_enabled",
                () -> node != null && node.isEnabled(), diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, prefix + "_clickable",
                () -> node != null && node.isClickable(), diagnosticFailures);
    }

    private RecoveryStopUiState awaitRenderedRecoveryStop(
            long initialGeneration, long deadline) throws Exception {
        long recoveryGeneration = 0L;
        int stableSamples = 0;
        int profileActionCount = 0;
        boolean profileActionsVerified = false;
        JSONObject last = snapshotResult("");
        while (System.currentTimeMillis() < deadline) {
            last = snapshotResult("");
            String state = last.optString("state");
            long generation = last.optLong("generation");
            if ("FAILED".equals(state)) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_RECOVERY_INVALID");
            }
            if (generation > initialGeneration && last.optBoolean("recovering")) {
                if (recoveryGeneration == 0L) {
                    recoveryGeneration = generation;
                } else if (generation != recoveryGeneration) {
                    throw new IllegalStateException(
                            "ANDROID_TEST_RECOVERY_STOP_GENERATION_ADVANCED");
                }
                if (!profileActionsVerified && recoveryMainStopVisible(last)) {
                    profileActionCount = verifyRecoveryProfileActions(
                            last.getJSONArray("profiles"), deadline, false);
                    profileActionsVerified = true;
                    returnToRecoveryStop(recoveryGeneration, deadline);
                    last = snapshotResult("");
                }
                if (profileActionsVerified && recoveryMainStopVisible(last)) {
                    stableSamples++;
                    if (stableSamples == UI_STABILITY_SAMPLES) {
                        return new RecoveryStopUiState(last, profileActionCount);
                    }
                } else {
                    stableSamples = 0;
                }
            } else if (generation > initialGeneration && recoveryGeneration > 0L) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_RECOVERY_INVALID");
            }
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException(
                recoveryGeneration == 0L
                        ? "ANDROID_TEST_RECOVERY_STOP_RECOVERY_TIMEOUT"
                        : "ANDROID_TEST_RECOVERY_STOP_UI_INVALID");
    }

    private boolean recoveryMainStopVisible(JSONObject snapshot) throws Exception {
        if (!snapshot.optBoolean("recovering")
                || !"STOP".equals(snapshot.optString("primary_action"))) return false;
        UiObject2 reconnecting = findUiObject("Reconnecting");
        UiObject2 mainAction = findUiObject(CONNECTION_ACTION_LABEL);
        while (mainAction != null && !mainAction.isClickable()) mainAction = mainAction.getParent();
        UiObject2 stopText = findUiObject("Stop");
        if (reconnecting == null || mainAction == null || stopText == null
                || !mainAction.isEnabled() || !stopText.isEnabled()) return false;
        Rect mainBounds = mainAction.getVisibleBounds();
        Rect stopBounds = stopText.getVisibleBounds();
        if (mainBounds.isEmpty() || stopBounds.isEmpty()
                || !Rect.intersects(mainBounds, stopBounds)) return false;
        return true;
    }

    private int verifyRecoveryProfileActions(
            JSONArray profiles, long deadline, boolean expectedEnabled) throws Exception {
        if (profiles == null || profiles.length() < 2) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_PROFILE_INVENTORY_INVALID");
        }
        Set<Integer> expected = new HashSet<>();
        for (int index = 0; index < profiles.length(); index++) {
            expected.add(profiles.getJSONObject(index).getInt("index"));
        }
        if (expected.size() != profiles.length()) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_PROFILE_INVENTORY_INVALID");
        }
        Set<Integer> verified = new HashSet<>();
        UiDevice device = uiDevice();
        int width = device.getDisplayWidth();
        int height = device.getDisplayHeight();
        int maximumSwipes = profiles.length() * 4 + 6;
        int swipes = 0;
        while (verified.size() < expected.size()
                && System.currentTimeMillis() < deadline
                && swipes <= maximumSwipes) {
            for (int profileIndex : expected) {
                if (verified.contains(profileIndex)) continue;
                String label = "Profile " + (profileIndex + 1) + " action";
                UiObject2 action = findUiObject(label);
                if (action == null) continue;
                while (action != null && !action.isClickable()) {
                    action = action.getParent();
                }
                if (action == null) {
                    throw new IllegalStateException(
                            "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_UNAVAILABLE");
                }
                if (action.isEnabled() != expectedEnabled) {
                    if (!expectedEnabled) {
                        throw recoveryProfileActionEnabled(profileIndex, label, action);
                    }
                    throw new IllegalStateException(
                            "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_NOT_RESTORED");
                }
                verified.add(profileIndex);
            }
            if (verified.size() == expected.size()) break;
            if (swipes == maximumSwipes
                    || System.currentTimeMillis() >= deadline) break;
            int startY = Math.max(1, (int) (height * 0.42f));
            int endY = Math.max(1, (int) (height * 0.34f));
            if (width <= 0 || height <= 0 || startY <= endY
                    || !device.swipe(width / 2, startY, width / 2, endY, 8)) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_UNAVAILABLE");
            }
            swipes++;
            waitForIdleBounded(device, deadline);
            SystemClock.sleep(POLL_MILLIS);
        }
        if (verified.size() != expected.size()) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_UNAVAILABLE");
        }
        return verified.size();
    }

    private IllegalStateException recoveryProfileActionEnabled(
            int profileIndex, String label, UiObject2 action) {
        IllegalStateException failure = new IllegalStateException(
                "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_ENABLED");
        StringBuilder diagnostic = new StringBuilder()
                .append("profile_index=").append(profileIndex)
                .append(" label=").append(JSONObject.quote(label))
                .append(" enabled_observed=true\n");
        List<Throwable> diagnosticFailures = new ArrayList<>();
        appendRecoveryActionField(
                diagnostic, "bounds", () -> action.getVisibleBounds().toShortString(),
                diagnosticFailures);
        appendRecoveryActionField(diagnostic, "text", action::getText, diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "description", action::getContentDescription,
                diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "class", action::getClassName, diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "resource", action::getResourceName, diagnosticFailures);
        appendRecoveryActionField(
                diagnostic, "clickable", action::isClickable, diagnosticFailures);
        try {
            JSONObject snapshot = snapshotResult("");
            JSONObject backend = new JSONObject()
                    .put("session_id", snapshot.optString("session_id"))
                    .put("sequence", snapshot.optLong("sequence", -1L))
                    .put("generation", snapshot.optLong("generation", -1L))
                    .put("state", snapshot.optString("state"))
                    .put("recovering", snapshot.optBoolean("recovering"))
                    .put("primary_action", snapshot.optString("primary_action"))
                    .put("configured", snapshot.optBoolean("configured"))
                    .put("can_switch", snapshot.optBoolean("can_switch"))
                    .put("digest", snapshot.optString("digest"))
                    .put("active_digest", snapshot.optString("active_digest"))
                    .put("active_mode", snapshot.optString("active_mode"))
                    .put("active_index", snapshot.optInt("active_index", -1))
                    .put("pending_target", snapshot.opt("pending_target") == null
                            ? JSONObject.NULL : snapshot.opt("pending_target"))
                    .put("active_profile", snapshot.opt("active_profile") == null
                            ? JSONObject.NULL : snapshot.opt("active_profile"))
                    .put("profiles", snapshot.optJSONArray("profiles"));
            diagnostic.append("backend_snapshot=").append(backend).append('\n');
        } catch (Throwable diagnosticFailure) {
            diagnostic.append("backend_snapshot=unavailable\n");
            diagnosticFailures.add(diagnosticFailure);
        }
        try {
            diagnostic.append("ui_hierarchy_xml_begin\n")
                    .append(dumpUiHierarchy())
                    .append("\nui_hierarchy_xml_end\n");
        } catch (Throwable diagnosticFailure) {
            diagnostic.append("ui_hierarchy_xml=unavailable\n");
            diagnosticFailures.add(diagnosticFailure);
        }
        failure.addSuppressed(new IllegalStateException(
                "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_DIAGNOSTICS\n" + diagnostic));
        for (Throwable diagnosticFailure : diagnosticFailures) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_PROFILE_ACTION_DIAGNOSTIC_FAILED",
                    diagnosticFailure));
        }
        return failure;
    }

    private void appendRecoveryActionField(
            StringBuilder diagnostic,
            String name,
            RecoveryDiagnosticValue value,
            List<Throwable> diagnosticFailures) {
        try {
            diagnostic.append(name).append('=')
                    .append(JSONObject.quote(String.valueOf(value.read())))
                    .append('\n');
        } catch (Throwable diagnosticFailure) {
            diagnostic.append(name).append("=unavailable\n");
            diagnosticFailures.add(diagnosticFailure);
        }
    }

    private interface RecoveryDiagnosticValue {
        Object read() throws Throwable;
    }

    private void returnToRecoveryStop(long recoveryGeneration, long deadline)
            throws Exception {
        UiDevice device = uiDevice();
        int width = device.getDisplayWidth();
        int height = device.getDisplayHeight();
        int startY = Math.max(1, (int) (height * 0.34f));
        int endY = Math.max(1, (int) (height * 0.42f));
        int maximumSwipes = 12;
        for (int swipes = 0; swipes <= maximumSwipes; swipes++) {
            JSONObject snapshot = snapshotResult("");
            if (snapshot.optLong("generation") != recoveryGeneration
                    || !snapshot.optBoolean("recovering")) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_RECOVERY_INVALID");
            }
            if (recoveryMainStopVisible(snapshot)) return;
            if (swipes == maximumSwipes
                    || System.currentTimeMillis() >= deadline
                    || width <= 0 || height <= 0
                    || !device.swipe(width / 2, startY, width / 2, endY, 8)) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_UI_INVALID");
            }
            waitForIdleBounded(device, deadline);
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_TEST_RECOVERY_STOP_UI_INVALID");
    }

    private JSONObject returnToStoppedRecoverySurface(long generation, long deadline)
            throws Exception {
        UiDevice device = uiDevice();
        int width = device.getDisplayWidth();
        int height = device.getDisplayHeight();
        int startY = Math.max(1, (int) (height * 0.34f));
        int endY = Math.max(1, (int) (height * 0.42f));
        int maximumSwipes = 12;
        int swipes = 0;
        while (System.currentTimeMillis() < deadline && swipes <= maximumSwipes) {
            JSONObject snapshot = snapshotResult("");
            if (snapshot.optLong("generation") > generation) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_GENERATION_ADVANCED");
            }
            if (!isStoppedRecoverySnapshot(snapshot, generation)) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_FINAL_STATE_INVALID");
            }
            try {
                JSONObject rendered = stoppedRecoveryRendering(snapshot, generation);
                if (rendered != null && System.currentTimeMillis() < deadline) return rendered;
            } catch (StaleObjectException replacedUi) {
                // Retry against the next accessibility tree within the same deadline.
            }
            if (swipes == maximumSwipes
                    || System.currentTimeMillis() >= deadline
                    || width <= 0 || height <= 0
                    || !device.swipe(width / 2, startY, width / 2, endY, 8)) {
                break;
            }
            swipes++;
            waitForIdleBounded(device, deadline);
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_TEST_RECOVERY_STOP_UI_INVALID");
    }

    private JSONObject awaitRecoveryStopCleanup(
            long recoveryGeneration, long deadline) throws Exception {
        while (System.currentTimeMillis() < deadline) {
            JSONObject snapshot = snapshotResult("");
            if ("FAILED".equals(snapshot.optString("state"))) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_FINAL_STATE_INVALID");
            }
            if (snapshot.optLong("generation") > recoveryGeneration) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_GENERATION_ADVANCED");
            }
            if (isStoppedRecoverySnapshot(snapshot, recoveryGeneration)) return snapshot;
            SystemClock.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException(
                "ANDROID_TEST_RECOVERY_STOP_CLEANUP_TIMEOUT");
    }

    private boolean isStoppedRecoverySnapshot(JSONObject snapshot, long generation) {
        return "IDLE".equals(snapshot.optString("state"))
                && snapshot.optLong("generation") == generation
                && snapshot.optBoolean("cleanup_complete")
                && !snapshot.optBoolean("recovering")
                && snapshot.optJSONObject("pending_target") == null
                && snapshot.optJSONObject("active_profile") == null;
    }

    private void enableTestRecoveryStopSeam() {
        try {
            if (!NativeRecoveryStopTestSeam.enable()) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_ENABLE_FAILED");
            }
        } catch (UnsatisfiedLinkError | NoClassDefFoundError failure) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_JNI_UNAVAILABLE", failure);
        }
    }

    private void armTestRecoveryStopSeam() {
        try {
            if (!NativeRecoveryStopTestSeam.arm()) {
                throw new IllegalStateException(
                        "ANDROID_TEST_RECOVERY_STOP_ARM_FAILED");
            }
        } catch (UnsatisfiedLinkError | NoClassDefFoundError failure) {
            throw new IllegalStateException(
                    "ANDROID_TEST_RECOVERY_STOP_JNI_UNAVAILABLE", failure);
        }
    }

    private String latestScreenshotLabel() throws Exception {
        if (screenshotHistory.length() == 0) return "";
        return screenshotHistory.getJSONObject(screenshotHistory.length() - 1)
                .getString("label");
    }

    private void ensureRenderedDisconnected(long timeout) throws Exception {
        JSONObject current = snapshotResult("");
        if (!"IDLE".equals(current.optString("state"))) {
            disconnectThroughRenderedUI(timeout);
            return;
        }
        waitForUiState("Disconnected", timeout);
    }

    /**
     * Reopen the actual Compose activity and verify that the visible state is
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
        return findUiObject(label, null);
    }

    private UiObject2 findUiObject(String label, UiLookupCounters counters) {
        UiDevice device = uiDevice();
        // Prefer stable accessibility descriptions when locating visible controls.
        UiObject2 value = findVisibleUiObject(
                device.findObjects(By.desc(label).pkg(context.getPackageName())), counters,
                "description");
        if (value != null) return value;
        return findVisibleUiObject(
                device.findObjects(By.text(label).pkg(context.getPackageName())), counters,
                "text");
    }

    private UiObject2 findVisibleUiObject(List<UiObject2> candidates) {
        return findVisibleUiObject(candidates, null, "");
    }

    private UiObject2 findVisibleUiObject(
            List<UiObject2> candidates, UiLookupCounters counters, String selectorKind) {
        if (counters != null) counters.recordCandidates(selectorKind, candidates.size());
        for (UiObject2 candidate : candidates) {
            try {
                // Accessibility can retain nodes from a hidden screen
                // while another screen is already rendered. A state assertion
                // must describe the visible UI, not any matching node still
                // present in the package's accessibility tree.
                Rect bounds = candidate.getVisibleBounds();
                if (!bounds.isEmpty()) {
                    if (counters != null) counters.visibleMatches++;
                    return candidate;
                }
                if (counters != null) counters.emptyBounds++;
            } catch (StaleObjectException ignored) {
                // The rendered tree can be replaced between lookup and bounds
                // access. Callers poll again within their existing deadline.
                if (counters != null) counters.staleObjects++;
            }
        }
        return null;
    }

    private UiObject2 waitForUiControl(String label, long timeout) throws Exception {
        long startedAt = SystemClock.uptimeMillis();
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        UiLookupCounters counters = new UiLookupCounters();
        while (System.currentTimeMillis() < deadline) {
            counters.attempts++;
            UiObject2 value = findUiObject(label, counters);
            if (value != null) return value;
            Thread.sleep(POLL_MILLIS);
        }
        throw uiControlTimeout(label, timeout, startedAt, counters, "none");
    }

    private void tapUiControl(String label, long timeout) throws Exception {
        UiDevice device = uiDevice();
        long startedAt = SystemClock.uptimeMillis();
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        UiLookupCounters counters = new UiLookupCounters();
        String lastBounds = "none";
        while (System.currentTimeMillis() < deadline) {
            counters.attempts++;
            UiObject2 value = findUiObject(label, counters);
            if (value != null) {
                try {
                    Rect bounds = value.getVisibleBounds();
                    if (bounds.isEmpty()) {
                        counters.emptyBounds++;
                    } else {
                        lastBounds = bounds.toShortString();
                        if (!device.click(bounds.centerX(), bounds.centerY())) {
                            throw new IllegalStateException("ANDROID_UI_TAP_FAILED");
                        }
                        waitForIdleBounded(device, deadline);
                        return;
                    }
                } catch (StaleObjectException ignored) {
                    // Retry if Compose replaced the visible node after lookup.
                    counters.staleObjects++;
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw uiControlTimeout(label, timeout, startedAt, counters, lastBounds);
    }

    private IllegalStateException uiControlTimeout(
            String label,
            long timeout,
            long startedAt,
            UiLookupCounters counters,
            String lastBounds) {
        String safeLabel = "About".equals(label)
                || "Back".equals(label)
                || "Subscription URL".equals(label)
                || CONNECTION_ACTION_LABEL.equals(label)
                ? label
                : "other";
        IllegalStateException failure = new IllegalStateException(
                "ANDROID_UI_CONTROL_TIMEOUT: label=" + safeLabel
                        + ", selector=By.desc(" + JSONObject.quote(safeLabel)
                        + ")|By.text(" + JSONObject.quote(safeLabel) + ")"
                        + ", package=" + context.getPackageName()
                        + ", timeoutMillis=" + timeout
                        + ", elapsedMillis="
                        + Math.max(0L, SystemClock.uptimeMillis() - startedAt)
                        + ", attempts=" + counters.attempts
                        + ", descriptionCandidates=" + counters.descriptionCandidates
                        + ", textCandidates=" + counters.textCandidates
                        + ", visibleMatches=" + counters.visibleMatches
                        + ", emptyBounds=" + counters.emptyBounds
                        + ", staleObjects=" + counters.staleObjects
                        + ", lastBounds=" + lastBounds);
        try {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_UI_CONTROL_TIMEOUT_DIAGNOSTICS\n"
                            + dumpUiTimeoutContext(label)));
        } catch (Throwable diagnosticFailure) {
            failure.addSuppressed(new IllegalStateException(
                    "ANDROID_UI_CONTROL_TIMEOUT_DIAGNOSTIC_COLLECTION_FAILED",
                    diagnosticFailure));
        }
        return failure;
    }

    /** Capture the currently rendered accessibility state without touching UI. */
    private String dumpUiTimeoutContext(String label) {
        StringBuilder output = new StringBuilder();
        UiDevice device = uiDevice();
        output.append("requested_label=").append(JSONObject.quote(label)).append('\n');
        try {
            output.append("foreground_package=")
                    .append(JSONObject.quote(String.valueOf(device.getCurrentPackageName())))
                    .append('\n');
        } catch (Throwable failure) {
            output.append("foreground_package_error=")
                    .append(JSONObject.quote(CompleteThrowableReporter.format(failure)))
                    .append('\n');
        }

        List<AccessibilityWindowInfo> windows = InstrumentationRegistry.getInstrumentation()
                .getUiAutomation().getWindows();
        output.append("window_count=").append(windows.size()).append('\n');
        for (int index = 0; index < windows.size(); index++) {
            AccessibilityWindowInfo window = windows.get(index);
            Rect bounds = new Rect();
            window.getBoundsInScreen(bounds);
            output.append("window[").append(index).append("] id=").append(window.getId())
                    .append(" type=").append(window.getType())
                    .append(" layer=").append(window.getLayer())
                    .append(" active=").append(window.isActive())
                    .append(" focused=").append(window.isFocused())
                    .append(" bounds=").append(bounds.toShortString())
                    .append(" title=").append(JSONObject.quote(String.valueOf(window.getTitle())))
                    .append('\n');
            AccessibilityNodeInfo root = window.getRoot();
            if (root == null) {
                output.append("  root=null\n");
                continue;
            }
            appendAccessibilityNode(output, root, "  ", "root");
        }
        return output.toString();
    }

    private void appendAccessibilityNode(
            StringBuilder output, AccessibilityNodeInfo node, String indent, String path) {
        Rect bounds = new Rect();
        node.getBoundsInScreen(bounds);
        output.append(indent).append(path)
                .append(" class=").append(JSONObject.quote(String.valueOf(node.getClassName())))
                .append(" package=").append(JSONObject.quote(String.valueOf(node.getPackageName())))
                .append(" resource=").append(JSONObject.quote(String.valueOf(node.getViewIdResourceName())))
                .append(" text=").append(JSONObject.quote(String.valueOf(node.getText())))
                .append(" description=").append(JSONObject.quote(String.valueOf(node.getContentDescription())))
                .append(" bounds=").append(bounds.toShortString())
                .append(" visible=").append(node.isVisibleToUser())
                .append(" focused=").append(node.isFocused())
                .append(" accessibilityFocused=").append(node.isAccessibilityFocused())
                .append(" enabled=").append(node.isEnabled())
                .append(" clickable=").append(node.isClickable())
                .append(" focusable=").append(node.isFocusable())
                .append(" children=").append(node.getChildCount())
                .append('\n');
        for (int index = 0; index < node.getChildCount(); index++) {
            AccessibilityNodeInfo child = node.getChild(index);
            if (child == null) {
                output.append(indent).append(path).append('/').append(index)
                        .append(" child=null\n");
                continue;
            }
            appendAccessibilityNode(output, child, indent + "  ", path + "/" + index);
        }
    }

    private static final class UiLookupCounters {
        int attempts;
        int descriptionCandidates;
        int textCandidates;
        int visibleMatches;
        int emptyBounds;
        int staleObjects;

        void recordCandidates(String selectorKind, int count) {
            if ("description".equals(selectorKind)) {
                descriptionCandidates += count;
            } else if ("text".equals(selectorKind)) {
                textCandidates += count;
            }
        }
    }

    private static final class RecoveryStopUiState {
        final JSONObject snapshot;
        final int profileActionCount;

        RecoveryStopUiState(JSONObject snapshot, int profileActionCount) {
            this.snapshot = snapshot;
            this.profileActionCount = profileActionCount;
        }
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
                    || findUiObject("Connecting") != null
                    || findUiObject("Connected") != null
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
                // category node. Poll for the category within the operation
                // deadline.
                String category = visibleErrorCategory(
                        Math.min(
                                ERROR_CATEGORY_TIMEOUT_MILLIS,
                                remainingTimeout(
                                        deadline, "ANDROID_UI_CONNECT_TIMEOUT")));
                // The visible error can be the previous consent-boundary
                // state while Android applies the grant and the retry starts.
                // After the category poll, continue waiting if the rendered
                // state has advanced to an in-progress connection state.
                String currentState = awaitVisibleConnectionState(
                        Math.min(
                                ERROR_CATEGORY_TIMEOUT_MILLIS,
                                remainingTimeout(
                                        deadline, "ANDROID_UI_CONNECT_TIMEOUT")));
                if (expected.equals(currentState)) return;
                if ("Connecting".equals(currentState)
                        || "Reconnecting".equals(currentState)) {
                    Thread.sleep(POLL_MILLIS);
                    continue;
                }
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
                "Connecting", "Connected", "Error", "Failed", "Disconnected",
        };
        long deadline = System.currentTimeMillis() + Math.max(1L, timeout);
        String fallback = "Unknown";
        while (System.currentTimeMillis() < deadline) {
            for (String state : states) {
                if (findUiObject(state) != null) {
                    fallback = state;
                    if (!"Disconnected".equals(state)) {
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
                if (findVisibleUiObject(device.findObjects(By.textContains(category)
                        .pkg(context.getPackageName()))) != null
                        || findVisibleUiObject(device.findObjects(By.descContains(category)
                                .pkg(context.getPackageName()))) != null) {
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
            if ("FAILED".equals(state)) {
                JSONObject failure = latest.optJSONObject("last_failure");
                String code = failure == null ? "" : failure.optString("code", "");
                String message = failure == null ? "" : failure.optString("message", "");
                String details = code.isEmpty() && message.isEmpty()
                        ? ""
                        : ": backend_code=" + code + " message=" + message;
                throw new IllegalStateException("ANDROID_SESSION_FAILED" + details);
            }
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
        // Compose Activity has been created.
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
        Activity live = findLiveNativeActivity();
        if (live != null) {
            foregroundActivity = live;
            return live;
        }
        foregroundActivity = null;
        Intent launch = context.getPackageManager()
                .getLaunchIntentForPackage(context.getPackageName());
        if (launch == null) throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_MISSING");
        if (!launchSubscriptionURL.isEmpty()) {
            launch = new Intent(Intent.ACTION_VIEW, new Uri.Builder().scheme("dobbyvpn")
                    .authority("import").appendQueryParameter("url", launchSubscriptionURL).build())
                    .setPackage(context.getPackageName());
        }
        // The controller may have launched the production app immediately
        // before --no-restart instrumentation. That Activity was resumed
        // before AndroidX's lifecycle monitor was installed, so it may not be
        // tracked even though it owns the rendered task. Bring the existing
        // task forward with NEW_TASK; the Activity reference and lifecycle
        // monitor both get a chance to observe the resumed screen.
        launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        Intent selectedLaunch = launch;
        try {
            // Schedule only the launch call, then observe the real resumed
            // Activity through the lifecycle monitor.
            instrumentation.runOnMainSync(() -> context.startActivity(selectedLaunch));
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

    /** Return the active Compose Activity even if instrumentation started late. */
    private Activity findLiveNativeActivity() {
        AtomicReference<Activity> live = new AtomicReference<>();
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        instrumentation.runOnMainSync(() -> {
            Activity activity = MainActivity.current;
            if (activity != null
                    && !activity.isFinishing()
                    && !activity.isDestroyed()
                    && activity.hasWindowFocus()
                    && context.getPackageName().equals(activity.getPackageName())) {
                live.set(activity);
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
        // The VPN consent dialog is owned by Android, not by Compose
        // Activity. Prefer stable system resource IDs, then the small set of
        // platform labels observed across API levels and emulator images.
        // System-owned buttons can report an unreliable accessibility
        // isClickable flag on some API levels even though UiObject2.click()
        // is the supported action; enabled plus the explicit selector is
        // sufficient.
        for (String resource : new String[]{
                "android:id/button1",
                "com.android.vpndialogs:id/button1",
                "com.android.about:id/button1",
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
     * Record consent state at timeout to distinguish an absent or foreign
     * system window, a disabled standard action, and a rendered failure.
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
        if (consentDiagnosticFailures.length() > 0) {
            diagnosis.put("errors", consentDiagnosticFailures);
        }
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
        } catch (Throwable failure) {
            recordConsentDiagnosticFailure("foreground_category", failure);
            return "NONE";
        }
    }

    private String consentButton1State(UiDevice device) {
        try {
            UiObject2 button = device.findObject(By.res("android:id/button1"));
            if (button == null) return "ABSENT";
            return button.isEnabled() ? "ENABLED" : "DISABLED";
        } catch (Throwable failure) {
            recordConsentDiagnosticFailure("consent_button_state", failure);
            return "UNAVAILABLE";
        }
    }

    private String vpnPermissionState() {
        try {
            return VpnService.prepare(context) == null ? "GRANTED" : "PENDING";
        } catch (Throwable failure) {
            recordConsentDiagnosticFailure("vpn_permission_state", failure);
            return "UNAVAILABLE";
        }
    }

    private void recordConsentDiagnosticFailure(String check, Throwable failure) {
        try {
            consentDiagnosticFailures.put(new JSONObject()
                    .put("check", check)
                    .put("detail", CompleteThrowableReporter.format(failure)));
        } catch (org.json.JSONException ignored) {
            // Keep the original diagnostic exception available to the complete
            // throwable report if the small JSON record cannot be constructed.
            failure.addSuppressed(ignored);
            consentDiagnosticFailures.put(CompleteThrowableReporter.format(failure));
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
        for (Network network : observedNetworks) {
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
        assertVpnDnsUsesSupportedAddressFamily(vpnProperties);
        URL identity = new URL(identityUrl);
        JSONArray identityAddresses = resolveIdentityIpv4s(physical, identity.getHost());
        JSONObject providerReady = awaitProviderDefaultVpn(deadlineElapsedRealtime);
        JSONObject ready = new JSONObject()
                .put("phase", "ready")
                .put("physical_interface", physicalInterface)
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
            JSONObject directProbe = networkRequest(
                    phasePhysical, identity.toString(), directRequired);
            JSONObject vpnProbe = routingProviderRequest(
                    identity.toString(), deadlineElapsedRealtime);
            JSONObject response = new JSONObject().put("phase", phase)
                    .put("direct", directProbe)
                    // Run the positive oracle in the ordinary test-APK
                    // provider process. Its default route is deliberately
                    // unbound, and its UID is checked against the test APK
                    // before the result crosses this process boundary.
                    .put("vpn", vpnProbe);
            writeJson(new File(control.getPath() + ".ready"), response);
        }
    }

    private static void assertVpnDnsUsesSupportedAddressFamily(LinkProperties properties) {
        List<InetAddress> servers = properties.getDnsServers();
        if (servers.isEmpty()) {
            throw new IllegalStateException("ANDROID_VPN_DNS_UNSUPPORTED_ADDRESS_FAMILY");
        }
        for (InetAddress server : servers) {
            if (!(server instanceof Inet4Address)) {
                throw new IllegalStateException("ANDROID_VPN_DNS_UNSUPPORTED_ADDRESS_FAMILY");
            }
        }
    }

    private JSONObject awaitProviderDefaultVpn(long deadlineElapsedRealtime) throws Exception {
        Bundle request = new Bundle();
        request.putLong(
                AndroidRoutingProbeProvider.KEY_DEADLINE_ELAPSED_REALTIME,
                deadlineElapsedRealtime);
        Bundle response;
        try {
            ContentResolver resolver = testContext.getContentResolver();
            response = resolver.call(
                    Uri.parse("content://" + AndroidRoutingProbeProvider.AUTHORITY),
                    AndroidRoutingProbeProvider.METHOD_AWAIT_DEFAULT_VPN,
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
            int probeUid = response.getInt(AndroidRoutingProbeProvider.KEY_PROBE_UID, -1);
            int testUid = testContext.getApplicationInfo().uid;
            int targetUid = context.getApplicationInfo().uid;
            String binding = response.getString(
                    AndroidRoutingProbeProvider.KEY_NETWORK_BINDING, "");
            String transport = response.getString(
                    AndroidRoutingProbeProvider.KEY_NETWORK_TRANSPORT, "");
            if (probeUid != testUid || probeUid == targetUid || !"default".equals(binding)
                    || !("vpn".equals(transport)
                    || "non_vpn".equals(transport)
                    || "none".equals(transport))) {
                throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_IDENTITY_INVALID");
            }
            String errorCode = response.getString(AndroidRoutingProbeProvider.KEY_ERROR_CODE);
            if (errorCode != null && !errorCode.isEmpty()) {
                if (!"ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN".equals(errorCode)) {
                    throw new IOException("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
                }
                String detail = response.getString(
                        AndroidRoutingProbeProvider.KEY_ERROR_DETAIL, "");
                throw new IOException(errorCode + (detail.isEmpty() ? "" : ": " + detail));
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

    private JSONObject routingProviderRequest(String endpoint, long deadlineElapsedRealtime)
            throws Exception {
        JSONObject result = AndroidRoutingProbeRetry.run(
                deadlineElapsedRealtime,
                timeoutMillis -> routingProviderRequestOnce(endpoint, timeoutMillis));
        JSONArray attemptErrors = result.optJSONArray("attempt_errors");
        if (attemptErrors != null) {
            for (int index = 0; index < attemptErrors.length(); index++) {
                System.out.println("ANDROID_ROUTING_PROBE_ATTEMPT_FAILURE "
                        + attemptErrors.getJSONObject(index).toString());
            }
        }
        return result;
    }

    private JSONObject routingProviderRequestOnce(String endpoint, int timeoutMillis)
            throws Exception {
        Bundle response;
        try {
            Bundle extras = new Bundle();
            extras.putInt(AndroidRoutingProbeProvider.KEY_TIMEOUT_MILLIS, timeoutMillis);
            ContentResolver resolver = testContext.getContentResolver();
            response = resolver.call(
                    Uri.parse("content://" + AndroidRoutingProbeProvider.AUTHORITY),
                    AndroidRoutingProbeProvider.METHOD_PROBE,
                    endpoint,
                    extras);
        } catch (SecurityException failure) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_ACCESS_DENIED", failure);
        } catch (Throwable failure) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_FAILED", failure);
        }
        if (response == null) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
        }

        try {
            int probeUid = response.getInt(AndroidRoutingProbeProvider.KEY_PROBE_UID, -1);
            int testUid = testContext.getApplicationInfo().uid;
            int targetUid = context.getApplicationInfo().uid;
            String binding = response.getString(
                    AndroidRoutingProbeProvider.KEY_NETWORK_BINDING, "");
            String transport = response.getString(
                    AndroidRoutingProbeProvider.KEY_NETWORK_TRANSPORT, "");
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
            String errorCode = response.getString(AndroidRoutingProbeProvider.KEY_ERROR_CODE);
            if (errorCode != null && !errorCode.isEmpty()) {
                if (!("ANDROID_NETWORK_REQUEST_FAILED".equals(errorCode)
                        || "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN".equals(errorCode))) {
                    return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
                }
                return result.put("error_code", errorCode).put(
                        "error_detail",
                        response.getString(AndroidRoutingProbeProvider.KEY_ERROR_DETAIL, ""));
            }
            if (!response.containsKey(AndroidRoutingProbeProvider.KEY_STATUS)
                    || !response.containsKey(AndroidRoutingProbeProvider.KEY_BODY)) {
                return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID");
            }
            return result
                    .put("status", response.getInt(AndroidRoutingProbeProvider.KEY_STATUS))
                    .put("body", response.getString(AndroidRoutingProbeProvider.KEY_BODY, ""))
                    .put("error_detail", response.getString(
                            AndroidRoutingProbeProvider.KEY_ERROR_DETAIL, ""));
        } catch (Throwable failure) {
            return routingProviderFailure("ANDROID_NETWORK_PROBE_PROVIDER_OUTPUT_INVALID", failure);
        }
    }

    private JSONObject routingProviderFailure(String code) throws Exception {
        return new JSONObject().put("error_code", code);
    }

    private JSONObject routingProviderFailure(String code, Throwable failure) throws Exception {
        return new JSONObject().put("error_code", code)
                .put("error_detail", CompleteThrowableReporter.format(failure));
    }

    static boolean isPhysicalNetworkCandidate(NetworkCapabilities capabilities) {
        return capabilities != null && isPhysicalNetworkCandidate(
                capabilities.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN),
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI),
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET),
                capabilities.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR));
    }

    static boolean isPhysicalNetworkCandidate(
            boolean notVpn, boolean wifi, boolean ethernet, boolean cellular) {
        return notVpn && (wifi || ethernet || cellular);
    }

    private Network findPhysicalNetwork() {
        if (connectivity == null) return null;
        Network fallback = null;
        for (Network network : observedNetworks) {
            NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
            if (!isPhysicalNetworkCandidate(capabilities)) continue;
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
            JSONObject detail = networkRequestFailure(failure);
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

    private JSONObject networkRequestFailure(Throwable failure) throws Exception {
        return new JSONObject()
                .put("error_code", "ANDROID_NETWORK_REQUEST_FAILED")
                .put("error_detail", CompleteThrowableReporter.format(failure));
    }

    private JSONObject shellNetworkRequest(String operation, String endpoint, int value)
            throws Exception {
        ApplicationInfo probeApplication = InstrumentationRegistry.getInstrumentation()
                .getContext().getApplicationInfo();
        String shellUid = shellCommand("su 2000 id -u").stdoutText().trim();
        if (!"2000".equals(shellUid)) {
            throw new IOException("ANDROID_NETWORK_PROBE_SHELL_UID_INVALID");
        }
        String probeRoot = "/data/local/tmp/dobbyvpn-probe-"
                + android.os.Process.myPid() + "-" + System.nanoTime();
        String output = "";
        Throwable operationFailure = null;
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
                    + " " + value;
            output = shellCommand(command).stdoutText().trim();
            if (output.isEmpty()) {
                throw new IOException("ANDROID_NETWORK_PROBE_OUTPUT_INVALID");
            }
        } catch (Exception | Error failure) {
            operationFailure = failure;
            throw failure;
        } finally {
            try {
                shellCommand("rm -rf " + probeRoot);
            } catch (Exception cleanupFailure) {
                if (operationFailure != null) {
                    operationFailure.addSuppressed(cleanupFailure);
                } else {
                    throw cleanupFailure;
                }
            }
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
                "grep -a -l AndroidNetworkProbeMain");
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
        return shellCommand(automation, command).stdoutText();
    }

    private ShellCommandResult shellCommand(String command) throws IOException {
        return shellCommand(
                InstrumentationRegistry.getInstrumentation().getUiAutomation(), command);
    }

    private ShellCommandResult shellCommand(UiAutomation automation, String command)
            throws IOException {
        ParcelFileDescriptor[] descriptors = automation.executeShellCommandRwe(command);
        if (descriptors == null || descriptors.length != 3) {
            throw new IOException("ANDROID_NETWORK_PROBE_PIPE_FAILED");
        }
        try (OutputStream input = new ParcelFileDescriptor.AutoCloseOutputStream(descriptors[1])) {
            // All shell probes are noninteractive; closing stdin lets the shell
            // command observe EOF before its output streams are collected.
        }
        PipeCapture stdoutCapture = new PipeCapture(
                new ParcelFileDescriptor.AutoCloseInputStream(descriptors[0]));
        PipeCapture stderrCapture = new PipeCapture(
                new ParcelFileDescriptor.AutoCloseInputStream(descriptors[2]));
        Thread stdoutReader = new Thread(stdoutCapture, "dobby-android-shell-stdout");
        Thread stderrReader = new Thread(stderrCapture, "dobby-android-shell-stderr");
        stdoutReader.start();
        stderrReader.start();
        IOException diagnosticFailure = joinPipeReaders(stdoutReader, stderrReader);
        if (stdoutCapture.failure != null) {
            diagnosticFailure = appendFailure(diagnosticFailure, stdoutCapture.failure);
        }
        if (stderrCapture.failure != null) {
            diagnosticFailure = appendFailure(diagnosticFailure, stderrCapture.failure);
        }
        ShellCommandResult result = new ShellCommandResult(
                stdoutCapture.bytes, stderrCapture.bytes);
        try {
            recordCommandOutput(result);
        } catch (IOException failure) {
            diagnosticFailure = appendFailure(diagnosticFailure, failure);
        }
        if (diagnosticFailure != null) {
            throw new IOException("ANDROID_NETWORK_PROBE_PIPE_FAILED", diagnosticFailure);
        }
        return result;
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
        PipeCapture stdoutCapture = new PipeCapture(commandOutput);
        PipeCapture stderrCapture = new PipeCapture(commandError);
        Thread stdoutReader = new Thread(stdoutCapture, "dobby-android-dd-stdout");
        Thread stderrReader = new Thread(stderrCapture, "dobby-android-dd-stderr");
        stdoutReader.start();
        stderrReader.start();
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
        IOException diagnosticFailure = joinPipeReaders(stdoutReader, stderrReader);
        if (stdoutCapture.failure != null) {
            diagnosticFailure = appendFailure(diagnosticFailure, stdoutCapture.failure);
        }
        if (stderrCapture.failure != null) {
            diagnosticFailure = appendFailure(diagnosticFailure, stderrCapture.failure);
        }
        try {
            recordCommandOutput(new ShellCommandResult(
                    stdoutCapture.bytes, stderrCapture.bytes));
        } catch (IOException failure) {
            if (diagnosticFailure == null) diagnosticFailure = failure;
            else diagnosticFailure.addSuppressed(failure);
        }
        if (writeFailure != null) {
            if (diagnosticFailure != null) writeFailure.addSuppressed(diagnosticFailure);
            throw new IOException("ANDROID_NETWORK_PROBE_STAGE_FAILED", writeFailure);
        }
        if (diagnosticFailure != null) {
            throw new IOException("ANDROID_NETWORK_PROBE_PIPE_FAILED", diagnosticFailure);
        }
        String stagedBytes = automationShell(automation, "stat -c %s " + path).trim();
        if (!Long.toString(expectedBytes).equals(stagedBytes)) {
            throw new IOException("ANDROID_NETWORK_PROBE_STAGE_SIZE_INVALID");
        }
    }

    private IOException joinPipeReaders(Thread stdoutReader, Thread stderrReader) {
        InterruptedException interruption = null;
        while (stdoutReader.isAlive() || stderrReader.isAlive()) {
            try {
                stdoutReader.join();
                stderrReader.join();
            } catch (InterruptedException failure) {
                if (interruption == null) interruption = failure;
                else interruption.addSuppressed(failure);
            }
        }
        if (interruption != null) {
            Thread.currentThread().interrupt();
            return new IOException("ANDROID_NETWORK_PROBE_PIPE_FAILED", interruption);
        }
        return null;
    }

    private IOException appendFailure(IOException primary, IOException additional) {
        if (primary == null) return additional;
        if (primary != additional) primary.addSuppressed(additional);
        return primary;
    }

    private JSONObject requiredShellNetworkRequest(String operation, String endpoint, int value)
            throws Exception {
        JSONObject result = shellNetworkRequest(operation, endpoint, value);
        if (result.has("error_code")) {
            String detail = result.optString("error_detail", "");
            throw new IOException("ANDROID_NETWORK_PROBE_FAILED"
                    + (detail.isEmpty() ? "" : ": " + detail));
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
            JSONObject error = value.optJSONObject("error");
            String code = error == null
                    ? "ANDROID_GO_OPERATION_FAILED"
                    : error.optString("code", "ANDROID_GO_OPERATION_FAILED");
            String message = error == null ? "" : error.optString("message", "");
            String details = message.isEmpty() ? "" : ": " + message;
            if (!code.equals(fixedFailureCode(code))) {
                details += " [backend_code=" + code + "]";
            }
            throw new IllegalStateException(fixedFailureCode(code) + details);
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

    private void recordCommandOutput(ShellCommandResult result) throws IOException {
        commandOutputDiagnostics.put(shellOutputJson(result.stdout, result.stderr));
    }

    static JSONObject shellOutputJson(byte[] stdout, byte[] stderr) throws IOException {
        try {
            return new JSONObject()
                    .put("stdout_base64", Base64.getEncoder().encodeToString(stdout))
                    .put("stderr_base64", Base64.getEncoder().encodeToString(stderr));
        } catch (org.json.JSONException failure) {
            throw new IOException("ANDROID_NETWORK_PROBE_OUTPUT_INVALID", failure);
        }
    }

    private static final class ShellCommandResult {
        final byte[] stdout;
        final byte[] stderr;

        ShellCommandResult(byte[] stdout, byte[] stderr) {
            this.stdout = stdout;
            this.stderr = stderr;
        }

        String stdoutText() {
            return new String(stdout, StandardCharsets.UTF_8);
        }
    }

    private static final class PipeCapture implements Runnable {
        final InputStream input;
        volatile byte[] bytes = new byte[0];
        volatile IOException failure;

        PipeCapture(InputStream input) {
            this.input = input;
        }

        @Override
        public void run() {
            ByteArrayOutputStream output = new ByteArrayOutputStream();
            try (InputStream source = input) {
                byte[] buffer = new byte[8192];
                int count;
                while ((count = source.read(buffer)) >= 0) output.write(buffer, 0, count);
            } catch (IOException readFailure) {
                failure = readFailure;
            } finally {
                bytes = output.toByteArray();
            }
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

    /** Publish one UI phase and its required rendered artifact. */
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
     * Capture selected rendered milestones as complete PNG artifacts. A
     * screenshot remains an extra artifact and never replaces the complete
     * instrumentation streams or observation JSON.
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
        File output = null;
        try {
            // A failure frame must describe the actual timed-out UI. In
            // particular, preserve an open keyboard until both accessibility
            // diagnostics and this screenshot have captured the state.
            if (!"failed".equals(state)) dismissNativeInputIfVisible();
            sourceBitmap = InstrumentationRegistry.getInstrumentation()
                    .getUiAutomation().takeScreenshot();
            if (sourceBitmap == null
                    || sourceBitmap.getWidth() <= 0 || sourceBitmap.getHeight() <= 0) {
                throw new IllegalStateException("ANDROID_UI_SCREENSHOT_CAPTURE_EMPTY");
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
                if (!sourceBitmap.compress(Bitmap.CompressFormat.PNG, 100, stream)) {
                    throw new IOException("ANDROID_UI_SCREENSHOT_PNG_ENCODE_FAILED");
                }
            }
            if (!output.isFile() || output.length() <= 8L) {
                throw new IOException("ANDROID_UI_SCREENSHOT_PNG_INVALID");
            }
            BitmapFactory.Options options = new BitmapFactory.Options();
            options.inJustDecodeBounds = true;
            BitmapFactory.decodeFile(output.getAbsolutePath(), options);
            if (options.outWidth != sourceBitmap.getWidth() || options.outHeight != sourceBitmap.getHeight()) {
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

    /** Send Back only when the Compose keyboard is visible. */
    private void dismissNativeInputIfVisible() throws Exception {
        UiDevice device = uiDevice();
        if (isImeVisible()) device.pressBack();
        device.waitForIdle();
    }

    private boolean isImeVisible() {
        AtomicReference<Boolean> visible = new AtomicReference<>(false);
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        instrumentation.runOnMainSync(() -> {
            Activity activity = MainActivity.current;
            View decor = activity == null ? null : activity.getWindow().getDecorView();
            if (decor == null) return;
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                WindowInsets insets = decor.getRootWindowInsets();
                visible.set(insets != null && insets.isVisible(WindowInsets.Type.ime()));
            } else {
                Rect frame = new Rect();
                decor.getWindowVisibleDisplayFrame(frame);
                int rootHeight = decor.getRootView().getHeight();
                visible.set(rootHeight > 0 && rootHeight - frame.bottom > rootHeight * 0.15f);
            }
        });
        return visible.get();
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
