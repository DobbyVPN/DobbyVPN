package com.dobby.feature.main.domain

import kotlin.test.Test
import kotlin.test.assertEquals
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
    fun malformedPayloadIsInternalFailure() {
        val result = SessionEnvelopeDecoder.decode("not json") { it.sessionString("digest") }

        assertEquals(
            SessionControllerResult.Failure("INTERNAL", SessionFailureCode.INTERNAL),
            result,
        )
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
        val result = SessionEnvelopeDecoder.decode("""{"ok":false,"error":{"code":"STALE_GENERATION"}}""") { Unit }

        assertEquals(
            SessionControllerResult.Failure("STALE_GENERATION", SessionFailureCode.STALE_GENERATION),
            result,
        )
    }

    @Test
    fun snapshotPayloadMapsTheCompletePublicSnapshot() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"generation":7,"state":"CONNECTED","configured":true,"cleanup_complete":false,"last_failure":"RUNTIME_FAILED"}}""",
        ) { it.toSessionSnapshot() }

        assertEquals(
            SessionSnapshot(7uL, SessionState.CONNECTED, configured = true, cleanupComplete = false, lastFailureCode = SessionFailureCode.RUNTIME_FAILED),
            assertIs<SessionControllerResult.Success<SessionSnapshot>>(result).value,
        )
    }

    @Test
    fun observationPayloadMapsOrderedEventsAndIgnoresMalformedItems() {
        val result = SessionEnvelopeDecoder.decode(
            """{"ok":true,"result":{"events":[{"generation":3,"sequence":8,"state":"PREPARING"},42,{"generation":3,"sequence":9,"state":"FAILED","failure":"PLATFORM_FAILED"}],"next_sequence":9}}""",
        ) { it.toSessionObservation() }

        assertEquals(
            SessionObservation(
                events = listOf(
                    SessionEvent(3uL, 8uL, SessionState.PREPARING),
                    SessionEvent(3uL, 9uL, SessionState.FAILED, SessionFailureCode.PLATFORM_FAILED),
                ),
                nextSequence = 9uL,
            ),
            assertIs<SessionControllerResult.Success<SessionObservation>>(result).value,
        )
    }
}
