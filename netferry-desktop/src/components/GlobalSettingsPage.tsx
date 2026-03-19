import { useState } from "react";
import { ArrowLeft } from "lucide-react";
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
  const [draft, setDraft] = useState<GlobalSettings>(settings);
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await onSave(draft);
      onBack();
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-screen flex-col bg-[#1c1c1e]">
      {/* Toolbar */}
      <div className="flex items-center gap-3 border-b border-white/[0.06] bg-[#1c1c1e]/90 px-6 py-3 backdrop-blur-xl">
        <button
          type="button"
          className="flex items-center gap-1.5 text-sm text-white/45 transition-colors hover:text-white/80"
          onClick={onBack}
        >
          <ArrowLeft className="h-4 w-4" />
          Back
        </button>
        <span className="text-white/20">/</span>
        <h1 className="text-[15px] font-semibold text-white/90">Settings</h1>
        <div className="ml-auto">
          <Button size="sm" onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </Button>
        </div>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl">
          <div className="rounded-2xl border border-white/[0.07] bg-white/[0.03] p-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]">
            <p className="mb-5 text-[11px] font-semibold uppercase tracking-widest text-white/30">
              Startup
            </p>
            <div>
              <label className="mb-1.5 block text-sm font-medium text-white/60">
                Auto-connect on startup
              </label>
              <p className="mb-2.5 text-xs text-white/35">
                Automatically connect to the selected profile when the app launches.
              </p>
              <Select
                value={draft.autoConnectProfileId ?? ""}
                onChange={(e) =>
                  setDraft({ ...draft, autoConnectProfileId: e.target.value || null })
                }
              >
                <option value="">— None —</option>
                {profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
