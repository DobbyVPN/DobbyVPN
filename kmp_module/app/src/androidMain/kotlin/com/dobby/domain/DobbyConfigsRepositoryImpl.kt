package com.dobby.domain

import android.content.SharedPreferences
import com.dobby.feature.main.domain.DobbyConfigsRepository

/** Android source repository; Go owns all configuration bytes and profile state. */
internal class DobbyConfigsRepositoryImpl(
    private val prefs: SharedPreferences,
) : DobbyConfigsRepository {
    private val secrets = AndroidKeystoreSecretStore(prefs)

    init {
        // Preserve the historical Cyrillic-c key exactly once, then remove all
        // cached config/profile/protocol/telemetry values without logging them.
        val legacy = "сonnectionURL"
        if (!prefs.contains("connectionURL") && prefs.contains(legacy)) {
            prefs.getString(legacy, null)?.let { secrets.write("connectionURL", it) }
        }
        secrets.migrate(listOf("connectionURL", legacy))
        listOf(
            "connectionConfig", "сonnectionConfig", "connectionProfiles",
            "activeConnectionProfileIndex", "vpnInterface", "geoRoutingConf",
            "telemetryEndpoint", "telemetryApiToken", "telemetryAttributes",
        ).forEach { prefs.edit().remove(it).remove("secure.v1.$it").apply() }
    }

    override fun getConnectionURL(): String = secrets.read("connectionURL")

    override fun setConnectionURL(connectionURL: String) {
        check(secrets.write("connectionURL", connectionURL)) { "secure connection source write failed" }
    }
}
