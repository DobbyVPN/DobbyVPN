package interop.session

import com.dobby.grpcproto.SessionConfigureRequest
import com.dobby.grpcproto.SessionConfigureResponse
import com.dobby.grpcproto.SessionResetRequest
import com.dobby.grpcproto.SessionResetResponse
import com.dobby.grpcproto.SessionSnapshot
import com.dobby.grpcproto.SessionSnapshotRequest
import com.dobby.grpcproto.SessionSnapshotResponse
import com.dobby.grpcproto.SessionStartMode
import com.dobby.grpcproto.SessionStartRequest
import com.dobby.grpcproto.SessionStartResponse
import com.dobby.grpcproto.SessionStopRequest
import com.dobby.grpcproto.SessionStopResponse
import com.dobby.grpcproto.VpnGrpcKt
import com.google.protobuf.ByteString
import io.grpc.Channel
import kotlinx.coroutines.flow.Flow

/** Thin typed calls to the desktop session service. Domain mapping stays with the app. */
class SessionGrpcLibrary(channel: Channel) {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    suspend fun configure(sessionID: String, sequence: Long, rawConfig: ByteArray): SessionConfigureResponse =
        stub.configure(
            SessionConfigureRequest.newBuilder()
                .setSessionId(sessionID)
                .setExpectedSequence(sequence)
                .setRawConfig(ByteString.copyFrom(rawConfig))
                .build(),
        )

    suspend fun start(
        sessionID: String,
        sequence: Long,
        mode: SessionStartMode,
        profileIndex: Int,
    ): SessionStartResponse = stub.start(
        SessionStartRequest.newBuilder()
            .setSessionId(sessionID)
            .setExpectedSequence(sequence)
            .setMode(mode)
            .setProfileIndex(profileIndex)
            .build(),
    )

    suspend fun stop(sessionID: String, generation: Long): SessionStopResponse =
        stub.stop(SessionStopRequest.newBuilder().setSessionId(sessionID).setGeneration(generation).build())

    suspend fun snapshot(sessionID: String = ""): SessionSnapshotResponse =
        stub.snapshot(SessionSnapshotRequest.newBuilder().setSessionId(sessionID).build())

    fun watch(sessionID: String = ""): Flow<SessionSnapshot> =
        stub.watch(SessionSnapshotRequest.newBuilder().setSessionId(sessionID).build())

    suspend fun reset(sessionID: String, sequence: Long): SessionResetResponse =
        stub.reset(SessionResetRequest.newBuilder().setSessionId(sessionID).setExpectedSequence(sequence).build())
}
