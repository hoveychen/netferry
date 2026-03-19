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
    <div className="flex h-screen flex-col bg-slate-100">
      {/* Header */}
      <div className="flex items-center gap-3 border-b border-slate-200 bg-white px-6 py-4">
        <button
          type="button"
          className="flex items-center gap-1 text-sm text-slate-500 hover:text-slate-800"
          onClick={onBack}
        >
          <ArrowLeft className="h-4 w-4" />
          Back
        </button>
        <span className="text-slate-300">/</span>
        <h1 className="text-base font-semibold text-slate-800">Global Settings</h1>
        <div className="ml-auto">
          <Button size="sm" onClick={save} disabled={saving}>
            {saving ? "Saving..." : "Save"}
          </Button>
        </div>
      </div>

      {/* Form */}
      <div className="flex-1 overflow-y-auto p-6">
        <div className="mx-auto max-w-2xl">
          <div className="rounded-xl border border-slate-200 bg-white p-6">
            <h2 className="mb-4 text-sm font-semibold uppercase tracking-wide text-slate-500">
              Startup
            </h2>
            <div>
              <label className="mb-1 block text-sm font-medium text-slate-700">
                Auto-connect on startup
              </label>
              <p className="mb-2 text-xs text-slate-400">
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
