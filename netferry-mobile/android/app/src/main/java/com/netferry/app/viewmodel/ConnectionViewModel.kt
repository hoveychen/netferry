package com.netferry.app.viewmodel

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.netferry.app.model.TunnelStats
import com.netferry.app.service.NetFerryVpnService
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn

class ConnectionViewModel(application: Application) : AndroidViewModel(application) {

    val vpnState: StateFlow<NetFerryVpnService.VpnState> =
        NetFerryVpnService.vpnState.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            NetFerryVpnService.VpnState.DISCONNECTED
        )

    val tunnelStats: StateFlow<TunnelStats> =
        NetFerryVpnService.tunnelStats.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            TunnelStats()
        )

    val logMessages: StateFlow<List<String>> =
        NetFerryVpnService.logMessages.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            emptyList()
        )

    val speedHistory: StateFlow<List<NetFerryVpnService.SpeedSample>> =
        NetFerryVpnService.speedHistory.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            emptyList()
        )

    val connectedProfileId: StateFlow<String?> =
        NetFerryVpnService.connectedProfileId.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            null
        )

    val deployProgress: StateFlow<NetFerryVpnService.DeployProgress?> =
        NetFerryVpnService.deployProgress.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            null
        )

    val lastError: StateFlow<String?> =
        NetFerryVpnService.lastError.stateIn(
            viewModelScope,
            SharingStarted.WhileSubscribed(5000),
            null
        )

    fun disconnect() {
        NetFerryVpnService.stopVpn(getApplication())
    }

    fun dismissError() {
        NetFerryVpnService.clearLastError()
    }
}
