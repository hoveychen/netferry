import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Monitor, Moon, Sun } from "lucide-react";

import type { GlobalSettings, Profile } from "@/types";
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
      <div className="flex items-center gap-3 px-6 py-3">
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
        </div>
      </div>
    </div>
  );
}
