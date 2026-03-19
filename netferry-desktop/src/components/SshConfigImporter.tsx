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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-2xl border border-white/[0.10] bg-[#2c2c2e] p-6 shadow-2xl shadow-black/60">
        <h3 className="mb-1 text-[17px] font-semibold text-white/90">
          Import from ~/.ssh/config
        </h3>
        <p className="mb-5 text-sm text-white/45">
          Select a host to pre-fill a new profile. You can review and edit before saving.
        </p>

        {loading && <p className="text-sm text-white/40">Loading…</p>}
        {error && <p className="text-sm text-[#ff453a]">{error}</p>}

        {!loading && !error && (
          <>
            <select
              className="mb-3.5 h-9 w-full rounded-lg border border-white/[0.10] bg-[#3a3a3c] px-3 py-2 text-sm text-white/90 outline-none transition-all focus:border-[#0a84ff]/60 focus:ring-2 focus:ring-[#0a84ff]/15 cursor-pointer"
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
              <div className="mb-5 rounded-xl border border-white/[0.07] bg-white/[0.04] p-4 text-sm">
                <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                  {(
                    [
                      ["HostName", selected.hostName],
                      ["User", selected.user],
                      ["Port", selected.port],
                      ["IdentityFile", selected.identityFile],
                      ...(selected.proxyJump ? [["ProxyJump", selected.proxyJump]] : []),
                    ] as [string, string | number | undefined][]
                  ).map(([label, value]) => (
                    <>
                      <span key={`l-${label}`} className="text-white/40">{label}</span>
                      <span key={`v-${label}`} className="truncate font-mono text-white/75">
                        {value ?? "—"}
                      </span>
                    </>
                  ))}
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
