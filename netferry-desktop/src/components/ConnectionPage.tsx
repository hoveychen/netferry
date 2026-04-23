import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ActiveConnection } from "@/stores/connectionStore";
import { joinGroupProfiles } from "@/stores/groupStore";
import { useProfileStore } from "@/stores/profileStore";
import type { ConnectionEvent, ConnectionStatus, DeployProgress, DestinationSnapshot, Profile, ProfileGroup, TunnelError, TunnelSnapshot, TunnelStats } from "@/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { countryCodeToFlag, getRegionInfo, type RegionInfo } from "@/lib/geoip";
import { tunnelColor } from "@/lib/tunnelColor";

interface Props {
  status: ConnectionStatus;
  activeProfile: Profile | null;
  /**
   * Currently active profile group, if any. When present with 2+ children we
   * render a per-profile summary row. When null/single-child we fall back to
   * the legacy single-profile layout.
   */
  activeGroup?: ProfileGroup | null;
  logs: string[];
  tunnelStats: TunnelStats | null;
  activeConnections: Map<number, ActiveConnection>;
  recentClosed: ConnectionEvent[];
  tunnelErrors: TunnelError[];
  destinations: DestinationSnapshot[];
  deployProgress: DeployProgress | null;
  deployReason: string | null;
  onDisconnect: () => Promise<void>;
}

type Tab = "speed" | "connections" | "destinations" | "logs" | "errors";

type DestSortKey = "totalBytes" | "activeConns" | "totalConns" | "rxSpeed" | "lastSeen";

interface SpeedPoint {
  rx: number;
  tx: number;
  t: number;
}

const MAX_HISTORY = 60;

function useContainerSize(ref: React.RefObject<HTMLDivElement | null>) {
  const [size, setSize] = useState({ width: 0, height: 0 });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => {
      const { width, height } = entry.contentRect;
      setSize({ width: Math.round(width), height: Math.round(height) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [ref]);
  return size;
}

const CHART_HEIGHT = 200;
const PAD = { top: 16, right: 16, bottom: 28, left: 56 };

function SpeedChart({ history }: { history: SpeedPoint[] }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const { width: W } = useContainerSize(containerRef);
  const H = CHART_HEIGHT;

  const innerW = Math.max(W - PAD.left - PAD.right, 0);
  const innerH = H - PAD.top - PAD.bottom;

  const allVals = history.flatMap((p) => [p.rx, p.tx]);
  const maxVal = Math.max(...allVals, 1024);
  const yMax = Math.ceil(maxVal / 1024) * 1024;

  const yScale = useCallback((v: number) => {
    return PAD.top + innerH - (v / yMax) * innerH;
  }, [innerH, yMax]);

  const xScale = useCallback((i: number) => {
    const n = Math.max(history.length - 1, 1);
    return PAD.left + (i / n) * innerW;
  }, [history.length, innerW]);

  function toPath(vals: number[]) {
    if (vals.length < 2) return "";
    return vals
      .map((v, i) => `${i === 0 ? "M" : "L"}${xScale(i).toFixed(1)},${yScale(v).toFixed(1)}`)
      .join(" ");
  }

  function toFill(vals: number[]) {
    if (vals.length < 2) return "";
    const line = toPath(vals);
    const last = vals.length - 1;
    return `${line} L${xScale(last).toFixed(1)},${(PAD.top + innerH).toFixed(1)} L${PAD.left.toFixed(1)},${(PAD.top + innerH).toFixed(1)} Z`;
  }

  const rxVals = history.map((p) => p.rx);
  const txVals = history.map((p) => p.tx);

  const yTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => ({
    v: yMax * f,
    y: yScale(yMax * f),
  }));

  // Compute x-axis tick interval based on available width
  const xTickInterval = innerW > 400 ? 10 : innerW > 200 ? 15 : 20;
  const xTicks = history
    .map((p, i) => ({ i, t: p.t }))
    .filter((_, i) => i % xTickInterval === 0);

  return (
    <div ref={containerRef} className="w-full" style={{ height: H }}>
      {W > 0 && (
        <svg width={W} height={H}>
          <defs>
            <linearGradient id="rxGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--success)" stopOpacity="0.25" />
              <stop offset="100%" stopColor="var(--success)" stopOpacity="0" />
            </linearGradient>
            <linearGradient id="txGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.20" />
              <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
            </linearGradient>
            <clipPath id="chart-clip">
              <rect x={PAD.left} y={PAD.top} width={innerW} height={innerH} />
            </clipPath>
          </defs>

          {yTicks.map(({ v, y }) => (
            <line
              key={v}
              x1={PAD.left} y1={y} x2={PAD.left + innerW} y2={y}
              stroke="var(--chart-grid)" strokeWidth="1"
            />
          ))}

          {yTicks.map(({ v, y }) => (
            <text
              key={v}
              x={PAD.left - 6} y={y + 4}
              textAnchor="end" fontSize="10"
              fill="var(--chart-label)" fontFamily="monospace"
            >
              {formatBytes(v)}
            </text>
          ))}

          {xTicks.map(({ i, t }) => (
            <text
              key={i}
              x={xScale(i)} y={PAD.top + innerH + 14}
              textAnchor="middle" fontSize="10"
              fill="var(--chart-label-dim)" fontFamily="monospace"
            >
              {new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
            </text>
          ))}

          <g clipPath="url(#chart-clip)">
            {rxVals.length >= 2 && <path d={toFill(rxVals)} fill="url(#rxGrad)" />}
            {txVals.length >= 2 && <path d={toFill(txVals)} fill="url(#txGrad)" />}
            {rxVals.length >= 2 && (
              <path d={toPath(rxVals)} fill="none" stroke="var(--success)" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
            )}
            {txVals.length >= 2 && (
              <path d={toPath(txVals)} fill="none" stroke="var(--accent)" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
            )}
            {rxVals.length >= 1 && (
              <circle cx={xScale(rxVals.length - 1)} cy={yScale(rxVals[rxVals.length - 1])} r="3" fill="var(--success)" />
            )}
            {txVals.length >= 1 && (
              <circle cx={xScale(txVals.length - 1)} cy={yScale(txVals[txVals.length - 1])} r="3" fill="var(--accent)" />
            )}
          </g>

          <rect
            x={PAD.left} y={PAD.top} width={innerW} height={innerH}
            fill="none" stroke="var(--chart-grid)" strokeWidth="1"
          />
        </svg>
      )}
    </div>
  );
}

function statusVariant(state: ConnectionStatus["state"]) {
  if (state === "connected") return "green";
  if (state === "connecting") return "warning";
  if (state === "reconnecting") return "warning";
  if (state === "error") return "red";
  return "gray";
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

function formatTime(ms: number): string {
  return new Date(ms).toLocaleTimeString();
}

const AVATAR_GRADIENTS = [
  "from-blue-500 to-indigo-600",
  "from-teal-500 to-cyan-600",
  "from-violet-500 to-purple-600",
  "from-rose-500 to-pink-600",
  "from-amber-500 to-orange-600",
  "from-emerald-500 to-green-600",
];

/** Format RTT from microseconds to a human-readable string. */
function formatRtt(us: number): string {
  if (us === 0) return "—";
  if (us < 1000) return `${us}µs`;
  return `${(us / 1000).toFixed(us < 10000 ? 1 : 0)}ms`;
}

/** RTT color class based on microseconds: green < 50ms, yellow < 200ms, red >= 200ms */
function rttColor(us: number): string {
  if (us === 0) return "text-t4";
  if (us >= 200_000) return "text-danger";
  if (us >= 50_000) return "text-c-yellow";
  return "text-t3";
}

/**
 * Diagnose tunnel health from RTT metrics and return a short human-readable
 * label + colour.
 */
function useDiagnoseTunnel() {
  const { t } = useTranslation();
  return (tunnel: TunnelSnapshot): { label: string; color: string } => {
    if (tunnel.lastRttUs === 0) return { label: t("connection.diagnosis.waitingForData"), color: "text-t4" };

    const lastMs = tunnel.lastRttUs / 1000;
    const minMs = tunnel.minRttUs / 1000;
    const jitterMs = tunnel.jitterUs / 1000;
    const inflation = minMs > 0 ? lastMs / minMs : 1;

    if (jitterMs > 30 && inflation > 2)
      return { label: t("connection.diagnosis.congestion"), color: "text-danger" };
    if (jitterMs > 30)
      return { label: t("connection.diagnosis.unstable"), color: "text-warning" };
    if (inflation > 3 && jitterMs < 15)
      return { label: t("connection.diagnosis.bufferbloat", { inflation: inflation.toFixed(0) }), color: "text-danger" };
    if (inflation > 1.8 && jitterMs < 15)
      return { label: t("connection.diagnosis.possibleBufferbloat", { inflation: inflation.toFixed(1) }), color: "text-c-yellow" };
    if (minMs > 150)
      return { label: t("connection.diagnosis.highLatency"), color: "text-c-yellow" };

    return { label: t("connection.diagnosis.healthy"), color: "text-success" };
  };
}

function TunnelBreakdown({ tunnels }: { tunnels: TunnelSnapshot[] }) {
  const { t } = useTranslation();
  const diagnoseTunnel = useDiagnoseTunnel();

  if (tunnels.length === 0) return null;
  const maxScore = Math.max(...tunnels.map((t) => t.congestionScore), 1);
  return (
    <div className="mt-4 grid gap-2" style={{ gridTemplateColumns: `repeat(${Math.min(tunnels.length, 4)}, minmax(0, 1fr))` }}>
      {tunnels.map((tun) => {
        const c = tunnelColor(tun.index);
        const congFrac = maxScore > 0 ? tun.congestionScore / maxScore : 0;
        const congColor = congFrac > 0.8 ? "var(--danger)" : congFrac > 0.5 ? "var(--c-yellow)" : "var(--success)";
        const diag = diagnoseTunnel(tun);
        return (
          <div key={tun.index} className={`rounded-xl border px-3 py-2.5 ${
            tun.state === "dead" ? "border-danger/20 bg-danger/[0.06] opacity-50" :
            tun.state === "reconnecting" ? "border-warning/20 bg-warning/[0.06]" :
            "border-sep bg-ov-4"
          }`}>
            <div className="flex items-center gap-1.5 mb-1.5">
              {tun.state === "alive" ? (
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              ) : tun.state === "reconnecting" ? (
                <span className="h-1.5 w-1.5 rounded-full bg-warning animate-pulse" />
              ) : (
                <span className="h-1.5 w-1.5 rounded-full bg-danger" />
              )}
              <span className={`text-[11px] font-semibold uppercase tracking-wider ${
                tun.state === "dead" ? "text-danger" :
                tun.state === "reconnecting" ? "text-warning" : c.text
              }`}>
                {t("connection.tunnel", { index: tun.index })}
              </span>
              {tun.state !== "alive" && (
                <span className={`text-[10px] ${tun.state === "dead" ? "text-danger/60" : "text-warning/60"}`}>
                  {tun.state === "reconnecting" ? t("connection.reconnecting") : t("connection.dead")}
                </span>
              )}
            </div>
            {tun.state === "dead" ? (
              <p className="text-[10px] text-danger/60">{t("connection.reconnectionFailed")}</p>
            ) : (
            <div className="flex flex-col gap-0.5">
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">↓</span>
                <span className={`font-mono text-xs font-semibold ${c.text}`}>{formatBytes(tun.rxBytesPerSec)}/s</span>
              </div>
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">↑</span>
                <span className="font-mono text-xs text-t3">{formatBytes(tun.txBytesPerSec)}/s</span>
              </div>
              <div className="flex justify-between items-baseline mt-0.5">
                <span className="text-[10px] text-t4">{t("connection.conns")}</span>
                <span className="font-mono text-xs text-t3">{tun.activeConns}</span>
              </div>
              {/* RTT row: last (min) */}
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">{t("connection.rtt")}</span>
                <span className={`font-mono text-xs ${rttColor(tun.lastRttUs)}`}>
                  {formatRtt(tun.lastRttUs)}
                  {tun.minRttUs > 0 && tun.minRttUs !== tun.lastRttUs && (
                    <span className="text-t5 ml-0.5">({formatRtt(tun.minRttUs)} min)</span>
                  )}
                </span>
              </div>
              {/* Jitter row */}
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">{t("connection.jitter")}</span>
                <span className={`font-mono text-xs ${tun.jitterUs > 30_000 ? "text-warning" : "text-t3"}`}>
                  {tun.lastRttUs === 0 ? "—" : formatRtt(tun.jitterUs)}
                </span>
              </div>
              {/* Congestion bar */}
              <div className="mt-1.5">
                <div className="flex justify-between items-baseline mb-0.5">
                  <span className="text-[10px] text-t4">{t("connection.load")}</span>
                  <span className="font-mono text-[10px] text-t4">{tun.congestionScore.toFixed(1)}</span>
                </div>
                <div className="h-1 w-full rounded-full bg-ov-8 overflow-hidden">
                  <div
                    className="h-full rounded-full transition-all duration-500"
                    style={{ width: `${Math.min(congFrac * 100, 100)}%`, backgroundColor: congColor }}
                  />
                </div>
              </div>
              {/* Diagnosis */}
              <p className={`mt-1 text-[10px] leading-tight ${diag.color}`}>{diag.label}</p>
            </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

/** Extract display host + scheme from connection info. Prefers the resolved host (SNI/HTTP Host). */
function parseHost(dstAddr: string, resolvedHost?: string): { host: string; port: number; scheme?: string } {
  const lastColon = dstAddr.lastIndexOf(":");
  const addrHost = lastColon > 0 ? dstAddr.slice(0, lastColon) : dstAddr;
  const port = lastColon > 0 ? Number(dstAddr.slice(lastColon + 1)) : 0;
  const scheme = port === 443 ? "https" : port === 80 ? "http" : undefined;
  return { host: resolvedHost || addrHost, port, scheme };
}

// Per-profile summary rendered above the per-tunnel breakdown when the active
// group has multiple profiles. Each card shows profile name + its tunnel stats.
function PerProfileBreakdown({
  profiles,
  tunnels,
  perProfileActiveConns,
}: {
  profiles: Profile[];
  tunnels: TunnelSnapshot[];
  perProfileActiveConns: Map<string, number>;
}) {
  const { t } = useTranslation();

  // Profile-id → TunnelSnapshot mapping. The relay stamps profileId on every
  // TunnelSnapshot; positional alignment is retained as a fallback for older
  // relay builds or if profileId is ever dropped from the wire.
  const snapshotByProfileId = new Map<string, TunnelSnapshot>();
  for (const tun of tunnels) {
    if (tun.profileId) snapshotByProfileId.set(tun.profileId, tun);
  }
  const positional = tunnels.length === profiles.length;

  const cols = Math.min(profiles.length, 4);

  return (
    <div
      className="grid gap-2"
      style={{ gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))` }}
    >
      {profiles.map((profile, idx) => {
        const tun =
          snapshotByProfileId.get(profile.id) ??
          (positional ? tunnels[idx] : undefined);
        const c = tunnelColor(idx + 1);
        const activeConns = perProfileActiveConns.get(profile.id) ?? 0;
        const isDefault = idx === 0;
        const hasTunStats = !!tun;
        return (
          <div
            key={profile.id}
            className="rounded-xl border border-sep bg-ov-4 px-3 py-2.5 shadow-[inset_0_1px_0_var(--inset-highlight)]"
          >
            <div className="flex items-center gap-1.5 mb-1.5">
              <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              <span className={`truncate text-[11px] font-semibold uppercase tracking-wider ${c.text}`}>
                {profile.name}
              </span>
              {isDefault && (
                <span className="ml-auto text-[10px] text-t5">
                  {t("connection.groupDefault", { defaultValue: "default" })}
                </span>
              )}
            </div>
            <div className="flex flex-col gap-0.5">
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">↓</span>
                <span className={`font-mono text-xs font-semibold ${c.text}`}>
                  {hasTunStats ? `${formatBytes(tun!.rxBytesPerSec)}/s` : "—"}
                </span>
              </div>
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">↑</span>
                <span className="font-mono text-xs text-t3">
                  {hasTunStats ? `${formatBytes(tun!.txBytesPerSec)}/s` : "—"}
                </span>
              </div>
              <div className="flex justify-between items-baseline mt-0.5">
                <span className="text-[10px] text-t4">{t("connection.conns")}</span>
                <span className="font-mono text-xs text-t3">{activeConns}</span>
              </div>
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-t4">{t("connection.rtt")}</span>
                <span className={`font-mono text-xs ${hasTunStats ? rttColor(tun!.lastRttUs) : "text-t5"}`}>
                  {hasTunStats ? formatRtt(tun!.lastRttUs) : "—"}
                </span>
              </div>
              {!hasTunStats && (
                <p className="mt-1 text-[10px] leading-tight text-t5">
                  {t("connection.perProfilePending", { defaultValue: "(pending per-profile stats)" })}
                </p>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function ConnectionPage({
  status,
  activeProfile,
  activeGroup,
  logs,
  tunnelStats,
  activeConnections,
  recentClosed,
  tunnelErrors,
  destinations,
  deployProgress,
  deployReason,
  onDisconnect,
}: Props) {
  const { t } = useTranslation();
  const [activeTab, setActiveTab] = useState<Tab>("speed");
  const [disconnecting, setDisconnecting] = useState(false);
  const [speedHistory, setSpeedHistory] = useState<SpeedPoint[]>([]);
  const [destSort, setDestSort] = useState<DestSortKey>("totalBytes");
  const logEndRef = useRef<HTMLDivElement>(null);
  const connEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (tunnelStats) {
      setSpeedHistory((prev) => [
        ...prev.slice(-(MAX_HISTORY - 1)),
        { rx: tunnelStats.rxBytesPerSec, tx: tunnelStats.txBytesPerSec, t: Date.now() },
      ]);
    }
  }, [tunnelStats]);

  useEffect(() => {
    if (activeTab === "logs") {
      logEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [logs, activeTab]);

  const [regionInfo, setRegionInfo] = useState<RegionInfo | undefined>();

  useEffect(() => {
    if (!activeProfile?.remote) {
      setRegionInfo(undefined);
      return;
    }
    let cancelled = false;
    getRegionInfo(activeProfile.remote).then((info) => {
      if (!cancelled) setRegionInfo(info);
    });
    return () => { cancelled = true; };
  }, [activeProfile?.remote]);

  const handleDisconnect = async () => {
    setDisconnecting(true);
    try {
      await onDisconnect();
    } finally {
      setDisconnecting(false);
    }
  };

  const avatarIdx = activeProfile
    ? activeProfile.name.charCodeAt(0) % AVATAR_GRADIENTS.length
    : 0;

  const statCards = tunnelStats
    ? [
        {
          label: t("connection.download"),
          value: formatBytes(tunnelStats.rxBytesPerSec) + "/s",
          sub: t("connection.total", { bytes: formatBytes(tunnelStats.totalRxBytes) }),
          color: "text-success",
          icon: "↓",
        },
        {
          label: t("connection.upload"),
          value: formatBytes(tunnelStats.txBytesPerSec) + "/s",
          sub: t("connection.total", { bytes: formatBytes(tunnelStats.totalTxBytes) }),
          color: "text-accent",
          icon: "↑",
        },
        {
          label: t("connection.connections"),
          value: String(tunnelStats.activeConns),
          sub: t("connection.totalCount", { count: tunnelStats.totalConns }),
          color: "text-t1",
          icon: "⇄",
        },
        {
          label: t("connection.dns"),
          value: String(tunnelStats.dnsQueries),
          sub: t("connection.queries"),
          color: "text-c-purple",
          icon: null,
        },
      ]
    : null;

  const activeConnCount = activeConnections.size;
  const tabs: { id: Tab; label: string; badge?: number }[] = [
    { id: "speed", label: t("connection.speed") },
    { id: "connections", label: t("connection.connections"), badge: activeConnCount || undefined },
    { id: "destinations", label: t("connection.destinations"), badge: destinations.length || undefined },
    { id: "logs", label: t("connection.logs") },
    { id: "errors", label: t("connection.errors"), badge: tunnelErrors.length || undefined },
  ];

  // Resolve the active group's profile-id references to concrete Profile
  // objects. Orphan ids (missing from profileStore) are dropped by the join.
  const profilesSnapshot = useProfileStore((s) => s.profiles);
  const groupChildren = useMemo<Profile[]>(
    () => (activeGroup ? joinGroupProfiles(activeGroup, profilesSnapshot) : []),
    [activeGroup, profilesSnapshot],
  );

  // Multi-profile view is only engaged when the caller passed an active group
  // with 2+ children. Everything else (no group, 1-child group, legacy single-
  // profile mode) uses the original single-profile layout.
  const isMultiProfile = groupChildren.length > 1;

  // Count active connections per profile. Connections without an
  // activeProfileId (e.g. direct-routed or legacy relays) are attributed to
  // the group's default profile (children[0]) so counts still render.
  const perProfileActiveConns = new Map<string, number>();
  if (isMultiProfile) {
    const defaultId = groupChildren[0]?.id ?? "";
    for (const conn of activeConnections.values()) {
      const key = conn.activeProfileId ?? defaultId;
      perProfileActiveConns.set(key, (perProfileActiveConns.get(key) ?? 0) + 1);
    }
  }

  // Pre-group active connections by profile id so the Connections tab can
  // render one section per profile when we are in multi-profile mode.
  const connsByProfile = new Map<string, ActiveConnection[]>();
  if (isMultiProfile) {
    const defaultId = groupChildren[0]?.id ?? "";
    for (const conn of activeConnections.values()) {
      const key = conn.activeProfileId ?? defaultId;
      const list = connsByProfile.get(key);
      if (list) list.push(conn);
      else connsByProfile.set(key, [conn]);
    }
  }

  return (
    <div className="flex h-full flex-col bg-surface">
      {/* Toolbar */}
      <div className="flex h-[52px] items-center justify-between border-b border-sep px-6">
        <div className="flex items-center gap-3">
          <div className="relative">
            {regionInfo?.type === "country" ? (
              <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-ov-6 text-xl shadow-[inset_0_1px_0_var(--inset-highlight)] ring-1 ring-bdr">
                {countryCodeToFlag(regionInfo.countryCode)}
              </div>
            ) : regionInfo?.type === "lan" || regionInfo?.type === "loopback" ? (
              <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-ov-6 text-lg shadow-[inset_0_1px_0_var(--inset-highlight)] ring-1 ring-bdr">
                🏠
              </div>
            ) : (
              <div
                className={`flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br ${AVATAR_GRADIENTS[avatarIdx]} text-sm font-bold text-white shadow-md`}
              >
                {activeProfile?.name.charAt(0).toUpperCase() ?? "?"}
              </div>
            )}
            {status.state === "connected" && (
              <span className="absolute -bottom-0.5 -right-0.5 flex h-3 w-3 items-center justify-center">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-50" />
                <span className="glow-pulse relative inline-flex h-2 w-2 rounded-full bg-success" />
              </span>
            )}
          </div>
          <div>
            <p className="text-[15px] font-semibold text-t1">
              {activeProfile?.name ?? "Unknown"}
            </p>
            <p className="text-xs text-t3">{activeProfile?.remote ?? ""}</p>
          </div>
          <Badge variant={statusVariant(status.state)} className="ml-1">
            {status.state}
          </Badge>
        </div>
        <Button
          variant="danger"
          onClick={handleDisconnect}
          disabled={disconnecting || status.state === "disconnected"}
        >
          {disconnecting ? t("connection.disconnecting") : t("connection.disconnect")}
        </Button>
      </div>

      {/* Stats cards */}
      {statCards && (
        <div className="grid grid-cols-4 gap-2.5 border-b border-sep bg-surface px-6 py-4">
          {statCards.map((stat) => (
            <div
              key={stat.label}
              className="rounded-xl border border-sep bg-ov-4 px-4 py-3 shadow-[inset_0_1px_0_var(--inset-highlight)]"
            >
              <p className="text-[11px] font-medium uppercase tracking-wider text-t4">
                {stat.icon && <span className="mr-1">{stat.icon}</span>}
                {stat.label}
              </p>
              <p className={`mt-1.5 font-mono text-2xl font-semibold tabular-nums ${stat.color}`}>
                {stat.value}
              </p>
              <p className="mt-0.5 text-[11px] text-t4">{stat.sub}</p>
            </div>
          ))}
        </div>
      )}

      {status.message && !deployProgress && (
        <div
          className={`border-b px-6 py-2 text-sm ${
            status.state === "reconnecting"
              ? "border-warning/20 bg-warning/[0.10] text-warning"
              : "border-c-yellow/20 bg-c-yellow/[0.08] text-c-yellow"
          }`}
        >
          {status.state === "reconnecting" && (
            <span className="mr-2 inline-block h-2 w-2 animate-pulse rounded-full bg-warning" />
          )}
          {status.message}
          {status.state === "reconnecting" && (
            <span className="ml-2 text-warning/50 text-xs">
              {t("connection.firewallActive")}
            </span>
          )}
        </div>
      )}

      {status.state === "connecting" && deployProgress && (
        <div className="border-b border-sep bg-surface px-6 py-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm text-t2">
              {deployReason === "first-deploy"
                ? t("connection.deployFirstDeploy")
                : deployReason === "update"
                  ? t("connection.deployUpdate")
                  : t("connection.deployGeneric")}
            </span>
            <span className="font-mono text-xs text-t3">
              {formatBytes(deployProgress.sent)} / {formatBytes(deployProgress.total)}
            </span>
          </div>
          <div className="h-2 w-full overflow-hidden rounded-full bg-ov-8">
            <div
              className="h-full rounded-full bg-accent transition-all duration-150"
              style={{ width: `${deployProgress.total > 0 ? (deployProgress.sent / deployProgress.total) * 100 : 0}%` }}
            />
          </div>
          <p className="mt-1.5 text-[11px] text-t4">
            {t("connection.percentComplete", { percent: Math.round(deployProgress.total > 0 ? (deployProgress.sent / deployProgress.total) * 100 : 0) })}
          </p>
        </div>
      )}

      {/* Tab bar */}
      <div className="flex items-center gap-1 border-b border-sep bg-surface px-4 py-2.5">
        {tabs.map(({ id, label, badge }) => {
          const isActive = activeTab === id;
          return (
            <button
              key={id}
              className={`rounded-lg px-3.5 py-1.5 text-sm font-medium transition-all duration-150 ${
                isActive
                  ? "bg-ov-10 text-t1 shadow-sm"
                  : "text-t3 hover:bg-ov-4 hover:text-t2"
              }`}
              onClick={() => setActiveTab(id)}
            >
              {label}
              {badge !== undefined && badge > 0 && (
                <span
                  className={`ml-1.5 rounded-full px-1.5 text-[11px] ${
                    id === "errors"
                      ? "bg-danger/20 text-danger"
                      : "bg-ov-10 text-t3"
                  }`}
                >
                  {badge}
                </span>
              )}
            </button>
          );
        })}
      </div>

      {/* Content area */}
      <div className="min-h-0 flex-1 overflow-hidden bg-sf-content">

        {activeTab === "speed" && (
          <div className="h-full overflow-y-auto p-4">
            {speedHistory.length === 0 ? (
              <p className="text-t4 text-sm">{t("connection.waitingForSpeed")}</p>
            ) : (
              <>
                <SpeedChart history={speedHistory} />
                <div className="mt-4 flex items-center gap-6 px-1">
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full bg-success" />
                    <span className="text-xs text-t3">{t("connection.download")}</span>
                    <span className="font-mono text-sm font-semibold text-success">
                      {formatBytes(speedHistory[speedHistory.length - 1].rx)}/s
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full bg-accent" />
                    <span className="text-xs text-t3">{t("connection.upload")}</span>
                    <span className="font-mono text-sm font-semibold text-accent">
                      {formatBytes(speedHistory[speedHistory.length - 1].tx)}/s
                    </span>
                  </div>
                  <span className="ml-auto text-xs text-t5">{t("connection.lastNSeconds", { count: speedHistory.length })}</span>
                </div>
                {isMultiProfile && (
                  <>
                    <p className="mt-5 mb-1 px-1 text-[11px] font-semibold uppercase tracking-widest text-t4">
                      {t("connection.perProfile", { defaultValue: "per profile" })}
                    </p>
                    <PerProfileBreakdown
                      profiles={groupChildren}
                      tunnels={tunnelStats?.tunnels ?? []}
                      perProfileActiveConns={perProfileActiveConns}
                    />
                  </>
                )}
                {tunnelStats?.tunnels && tunnelStats.tunnels.length > 1 && (
                  <>
                    <p className="mt-5 mb-1 px-1 text-[11px] font-semibold uppercase tracking-widest text-t4">
                      {t("connection.perTunnel")}
                    </p>
                    <TunnelBreakdown tunnels={tunnelStats.tunnels} />
                  </>
                )}
              </>
            )}
          </div>
        )}

        {activeTab === "connections" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs">
            {activeConnCount === 0 && recentClosed.length === 0 ? (
              <p className="text-t4">{t("connection.noConnections")}</p>
            ) : (
              <>
                {activeConnCount > 0 && (
                  <>
                    <p className="mb-2 text-[11px] font-semibold uppercase tracking-widest text-t4">
                      {t("connection.active", { count: activeConnCount })}
                    </p>
                    {[...activeConnections.values()]
                      .sort((a, b) => b.openedAt - a.openedAt)
                      .map((conn) => {
                        const { host, port, scheme } = parseHost(conn.dstAddr, conn.host);
                        const tc = conn.tunnelIndex ? tunnelColor(conn.tunnelIndex) : null;
                        // In multi-profile mode, attribute the connection to a
                        // specific profile (fall back to default child if the
                        // relay didn't stamp activeProfileId yet).
                        let profileBadge: { name: string; color: ReturnType<typeof tunnelColor> } | null = null;
                        if (isMultiProfile) {
                          const pid = conn.activeProfileId ?? groupChildren[0]?.id;
                          const idx = groupChildren.findIndex((p) => p.id === pid);
                          if (idx >= 0) {
                            profileBadge = {
                              name: groupChildren[idx].name,
                              color: tunnelColor(idx + 1),
                            };
                          }
                        }
                        return (
                          <div
                            key={conn.id}
                            className="mb-1 flex items-baseline gap-2 rounded-lg border border-success/15 bg-success/[0.06] px-3 py-2"
                          >
                            <span className="h-1.5 w-1.5 shrink-0 self-center rounded-full bg-success" />
                            <span className="shrink-0 text-t5">{formatTime(conn.openedAt)}</span>
                            {profileBadge && (
                              <span
                                className={`shrink-0 truncate max-w-[10rem] rounded px-1 py-0.5 text-[10px] font-semibold ${profileBadge.color.bg} ${profileBadge.color.text}`}
                                title={profileBadge.name}
                              >
                                {profileBadge.name}
                              </span>
                            )}
                            {tc && (
                              <span className={`shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold ${tc.bg} ${tc.text}`}>
                                T{conn.tunnelIndex}
                              </span>
                            )}
                            {scheme && (
                              <span className="rounded bg-ov-8 px-1 py-0.5 text-[10px] text-t3">
                                {scheme}
                              </span>
                            )}
                            <span className="truncate text-accent/80">{host}</span>
                            <span className="text-t5">:{port}</span>
                          </div>
                        );
                      })}
                  </>
                )}
                {recentClosed.length > 0 && (
                  <>
                    <p className="mb-2 mt-4 text-[11px] font-semibold uppercase tracking-widest text-t4">
                      {t("connection.recentlyClosed")}
                    </p>
                    {[...recentClosed]
                      .reverse()
                      .slice(0, 50)
                      .map((ev) => {
                        const { host, port, scheme } = parseHost(ev.dstAddr, ev.host);
                        return (
                          <div
                            key={ev.id}
                            className="mb-1 flex items-baseline gap-2 rounded-lg border border-sep bg-ov-3 px-3 py-2 opacity-50"
                          >
                            <span className="shrink-0 text-t5">{formatTime(ev.timestampMs)}</span>
                            {scheme && (
                              <span className="rounded bg-ov-8 px-1 py-0.5 text-[10px] text-t4">
                                {scheme}
                              </span>
                            )}
                            <span className="truncate text-t3">{host}</span>
                            <span className="text-t5">:{port}</span>
                          </div>
                        );
                      })}
                  </>
                )}
              </>
            )}
            <div ref={connEndRef} />
          </div>
        )}

        {activeTab === "destinations" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs">
            {destinations.length === 0 ? (
              <p className="text-t4">{t("connection.noDestinations")}</p>
            ) : (
              <>
                {/* Sort controls */}
                <div className="mb-3 flex items-center gap-2">
                  <span className="text-[11px] text-t4 uppercase tracking-wider">{t("connection.sortBy")}</span>
                  {([
                    ["totalBytes", t("connection.sortData")],
                    ["rxSpeed", t("connection.sortSpeed")],
                    ["activeConns", t("connection.sortActive")],
                    ["totalConns", t("connection.sortTotal")],
                    ["lastSeen", t("connection.sortRecent")],
                  ] as [DestSortKey, string][]).map(([key, label]) => (
                    <button
                      key={key}
                      className={`rounded-md px-2 py-1 text-[11px] transition-all ${
                        destSort === key
                          ? "bg-ov-12 text-t1"
                          : "text-t4 hover:bg-ov-6 hover:text-t3"
                      }`}
                      onClick={() => setDestSort(key)}
                    >
                      {label}
                    </button>
                  ))}
                </div>

                {/* Destination list */}
                {[...destinations]
                  .sort((a, b) => {
                    switch (destSort) {
                      case "totalBytes": return (b.rxBytes + b.txBytes) - (a.rxBytes + a.txBytes);
                      case "rxSpeed": return (b.rxBytesPerSec + b.txBytesPerSec) - (a.rxBytesPerSec + a.txBytesPerSec);
                      case "activeConns": return b.activeConns - a.activeConns;
                      case "totalConns": return b.totalConns - a.totalConns;
                      case "lastSeen": return b.lastSeenMs - a.lastSeenMs;
                      default: return 0;
                    }
                  })
                  .map((dest) => {
                    const totalBytes = dest.rxBytes + dest.txBytes;
                    const totalSpeed = dest.rxBytesPerSec + dest.txBytesPerSec;
                    const isActive = dest.activeConns > 0;
                    const isBlocked = dest.route === "blocked";
                    const isDirect = dest.route === "direct";
                    // In multi-profile mode, surface where this host's traffic
                    // flows. Pinned (assignedProfileId from a tunnel:X rule) wins
                    // over currently-routing (activeProfileId) so users can tell
                    // sticky rules from incidental dispatches.
                    let profileAttr: { label: string; color: ReturnType<typeof tunnelColor> } | null = null;
                    if (isMultiProfile && !isBlocked && !isDirect) {
                      const pinnedId = dest.assignedProfileId;
                      const liveId = dest.activeProfileId;
                      const pid = pinnedId ?? liveId;
                      const idx = pid ? groupChildren.findIndex((p) => p.id === pid) : -1;
                      if (idx >= 0) {
                        const name = groupChildren[idx].name;
                        profileAttr = {
                          label: pinnedId
                            ? t("connection.pinnedTo", { name })
                            : t("connection.routedVia", { name }),
                          color: tunnelColor(idx + 1),
                        };
                      }
                    }
                    return (
                      <div
                        key={dest.host}
                        className={`mb-1.5 rounded-xl border px-3 py-2.5 ${
                          isBlocked
                            ? "border-danger/15 bg-danger/[0.04] opacity-60"
                            : isDirect
                              ? "border-success/15 bg-success/[0.04]"
                              : isActive
                                ? "border-accent/15 bg-accent/[0.04]"
                                : "border-sep bg-ov-2"
                        }`}
                      >
                        <div className="flex items-center justify-between mb-1">
                          <div className="flex items-center gap-2 min-w-0">
                            {isBlocked ? (
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-danger" />
                            ) : isDirect ? (
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                            ) : isActive ? (
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" />
                            ) : null}
                            <span className={`truncate text-sm font-medium ${
                              isBlocked ? "text-t4 line-through" : isActive ? "text-t1" : "text-t3"
                            }`}>
                              {dest.host}
                            </span>
                          </div>
                          <div className="flex items-center gap-3 shrink-0 ml-3">
                            {profileAttr && (
                              <span
                                className={`truncate max-w-[12rem] rounded px-1.5 py-0.5 text-[10px] font-semibold ${profileAttr.color.bg} ${profileAttr.color.text}`}
                                title={profileAttr.label}
                              >
                                {profileAttr.label}
                              </span>
                            )}
                            {totalSpeed > 0 && (
                              <span className="text-success text-[11px]">
                                {formatBytes(totalSpeed)}/s
                              </span>
                            )}
                            <span className="text-t4 text-[11px]">
                              {formatBytes(totalBytes)}
                            </span>
                          </div>
                        </div>
                        {dest.processNames && dest.processNames.length > 0 && (
                          <div className="flex items-center gap-1.5 mb-1 flex-wrap">
                            {dest.processNames.map((name) => (
                              <span
                                key={name}
                                className="rounded-md bg-ov-6 px-1.5 py-0.5 text-[10px] text-t3"
                              >
                                {name}
                              </span>
                            ))}
                          </div>
                        )}
                        <div className="flex items-center gap-4 text-[11px]">
                          <span className="text-t4">
                            <span className={isActive ? "text-success" : "text-t4"}>
                              {t("connection.activeCount", { count: dest.activeConns })}
                            </span>
                            {" / "}
                            {t("connection.totalCount", { count: dest.totalConns })}
                          </span>
                          <span className="text-t5">
                            ↓ {formatBytes(dest.rxBytes)}
                          </span>
                          <span className="text-t5">
                            ↑ {formatBytes(dest.txBytes)}
                          </span>
                          {dest.rxBytesPerSec > 0 && (
                            <span className="text-success/60">
                              ↓ {formatBytes(dest.rxBytesPerSec)}/s
                            </span>
                          )}
                          {dest.txBytesPerSec > 0 && (
                            <span className="text-accent/60">
                              ↑ {formatBytes(dest.txBytesPerSec)}/s
                            </span>
                          )}
                          <span className="ml-auto text-t5">
                            {formatTime(dest.lastSeenMs)}
                          </span>
                        </div>
                      </div>
                    );
                  })}
              </>
            )}
          </div>
        )}

        {activeTab === "logs" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs text-t3">
            {logs.length === 0 ? (
              <p className="text-t4">{t("connection.waitingForOutput")}</p>
            ) : (
              logs.map((line, idx) => (
                <p key={`${idx}-${line}`} className="whitespace-pre-wrap break-words leading-5">
                  {line}
                </p>
              ))
            )}
            <div ref={logEndRef} />
          </div>
        )}

        {activeTab === "errors" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs">
            {tunnelErrors.length === 0 ? (
              <p className="text-t4">{t("connection.noErrors")}</p>
            ) : (
              [...tunnelErrors].reverse().map((err, idx) => (
                <div
                  key={idx}
                  className="mb-1.5 rounded-xl border border-danger/15 bg-danger/[0.07] p-2.5"
                >
                  <span className="text-t4">{formatTime(err.timestampMs)}</span>{" "}
                  <span className="text-danger/80">{err.message}</span>
                </div>
              ))
            )}
          </div>
        )}

      </div>
    </div>
  );
}
