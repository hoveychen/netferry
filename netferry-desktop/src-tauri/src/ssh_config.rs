use crate::models::SshHostEntry;
use std::fs;
use std::path::PathBuf;

fn parse_kv(line: &str) -> Option<(String, String)> {
    let trimmed = line.trim();
    if trimmed.is_empty() || trimmed.starts_with('#') {
        return None;
    }
    let mut parts = trimmed.splitn(2, char::is_whitespace);
    let key = parts.next()?.trim().to_lowercase();
    let value = parts.next()?.trim().to_string();
    if key.is_empty() || value.is_empty() {
        return None;
    }
    Some((key, value))
}

fn is_wildcard_host(host: &str) -> bool {
    host.split_whitespace()
        .filter(|p| !p.starts_with('!'))
        .all(|p| p.contains('*') || p.contains('?'))
}

fn expand_tilde(path: &str) -> String {
    if let Some(home) = dirs::home_dir() {
        if path == "~" {
            return home.to_string_lossy().to_string();
        }
        if let Some(rest) = path.strip_prefix("~/") {
            return home.join(rest).to_string_lossy().to_string();
        }
    }
    path.to_string()
}

/// Extract the IdentityFile declared under a wildcard `Host *` block.
/// Returns None if the config does not exist or contains no such entry.
pub fn get_default_identity_file() -> Option<String> {
    let home = dirs::home_dir()?;
    let path = PathBuf::from(home).join(".ssh").join("config");
    if !path.exists() {
        return None;
    }
    let raw = fs::read_to_string(path).ok()?;

    let mut in_wildcard_host = false;
    for line in raw.lines() {
        let Some((key, value)) = parse_kv(line) else {
            continue;
        };
        if key == "host" {
            in_wildcard_host = is_wildcard_host(&value);
            continue;
        }
        if in_wildcard_host && key == "identityfile" {
            return Some(expand_tilde(&value));
        }
    }
    None
}

pub fn parse_default_ssh_config() -> Result<Vec<SshHostEntry>, String> {
    let home = dirs::home_dir().ok_or_else(|| "Failed to locate user home directory".to_string())?;
    let path = PathBuf::from(home).join(".ssh").join("config");
    if !path.exists() {
        return Ok(Vec::new());
    }
    let raw = fs::read_to_string(path).map_err(|e| format!("Failed to read ~/.ssh/config: {e}"))?;
    parse_ssh_config(&raw)
}

pub fn parse_ssh_config(raw: &str) -> Result<Vec<SshHostEntry>, String> {
    let mut entries: Vec<SshHostEntry> = Vec::new();
    // Accumulate defaults from wildcard Host blocks (e.g. `Host *`).
    let mut wildcard_defaults = SshHostEntry {
        host: "*".to_string(),
        host_name: None,
        user: None,
        port: None,
        identity_file: None,
        proxy_jump: None,
        proxy_command: None,
    };
    let mut current: Option<SshHostEntry> = None;
    let mut current_is_wildcard = false;

    for line in raw.lines() {
        let Some((key, value)) = parse_kv(line) else {
            continue;
        };
        if key == "host" {
            if let Some(prev) = current.take() {
                if current_is_wildcard {
                    // Merge into wildcard_defaults (first-match-wins: only fill empty fields).
                    if wildcard_defaults.host_name.is_none() {
                        wildcard_defaults.host_name = prev.host_name;
                    }
                    if wildcard_defaults.user.is_none() {
                        wildcard_defaults.user = prev.user;
                    }
                    if wildcard_defaults.port.is_none() {
                        wildcard_defaults.port = prev.port;
                    }
                    if wildcard_defaults.identity_file.is_none() {
                        wildcard_defaults.identity_file = prev.identity_file;
                    }
                    if wildcard_defaults.proxy_jump.is_none() {
                        wildcard_defaults.proxy_jump = prev.proxy_jump;
                    }
                    if wildcard_defaults.proxy_command.is_none() {
                        wildcard_defaults.proxy_command = prev.proxy_command;
                    }
                } else {
                    entries.push(prev);
                }
            }
            current_is_wildcard = is_wildcard_host(&value);
            current = Some(SshHostEntry {
                host: value,
                host_name: None,
                user: None,
                port: None,
                identity_file: None,
                proxy_jump: None,
                proxy_command: None,
            });
            continue;
        }

        let Some(entry) = current.as_mut() else {
            continue;
        };

        match key.as_str() {
            "hostname" => entry.host_name = Some(value),
            "user" => entry.user = Some(value),
            "port" => entry.port = value.parse::<u16>().ok(),
            "identityfile" => entry.identity_file = Some(expand_tilde(&value)),
            "proxyjump" => entry.proxy_jump = Some(value),
            "proxycommand" => entry.proxy_command = Some(value),
            _ => {}
        }
    }

    if let Some(prev) = current {
        if current_is_wildcard {
            if wildcard_defaults.identity_file.is_none() {
                wildcard_defaults.identity_file = prev.identity_file;
            }
            if wildcard_defaults.user.is_none() {
                wildcard_defaults.user = prev.user;
            }
            if wildcard_defaults.port.is_none() {
                wildcard_defaults.port = prev.port;
            }
            if wildcard_defaults.proxy_jump.is_none() {
                wildcard_defaults.proxy_jump = prev.proxy_jump;
            }
            if wildcard_defaults.proxy_command.is_none() {
                wildcard_defaults.proxy_command = prev.proxy_command;
            }
        } else {
            entries.push(prev);
        }
    }

    // Apply wildcard defaults to fields not explicitly set in each host entry.
    for entry in &mut entries {
        if entry.identity_file.is_none() {
            entry.identity_file = wildcard_defaults.identity_file.clone();
        }
        if entry.user.is_none() {
            entry.user = wildcard_defaults.user.clone();
        }
        if entry.port.is_none() {
            entry.port = wildcard_defaults.port;
        }
        if entry.proxy_jump.is_none() {
            entry.proxy_jump = wildcard_defaults.proxy_jump.clone();
        }
        if entry.proxy_command.is_none() {
            entry.proxy_command = wildcard_defaults.proxy_command.clone();
        }
    }

    entries.sort_by(|a, b| a.host.cmp(&b.host));
    Ok(entries)
}
