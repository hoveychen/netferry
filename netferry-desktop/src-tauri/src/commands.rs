use crate::models::{ConnectionStatus, GlobalSettings, Profile, SshHostEntry};
use crate::{crypto, profiles, settings, sidecar, ssh_config, tray};
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
    Ok(result)
}

#[tauri::command]
pub fn delete_profile(app: AppHandle, profile_id: String) -> Result<Vec<Profile>, String> {
    let result = profiles::remove_profile(&app, &profile_id)?;
    tray::rebuild_tray_menu(&app);
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
) -> Result<ConnectionStatus, String> {
    sidecar::connect(app, state, profile)
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
pub fn list_method_features() -> Result<HashMap<String, Vec<String>>, String> {
    sidecar::query_method_features()
}

#[tauri::command]
pub fn update_tray_speed(app: AppHandle, rx_bytes_per_sec: f64, tx_bytes_per_sec: f64) {
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
    let title = format!("↓{} ↑{}", fmt(rx_bytes_per_sec), fmt(tx_bytes_per_sec));
    tray::update_tray_title(&app, Some(&title));
}

#[tauri::command]
pub fn export_profile(profile: Profile) -> Result<String, String> {
    // Verify the profile has all inline keys (exportable).
    if profile.identity_key.as_ref().map_or(true, |k| k.trim().is_empty()) {
        return Err("Profile must have an inline identity key to export".into());
    }
    for (i, jh) in profile.jump_hosts.iter().enumerate() {
        if jh.identity_key.as_ref().map_or(false, |_| false) {
            // identity_key is Some — fine
        } else if jh.identity_file.as_ref().map_or(false, |f| !f.trim().is_empty()) {
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
