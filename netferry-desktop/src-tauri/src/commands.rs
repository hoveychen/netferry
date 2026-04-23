use crate::models::{ConnectionStatus, GlobalSettings, Profile, ProfileGroup, SshHostEntry};
use crate::{crypto, groups, menu, priorities, profiles, settings, sidecar, ssh_config, tray};
use std::collections::HashMap;
use std::path::PathBuf;
use tauri::{AppHandle, Manager, State};

#[tauri::command]
pub fn get_stats_url(state: State<'_, sidecar::AppState>) -> Option<String> {
    sidecar::get_stats_url(state)
}

#[tauri::command]
pub fn list_profiles(app: AppHandle) -> Result<Vec<Profile>, String> {
    profiles::load_profiles(&app)
}

#[tauri::command]
pub fn save_profile(app: AppHandle, profile: Profile) -> Result<Vec<Profile>, String> {
    let result = profiles::upsert_profile(&app, profile)?;
    tray::rebuild_tray_menu(&app);
    menu::rebuild_app_menu(&app);
    Ok(result)
}

#[tauri::command]
pub fn delete_profile(app: AppHandle, profile_id: String) -> Result<Vec<Profile>, String> {
    let result = profiles::remove_profile(&app, &profile_id)?;
    tray::rebuild_tray_menu(&app);
    menu::rebuild_app_menu(&app);
    Ok(result)
}

#[tauri::command]
pub fn import_ssh_hosts(app: AppHandle) -> Result<Vec<SshHostEntry>, String> {
    let home = app
        .path()
        .home_dir()
        .map_err(|e| format!("Failed to resolve home directory: {e}"))?;
    ssh_config::parse_default_ssh_config(&home)
}

#[tauri::command]
pub fn get_default_identity_file(app: AppHandle) -> Option<String> {
    let home = app.path().home_dir().ok()?;
    ssh_config::get_default_identity_file(&home)
}

#[tauri::command]
pub fn connect_profile(
    app: AppHandle,
    state: State<'_, sidecar::AppState>,
    profile: Profile,
    group: Option<ProfileGroup>,
    children: Option<Vec<Profile>>,
) -> Result<ConnectionStatus, String> {
    // Group mode requires both `group` and a non-empty `children` list — the
    // children carry inline PEM keys that the temp group.json embeds. Solo
    // mode passes `null` for both and keeps the legacy single-tunnel path.
    let group_spec = match (group, children) {
        (Some(g), Some(c)) if !c.is_empty() => Some((g, c)),
        _ => None,
    };
    sidecar::connect(app, state, profile, group_spec)
}

#[tauri::command]
pub fn disconnect_profile(
    app: AppHandle,
    state: State<'_, sidecar::AppState>,
) -> Result<ConnectionStatus, String> {
    sidecar::disconnect(app, state)
}

#[tauri::command]
pub fn get_connection_status(
    state: State<'_, sidecar::AppState>,
) -> Result<ConnectionStatus, String> {
    sidecar::current_status(state)
}

#[tauri::command]
pub fn get_global_settings(app: AppHandle) -> Result<GlobalSettings, String> {
    settings::load_settings(&app)
}

#[tauri::command]
pub fn save_global_settings(app: AppHandle, settings: GlobalSettings) -> Result<(), String> {
    crate::settings::save_settings(&app, &settings)
}

#[tauri::command]
pub fn get_priorities(app: AppHandle) -> Result<HashMap<String, i32>, String> {
    priorities::load_priorities(&app)
}

#[tauri::command]
pub fn save_priorities(
    app: AppHandle,
    priorities: HashMap<String, i32>,
) -> Result<(), String> {
    priorities::save_priorities(&app, &priorities)
}

#[tauri::command]
pub fn get_routes(app: AppHandle) -> Result<HashMap<String, String>, String> {
    priorities::load_routes(&app)
}

#[tauri::command]
pub fn save_routes(
    app: AppHandle,
    routes: HashMap<String, String>,
) -> Result<(), String> {
    priorities::save_routes(&app, &routes)
}

// ── Profile groups (P1: data-layer only; runtime still uses flat profile list) ──

#[tauri::command]
pub fn list_groups(app: AppHandle) -> Result<Vec<ProfileGroup>, String> {
    groups::list_groups(&app)
}

#[tauri::command]
pub fn get_group(app: AppHandle, group_id: String) -> Result<Option<ProfileGroup>, String> {
    groups::load_group(&app, &group_id)
}

#[tauri::command]
pub fn save_group(app: AppHandle, group: ProfileGroup) -> Result<(), String> {
    groups::save_group(&app, &group)
}

#[tauri::command]
pub fn delete_group(app: AppHandle, group_id: String) -> Result<(), String> {
    groups::delete_group(&app, &group_id)
}

#[tauri::command]
pub fn add_profile_to_group(
    app: AppHandle,
    group_id: String,
    profile_id: String,
) -> Result<ProfileGroup, String> {
    let mut group = groups::load_group(&app, &group_id)?
        .ok_or_else(|| format!("Group not found: {group_id}"))?;
    if !group.children_ids.iter().any(|id| id == &profile_id) {
        group.children_ids.push(profile_id);
        groups::save_group(&app, &group)?;
    }
    Ok(group)
}

#[tauri::command]
pub fn remove_profile_from_group(
    app: AppHandle,
    group_id: String,
    profile_id: String,
) -> Result<ProfileGroup, String> {
    let mut group = groups::load_group(&app, &group_id)?
        .ok_or_else(|| format!("Group not found: {group_id}"))?;
    let before = group.children_ids.len();
    group.children_ids.retain(|id| id != &profile_id);
    if group.children_ids.len() != before {
        groups::save_group(&app, &group)?;
    }
    Ok(group)
}

#[tauri::command]
pub fn list_method_features() -> Result<HashMap<String, Vec<String>>, String> {
    sidecar::query_method_features()
}

#[tauri::command]
pub fn update_tray_info(
    app: AppHandle,
    display_mode: String,
    rx_bytes_per_sec: f64,
    tx_bytes_per_sec: f64,
    active_conns: u32,
) {
    fn fmt(bytes: f64) -> String {
        if bytes < 1024.0 {
            format!("{:.0} B/s", bytes)
        } else if bytes < 1024.0 * 1024.0 {
            format!("{:.1} KB/s", bytes / 1024.0)
        } else if bytes < 1024.0 * 1024.0 * 1024.0 {
            format!("{:.1} MB/s", bytes / (1024.0 * 1024.0))
        } else {
            format!("{:.2} GB/s", bytes / (1024.0 * 1024.0 * 1024.0))
        }
    }
    let title = match display_mode.as_str() {
        "speed" => Some(format!("↓{} ↑{}", fmt(rx_bytes_per_sec), fmt(tx_bytes_per_sec))),
        "connections" => Some(format!("⇌ {}", active_conns)),
        _ => None, // "none"
    };
    tray::update_tray_title(&app, title.as_deref());
}

#[tauri::command]
pub fn export_profile(profile: Profile) -> Result<String, String> {
    // Verify the profile has all inline keys (exportable).
    if profile.identity_key.as_ref().map_or(true, |k| k.trim().is_empty()) {
        return Err("Profile must have an inline identity key to export".into());
    }
    for (i, jh) in profile.jump_hosts.iter().enumerate() {
        if jh.identity_key.is_none()
            && jh.identity_file.as_ref().map_or(false, |f| !f.trim().is_empty())
        {
            return Err(format!(
                "Jump host {} uses a file-based identity; switch to inline PEM to export",
                i + 1
            ));
        }
    }
    let json = serde_json::to_string(&profile).map_err(|e| e.to_string())?;
    crypto::encrypt(json.as_bytes())
}

#[tauri::command]
pub fn export_profile_to_file(profile: Profile, path: PathBuf) -> Result<(), String> {
    if profile.identity_key.as_ref().map_or(true, |k| k.trim().is_empty()) {
        return Err("Profile must have an inline identity key to export".into());
    }
    for (i, jh) in profile.jump_hosts.iter().enumerate() {
        if jh.identity_key.is_none() && jh.identity_file.as_ref().map_or(false, |f| !f.trim().is_empty()) {
            return Err(format!(
                "Jump host {} uses a file-based identity; switch to inline PEM to export",
                i + 1
            ));
        }
    }
    let json = serde_json::to_string(&profile).map_err(|e| e.to_string())?;
    let encrypted = crypto::encrypt(json.as_bytes())?;
    std::fs::write(&path, encrypted).map_err(|e| format!("Failed to write file: {e}"))
}

#[tauri::command]
pub fn import_profile(app: AppHandle, data: String) -> Result<Vec<Profile>, String> {
    let plaintext = crypto::decrypt(&data)?;
    let json = String::from_utf8(plaintext).map_err(|_| "Invalid UTF-8 in decrypted data")?;
    let mut profile: Profile =
        serde_json::from_str(&json).map_err(|e| format!("Invalid profile data: {e}"))?;
    // Assign a new ID and mark as imported.
    profile.id = uuid::Uuid::new_v4().to_string();
    profile.imported = true;
    profiles::upsert_profile(&app, profile)
}

#[tauri::command]
pub fn import_profile_from_file(app: AppHandle, path: PathBuf) -> Result<Vec<Profile>, String> {
    let data = std::fs::read_to_string(&path)
        .map_err(|e| format!("Failed to read file: {e}"))?;
    let plaintext = crypto::decrypt(data.trim())?;
    let json = String::from_utf8(plaintext).map_err(|_| "Invalid UTF-8 in decrypted data")?;
    let mut profile: Profile =
        serde_json::from_str(&json).map_err(|e| format!("Invalid profile data: {e}"))?;
    profile.id = uuid::Uuid::new_v4().to_string();
    profile.imported = true;
    profiles::upsert_profile(&app, profile)
}

/// Returns the macOS privileged helper status as a string.
/// Possible values: "enabled", "requires_approval", "not_registered", "not_found", "os_too_old", "not_macos".
#[tauri::command]
pub fn get_helper_status() -> String {
    #[cfg(target_os = "macos")]
    {
        use crate::helper_ipc::{helper_status, HelperStatus};
        match helper_status() {
            HelperStatus::Enabled => "enabled".to_string(),
            HelperStatus::RequiresApproval => "requires_approval".to_string(),
            HelperStatus::NotRegistered => "not_registered".to_string(),
            HelperStatus::NotFound => "not_found".to_string(),
            HelperStatus::OsTooOld => "os_too_old".to_string(),
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        "not_macos".to_string()
    }
}

/// Attempts to register the macOS privileged helper daemon.
/// Returns Ok(true) if helper is now running, Ok(false) if not applicable, Err on failure.
#[tauri::command]
pub fn register_helper() -> Result<bool, String> {
    #[cfg(target_os = "macos")]
    {
        crate::helper_ipc::ensure_helper_running()
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(false)
    }
}

/// Set the native window theme so the vibrancy effect matches the app's chosen theme.
/// `theme` should be "light", "dark", or "system".
#[tauri::command]
pub fn set_window_theme(app: AppHandle, theme: String) {
    let tauri_theme = match theme.as_str() {
        "light" => Some(tauri::Theme::Light),
        "dark" => Some(tauri::Theme::Dark),
        _ => None, // system
    };
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.set_theme(tauri_theme);
    }
}

#[tauri::command]
pub fn get_app_version(app: AppHandle) -> String {
    app.config().version.clone().unwrap_or_else(|| "0.0.0".into())
}

#[tauri::command]
pub fn get_tunnel_version(state: State<'_, sidecar::AppState>) -> String {
    state.tunnel_version().to_string()
}

#[derive(serde::Serialize)]
pub struct UpdateInfo {
    pub has_update: bool,
    pub latest_version: String,
    pub current_version: String,
    pub release_url: String,
    pub release_notes: String,
}

#[tauri::command]
pub async fn check_for_update(app: AppHandle) -> Result<UpdateInfo, String> {
    let current = app
        .config()
        .version
        .clone()
        .unwrap_or_else(|| "0.0.0".into());

    let client = reqwest::Client::builder()
        .user_agent("NetFerry-Desktop")
        .build()
        .map_err(|e| e.to_string())?;

    let resp = client
        .get("https://api.github.com/repos/hoveychen/netferry/releases/latest")
        .send()
        .await
        .map_err(|e| format!("Failed to fetch release info: {e}"))?;

    if !resp.status().is_success() {
        return Err(format!("GitHub API returned status {}", resp.status()));
    }

    let body: serde_json::Value = resp.json().await.map_err(|e| e.to_string())?;

    let tag = body["tag_name"]
        .as_str()
        .unwrap_or("v0.0.0")
        .trim_start_matches('v');
    let release_url = body["html_url"].as_str().unwrap_or("").to_string();
    let release_notes = body["body"].as_str().unwrap_or("").to_string();

    let has_update = version_gt(tag, &current);

    Ok(UpdateInfo {
        has_update,
        latest_version: tag.to_string(),
        current_version: current,
        release_url,
        release_notes,
    })
}

/// Simple semver comparison: returns true if `a` > `b`.
fn version_gt(a: &str, b: &str) -> bool {
    let parse = |s: &str| -> Vec<u64> {
        s.split('.')
            .map(|p| p.parse::<u64>().unwrap_or(0))
            .collect()
    };
    let va = parse(a);
    let vb = parse(b);
    for i in 0..va.len().max(vb.len()) {
        let pa = va.get(i).copied().unwrap_or(0);
        let pb = vb.get(i).copied().unwrap_or(0);
        if pa != pb {
            return pa > pb;
        }
    }
    false
}

#[tauri::command]
pub async fn lookup_geoip(host: String) -> Result<String, String> {
    let url = format!("https://ipwho.is/{}", host);
    log::debug!("[geoip] fetching: {}", url);
    let resp = reqwest::get(&url).await.map_err(|e| {
        log::warn!("[geoip] request error: {}", e);
        e.to_string()
    })?;
    let status = resp.status();
    log::debug!("[geoip] status: {}", status);
    let body = resp.text().await.map_err(|e| e.to_string())?;
    log::debug!("[geoip] body: {}", &body[..body.len().min(200)]);
    Ok(body)
}
