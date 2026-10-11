package com.dobby.presentation

data class PermissionTarget(
    val sessionId: String,
    val digest: String,
    val generation: Long,
    val sourceRevision: Long,
)

data class PermissionSnapshot(
    val sessionId: String,
    val digest: String,
    val generation: Long,
    val sourceRevision: Long,
    val sourceDirty: Boolean,
    val hasPendingTarget: Boolean,
    val primaryAction: String,
    val canSwitch: Boolean,
)

object PermissionTargetFence {
    fun stillCurrent(target: PermissionTarget, snapshot: PermissionSnapshot): Boolean =
        !snapshot.sourceDirty &&
            target.sessionId == snapshot.sessionId &&
            target.digest == snapshot.digest &&
            target.generation == snapshot.generation &&
            target.sourceRevision == snapshot.sourceRevision

    fun mayStartAfterConsent(target: PermissionTarget, snapshot: PermissionSnapshot): Boolean =
        stillCurrent(target, snapshot) &&
            !snapshot.hasPendingTarget &&
            (snapshot.primaryAction == "START" || snapshot.canSwitch)
}
