import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Shield, Zap, Ban, Gauge } from "lucide-react";
import type { ActiveConnection } from "@/stores/connectionStore";
import { useRuleStore } from "@/stores/ruleStore";
import type { ConnectionEvent, ConnectionStatus, DeployProgress, DestinationSnapshot, Profile, RouteMode, TunnelError, TunnelSnapshot, TunnelStats } from "@/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

interface Props {
  status: ConnectionStatus;
  activeProfile: Profile | null;
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
              <stop offset="0%" stopColor="#30d158" stopOpacity="0.25" />
              <stop offset="100%" stopColor="#30d158" stopOpacity="0" />
            </linearGradient>
            <linearGradient id="txGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#0a84ff" stopOpacity="0.20" />
              <stop offset="100%" stopColor="#0a84ff" stopOpacity="0" />
            </linearGradient>
            <clipPath id="chart-clip">
              <rect x={PAD.left} y={PAD.top} width={innerW} height={innerH} />
            </clipPath>
          </defs>

          {yTicks.map(({ v, y }) => (
            <line
              key={v}
              x1={PAD.left} y1={y} x2={PAD.left + innerW} y2={y}
              stroke="rgba(255,255,255,0.06)" strokeWidth="1"
            />
          ))}

          {yTicks.map(({ v, y }) => (
            <text
              key={v}
              x={PAD.left - 6} y={y + 4}
              textAnchor="end" fontSize="10"
              fill="rgba(255,255,255,0.28)" fontFamily="monospace"
            >
              {formatBytes(v)}
            </text>
          ))}

          {xTicks.map(({ i, t }) => (
            <text
              key={i}
              x={xScale(i)} y={PAD.top + innerH + 14}
              textAnchor="middle" fontSize="10"
              fill="rgba(255,255,255,0.22)" fontFamily="monospace"
            >
              {new Date(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}
            </text>
          ))}

          <g clipPath="url(#chart-clip)">
            {rxVals.length >= 2 && <path d={toFill(rxVals)} fill="url(#rxGrad)" />}
            {txVals.length >= 2 && <path d={toFill(txVals)} fill="url(#txGrad)" />}
            {rxVals.length >= 2 && (
              <path d={toPath(rxVals)} fill="none" stroke="#30d158" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
            )}
            {txVals.length >= 2 && (
              <path d={toPath(txVals)} fill="none" stroke="#0a84ff" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" />
            )}
            {rxVals.length >= 1 && (
              <circle cx={xScale(rxVals.length - 1)} cy={yScale(rxVals[rxVals.length - 1])} r="3" fill="#30d158" />
            )}
            {txVals.length >= 1 && (
              <circle cx={xScale(txVals.length - 1)} cy={yScale(txVals[txVals.length - 1])} r="3" fill="#0a84ff" />
            )}
          </g>

          <rect
            x={PAD.left} y={PAD.top} width={innerW} height={innerH}
            fill="none" stroke="rgba(255,255,255,0.06)" strokeWidth="1"
          />
        </svg>
      )}
    </div>
  );
}

function statusVariant(state: ConnectionStatus["state"]) {
  if (state === "connected") return "green";
  if (state === "connecting") return "yellow";
  if (state === "reconnecting") return "yellow";
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

// Colors for tunnel index badges (0-based slot → color pair)
const TUNNEL_COLORS = [
  { bg: "bg-[#0a84ff]/20", text: "text-[#0a84ff]", dot: "bg-[#0a84ff]" },
  { bg: "bg-[#30d158]/20", text: "text-[#30d158]", dot: "bg-[#30d158]" },
  { bg: "bg-[#bf5af2]/20", text: "text-[#bf5af2]", dot: "bg-[#bf5af2]" },
  { bg: "bg-[#ff9f0a]/20", text: "text-[#ff9f0a]", dot: "bg-[#ff9f0a]" },
  { bg: "bg-[#ff453a]/20", text: "text-[#ff453a]", dot: "bg-[#ff453a]" },
  { bg: "bg-[#ffd60a]/20", text: "text-[#ffd60a]", dot: "bg-[#ffd60a]" },
];

function tunnelColor(idx: number) {
  return TUNNEL_COLORS[(idx - 1) % TUNNEL_COLORS.length];
}

/** Format RTT from microseconds to a human-readable string. */
function formatRtt(us: number): string {
  if (us === 0) return "—";
  if (us < 1000) return `${us}µs`;
  return `${(us / 1000).toFixed(us < 10000 ? 1 : 0)}ms`;
}

/** RTT color class based on microseconds: green < 50ms, yellow < 200ms, red >= 200ms */
function rttColor(us: number): string {
  if (us === 0) return "text-white/25";
  if (us >= 200_000) return "text-[#ff453a]";
  if (us >= 50_000) return "text-[#ffd60a]";
  return "text-white/50";
}

/**
 * Diagnose tunnel health from RTT metrics and return a short human-readable
 * label + colour.
 */
function useDiagnoseTunnel() {
  const { t } = useTranslation();
  return (tunnel: TunnelSnapshot): { label: string; color: string } => {
    if (tunnel.lastRttUs === 0) return { label: t("connection.diagnosis.waitingForData"), color: "text-white/25" };

    const lastMs = tunnel.lastRttUs / 1000;
    const minMs = tunnel.minRttUs / 1000;
    const jitterMs = tunnel.jitterUs / 1000;
    const inflation = minMs > 0 ? lastMs / minMs : 1;

    if (jitterMs > 30 && inflation > 2)
      return { label: t("connection.diagnosis.congestion"), color: "text-[#ff453a]" };
    if (jitterMs > 30)
      return { label: t("connection.diagnosis.unstable"), color: "text-[#ff9f0a]" };
    if (inflation > 3 && jitterMs < 15)
      return { label: t("connection.diagnosis.bufferbloat", { inflation: inflation.toFixed(0) }), color: "text-[#ff453a]" };
    if (inflation > 1.8 && jitterMs < 15)
      return { label: t("connection.diagnosis.possibleBufferbloat", { inflation: inflation.toFixed(1) }), color: "text-[#ffd60a]" };
    if (minMs > 150)
      return { label: t("connection.diagnosis.highLatency"), color: "text-[#ffd60a]" };

    return { label: t("connection.diagnosis.healthy"), color: "text-[#30d158]" };
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
        const congColor = congFrac > 0.8 ? "#ff453a" : congFrac > 0.5 ? "#ffd60a" : "#30d158";
        const diag = diagnoseTunnel(tun);
        return (
          <div key={tun.index} className={`rounded-xl border px-3 py-2.5 ${
            tun.state === "dead" ? "border-[#ff453a]/20 bg-[#ff453a]/[0.06] opacity-50" :
            tun.state === "reconnecting" ? "border-[#ff9f0a]/20 bg-[#ff9f0a]/[0.06]" :
            "border-white/[0.06] bg-white/[0.04]"
          }`}>
            <div className="flex items-center gap-1.5 mb-1.5">
              {tun.state === "alive" ? (
                <span className={`h-1.5 w-1.5 rounded-full ${c.dot}`} />
              ) : tun.state === "reconnecting" ? (
                <span className="h-1.5 w-1.5 rounded-full bg-[#ff9f0a] animate-pulse" />
              ) : (
                <span className="h-1.5 w-1.5 rounded-full bg-[#ff453a]" />
              )}
              <span className={`text-[11px] font-semibold uppercase tracking-wider ${
                tun.state === "dead" ? "text-[#ff453a]" :
                tun.state === "reconnecting" ? "text-[#ff9f0a]" : c.text
              }`}>
                {t("connection.tunnel", { index: tun.index })}
              </span>
              {tun.state !== "alive" && (
                <span className={`text-[10px] ${tun.state === "dead" ? "text-[#ff453a]/60" : "text-[#ff9f0a]/60"}`}>
                  {tun.state === "reconnecting" ? t("connection.reconnecting") : t("connection.dead")}
                </span>
              )}
            </div>
            {tun.state === "dead" ? (
              <p className="text-[10px] text-[#ff453a]/60">{t("connection.reconnectionFailed")}</p>
            ) : (
            <div className="flex flex-col gap-0.5">
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-white/30">↓</span>
                <span className={`font-mono text-xs font-semibold ${c.text}`}>{formatBytes(tun.rxBytesPerSec)}/s</span>
              </div>
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-white/30">↑</span>
                <span className="font-mono text-xs text-white/50">{formatBytes(tun.txBytesPerSec)}/s</span>
              </div>
              <div className="flex justify-between items-baseline mt-0.5">
                <span className="text-[10px] text-white/30">{t("connection.conns")}</span>
                <span className="font-mono text-xs text-white/50">{tun.activeConns}</span>
              </div>
              {/* RTT row: last (min) */}
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-white/30">{t("connection.rtt")}</span>
                <span className={`font-mono text-xs ${rttColor(tun.lastRttUs)}`}>
                  {formatRtt(tun.lastRttUs)}
                  {tun.minRttUs > 0 && tun.minRttUs !== tun.lastRttUs && (
                    <span className="text-white/20 ml-0.5">({formatRtt(tun.minRttUs)} min)</span>
                  )}
                </span>
              </div>
              {/* Jitter row */}
              <div className="flex justify-between items-baseline">
                <span className="text-[10px] text-white/30">{t("connection.jitter")}</span>
                <span className={`font-mono text-xs ${tun.jitterUs > 30_000 ? "text-[#ff9f0a]" : "text-white/40"}`}>
                  {tun.lastRttUs === 0 ? "—" : formatRtt(tun.jitterUs)}
                </span>
              </div>
              {/* Congestion bar */}
              <div className="mt-1.5">
                <div className="flex justify-between items-baseline mb-0.5">
                  <span className="text-[10px] text-white/30">{t("connection.load")}</span>
                  <span className="font-mono text-[10px] text-white/30">{tun.congestionScore.toFixed(1)}</span>
                </div>
                <div className="h-1 w-full rounded-full bg-white/[0.08] overflow-hidden">
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

const PRIORITY_META: Record<number, { label: string; color: string; ring: string; bg: string; dotColor: string }> = {
  1: { label: "Low",  color: "text-white/40",   ring: "ring-white/10",     bg: "bg-white/[0.06]",    dotColor: "bg-white/30" },
  2: { label: "Low+", color: "text-white/50",   ring: "ring-white/15",     bg: "bg-white/[0.08]",    dotColor: "bg-white/40" },
  3: { label: "Norm", color: "text-[#0a84ff]",  ring: "ring-[#0a84ff]/30", bg: "bg-[#0a84ff]/10",    dotColor: "bg-[#0a84ff]" },
  4: { label: "High", color: "text-[#ff9f0a]",  ring: "ring-[#ff9f0a]/30", bg: "bg-[#ff9f0a]/10",    dotColor: "bg-[#ff9f0a]" },
  5: { label: "Crit", color: "text-[#ff453a]",  ring: "ring-[#ff453a]/30", bg: "bg-[#ff453a]/10",    dotColor: "bg-[#ff453a]" },
};

const ROUTE_META: Record<RouteMode, { label: string; color: string; ring: string; bg: string; Icon: typeof Shield }> = {
  tunnel:  { label: "Tunnel",  color: "text-[#0a84ff]", ring: "ring-[#0a84ff]/30", bg: "bg-[#0a84ff]/10", Icon: Shield },
  direct:  { label: "Direct",  color: "text-[#30d158]", ring: "ring-[#30d158]/30", bg: "bg-[#30d158]/10", Icon: Zap },
  blocked: { label: "Blocked", color: "text-[#ff453a]", ring: "ring-[#ff453a]/30", bg: "bg-[#ff453a]/10", Icon: Ban },
};

function RouteBadge({ route, onChange }: { route: RouteMode; onChange: (r: RouteMode) => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const meta = ROUTE_META[route];

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        onClick={(e) => { e.stopPropagation(); setOpen(!open); }}
        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 transition-all hover:brightness-125 ${meta.bg} ${meta.color} ${meta.ring}`}
      >
        <meta.Icon size={11} />
        {meta.label}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 w-32 rounded-lg border border-white/[0.08] bg-[#2c2c2e] p-1 shadow-xl">
          {(["tunnel", "direct", "blocked"] as RouteMode[]).map((mode) => {
            const m = ROUTE_META[mode];
            const active = mode === route;
            return (
              <button
                key={mode}
                onClick={(e) => { e.stopPropagation(); onChange(mode); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-white/60 hover:bg-white/[0.06]"
                }`}
              >
                <m.Icon size={13} className={active ? m.color : "text-white/40"} />
                {m.label}
                {active && <span className="ml-auto text-[10px]">✓</span>}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function PriorityBadge({ priority, onChange }: { priority: number; onChange: (p: number) => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const meta = PRIORITY_META[priority] ?? PRIORITY_META[3];

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        onClick={(e) => { e.stopPropagation(); setOpen(!open); }}
        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 transition-all hover:brightness-125 ${meta.bg} ${meta.color} ${meta.ring}`}
      >
        <Gauge size={11} />
        {meta.label}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 w-32 rounded-lg border border-white/[0.08] bg-[#2c2c2e] p-1 shadow-xl">
          {[1, 2, 3, 4, 5].map((p) => {
            const m = PRIORITY_META[p];
            const active = p === priority;
            return (
              <button
                key={p}
                onClick={(e) => { e.stopPropagation(); onChange(p); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-white/60 hover:bg-white/[0.06]"
                }`}
              >
                <span className={`h-2 w-2 rounded-full ${m.dotColor}`} />
                {m.label}
                {active && <span className="ml-auto text-[10px]">✓</span>}
              </button>
            );
          })}
        </div>
      )}
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

export function ConnectionPage({
  status,
  activeProfile,
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
  const { setPriority: onSetDestinationPriority, setRoute: onSetDestinationRoute } = useRuleStore();
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
          color: "text-[#30d158]",
          icon: "↓",
        },
        {
          label: t("connection.upload"),
          value: formatBytes(tunnelStats.txBytesPerSec) + "/s",
          sub: t("connection.total", { bytes: formatBytes(tunnelStats.totalTxBytes) }),
          color: "text-[#0a84ff]",
          icon: "↑",
        },
        {
          label: t("connection.connections"),
          value: String(tunnelStats.activeConns),
          sub: t("connection.totalCount", { count: tunnelStats.totalConns }),
          color: "text-white/80",
          icon: "⇄",
        },
        {
          label: t("connection.dns"),
          value: String(tunnelStats.dnsQueries),
          sub: t("connection.queries"),
          color: "text-[#bf5af2]",
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

  return (
    <div className="flex h-screen flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <div className="flex items-center gap-3">
          <div className="relative">
            <div
              className={`flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br ${AVATAR_GRADIENTS[avatarIdx]} text-sm font-bold text-white shadow-md`}
            >
              {activeProfile?.name.charAt(0).toUpperCase() ?? "?"}
            </div>
            {status.state === "connected" && (
              <span className="absolute -bottom-0.5 -right-0.5 flex h-3 w-3 items-center justify-center">
                <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[#30d158] opacity-50" />
                <span className="glow-pulse relative inline-flex h-2 w-2 rounded-full bg-[#30d158]" />
              </span>
            )}
          </div>
          <div>
            <p className="text-[15px] font-semibold text-white/90">
              {activeProfile?.name ?? "Unknown"}
            </p>
            <p className="text-xs text-white/38">{activeProfile?.remote ?? ""}</p>
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
        <div className="grid grid-cols-4 gap-2.5 border-b border-white/[0.06] bg-[#1c1c1e] px-6 py-4">
          {statCards.map((stat) => (
            <div
              key={stat.label}
              className="rounded-xl border border-white/[0.06] bg-white/[0.04] px-4 py-3 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]"
            >
              <p className="text-[11px] font-medium uppercase tracking-wider text-white/30">
                {stat.icon && <span className="mr-1">{stat.icon}</span>}
                {stat.label}
              </p>
              <p className={`mt-1.5 font-mono text-2xl font-semibold tabular-nums ${stat.color}`}>
                {stat.value}
              </p>
              <p className="mt-0.5 text-[11px] text-white/25">{stat.sub}</p>
            </div>
          ))}
        </div>
      )}

      {status.message && !deployProgress && (
        <div
          className={`border-b px-6 py-2 text-sm ${
            status.state === "reconnecting"
              ? "border-[#ff9f0a]/20 bg-[#ff9f0a]/[0.10] text-[#ff9f0a]"
              : "border-[#ffd60a]/20 bg-[#ffd60a]/[0.08] text-[#ffd60a]"
          }`}
        >
          {status.state === "reconnecting" && (
            <span className="mr-2 inline-block h-2 w-2 animate-pulse rounded-full bg-[#ff9f0a]" />
          )}
          {status.message}
          {status.state === "reconnecting" && (
            <span className="ml-2 text-[#ff9f0a]/50 text-xs">
              {t("connection.firewallActive")}
            </span>
          )}
        </div>
      )}

      {status.state === "connecting" && deployProgress && (
        <div className="border-b border-white/[0.06] bg-[#1c1c1e] px-6 py-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm text-white/70">
              {deployReason === "first-deploy"
                ? t("connection.deployFirstDeploy")
                : deployReason === "update"
                  ? t("connection.deployUpdate")
                  : t("connection.deployGeneric")}
            </span>
            <span className="font-mono text-xs text-white/40">
              {formatBytes(deployProgress.sent)} / {formatBytes(deployProgress.total)}
            </span>
          </div>
          <div className="h-2 w-full overflow-hidden rounded-full bg-white/[0.08]">
            <div
              className="h-full rounded-full bg-[#0a84ff] transition-all duration-150"
              style={{ width: `${deployProgress.total > 0 ? (deployProgress.sent / deployProgress.total) * 100 : 0}%` }}
            />
          </div>
          <p className="mt-1.5 text-[11px] text-white/30">
            {t("connection.percentComplete", { percent: Math.round(deployProgress.total > 0 ? (deployProgress.sent / deployProgress.total) * 100 : 0) })}
          </p>
        </div>
      )}

      {/* Tab bar */}
      <div className="flex items-center gap-1 border-b border-white/[0.06] bg-[#1c1c1e] px-4 py-2.5">
        {tabs.map(({ id, label, badge }) => {
          const isActive = activeTab === id;
          return (
            <button
              key={id}
              className={`rounded-lg px-3.5 py-1.5 text-sm font-medium transition-all duration-150 ${
                isActive
                  ? "bg-white/[0.10] text-white/90 shadow-sm"
                  : "text-white/40 hover:bg-white/[0.05] hover:text-white/65"
              }`}
              onClick={() => setActiveTab(id)}
            >
              {label}
              {badge !== undefined && badge > 0 && (
                <span
                  className={`ml-1.5 rounded-full px-1.5 text-[11px] ${
                    id === "errors"
                      ? "bg-[#ff453a]/20 text-[#ff453a]"
                      : "bg-white/[0.10] text-white/50"
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
      <div className="min-h-0 flex-1 overflow-hidden bg-[#141416]">

        {activeTab === "speed" && (
          <div className="h-full overflow-y-auto p-4">
            {speedHistory.length === 0 ? (
              <p className="text-white/25 text-sm">{t("connection.waitingForSpeed")}</p>
            ) : (
              <>
                <SpeedChart history={speedHistory} />
                <div className="mt-4 flex items-center gap-6 px-1">
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full bg-[#30d158]" />
                    <span className="text-xs text-white/40">{t("connection.download")}</span>
                    <span className="font-mono text-sm font-semibold text-[#30d158]">
                      {formatBytes(speedHistory[speedHistory.length - 1].rx)}/s
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full bg-[#0a84ff]" />
                    <span className="text-xs text-white/40">{t("connection.upload")}</span>
                    <span className="font-mono text-sm font-semibold text-[#0a84ff]">
                      {formatBytes(speedHistory[speedHistory.length - 1].tx)}/s
                    </span>
                  </div>
                  <span className="ml-auto text-xs text-white/20">{t("connection.lastNSeconds", { count: speedHistory.length })}</span>
                </div>
                {tunnelStats?.tunnels && tunnelStats.tunnels.length > 1 && (
                  <>
                    <p className="mt-5 mb-1 px-1 text-[11px] font-semibold uppercase tracking-widest text-white/30">
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
              <p className="text-white/25">{t("connection.noConnections")}</p>
            ) : (
              <>
                {activeConnCount > 0 && (
                  <>
                    <p className="mb-2 text-[11px] font-semibold uppercase tracking-widest text-white/30">
                      {t("connection.active", { count: activeConnCount })}
                    </p>
                    {[...activeConnections.values()]
                      .sort((a, b) => b.openedAt - a.openedAt)
                      .map((conn) => {
                        const { host, port, scheme } = parseHost(conn.dstAddr, conn.host);
                        const tc = conn.tunnelIndex ? tunnelColor(conn.tunnelIndex) : null;
                        return (
                          <div
                            key={conn.id}
                            className="mb-1 flex items-baseline gap-2 rounded-lg border border-[#30d158]/15 bg-[#30d158]/[0.06] px-3 py-2"
                          >
                            <span className="h-1.5 w-1.5 shrink-0 self-center rounded-full bg-[#30d158]" />
                            <span className="shrink-0 text-white/20">{formatTime(conn.openedAt)}</span>
                            {tc && (
                              <span className={`shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold ${tc.bg} ${tc.text}`}>
                                T{conn.tunnelIndex}
                              </span>
                            )}
                            {scheme && (
                              <span className="rounded bg-white/[0.08] px-1 py-0.5 text-[10px] text-white/40">
                                {scheme}
                              </span>
                            )}
                            <span className="truncate text-[#0a84ff]/80">{host}</span>
                            <span className="text-white/20">:{port}</span>
                          </div>
                        );
                      })}
                  </>
                )}
                {recentClosed.length > 0 && (
                  <>
                    <p className="mb-2 mt-4 text-[11px] font-semibold uppercase tracking-widest text-white/30">
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
                            className="mb-1 flex items-baseline gap-2 rounded-lg border border-white/[0.04] bg-white/[0.03] px-3 py-2 opacity-50"
                          >
                            <span className="shrink-0 text-white/20">{formatTime(ev.timestampMs)}</span>
                            {scheme && (
                              <span className="rounded bg-white/[0.08] px-1 py-0.5 text-[10px] text-white/30">
                                {scheme}
                              </span>
                            )}
                            <span className="truncate text-white/40">{host}</span>
                            <span className="text-white/15">:{port}</span>
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
              <p className="text-white/25">{t("connection.noDestinations")}</p>
            ) : (
              <>
                {/* Sort controls */}
                <div className="mb-3 flex items-center gap-2">
                  <span className="text-[11px] text-white/30 uppercase tracking-wider">{t("connection.sortBy")}</span>
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
                          ? "bg-white/[0.12] text-white/80"
                          : "text-white/30 hover:bg-white/[0.06] hover:text-white/50"
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
                    return (
                      <div
                        key={dest.host}
                        className={`mb-1.5 rounded-xl border px-3 py-2.5 ${
                          isBlocked
                            ? "border-[#ff453a]/15 bg-[#ff453a]/[0.04] opacity-60"
                            : isDirect
                              ? "border-[#30d158]/15 bg-[#30d158]/[0.04]"
                              : isActive
                                ? "border-[#0a84ff]/15 bg-[#0a84ff]/[0.04]"
                                : "border-white/[0.04] bg-white/[0.02]"
                        }`}
                      >
                        <div className="flex items-center justify-between mb-1">
                          <div className="flex items-center gap-2 min-w-0">
                            {isBlocked ? (
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#ff453a]" />
                            ) : isDirect ? (
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#30d158]" />
                            ) : isActive ? (
                              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#0a84ff]" />
                            ) : null}
                            <span className={`truncate text-sm font-medium ${
                              isBlocked ? "text-white/30 line-through" : isActive ? "text-white/80" : "text-white/40"
                            }`}>
                              {dest.host}
                            </span>
                          </div>
                          <div className="flex items-center gap-3 shrink-0 ml-3">
                            {totalSpeed > 0 && (
                              <span className="text-[#30d158] text-[11px]">
                                {formatBytes(totalSpeed)}/s
                              </span>
                            )}
                            <span className="text-white/25 text-[11px]">
                              {formatBytes(totalBytes)}
                            </span>
                            <RouteBadge
                              route={dest.route}
                              onChange={(r) => onSetDestinationRoute(dest.host, r)}
                            />
                            <PriorityBadge
                              priority={dest.priority}
                              onChange={(p) => onSetDestinationPriority(dest.host, p)}
                            />
                          </div>
                        </div>
                        {dest.processNames && dest.processNames.length > 0 && (
                          <div className="flex items-center gap-1.5 mb-1 flex-wrap">
                            {dest.processNames.map((name) => (
                              <span
                                key={name}
                                className="rounded-md bg-white/[0.06] px-1.5 py-0.5 text-[10px] text-white/40"
                              >
                                {name}
                              </span>
                            ))}
                          </div>
                        )}
                        <div className="flex items-center gap-4 text-[11px]">
                          <span className="text-white/25">
                            <span className={isActive ? "text-[#30d158]" : "text-white/30"}>
                              {t("connection.activeCount", { count: dest.activeConns })}
                            </span>
                            {" / "}
                            {t("connection.totalCount", { count: dest.totalConns })}
                          </span>
                          <span className="text-white/20">
                            ↓ {formatBytes(dest.rxBytes)}
                          </span>
                          <span className="text-white/20">
                            ↑ {formatBytes(dest.txBytes)}
                          </span>
                          {dest.rxBytesPerSec > 0 && (
                            <span className="text-[#30d158]/60">
                              ↓ {formatBytes(dest.rxBytesPerSec)}/s
                            </span>
                          )}
                          {dest.txBytesPerSec > 0 && (
                            <span className="text-[#0a84ff]/60">
                              ↑ {formatBytes(dest.txBytesPerSec)}/s
                            </span>
                          )}
                          <span className="ml-auto text-white/15">
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
          <div className="h-full overflow-y-auto p-4 font-mono text-xs text-white/50">
            {logs.length === 0 ? (
              <p className="text-white/25">{t("connection.waitingForOutput")}</p>
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
              <p className="text-white/25">{t("connection.noErrors")}</p>
            ) : (
              [...tunnelErrors].reverse().map((err, idx) => (
                <div
                  key={idx}
                  className="mb-1.5 rounded-xl border border-[#ff453a]/15 bg-[#ff453a]/[0.07] p-2.5"
                >
                  <span className="text-white/25">{formatTime(err.timestampMs)}</span>{" "}
                  <span className="text-[#ff453a]/80">{err.message}</span>
                </div>
              ))
            )}
          </div>
        )}

      </div>
    </div>
  );
}
