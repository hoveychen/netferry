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
    let mut current: Option<SshHostEntry> = None;

    for line in raw.lines() {
        let Some((key, value)) = parse_kv(line) else {
            continue;
        };
        if key == "host" {
            if let Some(prev) = current.take() {
                if !prev.host.contains('*') && !prev.host.contains('?') {
                    entries.push(prev);
                }
            }
            current = Some(SshHostEntry {
                host: value.clone(),
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
        if !prev.host.contains('*') && !prev.host.contains('?') {
            entries.push(prev);
        }
    }

    entries.sort_by(|a, b| a.host.cmp(&b.host));
    Ok(entries)
}
