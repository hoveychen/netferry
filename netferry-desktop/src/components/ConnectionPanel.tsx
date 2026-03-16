import type { ConnectionStatus, Profile } from "@/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface Props {
  status: ConnectionStatus;
  activeProfile: Profile | null;
  logs: string[];
  onConnect: () => Promise<void>;
  onDisconnect: () => Promise<void>;
}

function statusVariant(state: ConnectionStatus["state"]) {
  if (state === "connected") return "green";
  if (state === "connecting") return "yellow";
  if (state === "error") return "red";
  return "gray";
}

export function ConnectionPanel({
  status,
  activeProfile,
  logs,
  onConnect,
  onDisconnect,
}: Props) {
  const canConnect = !!activeProfile && status.state !== "connecting" && status.state !== "connected";
  const canDisconnect = status.state === "connected" || status.state === "connecting";
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
      <div className="min-h-0 flex-1 rounded-md border border-slate-200 bg-slate-950 p-2 text-xs text-slate-100">
        <div className="mb-1 text-slate-400">Live Logs</div>
        <div className="h-full overflow-y-auto font-mono">
          {logs.length === 0 ? <p className="text-slate-500">No logs yet</p> : null}
          {logs.map((line, idx) => (
            <p key={`${idx}-${line}`} className="whitespace-pre-wrap break-words">
              {line}
            </p>
          ))}
        </div>
      </div>
    </Card>
  );
}
