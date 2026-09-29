use crate::groups;
use crate::models::ProfileGroup;
use crate::priorities;
use crate::profiles;
use crate::rules;
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

    // Legacy routes.json entries go to the global rules.json. The default group
    // takes part in the one-time rules merge as if its file already carried
    // them (the pre-global layout), so any other existing groups' rules are
    // merged too. If rules.json already exists it is authoritative and the
    // legacy routes are left in routes.json untouched.
    let active = settings_before
        .active_group_id
        .clone()
        .unwrap_or_else(|| DEFAULT_GROUP_ID.to_string());
    let rule_set = rules::load_rules_with_extra(
        app,
        vec![(DEFAULT_GROUP_ID.to_string(), legacy_default_group_raw(&legacy_routes))],
        Some(&active),
    )?;
    let children_ids: Vec<String> = profiles.iter().map(|p| p.id.clone()).collect();

    let group = ProfileGroup {
        id: DEFAULT_GROUP_ID.to_string(),
        name: DEFAULT_GROUP_NAME.to_string(),
        children_ids,
        legacy_children: Vec::new(),
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
        "migrate_v2: wrote groups/{DEFAULT_GROUP_ID}.json with {} child ids, {} rules in rules.json, {} priorities",
        group.children_ids.len(),
        rule_set.rules.len(),
        group.priorities.len(),
    );
    Ok(())
}

/// The legacy `routes.json` map shaped like a pre-global group file, for
/// `rules::merge_legacy_groups`. Values are normalised by the merge.
fn legacy_default_group_raw(legacy: &HashMap<String, String>) -> serde_json::Value {
    serde_json::json!({
        "id": DEFAULT_GROUP_ID,
        "name": DEFAULT_GROUP_NAME,
        "rules": legacy,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::RouteMode;

    #[test]
    fn legacy_routes_land_in_rules_json() {
        let dir = std::env::temp_dir().join(format!(
            "netferry-migrate-v2-test-{}-{}",
            std::process::id(),
            crate::models::now_ms()
        ));
        std::fs::create_dir_all(dir.join("groups")).unwrap();
        // Another pre-global group already on disk also contributes.
        std::fs::write(
            dir.join("groups/work.json"),
            r#"{"id":"work","name":"Work","rules":{"shared.com":"blocked","w.com":"direct"}}"#,
        )
        .unwrap();

        let legacy: HashMap<String, String> = [
            ("a.com", "direct"),
            ("b.com", "blocked"),
            ("c.com", "default"),
            ("shared.com", "tunnel"),
        ]
        .into_iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect();

        let set = rules::load_rules_in(
            &dir,
            Some(DEFAULT_GROUP_ID),
            vec![(DEFAULT_GROUP_ID.to_string(), legacy_default_group_raw(&legacy))],
        )
        .unwrap();
        assert_eq!(set.rules["a.com"], RouteMode::from_kind("direct"));
        assert_eq!(set.rules["b.com"], RouteMode::from_kind("blocked"));
        assert_eq!(set.rules["c.com"], RouteMode::tunnel());
        assert_eq!(set.rules["shared.com"], RouteMode::tunnel(), "active default wins");
        assert_eq!(set.rules["w.com"], RouteMode::from_kind("direct"));

        // Persisted: a plain reload (no extras) returns the same rules.
        let reloaded = rules::load_rules_in(&dir, Some(DEFAULT_GROUP_ID), Vec::new()).unwrap();
        assert_eq!(reloaded, set);
        let _ = std::fs::remove_dir_all(&dir);
    }
}
