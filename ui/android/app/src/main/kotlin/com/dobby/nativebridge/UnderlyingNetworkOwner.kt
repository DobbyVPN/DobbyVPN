package com.dobby.nativebridge

import android.net.ConnectivityManager
import android.net.DnsResolver
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Build
import android.os.CancellationSignal
import android.os.ParcelFileDescriptor
import android.os.SystemClock
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executor
import java.util.concurrent.TimeUnit
import org.json.JSONArray
import org.json.JSONObject

/** Captures the physical Network before VPN DNS is installed and retains its
 * native DNS cache, Private DNS and DNS64 behavior across protocol attempts. */
internal class UnderlyingNetworkOwner(
    private val service: VpnService,
    private val diagnostic: (String) -> Unit,
) {
    private val manager = service.getSystemService(ConnectivityManager::class.java)
    private val lock = Any()
    private var network: Network? = null
    private var signature = ""
    private var revision = 0L
    private var registered = false
    private val queries = mutableMapOf<Long, Query>()
    // Cancellation may cross JNI before its query is registered.
    private val canceled = mutableSetOf<Long>()
    private data class Query(val signal: CancellationSignal, val done: CountDownLatch)
    private data class Context(val network: Network, val revision: String)
    private val directExecutor = Executor { it.run() }

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(candidate: Network) = synchronized(lock) {
            if (registered) refreshFromCallback(candidate)
        }
        override fun onLinkPropertiesChanged(candidate: Network, properties: LinkProperties) = synchronized(lock) {
            if (registered && candidate == network) refreshFromCallback(candidate)
        }
        override fun onCapabilitiesChanged(candidate: Network, capabilities: NetworkCapabilities) = synchronized(lock) {
            if (registered && candidate == network) refreshFromCallback(candidate)
        }
        override fun onLost(candidate: Network) = synchronized(lock) {
            if (registered && candidate == network) refreshFromCallback(null)
        }
    }

    fun start(): Network = synchronized(lock) {
        if (!registered) {
            val request = NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                .build()
            registered = true
            try { manager.requestNetwork(request, callback) } catch (failure: Throwable) {
                registered = false
                throw failure
            }
        }
        refresh(selectPhysicalNetwork())
        context().network
    }

    private fun eligible(candidate: Network): Boolean = manager.getNetworkCapabilities(candidate)?.let {
        it.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN) &&
            it.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
    } == true

    private fun selectPhysicalNetwork(): Network? {
        manager.activeNetwork?.let { if (eligible(it)) return it }
        network?.let { if (eligible(it)) return it }
        return manager.allNetworks.filter(::eligible).sortedByDescending {
            manager.getNetworkCapabilities(it)?.hasCapability(NetworkCapabilities.NET_CAPABILITY_VALIDATED) == true
        }.firstOrNull()
    }

    private fun refresh(candidate: Network?) {
        val selected = candidate?.takeIf(::eligible)
        val properties = selected?.let(manager::getLinkProperties)
        val privateDNS = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P && properties != null)
            "${properties.isPrivateDnsActive}|${properties.privateDnsServerName}" else "unavailable"
        val next = if (properties == null) "unavailable" else
            "${selected.networkHandle}|${properties.interfaceName}|${properties.linkAddresses}|" +
                "${properties.dnsServers}|${properties.routes}|$privateDNS"
        if (signature != next) {
            // Invalidate outstanding lookups before asking Android to use the
            // new link. A rejected binding must never become a usable context.
            signature = ""
            network = null
            revision++
            queries.values.forEach { it.signal.cancel(); it.done.countDown() }
            val replacement = selected.takeIf { properties != null }
            check(service.setUnderlyingNetworks(replacement?.let { arrayOf(it) } ?: emptyArray())) {
                "VpnService.setUnderlyingNetworks rejected the physical network"
            }
            signature = next
            network = replacement
            diagnostic("underlying network revision=$revision context=$signature")
        }
    }

    private fun refreshFromCallback(candidate: Network?) {
        try {
            refresh(candidate)
        } catch (failure: Exception) {
            // Connectivity callbacks cannot return an error to Go. Keep the
            // failed context unavailable; the next resolver/protection call
            // retries capture and propagates any continuing failure.
            network = null
            signature = ""
            diagnostic("underlying network refresh failed: ${failure.stackTraceToString()}")
        }
    }

    private fun context(): Context {
        check(registered) { "Underlying network owner is stopped" }
        refresh(network?.takeIf(::eligible) ?: selectPhysicalNetwork())
        return Context(checkNotNull(network) { "No physical network is available" }, revision.toString())
    }

    fun snapshot(): String = synchronized(lock) {
        val current = context()
        JSONObject().put("revision", current.revision).toString()
    }

    fun protect(fd: Int): Boolean = synchronized(lock) {
        val current = context()
        check(service.protect(fd)) { "VpnService.protect returned false" }
        // fromFd duplicates the descriptor; closing it cannot close Go's socket.
        ParcelFileDescriptor.fromFd(fd).use { current.network.bindSocket(it.fileDescriptor) }
        true
    }

    fun resolve(expectedRevision: String, queryID: Long, host: String, timeoutMillis: Long): String {
        val startedAt = SystemClock.elapsedRealtime()
        val query = Query(CancellationSignal(), CountDownLatch(1))
        val current = synchronized(lock) {
            val captured = context()
            check(captured.revision == expectedRevision) { "Underlying network changed before DNS query" }
            check(queries.putIfAbsent(queryID, query) == null) { "Duplicate DNS query identity" }
            if (canceled.remove(queryID)) { query.signal.cancel(); query.done.countDown() }
            captured
        }
        var addresses: List<InetAddress>? = null
        var error: Throwable? = null
        try {
            check(timeoutMillis > 0) { "DNS deadline expired" }
            if (!query.signal.isCanceled && Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                DnsResolver.getInstance().query(current.network, host, DnsResolver.FLAG_EMPTY,
                    directExecutor, query.signal, object : DnsResolver.Callback<List<InetAddress>> {
                        override fun onAnswer(answer: List<InetAddress>, rcode: Int) {
                            if (rcode == 0) addresses = answer else error = IllegalStateException("DNS response code $rcode for $host")
                            query.done.countDown()
                        }
                        override fun onError(failure: DnsResolver.DnsException) {
                            error = failure
                            query.done.countDown()
                        }
                    })
            } else if (!query.signal.isCanceled) {
                // API 26–28 exposes only synchronous, Network-scoped native
                // resolution. Keep the JNI call owned until it returns; never
                // detach a lookup thread or accept a canceled/obsolete answer.
                addresses = current.network.getAllByName(host).toList()
                query.done.countDown()
            }
            val remaining = timeoutMillis - (SystemClock.elapsedRealtime() - startedAt)
            check(remaining > 0 && query.done.await(remaining, TimeUnit.MILLISECONDS)) { "Underlying DNS deadline expired for $host" }
            check(!query.signal.isCanceled) { "Underlying DNS query was canceled" }
            error?.let { throw it }
            synchronized(lock) {
                check(context() == current) { "Underlying network changed during DNS query" }
            }
            val answer = checkNotNull(addresses) { "Underlying DNS returned no answer" }
            check(answer.isNotEmpty()) { "Underlying DNS returned no addresses" }
            return JSONObject().put("addresses", JSONArray(answer.map { it.hostAddress })).toString()
        } finally {
            query.signal.cancel()
            synchronized(lock) { queries.remove(queryID); canceled.remove(queryID) }
        }
    }

    fun cancel(queryID: Long) = synchronized(lock) {
        val query = queries[queryID]
        if (query == null) canceled.add(queryID) else { query.signal.cancel(); query.done.countDown() }
    }

    // Go calls this after joining its cancellation callback, so neither a late
    // cancel nor an early cancel can leave a query identity retained forever.
    fun finish(queryID: Long) { synchronized(lock) { canceled.remove(queryID) } }

    fun stop() = synchronized(lock) {
        queries.values.forEach { it.signal.cancel(); it.done.countDown() }
        if (registered) manager.unregisterNetworkCallback(callback)
        registered = false
        network = null
        signature = ""
        canceled.clear()
    }
}
