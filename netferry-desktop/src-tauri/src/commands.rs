use crate::models::{ConnectionStatus, GlobalSettings, Profile, ProfileGroup, SshHostEntry};
use crate::{crypto, groups, menu, priorities, profiles, settings, sidecar, ssh_config, tray};
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
    menu::rebuild_app_menu(&app);
    Ok(result)
}

#[tauri::command]
pub fn delete_profile(app: AppHandle, profile_id: String) -> Result<Vec<Profile>, String> {
    let result = profiles::remove_profile(&app, &profile_id)?;
    tray::rebuild_tray_menu(&app);
    menu::rebuild_app_menu(&app);
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
    group: Option<ProfileGroup>,
    children: Option<Vec<Profile>>,
) -> Result<ConnectionStatus, String> {
    // Group mode requires both `group` and a non-empty `children` list — the
    // children carry inline PEM keys that the temp group.json embeds. Solo
    // mode passes `null` for both and keeps the legacy single-tunnel path.
    let group_spec = match (group, children) {
        (Some(g), Some(c)) if !c.is_empty() => Some((g, c)),
        _ => None,
    };
    sidecar::connect(app, state, profile, group_spec)
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
pub fn get_priorities(app: AppHandle) -> Result<HashMap<String, i32>, String> {
    priorities::load_priorities(&app)
}

#[tauri::command]
pub fn save_priorities(
    app: AppHandle,
    priorities: HashMap<String, i32>,
) -> Result<(), String> {
    priorities::save_priorities(&app, &priorities)
}

#[tauri::command]
pub fn get_routes(app: AppHandle) -> Result<HashMap<String, String>, String> {
    priorities::load_routes(&app)
}

#[tauri::command]
pub fn save_routes(
    app: AppHandle,
    routes: HashMap<String, String>,
) -> Result<(), String> {
    priorities::save_routes(&app, &routes)
}

// ── Profile groups (P1: data-layer only; runtime still uses flat profile list) ──

#[tauri::command]
pub fn list_groups(app: AppHandle) -> Result<Vec<ProfileGroup>, String> {
    groups::list_groups(&app)
}

#[tauri::command]
pub fn get_group(app: AppHandle, group_id: String) -> Result<Option<ProfileGroup>, String> {
    groups::load_group(&app, &group_id)
}

#[tauri::command]
pub fn save_group(app: AppHandle, group: ProfileGroup) -> Result<(), String> {
    groups::save_group(&app, &group)
}

#[tauri::command]
pub fn delete_group(app: AppHandle, group_id: String) -> Result<(), String> {
    groups::delete_group(&app, &group_id)
}

#[tauri::command]
pub fn add_profile_to_group(
    app: AppHandle,
    group_id: String,
    profile_id: String,
) -> Result<ProfileGroup, String> {
    let mut group = groups::load_group(&app, &group_id)?
        .ok_or_else(|| format!("Group not found: {group_id}"))?;
    if !group.children_ids.iter().any(|id| id == &profile_id) {
        group.children_ids.push(profile_id);
        groups::save_group(&app, &group)?;
    }
    Ok(group)
}

#[tauri::command]
pub fn remove_profile_from_group(
    app: AppHandle,
    group_id: String,
    profile_id: String,
) -> Result<ProfileGroup, String> {
    let mut group = groups::load_group(&app, &group_id)?
        .ok_or_else(|| format!("Group not found: {group_id}"))?;
    let before = group.children_ids.len();
    group.children_ids.retain(|id| id != &profile_id);
    if group.children_ids.len() != before {
        groups::save_group(&app, &group)?;
    }
    Ok(group)
}

#[tauri::command]
pub fn list_method_features() -> Result<HashMap<String, Vec<String>>, String> {
    sidecar::query_method_features()
}

#[tauri::command]
pub fn update_tray_info(
    app: AppHandle,
    display_mode: String,
    rx_bytes_per_sec: f64,
    tx_bytes_per_sec: f64,
    active_conns: u32,
) {
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
    let title = match display_mode.as_str() {
        "speed" => Some(format!("↓{} ↑{}", fmt(rx_bytes_per_sec), fmt(tx_bytes_per_sec))),
        "connections" => Some(format!("⇌ {}", active_conns)),
        _ => None, // "none"
    };
    tray::update_tray_title(&app, title.as_deref());
}

/// Inline any file-based identities into a clone of the profile so it is
/// self-contained for export. For each slot whose `identity_key` is empty but
/// whose `identity_file` is non-empty, the file is read (with `~` expanded)
/// and its contents take over `identity_key`; the path is then cleared so the
/// importer does not see a path that means nothing on their machine.
fn inline_identities_for_export(profile: &mut Profile, home: &std::path::Path) -> Result<(), String> {
    let needs_inline = |key: &Option<String>, file: &str| -> bool {
        key.as_deref().map_or(true, |k| k.trim().is_empty()) && !file.trim().is_empty()
    };

    if needs_inline(&profile.identity_key, &profile.identity_file) {
        let resolved = ssh_config::expand_tilde(profile.identity_file.trim(), home);
        let pem = std::fs::read_to_string(&resolved)
            .map_err(|e| format!("Failed to read identity file {resolved}: {e}"))?;
        profile.identity_key = Some(pem);
        profile.identity_file = String::new();
    }

    for (i, jh) in profile.jump_hosts.iter_mut().enumerate() {
        let file = jh.identity_file.clone().unwrap_or_default();
        if needs_inline(&jh.identity_key, &file) {
            let resolved = ssh_config::expand_tilde(file.trim(), home);
            let pem = std::fs::read_to_string(&resolved).map_err(|e| {
                format!(
                    "Failed to read jump host {} identity file {resolved}: {e}",
                    i + 1
                )
            })?;
            jh.identity_key = Some(pem);
            jh.identity_file = None;
        }
    }
    Ok(())
}

#[tauri::command]
pub fn export_profile(app: AppHandle, profile: Profile) -> Result<String, String> {
    let home = app
        .path()
        .home_dir()
        .map_err(|e| format!("Failed to resolve home directory: {e}"))?;
    let mut profile = profile;
    inline_identities_for_export(&mut profile, &home)?;
    let json = serde_json::to_string(&profile).map_err(|e| e.to_string())?;
    crypto::encrypt(json.as_bytes())
}

#[tauri::command]
pub fn export_profile_to_file(
    app: AppHandle,
    profile: Profile,
    path: PathBuf,
) -> Result<(), String> {
    let home = app
        .path()
        .home_dir()
        .map_err(|e| format!("Failed to resolve home directory: {e}"))?;
    let mut profile = profile;
    inline_identities_for_export(&mut profile, &home)?;
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

/// Returns the macOS privileged helper status as a string.
/// Possible values: "enabled", "requires_approval", "not_registered", "not_found", "os_too_old", "not_macos".
#[tauri::command]
pub fn get_helper_status() -> String {
    #[cfg(target_os = "macos")]
    {
        use crate::helper_ipc::{helper_status, HelperStatus};
        match helper_status() {
            HelperStatus::Enabled => "enabled".to_string(),
            HelperStatus::RequiresApproval => "requires_approval".to_string(),
            HelperStatus::NotRegistered => "not_registered".to_string(),
            HelperStatus::NotFound => "not_found".to_string(),
            HelperStatus::OsTooOld => "os_too_old".to_string(),
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        "not_macos".to_string()
    }
}

/// Attempts to register the macOS privileged helper daemon.
/// Returns Ok(true) if helper is now running, Ok(false) if not applicable, Err on failure.
#[tauri::command]
pub fn register_helper() -> Result<bool, String> {
    #[cfg(target_os = "macos")]
    {
        crate::helper_ipc::ensure_helper_running()
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(false)
    }
}

/// Unregisters the macOS privileged helper daemon. Returns Ok(true) on
/// success, Ok(false) if not applicable (non-macOS / OS too old), Err on
/// SMAppService failure. Surface for the Settings "Uninstall" button.
#[tauri::command]
pub fn unregister_helper() -> Result<bool, String> {
    #[cfg(target_os = "macos")]
    {
        crate::helper_ipc::unregister_helper()
    }
    #[cfg(not(target_os = "macos"))]
    {
        Ok(false)
    }
}

/// Opens the macOS "Login Items & Extensions" pane in System Settings so the
/// user can manually toggle or grant permission to the helper. macOS 13+ only.
#[tauri::command]
pub fn open_login_items_settings() -> Result<(), String> {
    #[cfg(target_os = "macos")]
    {
        use std::process::Command;
        Command::new("open")
            .arg("x-apple.systempreferences:com.apple.LoginItems-Settings.extension")
            .status()
            .map_err(|e| format!("Failed to open System Settings: {e}"))?;
        Ok(())
    }
    #[cfg(not(target_os = "macos"))]
    {
        Err("Only available on macOS".to_string())
    }
}

/// Set the native window theme so the vibrancy effect matches the app's chosen theme.
/// `theme` should be "light", "dark", or "system".
#[tauri::command]
pub fn set_window_theme(app: AppHandle, theme: String) {
    let tauri_theme = match theme.as_str() {
        "light" => Some(tauri::Theme::Light),
        "dark" => Some(tauri::Theme::Dark),
        _ => None, // system
    };
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.set_theme(tauri_theme);
    }
}

#[tauri::command]
pub fn get_app_version(app: AppHandle) -> String {
    app.config().version.clone().unwrap_or_else(|| "0.0.0".into())
}

#[tauri::command]
pub fn get_tunnel_version(state: State<'_, sidecar::AppState>) -> String {
    state.tunnel_version().to_string()
}

#[derive(serde::Serialize)]
pub struct UpdateInfo {
    pub has_update: bool,
    pub latest_version: String,
    pub current_version: String,
    pub release_url: String,
    pub release_notes: String,
}

#[tauri::command]
pub async fn check_for_update(app: AppHandle) -> Result<UpdateInfo, String> {
    let current = app
        .config()
        .version
        .clone()
        .unwrap_or_else(|| "0.0.0".into());

    let client = reqwest::Client::builder()
        .user_agent("NetFerry-Desktop")
        .build()
        .map_err(|e| e.to_string())?;

    let resp = client
        .get("https://api.github.com/repos/hoveychen/netferry/releases/latest")
        .send()
        .await
        .map_err(|e| format!("Failed to fetch release info: {e}"))?;

    if !resp.status().is_success() {
        return Err(format!("GitHub API returned status {}", resp.status()));
    }

    let body: serde_json::Value = resp.json().await.map_err(|e| e.to_string())?;

    let tag = body["tag_name"]
        .as_str()
        .unwrap_or("v0.0.0")
        .trim_start_matches('v');
    let release_url = body["html_url"].as_str().unwrap_or("").to_string();
    let release_notes = body["body"].as_str().unwrap_or("").to_string();

    let has_update = version_gt(tag, &current);

    Ok(UpdateInfo {
        has_update,
        latest_version: tag.to_string(),
        current_version: current,
        release_url,
        release_notes,
    })
}

/// Simple semver comparison: returns true if `a` > `b`.
fn version_gt(a: &str, b: &str) -> bool {
    let parse = |s: &str| -> Vec<u64> {
        s.split('.')
            .map(|p| p.parse::<u64>().unwrap_or(0))
            .collect()
    };
    let va = parse(a);
    let vb = parse(b);
    for i in 0..va.len().max(vb.len()) {
        let pa = va.get(i).copied().unwrap_or(0);
        let pb = vb.get(i).copied().unwrap_or(0);
        if pa != pb {
            return pa > pb;
        }
    }
    false
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::JumpHost;
    use std::path::{Path, PathBuf};

    struct Scratch {
        dir: PathBuf,
    }

    impl Scratch {
        fn new() -> Self {
            let dir = std::env::temp_dir()
                .join(format!("netferry-test-{}", uuid::Uuid::new_v4()));
            std::fs::create_dir_all(&dir).unwrap();
            Self { dir }
        }

        fn write(&self, name: &str, contents: &str) -> PathBuf {
            let p = self.dir.join(name);
            std::fs::write(&p, contents).unwrap();
            p
        }
    }

    impl Drop for Scratch {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.dir);
        }
    }

    fn empty_home() -> &'static Path {
        Path::new("/")
    }

    #[test]
    fn inlines_top_level_file_and_clears_path() {
        let s = Scratch::new();
        let key_path = s.write("id_ed25519", "PEM-CONTENTS-A");

        let mut p = Profile::default();
        p.identity_file = key_path.to_string_lossy().to_string();
        p.identity_key = None;

        inline_identities_for_export(&mut p, empty_home()).unwrap();

        assert_eq!(p.identity_key.as_deref(), Some("PEM-CONTENTS-A"));
        assert_eq!(p.identity_file, "");
    }

    #[test]
    fn keeps_existing_inline_key_untouched() {
        let mut p = Profile::default();
        p.identity_key = Some("ALREADY-INLINE".to_string());
        p.identity_file = "/some/path/that/should/not/be/read".to_string();

        inline_identities_for_export(&mut p, empty_home()).unwrap();

        assert_eq!(p.identity_key.as_deref(), Some("ALREADY-INLINE"));
        assert_eq!(p.identity_file, "/some/path/that/should/not/be/read");
    }

    #[test]
    fn both_empty_passes_through() {
        let mut p = Profile::default();
        // identity_file defaults to "" and identity_key to None.
        inline_identities_for_export(&mut p, empty_home()).unwrap();
        assert!(p.identity_key.is_none());
        assert_eq!(p.identity_file, "");
    }

    #[test]
    fn jump_host_file_inlined_and_path_cleared() {
        let s = Scratch::new();
        let jh_path = s.write("jump_id", "JUMP-PEM");

        let mut p = Profile::default();
        p.identity_key = Some("MAIN-INLINE".to_string());
        p.jump_hosts = vec![JumpHost {
            remote: "user@bastion".to_string(),
            identity_file: Some(jh_path.to_string_lossy().to_string()),
            identity_key: None,
        }];

        inline_identities_for_export(&mut p, empty_home()).unwrap();

        assert_eq!(p.jump_hosts[0].identity_key.as_deref(), Some("JUMP-PEM"));
        assert!(p.jump_hosts[0].identity_file.is_none());
    }

    #[test]
    fn multiple_jump_hosts_only_file_based_inlined() {
        let s = Scratch::new();
        let path1 = s.write("jh1", "PEM-1");

        let mut p = Profile::default();
        p.identity_key = Some("MAIN".to_string());
        p.jump_hosts = vec![
            JumpHost {
                remote: "user@a".to_string(),
                identity_file: Some(path1.to_string_lossy().to_string()),
                identity_key: None,
            },
            JumpHost {
                remote: "user@b".to_string(),
                identity_file: None,
                identity_key: Some("INLINE-2".to_string()),
            },
            JumpHost {
                remote: "user@c".to_string(),
                identity_file: None,
                identity_key: None,
            },
        ];

        inline_identities_for_export(&mut p, empty_home()).unwrap();

        assert_eq!(p.jump_hosts[0].identity_key.as_deref(), Some("PEM-1"));
        assert!(p.jump_hosts[0].identity_file.is_none());
        assert_eq!(p.jump_hosts[1].identity_key.as_deref(), Some("INLINE-2"));
        assert_eq!(p.jump_hosts[2].identity_key, None);
        assert_eq!(p.jump_hosts[2].identity_file, None);
    }

    #[test]
    fn missing_file_returns_error_mentioning_path() {
        let mut p = Profile::default();
        p.identity_file = "/definitely/does/not/exist/netferry-test-key".to_string();

        let err = inline_identities_for_export(&mut p, empty_home()).unwrap_err();
        assert!(
            err.contains("/definitely/does/not/exist/netferry-test-key"),
            "error should mention the path that failed: {err}"
        );
    }

    #[test]
    fn missing_jump_host_file_error_mentions_index() {
        let mut p = Profile::default();
        p.identity_key = Some("MAIN".to_string());
        p.jump_hosts = vec![JumpHost {
            remote: "user@x".to_string(),
            identity_file: Some("/no/such/jump/key".to_string()),
            identity_key: None,
        }];

        let err = inline_identities_for_export(&mut p, empty_home()).unwrap_err();
        assert!(err.contains("jump host 1"), "error should name jump host 1: {err}");
    }

    #[test]
    fn tilde_expanded_against_home() {
        let s = Scratch::new();
        // Place the key inside a synthetic "home" with a .ssh subdir.
        let home = s.dir.join("fakehome");
        std::fs::create_dir_all(home.join(".ssh")).unwrap();
        let key_path = home.join(".ssh").join("id_rsa");
        std::fs::write(&key_path, "TILDE-PEM").unwrap();

        let mut p = Profile::default();
        p.identity_file = "~/.ssh/id_rsa".to_string();

        inline_identities_for_export(&mut p, &home).unwrap();

        assert_eq!(p.identity_key.as_deref(), Some("TILDE-PEM"));
        assert_eq!(p.identity_file, "");
    }
}
