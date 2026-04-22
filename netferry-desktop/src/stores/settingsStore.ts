import { create } from "zustand";
import { getGlobalSettings, saveGlobalSettings } from "@/api";
import { useRuleStore } from "@/stores/ruleStore";
import type { GlobalSettings } from "@/types";

interface SettingsStore {
  settings: GlobalSettings;
  loading: boolean;
  loadSettings: () => Promise<void>;
  updateSettings: (settings: GlobalSettings) => Promise<void>;
}

export const useSettingsStore = create<SettingsStore>((set, get) => ({
  settings: { autoConnectProfileId: null, trayDisplayMode: "speed" },
  loading: false,
  loadSettings: async () => {
    set({ loading: true });
    try {
      const settings = await getGlobalSettings();
      set({ settings });
    } finally {
      set({ loading: false });
    }
  },
  updateSettings: async (settings) => {
    const prevGroupId = get().settings.activeGroupId ?? null;
    await saveGlobalSettings(settings);
    set({ settings });
    const nextGroupId = settings.activeGroupId ?? null;
    if (prevGroupId !== nextGroupId) {
      // Active group changed — reload rules from the new group and re-push to
      // the sidecar. Fire-and-forget; errors are already logged inside.
      useRuleStore.getState().loadRules();
    }
  },
}));
