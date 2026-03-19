import { useEffect, useState } from "react";
import { importSshHosts } from "@/api";
import type { Profile, SshHostEntry } from "@/types";
import { Button } from "@/components/ui/button";
import { newProfile } from "@/stores/profileStore";

interface Props {
  open: boolean;
  onClose: () => void;
  /** Called with a fully built (unsaved) profile ready for the detail page. */
  onImport: (profile: Profile) => void;
}

function buildRemote(entry: SshHostEntry): string {
  const host = entry.hostName ?? entry.host;
  const withUser = entry.user ? `${entry.user}@${host}` : host;
  return entry.port ? `${withUser}:${entry.port}` : withUser;
}

export function SshConfigImporter({ open, onClose, onImport }: Props) {
  const [hosts, setHosts] = useState<SshHostEntry[]>([]);
  const [selectedHost, setSelectedHost] = useState<string>("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>("");

  useEffect(() => {
    if (!open) return;
    setLoading(true);
    setError("");
    importSshHosts()
      .then((items) => {
        setHosts(items);
        setSelectedHost(items[0]?.host ?? "");
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  }, [open]);

  if (!open) return null;

  const selected = hosts.find((h) => h.host === selectedHost);

  const handleImport = () => {
    if (!selected) return;
    const sshParts: string[] = [];
    if (selected.proxyJump) sshParts.push(`-J ${selected.proxyJump}`);
    if (selected.proxyCommand) sshParts.push(`-o ProxyCommand='${selected.proxyCommand}'`);

    const profile: Profile = {
      ...newProfile(),
      name: selected.host,
      remote: buildRemote(selected),
      identityFile: selected.identityFile ?? "",
      extraSshOptions: sshParts.join(" ") || undefined,
    };
    onImport(profile);
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-lg rounded-xl border border-slate-200 bg-white p-6 shadow-lg">
        <h3 className="mb-1 text-lg font-semibold text-slate-800">Import from ~/.ssh/config</h3>
        <p className="mb-4 text-sm text-slate-500">
          Select a host to pre-fill a new profile. You can review and edit before saving.
        </p>

        {loading && <p className="text-sm text-slate-500">Loading…</p>}
        {error && <p className="text-sm text-rose-600">{error}</p>}

        {!loading && !error && (
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

            {selected && (
              <div className="mb-4 rounded-lg border border-slate-200 bg-slate-50 p-3 text-sm">
                <div className="grid grid-cols-2 gap-1">
                  <span className="text-slate-500">HostName</span>
                  <span className="font-mono text-slate-800">{selected.hostName ?? "—"}</span>
                  <span className="text-slate-500">User</span>
                  <span className="font-mono text-slate-800">{selected.user ?? "—"}</span>
                  <span className="text-slate-500">Port</span>
                  <span className="font-mono text-slate-800">{selected.port ?? "—"}</span>
                  <span className="text-slate-500">IdentityFile</span>
                  <span className="font-mono text-slate-800 break-all">{selected.identityFile ?? "—"}</span>
                  {selected.proxyJump && (
                    <>
                      <span className="text-slate-500">ProxyJump</span>
                      <span className="font-mono text-slate-800">{selected.proxyJump}</span>
                    </>
                  )}
                </div>
              </div>
            )}
          </>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleImport} disabled={!selected || loading}>
            Import
          </Button>
        </div>
      </div>
    </div>
  );
}
