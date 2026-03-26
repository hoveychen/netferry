import { useCallback, useEffect, useRef, useState } from "react";
import type { ActiveConnection } from "@/stores/connectionStore";
import type { ConnectionEvent, ConnectionStatus, DeployProgress, Profile, TunnelError, TunnelStats } from "@/types";
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
  deployProgress: DeployProgress | null;
  deployReason: string | null;
  onDisconnect: () => Promise<void>;
}

type Tab = "speed" | "connections" | "logs" | "errors";

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
  deployProgress,
  deployReason,
  onDisconnect,
}: Props) {
  const [activeTab, setActiveTab] = useState<Tab>("speed");
  const [disconnecting, setDisconnecting] = useState(false);
  const [speedHistory, setSpeedHistory] = useState<SpeedPoint[]>([]);
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

  useEffect(() => {
    if (tunnelErrors.length > 0) {
      setActiveTab("errors");
    }
  }, [tunnelErrors.length]);

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
          label: "Download",
          value: formatBytes(tunnelStats.rxBytesPerSec) + "/s",
          sub: "Total: " + formatBytes(tunnelStats.totalRxBytes),
          color: "text-[#30d158]",
          icon: "↓",
        },
        {
          label: "Upload",
          value: formatBytes(tunnelStats.txBytesPerSec) + "/s",
          sub: "Total: " + formatBytes(tunnelStats.totalTxBytes),
          color: "text-[#0a84ff]",
          icon: "↑",
        },
        {
          label: "Connections",
          value: String(tunnelStats.activeConns),
          sub: `${tunnelStats.totalConns} total`,
          color: "text-white/80",
          icon: "⇄",
        },
        {
          label: "DNS",
          value: String(tunnelStats.dnsQueries),
          sub: "queries",
          color: "text-[#bf5af2]",
          icon: null,
        },
      ]
    : null;

  const activeConnCount = activeConnections.size;
  const tabs: { id: Tab; label: string; badge?: number }[] = [
    { id: "speed", label: "Speed" },
    { id: "connections", label: "Connections", badge: activeConnCount || undefined },
    { id: "logs", label: "Logs" },
    { id: "errors", label: "Errors", badge: tunnelErrors.length || undefined },
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
          {disconnecting ? "Disconnecting…" : "Disconnect"}
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
        <div className="border-b border-[#ffd60a]/20 bg-[#ffd60a]/[0.08] px-6 py-2 text-sm text-[#ffd60a]">
          {status.message}
        </div>
      )}

      {status.state === "connecting" && deployProgress && (
        <div className="border-b border-white/[0.06] bg-[#1c1c1e] px-6 py-3">
          <div className="flex items-center justify-between mb-2">
            <span className="text-sm text-white/70">
              {deployReason === "first-deploy"
                ? "Uploading relay server (first deploy)…"
                : deployReason === "update"
                  ? "Uploading relay server (new version)…"
                  : "Uploading relay server…"}
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
            {Math.round(deployProgress.total > 0 ? (deployProgress.sent / deployProgress.total) * 100 : 0)}% complete
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
              <p className="text-white/25 text-sm">Waiting for speed data…</p>
            ) : (
              <>
                <SpeedChart history={speedHistory} />
                <div className="mt-4 flex items-center gap-6 px-1">
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full bg-[#30d158]" />
                    <span className="text-xs text-white/40">Download</span>
                    <span className="font-mono text-sm font-semibold text-[#30d158]">
                      {formatBytes(speedHistory[speedHistory.length - 1].rx)}/s
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full bg-[#0a84ff]" />
                    <span className="text-xs text-white/40">Upload</span>
                    <span className="font-mono text-sm font-semibold text-[#0a84ff]">
                      {formatBytes(speedHistory[speedHistory.length - 1].tx)}/s
                    </span>
                  </div>
                  <span className="ml-auto text-xs text-white/20">Last {speedHistory.length}s</span>
                </div>
              </>
            )}
          </div>
        )}

        {activeTab === "connections" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs">
            {activeConnCount === 0 && recentClosed.length === 0 ? (
              <p className="text-white/25">No connections yet.</p>
            ) : (
              <>
                {activeConnCount > 0 && (
                  <>
                    <p className="mb-2 text-[11px] font-semibold uppercase tracking-widest text-white/30">
                      Active ({activeConnCount})
                    </p>
                    {[...activeConnections.values()]
                      .sort((a, b) => b.openedAt - a.openedAt)
                      .map((conn) => {
                        const { host, port, scheme } = parseHost(conn.dstAddr, conn.host);
                        return (
                          <div
                            key={conn.id}
                            className="mb-1 flex items-baseline gap-2 rounded-lg border border-[#30d158]/15 bg-[#30d158]/[0.06] px-3 py-2"
                          >
                            <span className="h-1.5 w-1.5 shrink-0 self-center rounded-full bg-[#30d158]" />
                            <span className="shrink-0 text-white/20">{formatTime(conn.openedAt)}</span>
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
                      Recently Closed
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

        {activeTab === "logs" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs text-white/50">
            {logs.length === 0 ? (
              <p className="text-white/25">Waiting for output…</p>
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
              <p className="text-white/25">No errors.</p>
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
