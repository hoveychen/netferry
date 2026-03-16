use crate::sidecar::{self, AppState};
use tauri::menu::{MenuBuilder, MenuItemBuilder};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager};

pub fn setup_tray(app: &AppHandle) -> Result<(), tauri::Error> {
    let show_item = MenuItemBuilder::with_id("show_window", "Show Window").build(app)?;
    let disconnect_item = MenuItemBuilder::with_id("disconnect", "Disconnect").build(app)?;
    let quit_item = MenuItemBuilder::with_id("quit", "Quit").build(app)?;
    let menu = MenuBuilder::new(app)
        .item(&show_item)
        .item(&disconnect_item)
        .separator()
        .item(&quit_item)
        .build()?;

    let app_handle = app.clone();
    TrayIconBuilder::new()
        .menu(&menu)
        .tooltip("NetFerry: disconnected")
        .on_menu_event(move |app, event| match event.id().as_ref() {
            "show_window" => {
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.show();
                    let _ = window.set_focus();
                }
            }
            "disconnect" => {
                let state = app.state::<AppState>();
                let _ = sidecar::disconnect(app.clone(), state);
            }
            "quit" => {
                app.exit(0);
            }
            _ => {}
        })
        .on_tray_icon_event(move |tray, event| {
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
    let state = app_handle.state::<AppState>();
    if let Ok(status) = sidecar::current_status(state) {
        let _ = app_handle.emit(sidecar::STATUS_EVENT, status);
    }

    Ok(())
}

pub fn update_tray_tooltip(app: &AppHandle, text: &str) {
    if let Some(tray) = app.tray_by_id("main") {
        let _ = tray.set_tooltip(Some(text));
    }
}
