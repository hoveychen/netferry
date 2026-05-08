package com.netferry.app.model

import com.google.gson.Gson
import com.google.gson.annotations.SerializedName
import com.netferry.app.util.formatBytes
import com.netferry.app.util.formatSpeed

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

    val downloadSpeed: String get() = formatSpeed(rxBytesPerSec)
    val uploadSpeed: String get() = formatSpeed(txBytesPerSec)
    val totalDownloaded: String get() = formatBytes(rxBytesTotal)
    val totalUploaded: String get() = formatBytes(txBytesTotal)
}
