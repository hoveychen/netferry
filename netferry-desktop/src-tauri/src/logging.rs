//! File-based logging for NetFerry.
//!
//! The main desktop app uses `tauri-plugin-log` (registered in lib.rs) which
//! writes to both stderr and the Tauri app-log directory automatically.
//!
//! The privileged helper daemon runs as a standalone binary without Tauri, so
//! it keeps its own simplelog-based setup.
//!
//! Log locations:
//!   - Main app : <app_log_dir>/netferry.log  (managed by tauri-plugin-log)
//!   - Helper   : /var/log/netferry-helper.log (runs as root)

use log::LevelFilter;
use simplelog::{
    CombinedLogger, ConfigBuilder, TermLogger, TerminalMode, WriteLogger,
};
use std::fs;
use std::path::PathBuf;

/// Initialise logging for the privileged helper daemon.
pub fn init_helper_logging() {
    let config = ConfigBuilder::new()
        .set_time_format_rfc3339()
        .build();

    let mut loggers: Vec<Box<dyn simplelog::SharedLogger>> = vec![
        TermLogger::new(
            LevelFilter::Info,
            config.clone(),
            TerminalMode::Stderr,
            simplelog::ColorChoice::Auto,
        ),
    ];

    // Helper runs as root — write to /var/log/.
    let path = PathBuf::from("/var/log/netferry-helper.log");
    if let Ok(file) = fs::File::create(&path) {
        loggers.push(WriteLogger::new(LevelFilter::Debug, config, file));
    }

    let _ = CombinedLogger::init(loggers);
}
