import { create } from "zustand";
import { connectProfile, disconnectProfile, getConnectionStatus } from "@/api";
import type {
  ConnectionEvent,
  ConnectionStatus,
  Profile,
  TunnelError,
  TunnelStats,
} from "@/types";

interface ConnectionStore {
  status: ConnectionStatus;
  logs: string[];
  tunnelStats: TunnelStats | null;
  connectionEvents: ConnectionEvent[];
  tunnelErrors: TunnelError[];
  syncStatus: () => Promise<void>;
  connect: (profile: Profile) => Promise<void>;
  disconnect: () => Promise<void>;
  pushLog: (line: string) => void;
  setStatus: (status: ConnectionStatus) => void;
  setTunnelStats: (stats: TunnelStats) => void;
  pushConnectionEvent: (event: ConnectionEvent) => void;
  pushTunnelError: (error: TunnelError) => void;
}

export const useConnectionStore = create<ConnectionStore>((set) => ({
  status: { state: "disconnected" },
  logs: [],
  tunnelStats: null,
  connectionEvents: [],
  tunnelErrors: [],
  syncStatus: async () => {
    const status = await getConnectionStatus();
    set({ status });
  },
  connect: async (profile) => {
    set({
      status: { state: "connecting", profileId: profile.id },
      logs: [],
      tunnelStats: null,
      connectionEvents: [],
      tunnelErrors: [],
    });
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
    set({ status, tunnelStats: null });
  },
  pushLog: (line) =>
    set((s) => ({
      logs: [...s.logs.slice(-499), line],
    })),
  setStatus: (status) => set({ status }),
  setTunnelStats: (stats) => set({ tunnelStats: stats }),
  pushConnectionEvent: (event) =>
    set((s) => ({
      connectionEvents: [...s.connectionEvents.slice(-199), event],
    })),
  pushTunnelError: (error) =>
    set((s) => ({
      tunnelErrors: [...s.tunnelErrors.slice(-49), error],
    })),
}));
