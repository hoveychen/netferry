use serde::{Deserialize, Serialize};

fn default_auto_exclude_lan() -> bool {
    true
}

fn default_block_udp() -> bool {
    true
}

fn default_pool_size() -> u32 {
    4
}

fn default_tcp_balance_mode() -> String {
    "least-loaded".to_string()
}

fn default_latency_buffer_size() -> Option<u32> {
    Some(2097152)
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct JumpHost {
    pub remote: String,
    #[serde(default)]
    pub identity_file: Option<String>,
    #[serde(default)]
    pub identity_key: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Profile {
    pub id: String,
    pub name: String,
    // color and autoConnect are legacy fields kept for deserialization compat only.
    #[allow(dead_code)]
    #[serde(default, skip_serializing)]
    pub color: Option<String>,
    #[allow(dead_code)]
    #[serde(default, skip_serializing)]
    pub auto_connect: Option<bool>,
    pub remote: String,
    pub identity_file: String,
    #[serde(default)]
    pub identity_key: Option<String>,
    #[serde(default)]
    pub jump_hosts: Vec<JumpHost>,
    pub subnets: Vec<String>,
    pub dns: DnsMode,
    pub exclude_subnets: Vec<String>,
    pub auto_nets: bool,
    pub dns_target: Option<String>,
    pub method: String,
    pub remote_python: Option<String>,
    pub extra_ssh_options: Option<String>,
    pub disable_ipv6: bool,
    #[serde(default)]
    pub enable_udp: bool,
    #[serde(default = "default_block_udp")]
    pub block_udp: bool,
    pub notes: Option<String>,
    #[serde(default = "default_auto_exclude_lan")]
    pub auto_exclude_lan: bool,
    #[serde(default = "default_pool_size")]
    pub pool_size: u32,
    #[serde(default)]
    pub split_conn: bool,
    #[serde(default = "default_tcp_balance_mode")]
    pub tcp_balance_mode: String,
    #[serde(default = "default_latency_buffer_size")]
    pub latency_buffer_size: Option<u32>,
    #[serde(default)]
    pub imported: bool,
}

fn default_tray_display_mode() -> String {
    "speed".to_string()
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct GlobalSettings {
    pub auto_connect_profile_id: Option<String>,
    #[serde(default = "default_tray_display_mode")]
    pub tray_display_mode: String,
    // P1: id of the currently active ProfileGroup. Populated by migrate_v2 on
    // first launch after upgrade. Runtime still operates in single-profile mode.
    #[serde(default)]
    pub active_group_id: Option<String>,
}

/// RouteMode as persisted inside a ProfileGroup's `rules` map.
///
/// `kind`:
///   - "tunnel"  : route through `profile_id`'s child tunnel
///   - "default" : route through `children[0]` of the owning group
///   - "direct"  : bypass the tunnel, direct-dial
///   - "blocked" : reject the connection
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RouteMode {
    pub kind: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub profile_id: Option<String>,
}

/// A ProfileGroup bundles an ordered list of child profiles with a set of
/// destination rules. `children[0]` is the group's default profile. One group
/// is active at a time (see `GlobalSettings.active_group_id`).
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProfileGroup {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub children: Vec<Profile>,
    #[serde(default)]
    pub rules: std::collections::HashMap<String, RouteMode>,
    #[serde(default)]
    pub priorities: std::collections::HashMap<String, i32>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum DnsMode {
    Off,
    All,
    Specific,
}

impl Default for Profile {
    fn default() -> Self {
        Self {
            id: String::new(),
            name: "New Profile".to_string(),
            color: None,
            auto_connect: None,
            remote: String::new(),
            identity_file: String::new(),
            identity_key: None,
            jump_hosts: Vec::new(),
            subnets: vec!["0.0.0.0/0".to_string()],
            dns: DnsMode::All,
            exclude_subnets: Vec::new(),
            auto_nets: false,
            dns_target: None,
            method: "auto".to_string(),
            remote_python: None,
            extra_ssh_options: None,
            disable_ipv6: false,
            enable_udp: false,
            block_udp: true,
            notes: None,
            auto_exclude_lan: true,
            pool_size: 4,
            split_conn: false,
            tcp_balance_mode: "least-loaded".to_string(),
            latency_buffer_size: Some(2097152),
            imported: false,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SshHostEntry {
    pub host: String,
    pub host_name: Option<String>,
    pub user: Option<String>,
    pub port: Option<u16>,
    pub identity_file: Option<String>,
    pub proxy_jump: Option<String>,
    pub proxy_command: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ConnectionStatus {
    pub state: String,
    pub profile_id: Option<String>,
    pub message: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TunnelError {
    pub message: String,
    pub timestamp_ms: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DeployProgress {
    pub sent: u64,
    pub total: u64,
}

pub fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}
