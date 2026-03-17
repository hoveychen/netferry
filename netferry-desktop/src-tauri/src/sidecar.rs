use crate::models::{ConnectionStatus, DnsMode, Profile};
use std::io::{BufRead, BufReader};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, Manager, State};

#[cfg(unix)]
use std::os::unix::process::CommandExt;

pub const STATUS_EVENT: &str = "connection-status";
pub const LOG_EVENT: &str = "connection-log";

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
        // macOS uses osascript; Linux tries zenity → kdialog → ssh-askpass in order.
        let askpass = dir.join("netferry-askpass");
        #[cfg(target_os = "macos")]
        let askpass_content = String::from(
            "#!/bin/sh\n\
             /usr/bin/osascript \\\n\
             -e 'Tell application \"System Events\" to display dialog \
                 \"NetFerry needs administrator privileges to configure network routing.\" \
                 default answer \"\" with hidden answer with title \"NetFerry\"' \\\n\
             -e 'text returned of result'\n",
        );
        #[cfg(not(target_os = "macos"))]
        let askpass_content = String::from(
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
        );
        std::fs::write(&askpass, askpass_content).ok()?;
        std::fs::set_permissions(&askpass, std::fs::Permissions::from_mode(0o755)).ok()?;

        // sudo wrapper: re-invokes real sudo with -A so it uses SUDO_ASKPASS.
        // sshuttle resolves `sudo` via which(), so placing our wrapper first in
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

/// On Windows, sshuttle does not implement its own privilege elevation and
/// requires the process to already be running as Administrator.  We detect
/// this early and ask the OS to re-launch the app with a UAC elevation prompt.
#[cfg(target_os = "windows")]
pub fn ensure_elevated(app: &tauri::AppHandle) {
    use std::process::Command;

    // `net session` succeeds only when the caller has admin rights.
    let is_admin = Command::new("net")
        .args(["session"])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
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

pub struct AppState {
    pub child: Mutex<Option<Child>>,
    pub status: Mutex<ConnectionStatus>,
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
            #[cfg(unix)]
            sudo_helper: Mutex::new(None),
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

    let mut cmd = Command::new(binary);
    cmd.args(args).stdout(Stdio::piped()).stderr(Stdio::piped());

    // On Unix (macOS and Linux) the Tauri app has no controlling TTY, so sudo
    // cannot prompt for a password the usual way.  We inject a thin wrapper
    // named "sudo" at the front of PATH that re-invokes real sudo with -A,
    // combined with an OS-appropriate SUDO_ASKPASS helper (osascript on macOS,
    // zenity/kdialog/ssh-askpass on Linux).
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
        // whole group on disconnect (sshuttle spawns SSH and other children).
        cmd.process_group(0);
    }

    let mut child = cmd
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
        let profile_id = profile.id.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(err);
            let mut tunnel_connected = false;
            for line in reader.lines().map_while(Result::ok) {
                let _ = app_clone.emit(LOG_EVENT, format!("stderr: {line}"));

                // sshuttle prints "Connected to server." once the SSH tunnel is
                // established and the remote helper is ready.  Use this as the
                // signal to transition from "connecting" → "connected".
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
                }
            }

            // stderr EOF means the process has exited.  Clear state.child so
            // that the next connect() can run (disconnect() may have already
            // taken it; if not, we take and reap here).
            if let Ok(mut g) = app_clone.state::<AppState>().child.lock() {
                if let Some(mut c) = g.take() {
                    let _ = c.wait();
                }
            }

            // Only emit an error if we hadn't already seen a clean "connected"
            // state transition, or if the user hasn't triggered an intentional
            // disconnect (in which case disconnect() will have already set the status).
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

    // Return "connecting" – the stderr watcher thread above will emit
    // "connected" once sshuttle reports it is ready.
    let status = ConnectionStatus {
        state: "connecting".to_string(),
        profile_id: Some(profile.id),
        message: Some("Starting tunnel…".to_string()),
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
        #[cfg(unix)]
        {
            // Kill the whole process group so sshuttle's child processes (e.g. SSH)
            // are also terminated; otherwise they would remain as orphans.
            let pid = child.id() as i32;
            let _ = unsafe { libc::kill(-pid, libc::SIGKILL) };
        }
        #[cfg(not(unix))]
        {
            let _ = child.kill();
        }
        let _ = child.wait();
    }
    drop(lock);

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

pub fn current_status(state: State<'_, AppState>) -> Result<ConnectionStatus, String> {
    let guard = state
        .status
        .lock()
        .map_err(|_| "Status lock is poisoned".to_string())?;
    Ok(guard.clone())
}
