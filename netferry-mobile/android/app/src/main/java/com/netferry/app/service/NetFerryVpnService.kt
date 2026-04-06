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

        try {
            val builder = Builder()
                .setSession("NetFerry - $currentProfileName")
                .addAddress("10.0.0.1", 32)
                .setMtu(mtu)
                .addDnsServer("127.0.0.1")

            if (isFullTunnel) {
                builder.addRoute("0.0.0.0", 0)
            } else {
                val subnets = parseSubnets(subnetsJson)
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

            vpnInterface = builder.establish() ?: run {
                Log.e(TAG, "Failed to establish VPN interface")
                _vpnState.value = VpnState.ERROR
                stopSelf()
                return START_NOT_STICKY
            }

            val callback = object : PlatformCallback {
                override fun protectSocket(fd: Int): Boolean {
                    return this@NetFerryVpnService.protect(fd)
                }

                override fun onStateChange(state: String) {
                    Log.i(TAG, "Engine state: $state")
                    when (state) {
                        "connected" -> {
                            _vpnState.value = VpnState.CONNECTED
                            updateNotification("Connected to $currentProfileName")
                        }
                        "connecting" -> {
                            _vpnState.value = VpnState.CONNECTING
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
            val tunFd = vpnInterface!!.fd
            eng.startWithTUN(configJson, tunFd)

            Log.i(TAG, "Engine started with TUN fd=$tunFd")

        } catch (e: Exception) {
            Log.e(TAG, "Failed to start VPN engine", e)
            _vpnState.value = VpnState.ERROR
            _logMessages.value = _logMessages.value + "Error: ${e.message}"
            cleanup()
            stopSelf()
        }

        return START_STICKY
    }

    fun disconnect() {
        _vpnState.value = VpnState.DISCONNECTED
        _connectedProfileId.value = null
        cleanup()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
    }

    private fun cleanup() {
        try {
            engine?.stop()
        } catch (e: Exception) {
            Log.e(TAG, "Error stopping engine", e)
        }
        engine = null

        try {
            vpnInterface?.close()
        } catch (e: Exception) {
            Log.e(TAG, "Error closing VPN interface", e)
        }
        vpnInterface = null

        _logMessages.value = emptyList()
        _speedHistory.value = emptyList()
        _tunnelStats.value = TunnelStats()
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
