use crate::models::{ConnectionEvent, ConnectionStatus, DnsMode, Profile, TunnelError, now_ms};
use crate::stats;
use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::AtomicBool;
use std::sync::{Arc, Mutex};
use tauri::{AppHandle, Emitter, Manager, State};

#[cfg(unix)]
use std::os::unix::process::CommandExt;

#[cfg(target_os = "macos")]
use crate::helper_ipc;
#[cfg(target_os = "macos")]
use std::os::unix::net::UnixStream;

pub const STATUS_EVENT: &str = "connection-status";
pub const LOG_EVENT: &str = "connection-log";
pub const CONNECTION_EVENT: &str = "tunnel-connection";
pub const ERROR_EVENT: &str = "tunnel-error";

// ── Unix: sudo wrapper via SUDO_ASKPASS ────────────────────────────────────────

/// Temporary directory holding sudo/askpass wrapper scripts for Unix privilege elevation.
/// Dropped (and thus deleted) when the tunnel disconnects.
#[cfg(unix)]
struct SudoHelper {
    dir: std::path::PathBuf,
}

#[cfg(unix)]
impl SudoHelper {
    /// Create wrapper scripts in a unique temp directory and return the helper.
    fn create() -> Option<Self> {
        use std::os::unix::fs::PermissionsExt;

        let unique = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos();
        let dir = std::env::temp_dir().join(format!("netferry-sudo-{unique}"));
        std::fs::create_dir_all(&dir).ok()?;

        // askpass: GUI dialog that echoes the entered password to stdout.
        let askpass = dir.join("netferry-askpass");
        let askpass_content = Self::askpass_script();
        std::fs::write(&askpass, askpass_content).ok()?;
        std::fs::set_permissions(&askpass, std::fs::Permissions::from_mode(0o755)).ok()?;

        // sudo wrapper: re-invokes real sudo with -A so it uses SUDO_ASKPASS.
        // The tunnel resolves `sudo` via which(), so placing our wrapper first in
        // PATH is sufficient to intercept the call.
        let sudo_wrapper = dir.join("sudo");
        std::fs::write(
            &sudo_wrapper,
            format!(
                "#!/bin/sh\nSUDO_ASKPASS=\"{}\" exec /usr/bin/sudo -A \"$@\"\n",
                askpass.display()
            ),
        )
        .ok()?;
        std::fs::set_permissions(&sudo_wrapper, std::fs::Permissions::from_mode(0o755)).ok()?;

        Some(Self { dir })
    }

    /// Platform-specific askpass script content.
    fn askpass_script() -> String {
        #[cfg(target_os = "macos")]
        {
            // macOS: use osascript to show a native password dialog.
            String::from(
                "#!/bin/sh\n\
                 /usr/bin/osascript -e '\n\
                 set dialogResult to display dialog \"NetFerry needs administrator privileges to configure network routing.\" \
                 default answer \"\" with hidden answer buttons {\"Cancel\", \"OK\"} default button \"OK\" \
                 with title \"NetFerry\" with icon caution\n\
                 return text returned of dialogResult\n\
                 ' 2>/dev/null\n",
            )
        }
        #[cfg(not(target_os = "macos"))]
        {
            // Linux: try zenity → kdialog → x11-ssh-askpass → ssh-askpass.
            String::from(
                "#!/bin/sh\n\
                 MSG=\"NetFerry needs administrator privileges to configure network routing.\"\n\
                 if command -v zenity >/dev/null 2>&1; then\n\
                   zenity --password --title=\"NetFerry\"\n\
                 elif command -v kdialog >/dev/null 2>&1; then\n\
                   kdialog --password \"$MSG\" --title \"NetFerry\"\n\
                 elif command -v x11-ssh-askpass >/dev/null 2>&1; then\n\
                   x11-ssh-askpass \"$MSG\"\n\
                 elif command -v ssh-askpass >/dev/null 2>&1; then\n\
                   ssh-askpass \"$MSG\"\n\
                 else\n\
                   echo \"NetFerry: no GUI askpass helper found\" >&2\n\
                   exit 1\n\
                 fi\n",
            )
        }
    }

    fn dir_str(&self) -> String {
        self.dir.display().to_string()
    }
}

#[cfg(unix)]
impl Drop for SudoHelper {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.dir);
    }
}

// ── Windows: UAC elevation at startup ─────────────────────────────────────────

/// On Windows, the tunnel does not implement its own privilege elevation and
/// requires the process to already be running as Administrator.  We detect
/// this early and ask the OS to re-launch the app with a UAC elevation prompt.
#[cfg(target_os = "windows")]
pub fn ensure_elevated(app: &tauri::AppHandle) {
    use std::process::Command;

    // `net session` succeeds only when the caller has admin rights.
    let is_admin = Command::new("net")
        .args(["session"])
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false);

    if !is_admin {
        if let Ok(exe) = std::env::current_exe() {
            let exe_str = exe.to_string_lossy().replace('\'', "''");
            // PowerShell's Start-Process with -Verb RunAs triggers the UAC prompt.
            let _ = Command::new("powershell")
                .args([
                    "-NoProfile",
                    "-Command",
                    &format!("Start-Process -FilePath '{}' -Verb RunAs", exe_str),
                ])
                .spawn();
        }
        // Exit the current non-elevated instance so the elevated one takes over.
        app.exit(0);
    }
}

// ── Shared application state ──────────────────────────────────────────────────

pub struct AppState {
    pub child: Mutex<Option<Child>>,
    pub status: Mutex<ConnectionStatus>,
    pub stats_stop: Mutex<Option<Arc<AtomicBool>>>,

    /// macOS 13+: live socket to the privileged helper daemon.
    /// Dropping it signals the helper to kill the tunnel process.
    #[cfg(target_os = "macos")]
    pub helper_stream: Mutex<Option<UnixStream>>,

    /// Unix: per-connection sudo askpass wrapper; cleaned up on disconnect.
    #[cfg(unix)]
    sudo_helper: Mutex<Option<SudoHelper>>,
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
            stats_stop: Mutex::new(None),
            #[cfg(target_os = "macos")]
            helper_stream: Mutex::new(None),
            #[cfg(unix)]
            sudo_helper: Mutex::new(None),
        }
    }
}

// ── PID file helpers ──────────────────────────────────────────────────────────

#[cfg(unix)]
fn pid_file_path() -> std::path::PathBuf {
    std::env::temp_dir().join("netferry-tunnel.pid")
}

/// Called at app startup: if a stale PID file exists from a previous crash/kill,
/// terminate the leftover tunnel process group and remove the file.
pub fn kill_stale_tunnel() {
    #[cfg(unix)]
    {
        let path = pid_file_path();
        if let Ok(content) = std::fs::read_to_string(&path) {
            if let Ok(pgid) = content.trim().parse::<i32>() {
                // Best-effort; the process may already be gone.
                unsafe { libc::kill(-pgid, libc::SIGKILL) };
            }
            let _ = std::fs::remove_file(&path);
        }
    }
}

// ── Internal helpers ──────────────────────────────────────────────────────────

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

fn resolve_tunnel_exe() -> String {
    // Explicit overrides via environment variable take highest priority.
    for var in &["NETFERRY_TUNNEL_BIN", "NETFERRY_SSHUTTLE_BIN", "SSHUTTLE_BIN"] {
        if let Ok(path) = std::env::var(var) {
            if !path.trim().is_empty() {
                return path;
            }
        }
    }

    // Production bundle: the sidecar is placed next to the main executable
    // (e.g. NetFerry.app/Contents/MacOS/netferry-tunnel).
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let candidate = dir.join("netferry-tunnel");
            if candidate.exists() {
                return candidate.to_string_lossy().into_owned();
            }
        }
    }

    // Dev mode (cargo build / tauri dev): the binary lives in
    // src-tauri/binaries/netferry-tunnel-<target-triple>.
    // CARGO_MANIFEST_DIR and TARGET_TRIPLE are baked in at compile time.
    #[cfg(debug_assertions)]
    {
        let candidate = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join(format!("netferry-tunnel-{}", env!("TARGET_TRIPLE")));
        if candidate.exists() {
            return candidate.to_string_lossy().into_owned();
        }
    }

    // Last resort: hope it is available on PATH.
    "netferry-tunnel".to_string()
}

fn build_args(profile: &Profile) -> Vec<String> {
    let mut args: Vec<String> = Vec::new();

    // Verbose: tunnel logs individual connections to stderr (parsed by this process).
    args.push("-v".to_string());

    args.push("--remote".to_string());
    args.push(profile.remote.clone());

    // Subnets are positional arguments.
    for subnet in &profile.subnets {
        if !subnet.trim().is_empty() {
            args.push(subnet.clone());
        }
    }

    if profile.auto_nets {
        args.push("--auto-nets".to_string());
    }
    if !profile.identity_file.trim().is_empty() {
        args.push("--identity".to_string());
        args.push(profile.identity_file.clone());
    }
    if !profile.method.trim().is_empty() {
        args.push("--method".to_string());
        args.push(profile.method.clone());
    }
    if profile.disable_ipv6 {
        args.push("--no-ipv6".to_string());
    }
    match profile.dns {
        DnsMode::Off => {}
        DnsMode::All | DnsMode::Specific => args.push("--dns".to_string()),
    }
    if let Some(to_ns) = &profile.dns_target {
        if !to_ns.trim().is_empty() {
            args.push("--dns-target".to_string());
            args.push(to_ns.clone());
        }
    }
    if let Some(extra) = &profile.extra_ssh_options {
        if !extra.trim().is_empty() {
            args.push("--extra-ssh-opts".to_string());
            args.push(extra.clone());
        }
    }
    args
}

/// Parse a tunnel verbose "Accept TCP" line into a ConnectionEvent.
/// Expected format: "c : Accept TCP: <src_ip>:<src_port> -> <dst_ip>:<dst_port>."
fn parse_connection_event(line: &str) -> Option<ConnectionEvent> {
    let after = line.split("Accept TCP:").nth(1)?.trim();
    let after = after.trim_end_matches('.');
    let mut parts = after.splitn(2, " -> ");
    let src = parts.next()?.trim().to_string();
    let dst = parts.next()?.trim().to_string();
    Some(ConnectionEvent {
        src_addr: src,
        dst_addr: dst,
        timestamp_ms: now_ms(),
    })
}

/// Returns true if the line looks like an error or warning from the tunnel/SSH.
fn is_error_line(line: &str) -> bool {
    const KEYWORDS: &[&str] = &[
        "fatal:",
        "warning:",
        "connection refused",
        "connection reset",
        "no route to host",
        "ssh: connect to host",
        "permission denied",
        "host key verification failed",
    ];
    let lower = line.to_lowercase();
    KEYWORDS.iter().any(|kw| lower.contains(kw))
}

// ── macOS: helper-IPC event thread ────────────────────────────────────────────

/// Spawn a thread that reads JSON events from the helper socket and translates
/// them into the same Tauri events that the direct-child path emits.
/// The thread exits when the stream is closed (by either side).
#[cfg(target_os = "macos")]
fn spawn_helper_event_thread(
    app: AppHandle,
    stream: UnixStream,
    profile_id: String,
) {
    std::thread::spawn(move || {
        let reader = BufReader::new(stream);
        let mut tunnel_connected = false;

        for line in reader.lines().map_while(Result::ok) {
            let ev: serde_json::Value = match serde_json::from_str(&line) {
                Ok(v) => v,
                Err(_) => continue,
            };

            match ev["type"].as_str() {
                Some("started") => {
                    // Start bandwidth stats once we know the tunnel PID.
                    if let Some(pid) = ev["pid"].as_u64() {
                        let stop_flag =
                            stats::start_stats_monitoring(app.clone(), pid as u32);
                        if let Ok(mut g) = app.state::<AppState>().stats_stop.lock() {
                            *g = Some(stop_flag);
                        }
                    }
                }

                Some("log") => {
                    let stream_name = ev["stream"].as_str().unwrap_or("stderr");
                    let log_line = ev["line"].as_str().unwrap_or("").to_string();

                    if !tunnel_connected && log_line.contains("Connected to server.") {
                        tunnel_connected = true;
                        let status = ConnectionStatus {
                            state: "connected".to_string(),
                            profile_id: Some(profile_id.clone()),
                            message: Some("Tunnel established".to_string()),
                        };
                        let _ = app.emit(STATUS_EVENT, status.clone());
                        if let Ok(mut g) = app.state::<AppState>().status.lock() {
                            *g = status;
                        }
                        let _ = app.emit(LOG_EVENT, format!("{stream_name}: {log_line}"));
                        continue;
                    }

                    if log_line.contains("Accept TCP:") {
                        if let Some(event) = parse_connection_event(&log_line) {
                            let _ = app.emit(CONNECTION_EVENT, event);
                        }
                        continue;
                    }

                    if is_error_line(&log_line) {
                        let error = TunnelError {
                            message: log_line.clone(),
                            timestamp_ms: now_ms(),
                        };
                        let _ = app.emit(ERROR_EVENT, error);
                        let _ = app.emit(LOG_EVENT, format!("{stream_name}: {log_line}"));
                        continue;
                    }

                    let _ = app.emit(LOG_EVENT, format!("{stream_name}: {log_line}"));
                }

                Some("error") => {
                    let msg = ev["message"].as_str().unwrap_or("unknown error").to_string();
                    let _ = app.emit(LOG_EVENT, format!("helper: {msg}"));
                }

                // "exit" or stream EOF: tunnel process has ended.
                _ => {}
            }
        }

        // ── Cleanup after stream closes ──────────────────────────────────────

        if let Ok(mut h) = app.state::<AppState>().helper_stream.lock() {
            *h = None;
        }

        if let Ok(mut flag_guard) = app.state::<AppState>().stats_stop.lock() {
            if let Some(flag) = flag_guard.take() {
                stats::stop_stats_monitoring(&flag);
            }
        }

        let current = app
            .state::<AppState>()
            .status
            .lock()
            .map(|g| g.state.clone())
            .unwrap_or_default();
        if current == "connecting" || current == "connected" {
            let status = ConnectionStatus {
                state: "error".to_string(),
                profile_id: None,
                message: Some("Tunnel process exited unexpectedly".to_string()),
            };
            let _ = app.emit(STATUS_EVENT, status.clone());
            if let Ok(mut g) = app.state::<AppState>().status.lock() {
                *g = status;
            }
        }
    });
}

// ── connect() ─────────────────────────────────────────────────────────────────

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
    #[cfg(target_os = "macos")]
    {
        let h = state
            .helper_stream
            .lock()
            .map_err(|_| "helper_stream lock is poisoned".to_string())?;
        if h.is_some() {
            return Err("A connection is already running".to_string());
        }
    }

    let binary = resolve_tunnel_exe();

    // ── macOS 13+: route through the privileged helper daemon ─────────────────
    // On success the helper manages the tunnel process (running as root) and we
    // communicate via a Unix socket — no per-connection sudo prompt needed.
    // Falls back to the sudo+askpass path on macOS ≤ 12.
    #[cfg(target_os = "macos")]
    match helper_ipc::ensure_helper_running() {
        Ok(true) => {
            // The Go tunnel reads SSH config natively and receives HOME/USER/SSH_AUTH_SOCK
            // from the helper's env injection — no SSH wrapper script needed.
            let args = build_args(&profile);
            let stream = helper_ipc::start_tunnel(&binary, &args)
                .map_err(|e| format!("Helper IPC: {e}"))?;
            // Give the event thread a cloned read end; keep the original for
            // disconnect (dropping it signals the helper to kill the tunnel).
            let read_stream = stream
                .try_clone()
                .map_err(|e| format!("Socket clone: {e}"))?;
            spawn_helper_event_thread(app.clone(), read_stream, profile.id.clone());
            {
                let mut h = state
                    .helper_stream
                    .lock()
                    .map_err(|_| "helper_stream lock is poisoned".to_string())?;
                *h = Some(stream);
            }
            let status = ConnectionStatus {
                state: "connecting".to_string(),
                profile_id: Some(profile.id),
                message: Some("Starting tunnel\u{2026}".to_string()),
            };
            return set_status(&app, &state, status.clone()).map(|_| status);
        }
        Ok(false) => {
            // macOS < 13 or helper not available → fall through to sudo+askpass.
        }
        Err(e) => return Err(e),
    }

    let args = build_args(&profile);
    let mut cmd = Command::new(binary);
    cmd.args(args).stdout(Stdio::piped()).stderr(Stdio::piped());

    // The Tauri app has no controlling TTY, so sudo cannot prompt for a password
    // the usual way.  We inject a thin wrapper named "sudo" at the front of PATH
    // that re-invokes real sudo with -A, combined with an OS-appropriate
    // SUDO_ASKPASS helper (osascript dialog on macOS, zenity/kdialog on Linux).
    #[cfg(unix)]
    {
        if let Some(helper) = SudoHelper::create() {
            let current_path = std::env::var("PATH").unwrap_or_default();
            cmd.env("PATH", format!("{}:{current_path}", helper.dir_str()));
            let mut h = state
                .sudo_helper
                .lock()
                .map_err(|_| "sudo_helper lock is poisoned".to_string())?;
            *h = Some(helper);
        }
        // Put the tunnel process in its own process group so we can kill the
        // whole group on disconnect (the tunnel spawns SSH and other children).
        cmd.process_group(0);
    }


    let mut child = cmd
        .spawn()
        .map_err(|e| format!("Failed to start tunnel process: {e}"))?;

    // Record the process group ID so we can terminate any orphaned tunnel
    // processes if the app is killed (SIGKILL) before a clean disconnect.
    #[cfg(unix)]
    {
        let pgid = child.id();
        let _ = std::fs::write(pid_file_path(), pgid.to_string());
    }

    let stdout = child.stdout.take();
    let stderr = child.stderr.take();
    let child_pid = child.id();

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
        let profile_id = profile.id.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(err);
            let mut tunnel_connected = false;
            for line in reader.lines().map_while(Result::ok) {
                if !tunnel_connected && line.contains("Connected to server.") {
                    tunnel_connected = true;
                    let status = ConnectionStatus {
                        state: "connected".to_string(),
                        profile_id: Some(profile_id.clone()),
                        message: Some("Tunnel established".to_string()),
                    };
                    let _ = app_clone.emit(STATUS_EVENT, status.clone());
                    if let Ok(mut g) = app_clone.state::<AppState>().status.lock() {
                        *g = status;
                    }
                    let _ = app_clone.emit(LOG_EVENT, format!("stderr: {line}"));
                    continue;
                }

                if line.contains("Accept TCP:") {
                    if let Some(event) = parse_connection_event(&line) {
                        let _ = app_clone.emit(CONNECTION_EVENT, event);
                    }
                    continue;
                }

                if is_error_line(&line) {
                    let error = TunnelError {
                        message: line.clone(),
                        timestamp_ms: now_ms(),
                    };
                    let _ = app_clone.emit(ERROR_EVENT, error);
                    let _ = app_clone.emit(LOG_EVENT, format!("stderr: {line}"));
                    continue;
                }

                let _ = app_clone.emit(LOG_EVENT, format!("stderr: {line}"));
            }

            // stderr EOF means the process has exited.  Clear state.child so
            // that the next connect() can run.
            if let Ok(mut g) = app_clone.state::<AppState>().child.lock() {
                if let Some(mut c) = g.take() {
                    let _ = c.wait();
                }
            }

            // Stop bandwidth monitoring.
            if let Ok(mut flag_guard) = app_clone.state::<AppState>().stats_stop.lock() {
                if let Some(flag) = flag_guard.take() {
                    stats::stop_stats_monitoring(&flag);
                }
            }

            let current = app_clone
                .state::<AppState>()
                .status
                .lock()
                .map(|g| g.state.clone())
                .unwrap_or_default();
            if current == "connecting" || current == "connected" {
                let status = ConnectionStatus {
                    state: "error".to_string(),
                    profile_id: None,
                    message: Some("Tunnel process exited unexpectedly".to_string()),
                };
                let _ = app_clone.emit(STATUS_EVENT, status.clone());
                if let Ok(mut g) = app_clone.state::<AppState>().status.lock() {
                    *g = status;
                }
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

    // Start bandwidth monitoring in a background thread.
    let stop_flag = stats::start_stats_monitoring(app.clone(), child_pid);
    if let Ok(mut flag_guard) = state.stats_stop.lock() {
        *flag_guard = Some(stop_flag);
    }

    let status = ConnectionStatus {
        state: "connecting".to_string(),
        profile_id: Some(profile.id),
        message: Some("Starting tunnel…".to_string()),
    };
    set_status(&app, &state, status.clone())?;
    Ok(status)
}

// ── disconnect() ──────────────────────────────────────────────────────────────

pub fn disconnect(app: AppHandle, state: State<'_, AppState>) -> Result<ConnectionStatus, String> {
    // ── macOS: close the helper socket ────────────────────────────────────────
    // Dropping the socket signals the helper to kill the tunnel process group.
    // Pre-set our status to "disconnected" so the event thread (which also
    // watches for stream EOF) does not emit a spurious "error" event.
    #[cfg(target_os = "macos")]
    if let Ok(mut h) = state.helper_stream.lock() {
        if h.is_some() {
            if let Ok(mut g) = state.status.lock() {
                g.state = "disconnected".to_string();
            }
            *h = None;
        }
    }

    let mut lock = state
        .child
        .lock()
        .map_err(|_| "Process lock is poisoned".to_string())?;
    if let Some(mut child) = lock.take() {
        #[cfg(unix)]
        {
            // Kill the whole process group so the tunnel's child processes (e.g. SSH)
            // are also terminated; otherwise they would remain as orphans.
            let pid = child.id() as i32;
            let _ = unsafe { libc::kill(-pid, libc::SIGKILL) };
            // Clean up the PID file now that we've terminated the process group.
            let _ = std::fs::remove_file(pid_file_path());
        }
        #[cfg(not(unix))]
        {
            let _ = child.kill();
        }
        let _ = child.wait();
    }
    drop(lock);

    // Stop bandwidth monitoring thread.
    if let Ok(mut flag_guard) = state.stats_stop.lock() {
        if let Some(flag) = flag_guard.take() {
            stats::stop_stats_monitoring(&flag);
        }
    }

    // Release the sudo helper temp directory now that the tunnel is gone.
    #[cfg(unix)]
    {
        if let Ok(mut h) = state.sudo_helper.lock() {
            *h = None;
        }
    }

    let status = ConnectionStatus {
        state: "disconnected".to_string(),
        profile_id: None,
        message: Some("Disconnected".to_string()),
    };
    set_status(&app, &state, status.clone())?;
    Ok(status)
}

// ── current_status() ──────────────────────────────────────────────────────────

pub fn current_status(state: State<'_, AppState>) -> Result<ConnectionStatus, String> {
    let guard = state
        .status
        .lock()
        .map_err(|_| "Status lock is poisoned".to_string())?;
    Ok(guard.clone())
}
