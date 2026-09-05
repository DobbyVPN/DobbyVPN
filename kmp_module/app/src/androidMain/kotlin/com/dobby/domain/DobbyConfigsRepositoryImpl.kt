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
            prefs.getString(legacy, null)?.let {
                check(secrets.write("connectionURL", it)) { "legacy connection source migration failed" }
            }
        }
        secrets.migrate(listOf("connectionURL", legacy))
        listOf(
            "connectionConfig", "сonnectionConfig", "connectionProfiles",
            "activeConnectionProfileIndex", "vpnInterface", "geoRoutingConf",
            "telemetryEndpoint", "telemetryApiToken", "telemetryAttributes",
        ).forEach {
            check(prefs.edit().remove(it).remove("secure.v1.$it").commit()) {
                "obsolete private state removal failed for $it"
            }
        }
    }

    override fun getConnectionURL(): String = secrets.read("connectionURL")

    override fun setConnectionURL(connectionURL: String) {
        check(secrets.write("connectionURL", connectionURL)) { "secure connection source write failed" }
    }
}
