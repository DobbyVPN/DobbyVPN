package com.dobby;

import android.os.Process;

import org.json.JSONException;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.Base64;

/** HTTPS probe launched by UiAutomation as Android's ordinary shell UID. */
public final class GoUiNetworkProbeMain {
    private static final int MAX_BODY_TEXT_BYTES = 64 * 1024;

    private GoUiNetworkProbeMain() { }

    public static void main(String[] arguments) {
        if (arguments.length != 4) {
            throw new IllegalArgumentException("probe arguments are invalid");
        }
        String outputPath = outputPath(arguments[3]);
        JSONObject result;
        try {
            String operation = arguments[0];
            String decodedEndpoint = new String(
                    Base64.getUrlDecoder().decode(arguments[1]), "UTF-8");
            URL endpoint = secureUrl(decodedEndpoint);
            int value = Integer.parseInt(arguments[2]);
            if ("get".equals(operation)) {
                result = get(endpoint, value != 0);
            } else if ("upload".equals(operation)) {
                result = upload(endpoint, value);
            } else {
                throw new IllegalArgumentException("probe operation is invalid");
            }
        } catch (Throwable failure) {
            result = failureResult(failure);
        }
        System.out.println(result.toString());
        writeResult(outputPath, result);
    }

    private static JSONObject get(URL endpoint, boolean includeBody) throws Exception {
        HttpURLConnection connection = null;
        long started = System.nanoTime();
        try {
            connection = open(endpoint);
            int status = connection.getResponseCode();
            InputStream response = status >= 400
                    ? connection.getErrorStream() : connection.getInputStream();
            Body body = response == null ? new Body("", 0) : readBody(response, includeBody);
            return baseResult(started)
                    .put("status", status)
                    .put("body", body.text)
                    .put("body_bytes", body.bytes);
        } finally {
            if (connection != null) connection.disconnect();
        }
    }

    private static JSONObject upload(URL endpoint, int payloadBytes) throws Exception {
        if (payloadBytes < 1 || payloadBytes > 4 * 1024 * 1024) {
            throw new IllegalArgumentException("probe payload size is invalid");
        }
        HttpURLConnection connection = null;
        long started = System.nanoTime();
        try {
            connection = open(endpoint);
            connection.setDoOutput(true);
            connection.setRequestMethod("POST");
            connection.setFixedLengthStreamingMode(payloadBytes);
            connection.setRequestProperty("Content-Type", "application/octet-stream");
            try (OutputStream output = connection.getOutputStream()) {
                byte[] payload = new byte[64 * 1024];
                int remaining = payloadBytes;
                while (remaining > 0) {
                    int count = Math.min(remaining, payload.length);
                    output.write(payload, 0, count);
                    remaining -= count;
                }
            }
            int status = connection.getResponseCode();
            InputStream response = status >= 400
                    ? connection.getErrorStream() : connection.getInputStream();
            Body body = response == null ? new Body("", 0) : readBody(response, false);
            return baseResult(started)
                    .put("status", status)
                    .put("body_bytes", body.bytes)
                    .put("payload_bytes", payloadBytes);
        } finally {
            if (connection != null) connection.disconnect();
        }
    }

    private static HttpURLConnection open(URL endpoint) throws IOException {
        HttpURLConnection connection = (HttpURLConnection) endpoint.openConnection();
        connection.setConnectTimeout(8_000);
        connection.setReadTimeout(8_000);
        connection.setInstanceFollowRedirects(false);
        connection.setRequestProperty("User-Agent", "DobbyVPN-Harness/1");
        connection.setRequestProperty("Accept-Encoding", "identity");
        connection.setRequestProperty("Connection", "close");
        return connection;
    }

    private static URL secureUrl(String value) throws Exception {
        URL endpoint = new URL(value);
        if (!"https".equalsIgnoreCase(endpoint.getProtocol())) {
            throw new IllegalArgumentException("probe endpoint must use HTTPS");
        }
        if (endpoint.getUserInfo() != null || endpoint.getHost().isEmpty()) {
            throw new IllegalArgumentException("probe endpoint authority is invalid");
        }
        return endpoint;
    }

    private static String outputPath(String value) {
        if (!value.matches(
                "/data/local/tmp/dobbyvpn-probe-[0-9]+-[0-9]+/result\\.json")) {
            throw new IllegalArgumentException("probe output path is invalid");
        }
        return value;
    }

    private static void writeResult(String path, JSONObject result) {
        File outputFile = new File(path);
        File temporary = new File(path + ".tmp");
        try (FileOutputStream output = new FileOutputStream(temporary, false)) {
            output.write((result.toString() + "\n").getBytes("UTF-8"));
            output.getFD().sync();
        } catch (IOException failure) {
            throw new IllegalStateException("probe output write failed", failure);
        }
        if (!temporary.renameTo(outputFile)) {
            throw new IllegalStateException("probe output commit failed");
        }
    }

    private static Body readBody(InputStream input, boolean includeBody) throws IOException {
        try (InputStream source = input; ByteArrayOutputStream output = includeBody
                ? new ByteArrayOutputStream() : null) {
            byte[] buffer = new byte[8192];
            long bytes = 0;
            int count;
            while ((count = source.read(buffer)) >= 0) {
                bytes += count;
                if (output != null) {
                    if (bytes > MAX_BODY_TEXT_BYTES) {
                        throw new IOException("probe response body is too large");
                    }
                    output.write(buffer, 0, count);
                }
            }
            return new Body(output == null ? "" : output.toString("UTF-8"), bytes);
        }
    }

    private static JSONObject baseResult(long started) throws Exception {
        return new JSONObject()
                .put("network_binding", "default")
                .put("probe_uid", Process.myUid())
                .put("elapsed_ms", (System.nanoTime() - started) / 1_000_000.0);
    }

    private static JSONObject failureResult(Throwable failure) {
        try {
            return new JSONObject()
                    .put("network_binding", "default")
                    .put("probe_uid", Process.myUid())
                    .put("error_code", "ANDROID_NETWORK_REQUEST_FAILED");
        } catch (JSONException impossible) {
            throw new AssertionError(impossible);
        }
    }

    private static final class Body {
        final String text;
        final long bytes;

        Body(String text, long bytes) {
            this.text = text;
            this.bytes = bytes;
        }
    }
}
