package com.netferry.app.ui

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.ArrowDownward
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.PowerSettingsNew
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.TabRowDefaults
import androidx.compose.material3.TabRowDefaults.tabIndicatorOffset
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.netferry.app.R
import com.netferry.app.model.TunnelStats
import com.netferry.app.service.NetFerryVpnService
import com.netferry.app.ui.theme.StatusGreen
import com.netferry.app.ui.theme.StatusOrange
import com.netferry.app.ui.theme.StatusRed

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConnectionScreen(
    profileName: String,
    vpnState: NetFerryVpnService.VpnState,
    stats: TunnelStats,
    speedHistory: List<NetFerryVpnService.SpeedSample>,
    logMessages: List<String>,
    onDisconnect: () -> Unit,
    onBack: () -> Unit
) {
    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        stringResource(R.string.connection_title),
                        style = MaterialTheme.typography.headlineMedium
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back)
                        )
                    }
                },
                windowInsets = WindowInsets(0, 0, 0, 0),
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.background,
                    titleContentColor = MaterialTheme.colorScheme.onBackground
                )
            )
        },
        containerColor = MaterialTheme.colorScheme.background
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(horizontal = 16.dp),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Spacer(modifier = Modifier.height(12.dp))

            // Status header
            StatusHeader(vpnState = vpnState, profileName = profileName)

            Spacer(modifier = Modifier.height(20.dp))

            // Stats grid: 2x2
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

            // Tabbed content: Speed | Stats | Logs
            val tabTitles = listOf(
                stringResource(R.string.connection_speed),
                stringResource(R.string.connection_stats),
                stringResource(R.string.connection_logs)
            )
            var selectedTab by remember { mutableIntStateOf(0) }

            TabRow(
                selectedTabIndex = selectedTab,
                containerColor = Color.Transparent,
                contentColor = MaterialTheme.colorScheme.primary,
                indicator = { tabPositions ->
                    if (selectedTab < tabPositions.size) {
                        TabRowDefaults.SecondaryIndicator(
                            modifier = Modifier.tabIndicatorOffset(tabPositions[selectedTab]),
                            color = MaterialTheme.colorScheme.primary
                        )
                    }
                },
                divider = {}
            ) {
                tabTitles.forEachIndexed { index, title ->
                    Tab(
                        selected = selectedTab == index,
                        onClick = { selectedTab = index },
                        text = {
                            Text(
                                title,
                                style = MaterialTheme.typography.labelLarge,
                                fontWeight = if (selectedTab == index) FontWeight.SemiBold else FontWeight.Normal
                            )
                        }
                    )
                }
            }

            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .weight(1f)
            ) {
                when (selectedTab) {
                    0 -> {
                        // Speed chart
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .padding(top = 12.dp)
                        ) {
                            if (speedHistory.size >= 2) {
                                Box(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .height(180.dp)
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
                            } else {
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
                            }
                        }
                    }
                    1 -> {
                        // Stats detail
                        Column(
                            modifier = Modifier
                                .fillMaxSize()
                                .padding(top = 12.dp),
                            verticalArrangement = Arrangement.spacedBy(8.dp)
                        ) {
                            StatsRow(stringResource(R.string.connection_download), stats.downloadSpeed)
                            StatsRow(stringResource(R.string.connection_upload), stats.uploadSpeed)
                            StatsRow(stringResource(R.string.connection_active_conns), stats.activeConnections.toString())
                            StatsRow(stringResource(R.string.connection_total_conns), stats.totalConnections.toString())
                        }
                    }
                    2 -> {
                        // Logs
                        Box(
                            modifier = Modifier
                                .fillMaxSize()
                                .padding(top = 12.dp)
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
                }
            }

            Spacer(modifier = Modifier.height(16.dp))

            // Disconnect button
            OutlinedButton(
                onClick = onDisconnect,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(48.dp),
                shape = RoundedCornerShape(8.dp),
                border = androidx.compose.foundation.BorderStroke(
                    1.dp,
                    MaterialTheme.colorScheme.error
                )
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
}

@Composable
internal fun StatusHeader(
    vpnState: NetFerryVpnService.VpnState,
    profileName: String
) {
    val statusColor = when (vpnState) {
        NetFerryVpnService.VpnState.CONNECTED -> StatusGreen
        NetFerryVpnService.VpnState.CONNECTING -> StatusOrange
        NetFerryVpnService.VpnState.ERROR -> StatusRed
        NetFerryVpnService.VpnState.DISCONNECTED -> MaterialTheme.colorScheme.onSurfaceVariant
    }

    val statusText = when (vpnState) {
        NetFerryVpnService.VpnState.CONNECTED -> stringResource(R.string.connection_connected)
        NetFerryVpnService.VpnState.CONNECTING -> stringResource(R.string.connection_connecting)
        NetFerryVpnService.VpnState.ERROR -> stringResource(R.string.connection_error)
        NetFerryVpnService.VpnState.DISCONNECTED -> stringResource(R.string.connection_disconnected)
    }

    // Pulse animation for connecting
    val infiniteTransition = rememberInfiniteTransition(label = "pulse")
    val pulseScale by infiniteTransition.animateFloat(
        initialValue = 1f,
        targetValue = if (vpnState == NetFerryVpnService.VpnState.CONNECTING) 1.2f else 1f,
        animationSpec = infiniteRepeatable(
            animation = tween(800, easing = LinearEasing),
            repeatMode = RepeatMode.Reverse
        ),
        label = "pulse_scale"
    )

    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .border(
                width = 1.dp,
                color = MaterialTheme.colorScheme.outline,
                shape = RoundedCornerShape(12.dp)
            )
            .background(MaterialTheme.colorScheme.surface)
            .padding(20.dp)
    ) {
        // Status dot with pulse
        Box(
            modifier = Modifier
                .size(16.dp)
                .scale(pulseScale)
                .clip(CircleShape)
                .background(statusColor)
        )

        Spacer(modifier = Modifier.width(14.dp))

        Column {
            Text(
                text = statusText,
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface
            )
            Spacer(modifier = Modifier.height(2.dp))
            Text(
                text = profileName,
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant
            )
        }
    }
}

@Composable
internal fun StatCard(
    icon: ImageVector,
    label: String,
    value: String,
    subtitle: String,
    modifier: Modifier = Modifier
) {
    Box(
        modifier = modifier
            .clip(RoundedCornerShape(12.dp))
            .border(
                width = 1.dp,
                color = MaterialTheme.colorScheme.outline,
                shape = RoundedCornerShape(12.dp)
            )
            .background(MaterialTheme.colorScheme.surface)
            .padding(14.dp)
    ) {
        Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    icon,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(14.dp)
                )
                Spacer(modifier = Modifier.width(6.dp))
                Text(
                    label,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            Spacer(modifier = Modifier.height(6.dp))
            Text(
                text = value,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurface
            )
            if (subtitle.isNotEmpty()) {
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}

@Composable
internal fun StatsRow(label: String, value: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .border(
                width = 1.dp,
                color = MaterialTheme.colorScheme.outline,
                shape = RoundedCornerShape(8.dp)
            )
            .background(MaterialTheme.colorScheme.surface)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Text(
            text = value,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onSurface
        )
    }
}

@Composable
internal fun SpeedChart(
    history: List<NetFerryVpnService.SpeedSample>,
    modifier: Modifier = Modifier
) {
    val rxColor = StatusGreen
    val txColor = MaterialTheme.colorScheme.primary
    val labelColor = MaterialTheme.colorScheme.onSurfaceVariant
    val gridColor = MaterialTheme.colorScheme.outline.copy(alpha = 0.2f)
    val rxLabel = stringResource(R.string.connection_download)
    val txLabel = stringResource(R.string.connection_upload)

    Canvas(modifier = modifier) {
        if (history.size < 2) return@Canvas

        val maxSpeed = history.maxOf { maxOf(it.rxBytesPerSec, it.txBytesPerSec) }
            .coerceAtLeast(1024)

        // Choose a nice round max for the Y axis.
        val yMax = niceYMax(maxSpeed)

        val labelWidth = 48.dp.toPx()
        val legendHeight = 20.dp.toPx()
        val chartLeft = labelWidth
        val chartWidth = size.width - chartLeft
        val chartTop = legendHeight
        val chartHeight = size.height - chartTop
        val stepX = chartWidth / (history.size - 1).coerceAtLeast(1)

        // Grid lines + Y-axis labels (3 lines: 0, mid, max)
        val textPaint = android.graphics.Paint().apply {
            color = labelColor.toArgb()
            textSize = 10.sp.toPx()
            isAntiAlias = true
        }
        for (i in 0..2) {
            val frac = i / 2f
            val yPos = chartTop + chartHeight * (1f - frac)
            val value = (yMax * frac).toLong()
            // Grid line
            drawLine(gridColor, androidx.compose.ui.geometry.Offset(chartLeft, yPos), androidx.compose.ui.geometry.Offset(size.width, yPos), strokeWidth = 1.dp.toPx())
            // Label
            val label = formatSpeedShort(value)
            drawContext.canvas.nativeCanvas.drawText(label, 0f, yPos + 4.sp.toPx(), textPaint)
        }

        // Legend (top-right): colored line + text
        val legendY = 4.dp.toPx()
        val dotR = 4.dp.toPx()
        val legendTextPaint = android.graphics.Paint().apply {
            color = labelColor.toArgb()
            textSize = 11.sp.toPx()
            isAntiAlias = true
        }
        // Upload legend
        val txLabelWidth = legendTextPaint.measureText(txLabel)
        val txLegendX = size.width - txLabelWidth
        drawContext.canvas.nativeCanvas.drawText(txLabel, txLegendX, legendY + 11.sp.toPx(), legendTextPaint)
        drawCircle(txColor, dotR, androidx.compose.ui.geometry.Offset(txLegendX - dotR - 4.dp.toPx(), legendY + 7.dp.toPx()))
        // Download legend
        val rxLabelWidth = legendTextPaint.measureText(rxLabel)
        val rxLegendX = txLegendX - txLabelWidth - 20.dp.toPx()
        drawContext.canvas.nativeCanvas.drawText(rxLabel, rxLegendX, legendY + 11.sp.toPx(), legendTextPaint)
        drawCircle(rxColor, dotR, androidx.compose.ui.geometry.Offset(rxLegendX - dotR - 4.dp.toPx(), legendY + 7.dp.toPx()))

        // Download line
        val rxPath = Path()
        history.forEachIndexed { index, sample ->
            val x = chartLeft + index * stepX
            val y = chartTop + chartHeight - (sample.rxBytesPerSec.toFloat() / yMax * chartHeight)
            if (index == 0) rxPath.moveTo(x, y) else rxPath.lineTo(x, y)
        }
        drawPath(rxPath, rxColor, style = Stroke(width = 2.dp.toPx()))

        // Upload line
        val txPath = Path()
        history.forEachIndexed { index, sample ->
            val x = chartLeft + index * stepX
            val y = chartTop + chartHeight - (sample.txBytesPerSec.toFloat() / yMax * chartHeight)
            if (index == 0) txPath.moveTo(x, y) else txPath.lineTo(x, y)
        }
        drawPath(txPath, txColor, style = Stroke(width = 2.dp.toPx()))
    }
}

/** Pick a nice round ceiling for the Y axis. */
private fun niceYMax(maxBytes: Long): Float {
    if (maxBytes <= 0) return 1024f
    // Round up to a nice number (1, 2, 5 × power of 1024)
    val units = longArrayOf(
        1024, 2048, 5120, 10240,                          // KB range
        102_400, 512_000, 1_048_576,                       // 100KB..1MB
        2_097_152, 5_242_880, 10_485_760,                  // 2..10MB
        52_428_800, 104_857_600, 524_288_000, 1_073_741_824 // 50MB..1GB
    )
    for (u in units) {
        if (maxBytes <= u) return u.toFloat()
    }
    return (maxBytes * 1.2f)
}

/** Format bytes/sec as a short label for Y-axis (e.g. "1 KB", "5 MB"). */
private fun formatSpeedShort(bytesPerSec: Long): String {
    return when {
        bytesPerSec < 1024 -> "${bytesPerSec}B"
        bytesPerSec < 1_048_576 -> "${bytesPerSec / 1024}K"
        bytesPerSec < 1_073_741_824 -> "${bytesPerSec / 1_048_576}M"
        else -> "${bytesPerSec / 1_073_741_824}G"
    }
}
