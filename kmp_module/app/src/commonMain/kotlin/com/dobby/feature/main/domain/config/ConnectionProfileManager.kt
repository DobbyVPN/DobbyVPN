package com.dobby.feature.main.domain.config

import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionProfile
import com.dobby.feature.main.domain.DobbyConfigsRepository

internal class ConnectionProfileManager(
    private val repo: DobbyConfigsRepository,
    private val logger: Logger,
) {
    private val store = ConnectionProfileStore(repo, logger)
    private val applier = ConnectionProfileApplier(repo, logger)

    fun hasMultipleProfiles(): Boolean = store.hasMultipleProfiles()

    fun replaceProfilesAndApplyFirstAvailable(profiles: List<ConnectionProfile>): Boolean {
        if (profiles.isEmpty()) {
            logger.log("[Profiles] No profiles to save/apply")
            return false
        }

        store.setProfiles(profiles)
        for ((index, profile) in profiles.withIndex()) {
            repo.setActiveConnectionProfileIndex(index)
            if (applier.apply(profile)) return true
            logger.log("[Profiles] Profile index=$index could not be applied, trying next profile")
        }

        logger.log("[Profiles] No profile could be applied")
        return false
    }

    fun switchToNext(reason: String): Boolean {
        val profiles = store.getProfiles()
        if (profiles.size <= 1) {
            logger.log("[Failover] No next profile: profiles=${profiles.size} reason=$reason")
            return false
        }

        val current = store.activeIndex(profiles)
        for (offset in 1..profiles.size) {
            val next = (current + offset) % profiles.size
            repo.setActiveConnectionProfileIndex(next)
            logger.log(
                "[Failover] Switching profile reason=$reason " +
                    "fromIndex=$current toIndex=$next profiles=${profiles.size}"
            )
            if (applier.apply(profiles[next])) return true
            logger.log("[Failover] Profile index=$next could not be applied, trying next profile")
        }

        repo.setActiveConnectionProfileIndex(current)
        logger.log("[Failover] No applicable profile found after full cycle reason=$reason")
        return false
    }
}
