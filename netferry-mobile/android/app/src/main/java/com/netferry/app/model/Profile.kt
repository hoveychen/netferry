package com.netferry.app.model

import com.google.gson.annotations.SerializedName
import java.io.Serializable
import java.util.UUID

data class JumpHost(
    val remote: String = "",
    @SerializedName("identityKey")
    val identityKey: String = ""
) : Serializable

data class Profile(
    val id: String = UUID.randomUUID().toString(),
    val name: String = "",
    val remote: String = "",
    @SerializedName("identityKey")
    val identityKey: String = "",
    val jumpHosts: List<JumpHost> = emptyList(),
    val subnets: List<String> = listOf("0.0.0.0/0"),
    val excludeSubnets: List<String> = emptyList(),
    val autoNets: Boolean = false,
    val dns: String = "all",              // "off", "all", "specific"
    val dnsTarget: String = "",
    val enableUdp: Boolean = false,
    val blockUdp: Boolean = true,
    val poolSize: Int = 2,
    val splitConn: Boolean = false,
    val tcpBalanceMode: String = "least-loaded", // "round-robin" or "least-loaded"
    val latencyBufferSize: Int = 2097152,
    val autoExcludeLan: Boolean = true,
    val disableIpv6: Boolean = false,
    val extraSshOptions: String = "",
    val notes: String = "",
    val mtu: Int = 1500
) : Serializable {

    fun toConfigJson(): String {
        val sb = StringBuilder()
        sb.append("{")
        sb.append("\"remote\":${remote.toJsonString()},")
        sb.append("\"identityKey\":${identityKey.toJsonString()},")
        sb.append("\"jumpHosts\":[")
        sb.append(jumpHosts.joinToString(",") { jh ->
            "{\"remote\":${jh.remote.toJsonString()},\"identityKey\":${jh.identityKey.toJsonString()}}"
        })
        sb.append("],")
        sb.append("\"subnets\":${subnets.toJsonArray()},")
        sb.append("\"excludeSubnets\":${excludeSubnets.toJsonArray()},")
        sb.append("\"autoNets\":$autoNets,")
        sb.append("\"autoExcludeLan\":$autoExcludeLan,")
        sb.append("\"dns\":${dns.toJsonString()},")
        sb.append("\"dnsTarget\":${dnsTarget.toJsonString()},")
        sb.append("\"enableUdp\":$enableUdp,")
        sb.append("\"blockUdp\":$blockUdp,")
        sb.append("\"poolSize\":$poolSize,")
        sb.append("\"splitConn\":$splitConn,")
        sb.append("\"tcpBalanceMode\":${tcpBalanceMode.toJsonString()},")
        sb.append("\"latencyBufferSize\":$latencyBufferSize,")
        sb.append("\"disableIpv6\":$disableIpv6,")
        sb.append("\"extraSshOptions\":${extraSshOptions.toJsonString()},")
        sb.append("\"notes\":${notes.toJsonString()},")
        sb.append("\"mtu\":$mtu")
        sb.append("}")
        return sb.toString()
    }

    val isFullTunnel: Boolean
        get() = subnets.contains("0.0.0.0/0")

    companion object {
        private fun String.toJsonString(): String {
            val escaped = this
                .replace("\\", "\\\\")
                .replace("\"", "\\\"")
                .replace("\n", "\\n")
                .replace("\r", "\\r")
                .replace("\t", "\\t")
            return "\"$escaped\""
        }

        private fun List<String>.toJsonArray(): String {
            return joinToString(",", "[", "]") { it.toJsonString() }
        }
    }
}
