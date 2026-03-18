package interop.drivers

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.addTapDeviceRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class DriversGrpcLibrary(channel: ManagedChannel) : DriversLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun AddTapDevice(appDir: String) {
        return runBlocking {
            try {
                val request = addTapDeviceRequest { this.appDir = appDir }
                stub.addTapDevice(request)
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
