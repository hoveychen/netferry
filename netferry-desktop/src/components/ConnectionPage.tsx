import { useEffect, useRef, useState } from "react";
import type { ConnectionEvent, ConnectionStatus, Profile, TunnelError, TunnelStats } from "@/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

interface Props {
  status: ConnectionStatus;
  activeProfile: Profile | null;
  logs: string[];
  tunnelStats: TunnelStats | null;
  connectionEvents: ConnectionEvent[];
  tunnelErrors: TunnelError[];
  onDisconnect: () => Promise<void>;
}

type Tab = "logs" | "connections" | "errors";

function statusVariant(state: ConnectionStatus["state"]) {
  if (state === "connected") return "green";
  if (state === "connecting") return "yellow";
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

export function ConnectionPage({
  status,
  activeProfile,
  logs,
  tunnelStats,
  connectionEvents,
  tunnelErrors,
  onDisconnect,
}: Props) {
  const [activeTab, setActiveTab] = useState<Tab>("logs");
  const [disconnecting, setDisconnecting] = useState(false);
  const logEndRef = useRef<HTMLDivElement>(null);

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

      {/* Stats */}
      {tunnelStats && (
        <div className="grid grid-cols-4 gap-2.5 border-b border-white/[0.06] bg-[#1c1c1e] px-6 py-4">
          {[
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
              value: String(connectionEvents.length),
              sub: "tunneled",
              color: "text-white/80",
              icon: null,
            },
            {
              label: "Errors",
              value: String(tunnelErrors.length),
              sub: "detected",
              color: tunnelErrors.length > 0 ? "text-[#ff453a]" : "text-white/80",
              icon: null,
            },
          ].map((stat) => (
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

      {status.message && (
        <div className="border-b border-[#ffd60a]/20 bg-[#ffd60a]/[0.08] px-6 py-2 text-sm text-[#ffd60a]">
          {status.message}
        </div>
      )}

      {/* Tab bar (segmented control) */}
      <div className="flex items-center gap-1 border-b border-white/[0.06] bg-[#1c1c1e] px-4 py-2.5">
        {(["logs", "connections", "errors"] as Tab[]).map((tab) => {
          const isActive = activeTab === tab;
          const badge =
            tab === "connections" && connectionEvents.length > 0
              ? connectionEvents.length
              : tab === "errors" && tunnelErrors.length > 0
                ? tunnelErrors.length
                : null;
          return (
            <button
              key={tab}
              className={`rounded-lg px-3.5 py-1.5 text-sm font-medium transition-all duration-150 ${
                isActive
                  ? "bg-white/[0.10] text-white/90 shadow-sm"
                  : "text-white/40 hover:bg-white/[0.05] hover:text-white/65"
              }`}
              onClick={() => setActiveTab(tab)}
            >
              {tab.charAt(0).toUpperCase() + tab.slice(1)}
              {badge !== null && (
                <span
                  className={`ml-1.5 rounded-full px-1.5 text-[11px] ${
                    tab === "errors"
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

        {activeTab === "connections" && (
          <div className="h-full overflow-y-auto p-4 font-mono text-xs">
            {connectionEvents.length === 0 ? (
              <p className="text-white/25">No connections tunneled yet.</p>
            ) : (
              [...connectionEvents].reverse().map((evt, idx) => (
                <div key={idx} className="mb-1 border-b border-white/[0.04] pb-1">
                  <span className="text-white/25">{formatTime(evt.timestampMs)}</span>{" "}
                  <span className="text-[#30d158]/80">{evt.srcAddr}</span>
                  <span className="text-white/20"> → </span>
                  <span className="text-[#0a84ff]/80">{evt.dstAddr}</span>
                </div>
              ))
            )}
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
