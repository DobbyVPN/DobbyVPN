package com.dobby.feature.main.domain

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertIs

class SessionEnvelopeDecoderTest {
    @Test
    fun configurationMetadataMapsUrlAndProfiles() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"sequence":5,"digest":"digest","source_kind":"URL","profiles":[{"index":0,"protocol":"OUTLINE","description":"primary"}],"warnings":[]}}""",
        ) { it.toSessionConfiguration("owner") }

        assertEquals(
            SessionConfiguration(
                sessionId = "owner",
                sequence = 5u,
                digest = "digest",
                sourceKind = SessionSourceKind.URL,
                profiles = listOf(SessionProfile(0, SessionProtocol.OUTLINE, "primary")),
                warnings = emptyList(),
            ),
            assertIs<SessionControllerResult.Success<SessionConfiguration>>(result).value,
        )
    }

    @Test
    fun configurationRequiresNonnegativeRevision() {
        listOf(
            """{"ok":true,"result":{"digest":"digest","source_kind":"INLINE","profiles":[],"warnings":[]}}""",
            """{"ok":true,"result":{"sequence":-1,"digest":"digest","source_kind":"INLINE","profiles":[],"warnings":[]}}""",
        ).forEach { payload ->
            assertFails {
                SessionEnvelopeDecoder.decode(payload) { it.toSessionConfiguration("owner") }
            }
        }
    }

    @Test
    fun snapshotCarriesCurrentRevisionAndFailure() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"session_id":"owner","sequence":8,"generation":2,"state":"CONNECTED","configured":true,"digest":"digest","source_kind":"INLINE","profiles":[],"warnings":[],"active_profile":{"index":0,"protocol":"XRAY","description":"fast"},"last_failure":{"code":"RUNTIME_FAILED","message":"protocol stopped"},"cleanup_complete":false}}""",
        ) { it.toSessionSnapshot() }

        assertEquals(
            SessionSnapshot(
                sessionId = "owner",
                sequence = 8u,
                generation = 2u,
                state = SessionState.CONNECTED,
                configured = true,
                digest = "digest",
                sourceKind = SessionSourceKind.INLINE,
                profiles = emptyList(),
                warnings = emptyList(),
                activeProfile = SessionProfile(0, SessionProtocol.XRAY, "fast"),
                lastFailure = SessionFailure(SessionFailureCode.RUNTIME_FAILED, "protocol stopped"),
                cleanupComplete = false,
            ),
            assertIs<SessionControllerResult.Success<SessionSnapshot>>(result).value,
        )
    }

    @Test
    fun typedFailureIsPreservedAndIncompleteOrUnknownPayloadsAreRejected() {
        assertEquals(
            SessionControllerResult.Failure("refresh snapshot", SessionFailureCode.CONFLICT),
            SessionEnvelopeDecoder.decode(
                """{"ok":false,"error":{"code":"CONFLICT","message":"refresh snapshot"}}""",
            ) { Unit },
        )
        assertFails {
            SessionEnvelopeDecoder.decode("""{"ok":false,"error":{"code":"FUTURE"}}""") { Unit }
        }
        assertFails { SessionEnvelopeDecoder.decode("not json") { it.sessionString("digest") } }
    }

    @Test
    fun snapshotRequiresNonnegativeRevisionAndKnownState() {
        val invalidSnapshots = listOf(
            """{"ok":true,"result":{"session_id":"owner","sequence":-1,"generation":0,"state":"IDLE","configured":false,"digest":"","source_kind":"","profiles":[],"warnings":[],"cleanup_complete":true}}""",
            """{"ok":true,"result":{"session_id":"owner","sequence":1,"generation":0,"state":"FUTURE","configured":false,"digest":"","source_kind":"","profiles":[],"warnings":[],"cleanup_complete":true}}""",
        )
        invalidSnapshots.forEach { payload ->
            assertFails { SessionEnvelopeDecoder.decode(payload) { it.toSessionSnapshot() } }
        }
    }
}
