package com.dobby.ui

import android.util.JsonReader
import android.util.JsonToken
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.StringReader

private inline fun <reified T> JSONObject.field(name: String): T =
    get(name) as? T ?: throw JSONException("Invalid snapshot field: $name")

private fun JSONObject.integer(name: String): Long = when (val value = get(name)) {
    is Int -> value.toLong()
    is Long -> value
    else -> throw JSONException("Invalid snapshot integer: $name")
}

private fun JSONObject.index(): Int {
    val value = integer("index")
    if (value !in Int.MIN_VALUE.toLong()..Int.MAX_VALUE.toLong()) throw JSONException("Invalid profile index")
    return value.toInt()
}

private fun JSONObject.optionalObject(name: String): JSONObject? =
    if (isNull(name)) null else field<JSONObject>(name)

private fun JSONObject.optionalText(name: String): String = if (has(name)) field<String>(name) else ""

private fun JSONObject.profile() = ProfileData(index(), field("description"), field("protocol"))

// Android JSONObject converts unsigned integers above Long.MAX_VALUE to Double.
// Read that unusual value from its original JSON token to preserve the generation fence.
private fun networkGeneration(snapshot: JSONObject, envelope: String): ULong {
    if (snapshot.get("network_generation") !is Double) {
        return snapshot.integer("network_generation").takeIf { it >= 0 }?.toULong()
            ?: throw JSONException("Invalid network_generation")
    }
    JsonReader(StringReader(envelope)).use { reader ->
        reader.beginObject()
        while (reader.hasNext()) {
            if (reader.nextName() != "result") {
                reader.skipValue()
                continue
            }
            reader.beginObject()
            while (reader.hasNext()) {
                if (reader.nextName() == "network_generation" && reader.peek() == JsonToken.NUMBER) {
                    return reader.nextString().toULongOrNull() ?: throw JSONException("Invalid network_generation")
                }
                reader.skipValue()
            }
            reader.endObject()
        }
    }
    throw JSONException("Missing network_generation")
}

internal fun decodeSessionSnapshot(response: JSONObject, envelope: String): SessionData {
    val snapshot = response.getJSONObject("result")
    if (!snapshot.has("protocol_version") || snapshot.integer("protocol_version") != 1L) {
        throw JSONException("The app and the VPN service are different versions. Restart the app.")
    }
    snapshot.field<String>("service_version")
    snapshot.field<String>("source_kind")
    if (snapshot.has("service_restore_pending")) snapshot.field<Boolean>("service_restore_pending")
    val profiles = snapshot.field<JSONArray>("profiles")
    val active = snapshot.optionalObject("active_profile")?.profile()
    val failure = snapshot.optionalObject("last_failure")
    val index = snapshot.integer("active_index")
    if (index !in Int.MIN_VALUE.toLong()..Int.MAX_VALUE.toLong()) throw JSONException("Invalid active_index")
    return SessionData(
        sessionId = snapshot.field("session_id"),
        sequence = snapshot.integer("sequence").also { require(it >= 0) },
        generation = snapshot.integer("generation").also { require(it >= 0) },
        networkGeneration = networkGeneration(snapshot, envelope),
        state = snapshot.field("state"),
        primaryAction = snapshot.field("primary_action"),
        vpnActive = snapshot.field("vpn_active"),
        canRetry = snapshot.field("can_retry"),
        canSwitch = snapshot.field("can_switch"),
        configured = snapshot.field("configured"),
        logsUploadAvailable = snapshot.field("logs_upload_available"),
        sourceUrl = snapshot.optionalText("source_url"),
        sourceError = snapshot.optionalText("source_error"),
        offlineAcceptedAt = snapshot.optionalText("offline_accepted_at"),
        digest = snapshot.field("digest"),
        profiles = (0 until profiles.length()).map { profiles.getJSONObject(it).profile() },
        activeDigest = snapshot.field("active_digest"),
        activeMode = snapshot.field("active_mode"),
        activeIndex = index.toInt(),
        activeProfileIndex = active?.index ?: -1,
        activeProfile = active,
        pendingTarget = snapshot.optionalObject("pending_target")?.let {
            SelectionData(it.field("digest"), it.field("mode"), it.index())
        },
        recovering = snapshot.field("recovering"),
        cleanupComplete = snapshot.field("cleanup_complete"),
        failure = failure?.let {
            val message = it.field<String>("message")
            val code = it.field<String>("code")
            if (message.isBlank()) code else "$message ($code)"
        }.orEmpty(),
    )
}
