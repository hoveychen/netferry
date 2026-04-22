import { create } from "zustand";
import {
  getGlobalSettings,
  getGroup,
  getPriorities,
  savePriorities,
  saveGroup,
} from "@/api";
import type {
  DestinationPriorities,
  ProfileGroup,
  RouteMode,
  RouteModeV2,
} from "@/types";

/**
 * P3: routes are persisted inside the active ProfileGroup's `rules` map as
 * `RouteModeV2` tagged unions. Priorities remain in the legacy priorities.json
 * for now (separate scope).
 *
 * TODO(migration): one-shot migration from legacy `routes.json`
 * (`Record<string, "tunnel"|"direct"|"blocked">`) into the active group's
 * `rules` map on first launch. Left out to keep this change compile-focused.
 */
interface RuleStore {
  priorities: DestinationPriorities;
  /** Destination host → RouteModeV2. Mirrors active group's `rules`. */
  routes: Record<string, RouteModeV2>;
  /** In-memory copy of active group (needed so setRule/deleteRule can persist). */
  activeGroup: ProfileGroup | null;
  /** Load persisted rules (priorities + active group's rules) from Tauri. */
  loadRules: () => Promise<void>;
  /** Set priority for a single host. Persists and syncs to sidecar. */
  setPriority: (host: string, priority: number) => void;
  /** Set a RouteModeV2 rule for a host. Persists to the active group. */
  setRule: (host: string, mode: RouteModeV2) => void;
  /** Remove a rule for a host. Persists to the active group. */
  deleteRule: (host: string) => void;
  /**
   * Back-compat: accept the legacy `RouteMode` string and forward to setRule.
   * ConnectionPage still calls this with legacy strings.
   */
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

/**
 * Convert `RouteModeV2` map to the legacy `Record<string, RouteMode>` shape
 * that the Go sidecar currently understands. "default" collapses to "tunnel"
 * (the group's default child is what "default" means at runtime).
 *
 * TODO: once the sidecar grows native awareness of RouteModeV2, replace this
 * with a direct push of the tagged shape.
 */
function toLegacyRoutes(rules: Record<string, RouteModeV2>): Record<string, RouteMode> {
  const out: Record<string, RouteMode> = {};
  for (const [host, mode] of Object.entries(rules)) {
    switch (mode.kind) {
      case "tunnel":
      case "default":
        // both resolve to "tunnel through some child profile" at runtime
        out[host] = "tunnel";
        break;
      case "direct":
        out[host] = "direct";
        break;
      case "blocked":
        out[host] = "blocked";
        break;
    }
  }
  return out;
}

/** Push all route modes to the Go sidecar via its HTTP API. */
async function syncRoutesToSidecar(rules: Record<string, RouteModeV2>) {
  if (!currentStatsUrl) return;
  try {
    await fetch(`${currentStatsUrl}/routes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(toLegacyRoutes(rules)),
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

/** Translate a legacy `RouteMode` string into a `RouteModeV2` tagged union. */
function legacyToV2(route: RouteMode): RouteModeV2 {
  switch (route) {
    case "tunnel":
      return { kind: "default" };
    case "direct":
      return { kind: "direct" };
    case "blocked":
      return { kind: "blocked" };
  }
}

export const useRuleStore = create<RuleStore>((set, get) => ({
  priorities: {},
  routes: {},
  activeGroup: null,

  loadRules: async () => {
    const [priorities, settings] = await Promise.all([
      getPriorities(),
      getGlobalSettings(),
    ]);
    let activeGroup: ProfileGroup | null = null;
    let routes: Record<string, RouteModeV2> = {};
    const activeId = settings.activeGroupId ?? null;
    if (activeId) {
      try {
        const group = await getGroup(activeId);
        if (group) {
          activeGroup = group;
          routes = { ...group.rules };
        }
      } catch (err) {
        console.error("Failed to load active group:", err);
      }
    }
    set({ priorities, routes, activeGroup });
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

  setRule: (host, mode) => {
    const group = get().activeGroup;
    const nextRoutes: Record<string, RouteModeV2> = { ...get().routes };
    // Treat {kind:"default"} as "no override" and strip it out — the group's
    // default child is the fallback by definition. Anything else stores.
    if (mode.kind === "default") {
      delete nextRoutes[host];
    } else {
      nextRoutes[host] = mode;
    }
    set({ routes: nextRoutes });
    if (group) {
      const nextGroup: ProfileGroup = { ...group, rules: nextRoutes };
      set({ activeGroup: nextGroup });
      saveGroup(nextGroup).catch((err) => {
        console.error("Failed to persist group rules:", err);
      });
    }
    syncRoutesToSidecar(nextRoutes);
  },

  deleteRule: (host) => {
    const group = get().activeGroup;
    const nextRoutes: Record<string, RouteModeV2> = { ...get().routes };
    delete nextRoutes[host];
    set({ routes: nextRoutes });
    if (group) {
      const nextGroup: ProfileGroup = { ...group, rules: nextRoutes };
      set({ activeGroup: nextGroup });
      saveGroup(nextGroup).catch((err) => {
        console.error("Failed to persist group rules:", err);
      });
    }
    syncRoutesToSidecar(nextRoutes);
  },

  setRoute: (host, route) => {
    // Legacy callers (ConnectionPage) pass the old `"tunnel"|"direct"|"blocked"`
    // string. Forward through the V2 path.
    get().setRule(host, legacyToV2(route));
  },
}));
