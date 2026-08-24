package com.dobby.feature.vpn_service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import androidx.core.content.ContextCompat
import com.dobby.backend.GoBackendWrapper
import com.dobby.feature.logging.Logger
import com.dobby.feature.main.domain.ConnectionStateRepository
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import org.koin.android.ext.android.inject
import java.util.UUID

/**
 * Android's deliberately small side of a v1 VPN session.
 *
 * The Go manager owns configuration parsing, profile selection, probing, protocol
 * processes and tunnel lifecycle.  This service owns only Android's foreground
 * requirement, a fresh Builder/TUN for each generation, and socket protection.
 */
class DobbyVpnService : VpnService() {
    companion object {
        @Volatile
        internal var nativePlatformRegistrar: (DobbyVpnService) -> Unit =
            GoBackendWrapper::registerSessionPlatform

        internal fun resetNativePlatformRegistrar() {
            nativePlatformRegistrar = GoBackendWrapper::registerSessionPlatform
        }
        private const val ACTION_PREPARE = "com.dobby.vpn.action.PREPARE"
        private const val ACTION_STOP = "com.dobby.vpn.action.STOP"
        private const val EXTRA_SESSION_ID = "com.dobby.vpn.extra.SESSION_ID"
        private const val EXTRA_GENERATION = "com.dobby.vpn.extra.GENERATION"
        private const val EXTRA_USER_INITIATED_STOP = "com.dobby.vpn.extra.USER_INITIATED_STOP"
        private const val FOREGROUND_CHANNEL_ID = "dobby_vpn"
        private const val FOREGROUND_NOTIFICATION_ID = 101

        /** Starts only the Android shell; protocol work always starts through sessionapi. */
        fun createPrepareIntent(context: Context, sessionId: String): Intent =
            Intent(context, DobbyVpnService::class.java)
                .setAction(ACTION_PREPARE)
                .putExtra(EXTRA_SESSION_ID, sessionId)

        fun createStopIntent(context: Context, generation: Long, isUserInitiated: Boolean): Intent =
            Intent(context, DobbyVpnService::class.java)
                .setAction(ACTION_STOP)
                .putExtra(EXTRA_GENERATION, generation)
                .putExtra(EXTRA_USER_INITIATED_STOP, isUserInitiated)

        internal fun requestShell(context: Context, sessionId: String) {
            PlatformServiceRegistry.expect(sessionId)
            ContextCompat.startForegroundService(context, createPrepareIntent(context, sessionId))
        }
    }

    private val logger: Logger by inject()
    private val connectionState: ConnectionStateRepository by inject()
    val serviceId: String = UUID.randomUUID().toString().take(8)

    /** The service retains this original descriptor while Go owns a duplicated FD. */
    var vpnInterface: ParcelFileDescriptor? = null
    var goTunFd: Int? = null
    private var activeSessionId: String? = null
    private var activeGeneration: Long = -1L

    override fun onCreate() {
        super.onCreate()
        nativePlatformRegistrar(this)
        logger.log("[svc:$serviceId] created Android session platform shell")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val action = intent?.action ?: ACTION_PREPARE
        val sessionId = intent?.getStringExtra(EXTRA_SESSION_ID)
        val generation = intent?.getLongExtra(EXTRA_GENERATION, -1L) ?: -1L
        logger.log("[svc:$serviceId] command action=$action generation=$generation sessionProvided=${!sessionId.isNullOrBlank()}")
        when (action) {
            ACTION_PREPARE -> {
                // Foreground promotion precedes every possible callback to Go that may create a TUN.
                ensureForeground()
                logger.log("[svc:$serviceId] foreground promotion complete")
                if (!sessionId.isNullOrBlank()) activeSessionId = sessionId
                if (!sessionId.isNullOrBlank()) {
                    // Complete local preparation before waking clients waiting for readiness.
                    // Otherwise an observer can see a ready service before this transition has
                    // been fully recorded, making the lifecycle boundary racy.
                    logger.log("[svc:$serviceId] platform preparation complete")
                    PlatformServiceRegistry.ready(this, sessionId)
                }
            }
            ACTION_STOP -> releaseForIntent(generation, startId)
        }
        return START_NOT_STICKY
    }

    /** Called by the generation-correlated gomobile callback. */
    @Synchronized
    fun acquireTunnel(sessionId: String, generation: Long): Int {
        if (sessionId != activeSessionId) {
            logger.log("[svc:$serviceId] reject TUN for unprepared or stale session")
            return -1
        }
        if (generation < activeGeneration || vpnInterface != null || goTunFd != null) {
            logger.log("[svc:$serviceId] reject overlapping/stale TUN generation=$generation active=$activeGeneration")
            return -1
        }
        ensureForeground()
        val established = runCatching {
            // One policy for every protocol. Do not disallow this app: Dobby traffic is included
            // in the VPN and protocol sockets are explicitly protected before they dial.
            Builder()
                .setSession("Dobby VPN")
                .setMtu(1500)
                .addAddress("10.7.0.2", 32)
                .addRoute("0.0.0.0", 0)
                .addRoute("::", 0)
                .addDnsServer("1.1.1.1")
                .addDnsServer("2606:4700:4700::1111")
                .establish()
        }.getOrElse { error ->
            logger.log("[svc:$serviceId] TUN establish failed generation=$generation errorType=${error::class.simpleName ?: "UNKNOWN"}")
            null
        } ?: return -1

        val duplicate = runCatching { ParcelFileDescriptor.dup(established.fileDescriptor) }
            .getOrElse { error ->
                logger.log("[svc:$serviceId] TUN duplication failed generation=$generation errorType=${error::class.simpleName ?: "UNKNOWN"}")
                established.close()
                return -1
            }
        val fd = runCatching { duplicate.detachFd() }.getOrElse { error ->
            logger.log("[svc:$serviceId] TUN FD transfer failed generation=$generation errorType=${error::class.simpleName ?: "UNKNOWN"}")
            duplicate.close()
            established.close()
            return -1
        }
        vpnInterface = established
        goTunFd = fd
        activeGeneration = generation
        logger.log("[svc:$serviceId] fresh TUN acquired generation=$generation")
        return fd
    }

    /** Go has already closed [fd]; close exactly the service-owned matching descriptor. */
    @Synchronized
    fun releaseTunnel(sessionId: String, generation: Long, fd: Int) {
        if (sessionId != activeSessionId || generation != activeGeneration || fd != goTunFd) {
            logger.log("[svc:$serviceId] ignore stale TUN release generation=$generation active=$activeGeneration")
            return
        }
        closeTunnel("Go released generation=$generation")
    }

    @Synchronized
    fun protectProtocolSocket(sessionId: String, generation: Long, fd: Int): Boolean {
        if (sessionId != activeSessionId || generation != activeGeneration || fd < 0) {
            logger.log("[svc:$serviceId] reject stale socket protection generation=$generation active=$activeGeneration")
            return false
        }
        return protect(fd).also { protected ->
            if (!protected) logger.log("[svc:$serviceId] socket protection failed; native dial must abort")
        }
    }

    @Synchronized
    fun publishState(sessionId: String, generation: Long, sequence: Long, state: String, failureCode: String) {
        if (sessionId != activeSessionId || generation < activeGeneration) {
            logger.log("[svc:$serviceId] ignore stale Go state=$state generation=$generation")
            return
        }
        connectionState.tryPublishSessionEvent(
            sessionId = sessionId,
            generation = generation,
            sequence = sequence,
            state = state,
            failureCode = failureCode,
        )
        if (state == "IDLE" || state == "FAILED" || state == "DESTROYED") {
            if (vpnInterface == null) stopForeground(STOP_FOREGROUND_REMOVE)
        }
        logger.log("[svc:$serviceId] Go state=$state generation=$generation failure=$failureCode")
    }

    override fun onDestroy() {
        // Stop Go before closing the service-owned descriptor. Do not hold this monitor:
        // Stop can synchronously invoke ReleaseTunnel back into this service.
        val session = activeSessionId
        val generation = activeGeneration
        if (session != null && generation > 0L) {
            val stopped = runCatching {
                sessionStopSucceeded(GoBackendWrapper.stopSession(session, UUID.randomUUID().toString(), generation))
            }.getOrElse {
                logger.log("[svc:$serviceId] session stop during destroy failed errorType=${it::class.simpleName ?: "UNKNOWN"}")
                false
            }
            if (!stopped) logger.log("[svc:$serviceId] Go rejected stop during destroy; forcing Android descriptor close")
        }
        synchronized(this) {
            closeTunnel("service destroyed")
            activeSessionId = null
        }
        PlatformServiceRegistry.clear(this)
        super.onDestroy()
    }

    private fun releaseForIntent(generation: Long, startId: Int) {
        val (session, active) = synchronized(this) {
            if (activeGeneration > 0L && generation != activeGeneration) {
                logger.log("[svc:$serviceId] ignore stale stop intent generation=$generation active=$activeGeneration")
                return
            }
            activeSessionId to activeGeneration
        }
        // A generation-tagged intent is only a platform request. Ask Go to stop the
        // authoritative runtime first; its release callback closes the matching PFD.
        if (session != null && active > 0L) {
            val stopped = runCatching {
                sessionStopSucceeded(GoBackendWrapper.stopSession(session, UUID.randomUUID().toString(), active))
            }.getOrElse {
                logger.log("[svc:$serviceId] session stop from intent failed errorType=${it::class.simpleName ?: "UNKNOWN"}")
                false
            }
            if (!stopped) {
                logger.log("[svc:$serviceId] Go rejected generation-tagged stop; retaining descriptor")
                return
            }
        }
        synchronized(this) { closeTunnel("generation-tagged stop intent") }
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelfResult(startId)
    }

    private fun closeTunnel(reason: String) {
        val fd = goTunFd
        goTunFd = null
        runCatching { vpnInterface?.close() }
        vpnInterface = null
        activeGeneration = -1L
        logger.log("[svc:$serviceId] closed service-owned TUN descriptorPresent=${fd != null} reason=$reason")
    }

    private fun sessionStopSucceeded(payload: String): Boolean = runCatching {
        Json.parseToJsonElement(payload).jsonObject["ok"]?.jsonPrimitive?.booleanOrNull == true
    }.getOrDefault(false)

    private fun ensureForeground() {
        val manager = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            manager.createNotificationChannel(NotificationChannel(FOREGROUND_CHANNEL_ID, "Dobby VPN", NotificationManager.IMPORTANCE_LOW))
        }
        val notification: Notification = Notification.Builder(this, FOREGROUND_CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_sys_warning)
            .setContentTitle("Dobby VPN")
            .setContentText("VPN connection is active")
            .setOngoing(true)
            .build()
        startForeground(FOREGROUND_NOTIFICATION_ID, notification)
    }
}

/** Process-recreation safe hand-off between the controller and foreground service. */
internal object PlatformServiceRegistry {
    private var expectedSession: String? = null
    private var service: DobbyVpnService? = null
    private var preparedSession: String? = null
    private var readySignal = CompletableDeferred<DobbyVpnService>()

    @Synchronized fun expect(sessionId: String) {
        expectedSession = sessionId
        if (preparedSession != sessionId) readySignal = CompletableDeferred()
    }
    @Synchronized fun ready(candidate: DobbyVpnService, sessionId: String) {
        service = candidate
        preparedSession = sessionId
        if (!readySignal.isCompleted) readySignal.complete(candidate)
    }
    @Synchronized fun clear(candidate: DobbyVpnService) {
        // An older service can finish destruction after its replacement has
        // reached foreground-ready. It must not invalidate the replacement's
        // session or wake-up signal.
        if (service !== candidate) return
        service = null
        preparedSession = null
        readySignal = CompletableDeferred()
    }
    @Synchronized fun current(sessionId: String): DobbyVpnService? =
        service?.takeIf { preparedSession == sessionId && expectedSession == sessionId }
    suspend fun awaitReady(timeoutMillis: Long): Boolean =
        expectedSession?.let(::current) != null ||
            withTimeoutOrNull(timeoutMillis) { readySignal.await(); expectedSession?.let(::current) != null } == true
}
