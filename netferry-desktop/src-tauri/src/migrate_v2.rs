use crate::groups;
use crate::models::{ProfileGroup, RouteMode};
use crate::priorities;
use crate::profiles;
use crate::settings;
use std::collections::HashMap;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

pub const DEFAULT_GROUP_ID: &str = "default";
pub const DEFAULT_GROUP_NAME: &str = "Default";

fn sentinel_path(app: &AppHandle) -> Result<PathBuf, String> {
    let dir = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("Failed to read app data directory: {e}"))?;
    Ok(dir.join("groups").join(format!("{DEFAULT_GROUP_ID}.json")))
}

/// One-shot migration from legacy flat-file storage (profiles.json + routes.json +
/// priorities.json) into `groups/default.json`. Idempotent: if the target file
/// already exists, returns Ok immediately.
///
/// Legacy files are NOT deleted — P1 keeps them in place so existing commands
/// continue to operate as today. P2/P3 will switch reads/writes over to the
/// group-based layout.
pub fn run(app: &AppHandle) -> Result<(), String> {
    let sentinel = sentinel_path(app)?;
    if sentinel.exists() {
        return Ok(());
    }

    let profiles = profiles::load_profiles(app).unwrap_or_default();
    let legacy_routes = priorities::load_routes(app).unwrap_or_default();
    let legacy_priorities = priorities::load_priorities(app).unwrap_or_default();
    let settings_before = settings::load_settings(app).unwrap_or_default();

    let default_profile_id = settings_before
        .auto_connect_profile_id
        .as_deref()
        .filter(|id| profiles.iter().any(|p| p.id == *id))
        .map(|s| s.to_string())
        .or_else(|| profiles.first().map(|p| p.id.clone()));

    let rules = translate_routes(&legacy_routes, default_profile_id.as_deref());

    let group = ProfileGroup {
        id: DEFAULT_GROUP_ID.to_string(),
        name: DEFAULT_GROUP_NAME.to_string(),
        children: profiles,
        rules,
        priorities: legacy_priorities,
    };
    groups::save_group(app, &group)?;

    // Set active_group_id so a future P2 build picks up this group on launch.
    if settings_before.active_group_id.is_none() {
        let mut s = settings_before;
        s.active_group_id = Some(DEFAULT_GROUP_ID.to_string());
        settings::save_settings(app, &s)?;
    }

    log::info!(
        "migrate_v2: wrote groups/{DEFAULT_GROUP_ID}.json with {} profiles, {} rules, {} priorities",
        group.children.len(),
        group.rules.len(),
        group.priorities.len(),
    );
    Ok(())
}

fn translate_routes(
    legacy: &HashMap<String, String>,
    default_profile_id: Option<&str>,
) -> HashMap<String, RouteMode> {
    let mut out = HashMap::with_capacity(legacy.len());
    for (host, mode) in legacy {
        let route = match mode.as_str() {
            "direct" => RouteMode {
                kind: "direct".to_string(),
                profile_id: None,
            },
            "blocked" => RouteMode {
                kind: "blocked".to_string(),
                profile_id: None,
            },
            // Legacy "tunnel" (or any unrecognised value) maps to the user's
            // autoconnect profile (or first profile). If no profiles exist at
            // migration time, collapse to "default" kind which P2 will resolve
            // at runtime against the active group's children[0].
            _ => match default_profile_id {
                Some(pid) => RouteMode {
                    kind: "tunnel".to_string(),
                    profile_id: Some(pid.to_string()),
                },
                None => RouteMode {
                    kind: "default".to_string(),
                    profile_id: None,
                },
            },
        };
        out.insert(host.clone(), route);
    }
    out
}
