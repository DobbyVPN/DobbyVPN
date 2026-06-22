package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionProfile
import com.dobby.feature.main.domain.DobbyConfigsRepository
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

internal class ConnectionProfileStore(
    private val repo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
    }

    fun getProfiles(): List<ConnectionProfile> {
        val raw = repo.getConnectionProfiles()
        if (raw.isBlank()) return emptyList()

        return runCatching {
            json.decodeFromString(ListSerializer(ConnectionProfile.serializer()), raw)
        }.onFailure { e ->
            logger.log("[Profiles] Failed to decode connection profiles: ${e.message}")
        }.getOrDefault(emptyList())
    }

    fun setProfiles(profiles: List<ConnectionProfile>) {
        val raw = json.encodeToString(ListSerializer(ConnectionProfile.serializer()), profiles)
        repo.setConnectionProfiles(raw)
        logger.log("[Profiles] Saved profiles: count=${profiles.size}")
    }

    fun activeIndex(profiles: List<ConnectionProfile> = getProfiles()): Int {
        if (profiles.isEmpty()) return 0
        val stored = repo.getActiveConnectionProfileIndex()
        return stored.coerceIn(0, profiles.lastIndex)
    }

    fun hasMultipleProfiles(): Boolean = getProfiles().size > 1
}
