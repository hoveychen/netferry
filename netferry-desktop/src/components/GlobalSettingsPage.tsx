import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowDownUp, Monitor, Moon, Sun, Unplug, EyeOff, RotateCw, Download, Trash2, ExternalLink } from "lucide-react";
import { ask } from "@tauri-apps/plugin-dialog";
import {
  getAppVersion,
  getTunnelVersion,
  getHelperStatus,
  registerHelper,
  unregisterHelper,
  openLoginItemsSettings,
  type HelperStatus,
} from "@/api";

import type { GlobalSettings, Profile, TrayDisplayMode } from "@/types";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { getThemeMode, setThemeMode, type ThemeMode } from "@/lib/theme";

interface Props {
  settings: GlobalSettings;
  profiles: Profile[];
  onBack: () => void;
  onSave: (settings: GlobalSettings) => Promise<void>;
}

export function GlobalSettingsPage({ settings, profiles, onBack, onSave }: Props) {
  const { t, i18n } = useTranslation();
  const [draft, setDraft] = useState<GlobalSettings>(settings);
  const [saving, setSaving] = useState(false);
  const [language, setLanguage] = useState(i18n.language);
  const [theme, setTheme] = useState<ThemeMode>(getThemeMode);
  const [appVersion, setAppVersion] = useState("");
  const [tunnelVersion, setTunnelVersion] = useState("");
  const [helperStatus, setHelperStatus] = useState<HelperStatus | null>(null);
  const [helperWorking, setHelperWorking] = useState(false);

  useEffect(() => {
    getAppVersion().then(setAppVersion).catch(() => {});
    getTunnelVersion().then(setTunnelVersion).catch(() => {});
    getHelperStatus().then(setHelperStatus).catch(() => setHelperStatus(null));
  }, []);

  const refreshHelperStatus = async () => {
    try {
      setHelperStatus(await getHelperStatus());
    } catch {
      setHelperStatus(null);
    }
  };

  const handleHelperInstall = async () => {
    setHelperWorking(true);
    try {
      await registerHelper();
    } catch (e) {
      console.error("helper install failed", e);
    } finally {
      await refreshHelperStatus();
      setHelperWorking(false);
    }
  };

  const handleHelperUninstall = async () => {
    const ok = await ask(t("settings.helperUninstallConfirmBody"), {
      title: t("settings.helperUninstallConfirmTitle"),
      kind: "warning",
    });
    if (!ok) return;
    setHelperWorking(true);
    try {
      await unregisterHelper();
    } catch (e) {
      console.error("helper uninstall failed", e);
    } finally {
      await refreshHelperStatus();
      setHelperWorking(false);
    }
  };

  const handleOpenLoginItems = async () => {
    try {
      await openLoginItemsSettings();
    } catch (e) {
      console.error("open login items failed", e);
    }
  };

  const helperVisible =
    helperStatus !== null && helperStatus !== "not_macos" && helperStatus !== "os_too_old";
  const helperStatusLabel: Partial<Record<HelperStatus, string>> = {
    enabled: t("settings.helperStatusEnabled"),
    requires_approval: t("settings.helperStatusRequiresApproval"),
    not_registered: t("settings.helperStatusNotRegistered"),
    not_found: t("settings.helperStatusNotFound"),
  };
  const helperStatusTone =
    helperStatus === "enabled"
      ? "text-success"
      : helperStatus === "requires_approval"
        ? "text-warning"
        : "text-t3";

  const save = async () => {
    setSaving(true);
    try {
      // Persist language choice
      if (language !== i18n.language) {
        i18n.changeLanguage(language);
        localStorage.setItem("netferry_language", language);
      }
      await onSave(draft);
      onBack();
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col">
      {/* Toolbar */}
      <div className="flex h-[52px] items-center gap-3 px-6">
        <h1 className="text-[15px] font-semibold text-t1">{t("nav.settings")}</h1>
        <div className="ml-auto">
          <Button size="sm" onClick={save} disabled={saving}>
            {saving ? t("nav.saving") : t("nav.save")}
          </Button>
        </div>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl space-y-4">
          <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
              {t("settings.startup")}
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-t2">
                {t("settings.autoConnect")}
              </label>
              <p className="mb-2.5 text-xs text-t3">
                {t("settings.autoConnectDesc")}
              </p>
              <Select
                value={draft.autoConnectProfileId ?? ""}
                onChange={(e) =>
                  setDraft({ ...draft, autoConnectProfileId: e.target.value || null })
                }
              >
                <option value="">{t("settings.none")}</option>
                {profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>
          </div>

          <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
              {t("settings.appearance")}
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-t2">
                {t("settings.theme")}
              </label>
              <p className="mb-2.5 text-xs text-t3">
                {t("settings.themeDesc")}
              </p>
              <div className="flex gap-2">
                {([
                  { id: "system" as ThemeMode, icon: Monitor, label: t("settings.themeSystem") },
                  { id: "light" as ThemeMode, icon: Sun, label: t("settings.themeLight") },
                  { id: "dark" as ThemeMode, icon: Moon, label: t("settings.themeDark") },
                ] as const).map(({ id, icon: Icon, label }) => (
                  <button
                    key={id}
                    type="button"
                    className={`flex flex-1 items-center justify-center gap-2 rounded-xl px-3 py-2.5 text-sm font-medium transition-all ${
                      theme === id
                        ? "bg-accent/10 text-accent ring-1 ring-accent/30"
                        : "bg-ov-4 text-t3 hover:bg-ov-8 hover:text-t2"
                    }`}
                    onClick={() => {
                      setTheme(id);
                      setThemeMode(id);
                    }}
                  >
                    <Icon className="h-4 w-4" />
                    {label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
              {t("settings.trayDisplay")}
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-t2">
                {t("settings.trayDisplay")}
              </label>
              <p className="mb-2.5 text-xs text-t3">
                {t("settings.trayDisplayDesc")}
              </p>
              <div className="flex gap-2">
                {([
                  { id: "speed" as TrayDisplayMode, icon: ArrowDownUp, label: t("settings.traySpeed") },
                  { id: "connections" as TrayDisplayMode, icon: Unplug, label: t("settings.trayConnections") },
                  { id: "none" as TrayDisplayMode, icon: EyeOff, label: t("settings.trayNone") },
                ] as const).map(({ id, icon: Icon, label }) => (
                  <button
                    key={id}
                    type="button"
                    className={`flex flex-1 items-center justify-center gap-2 rounded-xl px-3 py-2.5 text-sm font-medium transition-all ${
                      draft.trayDisplayMode === id
                        ? "bg-accent/10 text-accent ring-1 ring-accent/30"
                        : "bg-ov-4 text-t3 hover:bg-ov-8 hover:text-t2"
                    }`}
                    onClick={() => setDraft({ ...draft, trayDisplayMode: id })}
                  >
                    <Icon className="h-4 w-4" />
                    {label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
              {t("settings.language")}
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-t2">
                {t("settings.language")}
              </label>
              <p className="mb-2.5 text-xs text-t3">
                {t("settings.languageDesc")}
              </p>
              <Select
                value={language}
                onChange={(e) => setLanguage(e.target.value)}
              >
                <option value="en">{t("settings.english")}</option>
                <option value="zh">{t("settings.chinese")}</option>
              </Select>
            </div>
          </div>

          {helperVisible && (
            <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
              <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
                {t("settings.privilegedHelper")}
              </p>
              <p className="mb-4 text-xs leading-relaxed text-t3">
                {t("settings.privilegedHelperDesc")}
              </p>
              <div className="mb-4 flex items-center gap-2 text-sm">
                <span className="text-t2">{t("settings.helperStatus")}:</span>
                <span className={`font-medium ${helperStatusTone}`}>
                  {helperStatusLabel[helperStatus!] ?? helperStatus}
                </span>
                <button
                  type="button"
                  className="ml-1 rounded p-1 text-t4 transition-colors hover:bg-ov-8 hover:text-t2 disabled:opacity-40"
                  onClick={refreshHelperStatus}
                  disabled={helperWorking}
                  title={t("settings.helperRefresh")}
                  aria-label={t("settings.helperRefresh")}
                >
                  <RotateCw className={`h-3.5 w-3.5 ${helperWorking ? "animate-spin" : ""}`} />
                </button>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button onClick={handleHelperInstall} disabled={helperWorking}>
                  <Download className="mr-1.5 h-3.5 w-3.5" />
                  {helperWorking ? t("settings.helperWorking") : t("settings.helperInstall")}
                </Button>
                <Button
                  variant="danger"
                  onClick={handleHelperUninstall}
                  disabled={helperWorking || helperStatus === "not_registered"}
                >
                  <Trash2 className="mr-1.5 h-3.5 w-3.5" />
                  {t("settings.helperUninstall")}
                </Button>
                <Button variant="outline" onClick={handleOpenLoginItems}>
                  <ExternalLink className="mr-1.5 h-3.5 w-3.5" />
                  {t("settings.helperOpenLoginItems")}
                </Button>
              </div>
            </div>
          )}

          <div className="rounded-2xl border border-sep bg-ov-3 p-6 shadow-[inset_0_1px_0_var(--inset-highlight)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-t4">
              {t("settings.about")}
            </p>
            <div className="space-y-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="text-t2">{t("settings.appVersion")}</span>
                <span className="text-t3">{appVersion || "—"}</span>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-t2">{t("settings.tunnelVersion")}</span>
                <span className="text-t3">{tunnelVersion || "—"}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
