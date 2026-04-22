import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Clipboard, Download, FileDown, Pencil, Plus, QrCode, Share2 } from "lucide-react";
import { save as showSaveDialog } from "@tauri-apps/plugin-dialog";
import type { Profile } from "@/types";
import { exportProfile, exportProfileToFile } from "@/api";
import { Button } from "@/components/ui/button";
import { ImportProfileDialog } from "@/components/ImportProfileDialog";
import { QrCodeExportDialog } from "@/components/QrCodeExportDialog";
import { countryCodeToFlag, getRegionInfo, type RegionInfo } from "@/lib/geoip";

interface Props {
  profiles: Profile[];
  connectedProfileId?: string;
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
      <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-ov-6 text-2xl shadow-[inset_0_1px_0_var(--inset-highlight)] ring-1 ring-bdr">
        {countryCodeToFlag(region.countryCode)}
      </div>
    );
  }
  if (region?.type === "lan" || region?.type === "loopback") {
    return (
      <div className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl bg-ov-6 text-xl shadow-[inset_0_1px_0_var(--inset-highlight)] ring-1 ring-bdr">
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

export function ProfileList({ profiles, connectedProfileId, onNew, onConnect, onEdit, onImport, onImportFile }: Props) {
  const { t } = useTranslation();
  const [regionMap, setRegionMap] = useState<Record<string, RegionInfo>>({});
  const [exportMenuId, setExportMenuId] = useState<string | null>(null);
  const [exportedId, setExportedId] = useState<string | null>(null);
  const [importDialogOpen, setImportDialogOpen] = useState(false);
  const [qrProfile, setQrProfile] = useState<Profile | null>(null);

  // Close export menu on outside click
  useEffect(() => {
    if (!exportMenuId) return;
    const close = () => setExportMenuId(null);
    window.addEventListener("click", close);
    return () => window.removeEventListener("click", close);
  }, [exportMenuId]);

  // Open import dialog when triggered from app menu bar
  useEffect(() => {
    const handler = () => setImportDialogOpen(true);
    window.addEventListener("menu-open-import", handler);
    return () => window.removeEventListener("menu-open-import", handler);
  }, []);

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

  useEffect(() => {
    for (const profile of profiles) {
      getRegionInfo(profile.remote).then((info) => {
        setRegionMap((prev) => ({ ...prev, [profile.id]: info }));
      });
    }
  }, [profiles]);

  return (
    <div className="flex h-full flex-col">
      {/* Toolbar */}
      <div className="flex h-[52px] items-center justify-between px-6">
        <h1 className="text-[15px] font-semibold text-t1">{t("nav.profiles")}</h1>
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
            <div className="mb-6 flex h-24 w-24 items-center justify-center rounded-[1.75rem] bg-gradient-to-br from-accent/20 to-[#5e5ce6]/20 shadow-[0_0_60px_color-mix(in_srgb,var(--accent)_15%,transparent)] ring-1 ring-bdr">
              <img src="/icon.png" alt="NetFerry" className="h-14 w-14 rounded-2xl" />
            </div>
            <h1 className="mb-2 text-2xl font-bold tracking-tight text-t1">
              {t("welcome.title")}
            </h1>
            <p className="mb-8 max-w-sm text-sm leading-relaxed text-t3">
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
                  className="flex flex-col items-center rounded-2xl border border-sep bg-ov-3 px-3 py-4"
                >
                  <span className="mb-1.5 text-xl">{f.icon}</span>
                  <span className="text-[13px] font-medium text-t2">{f.title}</span>
                  <span className="text-[11px] text-t4">{f.desc}</span>
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
            {profiles.map((profile) => {
              const isActive = profile.id === connectedProfileId;
              return (
              <div
                key={profile.id}
                className={`group relative flex flex-col rounded-2xl border p-5 shadow-[inset_0_1px_0_var(--inset-highlight)] transition-all duration-200 ${
                  isActive
                    ? "border-accent/30 bg-accent/[0.06] ring-1 ring-accent/20"
                    : "cursor-pointer border-sep bg-ov-4 hover:-translate-y-0.5 hover:border-edge hover:bg-ov-6 hover:shadow-2xl hover:shadow-black/40"
                }`}
                onClick={() => !isActive && onConnect(profile)}
              >
                {/* Action buttons */}
                <div className="absolute right-3.5 top-3.5 flex gap-1 opacity-0 transition-all group-hover:opacity-100">
                  {isExportable(profile) && (
                    <div className="relative">
                      <button
                        type="button"
                        className="rounded-lg p-1.5 text-t5 transition-all hover:bg-ov-8 hover:text-t2"
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
                          className="absolute right-0 top-full z-50 mt-1 w-48 rounded-xl border border-bdr bg-elevated py-1 shadow-2xl"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-t2 hover:bg-ov-8"
                            onClick={() => handleExportClipboard(profile)}
                          >
                            <Clipboard className="h-3.5 w-3.5" />
                            {t("profileList.copyToClipboard")}
                          </button>
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-t2 hover:bg-ov-8"
                            onClick={() => handleExportFile(profile)}
                          >
                            <FileDown className="h-3.5 w-3.5" />
                            {t("profileList.saveAsNfprofile")}
                          </button>
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-t2 hover:bg-ov-8"
                            onClick={() => {
                              setExportMenuId(null);
                              setQrProfile(profile);
                            }}
                          >
                            <QrCode className="h-3.5 w-3.5" />
                            {t("profileList.showQrCode")}
                          </button>
                        </div>
                      )}
                    </div>
                  )}
                  {!isActive && (
                  <button
                    type="button"
                    className="rounded-lg p-1.5 text-t5 transition-all hover:bg-ov-8 hover:text-t2"
                    onClick={(e) => {
                      e.stopPropagation();
                      onEdit(profile.id);
                    }}
                    title={t("profileList.editProfile")}
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </button>
                  )}
                </div>

                <div className="mb-4 flex items-center gap-3">
                  <div className="relative">
                    <ProfileAvatar profile={profile} region={regionMap[profile.id]} />
                    {isActive && (
                      <span className="absolute -bottom-0.5 -right-0.5 flex h-3 w-3 items-center justify-center">
                        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-50" />
                        <span className="relative inline-flex h-2 w-2 rounded-full bg-success" />
                      </span>
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[15px] font-semibold text-t1">
                      {profile.name}
                    </p>
                    <p className="truncate text-xs text-t3 mt-0.5">
                      {profile.remote || t("profileList.noRemoteSet")}
                    </p>
                  </div>
                </div>

                <div className="mt-auto flex flex-wrap items-center gap-1.5 border-t border-sep pt-3">
                  <span className="rounded-md bg-ov-6 px-2 py-0.5 font-mono text-[11px] text-t3">
                    DNS: {profile.dns}
                  </span>
                  {profile.autoExcludeLan && (
                    <span className="rounded-md bg-ov-6 px-2 py-0.5 text-[11px] text-t3">
                      {t("profileList.lanExcl")}
                    </span>
                  )}
                  <span className={`ml-auto text-[11px] transition-colors ${
                    isActive
                      ? "font-medium text-success"
                      : "text-t5 group-hover:text-accent"
                  }`}>
                    {isActive ? t("profileList.connected") : t("profileList.connect")}
                  </span>
                </div>
              </div>
              );
            })}
          </div>
        )}
      </div>

      {/* QR code export dialog */}
      {qrProfile && (
        <QrCodeExportDialog profile={qrProfile} onClose={() => setQrProfile(null)} />
      )}

      <ImportProfileDialog
        open={importDialogOpen}
        onClose={() => setImportDialogOpen(false)}
        onImport={onImport}
        onImportFile={onImportFile}
      />
    </div>
  );
}
