use crate::models::Profile;
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

fn profile_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    if !dir.exists() {
        fs::create_dir_all(&dir).map_err(|e| format!("Failed to create app data directory: {e}"))?;
    }
    Ok(dir.join("profiles.json"))
}

pub fn load_profiles(app: &AppHandle) -> Result<Vec<Profile>, String> {
    let path = profile_path(app)?;
    if !path.exists() {
        return Ok(Vec::new());
    }
    let raw = fs::read_to_string(&path).map_err(|e| format!("Failed to read profiles.json: {e}"))?;
    if raw.trim().is_empty() {
        return Ok(Vec::new());
    }
    serde_json::from_str::<Vec<Profile>>(&raw).map_err(|e| format!("Failed to parse profiles.json: {e}"))
}

pub fn save_profiles(app: &AppHandle, profiles: &[Profile]) -> Result<(), String> {
    let path = profile_path(app)?;
    let json = serde_json::to_string_pretty(profiles).map_err(|e| format!("Failed to serialize profiles: {e}"))?;
    fs::write(path, json).map_err(|e| format!("Failed to write profiles.json: {e}"))
}

pub fn upsert_profile(app: &AppHandle, profile: Profile) -> Result<Vec<Profile>, String> {
    let mut profiles = load_profiles(app)?;
    if let Some(i) = profiles.iter().position(|p| p.id == profile.id) {
        profiles[i] = profile;
    } else {
        profiles.push(profile);
    }
    save_profiles(app, &profiles)?;
    Ok(profiles)
}

pub fn remove_profile(app: &AppHandle, profile_id: &str) -> Result<Vec<Profile>, String> {
    let mut profiles = load_profiles(app)?;
    profiles.retain(|p| p.id != profile_id);
    save_profiles(app, &profiles)?;
    Ok(profiles)
}
