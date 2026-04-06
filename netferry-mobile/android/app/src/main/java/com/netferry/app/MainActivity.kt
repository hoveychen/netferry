package com.netferry.app

import android.content.Context
import android.net.VpnService
import android.os.Bundle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatDelegate
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.core.os.LocaleListCompat
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.netferry.app.model.Profile
import com.netferry.app.service.NetFerryVpnService
import com.netferry.app.ui.ConnectionScreen
import com.netferry.app.ui.ProfileDetailScreen
import com.netferry.app.ui.ProfileListScreen
import com.netferry.app.ui.QRScannerScreen
import com.netferry.app.ui.SettingsScreen
import com.netferry.app.ui.theme.NetFerryTheme
import com.netferry.app.viewmodel.ConnectionViewModel
import com.netferry.app.viewmodel.ProfileViewModel

class MainActivity : AppCompatActivity() {

    private var pendingConnectProfile: Profile? = null

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            pendingConnectProfile?.let { profile ->
                NetFerryVpnService.startVpn(this, profile)
            }
        }
        pendingConnectProfile = null
    }

    private fun getSettingsPrefs() =
        getSharedPreferences("netferry_settings", Context.MODE_PRIVATE)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()

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

                NavHost(
                    navController = navController,
                    startDestination = "profileList"
                ) {
                    composable("profileList") {
                        ProfileListScreen(
                            profiles = profiles,
                            connectedProfileId = connectedProfileId,
                            vpnState = vpnState,
                            onProfileClick = { profile ->
                                navController.navigate("profileDetail/${profile.id}")
                            },
                            onConnect = { profile ->
                                requestVpnAndConnect(profile)
                            },
                            onDisconnect = {
                                connectionViewModel.disconnect()
                            },
                            onDelete = { profile ->
                                profileViewModel.deleteProfile(profile.id)
                            },
                            onAddProfile = {
                                navController.navigate("profileDetail/new")
                            },
                            onSaveProfile = { profile ->
                                profileViewModel.saveProfile(profile)
                            },
                            onScanQR = {
                                navController.navigate("qr_scanner")
                            },
                            onSettings = {
                                navController.navigate("settings")
                            },
                            onConnectionScreenClick = {
                                navController.navigate("connection")
                            }
                        )
                    }

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
                            onBack = {
                                navController.popBackStack()
                            }
                        )
                    }

                    composable("connection") {
                        val connProfileName = connectedProfileId?.let { id ->
                            profiles.find { it.id == id }?.name
                        } ?: "Unknown"

                        ConnectionScreen(
                            profileName = connProfileName,
                            vpnState = vpnState,
                            stats = stats,
                            speedHistory = speedHistory,
                            logMessages = logMessages,
                            onDisconnect = {
                                connectionViewModel.disconnect()
                                navController.popBackStack()
                            },
                            onBack = {
                                navController.popBackStack()
                            }
                        )
                    }

                    composable("settings") {
                        val autoConnectId = profileViewModel.getAutoConnectProfileId()

                        SettingsScreen(
                            profiles = profiles,
                            autoConnectProfileId = autoConnectId,
                            appVersion = BuildConfig.VERSION_NAME,
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
                            },
                            onBack = {
                                navController.popBackStack()
                            }
                        )
                    }

                    composable("qr_scanner") {
                        QRScannerScreen(
                            onProfileImported = { profile ->
                                profileViewModel.saveProfile(profile)
                                navController.popBackStack()
                            },
                            onBack = {
                                navController.popBackStack()
                            }
                        )
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
        val prepareIntent = VpnService.prepare(this)
        if (prepareIntent != null) {
            pendingConnectProfile = profile
            vpnPermissionLauncher.launch(prepareIntent)
        } else {
            NetFerryVpnService.startVpn(this, profile)
        }
    }
}
