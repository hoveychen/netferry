import { create } from "zustand";
import { getPriorities, getRoutes, savePriorities, saveRoutes } from "@/api";
import type { DestinationPriorities, DestinationRoutes, RouteMode } from "@/types";

interface RuleStore {
  priorities: DestinationPriorities;
  routes: DestinationRoutes;
  /** Load persisted rules from Tauri storage. */
  loadRules: () => Promise<void>;
  /** Set priority for a single host. Persists and syncs to sidecar. */
  setPriority: (host: string, priority: number) => void;
  /** Set route mode for a single host. Persists and syncs to sidecar. */
  setRoute: (host: string, route: RouteMode) => void;
}

let currentStatsUrl: string | null = null;

/** Push all priorities to the Go sidecar via its HTTP API. */
async function syncPrioritiesToSidecar(priorities: DestinationPriorities) {
  if (!currentStatsUrl) return;
  try {
    await fetch(`${currentStatsUrl}/priorities`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(priorities),
    });
  } catch {
    // Sidecar may not be ready yet; ignore.
  }
}

/** Push all route modes to the Go sidecar via its HTTP API. */
async function syncRoutesToSidecar(routes: DestinationRoutes) {
  if (!currentStatsUrl) return;
  try {
    await fetch(`${currentStatsUrl}/routes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(routes),
    });
  } catch {
    // Sidecar may not be ready yet; ignore.
  }
}

/** Called by connectionStore when SSE connects to set the sidecar URL and push current rules. */
export function onSidecarConnected(url: string) {
  currentStatsUrl = url;
  const { priorities, routes } = useRuleStore.getState();
  syncPrioritiesToSidecar(priorities);
  syncRoutesToSidecar(routes);
}

/** Called by connectionStore when SSE disconnects. */
export function onSidecarDisconnected() {
  currentStatsUrl = null;
}

export const useRuleStore = create<RuleStore>((set, get) => ({
  priorities: {},
  routes: {},

  loadRules: async () => {
    const [priorities, routes] = await Promise.all([getPriorities(), getRoutes()]);
    set({ priorities, routes });
  },

  setPriority: (host, priority) => {
    const next = { ...get().priorities };
    if (priority === 3) {
      delete next[host];
    } else {
      next[host] = priority;
    }
    set({ priorities: next });
    savePriorities(next).catch(() => {});
    syncPrioritiesToSidecar(next);
  },

  setRoute: (host, route) => {
    const next = { ...get().routes };
    if (route === "tunnel") {
      delete next[host];
    } else {
      next[host] = route;
    }
    set({ routes: next });
    saveRoutes(next).catch(() => {});
    syncRoutesToSidecar(next);
  },
}));
