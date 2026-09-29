use crate::models::ConnectionStatus;
use crate::profiles;
use crate::sidecar::AppState;
use tauri::menu::{AboutMetadataBuilder, MenuBuilder, MenuItemBuilder, SubmenuBuilder};
use tauri::{AppHandle, Emitter, Manager};
use tauri_plugin_opener::OpenerExt;

pub fn setup_menu(app: &AppHandle) -> Result<(), tauri::Error> {
    let menu = build_app_menu(app)?;
    app.set_menu(menu)?;

    app.on_menu_event(|app, event| {
        let id = event.id().as_ref();
        match id {
            "check_updates" => {
                show_and_focus(app);
                let _ = app.emit("menu-check-updates", ());
            }
            "preferences" => {
                show_and_focus(app);
                let _ = app.emit("menu-navigate", "settings");
            }
            "import_file" => {
                show_and_focus(app);
                let _ = app.emit("menu-import-file", ());
            }
            "import_ssh" => {
                show_and_focus(app);
                let _ = app.emit("menu-import-ssh", ());
            }
            "close_window" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.hide();
                }
            }
            "menu_disconnect" => {
                let state = app.state::<AppState>();
                let _ = crate::sidecar::disconnect(app.clone(), state);
            }
            "menu_connect_default" => {
                let state = app.state::<AppState>();
                // Prefer auto-connect profile, fall back to first profile.
                let auto_id = crate::settings::load_settings(app)
                    .ok()
                    .and_then(|s| s.auto_connect_profile_id);
                if let Ok(all) = profiles::load_profiles(app) {
                    let profile = auto_id
                        .and_then(|id| all.iter().find(|p| p.id == id).cloned())
                        .or_else(|| all.into_iter().next());
                    if let Some(p) = profile {
                        let _ = crate::sidecar::connect(app.clone(), state, p);
                    }
                }
            }
            "zoom" => {
                if let Some(window) = app.get_webview_window("main") {
                    if window.is_maximized().unwrap_or(false) {
                        let _ = window.unmaximize();
                    } else {
                        let _ = window.maximize();
                    }
                }
            }
            "open_log" => {
                if let Ok(log_dir) = app.path().app_log_dir() {
                    let log_file = log_dir.join("netferry.log");
                    if log_file.exists() {
                        let _ = app.opener().reveal_item_in_dir(log_file);
                    } else {
                        let _ = app.opener().open_path(
                            log_dir.to_string_lossy().as_ref(),
                            None::<&str>,
                        );
                    }
                }
            }
            "view_stats" => {
                if let Some(url) = app
                    .state::<AppState>()
                    .stats_port
                    .lock()
                    .ok()
                    .and_then(|p| p.map(|port| format!("http://127.0.0.1:{port}")))
                {
                    let _ = app.opener().open_url(&url, None::<&str>);
                }
            }
            id if id.starts_with("menu_connect:") => {
                let profile_id = &id["menu_connect:".len()..];
                if let Ok(all) = profiles::load_profiles(app) {
                    if let Some(profile) = all.into_iter().find(|p| p.id == profile_id) {
                        let state = app.state::<AppState>();
                        let _ = crate::sidecar::connect(app.clone(), state, profile);
                    }
                }
            }
            _ => {}
        }
    });

    Ok(())
}

fn build_app_menu(app: &AppHandle) -> Result<tauri::menu::Menu<tauri::Wry>, tauri::Error> {
    // ── NetFerry (app) menu ──
    let engine_version = app.state::<AppState>().tunnel_version().to_string();
    let about_metadata = AboutMetadataBuilder::new()
        .credits(Some(format!("Engine Version: {engine_version}")))
        .build();

    let app_menu = SubmenuBuilder::new(app, "NetFerry")
        .about(Some(about_metadata))
        .separator()
        .item(
            &MenuItemBuilder::with_id("check_updates", "Check for Updates\u{2026}")
                .build(app)?,
        )
        .separator()
        .item(
            &MenuItemBuilder::with_id("preferences", "Preferences\u{2026}")
                .accelerator("CmdOrCtrl+,")
                .build(app)?,
        )
        .separator()
        .hide()
        .hide_others()
        .show_all()
        .separator()
        .quit()
        .build()?;

    // ── File menu ──
    let file_menu = SubmenuBuilder::new(app, "File")
        .item(
            &MenuItemBuilder::with_id("import_file", "Import Profile from File\u{2026}")
                .accelerator("CmdOrCtrl+O")
                .build(app)?,
        )
        .item(
            &MenuItemBuilder::with_id("import_ssh", "Import from SSH Config\u{2026}")
                .build(app)?,
        )
        .separator()
        .item(
            &MenuItemBuilder::with_id("close_window", "Close Window")
                .accelerator("CmdOrCtrl+W")
                .build(app)?,
        )
        .build()?;

    // ── Edit menu ──
    let edit_menu = SubmenuBuilder::new(app, "Edit")
        .undo()
        .redo()
        .separator()
        .cut()
        .copy()
        .paste()
        .select_all()
        .build()?;

    // ── Connection menu ──
    let connection_menu = build_connection_submenu(app)?;

    // ── Window menu ──
    let window_menu = SubmenuBuilder::new(app, "Window")
        .minimize()
        .item(&MenuItemBuilder::with_id("zoom", "Zoom").build(app)?)
        .build()?;

    // ── Help menu ──
    let help_menu = SubmenuBuilder::new(app, "Help")
        .item(
            &MenuItemBuilder::with_id("open_log", "Open Log File")
                .build(app)?,
        )
        .item(
            &MenuItemBuilder::with_id("view_stats", "View Stats Dashboard")
                .build(app)?,
        )
        .build()?;

    MenuBuilder::new(app)
        .item(&app_menu)
        .item(&file_menu)
        .item(&edit_menu)
        .item(&connection_menu)
        .item(&window_menu)
        .item(&help_menu)
        .build()
}

fn build_connection_submenu(
    app: &AppHandle,
) -> Result<tauri::menu::Submenu<tauri::Wry>, tauri::Error> {
    let current_status = app
        .state::<AppState>()
        .status
        .lock()
        .map(|g| g.clone())
        .unwrap_or_else(|_| ConnectionStatus {
            state: "disconnected".to_string(),
            profile_id: None,
            message: None,
        });

    let is_active = matches!(current_status.state.as_str(), "connected" | "connecting");
    let all_profiles = profiles::load_profiles(app).unwrap_or_default();

    let mut builder = SubmenuBuilder::new(app, "Connection");

    if is_active {
        builder = builder.item(
            &MenuItemBuilder::with_id("menu_disconnect", "Disconnect")
                .accelerator("CmdOrCtrl+Shift+D")
                .build(app)?,
        );
    } else {
        builder = builder.item(
            &MenuItemBuilder::with_id("menu_connect_default", "Connect")
                .accelerator("CmdOrCtrl+Shift+D")
                .enabled(!all_profiles.is_empty())
                .build(app)?,
        );

        if all_profiles.len() > 1 {
            builder = builder.separator();
            for p in &all_profiles {
                builder = builder.item(
                    &MenuItemBuilder::with_id(
                        format!("menu_connect:{}", p.id),
                        &p.name,
                    )
                    .build(app)?,
                );
            }
        }
    }

    builder.build()
}

/// Rebuild the entire app menu (call when connection status or profiles change).
pub fn rebuild_app_menu(app: &AppHandle) {
    if let Ok(menu) = build_app_menu(app) {
        let _ = app.set_menu(menu);
    }
}

fn show_and_focus(app: &AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.show();
        let _ = window.set_focus();
    }
}
