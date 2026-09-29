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
  FinalRoute,
  ProfileGroup,
  RouteMode,
  RouteRule,
  RuleGroup,
} from "@/types";
import { routesPayload } from "@/lib/ruleGroups";

const TUNNEL: FinalRoute = { kind: "tunnel" };

/**
 * Routing rules live in the active ProfileGroup: per-host overrides (`rules`),
 * ordered rule groups and the final route. They are pushed to the sidecar
 * as-is; the tunnel does the matching. Priorities remain in the legacy
 * priorities.json for now (separate scope).
 */
interface RuleStore {
  priorities: DestinationPriorities;
  /** Per-host overrides. Mirrors the active group's `rules`. */
  routes: Record<string, RouteRule>;
  /** In-memory copy of active group (needed so the setters can persist). */
  activeGroup: ProfileGroup | null;
  /** Load persisted rules (priorities + active group's rules) from Tauri. */
  loadRules: () => Promise<void>;
  /** Set priority for a single host. Persists and syncs to sidecar. */
  setPriority: (host: string, priority: number) => void;
  /** Set a per-host override. Persists to the active group. */
  setRoute: (host: string, route: RouteMode) => void;
  /** Remove a per-host override. Persists to the active group. */
  deleteRule: (host: string) => void;
  saveRuleGroup: (group: RuleGroup) => void;
  deleteRuleGroup: (id: string) => void;
  /** Move a rule group up (-1) or down (+1); order decides which group wins. */
  moveRuleGroup: (id: string, delta: -1 | 1) => void;
  setFinalRoute: (route: FinalRoute) => void;
  /**
   * Merge observed hosts into the active group's `knownHosts`. Called from
   * connectionStore whenever the relay emits a destinations snapshot, so
   * DestinationsPage can surface cross-session history. Skips the disk write
   * when there's nothing new.
   */
  recordObservedHosts: (hosts: string[]) => void;
}

let currentStatsUrl: string | null = null;

/**
 * Upper bound on the observed-host history persisted in a group's `knownHosts`.
 * A long browsing session touches thousands of unique hosts; left unbounded this
 * list grows forever (and is rewritten to disk on every new host), and it is the
 * dominant contributor to the destinations/rules page row count. We keep only
 * the most-recently-observed hosts. Hosts with a configured route/priority are
 * surfaced independently from the `routes`/`priorities` maps, so trimming here
 * never hides a configured rule.
 */
const MAX_KNOWN_HOSTS = 1000;

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

/** Push the active group's routing rules to the Go sidecar. */
async function syncRoutesToSidecar(routes: Record<string, RouteRule>, group: ProfileGroup | null) {
  if (!currentStatsUrl) return;
  try {
    await fetch(`${currentStatsUrl}/routes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(routesPayload(routes, group?.ruleGroups ?? [], group?.finalRoute ?? TUNNEL)),
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
  syncRoutesToSidecar(routes, activeGroup);
}

/** Called by connectionStore when SSE disconnects. */
export function onSidecarDisconnected() {
  currentStatsUrl = null;
}

export const useRuleStore = create<RuleStore>((set, get) => {
  /** Persist a new version of the active group and push its rules. */
  const commitGroup = (nextGroup: ProfileGroup) => {
    set({ activeGroup: nextGroup, routes: nextGroup.rules });
    saveGroup(nextGroup).catch((err) => console.error("Failed to persist group rules:", err));
    syncRoutesToSidecar(nextGroup.rules, nextGroup);
  };

  const commitRoutes = (nextRoutes: Record<string, RouteRule>) => {
    const group = get().activeGroup;
    if (group) {
      commitGroup({ ...group, rules: nextRoutes });
    } else {
      set({ routes: nextRoutes });
      syncRoutesToSidecar(nextRoutes, null);
    }
  };

  return {
    priorities: {},
    routes: {},
    activeGroup: null,

    loadRules: async () => {
      const [priorities, settings] = await Promise.all([
        getPriorities(),
        getGlobalSettings(),
      ]);
      let activeGroup: ProfileGroup | null = null;
      let routes: Record<string, RouteRule> = {};
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
      syncRoutesToSidecar(routes, activeGroup);
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
      commitRoutes({ ...get().routes, [host]: { kind: route } });
    },

    deleteRule: (host) => {
      const nextRoutes = { ...get().routes };
      delete nextRoutes[host];
      commitRoutes(nextRoutes);
    },

    saveRuleGroup: (ruleGroup) => {
      const group = get().activeGroup;
      if (!group) return;
      const ruleGroups = [...group.ruleGroups];
      const index = ruleGroups.findIndex((item) => item.id === ruleGroup.id);
      if (index >= 0) ruleGroups[index] = ruleGroup;
      else ruleGroups.push(ruleGroup);
      commitGroup({ ...group, ruleGroups });
    },

    deleteRuleGroup: (id) => {
      const group = get().activeGroup;
      if (!group) return;
      commitGroup({ ...group, ruleGroups: group.ruleGroups.filter((item) => item.id !== id) });
    },

    moveRuleGroup: (id, delta) => {
      const group = get().activeGroup;
      if (!group) return;
      const from = group.ruleGroups.findIndex((item) => item.id === id);
      const to = from + delta;
      if (from < 0 || to < 0 || to >= group.ruleGroups.length) return;
      const ruleGroups = [...group.ruleGroups];
      [ruleGroups[from], ruleGroups[to]] = [ruleGroups[to], ruleGroups[from]];
      commitGroup({ ...group, ruleGroups });
    },

    setFinalRoute: (finalRoute) => {
      const group = get().activeGroup;
      if (!group) return;
      commitGroup({ ...group, finalRoute });
    },

    recordObservedHosts: (hosts) => {
      const group = get().activeGroup;
      if (!group) return;
      const existing = new Set(group.knownHosts ?? []);
      const additions: string[] = [];
      for (const h of hosts) {
        if (!h) continue;
        if (!existing.has(h)) {
          existing.add(h);
          additions.push(h);
        }
      }
      if (additions.length === 0) return;
      let nextKnown = [...(group.knownHosts ?? []), ...additions];
      // Cap the history to the most-recent MAX_KNOWN_HOSTS (additions are appended,
      // so the tail is the newest). Trimming the front drops only stale observed
      // hosts, never configured rules.
      if (nextKnown.length > MAX_KNOWN_HOSTS) {
        nextKnown = nextKnown.slice(nextKnown.length - MAX_KNOWN_HOSTS);
      }
      const nextGroup: ProfileGroup = {
        ...group,
        knownHosts: nextKnown,
      };
      set({ activeGroup: nextGroup });
      saveGroup(nextGroup).catch((err) => {
        console.error("Failed to persist group knownHosts:", err);
      });
    },
  };
});
