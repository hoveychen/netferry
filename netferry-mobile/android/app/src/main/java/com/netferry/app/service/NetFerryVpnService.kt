package com.netferry.app.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.VpnService
import android.os.Build
import android.util.Log
import com.netferry.app.AppLog
import com.netferry.app.MainActivity
import com.netferry.app.R
import com.netferry.app.model.Profile
import com.netferry.app.model.TunnelStats
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import mobile.Mobile
import mobile.PlatformCallback

class NetFerryVpnService : VpnService() {

    private var engine: mobile.Engine? = null
    private var tunFd: Int = -1
    private var currentProfileName: String = ""

    // Tracks the device's current default network so we can ask the engine to
    // reconnect proactively when it changes (Wifi↔cellular, Wifi reconnect,
    // etc.). Without this the engine waits for smux's 30s KeepAliveTimeout
    // before noticing the underlying interface died.
    private var lastDefaultNetwork: Network? = null
    private var networkCallbackRegistered = false
    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            val previous = lastDefaultNetwork
            lastDefaultNetwork = network
            if (previous != null && previous != network) {
                AppLog.d(TAG, "default network changed ($previous -> $network); forcing SSH reconnect")
                engine?.notifyNetworkChange()
            }
        }

        override fun onLost(network: Network) {
            if (lastDefaultNetwork == network) {
                lastDefaultNetwork = null
                AppLog.d(TAG, "default network lost ($network); forcing SSH reconnect")
                engine?.notifyNetworkChange()
            }
        }
    }

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        instance = this
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        AppLog.d(TAG, "onStartCommand: action=${intent?.action}, hasConfig=${intent?.hasExtra(EXTRA_CONFIG_JSON)}")
        if (intent?.action == ACTION_STOP) {
            disconnect()
            return START_NOT_STICKY
        }

        val configJson = intent?.getStringExtra(EXTRA_CONFIG_JSON) ?: run {
            Log.e(TAG, "No config JSON in intent")
            stopSelf()
            return START_NOT_STICKY
        }
        currentProfileName = intent.getStringExtra(EXTRA_PROFILE_NAME) ?: "Unknown"
        val profileId = intent.getStringExtra(EXTRA_PROFILE_ID) ?: ""
        val subnetsJson = intent.getStringExtra(EXTRA_SUBNETS) ?: "[]"
        val isFullTunnel = intent.getBooleanExtra(EXTRA_FULL_TUNNEL, true)
        val mtu = intent.getIntExtra(EXTRA_MTU, 1500)

        _connectedProfileId.value = profileId
        startForeground(NOTIFICATION_ID, buildNotification("Connecting..."))
        _vpnState.value = VpnState.CONNECTING

        AppLog.d(TAG, "Building VPN: mtu=$mtu, fullTunnel=$isFullTunnel, subnets=$subnetsJson")

        try {
            val builder = Builder()
            AppLog.d(TAG, "Builder created")
            builder.setSession("NetFerry - $currentProfileName")
            AppLog.d(TAG, "setSession OK")
            builder.addAddress("10.0.0.1", 24)
            AppLog.d(TAG, "addAddress OK")
            builder.setMtu(mtu)
            AppLog.d(TAG, "setMtu OK")
            // Android rejects loopback (127.0.0.1) as VPN DNS. Use a virtual
            // address within the VPN subnet — the TUN forwarder intercepts all
            // port-53 traffic regardless of destination IP.
            builder.addDnsServer("10.0.0.2")
            AppLog.d(TAG, "addDnsServer OK")

            if (isFullTunnel) {
                builder.addRoute("0.0.0.0", 0)
                AppLog.d(TAG, "Added full-tunnel route")
            } else {
                val subnets = parseSubnets(subnetsJson)
                AppLog.d(TAG, "Adding ${subnets.size} subnet routes: $subnets")
                for (subnet in subnets) {
                    val parts = subnet.split("/")
                    if (parts.size == 2) {
                        builder.addRoute(parts[0], parts[1].toIntOrNull() ?: 32)
                    }
                }
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                builder.setMetered(false)
            }

            AppLog.d(TAG, "Calling builder.establish()...")
            val pfd = builder.establish() ?: run {
                AppLog.e(TAG, "builder.establish() returned null")
                _lastError.value = "VpnService.builder.establish() returned null (revoked or permission lost)"
                _vpnState.value = VpnState.ERROR
                stopSelf()
                return START_NOT_STICKY
            }
            // Detach the fd so the Go engine becomes its sole owner. Holding the
            // ParcelFileDescriptor and also handing its fd to os.NewFile in Go
            // results in two independent closers for the same kernel fd; if the
            // Java side closes first during disconnect, the kernel can recycle
            // the fd before Engine.Stop's tunFile.Close runs, and that second
            // close lands on whatever socket got the recycled number — easy to
            // crash mid-handshake when the SSH dial is still racing.
            tunFd = pfd.detachFd()
            pfd.close() // cheap no-op after detachFd; releases the wrapper.
            AppLog.d(TAG, "VPN interface established, fd=$tunFd")

            val callback = object : PlatformCallback {
                override fun protectSocket(fd: Int): Boolean {
                    return this@NetFerryVpnService.protect(fd)
                }

                override fun onStateChange(state: String) {
                    AppLog.d(TAG, "Engine state change: $state")
                    when (state) {
                        "connected" -> {
                            _vpnState.value = VpnState.CONNECTED
                            updateNotification("Connected to $currentProfileName")
                        }
                        "connecting" -> {
                            _vpnState.value = VpnState.CONNECTING
                        }
                        "reconnecting" -> {
                            _vpnState.value = VpnState.CONNECTING
                            updateNotification("Reconnecting to $currentProfileName...")
                        }
                        "disconnected" -> {
                            _vpnState.value = VpnState.DISCONNECTED
                            _connectedProfileId.value = null
                        }
                        "error" -> {
                            _vpnState.value = VpnState.ERROR
                        }
                    }
                }

                override fun onPortsChanged(socksPort: Int, dnsPort: Int) {
                    // No-op on Android: traffic goes through TUN forwarder directly.
                }

                override fun onLog(msg: String) {
                    Log.d(TAG, msg)
                    val logs = _logMessages.value.toMutableList()
                    logs.add(msg)
                    if (logs.size > MAX_LOG_LINES) {
                        logs.removeAt(0)
                    }
                    _logMessages.value = logs
                }

                override fun onDeployProgress(sent: Long, total: Long) {
                    val current = _deployProgress.value
                    _deployProgress.value = DeployProgress(
                        sent = sent,
                        total = total,
                        reason = current?.reason
                    )
                }

                override fun onDeployReason(reason: String) {
                    val current = _deployProgress.value
                    _deployProgress.value = DeployProgress(
                        sent = current?.sent ?: 0,
                        total = current?.total ?: 0,
                        reason = reason
                    )
                }

                override fun onStats(statsJSON: String) {
                    val stats = TunnelStats.fromJson(statsJSON)
                    _tunnelStats.value = stats

                    val history = _speedHistory.value.toMutableList()
                    history.add(SpeedSample(stats.rxBytesPerSec, stats.txBytesPerSec))
                    if (history.size > MAX_SPEED_HISTORY) {
                        history.removeAt(0)
                    }
                    _speedHistory.value = history
                }
            }

            val eng = Mobile.newEngine(callback)
            engine = eng

            // StartWithTUN connects SSH, starts SOCKS5/DNS relay, AND reads
            // IP packets from the TUN fd via a userspace TCP/IP stack (gVisor
            // netstack). TCP connections are forwarded through the mux tunnel;
            // DNS queries (port 53) are forwarded through the mux DNS channel.
            //
            // Engine.Start() blocks until the tunnel is established, so run
            // on a background thread to avoid freezing the UI.
            val startFd = tunFd
            AppLog.d(TAG, "Starting engine on background thread...")
            Thread {
                try {
                    eng.startWithTUN(configJson, startFd)
                    AppLog.d(TAG, "Engine started successfully with TUN fd=$startFd")
                    registerNetworkCallback()
                } catch (e: Exception) {
                    // Only handle if we haven't been disconnected already
                    if (engine != null) {
                        AppLog.e(TAG, "Engine startWithTUN failed", e)
                        _lastError.value = e.message ?: e.javaClass.simpleName
                        _vpnState.value = VpnState.ERROR
                        _logMessages.value = _logMessages.value + "Error: ${e.message}"
                    }
                }
            }.start()

        } catch (e: Exception) {
            AppLog.e(TAG, "Failed to start VPN setup (${e.javaClass.simpleName})", e)
            _lastError.value = "${e.javaClass.simpleName}: ${e.message ?: "<no message>"}"
            _vpnState.value = VpnState.ERROR
            _logMessages.value = _logMessages.value + "Error: ${e.message}"
            disconnect()
        }

        return START_REDELIVER_INTENT
    }

    fun disconnect() {
        _vpnState.value = VpnState.DISCONNECTED
        _connectedProfileId.value = null
        unregisterNetworkCallback()
        val eng = engine
        engine = null
        tunFd = -1

        // The Go engine owns the TUN fd (transferred via detachFd at establish
        // time). Engine.Stop closes it eagerly before waiting for goroutines,
        // so VPN routing tears down promptly even though Stop runs off the
        // main thread. We deliberately do NOT close the fd here — doing so
        // races Engine.Stop's tunFile.Close on the same kernel fd, which can
        // close a recycled socket if the SSH dial is still in flight.

        // engine.stop() blocks (waits for Go goroutines), run off main thread
        Thread {
            try {
                eng?.stop()
            } catch (e: Exception) {
                Log.e(TAG, "Error stopping engine", e)
            }
        }.start()
        _logMessages.value = emptyList()
        _speedHistory.value = emptyList()
        _tunnelStats.value = TunnelStats()
        _deployProgress.value = null
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    override fun onDestroy() {
        disconnect()
        instance = null
        super.onDestroy()
    }

    override fun onRevoke() {
        disconnect()
        super.onRevoke()
    }

    private fun createNotificationChannel() {
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.vpn_notification_channel),
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = "Shows VPN connection status"
            setShowBadge(false)
        }
        val manager = getSystemService(NotificationManager::class.java)
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(text: String): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java).apply {
                flags = Intent.FLAG_ACTIVITY_SINGLE_TOP
            },
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val disconnectIntent = PendingIntent.getService(
            this, 1,
            Intent(this, NetFerryVpnService::class.java).apply {
                action = ACTION_STOP
            },
            PendingIntent.FLAG_IMMUTABLE
        )

        return Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.vpn_notification_title))
            .setContentText(text)
            .setSmallIcon(android.R.drawable.ic_lock_lock)
            .setContentIntent(pendingIntent)
            .addAction(
                Notification.Action.Builder(
                    null,
                    getString(R.string.action_disconnect),
                    disconnectIntent
                ).build()
            )
            .setOngoing(true)
            .build()
    }

    private fun updateNotification(text: String) {
        val manager = getSystemService(NotificationManager::class.java)
        manager.notify(NOTIFICATION_ID, buildNotification(text))
    }

    private fun registerNetworkCallback() {
        if (networkCallbackRegistered) return
        try {
            val cm = getSystemService(ConnectivityManager::class.java)
            cm?.registerDefaultNetworkCallback(networkCallback)
            networkCallbackRegistered = true
            AppLog.d(TAG, "registered default network callback")
        } catch (e: Exception) {
            AppLog.e(TAG, "registerDefaultNetworkCallback failed", e)
        }
    }

    private fun unregisterNetworkCallback() {
        if (!networkCallbackRegistered) return
        try {
            val cm = getSystemService(ConnectivityManager::class.java)
            cm?.unregisterNetworkCallback(networkCallback)
        } catch (e: Exception) {
            AppLog.e(TAG, "unregisterNetworkCallback failed", e)
        }
        networkCallbackRegistered = false
        lastDefaultNetwork = null
    }

    private fun parseSubnets(json: String): List<String> {
        return try {
            json.trim('[', ']')
                .split(",")
                .map { it.trim().trim('"') }
                .filter { it.isNotEmpty() }
        } catch (e: Exception) {
            emptyList()
        }
    }

    data class SpeedSample(
        val rxBytesPerSec: Long,
        val txBytesPerSec: Long
    )

    data class DeployProgress(
        val sent: Long,
        val total: Long,
        val reason: String? = null
    )

    enum class VpnState {
        DISCONNECTED, CONNECTING, CONNECTED, ERROR
    }

    companion object {
        private const val TAG = "NetFerryVPN"
        private const val CHANNEL_ID = "netferry_vpn"
        private const val NOTIFICATION_ID = 1
        private const val MAX_LOG_LINES = 500
        private const val MAX_SPEED_HISTORY = 60

        const val ACTION_STOP = "com.netferry.app.STOP_VPN"
        const val EXTRA_CONFIG_JSON = "config_json"
        const val EXTRA_PROFILE_NAME = "profile_name"
        const val EXTRA_PROFILE_ID = "profile_id"
        const val EXTRA_SUBNETS = "subnets"
        const val EXTRA_FULL_TUNNEL = "full_tunnel"
        const val EXTRA_MTU = "mtu"

        var instance: NetFerryVpnService? = null
            private set

        private val _vpnState = MutableStateFlow(VpnState.DISCONNECTED)
        val vpnState: StateFlow<VpnState> = _vpnState.asStateFlow()

        private val _tunnelStats = MutableStateFlow(TunnelStats())
        val tunnelStats: StateFlow<TunnelStats> = _tunnelStats.asStateFlow()

        private val _logMessages = MutableStateFlow<List<String>>(emptyList())
        val logMessages: StateFlow<List<String>> = _logMessages.asStateFlow()

        private val _speedHistory = MutableStateFlow<List<SpeedSample>>(emptyList())
        val speedHistory: StateFlow<List<SpeedSample>> = _speedHistory.asStateFlow()

        private val _connectedProfileId = MutableStateFlow<String?>(null)
        val connectedProfileId: StateFlow<String?> = _connectedProfileId.asStateFlow()

        private val _deployProgress = MutableStateFlow<DeployProgress?>(null)
        val deployProgress: StateFlow<DeployProgress?> = _deployProgress.asStateFlow()

        private val _lastError = MutableStateFlow<String?>(null)
        val lastError: StateFlow<String?> = _lastError.asStateFlow()

        fun clearLastError() {
            _lastError.value = null
        }

        fun startVpn(context: Context, profile: Profile) {
            AppLog.d(TAG, "startVpn called: profile='${profile.name}', id=${profile.id}")
            // New attempt — wipe prior error so the banner/dialog doesn't linger.
            _lastError.value = null
            val intent = Intent(context, NetFerryVpnService::class.java).apply {
                putExtra(EXTRA_CONFIG_JSON, profile.toConfigJson())
                putExtra(EXTRA_PROFILE_NAME, profile.name)
                putExtra(EXTRA_PROFILE_ID, profile.id)
                putExtra(EXTRA_SUBNETS, profile.subnets.joinToString(",", "[", "]") { "\"$it\"" })
                putExtra(EXTRA_FULL_TUNNEL, profile.isFullTunnel)
                putExtra(EXTRA_MTU, profile.mtu)
            }
            context.startForegroundService(intent)
        }

        fun stopVpn(context: Context) {
            val intent = Intent(context, NetFerryVpnService::class.java).apply {
                action = ACTION_STOP
            }
            context.startService(intent)
        }
    }
}
