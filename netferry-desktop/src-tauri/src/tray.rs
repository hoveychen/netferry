use crate::models::ConnectionStatus;
use crate::profiles;
use crate::sidecar::{self, AppState};
use tauri::menu::{CheckMenuItemBuilder, MenuBuilder, MenuItemBuilder};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager};

fn build_menu(app: &AppHandle) -> Result<tauri::menu::Menu<tauri::Wry>, tauri::Error> {
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

    let all_profiles = profiles::load_profiles(app).unwrap_or_default();

    let show_item = MenuItemBuilder::with_id("show_window", "Show Window").build(app)?;
    let quit_item = MenuItemBuilder::with_id("quit", "Quit").build(app)?;

    let is_active = matches!(current_status.state.as_str(), "connected" | "connecting");

    if is_active {
        let connected_id = current_status.profile_id.as_deref().unwrap_or("");
        let connected_name = all_profiles
            .iter()
            .find(|p| p.id == connected_id)
            .map(|p| p.name.as_str())
            .unwrap_or("Unknown");
        let toggle = CheckMenuItemBuilder::with_id("toggle", connected_name)
            .checked(true)
            .build(app)?;
        MenuBuilder::new(app)
            .item(&toggle)
            .separator()
            .item(&show_item)
            .separator()
            .item(&quit_item)
            .build()
    } else {
        // Build profile items first so they live long enough for the builder.
        let profile_items: Result<Vec<_>, _> = all_profiles
            .iter()
            .map(|p| {
                MenuItemBuilder::with_id(
                    format!("connect:{}", p.id),
                    format!("Connect: {}", p.name),
                )
                .build(app)
            })
            .collect();
        let profile_items = profile_items?;

        let mut builder = MenuBuilder::new(app);
        if profile_items.is_empty() {
            let toggle = CheckMenuItemBuilder::with_id("toggle", "Not connected")
                .checked(false)
                .enabled(false)
                .build(app)?;
            builder = builder.item(&toggle);
        } else if profile_items.len() == 1 {
            let toggle = CheckMenuItemBuilder::with_id("toggle", &all_profiles[0].name)
                .checked(false)
                .build(app)?;
            builder = builder.item(&toggle);
        } else {
            let toggle = CheckMenuItemBuilder::with_id("toggle", "Disconnected")
                .checked(false)
                .enabled(false)
                .build(app)?;
            builder = builder.item(&toggle).separator();
            for item in &profile_items {
                builder = builder.item(item);
            }
        }
        builder
            .separator()
            .item(&show_item)
            .separator()
            .item(&quit_item)
            .build()
    }
}

pub fn setup_tray(app: &AppHandle) -> Result<(), tauri::Error> {
    // macOS：单色黑色轮廓，template image 让系统自动适配深色/浅色模式
    // Windows/Linux：彩色图标，适合深色任务栏
    #[cfg(target_os = "macos")]
    let icon = tauri::include_image!("icons/tray-icon.png");
    #[cfg(not(target_os = "macos"))]
    let icon = tauri::include_image!("icons/tray-icon-win.png");

    let menu = build_menu(app)?;

    let builder = TrayIconBuilder::with_id("main")
        .icon(icon)
        .menu(&menu);

    #[cfg(target_os = "macos")]
    let builder = builder.icon_as_template(true);

    builder
        .tooltip("NetFerry: disconnected")
        .on_menu_event(|app, event| match event.id().as_ref() {
            "show_window" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
            "toggle" => {
                let state = app.state::<AppState>();
                let is_active = state
                    .status
                    .lock()
                    .map(|g| matches!(g.state.as_str(), "connected" | "connecting"))
                    .unwrap_or(false);
                if is_active {
                    let _ = sidecar::disconnect(app.clone(), state);
                } else if let Ok(profiles) = profiles::load_profiles(app) {
                    if profiles.len() == 1 {
                        let _ = sidecar::connect(app.clone(), state, profiles.into_iter().next().unwrap());
                    }
                }
            }
            "quit" => {
                app.exit(0);
            }
            id if id.starts_with("connect:") => {
                let profile_id = &id["connect:".len()..];
                if let Ok(all_profiles) = profiles::load_profiles(app) {
                    if let Some(profile) =
                        all_profiles.into_iter().find(|p| p.id == profile_id)
                    {
                        let state = app.state::<AppState>();
                        let _ = sidecar::connect(app.clone(), state, profile);
                    }
                }
            }
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                let app = tray.app_handle();
                if let Some(window) = app.get_webview_window("main") {
                    let is_visible = window.is_visible().unwrap_or(false);
                    if is_visible {
                        let _ = window.hide();
                    } else {
                        let _ = window.show();
                        let _ = window.set_focus();
                    }
                }
            }
        })
        .build(app)?;

    // Emit an initial status event so the UI syncs immediately.
    let state = app.state::<AppState>();
    if let Ok(status) = sidecar::current_status(state) {
        let _ = app.emit(sidecar::STATUS_EVENT, status);
    }

    Ok(())
}

pub fn rebuild_tray_menu(app: &AppHandle) {
    if let Some(tray) = app.tray_by_id("main") {
        if let Ok(menu) = build_menu(app) {
            let _ = tray.set_menu(Some(menu));
        }
    }
}

pub fn update_tray_tooltip(app: &AppHandle, text: &str) {
    if let Some(tray) = app.tray_by_id("main") {
        let _ = tray.set_tooltip(Some(text));
    }
}

pub fn update_tray_title(app: &AppHandle, title: Option<&str>) {
    if let Some(tray) = app.tray_by_id("main") {
        let _ = tray.set_title(title);
    }
}
