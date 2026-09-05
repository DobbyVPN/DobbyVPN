package com.dobby.feature.main.domain

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlin.test.assertIs

class SessionEnvelopeDecoderTest {
    @Test
    fun validConfigurationPayloadPreservesSafeFieldsAndKnownProtocol() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"digest":"digest","profiles":[{"index":2,"protocol":"OUTLINE","description":"primary"}],"warnings":[{"code":"legacy","message":"accepted"}]}}""",
        ) { it.toSessionConfiguration() }

        assertEquals(
            SessionConfiguration(
                digest = "digest",
                profiles = listOf(SessionProfile(2, SessionProtocol.OUTLINE, "primary")),
                warnings = listOf(SessionWarning("legacy", "accepted")),
            ),
            assertIs<SessionControllerResult.Success<SessionConfiguration>>(result).value,
        )
    }

    @Test
    fun malformedPayloadPreservesTheDecoderFailure() {
        assertFails { SessionEnvelopeDecoder.decode("not json") { it.sessionString("digest") } }
    }

    @Test
    fun unknownProtocolStateAndFailureMapToUnknownWithoutRejectingPayload() {
        val protocol = SessionEnvelopeDecoder.decode("""{"ok":true,"result":{"protocol":"FUTURE"}}""") {
            it.sessionString("protocol").toSessionProtocol()
        }
        val state = SessionEnvelopeDecoder.decode("""{"ok":true,"result":{"state":"FUTURE"}}""") {
            it.sessionString("state").toSessionState()
        }
        val failure = SessionEnvelopeDecoder.decode("""{"ok":false,"error":{"code":"FUTURE"}}""") { Unit }

        assertEquals(SessionProtocol.UNKNOWN, assertIs<SessionControllerResult.Success<SessionProtocol>>(protocol).value)
        assertEquals(SessionState.UNKNOWN, assertIs<SessionControllerResult.Success<SessionState>>(state).value)
        assertEquals(SessionControllerResult.Failure("FUTURE", SessionFailureCode.UNKNOWN), failure)
    }

    @Test
    fun knownProtocolAndStateVocabularyMapsExactly() {
        assertEquals(SessionProtocol.OUTLINE, "OUTLINE".toSessionProtocol())
        assertEquals(SessionProtocol.XRAY, "XRAY".toSessionProtocol())
        assertEquals(SessionProtocol.TRUST_TUNNEL, "TRUST_TUNNEL".toSessionProtocol())
        assertEquals(SessionProtocol.UNSPECIFIED, "".toSessionProtocol())

        assertEquals(SessionState.IDLE, "IDLE".toSessionState())
        assertEquals(SessionState.CONFIGURED, "CONFIGURED".toSessionState())
        assertEquals(SessionState.PROBING, "PROBING".toSessionState())
        assertEquals(SessionState.PREPARING, "PREPARING".toSessionState())
        assertEquals(SessionState.CONNECTED, "CONNECTED".toSessionState())
        assertEquals(SessionState.STOPPING, "STOPPING".toSessionState())
        assertEquals(SessionState.FAILED, "FAILED".toSessionState())
        assertEquals(SessionState.DESTROYED, "DESTROYED".toSessionState())
        assertEquals(SessionState.UNSPECIFIED, "".toSessionState())
    }

    @Test
    fun typedFailurePayloadIsPreserved() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":false,"error":{"code":"STALE_GENERATION","message":"exact stale generation"}}""",
        ) { Unit }

        assertEquals(
            SessionControllerResult.Failure("exact stale generation", SessionFailureCode.STALE_GENERATION),
            result,
        )
    }

    @Test
    fun snapshotPayloadMapsTheCompletePublicSnapshot() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"session_id":"session-1","generation":7,"state":"CONNECTED","configured":true,"cleanup_complete":false,"last_failure":"RUNTIME_FAILED"}}""",
        ) { it.toSessionSnapshot() }

        assertEquals(
            SessionSnapshot(7uL, SessionState.CONNECTED, configured = true, cleanupComplete = false, lastFailureCode = SessionFailureCode.RUNTIME_FAILED, sessionId = "session-1"),
            assertIs<SessionControllerResult.Success<SessionSnapshot>>(result).value,
        )
    }

    @Test
    fun snapshot_requires_a_nonnegative_go_generation() {
        listOf(
            """{"ok":true,"result":{"state":"IDLE","configured":false,"cleanup_complete":true}}""",
            """{"ok":true,"result":{"generation":-1,"state":"IDLE","configured":false,"cleanup_complete":true}}""",
        ).forEach { payload ->
            assertFails { SessionEnvelopeDecoder.decode(payload) { it.toSessionSnapshot() } }
        }
    }

    @Test
    fun observationPayloadRejectsMalformedItems() {
        assertFails {
            SessionEnvelopeDecoder.decode(
                """{"ok":true,"result":{"events":[{"session_id":"session-1","generation":3,"sequence":8,"state":"PREPARING"},42,{"session_id":"session-1","generation":3,"sequence":9,"state":"FAILED","failure":"PLATFORM_FAILED"}],"next_sequence":9}}""",
            ) { it.toSessionObservation() }
        }
    }

    @Test
    fun observationWithout_go_authoritative_sequence_is_not_given_a_zero_fallback() {
        assertFails {
            SessionEnvelopeDecoder.decode(
                """{"ok":true,"result":{"events":[{"session_id":"session-1","generation":3,"state":"CONNECTED"}],"next_sequence":1}}""",
            ) { it.toSessionObservation() }
        }
    }

    @Test
    fun observation_with_zero_go_sequence_is_rejected() {
        assertFails {
            SessionEnvelopeDecoder.decode(
                """{"ok":true,"result":{"events":[{"session_id":"session-1","generation":3,"sequence":0,"state":"CONNECTED"}],"next_sequence":0}}""",
            ) { it.toSessionObservation() }
        }
    }

    @Test
    fun observation_with_negative_next_sequence_is_rejected() {
        assertFails {
            SessionEnvelopeDecoder.decode(
                """{"ok":true,"result":{"events":[],"next_sequence":-1}}""",
            ) { it.toSessionObservation() }
        }
    }

    @Test
    fun observation_with_mixed_go_session_identities_is_rejected() {
        assertFails {
            SessionEnvelopeDecoder.decode(
                """{"ok":true,"result":{"events":[{"session_id":"session-1","generation":1,"sequence":1,"state":"CONNECTED"},{"session_id":"session-2","generation":1,"sequence":2,"state":"FAILED"}],"next_sequence":2}}""",
            ) { it.toSessionObservation() }
        }
    }

    @Test
    fun active_event_requires_a_positive_go_generation_but_idle_allows_zero() {
        assertFails {
            SessionEnvelopeDecoder.decode(
                """{"ok":true,"result":{"events":[{"session_id":"session-1","generation":0,"sequence":1,"state":"CONNECTED"}],"next_sequence":1}}""",
            ) { it.toSessionObservation() }
        }

        val idle = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"events":[{"session_id":"session-1","generation":0,"sequence":1,"state":"IDLE"}],"next_sequence":1}}""",
        ) { it.toSessionObservation() }
        assertIs<SessionControllerResult.Success<SessionObservation>>(idle)
    }

    @Test
    fun start_and_stop_results_require_positive_go_generation() {
        listOf(
            """{"ok":true,"result":{}}""",
            """{"ok":true,"result":{"generation":0}}""",
            """{"ok":true,"result":{"generation":-1}}""",
        ).forEach { payload ->
            assertFails { SessionEnvelopeDecoder.decode(payload) { it.requiredPositiveSessionLong("generation").toULong() } }
            assertFails { SessionEnvelopeDecoder.decode(payload) { it.requiredPositiveSessionLong("generation").toULong() } }
        }
    }
}
