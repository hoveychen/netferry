mod commands;
#[cfg(target_os = "macos")]
mod helper_ipc;
mod models;
mod profiles;
mod settings;
mod sidecar;
mod ssh_config;
mod stats;
mod tray;

#[cfg(unix)]
extern crate libc;

use tauri::{Listener, Manager};

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

            // macOS 13+: register the privileged helper daemon via SMAppService.
            // On first launch this shows the native one-time authorisation dialog.
            // The call is idempotent on subsequent launches (returns immediately).
            #[cfg(target_os = "macos")]
            {
                let handle = app.handle().clone();
                std::thread::spawn(move || {
                    if let Err(e) = helper_ipc::ensure_helper_running() {
                        // Non-fatal: we fall back to sudo+askpass on connect.
                        eprintln!("NetFerry: helper registration: {e}");
                        let _ = handle; // keep handle alive
                    }
                });
            }

            // Kill any sshuttle process group left over from a previous crash or
            // force-quit (the PID file records the PGID written at connect time).
            sidecar::kill_stale_tunnel();

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
            commands::get_connection_status,
            commands::get_global_settings,
            commands::save_global_settings
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app_handle, event| {
            // On graceful exit (Cmd+Q, window close, tray quit), ensure the
            // sshuttle process group is terminated and the PID file is removed.
            if let tauri::RunEvent::Exit = event {
                let state = app_handle.state::<sidecar::AppState>();
                let mut lock = state.child.lock().unwrap_or_else(|e| e.into_inner());
                if let Some(mut child) = lock.take() {
                    #[cfg(unix)]
                    {
                        let pid = child.id() as i32;
                        let _ = unsafe { libc::kill(-pid, libc::SIGKILL) };
                        let _ = std::fs::remove_file(
                            std::env::temp_dir().join("netferry-tunnel.pid"),
                        );
                    }
                    #[cfg(not(unix))]
                    {
                        let _ = child.kill();
                    }
                    let _ = child.wait();
                }
            }
        });
}
