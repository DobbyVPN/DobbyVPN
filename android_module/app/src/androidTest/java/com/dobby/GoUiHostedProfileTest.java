package com.dobby;

import android.content.Context;
import android.net.ConnectivityManager;
import android.net.LinkProperties;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.os.Bundle;

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
    private static final long POLL_MILLIS = 100L;
    private static final long DEFAULT_TIMEOUT_MILLIS = 60_000L;
    private static final int STABILITY_SAMPLES = 5;

    private final Context context = InstrumentationRegistry.getInstrumentation().getTargetContext();
    private final ConnectivityManager connectivity =
            context.getSystemService(ConnectivityManager.class);

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
            byte[] profile = readBytes(profileFile);
            JSONArray operations = command.getJSONArray("operations");
            for (int i = 0; i < operations.length(); i++) {
                JSONObject operation = operations.getJSONObject(i);
                String name = operation.getString("operation");
                switch (name) {
                    case "configure": {
                        JSONObject configuredEnvelope = requireOK(
                                NativeGoSession.configure(sessionID, sequence, profile));
                        JSONObject configuredResult = configuredEnvelope.getJSONObject("result");
                        copyProfiles(observation, configuredResult.optJSONArray("profiles"));
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
                                sessionID, sequence, "profile_index", index));
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
                        observation.put("tunnel_interface", true);
                        break;
                    case "observe_routing_identity":
                        requireConnected(connected);
                        runRoutingProof(operation.getString("control_file"),
                                command.getJSONObject("endpoints").getString("identity_url"));
                        observation.put("routing_verified", true);
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
                        disconnectClean = "IDLE".equals(idle.optString("state"));
                        connected = false;
                        observation.put("disconnect_clean", disconnectClean);
                        break;
                    }
                    case "reconnect": {
                        ensureVpnReady();
                        int index = command.has("profile_index") ? command.getInt("profile_index") : 0;
                        JSONObject started = requireOK(NativeGoSession.start(
                                sessionID, sequence, "profile_index", index));
                        JSONObject startedResult = started.getJSONObject("result");
                        generation = startedResult.getLong("generation");
                        sequence = startedResult.getLong("sequence");
                        awaitState(sessionID, "CONNECTED", operationTimeout(operation));
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
        int result = NativeVpnBridge.prepare(context);
        if (result == 0) {
            acceptVpnConsent();
            result = NativeVpnBridge.prepare(context);
        }
        if (result != 1) throw new IllegalStateException("ANDROID_VPN_PERMISSION_OR_SERVICE_FAILED");
    }

    private void acceptVpnConsent() throws Exception {
        UiDevice device = UiDevice.getInstance(InstrumentationRegistry.getInstrumentation());
        long deadline = System.currentTimeMillis() + 15_000L;
        while (System.currentTimeMillis() < deadline) {
            for (String label : new String[]{"Allow", "OK", "Start now", "Allow VPN"}) {
                androidx.test.uiautomator.UiObject2 button = device.findObject(By.text(label));
                if (button != null) { button.click(); return; }
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
        Network physical = findPhysicalNetwork();
        if (vpn == null || physical == null) throw new IllegalStateException("ANDROID_NETWORK_IDENTITY_UNAVAILABLE");
        LinkProperties physicalProperties = connectivity.getLinkProperties(physical);
        LinkProperties vpnProperties = connectivity.getLinkProperties(vpn);
        String physicalInterface = physicalProperties == null ? null : physicalProperties.getInterfaceName();
        String vpnInterface = vpnProperties == null ? null : vpnProperties.getInterfaceName();
        if (physicalInterface == null || vpnInterface == null) throw new IllegalStateException("ANDROID_NETWORK_INTERFACE_UNAVAILABLE");
        URL identity = new URL(identityUrl);
        JSONObject ready = new JSONObject()
                .put("phase", "ready")
                .put("physical_interface", physicalInterface)
                .put("physical_transport", physicalTransport(physical))
                .put("vpn_interface", vpnInterface)
                // Resolve on the selected physical network. Resolving through
                // the process default can incorrectly follow the just-created
                // VPN route and was the source of intermittent emulator
                // UnknownHostException failures during the routing proof.
                .put("ipv4", resolveIdentityIpv4(physical, identity.getHost()))
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
                    .put("direct", networkRequest(phasePhysical, identityUrl, directRequired))
                    .put("vpn", networkRequest(phaseVpn, identityUrl, true));
            writeJson(new File(control.getPath() + ".ready"), response);
        }
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
        for (Network network : connectivity.getAllNetworks()) {
            NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
            if (capabilities != null && (capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)
                    ^ capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET))) return network;
        }
        return null;
    }

    private String physicalTransport(Network network) {
        NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
        if (capabilities != null && capabilities.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) return "wifi";
        if (capabilities != null && capabilities.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) return "ethernet";
        return "unknown";
    }

    private String resolveIdentityIpv4(Network network, String host) throws Exception {
        InetAddress[] addresses = network.getAllByName(host);
        for (InetAddress address : addresses) {
            if (address instanceof Inet4Address) return address.getHostAddress();
        }
        throw new IOException("ANDROID_IDENTITY_IPV4_UNAVAILABLE");
    }

    private JSONObject networkRequest(Network network, String endpoint, boolean required) throws Exception {
        try {
            HttpURLConnection connection = (HttpURLConnection) network.openConnection(new URL(endpoint));
            connection.setConnectTimeout(8_000);
            connection.setReadTimeout(8_000);
            int status = connection.getResponseCode();
            String body = readStream(connection.getInputStream());
            connection.disconnect();
            return new JSONObject().put("status", status).put("body", body);
        } catch (Throwable failure) {
            if (required) throw new IOException("ANDROID_NETWORK_REQUEST_FAILED", failure);
            return new JSONObject().put("error_type", failure.getClass().getName())
                    .put("error", String.valueOf(failure.getMessage()))
                    .put("stack", failure.toString());
        }
    }

    private void measureStability(String endpoint, long timeout) throws Exception {
        Network vpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
        if (vpn == null) throw new IllegalStateException("ANDROID_VPN_NETWORK_UNAVAILABLE");
        long deadline = System.currentTimeMillis() + timeout;
        for (int i = 0; i < STABILITY_SAMPLES; i++) {
            networkRequest(vpn, endpoint, true);
            if (i + 1 < STABILITY_SAMPLES) Thread.sleep(1_000L);
            if (System.currentTimeMillis() > deadline) throw new IllegalStateException("ANDROID_STABILITY_TIMEOUT");
        }
    }

    private JSONObject measureThroughput(String download, String upload, long timeout) throws Exception {
        Network vpn = findNetwork(NetworkCapabilities.TRANSPORT_VPN);
        if (vpn == null) throw new IllegalStateException("ANDROID_VPN_NETWORK_UNAVAILABLE");
        long started = System.nanoTime();
        String body = networkRequest(vpn, download, true).optString("body", "");
        double elapsed = Math.max(0.001, (System.nanoTime() - started) / 1_000_000_000.0);
        if (body.isEmpty()) throw new IOException("ANDROID_DOWNLOAD_EMPTY");
        double downloadMbps = body.length() * 8.0 / elapsed / 1_000_000.0;
        HttpURLConnection connection = (HttpURLConnection) vpn.openConnection(new URL(upload));
        connection.setDoOutput(true);
        connection.setRequestMethod("POST");
        connection.setConnectTimeout((int)Math.min(timeout, 8_000L));
        connection.setReadTimeout((int)Math.min(timeout, 8_000L));
        byte[] payload = new byte[64 * 1024];
        long uploadStarted = System.nanoTime();
        try (OutputStream output = connection.getOutputStream()) { output.write(payload); }
        int status = connection.getResponseCode();
        connection.disconnect();
        if (status < 200 || status >= 300) throw new IOException("ANDROID_UPLOAD_STATUS:" + status);
        double uploadMbps = payload.length * 8.0 /
                Math.max(0.001, (System.nanoTime() - uploadStarted) / 1_000_000_000.0) / 1_000_000.0;
        return new JSONObject().put("latency_ms", elapsed * 1000.0)
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
