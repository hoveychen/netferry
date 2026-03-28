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

export interface TunnelStats {
  rxBytesPerSec: number;
  txBytesPerSec: number;
  totalRxBytes: number;
  totalTxBytes: number;
  activeConns: number;
  totalConns: number;
  dnsQueries: number;
}

export interface ConnectionEvent {
  id: number;
  action: "open" | "close";
  srcAddr: string;
  dstAddr: string;
  host?: string;
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
