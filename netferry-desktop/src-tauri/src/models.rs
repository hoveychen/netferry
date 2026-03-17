use serde::{Deserialize, Serialize};

fn default_auto_exclude_lan() -> bool {
    true
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Profile {
    pub id: String,
    pub name: String,
    pub color: String,
    pub remote: String,
    pub identity_file: String,
    pub subnets: Vec<String>,
    pub dns: DnsMode,
    pub auto_connect: bool,
    pub exclude_subnets: Vec<String>,
    pub auto_nets: bool,
    pub dns_target: Option<String>,
    pub method: String,
    pub remote_python: Option<String>,
    pub extra_ssh_options: Option<String>,
    pub disable_ipv6: bool,
    pub notes: Option<String>,
    #[serde(default = "default_auto_exclude_lan")]
    pub auto_exclude_lan: bool,
    pub latency_buffer_size: Option<u32>,
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
            color: "slate".to_string(),
            remote: String::new(),
            identity_file: String::new(),
            subnets: vec!["0.0.0.0/0".to_string()],
            dns: DnsMode::Off,
            auto_connect: false,
            exclude_subnets: Vec::new(),
            auto_nets: false,
            dns_target: None,
            method: "auto".to_string(),
            remote_python: None,
            extra_ssh_options: None,
            disable_ipv6: false,
            notes: None,
            auto_exclude_lan: true,
            latency_buffer_size: None,
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
pub struct TunnelStats {
    pub rx_bytes_per_sec: u64,
    pub tx_bytes_per_sec: u64,
    pub total_rx_bytes: u64,
    pub total_tx_bytes: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ConnectionEvent {
    pub src_addr: String,
    pub dst_addr: String,
    pub timestamp_ms: u64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct TunnelError {
    pub message: String,
    pub timestamp_ms: u64,
}

pub fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64
}
