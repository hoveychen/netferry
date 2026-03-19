import { create } from "zustand";
import { getGlobalSettings, saveGlobalSettings } from "@/api";
import type { GlobalSettings } from "@/types";

interface SettingsStore {
  settings: GlobalSettings;
  loading: boolean;
  loadSettings: () => Promise<void>;
  updateSettings: (settings: GlobalSettings) => Promise<void>;
}

export const useSettingsStore = create<SettingsStore>((set) => ({
  settings: { autoConnectProfileId: null },
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
    await saveGlobalSettings(settings);
    set({ settings });
  },
}));
