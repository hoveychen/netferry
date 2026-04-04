import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Clipboard, Download, FileDown, Pencil, Plus, Share2 } from "lucide-react";
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

export function ProfileList({ profiles, onNew, onConnect, onEdit, onImport, onImportFile }: Props) {
  const { t } = useTranslation();
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
      alert(t("profileList.exportFailed", { error: err }));
    }
  };

  const handleExportFile = async (profile: Profile) => {
    setExportMenuId(null);
    try {
      const path = await showSaveDialog({
        title: t("exportDialog.title"),
        defaultPath: `${profile.name.replace(/[^a-zA-Z0-9_-]/g, "_")}.nfprofile`,
        filters: [{ name: "NetFerry Profile", extensions: ["nfprofile"] }],
      });
      if (!path) return;
      await exportProfileToFile(profile, path);
      setExportedId(profile.id);
      setTimeout(() => setExportedId(null), 2000);
    } catch (err) {
      alert(t("profileList.exportFailed", { error: err }));
    }
  };

  const handleImportFromFile = async () => {
    setImportError(null);
    try {
      const path = await showOpenDialog({
        title: t("importDialog.title"),
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
    <div className="flex h-full flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center justify-between border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <div className="flex items-center gap-2.5">
          <img src="/icon.png" alt="NetFerry" className="h-7 w-7 rounded-lg shadow-sm" />
          <span className="text-[15px] font-semibold tracking-tight text-white/90">{t("app.name")}</span>
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" size="sm" onClick={() => setImportDialogOpen(true)} title={t("profileList.importProfile")}>
            <Download className="h-4 w-4" />
          </Button>
          <Button size="sm" onClick={onNew}>
            <Plus className="mr-1 h-3.5 w-3.5" />
            {t("profileList.new")}
          </Button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-y-auto p-6">
        {profiles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            {/* Hero */}
            <div className="mb-6 flex h-24 w-24 items-center justify-center rounded-[1.75rem] bg-gradient-to-br from-[#0a84ff]/20 to-[#5e5ce6]/20 shadow-[0_0_60px_rgba(10,132,255,0.15)] ring-1 ring-white/[0.1]">
              <img src="/icon.png" alt="NetFerry" className="h-14 w-14 rounded-2xl" />
            </div>
            <h1 className="mb-2 text-2xl font-bold tracking-tight text-white/90">
              {t("welcome.title")}
            </h1>
            <p className="mb-8 max-w-sm text-sm leading-relaxed text-white/40">
              {t("welcome.description")}
            </p>

            {/* Feature highlights */}
            <div className="mb-8 grid w-full max-w-md grid-cols-3 gap-3">
              {[
                { icon: "🔒", title: t("welcome.encrypted"), desc: t("welcome.sshTunnel") },
                { icon: "⚡", title: t("welcome.fast"), desc: t("welcome.lowOverhead") },
                { icon: "🌍", title: t("welcome.global"), desc: t("welcome.anySshServer") },
              ].map((f) => (
                <div
                  key={f.title}
                  className="flex flex-col items-center rounded-2xl border border-white/[0.06] bg-white/[0.03] px-3 py-4"
                >
                  <span className="mb-1.5 text-xl">{f.icon}</span>
                  <span className="text-[13px] font-medium text-white/70">{f.title}</span>
                  <span className="text-[11px] text-white/30">{f.desc}</span>
                </div>
              ))}
            </div>

            <div className="flex items-center gap-3">
              <Button onClick={onNew}>
                <Plus className="mr-1.5 h-3.5 w-3.5" />
                {t("welcome.createProfile")}
              </Button>
              <Button variant="ghost" onClick={() => setImportDialogOpen(true)}>
                <Download className="mr-1.5 h-3.5 w-3.5" />
                {t("nav.import")}
              </Button>
            </div>
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
                        title={exportedId === profile.id ? t("profileList.exported") : t("profileList.exportProfile")}
                      >
                        {exportedId === profile.id ? (
                          <span className="text-[11px] text-emerald-400">{t("profileList.done")}</span>
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
                            {t("profileList.copyToClipboard")}
                          </button>
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-white/70 hover:bg-white/[0.08]"
                            onClick={() => handleExportFile(profile)}
                          >
                            <FileDown className="h-3.5 w-3.5" />
                            {t("profileList.saveAsNfprofile")}
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
                    title={profile.imported ? t("profileList.renameProfile") : t("profileList.editProfile")}
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
                      {profile.remote || t("profileList.noRemoteSet")}
                    </p>
                  </div>
                </div>

                <div className="mt-auto flex flex-wrap items-center gap-1.5 border-t border-white/[0.05] pt-3">
                  <span className="rounded-md bg-white/[0.06] px-2 py-0.5 font-mono text-[11px] text-white/40">
                    DNS: {profile.dns}
                  </span>
                  {profile.autoExcludeLan && (
                    <span className="rounded-md bg-white/[0.06] px-2 py-0.5 text-[11px] text-white/40">
                      {t("profileList.lanExcl")}
                    </span>
                  )}
                  {profile.imported && (
                    <span className="rounded-md bg-[#0a84ff]/15 px-2 py-0.5 text-[11px] text-[#0a84ff]/70">
                      {t("profileList.imported")}
                    </span>
                  )}
                  <span className="ml-auto text-[11px] text-white/20 transition-colors group-hover:text-[#0a84ff]">
                    {t("profileList.connect")}
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
            <h2 className="mb-4 text-lg font-semibold text-white/90">{t("importDialog.title")}</h2>

            {/* Open file */}
            <button
              type="button"
              className="mb-4 flex w-full items-center justify-center gap-2 rounded-xl border border-dashed border-white/[0.15] bg-white/[0.03] px-4 py-4 text-sm text-white/50 transition-colors hover:border-[#0a84ff]/40 hover:bg-[#0a84ff]/[0.05] hover:text-white/70"
              onClick={handleImportFromFile}
              disabled={importing}
            >
              <Download className="h-4 w-4" />
              {t("importDialog.openFile")}
            </button>

            <div className="mb-3 flex items-center gap-3">
              <div className="h-px flex-1 bg-white/[0.08]" />
              <span className="text-[11px] text-white/25">{t("importDialog.orPasteText")}</span>
              <div className="h-px flex-1 bg-white/[0.08]" />
            </div>

            <textarea
              className="mb-3 w-full rounded-xl border border-white/[0.1] bg-white/[0.05] px-4 py-3 font-mono text-xs text-white/80 placeholder-white/25 focus:border-[#0a84ff]/50 focus:outline-none"
              rows={5}
              value={importText}
              onChange={(e) => setImportText(e.target.value)}
              placeholder={t("importDialog.placeholder")}
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
                {t("nav.cancel")}
              </Button>
              <Button size="sm" onClick={handleImportFromText} disabled={importing || !importText.trim()}>
                {importing ? t("importDialog.importing") : t("nav.import")}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
