export type DnsMode = "off" | "all" | "specific";

export type MethodFeature = "ipv6" | "udp" | "dns" | "portRange";

/** Maps method name → list of supported features, as reported by the tunnel binary. */
export type MethodFeatures = Record<string, MethodFeature[]>;

export interface JumpHost {
  remote: string;
  identityFile?: string;
  identityKey?: string;
}

/** Carry the first SSH hop over fectun (FEC over UDP). 0 in k/m/rateMbps = fectun default. */
export interface FectunConfig {
  port: number;
  k: number;
  m: number;
  rateMbps: number;
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
  /** undefined = plain TCP first hop. */
  fectun?: FectunConfig;
  tcpBalanceMode?: "round-robin" | "least-loaded";
  latencyBufferSize?: number;
  imported?: boolean;
}

export type TrayDisplayMode = "speed" | "connections" | "none";

export interface GlobalSettings {
  autoConnectProfileId: string | null;
  trayDisplayMode: TrayDisplayMode;
  /** P1: set by migrate_v2 on first launch. Runtime still single-profile until P2/P3. */
  activeGroupId?: string | null;
  /** Also serve SOCKS5 (TCP + UDP) / an HTTP proxy on 0.0.0.0:<port> for other LAN devices. null = off. */
  lanSocks5Port?: number | null;
  lanHttpPort?: number | null;
}

// ── Routing rules: one global RuleSet, independent of profile groups ──

/** Route decision persisted in the RuleSet. The backend normalizes legacy
 *  `default` / `tunnel:<profileId>` values to `tunnel` on read. */
export interface RouteRule {
  kind: RouteMode;
}

/** The catch-all route (Clash's MATCH) is limited to tunnel or direct. */
export type FinalRoute = { kind: "tunnel" | "direct" };

export interface RuleGroup {
  id: string;
  name: string;
  /** A domain includes its apex and subdomains; =host matches exactly. */
  domains: string[];
  route: RouteRule;
}

/** The single global rule set (`rules.json`), applied whichever profile is connected. */
export interface RuleSet {
  /** Per-host overrides (host, IP or `*.suffix`); evaluated before rule groups. */
  rules: Record<string, RouteRule>;
  /** Ordered; the first group with a matching domain wins. */
  ruleGroups: RuleGroup[];
  /** Route for traffic no override or rule group matches. */
  finalRoute: FinalRoute;
  /** Most recently observed hosts/IPs (capped at 1000, newest last). Accumulated
   *  from SSE destination snapshots so DestinationsPage can surface hosts
   *  across sessions. */
  knownHosts: string[];
}

/** A folder of profiles. Has nothing to do with routing rules. */
export interface ProfileGroup {
  id: string;
  name: string;
  /** Ordered profile-id references. Profile objects live in `profiles.json`. */
  childrenIds: string[];
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
