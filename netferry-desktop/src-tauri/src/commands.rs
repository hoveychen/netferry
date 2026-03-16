use crate::models::{ConnectionStatus, Profile, SshHostEntry};
use crate::{profiles, sidecar, ssh_config, tray};
use tauri::{AppHandle, State};

#[tauri::command]
pub fn list_profiles(app: AppHandle) -> Result<Vec<Profile>, String> {
    profiles::load_profiles(&app)
}

#[tauri::command]
pub fn save_profile(app: AppHandle, profile: Profile) -> Result<Vec<Profile>, String> {
    profiles::upsert_profile(&app, profile)
}

#[tauri::command]
pub fn delete_profile(app: AppHandle, profile_id: String) -> Result<Vec<Profile>, String> {
    profiles::remove_profile(&app, &profile_id)
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
    let status = sidecar::connect(app.clone(), state, profile.clone())?;
    tray::update_tray_tooltip(&app, &format!("NetFerry: connected to {}", profile.name));
    Ok(status)
}

#[tauri::command]
pub fn disconnect_profile(
    app: AppHandle,
    state: State<'_, sidecar::AppState>,
) -> Result<ConnectionStatus, String> {
    let status = sidecar::disconnect(app.clone(), state)?;
    tray::update_tray_tooltip(&app, "NetFerry: disconnected");
    Ok(status)
}

#[tauri::command]
pub fn get_connection_status(
    state: State<'_, sidecar::AppState>,
) -> Result<ConnectionStatus, String> {
    sidecar::current_status(state)
}
