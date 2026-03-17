mod commands;
mod models;
mod profiles;
mod sidecar;
mod ssh_config;
mod stats;
mod tray;

use tauri::Listener;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .manage(sidecar::AppState::new())
        .setup(|app| {
            // On Windows, sshuttle cannot self-elevate, so the whole app must
            // run as Administrator.  Re-launch with UAC if needed.
            #[cfg(target_os = "windows")]
            sidecar::ensure_elevated(app.handle());

            tray::setup_tray(app.handle())?;

            // Rebuild tray menu and update tooltip whenever connection status changes.
            let app_handle = app.handle().clone();
            app.listen(sidecar::STATUS_EVENT, move |event| {
                tray::rebuild_tray_menu(&app_handle);
                if let Ok(status) =
                    serde_json::from_str::<models::ConnectionStatus>(event.payload())
                {
                    let tooltip = match status.state.as_str() {
                        "connected" => {
                            let name = status
                                .profile_id
                                .as_deref()
                                .and_then(|id| {
                                    profiles::load_profiles(&app_handle)
                                        .ok()
                                        .and_then(|ps| ps.into_iter().find(|p| p.id == id))
                                })
                                .map(|p| p.name)
                                .unwrap_or_else(|| "Unknown".to_string());
                            format!("NetFerry: connected to {name}")
                        }
                        "connecting" => "NetFerry: connecting\u{2026}".to_string(),
                        "error" => "NetFerry: connection error".to_string(),
                        _ => "NetFerry: disconnected".to_string(),
                    };
                    tray::update_tray_tooltip(&app_handle, &tooltip);
                }
            });

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_profiles,
            commands::save_profile,
            commands::delete_profile,
            commands::import_ssh_hosts,
            commands::get_default_identity_file,
            commands::connect_profile,
            commands::disconnect_profile,
            commands::get_connection_status
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
