package com.netferry.app

import android.content.Context
import android.net.VpnService
import android.os.Bundle
import com.netferry.app.AppLog
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatDelegate
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.VpnKey
import androidx.compose.material.icons.automirrored.filled.List
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.core.os.LocaleListCompat
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.netferry.app.model.Profile
import com.netferry.app.service.NetFerryVpnService
import com.netferry.app.ui.HomeScreen
import com.netferry.app.ui.ProfileDetailScreen
import com.netferry.app.ui.ProfileListScreen
import com.netferry.app.ui.QRScannerScreen
import com.netferry.app.ui.SettingsScreen
import com.netferry.app.ui.theme.NetFerryTheme
import com.netferry.app.viewmodel.ConnectionViewModel
import com.netferry.app.viewmodel.ProfileViewModel
import mobile.Mobile

class MainActivity : AppCompatActivity() {

    private var pendingConnectProfile: Profile? = null

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        AppLog.d(TAG, "VPN permission result: resultCode=${result.resultCode}, RESULT_OK=$RESULT_OK, pendingProfile=${pendingConnectProfile?.name}")
        if (result.resultCode == RESULT_OK) {
            pendingConnectProfile?.let { profile ->
                AppLog.d(TAG, "VPN permission granted, starting VPN for '${profile.name}'")
                NetFerryVpnService.startVpn(this, profile)
            } ?: AppLog.w(TAG, "VPN permission granted but pendingConnectProfile is null!")
        } else {
            AppLog.w(TAG, "VPN permission denied or cancelled")
        }
        pendingConnectProfile = null
    }

    private fun getSettingsPrefs() =
        getSharedPreferences("netferry_settings", Context.MODE_PRIVATE)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        window.isNavigationBarContrastEnforced = false

        // Apply saved language on startup
        val savedLanguage = getSettingsPrefs().getString("language", "system") ?: "system"
        applyLanguage(savedLanguage)

        setContent {
            val settingsPrefs = getSettingsPrefs()
            var themeMode by remember {
                mutableStateOf(settingsPrefs.getString("theme_mode", "system") ?: "system")
            }
            var languageMode by remember {
                mutableStateOf(settingsPrefs.getString("language", "system") ?: "system")
            }

            val darkTheme = when (themeMode) {
                "light" -> false
                "dark" -> true
                else -> isSystemInDarkTheme()
            }

            NetFerryTheme(darkTheme = darkTheme) {
                val navController = rememberNavController()
                val profileViewModel: ProfileViewModel = viewModel()
                val connectionViewModel: ConnectionViewModel = viewModel()

                val profiles by profileViewModel.profiles.collectAsState()
                val vpnState by connectionViewModel.vpnState.collectAsState()
                val connectedProfileId by connectionViewModel.connectedProfileId.collectAsState()
                val stats by connectionViewModel.tunnelStats.collectAsState()
                val speedHistory by connectionViewModel.speedHistory.collectAsState()
                val logMessages by connectionViewModel.logMessages.collectAsState()
                val deployProgress by connectionViewModel.deployProgress.collectAsState()
                val lastError by connectionViewModel.lastError.collectAsState()

                // Bottom navigation tabs
                data class TabItem(val route: String, val titleResId: Int, val icon: ImageVector)
                val tabs = listOf(
                    TabItem("home", R.string.nav_home, Icons.Default.VpnKey),
                    TabItem("profiles", R.string.nav_profiles, Icons.AutoMirrored.Filled.List),
                    TabItem("settings", R.string.nav_settings, Icons.Default.Settings)
                )
                val tabRoutes = tabs.map { it.route }.toSet()

                val navBackStackEntry by navController.currentBackStackEntryAsState()
                val currentRoute = navBackStackEntry?.destination?.route
                val showBottomBar = currentRoute in tabRoutes

                Scaffold(
                    bottomBar = {
                        if (showBottomBar) {
                            NavigationBar(
                                containerColor = MaterialTheme.colorScheme.surface,
                                contentColor = MaterialTheme.colorScheme.onSurface
                            ) {
                                tabs.forEach { tab ->
                                    NavigationBarItem(
                                        icon = { Icon(tab.icon, contentDescription = stringResource(tab.titleResId)) },
                                        label = { Text(stringResource(tab.titleResId)) },
                                        selected = currentRoute == tab.route,
                                        onClick = {
                                            navController.navigate(tab.route) {
                                                popUpTo(navController.graph.findStartDestination().id) {
                                                    saveState = true
                                                }
                                                launchSingleTop = true
                                                restoreState = true
                                            }
                                        },
                                        colors = NavigationBarItemDefaults.colors(
                                            selectedIconColor = MaterialTheme.colorScheme.primary,
                                            selectedTextColor = MaterialTheme.colorScheme.primary,
                                            indicatorColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.1f),
                                            unselectedIconColor = MaterialTheme.colorScheme.onSurfaceVariant,
                                            unselectedTextColor = MaterialTheme.colorScheme.onSurfaceVariant
                                        )
                                    )
                                }
                            }
                        }
                    },
                    containerColor = MaterialTheme.colorScheme.surface
                ) { innerPadding ->
                    NavHost(
                        navController = navController,
                        startDestination = "home",
                        modifier = Modifier.padding(innerPadding)
                    ) {
                        // ── Tab: Home ──────────────────────────────────────
                        composable("home") {
                            HomeScreen(
                                profiles = profiles,
                                vpnState = vpnState,
                                connectedProfileId = connectedProfileId,
                                stats = stats,
                                speedHistory = speedHistory,
                                logMessages = logMessages,
                                deployProgress = deployProgress,
                                lastError = lastError,
                                onConnect = { profile -> requestVpnAndConnect(profile) },
                                onDisconnect = { connectionViewModel.disconnect() }
                            )
                        }

                        // ── Tab: Profiles ──────────────────────────────────
                        composable("profiles") {
                            ProfileListScreen(
                                profiles = profiles,
                                connectedProfileId = connectedProfileId,
                                vpnState = vpnState,
                                onProfileClick = { profile ->
                                    navController.navigate("profileDetail/${profile.id}")
                                },
                                onConnect = { profile -> requestVpnAndConnect(profile) },
                                onDisconnect = { connectionViewModel.disconnect() },
                                onDelete = { profile -> profileViewModel.deleteProfile(profile.id) },
                                onAddProfile = { navController.navigate("profileDetail/new") },
                                onSaveProfile = { profile -> profileViewModel.saveProfile(profile) },
                                onScanQR = { navController.navigate("qr_scanner") }
                            )
                        }

                        // ── Tab: Settings ──────────────────────────────────
                        composable("settings") {
                            SettingsScreen(
                                profiles = profiles,
                                autoConnectProfileId = profileViewModel.getAutoConnectProfileId(),
                                appVersion = BuildConfig.VERSION_NAME,
                                engineVersion = Mobile.getVersion(),
                                themeMode = themeMode,
                                languageMode = languageMode,
                                onAutoConnectChanged = { id ->
                                    profileViewModel.setAutoConnectProfileId(id)
                                },
                                onThemeModeChanged = { mode ->
                                    themeMode = mode
                                    settingsPrefs.edit().putString("theme_mode", mode).apply()
                                },
                                onLanguageModeChanged = { mode ->
                                    languageMode = mode
                                    settingsPrefs.edit().putString("language", mode).apply()
                                    applyLanguage(mode)
                                }
                            )
                        }

                        // ── Sub-screen: Profile Detail ─────────────────────
                        composable(
                            "profileDetail/{profileId}",
                            arguments = listOf(
                                navArgument("profileId") { type = NavType.StringType }
                            )
                        ) { backStackEntry ->
                            val profileId = backStackEntry.arguments?.getString("profileId") ?: return@composable
                            val isNew = profileId == "new"
                            val profile = if (isNew) Profile() else profileViewModel.getProfile(profileId) ?: return@composable

                            ProfileDetailScreen(
                                initialProfile = profile,
                                isNew = isNew,
                                onSave = { updatedProfile ->
                                    profileViewModel.saveProfile(updatedProfile)
                                    navController.popBackStack()
                                },
                                onDelete = if (isNew) null else {
                                    {
                                        profileViewModel.deleteProfile(profileId)
                                        navController.popBackStack()
                                    }
                                },
                                onBack = { navController.popBackStack() }
                            )
                        }

                        // ── Sub-screen: QR Scanner ─────────────────────────
                        composable("qr_scanner") {
                            QRScannerScreen(
                                onProfileImported = { profile ->
                                    profileViewModel.saveProfile(profile)
                                    navController.popBackStack()
                                },
                                onBack = { navController.popBackStack() }
                            )
                        }
                    }
                }
            }
        }
    }

    private fun applyLanguage(mode: String) {
        val localeList = when (mode) {
            "en" -> LocaleListCompat.forLanguageTags("en")
            "zh" -> LocaleListCompat.forLanguageTags("zh-CN")
            else -> LocaleListCompat.getEmptyLocaleList()
        }
        AppCompatDelegate.setApplicationLocales(localeList)
    }

    private fun requestVpnAndConnect(profile: Profile) {
        AppLog.d(TAG, "requestVpnAndConnect: profile='${profile.name}', vpnState=${NetFerryVpnService.vpnState.value}, connectedId=${NetFerryVpnService.connectedProfileId.value}")
        val prepareIntent = VpnService.prepare(this)
        if (prepareIntent != null) {
            AppLog.d(TAG, "VPN permission needed, launching system dialog")
            pendingConnectProfile = profile
            vpnPermissionLauncher.launch(prepareIntent)
        } else {
            AppLog.d(TAG, "VPN permission already granted, starting VPN directly")
            NetFerryVpnService.startVpn(this, profile)
        }
    }

    companion object {
        private const val TAG = "NetFerryConnect"
    }
}
