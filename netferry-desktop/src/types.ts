export type DnsMode = "off" | "all" | "specific";

export type MethodFeature = "ipv6" | "udp" | "dns" | "portRange";

/** Maps method name → list of supported features, as reported by the tunnel binary. */
export type MethodFeatures = Record<string, MethodFeature[]>;

export interface JumpHost {
  remote: string;
  identityFile?: string;
  identityKey?: string;
}

export interface Profile {
  id: string;
  name: string;
  remote: string;
  identityFile: string;
  identityKey?: string;
  jumpHosts?: JumpHost[];
  subnets: string[];
  dns: DnsMode;
  excludeSubnets: string[];
  autoNets: boolean;
  dnsTarget?: string;
  method: string;
  remotePython?: string;
  extraSshOptions?: string;
  disableIpv6: boolean;
  enableUdp: boolean;
  blockUdp: boolean;
  notes?: string;
  autoExcludeLan: boolean;
  poolSize: number;
  splitConn: boolean;
  tcpBalanceMode?: "round-robin" | "least-loaded";
  latencyBufferSize?: number;
  imported?: boolean;
}

export type TrayDisplayMode = "speed" | "connections" | "none";

export interface GlobalSettings {
  autoConnectProfileId: string | null;
  trayDisplayMode: TrayDisplayMode;
}

export interface SshHostEntry {
  host: string;
  hostName?: string;
  user?: string;
  port?: number;
  identityFile?: string;
  proxyJump?: string;
  proxyCommand?: string;
}

export interface ConnectionStatus {
  state: "disconnected" | "connecting" | "connected" | "reconnecting" | "error";
  profileId?: string;
  message?: string;
}

export interface TunnelSnapshot {
  index: number;           // 1-based pool member index
  state: "alive" | "reconnecting" | "dead";
  rxBytesPerSec: number;
  txBytesPerSec: number;
  activeConns: number;
  totalConns: number;
  lastRttUs: number;       // SSH keepalive RTT in µs (0 = not yet measured)
  minRttUs: number;        // min RTT over recent ~5 min window in µs (network floor)
  maxRttUs: number;        // max RTT in µs
  jitterUs: number;        // |last - prev| in µs
  congestionScore: number; // streams × (1 + rtt_ms/50); lower = less loaded
}

export interface TunnelStats {
  rxBytesPerSec: number;
  txBytesPerSec: number;
  totalRxBytes: number;
  totalTxBytes: number;
  activeConns: number;
  totalConns: number;
  dnsQueries: number;
  tunnels?: TunnelSnapshot[]; // per-pool-member stats; absent when pool size == 1
}

export interface ConnectionEvent {
  id: number;
  action: "open" | "close";
  srcAddr: string;
  dstAddr: string;
  host?: string;
  tunnelIndex?: number; // 1-based pool member; 0 or absent = single tunnel
  timestampMs: number;
}

export interface DestinationSnapshot {
  host: string;          // hostname or IP
  activeConns: number;   // currently open connections
  totalConns: number;    // all-time connections opened
  rxBytes: number;       // cumulative bytes downloaded
  txBytes: number;       // cumulative bytes uploaded
  rxBytesPerSec: number; // download speed
  txBytesPerSec: number; // upload speed
  firstSeenMs: number;   // timestamp of first connection
  lastSeenMs: number;    // timestamp of last activity
  priority: number;      // 1=low, 3=normal, 5=high
  route: RouteMode;      // tunnel, direct, or blocked
  processNames?: string[]; // local processes that connected to this destination
}

export type RouteMode = "tunnel" | "direct" | "blocked";

/** Map of destination host → priority (1–5). Only non-default entries are stored. */
export type DestinationPriorities = Record<string, number>;

/** Map of destination host → route mode. Only non-default entries are stored. */
export type DestinationRoutes = Record<string, RouteMode>;

export interface TunnelError {
  message: string;
  timestampMs: number;
}

export interface DeployProgress {
  sent: number;
  total: number;
}

export interface UpdateInfo {
  has_update: boolean;
  latest_version: string;
  current_version: string;
  release_url: string;
  release_notes: string;
}
