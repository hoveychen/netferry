import { useEffect, useRef, useState } from "react";
import type { ConnectionEvent, ConnectionStatus, Profile, TunnelError, TunnelStats } from "@/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface Props {
  status: ConnectionStatus;
  activeProfile: Profile | null;
  logs: string[];
  tunnelStats: TunnelStats | null;
  connectionEvents: ConnectionEvent[];
  tunnelErrors: TunnelError[];
  onConnect: () => Promise<void>;
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

interface TabButtonProps {
  tab: Tab;
  active: Tab;
  onClick: (t: Tab) => void;
  children: React.ReactNode;
}

function TabButton({ tab, active, onClick, children }: TabButtonProps) {
  return (
    <button
      className={`px-3 py-1.5 text-xs font-medium transition-colors ${
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

export function ConnectionPanel({
  status,
  activeProfile,
  logs,
  tunnelStats,
  connectionEvents,
  tunnelErrors,
  onConnect,
  onDisconnect,
}: Props) {
  const canConnect =
    !!activeProfile && status.state !== "connecting" && status.state !== "connected";
  const canDisconnect = status.state === "connected" || status.state === "connecting";

  const [activeTab, setActiveTab] = useState<Tab>("logs");
  const logEndRef = useRef<HTMLDivElement>(null);

  // Auto-scroll log view to bottom when new lines arrive.
  useEffect(() => {
    if (activeTab === "logs") {
      logEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [logs, activeTab]);

  // Auto-switch to errors tab when a new error arrives.
  useEffect(() => {
    if (tunnelErrors.length > 0) {
      setActiveTab("errors");
    }
  }, [tunnelErrors.length]);

  return (
    <Card className="flex h-full flex-col p-4">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-800">Connection Status</h2>
        <Badge variant={statusVariant(status.state)}>{status.state}</Badge>
      </div>
      <p className="mb-3 text-sm text-slate-600">
        Active profile: <span className="font-medium">{activeProfile?.name ?? "None"}</span>
      </p>
      <div className="mb-4 flex gap-2">
        <Button onClick={onConnect} disabled={!canConnect}>
          Connect
        </Button>
        <Button variant="danger" onClick={onDisconnect} disabled={!canDisconnect}>
          Disconnect
        </Button>
      </div>
      {status.message ? <p className="mb-3 text-sm text-slate-500">{status.message}</p> : null}

      {tunnelStats && (
        <div className="mb-3 grid grid-cols-2 gap-2 rounded-md border border-slate-200 bg-slate-50 p-2 text-xs">
          <div>
            <div className="text-slate-500">↓ Download</div>
            <div className="font-mono font-semibold text-emerald-700">
              {formatBytes(tunnelStats.rxBytesPerSec)}/s
            </div>
            <div className="text-slate-400">Total: {formatBytes(tunnelStats.totalRxBytes)}</div>
          </div>
          <div>
            <div className="text-slate-500">↑ Upload</div>
            <div className="font-mono font-semibold text-sky-700">
              {formatBytes(tunnelStats.txBytesPerSec)}/s
            </div>
            <div className="text-slate-400">Total: {formatBytes(tunnelStats.totalTxBytes)}</div>
          </div>
        </div>
      )}

      <div className="min-h-0 flex-1 flex flex-col">
        <div className="flex border-b border-slate-200 mb-1">
          <TabButton tab="logs" active={activeTab} onClick={setActiveTab}>
            Logs
          </TabButton>
          <TabButton tab="connections" active={activeTab} onClick={setActiveTab}>
            Connections
            {connectionEvents.length > 0 && (
              <span className="ml-1 rounded-full bg-slate-200 px-1 text-slate-600">
                {connectionEvents.length}
              </span>
            )}
          </TabButton>
          <TabButton tab="errors" active={activeTab} onClick={setActiveTab}>
            Errors
            {tunnelErrors.length > 0 && (
              <span className="ml-1 rounded-full bg-rose-100 px-1 text-rose-700">
                {tunnelErrors.length}
              </span>
            )}
          </TabButton>
        </div>

        <div className="min-h-0 flex-1 rounded-md border border-slate-200 bg-slate-950 p-2 text-xs text-slate-100 overflow-hidden">
          {activeTab === "logs" && (
            <div className="h-full overflow-y-auto font-mono">
              {logs.length === 0 ? <p className="text-slate-500">No logs yet</p> : null}
              {logs.map((line, idx) => (
                <p key={`${idx}-${line}`} className="whitespace-pre-wrap break-words">
                  {line}
                </p>
              ))}
              <div ref={logEndRef} />
            </div>
          )}

          {activeTab === "connections" && (
            <div className="h-full overflow-y-auto">
              {connectionEvents.length === 0 ? (
                <p className="text-slate-500">No connections tunneled yet</p>
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
                <p className="text-slate-500">No errors</p>
              ) : (
                [...tunnelErrors].reverse().map((err, idx) => (
                  <div key={idx} className="mb-1 rounded border border-rose-800 bg-rose-950 p-1">
                    <span className="text-slate-400">{formatTime(err.timestampMs)}</span>{" "}
                    <span className="text-rose-300">{err.message}</span>
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      </div>
    </Card>
  );
}
