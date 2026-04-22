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
 * Routes are persisted inside the active ProfileGroup's `rules` map as
 * `RouteModeV2` tagged unions and pushed to the sidecar in the same shape.
 * Priorities remain in the legacy priorities.json for now (separate scope).
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

/** Push all route modes to the Go sidecar via its HTTP API as V2 tagged unions. */
async function syncRoutesToSidecar(rules: Record<string, RouteModeV2>) {
  if (!currentStatsUrl) return;
  try {
    await fetch(`${currentStatsUrl}/routes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rules),
    });
  } catch {
    // Sidecar may not be ready yet; ignore.
  }
}

/**
 * Push the active group payload to the Go sidecar's `/group` endpoint. Matches
 * `stats.ActiveGroup` on the relay side. Pass `null` to clear (legacy mode).
 */
async function syncActiveGroupToSidecar(group: ProfileGroup | null) {
  if (!currentStatsUrl) return;
  try {
    const body = group
      ? JSON.stringify({
          id: group.id,
          name: group.name,
          defaultProfileId: group.children[0]?.id ?? "",
          profileIds: group.children.map((c) => c.id),
        })
      : "null";
    await fetch(`${currentStatsUrl}/group`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
    });
  } catch {
    // Sidecar may not be ready yet; ignore.
  }
}

/** Called by connectionStore when SSE connects to set the sidecar URL and push current rules. */
export function onSidecarConnected(url: string) {
  currentStatsUrl = url;
  const { priorities, routes, activeGroup } = useRuleStore.getState();
  syncPrioritiesToSidecar(priorities);
  syncRoutesToSidecar(routes);
  syncActiveGroupToSidecar(activeGroup);
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
    // Re-push to the sidecar so mid-session reloads (e.g. active-group
    // switch) propagate without reconnecting. No-op when not connected.
    syncPrioritiesToSidecar(priorities);
    syncRoutesToSidecar(routes);
    syncActiveGroupToSidecar(activeGroup);
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
