import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { importSshHosts } from "@/api";
import type { JumpHost, Profile, SshHostEntry } from "@/types";
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

/** Try to extract a jump host from a ProxyCommand of the form:
 *    ssh -W %h:%p <host-alias>
 *  or  ssh -W %h:%p [-p port] [user@]hostname
 *  Returns null if the command doesn't match. */
function parseProxyCommandAsJump(cmd: string, allHosts: SshHostEntry[]): JumpHost[] | null {
  // Match: ssh [options...] -W %h:%p <destination>
  // The -W %h:%p can appear anywhere in the args.
  const trimmed = cmd.trim();
  // Must start with "ssh " and contain "-W %h:%p"
  if (!/^ssh\s/i.test(trimmed) || !trimmed.includes("-W %h:%p")) {
    return null;
  }
  // Remove "ssh" prefix and "-W %h:%p", then parse remaining tokens.
  const rest = trimmed
    .replace(/^ssh\s+/, "")
    .replace(/-W\s+%h:%p/, "")
    .trim();
  const tokens = rest.split(/\s+/).filter(Boolean);

  // Consume optional flags: -p <port>, -i <identity>, -o <option>=...
  let port: string | undefined;
  let identity: string | undefined;
  const remaining: string[] = [];
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i] === "-p" && i + 1 < tokens.length) {
      port = tokens[++i];
    } else if (tokens[i] === "-i" && i + 1 < tokens.length) {
      identity = tokens[++i];
    } else if (tokens[i] === "-o" && i + 1 < tokens.length) {
      i++; // skip option value
    } else if (tokens[i].startsWith("-")) {
      // skip unknown single flags
    } else {
      remaining.push(tokens[i]);
    }
  }

  // The last remaining token should be the destination (host alias or [user@]host).
  const dest = remaining.pop();
  if (!dest) return null;

  // Check if dest is a known Host alias.
  const entry = allHosts.find((h) => h.host === dest);
  if (entry) {
    return [{ remote: buildRemote(entry), identityFile: entry.identityFile ?? identity }];
  }

  // Treat as literal [user@]host[:port].
  let remote = dest;
  if (port) remote += `:${port}`;
  return [{ remote, identityFile: identity }];
}

/** Resolve a ProxyJump chain into JumpHost entries by looking up each hop in
 *  the parsed SSH config.  Falls back to using the hop string as-is when no
 *  matching Host entry exists. */
function resolveJumpHosts(proxyJump: string, allHosts: SshHostEntry[]): JumpHost[] {
  const hops = proxyJump.split(",").map((h) => h.trim()).filter(Boolean);
  return hops.map((hop) => {
    // Try to match a known Host alias.
    const entry = allHosts.find((h) => h.host === hop);
    if (entry) {
      return {
        remote: buildRemote(entry),
        identityFile: entry.identityFile ?? undefined,
      };
    }
    // No match — treat hop as a literal [user@]host[:port] spec.
    return { remote: hop };
  });
}

export function SshConfigImporter({ open, onClose, onImport }: Props) {
  const { t } = useTranslation();
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

    // Resolve ProxyJump chain into native jumpHosts.
    let jumpHosts: JumpHost[] | undefined;
    if (selected.proxyJump) {
      const resolved = resolveJumpHosts(selected.proxyJump, hosts);
      if (resolved.length > 0) jumpHosts = resolved;
    }

    // Try to interpret ProxyCommand as a jump host (ssh -W %h:%p pattern).
    // Fall back to extraSshOptions for unrecognised commands.
    const sshParts: string[] = [];
    if (selected.proxyCommand && !selected.proxyJump) {
      const parsed = parseProxyCommandAsJump(selected.proxyCommand, hosts);
      if (parsed && parsed.length > 0) {
        jumpHosts = parsed;
      } else {
        sshParts.push(`-o ProxyCommand='${selected.proxyCommand}'`);
      }
    }

    const profile: Profile = {
      ...newProfile(),
      name: selected.host,
      remote: buildRemote(selected),
      identityFile: selected.identityFile ?? "",
      jumpHosts,
      extraSshOptions: sshParts.join(" ") || undefined,
    };
    onImport(profile);
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-2xl border border-white/[0.10] bg-[#2c2c2e] p-6 shadow-2xl shadow-black/60">
        <h3 className="mb-1 text-[17px] font-semibold text-white/90">
          {t("sshImporter.title")}
        </h3>
        <p className="mb-5 text-sm text-white/45">
          {t("sshImporter.subtitle")}
        </p>

        {loading && <p className="text-sm text-white/40">{t("sshImporter.loading")}</p>}
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
                {selected.proxyJump && (
                  <div className="mt-3 border-t border-white/[0.06] pt-3">
                    <span className="text-white/40">{t("sshImporter.jumpHosts")}</span>
                    <div className="mt-1 space-y-1">
                      {resolveJumpHosts(selected.proxyJump, hosts).map((jh, i) => (
                        <div key={i} className="flex items-center gap-2 font-mono text-white/75">
                          <span className="text-[10px] text-white/25">{i + 1}.</span>
                          <span className="truncate">{jh.remote}</span>
                          {jh.identityFile && (
                            <span className="truncate text-white/35 text-xs">({jh.identityFile})</span>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
                {selected.proxyCommand && !selected.proxyJump && (() => {
                  const parsed = parseProxyCommandAsJump(selected.proxyCommand, hosts);
                  if (parsed && parsed.length > 0) {
                    return (
                      <div className="mt-3 border-t border-white/[0.06] pt-3">
                        <span className="text-white/40">{t("sshImporter.jumpHosts")}</span>
                        <span className="ml-2 text-[10px] text-white/25">{t("sshImporter.fromProxyCommand")}</span>
                        <div className="mt-1 space-y-1">
                          {parsed.map((jh, i) => (
                            <div key={i} className="flex items-center gap-2 font-mono text-white/75">
                              <span className="text-[10px] text-white/25">{i + 1}.</span>
                              <span className="truncate">{jh.remote}</span>
                              {jh.identityFile && (
                                <span className="truncate text-white/35 text-xs">({jh.identityFile})</span>
                              )}
                            </div>
                          ))}
                        </div>
                      </div>
                    );
                  }
                  return (
                    <div className="mt-3 border-t border-white/[0.06] pt-3">
                      <span className="text-white/40">ProxyCommand</span>
                      <p className="mt-1 truncate font-mono text-white/75">{selected.proxyCommand}</p>
                    </div>
                  );
                })()}
              </div>
            )}
          </>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t("nav.cancel")}
          </Button>
          <Button onClick={handleImport} disabled={!selected || loading}>
            {t("nav.import")}
          </Button>
        </div>
      </div>
    </div>
  );
}
