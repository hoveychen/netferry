export type DnsMode = "off" | "all" | "specific";

export interface Profile {
  id: string;
  name: string;
  color: string;
  remote: string;
  identityFile: string;
  subnets: string[];
  dns: DnsMode;
  autoConnect: boolean;
  excludeSubnets: string[];
  autoNets: boolean;
  dnsTarget?: string;
  method: string;
  remotePython?: string;
  extraSshOptions?: string;
  disableIpv6: boolean;
  notes?: string;
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
