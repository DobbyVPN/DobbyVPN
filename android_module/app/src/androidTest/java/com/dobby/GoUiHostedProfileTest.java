package com.dobby;

import android.app.Activity;
import android.app.Instrumentation;
import android.app.UiAutomation;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.VpnService;
import android.os.Build;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;

import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import androidx.test.uiautomator.By;
import androidx.test.uiautomator.UiDevice;

import com.dobby.nativebridge.NativeGoSession;
import com.dobby.nativebridge.NativeVpnBridge;

import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.Inet4Address;
import java.net.InetAddress;
import java.net.URL;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Enumeration;
import java.util.List;
import java.util.zip.ZipEntry;
import java.util.zip.ZipFile;

/**
 * Android's real functional seam for the Go/Fyne application.
 *
 * The external Torturer runner supplies only an ordered command file and an
 * opaque profile. This test drives the same Go binding as the visible Fyne
 * activity, while Kotlin/Java remains responsible for Android permission,
 * Network/VpnService observations, and disposable test files.
 */
@RunWith(AndroidJUnit4.class)
public final class GoUiHostedProfileTest {
    private static final String COMMAND_ARGUMENT = "dobby.hosted_command_file";
    private static final String REAL_PROFILE_ARGUMENT = "dobby.real_profile";
    private static final String START_MODE_PROFILE_INDEX = "PROFILE_INDEX";
    private static final Class<GoUiNetworkProbeMain> NETWORK_PROBE_CLASS =
            GoUiNetworkProbeMain.class;
    private static final long POLL_MILLIS = 100L;
    private static final long DEFAULT_TIMEOUT_MILLIS = 60_000L;
    private static final long NETWORK_RECOVERY_TIMEOUT_MILLIS = 10_000L;
    private static final int STABILITY_SAMPLES = 5;
    private static final int ROUTING_REQUEST_ATTEMPTS = 3;

    private final Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
    private final ConnectivityManager connectivity =
            context.getSystemService(ConnectivityManager.class);
    private Activity foregroundActivity;

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
        String sessionID = "";
        long sequence = 0L;
        long generation = 0L;
        boolean configured = false;
        boolean connected = false;
        boolean disconnectClean = false;

        try {
            NativeGoSession.attach(context);
            JSONObject initial = snapshotResult("");
            sessionID = initial.getString("session_id");
            sequence = initial.getLong("sequence");
            byte[] profile = readBytes(profileFile);
            JSONArray operations = command.getJSONArray("operations");
            for (int i = 0; i < operations.length(); i++) {
                JSONObject operation = operations.getJSONObject(i);
                String name = operation.getString("operation");
                String operationID = operation.getString("id");
                switch (name) {
                    case "configure": {
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
                        break;
                    }
                    case "connect": {
                        if (!configured) throw new IllegalStateException("ANDROID_CONNECT_BEFORE_CONFIGURE");
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
                                command.getJSONObject("endpoints").getString("identity_url"));
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
                                command.getJSONObject("endpoints").getString("identity_url"));
                        observation.put("network_transition_verified", true);
                        break;
                    case "disconnect": {
                        if (generation <= 0) throw new IllegalStateException("ANDROID_DISCONNECT_WITHOUT_GENERATION");
                        requireOK(NativeGoSession.stop(sessionID, generation));
                        JSONObject idle = awaitState(sessionID, "IDLE", operationTimeout(operation));
                        sequence = idle.optLong("sequence", sequence);
                        boolean vpnRemoved = awaitVpnNetwork(
                                false, operationTimeout(operation)) == null;
                        disconnectClean = "IDLE".equals(idle.optString("state")) && vpnRemoved;
                        connected = false;
                        observation.put("disconnect_clean", disconnectClean);
                        break;
                    }
                    case "reconnect": {
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
                        break;
                    }
                    case "inspect_cleanup": {
                        JSONObject finalSnapshot = snapshotResult(sessionID);
                        boolean idle = "IDLE".equals(finalSnapshot.optString("state"));
                        boolean noVpn = awaitVpnNetwork(false, operationTimeout(operation)) == null;
                        observation.put("cleanup_verified", idle && noVpn);
                        observation.put("final_disconnect_clean", idle && noVpn);
                        break;
                    }
                    case "process_loss":
                        // The owner kills/restarts the target process around
                        // this marker. The recovery phase is a fresh test
                        // invocation and will execute the same Go calls.
                        break;
                    default:
                        throw new IllegalArgumentException("ANDROID_OPERATION_UNSUPPORTED:" + name);
                }
            }
        } catch (Throwable failure) {
            observation.put("error_code", failure.getMessage() == null
                    ? "ANDROID_HOSTED_DRIVER_FAILED" : failure.getMessage());
        } finally {
            try { commandFile.delete(); } catch (Throwable ignored) { }
            try { profileFile.delete(); } catch (Throwable ignored) { }
            writeJson(outputFile, observation);
        }
    }

    private JSONObject baseObservation(JSONObject command) throws Exception {
        JSONObject output = new JSONObject();
        if (command.has("source_sha")) output.put("source_sha", command.getString("source_sha"));
        output.put("connections", new JSONArray());
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
                    "ANDROID_SESSION_FAILED:" + latest.optJSONObject("last_failure"));
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_SESSION_STATE_TIMEOUT:" + expected);
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
        if (foregroundActivity != null && !foregroundActivity.isFinishing()) {
            return foregroundActivity;
        }
        Intent launch = context.getPackageManager()
                .getLaunchIntentForPackage(context.getPackageName());
        if (launch == null) throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_MISSING");
        launch.addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_NEW_TASK);
        Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
        foregroundActivity = instrumentation.startActivitySync(launch);
        instrumentation.waitForIdleSync();
        if (foregroundActivity == null || foregroundActivity.isFinishing()) {
            throw new IllegalStateException("ANDROID_LAUNCH_ACTIVITY_FAILED");
        }
        return foregroundActivity;
    }

    private void acceptVpnConsent() throws Exception {
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        long deadline = System.currentTimeMillis() + 15_000L;
        while (System.currentTimeMillis() < deadline) {
            for (String label : new String[]{"Allow", "OK", "Start now", "Allow VPN"}) {
                androidx.test.uiautomator.UiObject2 button = device.findObject(By.text(label));
                if (button != null) {
                    button.click();
                    // A UI Automator click returns before the system has
                    // persisted the VPN grant. Wait for the authoritative
                    // VpnService check so the next prepare call cannot open a
                    // second consent dialog and report a false failure.
                    while (System.currentTimeMillis() < deadline) {
                        if (VpnService.prepare(context) == null) {
                            device.waitForIdle();
                            return;
                        }
                        Thread.sleep(POLL_MILLIS);
                    }
                }
            }
            Thread.sleep(POLL_MILLIS);
        }
        throw new IllegalStateException("ANDROID_VPN_CONSENT_TIMEOUT");
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

    private void runRoutingProof(String controlName, String identityUrl) throws Exception {
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
        JSONObject ready = new JSONObject()
                .put("phase", "ready")
                .put("physical_interface", physicalInterface)
                .put("physical_transport", physicalTransport(physical))
                .put("vpn_interface", vpnInterface)
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
                    throw new IllegalStateException(
                            "ANDROID_ROUTING_PROOF_FAILED:" + request.optString("error", "unknown"));
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
                    // UiAutomation launches the positive request as Android's
                    // ordinary shell UID. Unlike the VPN-owning application,
                    // that UID follows the default VPN route. The host proves
                    // the route with tun0 counters and proves the explicitly
                    // physical request is blocked by the eth0 firewall rule.
                    .put("vpn", routingVpnRequest(identity.toString()));
            writeJson(new File(control.getPath() + ".ready"), response);
        }
    }

    private JSONObject routingVpnRequest(String endpoint) throws Exception {
        JSONObject latest = null;
        for (int attempt = 0; attempt < ROUTING_REQUEST_ATTEMPTS; attempt++) {
            latest = shellNetworkRequest("get", endpoint, 1);
            if (!latest.has("error_type")) return latest;
            if (attempt + 1 < ROUTING_REQUEST_ATTEMPTS) Thread.sleep(250L);
        }
        return latest;
    }

    /**
     * A transition has two host/app handshakes: first acknowledge the
     * transition request, then run the routing proof on the dedicated
     * .routing control file after the host has toggled and restored the
     * physical uplink. Keeping these files separate prevents a stale ready
     * response from being mistaken for the proof response.
     */
    private void runNetworkTransition(String controlName, String identityUrl) throws Exception {
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
        runRoutingProof(controlName + ".routing", identityUrl);
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
        long deadline = System.currentTimeMillis() + NETWORK_RECOVERY_TIMEOUT_MILLIS;
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
        if (network == null) {
            return shellNetworkRequest("get", endpoint, 1);
        }
        HttpURLConnection connection = null;
        URL url = new URL(endpoint);
        try {
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
                    .put("network_id", network.getNetworkHandle())
                    .put("network_binding", "explicit-physical")
                    .put("status", status)
                    .put("body", body);
        } catch (Throwable failure) {
            JSONObject detail = networkRequestFailure(network, url, failure);
            if (required) {
                throw new IOException(
                        "ANDROID_NETWORK_REQUEST_FAILED:" + detail.toString(), failure);
            }
            return detail;
        } finally {
            if (connection != null) connection.disconnect();
        }
    }

    private JSONObject networkRequestFailure(Network network, URL endpoint, Throwable failure)
            throws Exception {
        String detail = sanitizedFailure(failure);
        NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
        String binding = capabilities != null
                && capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)
                ? "explicit-vpn" : "explicit-physical";
        return new JSONObject()
                .put("network_id", network.getNetworkHandle())
                .put("network_binding", binding)
                .put("host", endpoint.getHost())
                .put("port", endpoint.getPort() > 0 ? endpoint.getPort() : 443)
                .put("error_type", failure.getClass().getName())
                .put("error", detail)
                .put("stack", failure.toString());
    }

    private String sanitizedFailure(Throwable failure) {
        StringBuilder detail = new StringBuilder();
        Throwable current = failure;
        int depth = 0;
        while (current != null && depth < 3) {
            if (depth > 0) detail.append(" <- ");
            detail.append(current.getClass().getSimpleName());
            String message = current.getMessage();
            if (message != null && !message.isEmpty()) {
                detail.append(":").append(message);
            }
            current = current.getCause();
            depth++;
        }
        return detail.toString().replace('\r', ' ').replace('\n', ' ');
    }

    private JSONObject shellNetworkRequest(String operation, String endpoint, int value)
            throws Exception {
        ApplicationInfo probeApplication = InstrumentationRegistry.getInstrumentation()
                .getContext().getApplicationInfo();
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        String shellUid = device.executeShellCommand("su 2000 id -u").trim();
        if (!"2000".equals(shellUid)) {
            throw new IOException("ANDROID_NETWORK_PROBE_SHELL_UID_INVALID:" + shellUid);
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
                String launchLog = device.executeShellCommand(
                        "logcat -d -t 100 -v brief -s appproc AndroidRuntime System.err").trim();
                throw new IOException("ANDROID_NETWORK_PROBE_OUTPUT_INVALID:"
                        + sanitizedOutput(launchOutput + " " + launchLog));
            }
        } finally {
            device.executeShellCommand("rm -rf " + probeRoot);
        }
        JSONObject result;
        try {
            result = new JSONObject(output);
        } catch (Throwable failure) {
            throw new IOException("ANDROID_NETWORK_PROBE_OUTPUT_INVALID:"
                    + sanitizedOutput(output), failure);
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
            throw new IOException("ANDROID_NETWORK_PROBE_DIRECTORY_FAILED:"
                    + sanitizedOutput(mkdirOutput));
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
            throw new IOException("ANDROID_NETWORK_PROBE_PERMISSIONS_FAILED:"
                    + sanitizedOutput(chmodOutput));
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
        String stdout = readStream(commandOutput).trim();
        String stderr = readStream(commandError).trim();
        if (writeFailure != null) {
            throw new IOException("ANDROID_NETWORK_PROBE_STAGE_FAILED:"
                    + sanitizedOutput(stdout + " " + stderr), writeFailure);
        }
        String stagedBytes = automationShell(automation, "stat -c %s " + path).trim();
        if (!Long.toString(expectedBytes).equals(stagedBytes)) {
            throw new IOException("ANDROID_NETWORK_PROBE_STAGE_SIZE_INVALID:"
                    + sanitizedOutput(stagedBytes));
        }
    }

    private String sanitizedOutput(String output) {
        String detail = output.replace('\r', ' ').replace('\n', ' ').trim();
        return detail.length() <= 512 ? detail : detail.substring(0, 512);
    }

    private JSONObject requiredShellNetworkRequest(String operation, String endpoint, int value)
            throws Exception {
        JSONObject result = shellNetworkRequest(operation, endpoint, value);
        if (result.has("error_type")) {
            throw new IOException("ANDROID_NETWORK_PROBE_FAILED:" + result.toString());
        }
        return result;
    }

    private void measureStability(String endpoint, long timeout) throws Exception {
        Network vpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
        if (vpn == null) throw new IllegalStateException("ANDROID_VPN_NETWORK_UNAVAILABLE");
        long deadline = System.currentTimeMillis() + timeout;
        for (int i = 0; i < STABILITY_SAMPLES; i++) {
            requiredShellNetworkRequest("get", endpoint, 0);
            if (i + 1 < STABILITY_SAMPLES) Thread.sleep(1_000L);
            if (System.currentTimeMillis() > deadline) throw new IllegalStateException("ANDROID_STABILITY_TIMEOUT");
        }
    }

    private JSONObject measureThroughput(String download, String upload, long timeout) throws Exception {
        Network vpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
        if (vpn == null) throw new IllegalStateException("ANDROID_VPN_NETWORK_UNAVAILABLE");
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
        if (status < 200 || status >= 300) throw new IOException("ANDROID_UPLOAD_STATUS:" + status);
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
        if (!value.optBoolean("ok", false)) throw new IllegalStateException(
                value.optJSONObject("error") == null ? "ANDROID_GO_OPERATION_FAILED" :
                        value.getJSONObject("error").optString("code", "ANDROID_GO_OPERATION_FAILED"));
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
        if (!file.isFile()) throw new IOException("ANDROID_CONTROL_TIMEOUT:" + file.getName());
    }

    private void writeJson(File file, JSONObject value) throws IOException {
        File temporary = new File(file.getPath() + ".tmp");
        try (FileOutputStream output = new FileOutputStream(temporary, false)) {
            output.write(value.toString().getBytes("UTF-8"));
            output.getFD().sync();
        }
        if (!temporary.renameTo(file)) {
            throw new IOException("ANDROID_ATOMIC_WRITE_FAILED:" + file.getName());
        }
    }

    private void deleteIfPresent(File file) {
        if (file.exists() && !file.delete()) {
            throw new IllegalStateException("ANDROID_CONTROL_STALE_FILE:" + file.getName());
        }
    }

}
