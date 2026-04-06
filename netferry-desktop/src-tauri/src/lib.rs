mod commands;
mod crypto;
#[cfg(target_os = "macos")]
mod helper_ipc;
pub mod logging;
mod menu;
mod models;
mod priorities;
mod profiles;
mod settings;
mod sidecar;
mod ssh_config;
mod stats;
mod tray;

#[cfg(unix)]
extern crate libc;

use tauri::{Emitter, Listener, Manager};
use tauri_plugin_deep_link::DeepLinkExt;

/// Emit "import-profile-file" events for any .nfprofile paths found in an
/// argument list (used by the single-instance plugin on Windows/Linux).
fn emit_nfprofile_paths<'a>(app: &tauri::AppHandle, args: impl Iterator<Item = &'a str>) {
    for arg in args {
        let path = std::path::Path::new(arg);
        if path.extension().is_some_and(|ext| ext == "nfprofile") {
            let _ = app.emit("import-profile-file", arg.to_string());
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(
            tauri_plugin_log::Builder::new()
                .target(tauri_plugin_log::Target::new(
                    tauri_plugin_log::TargetKind::Stdout,
                ))
                .target(tauri_plugin_log::Target::new(
                    tauri_plugin_log::TargetKind::LogDir {
                        file_name: Some("netferry.log".into()),
                    },
                ))
                .max_file_size(1_000_000) // 1 MB per log file
                .rotation_strategy(tauri_plugin_log::RotationStrategy::KeepOne)
                .level(log::LevelFilter::Debug)
                .build(),
        )
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_deep_link::init())
        .plugin(tauri_plugin_single_instance::init(|app, argv, _cwd| {
            // Windows/Linux: when a second instance is spawned (e.g. double-clicking
            // a .nfprofile file), the single-instance plugin forwards the argv here.
            emit_nfprofile_paths(app, argv.iter().map(|s| s.as_str()));
        }))
        .manage(sidecar::AppState::new())
        .setup(|app| {
            // On Windows, the tunnel cannot self-elevate, so the whole app must
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
                        log::warn!("Helper registration failed (will fall back to sudo): {e}");
                        let _ = handle; // keep handle alive
                    }
                });
            }

            // macOS vibrancy (frosted glass)
            #[cfg(target_os = "macos")]
            {
                use tauri::window::{Color, Effect, EffectState, EffectsBuilder};
                use tauri::Manager;
                if let Some(window) = app.get_webview_window("main") {
                    let _ = window.set_background_color(Some(Color(0, 0, 0, 0)));
                    let effects = EffectsBuilder::new()
                        .effect(Effect::Sidebar)
                        .state(EffectState::FollowsWindowActiveState)
                        .build();
                    let _ = window.set_effects(effects);
                }
            }

            // Kill any tunnel process group left over from a previous crash or
            // force-quit (the PID file records the PGID written at connect time).
            sidecar::kill_stale_tunnel();

            tray::setup_tray(app.handle())?;
            menu::setup_menu(app.handle())?;

            // Handle .nfprofile files opened while the app is already running.
            // On macOS this fires for double-click / Finder "Open With";
            // on Windows/Linux the single-instance plugin above handles it instead.
            let handle = app.handle().clone();
            app.deep_link().on_open_url(move |event| {
                for url in event.urls() {
                    if let Ok(path) = url.to_file_path() {
                        if path.extension().is_some_and(|ext| ext == "nfprofile") {
                            let _ = handle.emit("import-profile-file", path.to_string_lossy().into_owned());
                        }
                    }
                }
            });

            // On Linux (and debug-mode Windows) the installer doesn't register
            // file associations at OS level, so register at runtime.
            #[cfg(any(target_os = "linux", all(debug_assertions, windows)))]
            app.deep_link().register_all()?;

            // Rebuild tray menu and update tooltip whenever connection status changes.
            let app_handle = app.handle().clone();
            app.listen(sidecar::STATUS_EVENT, move |event| {
                tray::rebuild_tray_menu(&app_handle);
                menu::rebuild_app_menu(&app_handle);
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
                    if matches!(status.state.as_str(), "disconnected" | "error") {
                        tray::update_tray_title(&app_handle, None);
                    }
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
            commands::save_global_settings,
            commands::get_priorities,
            commands::save_priorities,
            commands::get_routes,
            commands::save_routes,
            commands::lookup_geoip,
            commands::get_stats_url,
            commands::list_method_features,
            commands::update_tray_info,
            commands::export_profile,
            commands::export_profile_to_file,
            commands::import_profile,
            commands::import_profile_from_file,
            commands::get_helper_status,
            commands::register_helper,
            commands::set_window_theme,
            commands::get_app_version,
            commands::check_for_update
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app_handle, event| {
            // On graceful exit (Cmd+Q, window close, tray quit), ensure the
            // tunnel process group is terminated and the PID file is removed.
            if let tauri::RunEvent::Exit = event {
                let state = app_handle.state::<sidecar::AppState>();

                // macOS: signal the helper to kill the tunnel by writing a
                // disconnect byte and closing the socket.
                #[cfg(target_os = "macos")]
                if let Ok(mut h) = state.helper_stream.lock() {
                    if let Some(ref stream) = *h {
                        use std::io::Write;
                        let _ = (&*stream).write_all(b"q");
                        let _ = (&*stream).flush();
                        let _ = stream.shutdown(std::net::Shutdown::Both);
                        *h = None;
                    }
                }

                let mut lock = state.child.lock().unwrap_or_else(|e| e.into_inner());
                if let Some(mut child) = lock.take() {
                    #[cfg(unix)]
                    {
                        // Send SIGTERM first so the tunnel can run fw.Restore()
                        // to clean up pf rules. SIGKILL skips cleanup and leaves
                        // stale redirect rules that break networking system-wide.
                        let pid = child.id() as i32;
                        let _ = unsafe { libc::kill(-pid, libc::SIGTERM) };
                        let exited = sidecar::wait_child_with_timeout(
                            &mut child,
                            std::time::Duration::from_secs(3),
                        );
                        if !exited {
                            let _ = unsafe { libc::kill(-pid, libc::SIGKILL) };
                        }
                        let _ = child.wait();
                        let _ = std::fs::remove_file(
                            std::env::temp_dir().join("netferry-tunnel.pid"),
                        );
                    }
                    #[cfg(not(unix))]
                    {
                        let _ = child.kill();
                        let _ = child.wait();
                    }
                }
                drop(lock);

                // Safety net: remove any stale pf anchors left behind if
                // the tunnel didn't clean up in time (e.g. SIGKILL path).
                #[cfg(target_os = "macos")]
                sidecar::clean_stale_pf_anchors_public();
            }
        });
}
