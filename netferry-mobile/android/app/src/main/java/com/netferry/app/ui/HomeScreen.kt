package com.netferry.app.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDownward
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netferry.app.R
import com.netferry.app.model.Profile
import com.netferry.app.model.TunnelStats
import com.netferry.app.model.TunnelStats.Companion.formatBytes
import com.netferry.app.service.NetFerryVpnService

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HomeScreen(
    profiles: List<Profile>,
    vpnState: NetFerryVpnService.VpnState,
    connectedProfileId: String?,
    stats: TunnelStats,
    speedHistory: List<NetFerryVpnService.SpeedSample>,
    logMessages: List<String>,
    deployProgress: NetFerryVpnService.DeployProgress? = null,
    onConnect: (Profile) -> Unit,
    onDisconnect: () -> Unit
) {
    when (vpnState) {
        NetFerryVpnService.VpnState.DISCONNECTED,
        NetFerryVpnService.VpnState.ERROR -> {
            DisconnectedHome(
                profiles = profiles,
                isError = vpnState == NetFerryVpnService.VpnState.ERROR,
                onConnect = onConnect
            )
        }
        NetFerryVpnService.VpnState.CONNECTING -> {
            ConnectingHome(
                profileName = connectedProfileId?.let { id ->
                    profiles.find { it.id == id }?.name
                } ?: "",
                deployProgress = deployProgress,
                onDisconnect = onDisconnect
            )
        }
        NetFerryVpnService.VpnState.CONNECTED -> {
            ConnectedHome(
                profileName = connectedProfileId?.let { id ->
                    profiles.find { it.id == id }?.name
                } ?: "",
                vpnState = vpnState,
                stats = stats,
                speedHistory = speedHistory,
                logMessages = logMessages,
                onDisconnect = onDisconnect
            )
        }
    }
}

// ── Disconnected: Hero + Profile Selector + Connect ──────────────────────────

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun DisconnectedHome(
    profiles: List<Profile>,
    isError: Boolean,
    onConnect: (Profile) -> Unit
) {
    var selectedProfileId by rememberSaveable { mutableStateOf<String?>(null) }
    // Initialize to first profile if none selected
    val effectiveId = selectedProfileId ?: profiles.firstOrNull()?.id
    val selectedProfile = profiles.find { it.id == effectiveId }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 24.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Spacer(modifier = Modifier.height(80.dp))

        // App icon
        Image(
            painter = painterResource(R.mipmap.ic_launcher),
            contentDescription = null,
            modifier = Modifier
                .size(88.dp)
                .clip(CircleShape)
        )

        Spacer(modifier = Modifier.height(20.dp))

        Text(
            text = stringResource(R.string.app_name),
            style = MaterialTheme.typography.headlineLarge,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.onBackground
        )

        Spacer(modifier = Modifier.height(6.dp))

        Text(
            text = stringResource(R.string.settings_app_desc),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )

        Spacer(modifier = Modifier.height(48.dp))

        // Error banner
        if (isError) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(MaterialTheme.colorScheme.error.copy(alpha = 0.08f))
                    .padding(16.dp)
            ) {
                Text(
                    text = stringResource(R.string.connection_error),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error
                )
            }
            Spacer(modifier = Modifier.height(16.dp))
        }

        if (profiles.isEmpty()) {
            // No profiles state
            Text(
                text = stringResource(R.string.home_no_profiles),
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurface
            )
            Spacer(modifier = Modifier.height(4.dp))
            Text(
                text = stringResource(R.string.home_no_profiles_desc),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center
            )
        } else {
            // Profile selector dropdown
            var expanded by rememberSaveable { mutableStateOf(false) }

            ExposedDropdownMenuBox(
                expanded = expanded,
                onExpandedChange = { expanded = it },
                modifier = Modifier.fillMaxWidth()
            ) {
                OutlinedTextField(
                    value = selectedProfile?.name ?: stringResource(R.string.home_select_profile),
                    onValueChange = {},
                    readOnly = true,
                    trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = expanded) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .menuAnchor(),
                    shape = RoundedCornerShape(12.dp)
                )

                ExposedDropdownMenu(
                    expanded = expanded,
                    onDismissRequest = { expanded = false }
                ) {
                    profiles.forEach { profile ->
                        DropdownMenuItem(
                            text = {
                                Column {
                                    Text(
                                        profile.name,
                                        style = MaterialTheme.typography.bodyLarge
                                    )
                                    Text(
                                        profile.remote,
                                        style = MaterialTheme.typography.bodySmall,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant
                                    )
                                }
                            },
                            onClick = {
                                selectedProfileId = profile.id
                                expanded = false
                            }
                        )
                    }
                }
            }

            Spacer(modifier = Modifier.height(24.dp))

            // Connect button
            Button(
                onClick = { selectedProfile?.let { onConnect(it) } },
                modifier = Modifier
                    .fillMaxWidth()
                    .height(56.dp),
                shape = RoundedCornerShape(16.dp),
                enabled = selectedProfile != null,
                colors = ButtonDefaults.buttonColors(
                    containerColor = MaterialTheme.colorScheme.primary
                )
            ) {
                Text(
                    stringResource(R.string.action_connect),
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold
                )
            }
        }

        Spacer(modifier = Modifier.height(32.dp))
    }
}

// ── Connecting: Status + Progress ────────────────────────────────────────────

@Composable
private fun ConnectingHome(
    profileName: String,
    deployProgress: NetFerryVpnService.DeployProgress? = null,
    onDisconnect: () -> Unit
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Spacer(modifier = Modifier.height(24.dp))

        StatusHeader(
            vpnState = NetFerryVpnService.VpnState.CONNECTING,
            profileName = profileName
        )

        Spacer(modifier = Modifier.height(24.dp))

        if (deployProgress != null && deployProgress.total > 0 && deployProgress.reason != "up-to-date") {
            // Deploy progress with percentage
            val fraction = deployProgress.sent.toFloat() / deployProgress.total.toFloat()
            val percent = (fraction * 100).toInt()

            Text(
                text = when (deployProgress.reason) {
                    "first-deploy" -> stringResource(R.string.deploy_first)
                    "update" -> stringResource(R.string.deploy_update)
                    else -> stringResource(R.string.deploy_uploading)
                },
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )

            Spacer(modifier = Modifier.height(8.dp))

            LinearProgressIndicator(
                progress = { fraction },
                modifier = Modifier
                    .fillMaxWidth()
                    .height(6.dp)
                    .clip(RoundedCornerShape(4.dp)),
                color = MaterialTheme.colorScheme.primary,
                trackColor = MaterialTheme.colorScheme.surfaceVariant,
            )

            Spacer(modifier = Modifier.height(4.dp))

            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Text(
                    text = "${formatBytes(deployProgress.sent)} / ${formatBytes(deployProgress.total)}",
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Text(
                    text = "$percent%",
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        } else {
            // Indeterminate progress (no deploy or already up-to-date)
            LinearProgressIndicator(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(4.dp)),
                color = MaterialTheme.colorScheme.primary,
                trackColor = MaterialTheme.colorScheme.surfaceVariant
            )
        }

        Spacer(modifier = Modifier.height(32.dp))

        OutlinedButton(
            onClick = onDisconnect,
            modifier = Modifier
                .fillMaxWidth()
                .height(48.dp),
            shape = RoundedCornerShape(8.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.error)
        ) {
            Text(
                stringResource(R.string.action_disconnect),
                color = MaterialTheme.colorScheme.error,
                fontWeight = FontWeight.SemiBold
            )
        }

        Spacer(modifier = Modifier.height(16.dp))
    }
}

// ── Connected: Stats + Chart + Logs ──────────────────────────────────────────

@Composable
private fun ConnectedHome(
    profileName: String,
    vpnState: NetFerryVpnService.VpnState,
    stats: TunnelStats,
    speedHistory: List<NetFerryVpnService.SpeedSample>,
    logMessages: List<String>,
    onDisconnect: () -> Unit
) {
    var logsExpanded by rememberSaveable { mutableStateOf(false) }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp),
        horizontalAlignment = Alignment.CenterHorizontally
    ) {
        Spacer(modifier = Modifier.height(12.dp))

        StatusHeader(vpnState = vpnState, profileName = profileName)

        Spacer(modifier = Modifier.height(20.dp))

        // Stats grid 2x2
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            StatCard(
                icon = Icons.Default.ArrowDownward,
                label = stringResource(R.string.connection_download),
                value = stats.downloadSpeed,
                subtitle = stringResource(R.string.connection_total_bytes, stats.totalDownloaded),
                modifier = Modifier.weight(1f)
            )
            StatCard(
                icon = Icons.Default.ArrowUpward,
                label = stringResource(R.string.connection_upload),
                value = stats.uploadSpeed,
                subtitle = stringResource(R.string.connection_total_bytes, stats.totalUploaded),
                modifier = Modifier.weight(1f)
            )
        }

        Spacer(modifier = Modifier.height(12.dp))

        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(12.dp)
        ) {
            StatCard(
                icon = Icons.Default.Hub,
                label = stringResource(R.string.connection_active_conns),
                value = stats.activeConnections.toString(),
                subtitle = stringResource(R.string.connection_total_bytes, stats.totalConnections.toString()),
                modifier = Modifier.weight(1f)
            )
            StatCard(
                icon = Icons.Default.Dns,
                label = stringResource(R.string.connection_dns_queries),
                value = stats.dnsQueries.toString(),
                subtitle = "",
                modifier = Modifier.weight(1f)
            )
        }

        Spacer(modifier = Modifier.height(16.dp))

        // Speed chart
        if (speedHistory.size >= 2) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(160.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .border(
                        width = 1.dp,
                        color = MaterialTheme.colorScheme.outline,
                        shape = RoundedCornerShape(12.dp)
                    )
                    .background(MaterialTheme.colorScheme.surface)
            ) {
                SpeedChart(
                    history = speedHistory,
                    modifier = Modifier
                        .fillMaxSize()
                        .padding(12.dp)
                )
            }
            Spacer(modifier = Modifier.height(16.dp))
        }

        // Collapsible logs section
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(8.dp))
                .clickable { logsExpanded = !logsExpanded }
                .padding(vertical = 8.dp, horizontal = 4.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Text(
                text = stringResource(R.string.home_logs).uppercase(),
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f)
            )
            Icon(
                if (logsExpanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(20.dp)
            )
        }

        AnimatedVisibility(
            visible = logsExpanded,
            enter = expandVertically(),
            exit = shrinkVertically()
        ) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(200.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .border(
                        width = 1.dp,
                        color = MaterialTheme.colorScheme.outline,
                        shape = RoundedCornerShape(12.dp)
                    )
                    .background(MaterialTheme.colorScheme.surface)
            ) {
                if (logMessages.isEmpty()) {
                    Box(
                        modifier = Modifier.fillMaxSize(),
                        contentAlignment = Alignment.Center
                    ) {
                        Text(
                            stringResource(R.string.connection_no_logs),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                    }
                } else {
                    val listState = rememberLazyListState()
                    LaunchedEffect(logMessages.size) {
                        if (logMessages.isNotEmpty()) {
                            listState.animateScrollToItem(logMessages.size - 1)
                        }
                    }
                    LazyColumn(
                        state = listState,
                        modifier = Modifier
                            .fillMaxSize()
                            .padding(10.dp)
                    ) {
                        items(logMessages) { msg ->
                            Text(
                                text = msg,
                                style = MaterialTheme.typography.bodySmall.copy(
                                    fontFamily = FontFamily.Monospace,
                                    fontSize = 11.sp,
                                    lineHeight = 16.sp
                                ),
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                }
            }
        }

        Spacer(modifier = Modifier.height(24.dp))

        // Disconnect button
        OutlinedButton(
            onClick = onDisconnect,
            modifier = Modifier
                .fillMaxWidth()
                .height(48.dp),
            shape = RoundedCornerShape(8.dp),
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.error)
        ) {
            Icon(
                Icons.Default.PowerSettingsNew,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.error,
                modifier = Modifier.size(20.dp)
            )
            Spacer(modifier = Modifier.width(8.dp))
            Text(
                stringResource(R.string.action_disconnect),
                color = MaterialTheme.colorScheme.error,
                fontWeight = FontWeight.SemiBold
            )
        }

        Spacer(modifier = Modifier.height(16.dp))
    }
}
