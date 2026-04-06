package com.netferry.app.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.netferry.app.R
import com.netferry.app.model.JumpHost
import com.netferry.app.model.Profile

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProfileDetailScreen(
    initialProfile: Profile,
    isNew: Boolean,
    onSave: (Profile) -> Unit,
    onDelete: (() -> Unit)?,
    onBack: () -> Unit
) {
    var name by remember { mutableStateOf(initialProfile.name) }
    var remote by remember { mutableStateOf(initialProfile.remote) }
    var identityKey by remember { mutableStateOf(initialProfile.identityKey) }
    var jumpHostEntries by remember {
        mutableStateOf(
            initialProfile.jumpHosts.ifEmpty { emptyList() }
        )
    }
    var subnets by remember { mutableStateOf(initialProfile.subnets.joinToString("\n")) }
    var excludeSubnets by remember { mutableStateOf(initialProfile.excludeSubnets.joinToString("\n")) }
    var dns by remember { mutableStateOf(initialProfile.dns) }
    var dnsTarget by remember { mutableStateOf(initialProfile.dnsTarget) }
    var autoNets by remember { mutableStateOf(initialProfile.autoNets) }
    var autoExcludeLan by remember { mutableStateOf(initialProfile.autoExcludeLan) }
    var enableUdp by remember { mutableStateOf(initialProfile.enableUdp) }
    var blockUdp by remember { mutableStateOf(initialProfile.blockUdp) }
    var poolSize by remember { mutableIntStateOf(initialProfile.poolSize) }
    var splitConn by remember { mutableStateOf(initialProfile.splitConn) }
    var tcpBalanceMode by remember { mutableStateOf(initialProfile.tcpBalanceMode) }
    var latencyBufferSize by remember { mutableIntStateOf(initialProfile.latencyBufferSize) }
    var disableIpv6 by remember { mutableStateOf(initialProfile.disableIpv6) }
    var extraSshOptions by remember { mutableStateOf(initialProfile.extraSshOptions) }
    var notes by remember { mutableStateOf(initialProfile.notes) }
    var mtu by remember { mutableIntStateOf(initialProfile.mtu) }
    var advancedExpanded by remember { mutableStateOf(false) }
    var showDeleteDialog by remember { mutableStateOf(false) }

    var nameError by remember { mutableStateOf<String?>(null) }
    var remoteError by remember { mutableStateOf<String?>(null) }
    var keyError by remember { mutableStateOf<String?>(null) }

    // Pull string resources outside of lambda for validation
    val errorNameRequired = stringResource(R.string.error_name_required)
    val errorRemoteRequired = stringResource(R.string.error_remote_required)
    val errorKeyRequired = stringResource(R.string.error_key_required)

    fun validate(): Boolean {
        var valid = true
        if (name.isBlank()) {
            nameError = errorNameRequired
            valid = false
        } else {
            nameError = null
        }
        if (remote.isBlank()) {
            remoteError = errorRemoteRequired
            valid = false
        } else {
            remoteError = null
        }
        if (identityKey.isBlank()) {
            keyError = errorKeyRequired
            valid = false
        } else {
            keyError = null
        }
        return valid
    }

    if (showDeleteDialog && onDelete != null) {
        AlertDialog(
            onDismissRequest = { showDeleteDialog = false },
            title = { Text(stringResource(R.string.profile_delete_title)) },
            text = { Text(stringResource(R.string.profile_delete_message, name)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        showDeleteDialog = false
                        onDelete()
                    }
                ) {
                    Text(
                        stringResource(R.string.action_delete),
                        color = MaterialTheme.colorScheme.error
                    )
                }
            },
            dismissButton = {
                TextButton(onClick = { showDeleteDialog = false }) {
                    Text(stringResource(R.string.action_cancel))
                }
            }
        )
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        if (isNew) stringResource(R.string.profile_new)
                        else stringResource(R.string.profile_edit),
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
                actions = {
                    if (!isNew && onDelete != null) {
                        IconButton(onClick = { showDeleteDialog = true }) {
                            Icon(
                                Icons.Default.Delete,
                                contentDescription = stringResource(R.string.action_delete),
                                tint = MaterialTheme.colorScheme.error
                            )
                        }
                    }
                },
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
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp)
        ) {
            // ── CONNECTION section ─────────────────────────────────────────
            SectionHeader(stringResource(R.string.profile_section_connection))

            SectionCard {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it; nameError = null },
                    label = { Text(stringResource(R.string.profile_name)) },
                    placeholder = { Text(stringResource(R.string.profile_name_placeholder)) },
                    isError = nameError != null,
                    supportingText = nameError?.let { { Text(it) } },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )

                Spacer(modifier = Modifier.height(12.dp))

                OutlinedTextField(
                    value = remote,
                    onValueChange = { remote = it; remoteError = null },
                    label = { Text(stringResource(R.string.profile_remote)) },
                    placeholder = { Text(stringResource(R.string.profile_remote_hint)) },
                    isError = remoteError != null,
                    supportingText = remoteError?.let { { Text(it) } },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )

                Spacer(modifier = Modifier.height(12.dp))

                OutlinedTextField(
                    value = identityKey,
                    onValueChange = { identityKey = it; keyError = null },
                    label = { Text(stringResource(R.string.profile_identity_key)) },
                    isError = keyError != null,
                    supportingText = keyError?.let { { Text(it) } },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(140.dp),
                    textStyle = MaterialTheme.typography.bodySmall.copy(
                        fontFamily = FontFamily.Monospace
                    ),
                    maxLines = 20
                )

                if (jumpHostEntries.isNotEmpty()) {
                    Spacer(modifier = Modifier.height(12.dp))

                    Text(
                        stringResource(R.string.profile_jump_hosts),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )

                    Spacer(modifier = Modifier.height(8.dp))

                    jumpHostEntries.forEachIndexed { index, jh ->
                        var jhExpanded by remember { mutableStateOf(false) }

                        Box(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clip(RoundedCornerShape(8.dp))
                                .border(
                                    width = 1.dp,
                                    color = MaterialTheme.colorScheme.outline,
                                    shape = RoundedCornerShape(8.dp)
                                )
                                .padding(12.dp)
                        ) {
                            Column {
                                Row(
                                    modifier = Modifier.fillMaxWidth(),
                                    verticalAlignment = Alignment.CenterVertically
                                ) {
                                    OutlinedTextField(
                                        value = jh.remote,
                                        onValueChange = { newRemote ->
                                            jumpHostEntries = jumpHostEntries.toMutableList().also {
                                                it[index] = it[index].copy(remote = newRemote)
                                            }
                                        },
                                        label = { Text(stringResource(R.string.profile_jump_host_remote)) },
                                        placeholder = { Text(stringResource(R.string.profile_jump_host_remote_hint)) },
                                        singleLine = true,
                                        modifier = Modifier.weight(1f)
                                    )
                                    Spacer(modifier = Modifier.width(4.dp))
                                    IconButton(
                                        onClick = {
                                            jumpHostEntries = jumpHostEntries.toMutableList().also {
                                                it.removeAt(index)
                                            }
                                        }
                                    ) {
                                        Icon(
                                            Icons.Default.Close,
                                            contentDescription = stringResource(R.string.profile_jump_host_remove),
                                            tint = MaterialTheme.colorScheme.error
                                        )
                                    }
                                }

                                Row(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .clip(RoundedCornerShape(4.dp))
                                        .clickable { jhExpanded = !jhExpanded }
                                        .padding(vertical = 4.dp),
                                    verticalAlignment = Alignment.CenterVertically
                                ) {
                                    Text(
                                        stringResource(R.string.profile_jump_host_key),
                                        style = MaterialTheme.typography.labelMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier.weight(1f)
                                    )
                                    Icon(
                                        if (jhExpanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore,
                                        contentDescription = null,
                                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier.size(18.dp)
                                    )
                                }

                                AnimatedVisibility(
                                    visible = jhExpanded,
                                    enter = expandVertically(),
                                    exit = shrinkVertically()
                                ) {
                                    OutlinedTextField(
                                        value = jh.identityKey,
                                        onValueChange = { newKey ->
                                            jumpHostEntries = jumpHostEntries.toMutableList().also {
                                                it[index] = it[index].copy(identityKey = newKey)
                                            }
                                        },
                                        label = { Text(stringResource(R.string.profile_jump_host_key)) },
                                        modifier = Modifier
                                            .fillMaxWidth()
                                            .height(120.dp),
                                        textStyle = MaterialTheme.typography.bodySmall.copy(
                                            fontFamily = FontFamily.Monospace
                                        ),
                                        maxLines = 15
                                    )
                                }
                            }
                        }

                        if (index < jumpHostEntries.lastIndex) {
                            Spacer(modifier = Modifier.height(8.dp))
                        }
                    }
                }

                Spacer(modifier = Modifier.height(8.dp))

                OutlinedButton(
                    onClick = {
                        jumpHostEntries = jumpHostEntries + JumpHost()
                    },
                    modifier = Modifier.fillMaxWidth(),
                    shape = RoundedCornerShape(8.dp),
                    border = BorderStroke(1.dp, MaterialTheme.colorScheme.outline)
                ) {
                    Icon(
                        Icons.Default.Add,
                        contentDescription = null,
                        modifier = Modifier.size(18.dp),
                        tint = MaterialTheme.colorScheme.primary
                    )
                    Spacer(modifier = Modifier.width(8.dp))
                    Text(
                        stringResource(R.string.profile_jump_host_add),
                        color = MaterialTheme.colorScheme.primary
                    )
                }
            }

            Spacer(modifier = Modifier.height(16.dp))

            // ── ROUTING section ────────────────────────────────────────────
            SectionHeader(stringResource(R.string.profile_section_routing))

            SectionCard {
                OutlinedTextField(
                    value = subnets,
                    onValueChange = { subnets = it },
                    label = { Text(stringResource(R.string.profile_subnets)) },
                    placeholder = { Text(stringResource(R.string.profile_subnets_hint)) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(80.dp),
                    maxLines = 5
                )

                Spacer(modifier = Modifier.height(12.dp))

                OutlinedTextField(
                    value = excludeSubnets,
                    onValueChange = { excludeSubnets = it },
                    label = { Text(stringResource(R.string.profile_exclude_subnets)) },
                    placeholder = { Text(stringResource(R.string.profile_subnets_hint)) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(80.dp),
                    maxLines = 5
                )

                Spacer(modifier = Modifier.height(8.dp))

                ToggleRow(
                    title = stringResource(R.string.profile_auto_nets),
                    description = stringResource(R.string.profile_auto_nets_desc),
                    checked = autoNets,
                    onCheckedChange = { autoNets = it }
                )

                ToggleRow(
                    title = stringResource(R.string.profile_auto_exclude_lan),
                    description = stringResource(R.string.profile_auto_exclude_lan_desc),
                    checked = autoExcludeLan,
                    onCheckedChange = { autoExcludeLan = it }
                )
            }

            Spacer(modifier = Modifier.height(16.dp))

            // ── DNS section ────────────────────────────────────────────────
            SectionHeader(stringResource(R.string.profile_section_dns))

            SectionCard {
                Text(
                    stringResource(R.string.profile_dns_mode),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                Spacer(modifier = Modifier.height(8.dp))
                SegmentedButtons(
                    options = listOf(
                        "off" to stringResource(R.string.profile_dns_off),
                        "all" to stringResource(R.string.profile_dns_all),
                        "specific" to stringResource(R.string.profile_dns_specific)
                    ),
                    selected = dns,
                    onSelected = { dns = it }
                )

                AnimatedVisibility(visible = dns != "off") {
                    Column {
                        Spacer(modifier = Modifier.height(12.dp))
                        OutlinedTextField(
                            value = dnsTarget,
                            onValueChange = { dnsTarget = it },
                            label = { Text(stringResource(R.string.profile_dns_target)) },
                            placeholder = { Text(stringResource(R.string.profile_dns_target_hint)) },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth()
                        )
                    }
                }
            }

            Spacer(modifier = Modifier.height(16.dp))

            // ── ADVANCED section (collapsed by default) ────────────────────
            CollapsibleSection(
                title = stringResource(R.string.profile_section_advanced),
                expanded = advancedExpanded,
                onToggle = { advancedExpanded = !advancedExpanded }
            ) {
                SectionCard {
                    OutlinedTextField(
                        value = poolSize.toString(),
                        onValueChange = { poolSize = it.toIntOrNull() ?: poolSize },
                        label = { Text(stringResource(R.string.profile_pool_size)) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )

                    Spacer(modifier = Modifier.height(8.dp))

                    ToggleRow(
                        title = stringResource(R.string.profile_split_conn),
                        description = stringResource(R.string.profile_split_conn_desc),
                        checked = splitConn,
                        onCheckedChange = { splitConn = it }
                    )

                    Spacer(modifier = Modifier.height(4.dp))

                    Text(
                        stringResource(R.string.profile_tcp_balance),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Spacer(modifier = Modifier.height(8.dp))
                    SegmentedButtons(
                        options = listOf(
                            "least-loaded" to stringResource(R.string.profile_tcp_least_loaded),
                            "round-robin" to stringResource(R.string.profile_tcp_round_robin)
                        ),
                        selected = tcpBalanceMode,
                        onSelected = { tcpBalanceMode = it }
                    )

                    Spacer(modifier = Modifier.height(8.dp))

                    ToggleRow(
                        title = stringResource(R.string.profile_enable_udp),
                        checked = enableUdp,
                        onCheckedChange = { enableUdp = it }
                    )

                    ToggleRow(
                        title = stringResource(R.string.profile_block_udp),
                        description = stringResource(R.string.profile_block_udp_desc),
                        checked = blockUdp,
                        onCheckedChange = { blockUdp = it }
                    )

                    ToggleRow(
                        title = stringResource(R.string.profile_disable_ipv6),
                        checked = disableIpv6,
                        onCheckedChange = { disableIpv6 = it }
                    )

                    Spacer(modifier = Modifier.height(4.dp))

                    OutlinedTextField(
                        value = mtu.toString(),
                        onValueChange = { mtu = it.toIntOrNull() ?: mtu },
                        label = { Text(stringResource(R.string.profile_mtu)) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )

                    Spacer(modifier = Modifier.height(12.dp))

                    OutlinedTextField(
                        value = latencyBufferSize.toString(),
                        onValueChange = { latencyBufferSize = it.toIntOrNull() ?: latencyBufferSize },
                        label = { Text(stringResource(R.string.profile_latency_buffer)) },
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )

                    Spacer(modifier = Modifier.height(12.dp))

                    OutlinedTextField(
                        value = extraSshOptions,
                        onValueChange = { extraSshOptions = it },
                        label = { Text(stringResource(R.string.profile_extra_ssh)) },
                        placeholder = { Text(stringResource(R.string.profile_extra_ssh_hint)) },
                        singleLine = true,
                        modifier = Modifier.fillMaxWidth()
                    )
                }
            }

            Spacer(modifier = Modifier.height(16.dp))

            // ── NOTES section ──────────────────────────────────────────────
            SectionHeader(stringResource(R.string.profile_notes))

            SectionCard {
                OutlinedTextField(
                    value = notes,
                    onValueChange = { notes = it },
                    label = { Text(stringResource(R.string.profile_notes)) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(100.dp),
                    maxLines = 5
                )
            }

            Spacer(modifier = Modifier.height(24.dp))

            // ── Save button ────────────────────────────────────────────────
            Button(
                onClick = {
                    if (validate()) {
                        val profile = initialProfile.copy(
                            name = name.trim(),
                            remote = remote.trim(),
                            identityKey = identityKey.trim(),
                            jumpHosts = jumpHostEntries.filter { it.remote.isNotBlank() },
                            subnets = subnets.lines().map { it.trim() }.filter { it.isNotEmpty() }.ifEmpty { listOf("0.0.0.0/0") },
                            excludeSubnets = excludeSubnets.lines().map { it.trim() }.filter { it.isNotEmpty() },
                            autoNets = autoNets,
                            autoExcludeLan = autoExcludeLan,
                            dns = dns,
                            dnsTarget = dnsTarget.trim(),
                            enableUdp = enableUdp,
                            blockUdp = blockUdp,
                            poolSize = poolSize.coerceIn(1, 10),
                            splitConn = splitConn,
                            tcpBalanceMode = tcpBalanceMode,
                            latencyBufferSize = latencyBufferSize,
                            disableIpv6 = disableIpv6,
                            extraSshOptions = extraSshOptions.trim(),
                            notes = notes.trim(),
                            mtu = mtu.coerceIn(576, 9000)
                        )
                        onSave(profile)
                    }
                },
                modifier = Modifier
                    .fillMaxWidth()
                    .height(48.dp),
                shape = RoundedCornerShape(8.dp),
                colors = ButtonDefaults.buttonColors(
                    containerColor = MaterialTheme.colorScheme.primary
                )
            ) {
                Text(
                    stringResource(R.string.action_save),
                    fontWeight = FontWeight.SemiBold
                )
            }

            Spacer(modifier = Modifier.height(32.dp))
        }
    }
}

// ── Reusable composables ─────────────────────────────────────────────────────

@Composable
private fun SectionHeader(title: String) {
    Text(
        text = title.uppercase(),
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        letterSpacing = MaterialTheme.typography.labelMedium.letterSpacing,
        modifier = Modifier.padding(bottom = 8.dp, start = 4.dp)
    )
}

@Composable
private fun SectionCard(content: @Composable () -> Unit) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .border(
                width = 1.dp,
                color = MaterialTheme.colorScheme.outline,
                shape = RoundedCornerShape(12.dp)
            )
            .background(MaterialTheme.colorScheme.surface)
            .padding(16.dp)
    ) {
        Column { content() }
    }
}

@Composable
private fun ToggleRow(
    title: String,
    description: String? = null,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 8.dp),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurface
            )
            if (description != null) {
                Text(
                    text = description,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
        Spacer(modifier = Modifier.width(12.dp))
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            colors = SwitchDefaults.colors(
                checkedTrackColor = MaterialTheme.colorScheme.primary,
                checkedThumbColor = MaterialTheme.colorScheme.onPrimary
            )
        )
    }
}

@Composable
private fun SegmentedButtons(
    options: List<Pair<String, String>>,
    selected: String,
    onSelected: (String) -> Unit
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(0.dp)
    ) {
        options.forEachIndexed { index, (value, label) ->
            val isSelected = selected == value
            val shape = when (index) {
                0 -> RoundedCornerShape(topStart = 8.dp, bottomStart = 8.dp)
                options.lastIndex -> RoundedCornerShape(topEnd = 8.dp, bottomEnd = 8.dp)
                else -> RoundedCornerShape(0.dp)
            }
            Box(
                modifier = Modifier
                    .weight(1f)
                    .clip(shape)
                    .border(
                        width = 1.dp,
                        color = if (isSelected) MaterialTheme.colorScheme.primary
                        else MaterialTheme.colorScheme.outline,
                        shape = shape
                    )
                    .background(
                        if (isSelected) MaterialTheme.colorScheme.primary.copy(alpha = 0.1f)
                        else MaterialTheme.colorScheme.surface
                    )
                    .clickable { onSelected(value) }
                    .padding(vertical = 10.dp),
                contentAlignment = Alignment.Center
            ) {
                Text(
                    text = label,
                    style = MaterialTheme.typography.labelLarge,
                    color = if (isSelected) MaterialTheme.colorScheme.primary
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                    fontWeight = if (isSelected) FontWeight.SemiBold else FontWeight.Normal
                )
            }
        }
    }
}

@Composable
private fun CollapsibleSection(
    title: String,
    expanded: Boolean,
    onToggle: () -> Unit,
    content: @Composable () -> Unit
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .clickable(onClick = onToggle)
            .padding(vertical = 4.dp, horizontal = 4.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(
            text = title.uppercase(),
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f)
        )
        Icon(
            if (expanded) Icons.Default.ExpandLess else Icons.Default.ExpandMore,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(20.dp)
        )
    }
    Spacer(modifier = Modifier.height(8.dp))
    AnimatedVisibility(
        visible = expanded,
        enter = expandVertically(),
        exit = shrinkVertically()
    ) {
        content()
    }
}

