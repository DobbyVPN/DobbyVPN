package com.dobby;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.net.ConnectivityManager;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.Uri;
import android.os.Bundle;
import android.os.Process;
import android.os.SystemClock;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;

import javax.net.ssl.HttpsURLConnection;

/**
 * Ordinary-app HTTPS routing oracle for the hosted Android test.
 *
 * The provider is declared only in the instrumented test APK and runs in its
 * own process. It deliberately does not select or bind a {@code Network}; the
 * socket therefore follows the test APK process's default route while still
 * carrying the test APK UID.
 */
public final class AndroidRoutingProbeProvider extends ContentProvider {
    public static final String AUTHORITY = "com.dobby.vpn.test.routing_probe";
    public static final String METHOD_PROBE = "probe";
    public static final String METHOD_AWAIT_DEFAULT_VPN = "await_default_vpn";
    public static final String KEY_DEADLINE_ELAPSED_REALTIME =
            "deadline_elapsed_realtime";
    public static final String KEY_STATUS = "status";
    public static final String KEY_BODY = "body";
    public static final String KEY_PROBE_UID = "probe_uid";
    public static final String KEY_NETWORK_BINDING = "network_binding";
    public static final String KEY_NETWORK_TRANSPORT = "network_transport";
    public static final String KEY_ERROR_CODE = "error_code";

    private static final String NETWORK_BINDING_DEFAULT = "default";
    private static final String NETWORK_TRANSPORT_VPN = "vpn";
    private static final String NETWORK_TRANSPORT_NON_VPN = "non_vpn";
    private static final String NETWORK_TRANSPORT_NONE = "none";
    private static final String REQUEST_ERROR_CODE = "ANDROID_NETWORK_REQUEST_FAILED";
    private static final String DEFAULT_NOT_VPN_ERROR_CODE =
            "ANDROID_NETWORK_PROBE_DEFAULT_NOT_VPN";
    private static final int CONNECT_TIMEOUT_MILLIS = 8_000;
    private static final int READ_TIMEOUT_MILLIS = 8_000;

    @Override
    public boolean onCreate() {
        return true;
    }

    @Override
    public Bundle call(String method, String argument, Bundle extras) {
        if (METHOD_AWAIT_DEFAULT_VPN.equals(method)) {
            try {
                return awaitDefaultVpn(extras);
            } catch (Throwable failure) {
                return failureResult(DEFAULT_NOT_VPN_ERROR_CODE, defaultNetworkTransport());
            }
        }
        if (!METHOD_PROBE.equals(method)) {
            return failureResult();
        }
        try {
            return request(argument);
        } catch (Throwable failure) {
            return failureResult();
        }
    }

    private Bundle awaitDefaultVpn(Bundle extras) throws Exception {
        long deadlineElapsedRealtime = extras == null
                ? -1L
                : extras.getLong(KEY_DEADLINE_ELAPSED_REALTIME, -1L);
        ConnectivityManager connectivity = connectivityManager();
        if (connectivity == null) {
            return failureResult(DEFAULT_NOT_VPN_ERROR_CODE, NETWORK_TRANSPORT_NONE);
        }

        // Check the current default before registering. A VPN may already be
        // active when this provider process is first created.
        Network current = connectivity.getActiveNetwork();
        if (isDefaultVpn(connectivity, current)) {
            return baseResult(NETWORK_TRANSPORT_VPN);
        }
        if (deadlineElapsedRealtime <= SystemClock.elapsedRealtime()) {
            return failureResult(DEFAULT_NOT_VPN_ERROR_CODE,
                    networkTransport(connectivity, current));
        }

        CountDownLatch ready = new CountDownLatch(1);
        AtomicReference<Network> observed = new AtomicReference<>();
        ConnectivityManager.NetworkCallback callback =
                new ConnectivityManager.NetworkCallback() {
                    @Override
                    public void onAvailable(Network network) {
                        signalIfDefaultVpn(connectivity, network, observed, ready);
                    }

                    @Override
                    public void onCapabilitiesChanged(
                            Network network, NetworkCapabilities capabilities) {
                        signalIfDefaultVpn(connectivity, network, observed, ready);
                    }
                };
        boolean registered = false;
        try {
            connectivity.registerDefaultNetworkCallback(callback);
            registered = true;

            // Close the race between the initial check and registration: the
            // default can become a VPN before the callback is delivered.
            Network afterRegistration = connectivity.getActiveNetwork();
            signalIfDefaultVpn(connectivity, afterRegistration, observed, ready);

            long remainingMillis = deadlineElapsedRealtime - SystemClock.elapsedRealtime();
            boolean signaled;
            try {
                signaled = remainingMillis > 0
                        && ready.await(remainingMillis, TimeUnit.MILLISECONDS);
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                signaled = false;
            }
            if (!signaled) {
                return failureResult(DEFAULT_NOT_VPN_ERROR_CODE,
                        networkTransport(connectivity, connectivity.getActiveNetwork()));
            }
            Network selected = observed.get();
            if (!isDefaultVpn(connectivity, selected)) {
                return failureResult(DEFAULT_NOT_VPN_ERROR_CODE,
                        networkTransport(connectivity, connectivity.getActiveNetwork()));
            }
            return baseResult(NETWORK_TRANSPORT_VPN);
        } finally {
            if (registered) {
                connectivity.unregisterNetworkCallback(callback);
            }
        }
    }

    private static void signalIfDefaultVpn(
            ConnectivityManager connectivity,
            Network network,
            AtomicReference<Network> observed,
            CountDownLatch ready) {
        if (isDefaultVpn(connectivity, network)
                && observed.compareAndSet(null, network)) {
            ready.countDown();
        }
    }

    private static boolean isDefaultVpn(ConnectivityManager connectivity, Network network) {
        return NETWORK_TRANSPORT_VPN.equals(networkTransport(connectivity, network));
    }

    private Bundle request(String value) throws Exception {
        String defaultTransport = defaultNetworkTransport();
        if (!NETWORK_TRANSPORT_VPN.equals(defaultTransport)) {
            return failureResult(DEFAULT_NOT_VPN_ERROR_CODE, defaultTransport);
        }
        URL endpoint = validatedHttpsUrl(value);
        HttpsURLConnection connection = null;
        try {
            // This is intentionally the ordinary URL connection. Do not
            // select a Network or alter process network binding: the provider
            // process's default network is the routing oracle.
            connection = (HttpsURLConnection) endpoint.openConnection();
            connection.setConnectTimeout(CONNECT_TIMEOUT_MILLIS);
            connection.setReadTimeout(READ_TIMEOUT_MILLIS);
            connection.setInstanceFollowRedirects(false);
            connection.setRequestMethod("GET");
            connection.setRequestProperty("User-Agent", "DobbyVPN-Harness/1");
            connection.setRequestProperty("Accept-Encoding", "identity");
            connection.setRequestProperty("Connection", "close");
            int status = connection.getResponseCode();
            InputStream response = status >= 400
                    ? connection.getErrorStream() : connection.getInputStream();
            long bodyDeadline = System.nanoTime()
                    + READ_TIMEOUT_MILLIS * 1_000_000L;
            String body = response == null ? "" : readBody(response, bodyDeadline);
            Bundle result = baseResult(defaultTransport);
            result.putInt(KEY_STATUS, status);
            result.putString(KEY_BODY, body);
            return result;
        } finally {
            if (connection != null) {
                connection.disconnect();
            }
        }
    }

    private static URL validatedHttpsUrl(String value) throws Exception {
        if (value == null || value.isEmpty() || value.length() > 2_048) {
            throw new IllegalArgumentException("routing probe URL is invalid");
        }
        URL endpoint = new URL(value);
        if (!"https".equalsIgnoreCase(endpoint.getProtocol())
                || endpoint.getHost().isEmpty()
                || endpoint.getUserInfo() != null
                || endpoint.getRef() != null
                || endpoint.getQuery() != null
                || value.indexOf('?') >= 0
                || containsWhitespace(value)) {
            throw new IllegalArgumentException("routing probe URL is invalid");
        }
        return endpoint;
    }

    private static boolean containsWhitespace(String value) {
        for (int index = 0; index < value.length(); index++) {
            if (Character.isWhitespace(value.charAt(index))) return true;
        }
        return false;
    }

    private static String readBody(InputStream input, long deadlineNanos) throws IOException {
        try (InputStream source = input;
             ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[8 * 1024];
            int count;
            while (true) {
                if (System.nanoTime() >= deadlineNanos) {
                    throw new IOException("routing probe response timed out");
                }
                count = source.read(buffer);
                if (count < 0) break;
                output.write(buffer, 0, count);
            }
            return output.toString("UTF-8");
        }
    }

    private static Bundle baseResult(String defaultTransport) {
        Bundle result = new Bundle();
        result.putInt(KEY_PROBE_UID, Process.myUid());
        result.putString(KEY_NETWORK_BINDING, NETWORK_BINDING_DEFAULT);
        result.putString(KEY_NETWORK_TRANSPORT, defaultTransport);
        return result;
    }

    private Bundle failureResult() {
        return failureResult(REQUEST_ERROR_CODE, defaultNetworkTransport());
    }

    private static Bundle failureResult(String errorCode, String defaultTransport) {
        Bundle result = baseResult(defaultTransport);
        result.putString(KEY_ERROR_CODE, errorCode);
        return result;
    }

    private ConnectivityManager connectivityManager() {
        Context owner = getContext();
        return owner == null
                ? null
                : (ConnectivityManager) owner.getSystemService(
                        Context.CONNECTIVITY_SERVICE);
    }

    private String defaultNetworkTransport() {
        ConnectivityManager connectivity = connectivityManager();
        if (connectivity == null) return NETWORK_TRANSPORT_NONE;
        return networkTransport(connectivity, connectivity.getActiveNetwork());
    }

    private static String networkTransport(
            ConnectivityManager connectivity, Network network) {
        if (connectivity == null || network == null) return NETWORK_TRANSPORT_NONE;
        NetworkCapabilities capabilities = connectivity.getNetworkCapabilities(network);
        if (capabilities == null) return NETWORK_TRANSPORT_NONE;
        return capabilities.hasTransport(NetworkCapabilities.TRANSPORT_VPN)
                ? NETWORK_TRANSPORT_VPN : NETWORK_TRANSPORT_NON_VPN;
    }

    @Override
    public Cursor query(
            Uri uri,
            String[] projection,
            String selection,
            String[] selectionArgs,
            String sortOrder) {
        return null;
    }

    @Override
    public String getType(Uri uri) {
        return null;
    }

    @Override
    public Uri insert(Uri uri, ContentValues values) {
        return null;
    }

    @Override
    public int delete(Uri uri, String selection, String[] selectionArgs) {
        return 0;
    }

    @Override
    public int update(
            Uri uri,
            ContentValues values,
            String selection,
            String[] selectionArgs) {
        return 0;
    }
}
