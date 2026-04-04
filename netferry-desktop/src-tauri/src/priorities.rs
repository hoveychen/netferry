use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

fn priorities_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    if !dir.exists() {
        fs::create_dir_all(&dir)
            .map_err(|e| format!("Failed to create app data directory: {e}"))?;
    }
    Ok(dir.join("priorities.json"))
}

/// Load destination priorities from disk. Returns an empty map if the file
/// doesn't exist yet (first run).
pub fn load_priorities(app: &AppHandle) -> Result<HashMap<String, i32>, String> {
    let path = priorities_path(app)?;
    if !path.exists() {
        return Ok(HashMap::new());
    }
    let raw =
        fs::read_to_string(&path).map_err(|e| format!("Failed to read priorities.json: {e}"))?;
    if raw.trim().is_empty() {
        return Ok(HashMap::new());
    }
    serde_json::from_str::<HashMap<String, i32>>(&raw)
        .map_err(|e| format!("Failed to parse priorities.json: {e}"))
}

/// Persist destination priorities to disk.
pub fn save_priorities(app: &AppHandle, priorities: &HashMap<String, i32>) -> Result<(), String> {
    let path = priorities_path(app)?;
    let json = serde_json::to_string_pretty(priorities)
        .map_err(|e| format!("Failed to serialize priorities: {e}"))?;
    fs::write(path, json).map_err(|e| format!("Failed to write priorities.json: {e}"))
}

fn routes_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    if !dir.exists() {
        fs::create_dir_all(&dir)
            .map_err(|e| format!("Failed to create app data directory: {e}"))?;
    }
    Ok(dir.join("routes.json"))
}

/// Load destination route modes from disk. Returns an empty map if the file
/// doesn't exist yet (first run). Values: "tunnel", "direct", "blocked".
pub fn load_routes(app: &AppHandle) -> Result<HashMap<String, String>, String> {
    let path = routes_path(app)?;
    if !path.exists() {
        return Ok(HashMap::new());
    }
    let raw =
        fs::read_to_string(&path).map_err(|e| format!("Failed to read routes.json: {e}"))?;
    if raw.trim().is_empty() {
        return Ok(HashMap::new());
    }
    serde_json::from_str::<HashMap<String, String>>(&raw)
        .map_err(|e| format!("Failed to parse routes.json: {e}"))
}

/// Persist destination route modes to disk.
pub fn save_routes(app: &AppHandle, routes: &HashMap<String, String>) -> Result<(), String> {
    let path = routes_path(app)?;
    let json = serde_json::to_string_pretty(routes)
        .map_err(|e| format!("Failed to serialize routes: {e}"))?;
    fs::write(path, json).map_err(|e| format!("Failed to write routes.json: {e}"))
}
