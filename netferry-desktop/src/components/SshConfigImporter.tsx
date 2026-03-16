import { useEffect, useState } from "react";
import { importSshHosts } from "@/api";
import type { Profile, SshHostEntry } from "@/types";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";

interface Props {
  open: boolean;
  onClose: () => void;
  onApply: (next: Partial<Profile>) => Promise<void>;
}

function buildRemote(entry: SshHostEntry): string {
  const host = entry.hostName ?? entry.host;
  const withUser = entry.user ? `${entry.user}@${host}` : host;
  return entry.port ? `${withUser}:${entry.port}` : withUser;
}

export function SshConfigImporter({ open, onClose, onApply }: Props) {
  const [hosts, setHosts] = useState<SshHostEntry[]>([]);
  const [selectedHost, setSelectedHost] = useState<string>("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>("");
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<string>("");

  useEffect(() => {
    if (!open) {
      return;
    }
    setLoading(true);
    importSshHosts()
      .then((items) => {
        setHosts(items);
        setSelectedHost(items[0]?.host ?? "");
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  }, [open]);

  if (!open) {
    return null;
  }

  const selected = hosts.find((h) => h.host === selectedHost);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <Card className="w-full max-w-2xl p-4">
        <h3 className="mb-3 text-lg font-semibold">Import from ~/.ssh/config</h3>
        {loading ? <p className="text-sm text-slate-500">Loading...</p> : null}
        {error ? <p className="text-sm text-rose-600">{error}</p> : null}
        {!loading && !error ? (
          <>
            <select
              className="mb-3 h-9 w-full rounded-md border border-slate-300 px-2 text-sm"
              value={selectedHost}
              onChange={(e) => setSelectedHost(e.target.value)}
            >
              {hosts.map((h) => (
                <option key={h.host} value={h.host}>
                  {h.host}
                </option>
              ))}
            </select>
            {selected ? (
              <div className="mb-3 rounded-md border border-slate-200 bg-slate-50 p-3 text-sm">
                <p>
                  HostName: <span className="font-mono">{selected.hostName ?? "-"}</span>
                </p>
                <p>
                  User: <span className="font-mono">{selected.user ?? "-"}</span>
                </p>
                <p>
                  Port: <span className="font-mono">{selected.port ?? "-"}</span>
                </p>
                <p>
                  IdentityFile:{" "}
                  <span className="font-mono">{selected.identityFile ?? "-"}</span>
                </p>
              </div>
            ) : null}
          </>
        ) : null}
        {applyError ? (
          <p className="mb-2 text-sm text-rose-600">{applyError}</p>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose} disabled={applying}>
            Cancel
          </Button>
          <Button
            onClick={async () => {
              if (!selected) {
                return;
              }
              setApplyError("");
              setApplying(true);
              try {
                const sshParts: string[] = [];
                if (selected.proxyJump) {
                  sshParts.push(`-J ${selected.proxyJump}`);
                }
                if (selected.proxyCommand) {
                  sshParts.push(`-o ProxyCommand='${selected.proxyCommand}'`);
                }
                await onApply({
                  name: selected.host,
                  remote: buildRemote(selected),
                  identityFile: selected.identityFile ?? "",
                  extraSshOptions: sshParts.join(" ") || undefined,
                });
                onClose();
              } catch (e) {
                setApplyError(String(e));
              } finally {
                setApplying(false);
              }
            }}
            disabled={!selected || applying}
          >
            {applying ? "Applying..." : "Import and Apply"}
          </Button>
        </div>
      </Card>
    </div>
  );
}
