//! macOS privileged-helper IPC client.
//!
//! Provides two things:
//!   1. Registration – wraps the SMAppService ObjC API so the helper daemon
//!      can be installed (one-time native authorisation dialog).
//!   2. Connection – opens the Unix socket that the helper listens on and
//!      sends a `connect` request, returning the live stream for event reading.

use std::io::Write;
use std::os::unix::net::UnixStream;
use std::time::{Duration, Instant};

pub const SOCKET_PATH: &str = "/var/run/com.hoveychen.netferry.helper.sock";

// ── SMAppService FFI (compiled from smappservice.m) ───────────────────────────

extern "C" {
    /// Returns SMAppServiceStatus (0–3) or -1 if macOS < 13.
    fn netferry_helper_status() -> i32;
    /// Registers the LaunchDaemon; may show authorisation dialog. 0=ok.
    fn netferry_register_helper() -> i32;
}

/// High-level status mirroring SMAppServiceStatus.
#[derive(Debug, PartialEq, Eq)]
pub enum HelperStatus {
    NotRegistered,
    Enabled,
    RequiresApproval,
    NotFound,
    OsTooOld, // macOS < 13
}

pub fn helper_status() -> HelperStatus {
    match unsafe { netferry_helper_status() } {
        0 => HelperStatus::NotRegistered,
        1 => HelperStatus::Enabled,
        2 => HelperStatus::RequiresApproval,
        3 => HelperStatus::NotFound,
        _ => HelperStatus::OsTooOld,
    }
}

/// Ensure the helper is registered and running.
///
/// * On first call: shows the macOS native authorisation dialog (one time).
/// * On subsequent calls: returns immediately (already enabled).
/// * If macOS < 13: returns `Ok(false)` → caller should fall back to sudo.
///
/// Returns `Ok(true)` when the helper is ready, `Ok(false)` when not available
/// (old OS), or `Err` when registration failed or the socket didn't become
/// reachable within the timeout.
pub fn ensure_helper_running() -> Result<bool, String> {
    if helper_status() == HelperStatus::OsTooOld {
        return Ok(false);
    }

    // Always call register() — it is idempotent:
    //   • not yet registered  → registers and shows the one-time auth dialog
    //   • already registered, same version → no-op
    //   • already registered, newer bundle version → silently updates the helper
    // Skipping this call when status == Enabled would leave stale helper binaries
    // after the user installs a new version of the app.
    let rc = unsafe { netferry_register_helper() };
    match rc {
        0 => {} // ok (registered, already registered no-op, or silently updated)
        -1 => return Err("Failed to register the privileged helper. \
                          Check System Settings → Privacy & Security → \
                          Login Items & Extensions.".to_string()),
        _ => return Ok(false), // -2 = OS too old
    }

    // Wait up to 8 s for the helper to start and bind the socket.
    wait_for_socket(Duration::from_secs(8))?;
    Ok(true)
}

/// Poll until the helper's Unix socket is connectable, or return an error.
fn wait_for_socket(timeout: Duration) -> Result<(), String> {
    let deadline = Instant::now() + timeout;
    loop {
        if UnixStream::connect(SOCKET_PATH).is_ok() {
            return Ok(());
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "Timed out waiting for the privileged helper at {SOCKET_PATH}. \
                 Try restarting the app."
            ));
        }
        std::thread::sleep(Duration::from_millis(300));
    }
}

// ── Connect ───────────────────────────────────────────────────────────────────

/// Open a connection to the helper and send a `connect` command.
///
/// Returns the live `UnixStream`; the caller should spawn a thread to read
/// JSON event lines from it.  Dropping the stream signals the helper to kill
/// the tunnel process.
pub fn start_tunnel(sshuttle_bin: &str, args: &[String]) -> Result<UnixStream, String> {
    let mut stream =
        UnixStream::connect(SOCKET_PATH).map_err(|e| format!("Helper socket: {e}"))?;

    // Pass the real user's environment so the helper (running as root) looks at
    // ~/.ssh/known_hosts and ~/.ssh/config of the actual user, not /var/root/.ssh.
    let mut env = serde_json::Map::new();
    for key in &["HOME", "USER", "SSH_AUTH_SOCK", "SSH_AGENT_PID"] {
        if let Ok(val) = std::env::var(key) {
            env.insert(key.to_string(), serde_json::Value::String(val));
        }
    }
    // Prepend the SSH wrapper dir to PATH so that ProxyCommand's inner `ssh`
    // calls also find the wrapper (which injects -F for the real user's config).
    let current_path = std::env::var("PATH").unwrap_or_default();
    env.insert(
        "PATH".to_string(),
        serde_json::Value::String(format!("/tmp/com.hoveychen.netferry:{current_path}")),
    );

    let req = serde_json::json!({
        "cmd": "connect",
        "sshuttle_bin": sshuttle_bin,
        "args": args,
        "env": env,
    });
    writeln!(stream, "{req}").map_err(|e| format!("Helper write: {e}"))?;

    Ok(stream)
}

