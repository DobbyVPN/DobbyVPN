package com.dobby.presentation

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PermissionTargetFenceTest {
    private val target = PermissionTarget(
        sessionId = "session-a",
        digest = "digest-a",
        generation = 4,
        sourceRevision = 8,
    )

    @Test
    fun capturedTargetSurvivesConsentOnlyWhileItsIdentityIsCurrent() {
        assertTrue(PermissionTargetFence.stillCurrent(target, snapshot()))
        assertFalse(PermissionTargetFence.stillCurrent(target, snapshot(sourceRevision = 9)))
        assertFalse(PermissionTargetFence.stillCurrent(target, snapshot(sourceDirty = true)))
        assertFalse(PermissionTargetFence.stillCurrent(target, snapshot(sessionId = "session-b")))
        assertFalse(PermissionTargetFence.stillCurrent(target, snapshot(digest = "digest-b")))
        assertFalse(PermissionTargetFence.stillCurrent(target, snapshot(generation = 5)))
    }

    @Test
    fun freshBackendSnapshotMustStillAllowSelectionAfterConsent() {
        assertTrue(PermissionTargetFence.mayStartAfterConsent(target, snapshot()))
        assertFalse(PermissionTargetFence.mayStartAfterConsent(target, snapshot(hasPendingTarget = true)))
        assertFalse(PermissionTargetFence.mayStartAfterConsent(target, snapshot(primaryAction = "STOP")))
        assertTrue(PermissionTargetFence.mayStartAfterConsent(
            target,
            snapshot(primaryAction = "STOP", canSwitch = true),
        ))
    }

    private fun snapshot(
        sessionId: String = target.sessionId,
        digest: String = target.digest,
        generation: Long = target.generation,
        sourceRevision: Long = target.sourceRevision,
        sourceDirty: Boolean = false,
        hasPendingTarget: Boolean = false,
        primaryAction: String = "START",
        canSwitch: Boolean = false,
    ) = PermissionSnapshot(
        sessionId,
        digest,
        generation,
        sourceRevision,
        sourceDirty,
        hasPendingTarget,
        primaryAction,
        canSwitch,
    )
}
