use crate::models::{ConnectionStatus, Profile, SshHostEntry};
use crate::{profiles, sidecar, ssh_config, tray};
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
