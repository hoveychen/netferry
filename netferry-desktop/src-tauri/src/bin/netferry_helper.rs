//! NetFerry Privileged Helper Daemon
//!
//! This binary is installed as a macOS LaunchDaemon (root) via SMAppService.
//! It listens on a Unix domain socket and executes the tunnel binary on behalf of the
//! main (unprivileged) application, streaming stdout/stderr back as JSON lines.
//!
//! Protocol (newline-delimited JSON):
//!
//! Request  (main app → helper):
//!   {"cmd":"connect","tunnel_bin":"/path","args":[...]}
//!   {"cmd":"ping"}
//!   {"cmd":"version"}
//!
//! Response (helper → main app):
//!   {"type":"pong"}
//!   {"type":"version","version":2}
//!   {"type":"started","pid":12345}
//!   {"type":"log","stream":"stdout"|"stderr","line":"..."}
//!   {"type":"exit","code":0}
//!   {"type":"error","message":"..."}

#[cfg(unix)]
use std::io::{BufRead, BufReader, Read, Write};
#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;
#[cfg(unix)]
use std::os::unix::net::{UnixListener, UnixStream};
#[cfg(unix)]
use std::process::{Command, Stdio};
#[cfg(unix)]
use std::sync::mpsc;

pub const SOCKET_PATH: &str = "/var/run/com.hoveychen.netferry.helper.sock";

/// Protocol version — bump this whenever the IPC wire format changes so the
/// main app can detect stale helper daemons and force a restart.
pub const PROTOCOL_VERSION: u32 = 2;

// ── Wire types ────────────────────────────────────────────────────────────────

#[derive(serde::Deserialize)]
#[serde(tag = "cmd", rename_all = "lowercase")]
enum Request {
    Connect {
        tunnel_bin: String,
        args: Vec<String>,
        /// Caller's environment variables (HOME, USER, SSH_AUTH_SOCK, …) so that
        /// SSH uses the real user's ~/.ssh instead of /var/root/.ssh.
        #[serde(default)]
        env: std::collections::HashMap<String, String>,
    },
    Ping,
    Version,
}

#[derive(serde::Serialize)]
#[serde(tag = "type", rename_all = "lowercase")]
enum Response {
    Pong,
    #[serde(rename = "version")]
    Version { version: u32 },
    Started { pid: u32 },
    Log { stream: String, line: String },
    Exit { code: i32 },
    Error { message: String },
}

#[cfg(unix)]
fn send(stream: &mut UnixStream, resp: &Response) -> bool {
    match serde_json::to_string(resp) {
        Ok(json) => writeln!(stream, "{json}").is_ok(),
        Err(_) => false,
    }
}

// ── Connection handler ─────────────────────────────────────────────────────────

#[cfg(unix)]
fn handle_connection(stream: UnixStream) {
    let mut write_stream = match stream.try_clone() {
        Ok(s) => s,
        Err(_) => return,
    };

    // Read the single request line.
    let mut read_stream = BufReader::new(stream);
    let mut line = String::new();
    if read_stream.read_line(&mut line).unwrap_or(0) == 0 {
        return;
    }

    let req: Request = match serde_json::from_str(line.trim()) {
        Ok(r) => r,
        Err(e) => {
            log::error!("Bad request: {e}");
            send(
                &mut write_stream,
                &Response::Error {
                    message: format!("Bad request: {e}"),
                },
            );
            return;
        }
    };

    match req {
        Request::Ping => {
            log::debug!("Received ping");
            send(&mut write_stream, &Response::Pong);
        }

        Request::Version => {
            log::info!("Version query → v{PROTOCOL_VERSION}");
            send(&mut write_stream, &Response::Version { version: PROTOCOL_VERSION });
        }

        Request::Connect { tunnel_bin, args, env } => {
            log::info!("Connect request: tunnel_bin={tunnel_bin}, args={args:?}");
            let mut cmd = Command::new(&tunnel_bin);
            cmd.args(&args)
                .stdout(Stdio::piped())
                .stderr(Stdio::piped());

            // Inject the real user's env so SSH reads the correct ~/.ssh directory.
            for (key, val) in &env {
                cmd.env(key, val);
            }

            // Put the tunnel in its own process group so we can kill the whole
            // group when the socket closes (the tunnel spawns SSH children).
            unsafe {
                use std::os::unix::process::CommandExt;
                cmd.pre_exec(|| {
                    libc::setpgid(0, 0);
                    Ok(())
                });
            }

            let mut child = match cmd.spawn() {
                Ok(c) => c,
                Err(e) => {
                    log::error!("Failed to start tunnel: {e}");
                    send(
                        &mut write_stream,
                        &Response::Error {
                            message: format!("Failed to start tunnel: {e}"),
                        },
                    );
                    return;
                }
            };

            let pid = child.id();
            log::info!("Tunnel started, pid={pid}");
            send(&mut write_stream, &Response::Started { pid });

            let stdout = child.stdout.take().expect("stdout");
            let stderr = child.stderr.take().expect("stderr");

            // Merge stdout + stderr into a single channel.
            let (tx, rx) = mpsc::channel::<Response>();

            let tx_out = tx.clone();
            std::thread::spawn(move || {
                for line in BufReader::new(stdout).lines().map_while(Result::ok) {
                    if tx_out
                        .send(Response::Log {
                            stream: "stdout".into(),
                            line,
                        })
                        .is_err()
                    {
                        break;
                    }
                }
            });

            let tx_err = tx.clone();
            std::thread::spawn(move || {
                for line in BufReader::new(stderr).lines().map_while(Result::ok) {
                    if tx_err
                        .send(Response::Log {
                            stream: "stderr".into(),
                            line,
                        })
                        .is_err()
                    {
                        break;
                    }
                }
            });

            // Drop our copy of tx so the channel closes when both threads finish.
            drop(tx);

            // Watchdog: monitor the read end of the socket.  When the main app
            // disconnects — either by closing the socket (EOF) or by writing any
            // data (explicit disconnect signal) — we kill the tunnel process group.
            let watchdog_pid = pid;
            std::thread::spawn(move || {
                let mut buf = [0u8; 1];
                // read_line already consumed the request; any further read will
                // block until the peer closes the socket or sends data.
                loop {
                    match read_stream.read(&mut buf) {
                        Ok(0) | Err(_) => {
                            log::info!("Socket EOF detected (app disconnected), stopping tunnel pgid={watchdog_pid}");
                        }
                        Ok(_) => {
                            // App sent data — explicit disconnect signal.
                            log::info!("Disconnect signal received from app, stopping tunnel pgid={watchdog_pid}");
                        }
                    }
                    // Send SIGTERM first so the tunnel can clean up firewall rules.
                    unsafe { libc::kill(-(watchdog_pid as i32), libc::SIGTERM) };
                    // Wait briefly, then SIGKILL as fallback.
                    std::thread::sleep(std::time::Duration::from_secs(3));
                    unsafe { libc::kill(-(watchdog_pid as i32), libc::SIGKILL) };
                    return;
                }
            });

            // Relay events to the socket; stop if the socket write fails
            // (main app disconnected → kill process group).
            for event in rx {
                if !send(&mut write_stream, &event) {
                    log::info!("Socket write failed (app disconnected), stopping tunnel pgid={pid}");
                    // SIGTERM first for firewall cleanup, then SIGKILL.
                    unsafe { libc::kill(-(pid as i32), libc::SIGTERM) };
                    std::thread::sleep(std::time::Duration::from_secs(3));
                    unsafe { libc::kill(-(pid as i32), libc::SIGKILL) };
                    let _ = child.wait();
                    return;
                }
            }

            // Process exited naturally.
            let code = child
                .wait()
                .map(|s| s.code().unwrap_or(-1))
                .unwrap_or(-1);
            log::info!("Tunnel exited with code={code}");
            send(&mut write_stream, &Response::Exit { code });
            // Shutdown the socket so the app sees EOF immediately.
            // The watchdog thread still holds a read fd, which would keep
            // the socket alive and prevent the app from detecting the exit.
            let _ = write_stream.shutdown(std::net::Shutdown::Both);
        }
    }
}

// ── Main ──────────────────────────────────────────────────────────────────────

#[cfg(not(unix))]
fn main() {
    eprintln!("netferry-helper is not supported on this platform");
    std::process::exit(1);
}

#[cfg(unix)]
fn main() {
    netferry_desktop_lib::logging::init_helper_logging();
    log::info!("netferry-helper starting (protocol v{PROTOCOL_VERSION})");

    // Clean up any stale socket from a previous crash.
    let _ = std::fs::remove_file(SOCKET_PATH);

    let listener = match UnixListener::bind(SOCKET_PATH) {
        Ok(l) => l,
        Err(e) => {
            log::error!("Cannot bind {SOCKET_PATH}: {e}");
            std::process::exit(1);
        }
    };

    // Allow the unprivileged main app to connect.
    if let Err(e) =
        std::fs::set_permissions(SOCKET_PATH, std::fs::Permissions::from_mode(0o666))
    {
        log::error!("Cannot chmod socket: {e}");
    }

    log::info!("Listening on {SOCKET_PATH}");
    for stream in listener.incoming() {
        match stream {
            Ok(s) => {
                log::debug!("Accepted new connection");
                std::thread::spawn(|| handle_connection(s));
            }
            Err(e) => {
                log::error!("Accept error: {e}");
            }
        }
    }
}
