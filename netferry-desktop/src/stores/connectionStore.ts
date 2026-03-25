import { create } from "zustand";
import { connectProfile, disconnectProfile, getConnectionStatus, getStatsUrl, updateTraySpeed } from "@/api";
import type {
  ConnectionEvent,
  ConnectionStatus,
  DeployProgress,
  Profile,
  TunnelError,
  TunnelStats,
} from "@/types";

/** An active connection tracked by id. */
export interface ActiveConnection {
  id: number;
  srcAddr: string;
  dstAddr: string;
  host?: string;
  openedAt: number;
}

interface ConnectionStore {
  status: ConnectionStatus;
  logs: string[];
  tunnelStats: TunnelStats | null;
  /** Currently active connections keyed by id. */
  activeConnections: Map<number, ActiveConnection>;
  /** Recently closed connection ids (kept briefly for UI fade-out). */
  recentClosed: ConnectionEvent[];
  tunnelErrors: TunnelError[];
  deployProgress: DeployProgress | null;
  deployReason: string | null;
  syncStatus: () => Promise<void>;
  connect: (profile: Profile) => Promise<void>;
  disconnect: () => Promise<void>;
  pushLog: (line: string) => void;
  setStatus: (status: ConnectionStatus) => void;
  setTunnelStats: (stats: TunnelStats) => void;
  handleConnectionEvent: (event: ConnectionEvent) => void;
  pushTunnelError: (error: TunnelError) => void;
  setDeployProgress: (progress: DeployProgress | null) => void;
  setDeployReason: (reason: string | null) => void;
  startSSE: (url: string) => void;
  stopSSE: () => void;
}

let sseSource: EventSource | null = null;

function stopSSEInternal() {
  if (sseSource) {
    sseSource.close();
    sseSource = null;
  }
}

export const useConnectionStore = create<ConnectionStore>((set, get) => ({
  status: { state: "disconnected" },
  logs: [],
  tunnelStats: null,
  activeConnections: new Map(),
  recentClosed: [],
  tunnelErrors: [],
  deployProgress: null,
  deployReason: null,

  syncStatus: async () => {
    const status = await getConnectionStatus();
    set({ status });
    // If already connected, try to recover the SSE URL.
    if (status.state === "connected" || status.state === "connecting" || status.state === "reconnecting") {
      const url = await getStatsUrl();
      if (url) get().startSSE(url);
    }
  },

  connect: async (profile) => {
    set({
      status: { state: "connecting", profileId: profile.id },
      logs: [],
      tunnelStats: null,
      activeConnections: new Map(),
      recentClosed: [],
      tunnelErrors: [],
      deployProgress: null,
      deployReason: null,
    });
    stopSSEInternal();
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
    stopSSEInternal();
    const status = await disconnectProfile();
    set({ status, tunnelStats: null, activeConnections: new Map(), recentClosed: [], deployProgress: null, deployReason: null });
  },

  pushLog: (line) =>
    set((s) => ({ logs: [...s.logs.slice(-499), line] })),

  setStatus: (status) => set({ status }),

  setTunnelStats: (stats) => set({ tunnelStats: stats }),

  handleConnectionEvent: (event) =>
    set((s) => {
      const active = new Map(s.activeConnections);
      if (event.action === "open") {
        active.set(event.id, {
          id: event.id,
          srcAddr: event.srcAddr,
          dstAddr: event.dstAddr,
          host: event.host,
          openedAt: event.timestampMs,
        });
      } else {
        active.delete(event.id);
      }
      const recentClosed =
        event.action === "close"
          ? [...s.recentClosed.slice(-99), event]
          : s.recentClosed;
      return { activeConnections: active, recentClosed };
    }),

  pushTunnelError: (error) =>
    set((s) => ({
      tunnelErrors: [...s.tunnelErrors.slice(-49), error],
    })),

  setDeployProgress: (progress) => set({ deployProgress: progress }),

  setDeployReason: (reason) => set({ deployReason: reason }),

  startSSE: (url: string) => {
    stopSSEInternal();
    const es = new EventSource(`${url}/events`);
    sseSource = es;

    es.addEventListener("stats", (e) => {
      try {
        const stats: TunnelStats = JSON.parse((e as MessageEvent).data);
        get().setTunnelStats(stats);
        updateTraySpeed(stats.rxBytesPerSec, stats.txBytesPerSec).catch(() => {});
      } catch {}
    });

    es.addEventListener("connection", (e) => {
      try {
        const event: ConnectionEvent = JSON.parse((e as MessageEvent).data);
        get().handleConnectionEvent(event);
      } catch {}
    });

    es.onerror = () => {
      // SSE will auto-reconnect; nothing to do here.
    };
  },

  stopSSE: () => {
    stopSSEInternal();
  },
}));
