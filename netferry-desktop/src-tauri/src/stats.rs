use crate::models::TunnelStats;
use crate::tray;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use sysinfo::{Pid, ProcessRefreshKind, ProcessesToUpdate, System};
use tauri::{AppHandle, Emitter};

pub const STATS_EVENT: &str = "tunnel-stats";

const POLL_INTERVAL: Duration = Duration::from_secs(1);
const FIND_INTERVAL: Duration = Duration::from_millis(250);
const SSH_FIND_TIMEOUT: Duration = Duration::from_secs(15);

/// Start a background thread that polls the SSH child process's I/O stats once per second.
/// Returns an `Arc<AtomicBool>` stop flag — call `stop_stats_monitoring` with it to halt the thread.
pub fn start_stats_monitoring(app: AppHandle, tunnel_pid: u32) -> Arc<AtomicBool> {
    let stop_flag = Arc::new(AtomicBool::new(false));
    let stop_clone = Arc::clone(&stop_flag);

    std::thread::spawn(move || {
        // Phase 1: find the SSH child PID spawned by the tunnel.
        let ssh_pid = find_ssh_child_pid(tunnel_pid, SSH_FIND_TIMEOUT);
        let target_pid = match ssh_pid {
            Some(p) => p,
            None => return, // Timed out — connection likely failed; stderr watcher handles that.
        };

        // Phase 2: poll I/O stats until stopped.
        let mut prev_read: u64 = 0;
        let mut prev_write: u64 = 0;
        let mut total_rx: u64 = 0;
        let mut total_tx: u64 = 0;
        let mut first_sample = true;

        loop {
            if stop_clone.load(Ordering::Relaxed) {
                break;
            }

            let (current_read, current_write) = read_process_io(target_pid);

            if first_sample {
                // Establish baseline — don't emit a spike on the very first sample.
                prev_read = current_read;
                prev_write = current_write;
                first_sample = false;
            } else {
                let rx_per_sec = current_read.saturating_sub(prev_read);
                let tx_per_sec = current_write.saturating_sub(prev_write);
                total_rx = total_rx.saturating_add(rx_per_sec);
                total_tx = total_tx.saturating_add(tx_per_sec);
                prev_read = current_read;
                prev_write = current_write;

                let stats = TunnelStats {
                    rx_bytes_per_sec: rx_per_sec,
                    tx_bytes_per_sec: tx_per_sec,
                    total_rx_bytes: total_rx,
                    total_tx_bytes: total_tx,
                };
                let _ = app.emit(STATS_EVENT, stats);
                let title = format!(
                    "↓{} ↑{}",
                    fmt_speed(rx_per_sec),
                    fmt_speed(tx_per_sec)
                );
                tray::update_tray_title(&app, Some(&title));
            }

            std::thread::sleep(POLL_INTERVAL);
        }
    });

    stop_flag
}

/// Signal the stats polling thread to stop.
pub fn stop_stats_monitoring(flag: &Arc<AtomicBool>) {
    flag.store(true, Ordering::Relaxed);
}

fn fmt_speed(bytes_per_sec: u64) -> String {
    if bytes_per_sec < 1024 {
        format!("{}B", bytes_per_sec)
    } else if bytes_per_sec < 1024 * 1024 {
        format!("{:.1}K", bytes_per_sec as f64 / 1024.0)
    } else {
        format!("{:.1}M", bytes_per_sec as f64 / (1024.0 * 1024.0))
    }
}

/// Find the SSH child process spawned by the tunnel (identified by parent PID and name).
/// Polls every 250 ms until found or `timeout` expires.
fn find_ssh_child_pid(parent_pid: u32, timeout: Duration) -> Option<u32> {
    let deadline = std::time::Instant::now() + timeout;
    let mut sys = System::new();

    loop {
        if std::time::Instant::now() >= deadline {
            return None;
        }

        sys.refresh_processes_specifics(
            ProcessesToUpdate::All,
            true,
            ProcessRefreshKind::new(),
        );

        for (pid, process) in sys.processes() {
            let is_child = process
                .parent()
                .map(|p| p.as_u32() == parent_pid)
                .unwrap_or(false);

            if is_child {
                let name = process.name().to_string_lossy().to_lowercase();
                if name.starts_with("ssh") || name == "sshpass" {
                    return Some(pid.as_u32());
                }
            }
        }

        std::thread::sleep(FIND_INTERVAL);
    }
}

/// Read cumulative I/O bytes (read, written) for the given PID.
/// Returns (0, 0) if the process is not found or stats are unavailable.
fn read_process_io(pid: u32) -> (u64, u64) {
    #[cfg(target_os = "linux")]
    {
        read_proc_io_linux(pid)
    }
    #[cfg(not(target_os = "linux"))]
    {
        read_sysinfo_io(pid)
    }
}

/// Linux: parse /proc/[pid]/io which reports all VFS-level I/O including sockets and pipes.
/// `rchar` = bytes read (includes socket reads), `wchar` = bytes written (includes socket writes).
#[cfg(target_os = "linux")]
fn read_proc_io_linux(pid: u32) -> (u64, u64) {
    let path = format!("/proc/{}/io", pid);
    let content = match std::fs::read_to_string(&path) {
        Ok(c) => c,
        Err(_) => return (0, 0),
    };

    let mut rchar: u64 = 0;
    let mut wchar: u64 = 0;

    for line in content.lines() {
        if let Some(val) = line.strip_prefix("rchar: ") {
            rchar = val.trim().parse().unwrap_or(0);
        } else if let Some(val) = line.strip_prefix("wchar: ") {
            wchar = val.trim().parse().unwrap_or(0);
        }
    }

    (rchar, wchar)
}

/// macOS / Windows: use sysinfo's disk_usage() which wraps:
///   - Windows: GetProcessIoCounters (includes socket + pipe I/O) ✓
///   - macOS: proc_pid_rusage (includes pipe I/O; socket-only bytes may be partial)
#[cfg(not(target_os = "linux"))]
fn read_sysinfo_io(pid: u32) -> (u64, u64) {
    let mut sys = System::new();
    let sysinfo_pid = Pid::from_u32(pid);
    sys.refresh_processes_specifics(
        ProcessesToUpdate::Some(&[sysinfo_pid]),
        true,
        ProcessRefreshKind::new().with_disk_usage(),
    );

    match sys.process(sysinfo_pid) {
        Some(proc) => {
            let usage = proc.disk_usage();
            (usage.total_read_bytes, usage.total_written_bytes)
        }
        None => (0, 0),
    }
}
