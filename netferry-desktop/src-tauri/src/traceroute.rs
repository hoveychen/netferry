use futures_util::StreamExt;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::collections::HashMap;
use std::io::{BufRead, BufReader, Write};
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::sync::Mutex;
use tauri::{AppHandle, Emitter, Manager, State};

pub const HOP_EVENT: &str = "traceroute-hop";
pub const DONE_EVENT: &str = "traceroute-done";
pub const DOWNLOAD_PROGRESS_EVENT: &str = "traceroute-download-progress";

/// Pinned upstream version. Bump together with INSTALL_VERSION below
/// when validating a newer NextTrace release.
const NEXTTRACE_VERSION: &str = "v1.6.4";
/// Subdirectory under app_data_dir; bump if the on-disk binary layout changes.
const INSTALL_DIR: &str = "nexttrace";

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

#[derive(Serialize, Clone)]
pub struct DownloadProgress {
    pub bytes: u64,
    pub total: u64,
    /// "downloading" | "done" | "error"
    pub phase: String,
    pub message: Option<String>,
}

#[derive(Serialize, Clone)]
pub struct InstallStatus {
    pub installed: bool,
    pub path: String,
    /// One of: "env", "appdata", "dev", "path".
    pub source: String,
    pub version: String,
    /// Where downloads land if `installed = false`. Surfaced so the UI can
    /// tell users where to drop a manually downloaded binary.
    #[serde(rename = "expectedPath")]
    pub expected_path: String,
    /// Direct download URL for the current platform — UI can offer it as a
    /// fallback when automatic download is blocked (e.g. GFW).
    #[serde(rename = "downloadUrl")]
    pub download_url: String,
}

/// Map the build target triple to the matching nxtrace/NTrace-core release
/// asset (file name + expected SHA-256). Returns None for triples we have
/// not validated. Hashes are pinned for NEXTTRACE_VERSION; bump them
/// together when upgrading.
fn release_asset() -> Option<(&'static str, &'static str)> {
    let triple = env!("TARGET_TRIPLE");
    Some(match triple {
        "aarch64-apple-darwin" => (
            "nexttrace-tiny_darwin_arm64",
            "e666b60fe8d2b0bf12555e4f463f2bba596fee9d03dadee35e3a2edf7e6c86fd",
        ),
        "x86_64-apple-darwin" => (
            "nexttrace-tiny_darwin_amd64",
            "3ed6893cd438d0dfb8fe2d4c0060ad7fa9becc7968a8016a40da23599174e59f",
        ),
        "x86_64-pc-windows-msvc" => (
            "nexttrace-tiny_windows_amd64.exe",
            "808d7c5eab3569e7009d4698872bc1aecba34804019ce8df738abfd2e13d7e4d",
        ),
        "aarch64-pc-windows-msvc" => (
            "nexttrace-tiny_windows_arm64.exe",
            "a31915cdaf387be05158ce680466104b1b76c13fdf0ceedd1fa9c65c7de05998",
        ),
        "x86_64-unknown-linux-gnu" => (
            "nexttrace-tiny_linux_amd64",
            "03d514c7de478c4bb1ea8a43e771d6e8eb0a4f8a7347a36cf2b48c691f067e03",
        ),
        "aarch64-unknown-linux-gnu" => (
            "nexttrace-tiny_linux_arm64",
            "9670182456da65dd6a05a40cdca37f17ccf81581ce78d1132d48072c9b6d68a9",
        ),
        _ => return None,
    })
}

fn release_asset_name() -> Option<&'static str> {
    release_asset().map(|(n, _)| n)
}

fn release_download_url() -> Option<String> {
    release_asset_name().map(|name| {
        format!(
            "https://github.com/nxtrace/NTrace-core/releases/download/{NEXTTRACE_VERSION}/{name}"
        )
    })
}

fn install_dir(app: &AppHandle) -> Result<PathBuf, String> {
    let base = app
        .path()
        .app_data_dir()
        .map_err(|e| format!("app_data_dir unavailable: {e}"))?;
    Ok(base.join(INSTALL_DIR))
}

fn install_path(app: &AppHandle) -> Result<PathBuf, String> {
    Ok(install_dir(app)?.join(if cfg!(windows) { "nexttrace.exe" } else { "nexttrace" }))
}

/// Locate an existing nexttrace binary without touching the network.
/// Resolution order: env override → app data dir (canonical install
/// location) → dev-mode local `binaries/` checkout → return None.
fn find_existing(app: &AppHandle) -> Option<(PathBuf, &'static str)> {
    if let Ok(path) = std::env::var("NETFERRY_NEXTTRACE_BIN") {
        let p = PathBuf::from(path.trim());
        if p.exists() {
            return Some((p, "env"));
        }
    }
    if let Ok(p) = install_path(app) {
        if p.exists() {
            return Some((p, "appdata"));
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
            return Some((candidate, "dev"));
        }
    }
    None
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
pub fn nexttrace_status(app: AppHandle) -> Result<InstallStatus, String> {
    let expected = install_path(&app)?
        .to_string_lossy()
        .into_owned();
    let download_url = release_download_url().unwrap_or_default();
    if let Some((p, src)) = find_existing(&app) {
        Ok(InstallStatus {
            installed: true,
            path: p.to_string_lossy().into_owned(),
            source: src.to_string(),
            version: NEXTTRACE_VERSION.to_string(),
            expected_path: expected,
            download_url,
        })
    } else {
        Ok(InstallStatus {
            installed: false,
            path: String::new(),
            source: String::new(),
            version: NEXTTRACE_VERSION.to_string(),
            expected_path: expected,
            download_url,
        })
    }
}

#[tauri::command]
pub async fn ensure_nexttrace_installed(app: AppHandle) -> Result<InstallStatus, String> {
    if let Some((p, src)) = find_existing(&app) {
        return Ok(InstallStatus {
            installed: true,
            path: p.to_string_lossy().into_owned(),
            source: src.to_string(),
            version: NEXTTRACE_VERSION.to_string(),
            expected_path: install_path(&app)?.to_string_lossy().into_owned(),
            download_url: release_download_url().unwrap_or_default(),
        });
    }

    let (asset_name, expected_sha256) = release_asset().ok_or_else(|| {
        format!(
            "No prebuilt NextTrace binary available for this platform ({}). Install it manually and set NETFERRY_NEXTTRACE_BIN.",
            env!("TARGET_TRIPLE")
        )
    })?;
    let url = format!(
        "https://github.com/nxtrace/NTrace-core/releases/download/{NEXTTRACE_VERSION}/{asset_name}"
    );
    let target = install_path(&app)?;

    let _ = app.emit(
        DOWNLOAD_PROGRESS_EVENT,
        DownloadProgress {
            bytes: 0,
            total: 0,
            phase: "downloading".into(),
            message: Some(url.clone()),
        },
    );

    match download_to(&app, &url, &target, expected_sha256).await {
        Ok(()) => {
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                let _ = std::fs::set_permissions(&target, std::fs::Permissions::from_mode(0o755));
            }
            let _ = app.emit(
                DOWNLOAD_PROGRESS_EVENT,
                DownloadProgress {
                    bytes: 0,
                    total: 0,
                    phase: "done".into(),
                    message: None,
                },
            );
            Ok(InstallStatus {
                installed: true,
                path: target.to_string_lossy().into_owned(),
                source: "appdata".into(),
                version: NEXTTRACE_VERSION.to_string(),
                expected_path: target.to_string_lossy().into_owned(),
                download_url: url,
            })
        }
        Err(e) => {
            // Best-effort cleanup of any partial file.
            let _ = std::fs::remove_file(&target);
            let _ = app.emit(
                DOWNLOAD_PROGRESS_EVENT,
                DownloadProgress {
                    bytes: 0,
                    total: 0,
                    phase: "error".into(),
                    message: Some(e.clone()),
                },
            );
            Err(e)
        }
    }
}

async fn download_to(
    app: &AppHandle,
    url: &str,
    target: &std::path::Path,
    expected_sha256: &str,
) -> Result<(), String> {
    if let Some(parent) = target.parent() {
        std::fs::create_dir_all(parent).map_err(|e| format!("Create install dir: {e}"))?;
    }

    let client = reqwest::Client::builder()
        .user_agent("NetFerry-Desktop")
        .build()
        .map_err(|e| format!("HTTP client init: {e}"))?;

    let resp = client
        .get(url)
        .send()
        .await
        .map_err(|e| format!("Request failed: {e}"))?;
    if !resp.status().is_success() {
        return Err(format!("GitHub returned status {}", resp.status()));
    }
    let total = resp.content_length().unwrap_or(0);

    // Write to a sibling .part file then atomically rename, so a partial
    // download never gets picked up as a usable binary.
    let part_path = target.with_extension("part");
    let mut file = std::fs::File::create(&part_path)
        .map_err(|e| format!("Open download file: {e}"))?;

    let mut hasher = Sha256::new();
    let mut downloaded: u64 = 0;
    let mut stream = resp.bytes_stream();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(|e| format!("Network read: {e}"))?;
        file.write_all(&chunk).map_err(|e| format!("Disk write: {e}"))?;
        hasher.update(&chunk);
        downloaded += chunk.len() as u64;
        let _ = app.emit(
            DOWNLOAD_PROGRESS_EVENT,
            DownloadProgress {
                bytes: downloaded,
                total,
                phase: "downloading".into(),
                message: None,
            },
        );
    }
    drop(file);

    if total > 0 && downloaded != total {
        let _ = std::fs::remove_file(&part_path);
        return Err(format!(
            "Truncated download: got {downloaded} of {total} bytes"
        ));
    }

    // Verify SHA-256 before publishing the binary, so a tampered or
    // mirrored asset never gets executed. Compare against the lower-case
    // hex of the pinned hash.
    let actual = hex::encode(hasher.finalize());
    if !actual.eq_ignore_ascii_case(expected_sha256) {
        let _ = std::fs::remove_file(&part_path);
        return Err(format!(
            "SHA-256 mismatch: expected {expected_sha256}, got {actual}"
        ));
    }

    std::fs::rename(&part_path, target).map_err(|e| format!("Rename to final path: {e}"))?;
    Ok(())
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
    let exe = find_existing(&app)
        .map(|(p, _)| p.to_string_lossy().into_owned())
        .ok_or_else(|| {
            "NextTrace binary not installed. Click \"Prepare tool\" first.".to_string()
        })?;

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

    #[test]
    fn release_asset_name_known_for_supported_triples() {
        // Just ensure the function returns Some for the host triple under test.
        // (In CI-scoped tests this proves the platform we run tests on is wired.)
        assert!(release_asset_name().is_some());
    }

    #[test]
    fn release_asset_sha256_is_lowercase_hex_of_correct_length() {
        let (_, sha) = release_asset().expect("host triple wired");
        assert_eq!(sha.len(), 64, "SHA-256 hex must be 64 chars");
        assert!(
            sha.chars().all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase()),
            "SHA-256 hex must be lowercase: {sha}"
        );
    }
}
