package interop.dnscache

import com.dobby.grpcproto.VpnGrpcKt
import com.dobby.grpcproto.empty
import com.dobby.grpcproto.setDNSCacheEntriesRequest
import interop.exceptions.VpnServiceStatusException
import io.grpc.ManagedChannel
import io.grpc.StatusException
import kotlinx.coroutines.runBlocking

open class DnsCacheGrpcLibrary(channel: ManagedChannel) : DnsCacheLibrary {
    private val stub = VpnGrpcKt.VpnCoroutineStub(channel)

    override fun ClearDNSCache() {
        return runBlocking {
            try {
                stub.clearDNSCache(empty {})
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }

    override fun SetDNSCacheEntries(entries: String, source: String): Int {
        return runBlocking {
            try {
                val response = stub.setDNSCacheEntries(
                    setDNSCacheEntriesRequest {
                        this.entries = entries
                        this.source = source
                    }
                )

                response.cachedCount
            } catch (e: StatusException) {
                throw VpnServiceStatusException(e)
            }
        }
    }
}
