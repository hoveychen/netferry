package com.netferry.app.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
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
    private var vpnInterface: ParcelFileDescriptor? = null
    private var currentProfileName: String = ""

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
            vpnInterface = builder.establish() ?: run {
                AppLog.e(TAG, "builder.establish() returned null")
                _vpnState.value = VpnState.ERROR
                stopSelf()
                return START_NOT_STICKY
            }
            AppLog.d(TAG, "VPN interface established, fd=${vpnInterface!!.fd}")

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
            val tunFd = vpnInterface!!.fd
            AppLog.d(TAG, "Starting engine on background thread...")
            Thread {
                try {
                    eng.startWithTUN(configJson, tunFd)
                    AppLog.d(TAG, "Engine started successfully with TUN fd=$tunFd")
                } catch (e: Exception) {
                    // Only handle if we haven't been disconnected already
                    if (engine != null) {
                        AppLog.e(TAG, "Engine startWithTUN failed", e)
                        _vpnState.value = VpnState.ERROR
                        _logMessages.value = _logMessages.value + "Error: ${e.message}"
                    }
                }
            }.start()

        } catch (e: Exception) {
            AppLog.e(TAG, "Failed to start VPN setup (${e.javaClass.simpleName})", e)
            _vpnState.value = VpnState.ERROR
            _logMessages.value = _logMessages.value + "Error: ${e.message}"
            disconnect()
        }

        return START_REDELIVER_INTENT
    }

    fun disconnect() {
        _vpnState.value = VpnState.DISCONNECTED
        _connectedProfileId.value = null
        val eng = engine
        val vpnFd = vpnInterface
        engine = null
        vpnInterface = null

        // Close VPN interface FIRST on the main thread to tear down VPN routing
        // immediately. Engine.Stop() also closes the underlying fd (no-op double
        // close), so this is safe even if the background thread races.
        try {
            vpnFd?.close()
        } catch (e: Exception) {
            Log.e(TAG, "Error closing VPN interface", e)
        }

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

        fun startVpn(context: Context, profile: Profile) {
            AppLog.d(TAG, "startVpn called: profile='${profile.name}', id=${profile.id}")
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
