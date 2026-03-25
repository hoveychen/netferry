import { useEffect, useState } from "react";
import { Clipboard, Download, FileDown, Pencil, Plus, Settings, Share2 } from "lucide-react";
import { save as showSaveDialog, open as showOpenDialog } from "@tauri-apps/plugin-dialog";
import type { Profile } from "@/types";
import { exportProfile, exportProfileToFile } from "@/api";
import { Button } from "@/components/ui/button";
import { countryCodeToFlag, getRegionInfo, type RegionInfo } from "@/lib/geoip";

interface Props {
  profiles: Profile[];
  onNew: () => void;
  onConnect: (profile: Profile) => void;
  onEdit: (id: string) => void;
  onOpenSettings: () => void;
  onImport: (data: string) => Promise<void>;
  onImportFile: (path: string) => Promise<void>;
}

const AVATAR_GRADIENTS = [
  "from-blue-500 to-indigo-600",
  "from-teal-500 to-cyan-600",
  "from-violet-500 to-purple-600",
  "from-rose-500 to-pink-600",
  "from-amber-500 to-orange-600",
  "from-emerald-500 to-green-600",
];

function ProfileAvatar({ profile, region }: { profile: Profile; region?: RegionInfo }) {
  if (region?.type === "country") {
    return (
      <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-white/[0.07] text-2xl shadow-[inset_0_1px_0_rgba(255,255,255,0.08)] ring-1 ring-white/[0.08]">
        {countryCodeToFlag(region.countryCode)}
      </div>
    );
  }
  if (region?.type === "lan" || region?.type === "loopback") {
    return (
      <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-white/[0.07] text-xl shadow-[inset_0_1px_0_rgba(255,255,255,0.08)] ring-1 ring-white/[0.08]">
        🏠
      </div>
    );
  }
  const idx = profile.name.charCodeAt(0) % AVATAR_GRADIENTS.length;
  return (
    <div
      className={`flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-gradient-to-br ${AVATAR_GRADIENTS[idx]} text-sm font-bold text-white shadow-lg`}
    >
      {profile.name.charAt(0).toUpperCase()}
    </div>
  );
}

/** A profile is exportable when all identity material is inline (no file paths). */
function isExportable(profile: Profile): boolean {
  if (!profile.identityKey?.trim()) return false;
  for (const jh of profile.jumpHosts ?? []) {
    if (jh.identityFile?.trim() && !jh.identityKey?.trim()) return false;
  }
  return true;
}

export function ProfileList({ profiles, onNew, onConnect, onEdit, onOpenSettings, onImport, onImportFile }: Props) {
  const [regionMap, setRegionMap] = useState<Record<string, RegionInfo>>({});
  const [exportMenuId, setExportMenuId] = useState<string | null>(null);
  const [exportedId, setExportedId] = useState<string | null>(null);
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [importError, setImportError] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);

  // Close export menu on outside click
  useEffect(() => {
    if (!exportMenuId) return;
    const close = () => setExportMenuId(null);
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, [exportMenuId]);

  const handleExportClipboard = async (profile: Profile) => {
    setExportMenuId(null);
    try {
      const encrypted = await exportProfile(profile);
      await navigator.clipboard.writeText(encrypted);
      setExportedId(profile.id);
      setTimeout(() => setExportedId(null), 2000);
    } catch (err) {
      alert(`Export failed: ${err}`);
    }
  };

  const handleExportFile = async (profile: Profile) => {
    setExportMenuId(null);
    try {
      const path = await showSaveDialog({
        title: "Export Profile",
        defaultPath: `${profile.name.replace(/[^a-zA-Z0-9_-]/g, "_")}.nfprofile`,
        filters: [{ name: "NetFerry Profile", extensions: ["nfprofile"] }],
      });
      if (!path) return;
      await exportProfileToFile(profile, path);
      setExportedId(profile.id);
      setTimeout(() => setExportedId(null), 2000);
    } catch (err) {
      alert(`Export failed: ${err}`);
    }
  };

  const handleImportFromFile = async () => {
    setImportError(null);
    try {
      const path = await showOpenDialog({
        title: "Import Profile",
        filters: [{ name: "NetFerry Profile", extensions: ["nfprofile"] }],
        multiple: false,
        directory: false,
      });
      if (!path) return;
      setImporting(true);
      await onImportFile(path);
      setImportDialogOpen(false);
      setImportText("");
    } catch (err) {
      setImportError(String(err));
    } finally {
      setImporting(false);
    }
  };

  const handleImportFromText = async () => {
    if (!importText.trim()) return;
    setImporting(true);
    setImportError(null);
    try {
      await onImport(importText.trim());
      setImportDialogOpen(false);
      setImportText("");
    } catch (err) {
      setImportError(String(err));
    } finally {
      setImporting(false);
    }
  };

  useEffect(() => {
    for (const profile of profiles) {
      getRegionInfo(profile.remote).then((info) => {
        setRegionMap((prev) => ({ ...prev, [profile.id]: info }));
      });
    }
  }, [profiles]);

  return (
    <div className="flex h-screen flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <div className="flex items-center gap-2.5">
          <img src="/icon.png" alt="NetFerry" className="h-7 w-7 rounded-lg shadow-sm" />
          <span className="text-[15px] font-semibold tracking-tight text-white/90">NetFerry</span>
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" size="sm" onClick={onOpenSettings} title="Settings">
            <Settings className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setImportDialogOpen(true)} title="Import profile">
            <Download className="h-4 w-4" />
          </Button>
          <Button size="sm" onClick={onNew}>
            <Plus className="mr-1 h-3.5 w-3.5" />
            New
          </Button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        {profiles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-24 text-center">
            <div className="mb-5 flex h-20 w-20 items-center justify-center rounded-3xl bg-white/[0.05] ring-1 ring-white/[0.07]">
              <img src="/icon.png" alt="NetFerry" className="h-12 w-12 rounded-2xl opacity-50" />
            </div>
            <p className="mb-1.5 text-[17px] font-semibold text-white/80">No profiles yet</p>
            <p className="mb-6 max-w-xs text-sm leading-relaxed text-white/35">
              Create a profile to start tunneling traffic securely via SSH.
            </p>
            <Button onClick={onNew}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Profile
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {profiles.map((profile) => (
              <div
                key={profile.id}
                className="group relative flex cursor-pointer flex-col rounded-2xl border border-white/[0.07] bg-white/[0.04] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.05)] transition-all duration-200 hover:-translate-y-0.5 hover:border-white/[0.13] hover:bg-white/[0.07] hover:shadow-2xl hover:shadow-black/40"
                onClick={() => onConnect(profile)}
              >
                {/* Action buttons */}
                <div className="absolute right-3.5 top-3.5 flex gap-1 opacity-0 transition-all group-hover:opacity-100">
                  {isExportable(profile) && (
                    <div className="relative">
                      <button
                        type="button"
                        className="rounded-lg p-1.5 text-white/20 transition-all hover:bg-white/[0.08] hover:text-white/65"
                        onClick={(e) => {
                          e.stopPropagation();
                          setExportMenuId(exportMenuId === profile.id ? null : profile.id);
                        }}
                        title={exportedId === profile.id ? "Exported!" : "Export profile"}
                      >
                        {exportedId === profile.id ? (
                          <span className="text-[11px] text-emerald-400">Done!</span>
                        ) : (
                          <Share2 className="h-3.5 w-3.5" />
                        )}
                      </button>
                      {exportMenuId === profile.id && (
                        <div
                          className="absolute right-0 top-full z-50 mt-1 w-48 rounded-xl border border-white/[0.1] bg-[#2c2c2e] py-1 shadow-2xl"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-white/70 hover:bg-white/[0.08]"
                            onClick={() => handleExportClipboard(profile)}
                          >
                            <Clipboard className="h-3.5 w-3.5" />
                            Copy to clipboard
                          </button>
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-white/70 hover:bg-white/[0.08]"
                            onClick={() => handleExportFile(profile)}
                          >
                            <FileDown className="h-3.5 w-3.5" />
                            Save as .nfprofile
                          </button>
                        </div>
                      )}
                    </div>
                  )}
                  <button
                    type="button"
                    className="rounded-lg p-1.5 text-white/20 transition-all hover:bg-white/[0.08] hover:text-white/65"
                    onClick={(e) => {
                      e.stopPropagation();
                      onEdit(profile.id);
                    }}
                    title={profile.imported ? "Rename profile" : "Edit profile"}
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </button>
                </div>

                <div className="mb-4 flex items-center gap-3">
                  <ProfileAvatar profile={profile} region={regionMap[profile.id]} />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[15px] font-semibold text-white/90">
                      {profile.name}
                    </p>
                    <p className="truncate text-xs text-white/38 mt-0.5">
                      {profile.remote || "No remote set"}
                    </p>
                  </div>
                </div>

                <div className="mt-auto flex flex-wrap items-center gap-1.5 border-t border-white/[0.05] pt-3">
                  <span className="rounded-md bg-white/[0.06] px-2 py-0.5 font-mono text-[11px] text-white/40">
                    DNS: {profile.dns}
                  </span>
                  {profile.autoExcludeLan && (
                    <span className="rounded-md bg-white/[0.06] px-2 py-0.5 text-[11px] text-white/40">
                      LAN excl.
                    </span>
                  )}
                  {profile.imported && (
                    <span className="rounded-md bg-[#0a84ff]/15 px-2 py-0.5 text-[11px] text-[#0a84ff]/70">
                      Imported
                    </span>
                  )}
                  <span className="ml-auto text-[11px] text-white/20 transition-colors group-hover:text-[#0a84ff]">
                    Connect →
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Import dialog */}
      {importDialogOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm">
          <div className="w-full max-w-lg rounded-2xl border border-white/[0.1] bg-[#2c2c2e] p-6 shadow-2xl">
            <h2 className="mb-4 text-lg font-semibold text-white/90">Import Profile</h2>

            {/* Open file */}
            <button
              type="button"
              className="mb-4 flex w-full items-center justify-center gap-2 rounded-xl border border-dashed border-white/[0.15] bg-white/[0.03] px-4 py-4 text-sm text-white/50 transition-colors hover:border-[#0a84ff]/40 hover:bg-[#0a84ff]/[0.05] hover:text-white/70"
              onClick={handleImportFromFile}
              disabled={importing}
            >
              <Download className="h-4 w-4" />
              Open .nfprofile file
            </button>

            <div className="mb-3 flex items-center gap-3">
              <div className="h-px flex-1 bg-white/[0.08]" />
              <span className="text-[11px] text-white/25">or paste text</span>
              <div className="h-px flex-1 bg-white/[0.08]" />
            </div>

            <textarea
              className="mb-3 w-full rounded-xl border border-white/[0.1] bg-white/[0.05] px-4 py-3 font-mono text-xs text-white/80 placeholder-white/25 focus:border-[#0a84ff]/50 focus:outline-none"
              rows={5}
              value={importText}
              onChange={(e) => setImportText(e.target.value)}
              placeholder="Paste encrypted profile data here..."
            />
            {importError && (
              <p className="mb-3 rounded-lg border border-[#ff453a]/20 bg-[#ff453a]/[0.08] px-3 py-2 text-sm text-[#ff453a]">
                {importError}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setImportDialogOpen(false);
                  setImportText("");
                  setImportError(null);
                }}
              >
                Cancel
              </Button>
              <Button size="sm" onClick={handleImportFromText} disabled={importing || !importText.trim()}>
                {importing ? "Importing..." : "Import"}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
