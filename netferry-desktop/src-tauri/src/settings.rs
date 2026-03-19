use crate::models::GlobalSettings;
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

fn settings_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    if !dir.exists() {
        fs::create_dir_all(&dir)
            .map_err(|e| format!("Failed to create app data directory: {e}"))?;
    }
    Ok(dir.join("settings.json"))
}

pub fn load_settings(app: &AppHandle) -> Result<GlobalSettings, String> {
    let path = settings_path(app)?;
    if !path.exists() {
        return Ok(GlobalSettings::default());
    }
    let raw =
        fs::read_to_string(&path).map_err(|e| format!("Failed to read settings.json: {e}"))?;
    if raw.trim().is_empty() {
        return Ok(GlobalSettings::default());
    }
    serde_json::from_str::<GlobalSettings>(&raw)
        .map_err(|e| format!("Failed to parse settings.json: {e}"))
}

pub fn save_settings(app: &AppHandle, settings: &GlobalSettings) -> Result<(), String> {
    let path = settings_path(app)?;
    let json = serde_json::to_string_pretty(settings)
        .map_err(|e| format!("Failed to serialize settings: {e}"))?;
    fs::write(path, json).map_err(|e| format!("Failed to write settings.json: {e}"))
}
