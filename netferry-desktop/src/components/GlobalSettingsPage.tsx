import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { GlobalSettings, Profile } from "@/types";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";

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
    <div className="flex h-full flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center gap-3 border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <h1 className="text-[15px] font-semibold text-white/90">{t("nav.settings")}</h1>
        <div className="ml-auto">
          <Button size="sm" onClick={save} disabled={saving}>
            {saving ? t("nav.saving") : t("nav.save")}
          </Button>
        </div>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl space-y-4">
          <div className="rounded-2xl border border-white/[0.07] bg-white/[0.03] p-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-white/30">
              {t("settings.startup")}
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-white/60">
                {t("settings.autoConnect")}
              </label>
              <p className="mb-2.5 text-xs text-white/35">
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

          <div className="rounded-2xl border border-white/[0.07] bg-white/[0.03] p-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-white/30">
              {t("settings.language")}
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-white/60">
                {t("settings.language")}
              </label>
              <p className="mb-2.5 text-xs text-white/35">
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
