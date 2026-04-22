import { create } from "zustand";
import { connectProfile, disconnectProfile, getConnectionStatus, getStatsUrl, updateTrayInfo } from "@/api";
import { useSettingsStore } from "@/stores/settingsStore";
import { onSidecarConnected, onSidecarDisconnected } from "@/stores/ruleStore";
import type {
  ConnectionEvent,
  ConnectionStatus,
  DeployProgress,
  DestinationSnapshot,
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
  tunnelIndex?: number; // 1-based pool member; 0 or absent = single tunnel
  /**
   * Profile that dispatched this connection in multi-profile mode; undefined
   * in legacy single-profile mode or when the relay has not yet started
   * emitting the field on SSE events (see ConnectionEvent.activeProfileId).
   */
  activeProfileId?: string;
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
  /** Per-destination aggregate stats. */
  destinations: DestinationSnapshot[];
  deployProgress: DeployProgress | null;
  deployReason: string | null;
  syncStatus: () => Promise<void>;
  connect: (profile: Profile) => Promise<void>;
  disconnect: () => Promise<void>;
  pushLog: (line: string) => void;
  setStatus: (status: ConnectionStatus) => void;
  setTunnelStats: (stats: TunnelStats) => void;
  handleConnectionEvent: (event: ConnectionEvent) => void;
  handleConnectionEventBatch: (events: ConnectionEvent[]) => void;
  handleConnectionsSnapshot: (events: ConnectionEvent[]) => void;
  handleDestinationsSnapshot: (dests: DestinationSnapshot[]) => void;
  pushTunnelError: (error: TunnelError) => void;
  setDeployProgress: (progress: DeployProgress | null) => void;
  setDeployReason: (reason: string | null) => void;
  startSSE: (url: string) => void;
  stopSSE: () => void;
}

let sseSource: EventSource | null = null;

// Buffer connection events and flush at most once per 500ms to avoid
// per-event React re-renders that can pin the CPU at 100%.
let connEventBuffer: ConnectionEvent[] = [];
let connFlushTimer: ReturnType<typeof setTimeout> | null = null;

function flushConnEvents(store: ReturnType<typeof useConnectionStore.getState>) {
  connFlushTimer = null;
  if (connEventBuffer.length === 0) return;
  const batch = connEventBuffer;
  connEventBuffer = [];
  store.handleConnectionEventBatch(batch);
}

function stopSSEInternal() {
  if (sseSource) {
    sseSource.close();
    sseSource = null;
  }
  onSidecarDisconnected();
  if (connFlushTimer) {
    clearTimeout(connFlushTimer);
    connFlushTimer = null;
  }
  connEventBuffer = [];
}

export const useConnectionStore = create<ConnectionStore>((set, get) => ({
  status: { state: "disconnected" },
  logs: [],
  tunnelStats: null,
  activeConnections: new Map(),
  recentClosed: [],
  tunnelErrors: [],
  destinations: [],
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
      destinations: [],
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
    set({ status, tunnelStats: null, activeConnections: new Map(), recentClosed: [], destinations: [], deployProgress: null, deployReason: null });
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
          tunnelIndex: event.tunnelIndex,
          activeProfileId: event.activeProfileId,
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

  handleConnectionEventBatch: (events: ConnectionEvent[]) =>
    set((s) => {
      const active = new Map(s.activeConnections);
      let closed = s.recentClosed;
      for (const event of events) {
        if (event.action === "open") {
          active.set(event.id, {
            id: event.id,
            srcAddr: event.srcAddr,
            dstAddr: event.dstAddr,
            host: event.host,
            tunnelIndex: event.tunnelIndex,
            activeProfileId: event.activeProfileId,
            openedAt: event.timestampMs,
          });
        } else {
          active.delete(event.id);
          closed = [...closed.slice(-99), event];
        }
      }
      return { activeConnections: active, recentClosed: closed };
    }),

  handleConnectionsSnapshot: (events: ConnectionEvent[]) =>
    set(() => {
      const active = new Map<number, ActiveConnection>();
      for (const ev of events) {
        active.set(ev.id, {
          id: ev.id,
          srcAddr: ev.srcAddr,
          dstAddr: ev.dstAddr,
          host: ev.host,
          tunnelIndex: ev.tunnelIndex,
          activeProfileId: ev.activeProfileId,
          openedAt: ev.timestampMs,
        });
      }
      return { activeConnections: active };
    }),

  handleDestinationsSnapshot: (dests: DestinationSnapshot[]) =>
    set({ destinations: dests }),

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

    // Notify ruleStore so it pushes current rules to the sidecar.
    onSidecarConnected(url);

    es.addEventListener("stats", (e) => {
      try {
        const stats: TunnelStats = JSON.parse((e as MessageEvent).data);
        get().setTunnelStats(stats);
        const displayMode = useSettingsStore.getState().settings.trayDisplayMode ?? "speed";
        updateTrayInfo(displayMode, stats.rxBytesPerSec, stats.txBytesPerSec, stats.activeConns).catch(() => {});
      } catch {}
    });

    es.addEventListener("connection", (e) => {
      try {
        const event: ConnectionEvent = JSON.parse((e as MessageEvent).data);
        connEventBuffer.push(event);
        if (!connFlushTimer) {
          connFlushTimer = setTimeout(() => flushConnEvents(get()), 500);
        }
      } catch {}
    });

    es.addEventListener("connections_snapshot", (e) => {
      try {
        const events: ConnectionEvent[] = JSON.parse((e as MessageEvent).data);
        get().handleConnectionsSnapshot(events);
      } catch {}
    });

    es.addEventListener("destinations_snapshot", (e) => {
      try {
        const dests: DestinationSnapshot[] = JSON.parse((e as MessageEvent).data);
        get().handleDestinationsSnapshot(dests);
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
