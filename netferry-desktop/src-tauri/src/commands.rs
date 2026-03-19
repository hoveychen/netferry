use crate::models::{ConnectionStatus, GlobalSettings, Profile, SshHostEntry};
use crate::{profiles, settings, sidecar, ssh_config, tray};
use tauri::{AppHandle, State};

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
pub fn import_ssh_hosts() -> Result<Vec<SshHostEntry>, String> {
    ssh_config::parse_default_ssh_config()
}

#[tauri::command]
pub fn get_default_identity_file() -> Option<String> {
    ssh_config::get_default_identity_file()
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
pub async fn lookup_geoip(host: String) -> Result<String, String> {
    let url = format!("https://ipwho.is/{}", host);
    eprintln!("[geoip] fetching: {}", url);
    let resp = reqwest::get(&url).await.map_err(|e| {
        eprintln!("[geoip] request error: {}", e);
        e.to_string()
    })?;
    let status = resp.status();
    eprintln!("[geoip] status: {}", status);
    let body = resp.text().await.map_err(|e| e.to_string())?;
    eprintln!("[geoip] body: {}", &body[..body.len().min(200)]);
    Ok(body)
}
