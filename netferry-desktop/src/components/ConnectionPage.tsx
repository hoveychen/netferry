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

function TabButton({
  tab,
  active,
  onClick,
  children,
}: {
  tab: Tab;
  active: Tab;
  onClick: (t: Tab) => void;
  children: React.ReactNode;
}) {
  return (
    <button
      className={`px-4 py-2 text-sm font-medium transition-colors ${
        active === tab
          ? "border-b-2 border-slate-800 text-slate-800"
          : "text-slate-500 hover:text-slate-700"
      }`}
      onClick={() => onClick(tab)}
    >
      {children}
    </button>
  );
}

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

  return (
    <div className="flex h-screen flex-col bg-slate-100">
      {/* Header */}
      <div className="flex items-center justify-between border-b border-slate-200 bg-white px-6 py-4">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-full bg-slate-800 text-sm font-bold text-white">
            {activeProfile?.name.charAt(0).toUpperCase() ?? "?"}
          </div>
          <div>
            <p className="font-semibold text-slate-800">{activeProfile?.name ?? "Unknown"}</p>
            <p className="text-xs text-slate-400">{activeProfile?.remote ?? ""}</p>
          </div>
          <Badge variant={statusVariant(status.state)} className="ml-2">
            {status.state}
          </Badge>
        </div>
        <Button
          variant="danger"
          onClick={handleDisconnect}
          disabled={disconnecting || status.state === "disconnected"}
        >
          {disconnecting ? "Disconnecting..." : "Disconnect"}
        </Button>
      </div>

      {/* Stats row */}
      {tunnelStats && (
        <div className="grid grid-cols-4 gap-px border-b border-slate-200 bg-slate-200">
          {[
            { label: "↓ Download", value: formatBytes(tunnelStats.rxBytesPerSec) + "/s", sub: "Total: " + formatBytes(tunnelStats.totalRxBytes), color: "text-emerald-700" },
            { label: "↑ Upload", value: formatBytes(tunnelStats.txBytesPerSec) + "/s", sub: "Total: " + formatBytes(tunnelStats.totalTxBytes), color: "text-sky-700" },
            { label: "Connections", value: String(connectionEvents.length), sub: "tunneled", color: "text-slate-700" },
            { label: "Errors", value: String(tunnelErrors.length), sub: "detected", color: tunnelErrors.length > 0 ? "text-rose-700" : "text-slate-700" },
          ].map((stat) => (
            <div key={stat.label} className="bg-white px-6 py-3">
              <p className="text-xs text-slate-500">{stat.label}</p>
              <p className={`text-xl font-mono font-bold ${stat.color}`}>{stat.value}</p>
              <p className="text-xs text-slate-400">{stat.sub}</p>
            </div>
          ))}
        </div>
      )}

      {status.message && (
        <div className="border-b border-slate-200 bg-amber-50 px-6 py-2 text-sm text-amber-700">
          {status.message}
        </div>
      )}

      {/* Tabs */}
      <div className="flex border-b border-slate-200 bg-white">
        <TabButton tab="logs" active={activeTab} onClick={setActiveTab}>
          Logs
        </TabButton>
        <TabButton tab="connections" active={activeTab} onClick={setActiveTab}>
          Connections
          {connectionEvents.length > 0 && (
            <span className="ml-1.5 rounded-full bg-slate-200 px-1.5 text-xs text-slate-600">
              {connectionEvents.length}
            </span>
          )}
        </TabButton>
        <TabButton tab="errors" active={activeTab} onClick={setActiveTab}>
          Errors
          {tunnelErrors.length > 0 && (
            <span className="ml-1.5 rounded-full bg-rose-100 px-1.5 text-xs text-rose-700">
              {tunnelErrors.length}
            </span>
          )}
        </TabButton>
      </div>

      {/* Log area */}
      <div className="min-h-0 flex-1 bg-slate-950 p-4 text-xs text-slate-100">
        {activeTab === "logs" && (
          <div className="h-full overflow-y-auto font-mono">
            {logs.length === 0 ? (
              <p className="text-slate-500">Waiting for output…</p>
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
          <div className="h-full overflow-y-auto">
            {connectionEvents.length === 0 ? (
              <p className="text-slate-500">No connections tunneled yet.</p>
            ) : (
              [...connectionEvents].reverse().map((evt, idx) => (
                <div key={idx} className="mb-1 border-b border-slate-800 pb-1">
                  <span className="text-slate-400">{formatTime(evt.timestampMs)}</span>{" "}
                  <span className="text-emerald-400">{evt.srcAddr}</span>
                  <span className="text-slate-500"> → </span>
                  <span className="text-sky-300">{evt.dstAddr}</span>
                </div>
              ))
            )}
          </div>
        )}

        {activeTab === "errors" && (
          <div className="h-full overflow-y-auto">
            {tunnelErrors.length === 0 ? (
              <p className="text-slate-500">No errors.</p>
            ) : (
              [...tunnelErrors].reverse().map((err, idx) => (
                <div key={idx} className="mb-1 rounded border border-rose-800 bg-rose-950 p-1.5">
                  <span className="text-slate-400">{formatTime(err.timestampMs)}</span>{" "}
                  <span className="text-rose-300">{err.message}</span>
                </div>
              ))
            )}
          </div>
        )}
      </div>
    </div>
  );
}
