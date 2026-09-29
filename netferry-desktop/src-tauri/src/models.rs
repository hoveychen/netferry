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

/// Carries the first SSH hop over fectun (FEC over UDP). The tunnel brings
/// the fectun daemon up on the hop's host over SSH and learns its key, so
/// only the UDP `port` is configured. 0 in k/m/rate means the fectun default.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FectunConfig {
    pub port: u16,
    #[serde(default)]
    pub k: u32,
    #[serde(default)]
    pub m: u32,
    #[serde(default)]
    pub rate_mbps: f64,
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
    /// None = plain TCP first hop.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fectun: Option<FectunConfig>,
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
    /// When set, the tunnel also serves SOCKS5 (TCP + UDP) / an HTTP proxy on
    /// 0.0.0.0:<port> so other LAN devices can route through it by configuring
    /// a proxy. None = off.
    #[serde(default)]
    pub lan_socks5_port: Option<u16>,
    #[serde(default)]
    pub lan_http_port: Option<u16>,
}

/// RouteMode as persisted in the global rule set (`rules.json`).
///
/// `kind` is always one of:
///   - "tunnel"  : route through the (single) connected tunnel
///   - "direct"  : bypass the tunnel, direct-dial
///   - "blocked" : reject the connection
///
/// Serialized as `{"kind":"..."}`. Deserialization is lenient for legacy data:
/// `"default"`, `{"kind":"tunnel","profileId":..}`, bare strings, and unknown or
/// empty values all normalise (unknown → tunnel).
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(from = "RawRouteMode")]
pub struct RouteMode {
    pub kind: String,
}

impl RouteMode {
    pub fn tunnel() -> Self {
        Self::from_kind("tunnel")
    }

    /// Normalise any legacy/unknown kind string to tunnel/direct/blocked.
    pub fn from_kind(kind: &str) -> Self {
        let k = match kind.trim().to_ascii_lowercase().as_str() {
            "direct" => "direct",
            "blocked" => "blocked",
            _ => "tunnel",
        };
        Self { kind: k.to_string() }
    }

    /// Fallback routes may only be tunnel or direct; blocked is coerced to tunnel.
    pub fn as_final(&self) -> Self {
        if self.kind == "direct" {
            self.clone()
        } else {
            Self::tunnel()
        }
    }
}

impl Default for RouteMode {
    fn default() -> Self {
        Self::tunnel()
    }
}

#[derive(Deserialize)]
#[serde(untagged)]
enum RawRouteMode {
    Str(String),
    Obj {
        #[serde(default)]
        kind: Option<String>,
    },
    Other(#[allow(dead_code)] serde_json::Value),
}

impl From<RawRouteMode> for RouteMode {
    fn from(raw: RawRouteMode) -> Self {
        match raw {
            RawRouteMode::Str(s) => RouteMode::from_kind(&s),
            RawRouteMode::Obj { kind } => RouteMode::from_kind(kind.as_deref().unwrap_or("")),
            RawRouteMode::Other(_) => RouteMode::tunnel(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RuleGroup {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub domains: Vec<String>,
    pub route: RouteMode,
}

/// A ProfileGroup is a folder of profile-id references; it has nothing to do
/// with routing (rules are global, see `rules.rs`). Profile objects live in
/// `profiles.json`. Legacy group files may still carry `rules`, `ruleGroups`,
/// `finalRoute` and `knownHosts`; those are ignored here (read once by the
/// rules.json migration) and disappear on the next save.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProfileGroup {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub children_ids: Vec<String>,
    /// Legacy pre-B field: full Profile objects embedded in the group. Accepted
    /// on read for backward compat; `normalize_legacy()` extracts ids and this
    /// field is never written back.
    #[serde(default, skip_serializing, rename = "children")]
    pub legacy_children: Vec<Profile>,
    #[serde(default)]
    pub priorities: std::collections::HashMap<String, i32>,
}

impl ProfileGroup {
    /// Populate `children_ids` from `legacy_children` if the group was loaded
    /// from the pre-B on-disk format. Returns true if migration happened, so
    /// callers can persist the normalised form.
    pub fn normalize_legacy(&mut self) -> bool {
        let migrated = self.children_ids.is_empty() && !self.legacy_children.is_empty();
        if migrated {
            self.children_ids = self.legacy_children.iter().map(|p| p.id.clone()).collect();
        }
        let had_legacy = !self.legacy_children.is_empty();
        self.legacy_children.clear();
        migrated || had_legacy
    }
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
            fectun: None,
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
