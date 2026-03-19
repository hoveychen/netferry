export type RegionInfo =
  | { type: "lan" }
  | { type: "loopback" }
  | { type: "country"; countryCode: string }
  | { type: "unknown" };

const CACHE_KEY_PREFIX = "geoip_";
const CACHE_TTL_MS = 7 * 24 * 60 * 60 * 1000; // 7 days

interface CacheEntry {
  info: RegionInfo;
  timestamp: number;
}

function ip2num(ip: string): number {
  return ip.split(".").reduce((acc, octet) => ((acc << 8) | parseInt(octet, 10)) >>> 0, 0);
}

const PRIVATE_RANGES: [number, number][] = [
  [ip2num("10.0.0.0"), ip2num("10.255.255.255")],
  [ip2num("172.16.0.0"), ip2num("172.31.255.255")],
  [ip2num("192.168.0.0"), ip2num("192.168.255.255")],
  [ip2num("169.254.0.0"), ip2num("169.254.255.255")],
];
const LOOPBACK_START = ip2num("127.0.0.0");
const LOOPBACK_END = ip2num("127.255.255.255");

function isIpv4(host: string): boolean {
  return /^(\d{1,3}\.){3}\d{1,3}$/.test(host);
}

function classifyIpv4(host: string): RegionInfo | null {
  if (!isIpv4(host)) return null;
  const n = ip2num(host);
  if (n >= LOOPBACK_START && n <= LOOPBACK_END) return { type: "loopback" };
  for (const [s, e] of PRIVATE_RANGES) {
    if (n >= s && n <= e) return { type: "lan" };
  }
  return null;
}

function isLocalHostname(host: string): boolean {
  return (
    host === "localhost" ||
    host.endsWith(".local") ||
    host.endsWith(".lan") ||
    host.endsWith(".internal")
  );
}

/** Extract the host portion from an SSH remote string (user@host or user@host:port). */
export function parseHost(remote: string): string | null {
  const at = remote.indexOf("@");
  if (at === -1) return null;
  let host = remote.slice(at + 1);
  const colon = host.lastIndexOf(":");
  if (colon !== -1) host = host.slice(0, colon);
  return host.trim() || null;
}

/** Convert ISO 3166-1 alpha-2 country code to flag emoji. */
export function countryCodeToFlag(code: string): string {
  return [...code.toUpperCase()]
    .map((c) => String.fromCodePoint(0x1f1e6 + c.charCodeAt(0) - 65))
    .join("");
}

function getCached(host: string): RegionInfo | null {
  const raw = localStorage.getItem(CACHE_KEY_PREFIX + host);
  if (!raw) return null;
  try {
    const entry: CacheEntry = JSON.parse(raw);
    if (Date.now() - entry.timestamp < CACHE_TTL_MS) return entry.info;
    localStorage.removeItem(CACHE_KEY_PREFIX + host);
  } catch {
    // ignore malformed cache
  }
  return null;
}

function setCache(host: string, info: RegionInfo): void {
  try {
    const entry: CacheEntry = { info, timestamp: Date.now() };
    localStorage.setItem(CACHE_KEY_PREFIX + host, JSON.stringify(entry));
  } catch {
    // ignore storage errors (e.g. quota exceeded)
  }
}

/**
 * Resolve region info for an SSH remote string.
 * Uses localStorage for a 7-day cache to avoid repeated API calls.
 */
export async function getRegionInfo(remote: string): Promise<RegionInfo> {
  const host = parseHost(remote);
  if (!host) return { type: "unknown" };

  // Fast local classification — no network needed
  if (isLocalHostname(host)) return { type: "lan" };
  const ipClass = classifyIpv4(host);
  if (ipClass) return ipClass;

  // Cache hit
  const cached = getCached(host);
  if (cached) return cached;

  // Remote lookup via ipwho.is (free, HTTPS, supports hostnames)
  try {
    const res = await fetch(`https://ipwho.is/${encodeURIComponent(host)}`);
    const data = await res.json();
    const info: RegionInfo =
      data.success && data.country_code
        ? { type: "country", countryCode: data.country_code }
        : { type: "unknown" };
    setCache(host, info);
    return info;
  } catch {
    return { type: "unknown" };
  }
}
