mod commands;
mod models;
mod profiles;
mod sidecar;
mod ssh_config;
mod tray;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .manage(sidecar::AppState::new())
        .setup(|app| {
            tray::setup_tray(app.handle())?;
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_profiles,
            commands::save_profile,
            commands::delete_profile,
            commands::import_ssh_hosts,
            commands::connect_profile,
            commands::disconnect_profile,
            commands::get_connection_status
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
