use crate::models::{ConnectionStatus, DnsMode, Profile, TunnelError, now_ms};
use std::collections::HashMap;
use std::io::{BufRead, BufReader};
use std::net::ToSocketAddrs;
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, OnceLock};
use tauri::{AppHandle, Emitter, Manager, State};

#[cfg(unix)]
use std::os::unix::process::CommandExt;

#[cfg(target_os = "macos")]
use crate::helper_ipc;
#[cfg(target_os = "macos")]
use std::os::unix::net::UnixStream;

pub const STATUS_EVENT: &str = "connection-status";
pub const LOG_EVENT: &str = "connection-log";
pub const ERROR_EVENT: &str = "tunnel-error";
pub const STATS_PORT_EVENT: &str = "stats-port";
pub const DEPLOY_PROGRESS_EVENT: &str = "deploy-progress";
pub const DEPLOY_REASON_EVENT: &str = "deploy-reason";

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
    use std::os::windows::process::CommandExt;
    use std::process::Command;

    const CREATE_NO_WINDOW: u32 = 0x08000000;

    // `net session` succeeds only when the caller has admin rights.
    let is_admin = Command::new("net")
        .args(["session"])
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .creation_flags(CREATE_NO_WINDOW)
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
                .creation_flags(CREATE_NO_WINDOW)
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
    pub stats_port: Mutex<Option<u16>>,

    /// Cached tunnel/engine version string, populated on first read.
    pub tunnel_version: OnceLock<String>,

    /// macOS 13+: live socket to the privileged helper daemon.
    /// Dropping it signals the helper to kill the tunnel process.
    #[cfg(target_os = "macos")]
    pub helper_stream: Mutex<Option<UnixStream>>,

    /// Unix: per-connection sudo askpass wrapper; cleaned up on disconnect.
    #[cfg(unix)]
    sudo_helper: Mutex<Option<SudoHelper>>,

    /// The profile that was last successfully connected, used for auto-reconnect.
    pub last_connected_profile: Mutex<Option<Profile>>,
    /// Set to true to cancel an in-progress reconnection loop.
    pub reconnect_cancel: Mutex<Option<Arc<AtomicBool>>>,
    /// Cached resolved address of the SSH server.  Used by the reconnect loop
    /// so that the reachability check does not depend on DNS (which may be
    /// broken while old firewall rules redirect DNS to the dead proxy port).
    pub resolved_remote_addr: Mutex<Option<std::net::SocketAddr>>,
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
            stats_port: Mutex::new(None),
            tunnel_version: OnceLock::new(),
            #[cfg(target_os = "macos")]
            helper_stream: Mutex::new(None),
            #[cfg(unix)]
            sudo_helper: Mutex::new(None),
            last_connected_profile: Mutex::new(None),
            reconnect_cancel: Mutex::new(None),
            resolved_remote_addr: Mutex::new(None),
        }
    }

    /// Returns the tunnel/engine version, querying `netferry-tunnel --version`
    /// once on first call and caching the result for the rest of the process
    /// lifetime. Falls back to `"unknown"` on query failure.
    pub fn tunnel_version(&self) -> &str {
        self.tunnel_version
            .get_or_init(|| query_tunnel_version().unwrap_or_else(|_| "unknown".into()))
    }
}

/// Runs `netferry-tunnel --version` and returns the version string.
pub fn query_tunnel_version() -> Result<String, String> {
    let binary = resolve_tunnel_exe();
    let mut cmd = Command::new(&binary);
    cmd.arg("--version")
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    let output = cmd
        .output()
        .map_err(|e| format!("Failed to run tunnel binary: {e}"))?;
    if !output.status.success() {
        return Err(format!(
            "tunnel --version exited with {}",
            output.status
        ));
    }
    Ok(String::from_utf8_lossy(&output.stdout).trim().to_string())
}

/// Runs `netferry-tunnel --list-features` and returns the parsed JSON.
pub fn query_method_features() -> Result<HashMap<String, Vec<String>>, String> {
    let binary = resolve_tunnel_exe();
    let mut cmd = Command::new(&binary);
    cmd.arg("--list-features")
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }
    let output = cmd
        .output()
        .map_err(|e| format!("Failed to run tunnel binary: {e}"))?;
    if !output.status.success() {
        return Err(format!(
            "tunnel --list-features exited with {}",
            output.status
        ));
    }
    serde_json::from_slice(&output.stdout)
        .map_err(|e| format!("Failed to parse features JSON: {e}"))
}

/// Returns the stats server URL if available.
pub fn get_stats_url(state: State<'_, AppState>) -> Option<String> {
    state.stats_port.lock().ok()?.map(|p| format!("http://127.0.0.1:{p}"))
}

// ── PID file helpers ──────────────────────────────────────────────────────────

#[cfg(unix)]
fn pid_file_path() -> std::path::PathBuf {
    std::env::temp_dir().join("netferry-tunnel.pid")
}

/// Called at app startup: if a stale PID file exists from a previous crash/kill,
/// terminate the leftover tunnel process group and remove the file.
/// Also cleans up any stale pf anchors on macOS.
pub fn kill_stale_tunnel() {
    #[cfg(unix)]
    {
        let path = pid_file_path();
        if let Ok(content) = std::fs::read_to_string(&path) {
            if let Ok(pgid) = content.trim().parse::<i32>() {
                // Try SIGTERM first for firewall cleanup.
                unsafe { libc::kill(-pgid, libc::SIGTERM) };
                std::thread::sleep(std::time::Duration::from_secs(2));
                // Then SIGKILL as fallback.
                unsafe { libc::kill(-pgid, libc::SIGKILL) };
            }
            let _ = std::fs::remove_file(&path);
        }
    }
    // Always clean stale pf anchors regardless of PID file presence —
    // the previous tunnel may have been killed before writing the file.
    #[cfg(target_os = "macos")]
    clean_stale_pf_anchors();
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

/// Prepare identity env vars and jump host JSON for the tunnel binary.
/// PEM key material is passed via environment variables — never written to disk,
/// never visible in `ps aux`.
fn prepare_identity_args(profile: &Profile) -> Result<PreparedIdentity, String> {
    let mut env_vars: Vec<(String, String)> = Vec::new();

    // Main identity key → NETFERRY_IDENTITY_PEM env var.
    if let Some(k) = &profile.identity_key {
        if !k.trim().is_empty() {
            env_vars.push(("NETFERRY_IDENTITY_PEM".to_string(), k.clone()));
        }
    }

    // Jump hosts: inline keys → NETFERRY_JUMP_KEY_{i} env vars.
    // identityFile (path on disk) is still passed in JSON for file-based keys.
    let mut jump_specs: Vec<serde_json::Value> = Vec::new();
    for (i, jh) in profile.jump_hosts.iter().enumerate() {
        if let Some(k) = &jh.identity_key {
            if !k.trim().is_empty() {
                env_vars.push((format!("NETFERRY_JUMP_KEY_{i}"), k.clone()));
            }
        }
        // Only include identityFile in JSON when there is no inline key.
        let identity_file = match &jh.identity_key {
            Some(k) if !k.trim().is_empty() => None,
            _ => jh.identity_file.clone().filter(|f| !f.trim().is_empty()),
        };
        let mut spec = serde_json::json!({ "remote": jh.remote });
        if let Some(f) = identity_file {
            spec["identityFile"] = serde_json::Value::String(f);
        }
        jump_specs.push(spec);
    }

    Ok(PreparedIdentity {
        env_vars,
        jump_json: if jump_specs.is_empty() {
            None
        } else {
            Some(serde_json::to_string(&jump_specs).unwrap())
        },
    })
}

struct PreparedIdentity {
    /// Environment variables carrying PEM key material, to be injected into
    /// the tunnel child process.  Keys: NETFERRY_IDENTITY_PEM,
    /// NETFERRY_JUMP_KEY_0, NETFERRY_JUMP_KEY_1, …
    env_vars: Vec<(String, String)>,
    jump_json: Option<String>,
}

fn build_args(profile: &Profile, prepared: &PreparedIdentity) -> Vec<String> {
    let mut args: Vec<String> = Vec::new();

    // Verbose: tunnel logs individual connections to stderr (parsed by this process).
    args.push("-v".to_string());

    args.push("--remote".to_string());
    args.push(profile.remote.clone());

    if profile.auto_nets {
        args.push("--auto-nets".to_string());
    }
    // Inline key is passed via NETFERRY_IDENTITY_PEM env var (set at spawn time).
    // Only fall back to identity_file when there is no inline key.
    let has_inline_key = profile.identity_key.as_deref().map_or(false, |k| !k.trim().is_empty());
    if !has_inline_key && !profile.identity_file.trim().is_empty() {
        args.push("--identity".to_string());
        args.push(profile.identity_file.clone());
    }
    // Explicit jump hosts.
    if let Some(json) = &prepared.jump_json {
        args.push("--jump".to_string());
        args.push(json.clone());
    }
    if !profile.method.trim().is_empty() {
        args.push("--method".to_string());
        args.push(profile.method.clone());
    }
    if profile.pool_size > 1 {
        args.push("--pool".to_string());
        args.push(profile.pool_size.to_string());
        // Only pass the flag when explicitly choosing round-robin; least-loaded is the binary default.
        if profile.tcp_balance_mode == "round-robin" {
            args.push("--tcp-balance".to_string());
            args.push("round-robin".to_string());
        }
    }
    if profile.split_conn {
        args.push("--split".to_string());
    }
    if profile.disable_ipv6 {
        args.push("--no-ipv6".to_string());
    }
    if !profile.block_udp {
        args.push("--no-block-udp".to_string());
    }
    if profile.enable_udp {
        args.push("--udp".to_string());
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
    {
        let mut exclude_cidrs: Vec<String> = Vec::new();

        // Detect local network interfaces and exclude their /16 subnets.
        if profile.auto_exclude_lan {
            if let Ok(ifaces) = local_ip_address::list_afinet_netifas() {
                for (_name, ip) in &ifaces {
                    if let std::net::IpAddr::V4(v4) = ip {
                        if v4.is_loopback() || v4.is_link_local() {
                            continue;
                        }
                        let octets = v4.octets();
                        let cidr = format!("{}.{}.0.0/16", octets[0], octets[1]);
                        if !exclude_cidrs.contains(&cidr) {
                            exclude_cidrs.push(cidr);
                        }
                    }
                }
            }
        }

        // User-specified exclude subnets.
        for s in &profile.exclude_subnets {
            let s = s.trim();
            if !s.is_empty() && !exclude_cidrs.contains(&s.to_string()) {
                exclude_cidrs.push(s.to_string());
            }
        }

        if !exclude_cidrs.is_empty() {
            args.push("--exclude".to_string());
            args.push(exclude_cidrs.join(","));
        }
    }

    // Subnets are positional arguments and MUST come last.
    // Go's flag.Parse() stops at the first non-flag argument, so any flags
    // after a positional arg would be treated as positional args too.
    for subnet in &profile.subnets {
        if !subnet.trim().is_empty() {
            args.push(subnet.clone());
        }
    }

    args
}

/// Parse `c : deploy-reason: <reason>` and emit a reason event.
fn handle_deploy_reason_line(app: &AppHandle, line: &str) -> bool {
    let marker = "deploy-reason: ";
    let pos = match line.find(marker) {
        Some(p) => p,
        None => return false,
    };
    let reason = line[pos + marker.len()..].trim();
    if !reason.is_empty() {
        let _ = app.emit(DEPLOY_REASON_EVENT, reason.to_string());
        return true;
    }
    false
}

/// Parse `c : deploy-progress: SENT/TOTAL` and emit a progress event.
fn handle_deploy_progress_line(app: &AppHandle, line: &str) -> bool {
    let marker = "deploy-progress: ";
    let pos = match line.find(marker) {
        Some(p) => p,
        None => return false,
    };
    let payload = line[pos + marker.len()..].trim();
    let parts: Vec<&str> = payload.splitn(2, '/').collect();
    if parts.len() == 2 {
        if let (Ok(sent), Ok(total)) = (parts[0].parse::<u64>(), parts[1].parse::<u64>()) {
            let progress = crate::models::DeployProgress { sent, total };
            let _ = app.emit(DEPLOY_PROGRESS_EVENT, progress);
            return true;
        }
    }
    false
}

/// Parse `c : stats-port: XXXX` and store/emit the port.
fn handle_stats_port_line(app: &AppHandle, line: &str) -> bool {
    let marker = "stats-port: ";
    let pos = match line.find(marker) {
        Some(p) => p,
        None => return false,
    };
    let port_str = line[pos + marker.len()..].trim();
    if let Ok(port) = port_str.parse::<u16>() {
        if let Ok(mut g) = app.state::<AppState>().stats_port.lock() {
            *g = Some(port);
        }

        // Push persisted destination rules to the sidecar BEFORE the proxy
        // starts listening. This eliminates the race where connections arrive
        // with default "tunnel" route mode before the frontend has a chance to
        // push the persisted rules.
        push_persisted_rules_to_sidecar(app, port);

        let _ = app.emit(STATS_PORT_EVENT, port);
        return true;
    }
    false
}

/// Immediately push persisted priorities and routes to the Go sidecar's HTTP
/// API. Uses raw TCP to avoid depending on an async HTTP client.
fn push_persisted_rules_to_sidecar(app: &AppHandle, port: u16) {
    use crate::priorities;
    use std::io::{Read, Write};
    use std::net::TcpStream;

    let post = |path: &str, body: &str| -> Result<(), String> {
        let addr = format!("127.0.0.1:{}", port);
        let mut stream = TcpStream::connect(&addr).map_err(|e| e.to_string())?;
        stream
            .set_write_timeout(Some(std::time::Duration::from_secs(2)))
            .ok();
        stream
            .set_read_timeout(Some(std::time::Duration::from_secs(2)))
            .ok();
        let req = format!(
            "POST {} HTTP/1.1\r\nHost: 127.0.0.1:{}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            path, port, body.len(), body
        );
        stream.write_all(req.as_bytes()).map_err(|e| e.to_string())?;
        // Drain the response.
        let mut buf = [0u8; 512];
        let _ = stream.read(&mut buf);
        Ok(())
    };

    // Push priorities.
    if let Ok(prios) = priorities::load_priorities(app) {
        if !prios.is_empty() {
            if let Ok(body) = serde_json::to_string(&prios) {
                if let Err(e) = post("/priorities", &body) {
                    log::warn!("push priorities to sidecar: {}", e);
                }
            }
        }
    }

    // Push routes and active group snapshot. Routes live inside the active
    // group's `rules` map as V2 tagged unions — the Go tunnel's
    // `RouteMode.UnmarshalJSON` accepts this object form natively.
    #[derive(serde::Serialize)]
    struct ActiveGroupPayload<'a> {
        id: &'a str,
        #[serde(skip_serializing_if = "str::is_empty")]
        name: &'a str,
        #[serde(rename = "defaultProfileId")]
        default_profile_id: &'a str,
        #[serde(rename = "profileIds")]
        profile_ids: Vec<&'a str>,
    }
    if let Ok(settings) = crate::settings::load_settings(app) {
        if let Some(group_id) = settings.active_group_id.as_deref() {
            if let Ok(Some(group)) = crate::groups::load_group(app, group_id) {
                if !group.rules.is_empty() {
                    if let Ok(body) = serde_json::to_string(&group.rules) {
                        if let Err(e) = post("/routes", &body) {
                            log::warn!("push routes to sidecar: {}", e);
                        }
                    }
                }
                let default_id = group
                    .children
                    .iter()
                    .find(|c| !c.id.is_empty())
                    .map(|c| c.id.as_str())
                    .unwrap_or("");
                let payload = ActiveGroupPayload {
                    id: &group.id,
                    name: &group.name,
                    default_profile_id: default_id,
                    profile_ids: group.children.iter().map(|c| c.id.as_str()).collect(),
                };
                if let Ok(body) = serde_json::to_string(&payload) {
                    if let Err(e) = post("/group", &body) {
                        log::warn!("push group to sidecar: {}", e);
                    }
                }
            }
        }
    }
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
    profile: Profile,
) {
    let profile_id = profile.id.clone();
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
                    // Stats are now served by the tunnel's own HTTP/SSE server;
                    // no process I/O polling needed here.
                }

                Some("log") => {
                    let stream_name = ev["stream"].as_str().unwrap_or("stderr");
                    let log_line = ev["line"].as_str().unwrap_or("").to_string();

                    if handle_stats_port_line(&app, &log_line) {
                        continue;
                    }

                    if handle_deploy_reason_line(&app, &log_line) {
                        continue;
                    }

                    if handle_deploy_progress_line(&app, &log_line) {
                        continue;
                    }

                    if !tunnel_connected && log_line.contains("Connected to server.") {
                        tunnel_connected = true;
                        // Store profile for auto-reconnect on network loss.
                        if let Ok(mut g) = app.state::<AppState>().last_connected_profile.lock() {
                            *g = Some(profile.clone());
                        }
                        // Cache the resolved SSH server address for reconnect
                        // reachability checks (avoids DNS dependency).
                        cache_remote_addr(&app, &profile.remote);
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
                    log::error!("Helper error: {msg}");
                    let _ = app.emit(LOG_EVENT, format!("helper: {msg}"));
                }

                Some("exit") => {
                    let code = ev["code"].as_i64().unwrap_or(-1);
                    log::info!("Tunnel process exited with code={code}");
                    let _ = app.emit(LOG_EVENT, format!("helper: tunnel exited (code {code})"));
                    break;
                }

                _ => {}
            }
        }

        // ── Cleanup after stream closes ──────────────────────────────────────

        if let Ok(mut h) = app.state::<AppState>().helper_stream.lock() {
            *h = None;
        }

        if let Ok(mut g) = app.state::<AppState>().stats_port.lock() {
            *g = None;
        }

        let current = app
            .state::<AppState>()
            .status
            .lock()
            .map(|g| g.state.clone())
            .unwrap_or_default();
        if current == "connected" {
            // Was connected and tunnel died → auto-reconnect.
            let profile = app
                .state::<AppState>()
                .last_connected_profile
                .lock()
                .ok()
                .and_then(|g| g.clone());
            if let Some(profile) = profile {
                spawn_reconnect_thread(app, profile);
                return;
            }
        }
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

// ── Auto-reconnect ────────────────────────────────────────────────────────────

/// Extract the SSH host and port from a profile's remote string.
/// Supports formats: "host", "user@host", "user@host:port", "host:port".
fn parse_remote_host_port(remote: &str) -> (String, u16) {
    let without_user = if let Some(at) = remote.find('@') {
        &remote[at + 1..]
    } else {
        remote
    };
    if let Some(colon) = without_user.rfind(':') {
        let host = &without_user[..colon];
        let port = without_user[colon + 1..].parse::<u16>().unwrap_or(22);
        (host.to_string(), port)
    } else {
        (without_user.to_string(), 22)
    }
}

/// Resolve the SSH server's address and cache it in AppState for reconnect
/// reachability checks.  Called once when the tunnel first reports "Connected".
fn cache_remote_addr(app: &AppHandle, remote: &str) {
    let (host, port) = parse_remote_host_port(remote);
    if let Ok(mut addrs) = format!("{host}:{port}").to_socket_addrs() {
        if let Some(addr) = addrs.next() {
            if let Ok(mut g) = app.state::<AppState>().resolved_remote_addr.lock() {
                *g = Some(addr);
                log::info!("Cached remote addr for reconnect: {addr}");
            }
        }
    }
}

/// Spawn a background thread that retries connecting until it succeeds.
/// Uses a fixed retry interval (no exponential backoff) and emits countdown status
/// so the frontend can display retry progress.
fn spawn_reconnect_thread(app: AppHandle, profile: Profile) {
    // Set up cancellation token.
    let cancel = Arc::new(AtomicBool::new(false));
    if let Ok(mut g) = app.state::<AppState>().reconnect_cancel.lock() {
        *g = Some(cancel.clone());
    }

    let profile_id = profile.id.clone();

    // Emit reconnecting status.
    let status = ConnectionStatus {
        state: "reconnecting".to_string(),
        profile_id: Some(profile_id.clone()),
        message: Some("Network lost, waiting to reconnect\u{2026}".to_string()),
    };
    let _ = app.emit(STATUS_EVENT, status.clone());
    if let Ok(mut g) = app.state::<AppState>().status.lock() {
        *g = status;
    }

    std::thread::spawn(move || {
        const RETRY_INTERVAL: u64 = 5; // fixed interval in seconds
        let mut attempt: usize = 0;

        loop {
            attempt += 1;

            // Countdown with 1-second status updates so the frontend can show progress.
            for remaining in (1..=RETRY_INTERVAL).rev() {
                if cancel.load(Ordering::Relaxed) {
                    log::info!("Reconnect cancelled");
                    return;
                }
                let msg = format!(
                    "Reconnecting in {remaining}s (attempt #{attempt})\u{2026}"
                );
                let status = ConnectionStatus {
                    state: "reconnecting".to_string(),
                    profile_id: Some(profile_id.clone()),
                    message: Some(msg),
                };
                let _ = app.emit(STATUS_EVENT, status.clone());
                if let Ok(mut g) = app.state::<AppState>().status.lock() {
                    *g = status;
                }
                std::thread::sleep(std::time::Duration::from_secs(1));
            }

            if cancel.load(Ordering::Relaxed) {
                return;
            }

            // Update status: attempting now.
            let msg = format!("Reconnecting now (attempt #{attempt})\u{2026}");
            let _ = app.emit(LOG_EVENT, format!("reconnect: {msg}"));
            let status = ConnectionStatus {
                state: "reconnecting".to_string(),
                profile_id: Some(profile_id.clone()),
                message: Some(msg),
            };
            let _ = app.emit(STATUS_EVENT, status.clone());
            if let Ok(mut g) = app.state::<AppState>().status.lock() {
                *g = status;
            }

            // Clear the cancel token before reconnecting.
            if let Ok(mut g) = app.state::<AppState>().reconnect_cancel.lock() {
                *g = None;
            }

            // Attempt reconnect.
            let state: State<'_, AppState> = app.state();
            match connect(app.clone(), state, profile.clone()) {
                Ok(_) => {
                    log::info!("Reconnect succeeded for profile '{}'", profile.id);
                    return;
                }
                Err(e) => {
                    log::error!("Reconnect failed: {e}");
                    let _ = app.emit(LOG_EVENT, format!("reconnect: connect() failed: {e}"));
                    // Re-set cancel token and continue retrying.
                    if let Ok(mut g) = app.state::<AppState>().reconnect_cancel.lock() {
                        *g = Some(cancel.clone());
                    }
                    let status = ConnectionStatus {
                        state: "reconnecting".to_string(),
                        profile_id: Some(profile_id.clone()),
                        message: Some(format!(
                            "Reconnect failed (attempt #{attempt}): {e}"
                        )),
                    };
                    let _ = app.emit(STATUS_EVENT, status.clone());
                    if let Ok(mut g) = app.state::<AppState>().status.lock() {
                        *g = status;
                    }
                }
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
    // Cancel any pending reconnection attempt.
    if let Ok(mut g) = state.reconnect_cancel.lock() {
        if let Some(cancel) = g.take() {
            cancel.store(true, Ordering::Relaxed);
        }
    }

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
    log::info!("Connecting profile '{}', tunnel binary: {binary}", profile.id);

    // If the profile carries inline PEM key material, write it to a temp file
    // so the tunnel binary can read it via --identity.
    let prepared = prepare_identity_args(&profile)?;

    // ── macOS 13+: route through the privileged helper daemon ─────────────────
    // On success the helper manages the tunnel process (running as root) and we
    // communicate via a Unix socket — no per-connection sudo prompt needed.
    // Falls back to the sudo+askpass path on macOS ≤ 12.
    #[cfg(target_os = "macos")]
    match helper_ipc::ensure_helper_running() {
        Ok(true) => {
            log::info!("Using privileged helper daemon for connection");
            // The Go tunnel reads SSH config natively and receives HOME/USER/SSH_AUTH_SOCK
            // from the helper's env injection — no SSH wrapper script needed.
            let args = build_args(&profile, &prepared);
            log::debug!("Tunnel args: {:?}", args);
            let stream = helper_ipc::start_tunnel(&binary, &args, &prepared.env_vars)
                .map_err(|e| format!("Helper IPC: {e}"))?;
            // Give the event thread a cloned read end; keep the original for
            // disconnect (dropping it signals the helper to kill the tunnel).
            let read_stream = stream
                .try_clone()
                .map_err(|e| format!("Socket clone: {e}"))?;
            spawn_helper_event_thread(app.clone(), read_stream, profile.clone());
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
            log::info!("Helper not available, falling back to sudo+askpass");
            // macOS < 13 or helper not available → fall through to sudo+askpass.
        }
        Err(e) => {
            log::error!("Helper error: {e}");
            return Err(e);
        }
    }

    let args = build_args(&profile, &prepared);
    let mut cmd = Command::new(binary);
    cmd.args(args).stdout(Stdio::piped()).stderr(Stdio::piped());
    // Inject PEM key material as env vars (never written to disk, not in ps aux).
    for (k, v) in &prepared.env_vars {
        cmd.env(k, v);
    }

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

    // On Windows, prevent the child process from creating a visible console window.
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        cmd.creation_flags(CREATE_NO_WINDOW);
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
        let profile_clone = profile.clone();
        let profile_id = profile.id.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(err);
            let mut tunnel_connected = false;
            for line in reader.lines().map_while(Result::ok) {
                if handle_stats_port_line(&app_clone, &line) {
                    continue;
                }

                if handle_deploy_reason_line(&app_clone, &line) {
                    continue;
                }

                if handle_deploy_progress_line(&app_clone, &line) {
                    continue;
                }

                if !tunnel_connected && line.contains("Connected to server.") {
                    tunnel_connected = true;
                    // Store profile for auto-reconnect on network loss.
                    if let Ok(mut g) = app_clone.state::<AppState>().last_connected_profile.lock() {
                        *g = Some(profile_clone.clone());
                    }
                    // Cache the resolved SSH server address for reconnect
                    // reachability checks (avoids DNS dependency).
                    cache_remote_addr(&app_clone, &profile_clone.remote);
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

            // stderr EOF means the process has exited. Clear state.child so
            // that the next connect() can run.
            if let Ok(mut g) = app_clone.state::<AppState>().child.lock() {
                if let Some(mut c) = g.take() {
                    let _ = c.wait();
                }
            }

            if let Ok(mut g) = app_clone.state::<AppState>().stats_port.lock() {
                *g = None;
            }

            let current = app_clone
                .state::<AppState>()
                .status
                .lock()
                .map(|g| g.state.clone())
                .unwrap_or_default();
            if current == "connected" {
                // Was connected and tunnel died → auto-reconnect.
                let profile = app_clone
                    .state::<AppState>()
                    .last_connected_profile
                    .lock()
                    .ok()
                    .and_then(|g| g.clone());
                if let Some(profile) = profile {
                    spawn_reconnect_thread(app_clone, profile);
                    return;
                }
            }
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
    log::info!("Disconnecting tunnel");

    // Cancel any pending reconnection attempt.
    if let Ok(mut g) = state.reconnect_cancel.lock() {
        if let Some(cancel) = g.take() {
            cancel.store(true, Ordering::Relaxed);
        }
    }
    // Clear the stored profile so we don't auto-reconnect after manual disconnect.
    if let Ok(mut g) = state.last_connected_profile.lock() {
        *g = None;
    }

    // ── macOS: close the helper socket ────────────────────────────────────────
    // Signals the helper to kill the tunnel process group.
    // Pre-set our status to "disconnected" so the event thread (which also
    // watches for stream EOF) does not emit a spurious "error" event.
    #[cfg(target_os = "macos")]
    if let Ok(mut h) = state.helper_stream.lock() {
        if let Some(ref stream) = *h {
            // Write an explicit disconnect byte so the helper watchdog kills
            // the tunnel immediately.  This is more reliable than relying on
            // socket EOF alone, which may not propagate when cloned fds exist.
            use std::io::Write;
            let _ = (&*stream).write_all(b"q");
            let _ = (&*stream).flush();
            // Also shut down the socket so the cloned fd held by the
            // event-reader thread sees EOF.
            let _ = stream.shutdown(std::net::Shutdown::Both);
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
            // Send SIGTERM first so the tunnel can run fw.Restore() to clean up
            // firewall rules (pf/nft). SIGKILL would skip cleanup and leave stale
            // DNS redirect rules that break name resolution system-wide.
            let pid = child.id() as i32;
            let _ = unsafe { libc::kill(-pid, libc::SIGTERM) };
            // Give it a moment to clean up, then force-kill if still alive.
            let exited = wait_with_timeout(&mut child, std::time::Duration::from_secs(3));
            if !exited {
                log::warn!("Tunnel did not exit after SIGTERM, sending SIGKILL");
                let _ = unsafe { libc::kill(-pid, libc::SIGKILL) };
                let _ = child.wait();
            }
            // Clean up the PID file now that we've terminated the process group.
            let _ = std::fs::remove_file(pid_file_path());
        }
        #[cfg(not(unix))]
        {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
    drop(lock);

    // Safety net: clean up any stale pf anchors in case SIGTERM cleanup failed.
    #[cfg(target_os = "macos")]
    clean_stale_pf_anchors();

    if let Ok(mut g) = state.stats_port.lock() {
        *g = None;
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

// ── Utility helpers ──────────────────────────────────────────────────────────

/// Wait for a child process to exit, returning true if it exited within the timeout.
/// Public alias for use from the app exit handler in lib.rs.
#[cfg(unix)]
pub fn wait_child_with_timeout(
    child: &mut std::process::Child,
    timeout: std::time::Duration,
) -> bool {
    wait_with_timeout(child, timeout)
}

/// Wait for a child process to exit, returning true if it exited within the timeout.
#[cfg(unix)]
fn wait_with_timeout(child: &mut std::process::Child, timeout: std::time::Duration) -> bool {
    let start = std::time::Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(_)) => return true,
            Ok(None) => {
                if start.elapsed() >= timeout {
                    return false;
                }
                std::thread::sleep(std::time::Duration::from_millis(100));
            }
            Err(_) => return true, // process already gone
        }
    }
}

/// Public alias for use from the app exit handler in lib.rs.
#[cfg(target_os = "macos")]
pub fn clean_stale_pf_anchors_public() {
    clean_stale_pf_anchors();
}

/// Remove any leftover netferry-* pf anchors.  Called after tunnel disconnect
/// as a safety net in case the tunnel process was killed before it could clean
/// up its own rules (e.g. SIGKILL from a prior crash).
#[cfg(target_os = "macos")]
fn clean_stale_pf_anchors() {
    use std::process::Command;

    let output = match Command::new("pfctl").args(["-s", "all"]).output() {
        Ok(o) => o,
        Err(_) => return,
    };
    // Parse pfctl output for lines like: anchor "netferry-12345" ...
    // or: rdr-anchor "netferry-12345" ...
    let text = String::from_utf8_lossy(&output.stdout);
    let needle = "\"netferry-";
    for line in text.lines() {
        if let Some(start) = line.find(needle) {
            let name_start = start + 1; // skip opening quote
            if let Some(end) = line[name_start..].find('"') {
                let anchor = &line[name_start..name_start + end];
                log::info!("Cleaning stale pf anchor: {anchor}");
                let _ = Command::new("pfctl")
                    .args(["-a", anchor, "-F", "all"])
                    .output();
            }
        }
    }

    // Release any leaked pf enable token from a SIGKILL'd tunnel.
    // The Go tunnel persists the token to this file during Setup().
    let token_path = std::env::temp_dir().join("netferry-pf-token");
    if let Ok(tok) = std::fs::read_to_string(&token_path) {
        let token = tok.trim();
        if !token.is_empty() {
            log::info!("Releasing stale pf token: {token}");
            let _ = Command::new("pfctl").args(["-X", token]).output();
        }
        let _ = std::fs::remove_file(&token_path);
    }
}
