import { create } from "zustand";
import { getPriorities, getRules, savePriorities, saveRules } from "@/api";
import type {
  DestinationPriorities,
  FinalRoute,
  RouteMode,
  RouteRule,
  RuleGroup,
  RuleSet,
} from "@/types";
import { routesPayload } from "@/lib/ruleGroups";

const EMPTY_RULE_SET: RuleSet = {
  rules: {},
  ruleGroups: [],
  finalRoute: { kind: "tunnel" },
  knownHosts: [],
};

/**
 * Routing rules are one global RuleSet (`rules.json`), independent of profile
 * groups: per-host overrides (`rules`), ordered rule groups and the final
 * route. They are pushed to the sidecar as-is; the tunnel does the matching.
 * Priorities remain in the legacy priorities.json (separate scope).
 */
interface RuleStore {
  priorities: DestinationPriorities;
  /** The global rule set. Empty until `loadRules` resolves. */
  ruleSet: RuleSet;
  /** True once the rule set has been read from disk. Setters are no-ops before
   *  that so an early write can't clobber `rules.json` with the empty default. */
  loaded: boolean;
  /** Load persisted priorities and the global rule set from Tauri. */
  loadRules: () => Promise<void>;
  /** Set priority for a single host. Persists and syncs to sidecar. */
  setPriority: (host: string, priority: number) => void;
  /** Set a per-host override. */
  setRoute: (host: string, route: RouteMode) => void;
  /** Remove a per-host override. */
  deleteRule: (host: string) => void;
  saveRuleGroup: (group: RuleGroup) => void;
  deleteRuleGroup: (id: string) => void;
  /** Move a rule group up (-1) or down (+1); order decides which group wins. */
  moveRuleGroup: (id: string, delta: -1 | 1) => void;
  setFinalRoute: (route: FinalRoute) => void;
  /**
   * Merge observed hosts into `ruleSet.knownHosts`. Called from connectionStore
   * whenever the relay emits a destinations snapshot, so DestinationsPage can
   * surface cross-session history. Skips the disk write when there's nothing new.
   */
  recordObservedHosts: (hosts: string[]) => void;
}

let currentStatsUrl: string | null = null;

/**
 * Upper bound on the observed-host history persisted in `knownHosts`.
 * A long browsing session touches thousands of unique hosts; left unbounded this
 * list grows forever (and is rewritten to disk on every new host), and it is the
 * dominant contributor to the destinations/rules page row count. We keep only
 * the most-recently-observed hosts. Hosts with a configured route/priority are
 * surfaced independently from the `rules`/`priorities` maps, so trimming here
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

/** Push the routing rules to the Go sidecar. */
async function syncRoutesToSidecar(ruleSet: RuleSet) {
  if (!currentStatsUrl) return;
  try {
    await fetch(`${currentStatsUrl}/routes`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(routesPayload(ruleSet.rules, ruleSet.ruleGroups, ruleSet.finalRoute)),
    });
  } catch {
    // Sidecar may not be ready yet; ignore.
  }
}

/** Called by connectionStore when SSE connects to set the sidecar URL and push current rules. */
export function onSidecarConnected(url: string) {
  currentStatsUrl = url;
  const { priorities, ruleSet, loaded } = useRuleStore.getState();
  syncPrioritiesToSidecar(priorities);
  // Before the first load the sidecar already has the rules the desktop pushed
  // at startup; don't overwrite them with the empty default.
  if (loaded) syncRoutesToSidecar(ruleSet);
}

/** Called by connectionStore when SSE disconnects. */
export function onSidecarDisconnected() {
  currentStatsUrl = null;
}

export const useRuleStore = create<RuleStore>((set, get) => {
  /** Persist a new rule set; push it to the sidecar when routing changed. */
  const commit = (next: RuleSet, pushRoutes = true) => {
    set({ ruleSet: next });
    saveRules(next).catch((err) => console.error("Failed to persist rules:", err));
    if (pushRoutes) syncRoutesToSidecar(next);
  };

  /** Apply `update` to the loaded rule set; no-op before the first load. */
  const update = (fn: (current: RuleSet) => RuleSet | null) => {
    if (!get().loaded) return;
    const next = fn(get().ruleSet);
    if (next) commit(next);
  };

  return {
    priorities: {},
    ruleSet: EMPTY_RULE_SET,
    loaded: false,

    loadRules: async () => {
      const [priorities, ruleSet] = await Promise.all([
        getPriorities().catch((err) => {
          console.error("Failed to load priorities:", err);
          return {} as DestinationPriorities;
        }),
        getRules().catch((err) => {
          console.error("Failed to load rules:", err);
          return null;
        }),
      ]);
      set({ priorities });
      syncPrioritiesToSidecar(priorities);
      if (!ruleSet) return;
      const normalized: RuleSet = {
        rules: ruleSet.rules ?? {},
        ruleGroups: ruleSet.ruleGroups ?? [],
        finalRoute: ruleSet.finalRoute ?? EMPTY_RULE_SET.finalRoute,
        knownHosts: ruleSet.knownHosts ?? [],
      };
      set({ ruleSet: normalized, loaded: true });
      // Re-push so a mid-session reload propagates without reconnecting.
      // No-op when not connected.
      syncRoutesToSidecar(normalized);
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

    setRoute: (host, route) =>
      update((rs) => ({ ...rs, rules: { ...rs.rules, [host]: { kind: route } as RouteRule } })),

    deleteRule: (host) =>
      update((rs) => {
        if (!(host in rs.rules)) return null;
        const rules = { ...rs.rules };
        delete rules[host];
        return { ...rs, rules };
      }),

    saveRuleGroup: (ruleGroup) =>
      update((rs) => {
        const ruleGroups = [...rs.ruleGroups];
        const index = ruleGroups.findIndex((item) => item.id === ruleGroup.id);
        if (index >= 0) ruleGroups[index] = ruleGroup;
        else ruleGroups.push(ruleGroup);
        return { ...rs, ruleGroups };
      }),

    deleteRuleGroup: (id) =>
      update((rs) => ({ ...rs, ruleGroups: rs.ruleGroups.filter((item) => item.id !== id) })),

    moveRuleGroup: (id, delta) =>
      update((rs) => {
        const from = rs.ruleGroups.findIndex((item) => item.id === id);
        const to = from + delta;
        if (from < 0 || to < 0 || to >= rs.ruleGroups.length) return null;
        const ruleGroups = [...rs.ruleGroups];
        [ruleGroups[from], ruleGroups[to]] = [ruleGroups[to], ruleGroups[from]];
        return { ...rs, ruleGroups };
      }),

    setFinalRoute: (finalRoute) => update((rs) => ({ ...rs, finalRoute })),

    recordObservedHosts: (hosts) => {
      const { loaded, ruleSet } = get();
      if (!loaded) return;
      const existing = new Set(ruleSet.knownHosts);
      const additions: string[] = [];
      for (const h of hosts) {
        if (!h) continue;
        if (!existing.has(h)) {
          existing.add(h);
          additions.push(h);
        }
      }
      if (additions.length === 0) return;
      let knownHosts = [...ruleSet.knownHosts, ...additions];
      // Cap the history to the most-recent MAX_KNOWN_HOSTS (additions are appended,
      // so the tail is the newest). Trimming the front drops only stale observed
      // hosts, never configured rules.
      if (knownHosts.length > MAX_KNOWN_HOSTS) {
        knownHosts = knownHosts.slice(knownHosts.length - MAX_KNOWN_HOSTS);
      }
      // knownHosts doesn't affect routing; skip the sidecar push.
      commit({ ...ruleSet, knownHosts }, false);
    },
  };
});
