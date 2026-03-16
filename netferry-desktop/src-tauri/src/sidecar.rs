use crate::models::{ConnectionStatus, DnsMode, Profile};
use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, State};

pub const STATUS_EVENT: &str = "connection-status";
pub const LOG_EVENT: &str = "connection-log";

pub struct AppState {
    pub child: Mutex<Option<Child>>,
    pub status: Mutex<ConnectionStatus>,
}

impl AppState {
    pub fn new() -> Self {
        Self {
            child: Mutex::new(None),
            status: Mutex::new(ConnectionStatus {
                state: "disconnected".to_string(),
                profile_id: None,
                message: None,
            }),
        }
    }
}

fn emit_status(app: &AppHandle, status: &ConnectionStatus) {
    let _ = app.emit(STATUS_EVENT, status.clone());
}

fn set_status(
    app: &AppHandle,
    state: &State<'_, AppState>,
    status: ConnectionStatus,
) -> Result<(), String> {
    let mut guard = state
        .status
        .lock()
        .map_err(|_| "Status lock is poisoned".to_string())?;
    *guard = status.clone();
    drop(guard);
    emit_status(app, &status);
    Ok(())
}

fn resolve_sshuttle_exe() -> String {
    if let Ok(path) = std::env::var("NETFERRY_TUNNEL_BIN") {
        if !path.trim().is_empty() {
            return path;
        }
    }
    if let Ok(path) = std::env::var("NETFERRY_SSHUTTLE_BIN") {
        if !path.trim().is_empty() {
            return path;
        }
    }
    if let Ok(path) = std::env::var("SSHUTTLE_BIN") {
        if !path.trim().is_empty() {
            return path;
        }
    }
    "netferry-tunnel".to_string()
}

fn build_args(profile: &Profile) -> Vec<String> {
    let mut args: Vec<String> = Vec::new();
    let mut ssh_cmd_parts: Vec<String> = vec!["ssh".to_string()];
    args.push("--remote".to_string());
    args.push(profile.remote.clone());

    for subnet in &profile.subnets {
        if !subnet.trim().is_empty() {
            args.push(subnet.clone());
        }
    }
    if profile.auto_nets {
        args.push("--auto-nets".to_string());
    }
    for subnet in &profile.exclude_subnets {
        if !subnet.trim().is_empty() {
            args.push("--exclude".to_string());
            args.push(subnet.clone());
        }
    }
    if !profile.identity_file.trim().is_empty() {
        ssh_cmd_parts.push("-i".to_string());
        ssh_cmd_parts.push(profile.identity_file.clone());
    }
    if !profile.method.trim().is_empty() {
        args.push("--method".to_string());
        args.push(profile.method.clone());
    }
    if profile.disable_ipv6 {
        args.push("--disable-ipv6".to_string());
    }
    if let Some(py) = &profile.remote_python {
        if !py.trim().is_empty() {
            args.push("--python".to_string());
            args.push(py.clone());
        }
    }
    match profile.dns {
        DnsMode::Off => {}
        DnsMode::All | DnsMode::Specific => args.push("--dns".to_string()),
    }
    if let Some(to_ns) = &profile.dns_target {
        if !to_ns.trim().is_empty() {
            args.push("--to-ns".to_string());
            args.push(to_ns.clone());
        }
    }
    if let Some(extra) = &profile.extra_ssh_options {
        if !extra.trim().is_empty() {
            ssh_cmd_parts.push(extra.clone());
        }
    }
    args.push("--ssh-cmd".to_string());
    args.push(ssh_cmd_parts.join(" "));
    args
}

pub fn connect(
    app: AppHandle,
    state: State<'_, AppState>,
    profile: Profile,
) -> Result<ConnectionStatus, String> {
    {
        let lock = state
            .child
            .lock()
            .map_err(|_| "Process lock is poisoned".to_string())?;
        if lock.is_some() {
            return Err("A connection is already running".to_string());
        }
    }

    let binary = resolve_sshuttle_exe();
    let args = build_args(&profile);
    let mut child = Command::new(binary)
        .args(args)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| format!("Failed to start tunnel process: {e}"))?;

    let stdout = child.stdout.take();
    let stderr = child.stderr.take();

    if let Some(out) = stdout {
        let app_clone = app.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(out);
            for line in reader.lines().map_while(Result::ok) {
                let _ = app_clone.emit(LOG_EVENT, format!("stdout: {line}"));
            }
        });
    }

    if let Some(err) = stderr {
        let app_clone = app.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(err);
            for line in reader.lines().map_while(Result::ok) {
                let _ = app_clone.emit(LOG_EVENT, format!("stderr: {line}"));
            }
        });
    }

    {
        let mut lock = state
            .child
            .lock()
            .map_err(|_| "Process lock is poisoned".to_string())?;
        *lock = Some(child);
    }

    let status = ConnectionStatus {
        state: "connected".to_string(),
        profile_id: Some(profile.id),
        message: Some("NetFerry tunnel started".to_string()),
    };
    set_status(&app, &state, status.clone())?;
    Ok(status)
}

pub fn disconnect(app: AppHandle, state: State<'_, AppState>) -> Result<ConnectionStatus, String> {
    let mut lock = state
        .child
        .lock()
        .map_err(|_| "Process lock is poisoned".to_string())?;
    if let Some(mut child) = lock.take() {
        let _ = child.kill();
        let _ = child.wait();
    }
    drop(lock);

    let status = ConnectionStatus {
        state: "disconnected".to_string(),
        profile_id: None,
        message: Some("Disconnected".to_string()),
    };
    set_status(&app, &state, status.clone())?;
    Ok(status)
}

pub fn current_status(state: State<'_, AppState>) -> Result<ConnectionStatus, String> {
    let guard = state
        .status
        .lock()
        .map_err(|_| "Status lock is poisoned".to_string())?;
    Ok(guard.clone())
}
