//! NetFerry Privileged Helper Daemon
//!
//! This binary is installed as a macOS LaunchDaemon (root) via SMAppService.
//! It listens on a Unix domain socket and executes sshuttle on behalf of the
//! main (unprivileged) application, streaming stdout/stderr back as JSON lines.
//!
//! Protocol (newline-delimited JSON):
//!
//! Request  (main app → helper):
//!   {"cmd":"connect","sshuttle_bin":"/path","args":[...]}
//!   {"cmd":"ping"}
//!
//! Response (helper → main app):
//!   {"type":"pong"}
//!   {"type":"started","pid":12345}
//!   {"type":"log","stream":"stdout"|"stderr","line":"..."}
//!   {"type":"exit","code":0}
//!   {"type":"error","message":"..."}

use std::io::{BufRead, BufReader, Write};
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::process::{Command, Stdio};
use std::sync::mpsc;

pub const SOCKET_PATH: &str = "/var/run/com.hoveychen.netferry.helper.sock";

// ── Wire types ────────────────────────────────────────────────────────────────

#[derive(serde::Deserialize)]
#[serde(tag = "cmd", rename_all = "lowercase")]
enum Request {
    Connect {
        sshuttle_bin: String,
        args: Vec<String>,
        /// Caller's environment variables (HOME, USER, SSH_AUTH_SOCK, …) so that
        /// SSH uses the real user's ~/.ssh instead of /var/root/.ssh.
        #[serde(default)]
        env: std::collections::HashMap<String, String>,
    },
    Ping,
}

#[derive(serde::Serialize)]
#[serde(tag = "type", rename_all = "lowercase")]
enum Response {
    Pong,
    Started { pid: u32 },
    Log { stream: String, line: String },
    Exit { code: i32 },
    Error { message: String },
}

fn send(stream: &mut UnixStream, resp: &Response) -> bool {
    match serde_json::to_string(resp) {
        Ok(json) => writeln!(stream, "{json}").is_ok(),
        Err(_) => false,
    }
}

// ── Connection handler ─────────────────────────────────────────────────────────

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
            send(&mut write_stream, &Response::Pong);
        }

        Request::Connect { sshuttle_bin, args, env } => {
            let mut cmd = Command::new(&sshuttle_bin);
            cmd.args(&args)
                .stdout(Stdio::piped())
                .stderr(Stdio::piped());

            // Inject the real user's env so SSH reads the correct ~/.ssh directory.
            for (key, val) in &env {
                cmd.env(key, val);
            }

            // Put the tunnel in its own process group so we can kill the whole
            // group when the socket closes (sshuttle spawns SSH children).
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

            // Relay events to the socket; stop if the socket write fails
            // (main app disconnected → kill process group).
            for event in rx {
                if !send(&mut write_stream, &event) {
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
            send(&mut write_stream, &Response::Exit { code });
        }
    }
}

// ── Main ──────────────────────────────────────────────────────────────────────

fn main() {
    // Clean up any stale socket from a previous crash.
    let _ = std::fs::remove_file(SOCKET_PATH);

    let listener = match UnixListener::bind(SOCKET_PATH) {
        Ok(l) => l,
        Err(e) => {
            eprintln!("netferry-helper: cannot bind {SOCKET_PATH}: {e}");
            std::process::exit(1);
        }
    };

    // Allow the unprivileged main app to connect.
    if let Err(e) =
        std::fs::set_permissions(SOCKET_PATH, std::fs::Permissions::from_mode(0o666))
    {
        eprintln!("netferry-helper: cannot chmod socket: {e}");
    }

    for stream in listener.incoming() {
        match stream {
            Ok(s) => {
                std::thread::spawn(|| handle_connection(s));
            }
            Err(e) => {
                eprintln!("netferry-helper: accept error: {e}");
            }
        }
    }
}
