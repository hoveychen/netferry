import { create } from "zustand";
import { connectProfile, disconnectProfile, getConnectionStatus } from "@/api";
import type { ConnectionStatus, Profile } from "@/types";

interface ConnectionStore {
  status: ConnectionStatus;
  logs: string[];
  syncStatus: () => Promise<void>;
  connect: (profile: Profile) => Promise<void>;
  disconnect: () => Promise<void>;
  pushLog: (line: string) => void;
  setStatus: (status: ConnectionStatus) => void;
}

export const useConnectionStore = create<ConnectionStore>((set) => ({
  status: { state: "disconnected" },
  logs: [],
  syncStatus: async () => {
    const status = await getConnectionStatus();
    set({ status });
  },
  connect: async (profile) => {
    set({ status: { state: "connecting", profileId: profile.id }, logs: [] });
    try {
      const status = await connectProfile(profile);
      set({ status });
    } catch (e) {
      set({
        status: {
          state: "error",
          message: typeof e === "string" ? e : (e as Error)?.message ?? "Unknown error",
        },
      });
    }
  },
  disconnect: async () => {
    const status = await disconnectProfile();
    set({ status });
  },
  pushLog: (line) =>
    set((s) => ({
      logs: [...s.logs.slice(-499), line],
    })),
  setStatus: (status) => set({ status }),
}));
