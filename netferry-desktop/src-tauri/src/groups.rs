use crate::models::ProfileGroup;
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

fn groups_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    let sub = dir.join("groups");
    if !sub.exists() {
        fs::create_dir_all(&sub)
            .map_err(|e| format!("Failed to create groups directory: {e}"))?;
    }
    Ok(sub)
}

fn group_path(app: &AppHandle, group_id: &str) -> Result<PathBuf, String> {
    if group_id.is_empty() || group_id.contains('/') || group_id.contains('\\') {
        return Err(format!("Invalid group id: {group_id}"));
    }
    Ok(groups_dir(app)?.join(format!("{group_id}.json")))
}

pub fn list_groups(app: &AppHandle) -> Result<Vec<ProfileGroup>, String> {
    let dir = groups_dir(app)?;
    let mut out = Vec::new();
    let entries = fs::read_dir(&dir)
        .map_err(|e| format!("Failed to read groups directory: {e}"))?;
    for entry in entries.flatten() {
        let path = entry.path();
        if path.extension().and_then(|e| e.to_str()) != Some("json") {
            continue;
        }
        let raw = fs::read_to_string(&path)
            .map_err(|e| format!("Failed to read {}: {e}", path.display()))?;
        if raw.trim().is_empty() {
            continue;
        }
        let mut group: ProfileGroup = serde_json::from_str(&raw)
            .map_err(|e| format!("Failed to parse {}: {e}", path.display()))?;
        if group.normalize_legacy() {
            let _ = save_group(app, &group);
        }
        out.push(group);
    }
    out.sort_by(|a, b| a.name.cmp(&b.name));
    Ok(out)
}

pub fn load_group(app: &AppHandle, group_id: &str) -> Result<Option<ProfileGroup>, String> {
    let path = group_path(app, group_id)?;
    if !path.exists() {
        return Ok(None);
    }
    let raw = fs::read_to_string(&path)
        .map_err(|e| format!("Failed to read {}: {e}", path.display()))?;
    if raw.trim().is_empty() {
        return Ok(None);
    }
    let mut group: ProfileGroup = serde_json::from_str(&raw)
        .map_err(|e| format!("Failed to parse {}: {e}", path.display()))?;
    if group.normalize_legacy() {
        let _ = save_group(app, &group);
    }
    Ok(Some(group))
}

pub fn save_group(app: &AppHandle, group: &ProfileGroup) -> Result<(), String> {
    let path = group_path(app, &group.id)?;
    let json = serde_json::to_string_pretty(group)
        .map_err(|e| format!("Failed to serialize group: {e}"))?;
    fs::write(&path, json).map_err(|e| format!("Failed to write {}: {e}", path.display()))
}

pub fn delete_group(app: &AppHandle, group_id: &str) -> Result<(), String> {
    let path = group_path(app, group_id)?;
    if path.exists() {
        fs::remove_file(&path)
            .map_err(|e| format!("Failed to delete {}: {e}", path.display()))?;
    }
    Ok(())
}
