package interop.session

import com.dobby.grpcproto.SessionEvent as ProtoEvent
import com.dobby.grpcproto.SessionFailure as ProtoFailure
import com.dobby.grpcproto.SessionFailureCode as ProtoFailureCode
import com.dobby.grpcproto.SessionProfile as ProtoProfile
import com.dobby.grpcproto.SessionProtocol as ProtoProtocol
import com.dobby.grpcproto.SessionState as ProtoState
import com.dobby.grpcproto.SessionWarning as ProtoWarning
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals

class SessionGrpcLibraryTest {
    @Test
    fun configureRequestPreservesIdsAndArbitraryConfigBytes() {
        val rawConfig = byteArrayOf(0x5b, 0x5b, 0x58, 0x72, 0x61, 0x79, 0x5d, 0x5d, 0x0a, 0xff.toByte())
        val request = SessionRequests.configure("session-7", "configure-9", rawConfig)

        assertEquals("session-7", request.sessionId)
        assertEquals("configure-9", request.commandId)
        assertContentEquals(
            rawConfig,
            request.rawConfig.toByteArray(),
        )
    }

    @Test
    fun startAndStopRequestsPreserveIdempotencyAndGenerationFields() {
        val start = SessionRequests.start("session-7", "start-4", SessionStartTarget.ProfileIndex(3))
        val stop = SessionRequests.stop("session-7", "stop-8", ULong.MAX_VALUE)

        assertEquals("session-7", start.sessionId)
        assertEquals("start-4", start.commandId)
        assertEquals(3, start.profileIndex)
        assertEquals(com.dobby.grpcproto.SessionStartMode.SESSION_START_MODE_PROFILE_INDEX, start.mode)
        assertEquals("session-7", stop.sessionId)
        assertEquals("stop-8", stop.commandId)
        assertEquals(ULong.MAX_VALUE, stop.generation.toULong())
    }

    @Test
    fun orderedEventMapsSafeTypedFields() {
        val event = ProtoEvent.newBuilder()
            .setSessionId("session-7")
            .setGeneration(12)
            .setSequence(42)
            .setState(ProtoState.SESSION_STATE_FAILED)
            .setProfile(
                ProtoProfile.newBuilder()
                    .setIndex(2)
                    .setProtocol(ProtoProtocol.SESSION_PROTOCOL_XRAY)
                    .setDescription("fallback")
                    .build(),
            )
            .setFailure(
                ProtoFailure.newBuilder()
                    .setCode(ProtoFailureCode.SESSION_FAILURE_CODE_RUNTIME_FAILED)
                    .setMessage("operation failed")
                    .build(),
            )
            .setWarning(ProtoWarning.newBuilder().setCode("RETRY").setMessage("retrying").build())
            .build()

        val mapped = event.toTransport()

        assertEquals("session-7", mapped.sessionId)
        assertEquals(12u, mapped.generation)
        assertEquals(42u, mapped.sequence)
        assertEquals(SessionState.FAILED, mapped.state)
        assertEquals(SessionProtocol.XRAY, mapped.profile?.protocol)
        assertEquals(SessionFailureCode.RUNTIME_FAILED, mapped.failure?.code)
        assertEquals("RETRY", mapped.warning?.code)
    }
}
