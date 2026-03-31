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

export interface GlobalSettings {
  autoConnectProfileId: string | null;
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
  index: number;            // 1-based pool member index
  rxBytesPerSec: number;
  txBytesPerSec: number;
  activeConns: number;
  totalConns: number;
  lastKeepaliveRtt: number; // SSH keepalive RTT in ms (0 = not yet measured)
  maxKeepaliveRtt: number;  // max RTT seen on this tunnel in ms
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

export interface TunnelError {
  message: string;
  timestampMs: number;
}

export interface DeployProgress {
  sent: number;
  total: number;
}
