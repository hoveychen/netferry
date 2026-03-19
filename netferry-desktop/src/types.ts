export type DnsMode = "off" | "all" | "specific";

export interface Profile {
  id: string;
  name: string;
  remote: string;
  identityFile: string;
  subnets: string[];
  dns: DnsMode;
  excludeSubnets: string[];
  autoNets: boolean;
  dnsTarget?: string;
  method: string;
  remotePython?: string;
  extraSshOptions?: string;
  disableIpv6: boolean;
  notes?: string;
  autoExcludeLan: boolean;
  latencyBufferSize?: number;
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
  state: "disconnected" | "connecting" | "connected" | "error";
  profileId?: string;
  message?: string;
}

export interface TunnelStats {
  rxBytesPerSec: number;
  txBytesPerSec: number;
  totalRxBytes: number;
  totalTxBytes: number;
}

export interface ConnectionEvent {
  srcAddr: string;
  dstAddr: string;
  timestampMs: number;
}

export interface TunnelError {
  message: string;
  timestampMs: number;
}
