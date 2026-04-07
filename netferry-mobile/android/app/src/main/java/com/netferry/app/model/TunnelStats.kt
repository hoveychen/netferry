package com.netferry.app.model

import com.google.gson.Gson
import com.google.gson.annotations.SerializedName

data class TunnelStats(
    @SerializedName("rxBytesPerSec")
    val rxBytesPerSec: Long = 0,
    @SerializedName("txBytesPerSec")
    val txBytesPerSec: Long = 0,
    @SerializedName("totalRxBytes")
    val rxBytesTotal: Long = 0,
    @SerializedName("totalTxBytes")
    val txBytesTotal: Long = 0,
    @SerializedName("activeConns")
    val activeConnections: Int = 0,
    @SerializedName("totalConns")
    val totalConnections: Long = 0,
    @SerializedName("dnsQueries")
    val dnsQueries: Long = 0
) {
    companion object {
        private val gson = Gson()

        fun fromJson(json: String): TunnelStats {
            return try {
                gson.fromJson(json, TunnelStats::class.java) ?: TunnelStats()
            } catch (e: Exception) {
                TunnelStats()
            }
        }
    }

    fun formatSpeed(bytesPerSec: Long): String {
        return when {
            bytesPerSec < 1024 -> "$bytesPerSec B/s"
            bytesPerSec < 1024 * 1024 -> "%.1f KB/s".format(bytesPerSec / 1024.0)
            bytesPerSec < 1024L * 1024 * 1024 -> "%.1f MB/s".format(bytesPerSec / (1024.0 * 1024))
            else -> "%.2f GB/s".format(bytesPerSec / (1024.0 * 1024 * 1024))
        }
    }

    fun formatBytes(bytes: Long): String {
        return when {
            bytes < 1024 -> "$bytes B"
            bytes < 1024 * 1024 -> "%.1f KB".format(bytes / 1024.0)
            bytes < 1024L * 1024 * 1024 -> "%.1f MB".format(bytes / (1024.0 * 1024))
            else -> "%.2f GB".format(bytes / (1024.0 * 1024 * 1024))
        }
    }

    val downloadSpeed: String get() = formatSpeed(rxBytesPerSec)
    val uploadSpeed: String get() = formatSpeed(txBytesPerSec)
    val totalDownloaded: String get() = formatBytes(rxBytesTotal)
    val totalUploaded: String get() = formatBytes(txBytesTotal)
}
