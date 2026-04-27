use serde::Serialize;
use std::collections::HashMap;
use std::io::{BufRead, BufReader};
use std::process::{Command, Stdio};
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, Manager, State};

pub const HOP_EVENT: &str = "traceroute-hop";
pub const DONE_EVENT: &str = "traceroute-done";

#[derive(Default)]
pub struct TracerouteState {
    /// session_id -> child PID. PIDs let cancel/exit cleanup signal the
    /// child without holding the `Child` itself, which is moved into the
    /// reaper thread that calls wait().
    sessions: Mutex<HashMap<String, u32>>,
}

#[derive(Serialize, Clone)]
pub struct HopPayload {
    #[serde(rename = "sessionId")]
    pub session_id: String,
    pub ttl: u8,
    pub ip: Option<String>,
    pub hostname: Option<String>,
    #[serde(rename = "rttMs")]
    pub rtt_ms: Option<f64>,
    pub asn: Option<String>,
    pub owner: Option<String>,
    pub country: Option<String>,
    pub province: Option<String>,
    pub city: Option<String>,
    pub isp: Option<String>,
    /// True when nexttrace printed `*` for this TTL (no reply).
    pub timeout: bool,
}

#[derive(Serialize, Clone)]
pub struct DonePayload {
    #[serde(rename = "sessionId")]
    pub session_id: String,
    #[serde(rename = "exitCode")]
    pub exit_code: Option<i32>,
}

/// Mirror of the resolution strategy in `sidecar::resolve_tunnel_exe` so the
/// nexttrace sidecar is found in dev (`binaries/nexttrace-<triple>`),
/// production bundle (next to the main exe), or via env override.
fn resolve_nexttrace_exe() -> String {
    if let Ok(path) = std::env::var("NETFERRY_NEXTTRACE_BIN") {
        if !path.trim().is_empty() {
            return path;
        }
    }
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let candidate = dir.join(if cfg!(windows) { "nexttrace.exe" } else { "nexttrace" });
            if candidate.exists() {
                return candidate.to_string_lossy().into_owned();
            }
        }
    }
    #[cfg(debug_assertions)]
    {
        let mut name = format!("nexttrace-{}", env!("TARGET_TRIPLE"));
        if cfg!(windows) {
            name.push_str(".exe");
        }
        let candidate = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join(name);
        if candidate.exists() {
            return candidate.to_string_lossy().into_owned();
        }
    }
    "nexttrace".to_string()
}

/// Extract the host portion from a profile.remote-style string. Accepts:
///   `host`, `host:port`, `user@host`, `user@host:port`,
///   `[ipv6]:port`, `user@[ipv6]:port`, raw IPv6.
fn parse_target(raw: &str) -> Result<String, String> {
    let s = raw.trim();
    if s.is_empty() {
        return Err("Target host is empty".into());
    }
    let after_user = s.rsplit_once('@').map(|(_, rest)| rest).unwrap_or(s);
    let host = if let Some(rest) = after_user.strip_prefix('[') {
        // bracketed IPv6, optional :port after `]`.
        match rest.find(']') {
            Some(end) => &rest[..end],
            None => after_user,
        }
    } else if after_user.matches(':').count() == 1 {
        // host:port (single colon → not IPv6 without brackets).
        after_user.rsplit_once(':').map(|(h, _)| h).unwrap_or(after_user)
    } else {
        // No colon, or multiple colons (raw IPv6) — keep as-is.
        after_user
    };
    let host = host.trim();
    if host.is_empty() {
        return Err("Target host is empty after parsing".into());
    }
    Ok(host.to_string())
}

fn parse_raw_line(line: &str, session_id: &str) -> Option<HopPayload> {
    let trimmed = line.trim_end_matches('\r').trim();
    if trimmed.is_empty() {
        return None;
    }
    let parts: Vec<&str> = trimmed.split('|').collect();
    if parts.len() < 2 {
        return None;
    }
    let ttl: u8 = parts[0].trim().parse().ok()?;
    let get = |i: usize| -> Option<String> {
        parts
            .get(i)
            .map(|s| s.trim().to_string())
            .filter(|s| !s.is_empty())
    };
    let ip_field = get(1);
    let timeout = matches!(ip_field.as_deref(), Some("*"));
    Some(HopPayload {
        session_id: session_id.to_string(),
        ttl,
        ip: if timeout { None } else { ip_field },
        hostname: get(2),
        rtt_ms: get(3).and_then(|s| s.parse().ok()),
        asn: get(4),
        owner: get(5),
        country: get(6),
        province: get(7),
        city: get(8),
        isp: get(9),
        timeout,
    })
}

#[tauri::command]
pub fn start_traceroute(
    app: AppHandle,
    state: State<'_, TracerouteState>,
    target: String,
    max_hops: Option<u8>,
    queries: Option<u8>,
    geo_source: Option<String>,
) -> Result<String, String> {
    let host = parse_target(&target)?;
    let session_id = uuid::Uuid::new_v4().to_string();
    let exe = resolve_nexttrace_exe();

    let mut cmd = Command::new(&exe);
    cmd.args(["--raw", "--no-color"]);
    let m = max_hops.unwrap_or(30).clamp(1, 64);
    cmd.args(["-m", &m.to_string()]);
    let q = queries.unwrap_or(1).clamp(1, 5);
    cmd.args(["-q", &q.to_string()]);
    if let Some(g) = geo_source.as_deref() {
        let g = g.trim();
        if !g.is_empty() {
            cmd.args(["-d", g]);
        }
    }
    cmd.arg(&host);
    cmd.stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());

    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        cmd.creation_flags(CREATE_NO_WINDOW);
    }

    let mut child = cmd
        .spawn()
        .map_err(|e| format!("Failed to spawn nexttrace ({exe}): {e}"))?;
    let pid = child.id();
    let stdout = child
        .stdout
        .take()
        .ok_or_else(|| "nexttrace stdout missing".to_string())?;
    let stderr = child
        .stderr
        .take()
        .ok_or_else(|| "nexttrace stderr missing".to_string())?;

    state
        .sessions
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .insert(session_id.clone(), pid);

    {
        let app = app.clone();
        let sid = session_id.clone();
        std::thread::spawn(move || {
            let reader = BufReader::new(stdout);
            for line in reader.lines().map_while(Result::ok) {
                if let Some(hop) = parse_raw_line(&line, &sid) {
                    let _ = app.emit(HOP_EVENT, hop);
                }
            }
        });
    }

    {
        std::thread::spawn(move || {
            let reader = BufReader::new(stderr);
            for line in reader.lines().map_while(Result::ok) {
                log::debug!("[nexttrace stderr] {line}");
            }
        });
    }

    {
        let app = app.clone();
        let sid = session_id.clone();
        std::thread::spawn(move || {
            let exit_code = child.wait().ok().and_then(|s| s.code());
            let _ = app.emit(
                DONE_EVENT,
                DonePayload {
                    session_id: sid.clone(),
                    exit_code,
                },
            );
            if let Some(state) = app.try_state::<TracerouteState>() {
                state
                    .sessions
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .remove(&sid);
            }
        });
    }

    Ok(session_id)
}

#[tauri::command]
pub fn cancel_traceroute(
    state: State<'_, TracerouteState>,
    session_id: String,
) -> Result<(), String> {
    let pid_opt = state
        .sessions
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .remove(&session_id);
    if let Some(pid) = pid_opt {
        kill_pid(pid);
    }
    Ok(())
}

fn kill_pid(pid: u32) {
    #[cfg(unix)]
    {
        unsafe {
            libc::kill(pid as i32, libc::SIGTERM);
        }
    }
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        let _ = Command::new("taskkill")
            .args(["/F", "/T", "/PID", &pid.to_string()])
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .creation_flags(CREATE_NO_WINDOW)
            .status();
    }
}

/// Called from the app's RunEvent::Exit handler to terminate any
/// in-flight traceroute children before the process tears down.
pub fn kill_all_sessions(state: &TracerouteState) {
    let pids: Vec<u32> = state
        .sessions
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .drain()
        .map(|(_, pid)| pid)
        .collect();
    for pid in pids {
        kill_pid(pid);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_target_strips_user_and_port() {
        assert_eq!(parse_target("user@host.example:22").unwrap(), "host.example");
        assert_eq!(parse_target("host.example").unwrap(), "host.example");
        assert_eq!(parse_target("host:2222").unwrap(), "host");
        assert_eq!(parse_target("user@1.2.3.4").unwrap(), "1.2.3.4");
        assert_eq!(parse_target("[::1]:22").unwrap(), "::1");
        assert_eq!(parse_target("user@[2001:db8::1]:22").unwrap(), "2001:db8::1");
        assert_eq!(parse_target("2001:db8::1").unwrap(), "2001:db8::1");
        assert!(parse_target("").is_err());
        assert!(parse_target("   ").is_err());
    }

    #[test]
    fn parse_raw_line_data_row() {
        let h = parse_raw_line("3|1.2.3.4|host.x|12.5|13335|Cloudflare|US|CA|San Jose|Cloudflare|37.0|-122.0", "sid").unwrap();
        assert_eq!(h.ttl, 3);
        assert_eq!(h.ip.as_deref(), Some("1.2.3.4"));
        assert_eq!(h.hostname.as_deref(), Some("host.x"));
        assert_eq!(h.rtt_ms, Some(12.5));
        assert_eq!(h.asn.as_deref(), Some("13335"));
        assert_eq!(h.owner.as_deref(), Some("Cloudflare"));
        assert!(!h.timeout);
    }

    #[test]
    fn parse_raw_line_timeout() {
        let h = parse_raw_line("4|*||||||", "sid").unwrap();
        assert_eq!(h.ttl, 4);
        assert!(h.timeout);
        assert!(h.ip.is_none());
        assert!(h.rtt_ms.is_none());
    }

    #[test]
    fn parse_raw_line_skips_headers() {
        assert!(parse_raw_line("NextTrace v1.6.4 ...", "sid").is_none());
        assert!(parse_raw_line("IP Geo Data Provider: LeoMoeAPI", "sid").is_none());
        assert!(parse_raw_line("MapTrace URL: ...", "sid").is_none());
        assert!(parse_raw_line("", "sid").is_none());
    }
}
