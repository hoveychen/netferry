import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Shield, Zap, Ban, Gauge, Search, X } from "lucide-react";
import type { DestinationSnapshot, Profile, RouteModeV2 } from "@/types";
import { useConnectionStore } from "@/stores/connectionStore";
import { useRuleStore } from "@/stores/ruleStore";
import { joinGroupProfiles, useGroupStore } from "@/stores/groupStore";
import { useProfileStore } from "@/stores/profileStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { tunnelColor } from "@/lib/tunnelColor";

const PRIORITY_META: Record<number, { label: string; color: string; ring: string; bg: string; dotColor: string }> = {
  1: { label: "Low",  color: "text-t3",   ring: "ring-sep",     bg: "bg-ov-6",    dotColor: "bg-t4" },
  2: { label: "Low+", color: "text-t3",   ring: "ring-bdr",     bg: "bg-ov-8",    dotColor: "bg-t3" },
  3: { label: "Norm", color: "text-accent",  ring: "ring-accent/30", bg: "bg-accent/10",    dotColor: "bg-accent" },
  4: { label: "High", color: "text-warning",  ring: "ring-warning/30", bg: "bg-warning/10",    dotColor: "bg-warning" },
  5: { label: "Crit", color: "text-danger",  ring: "ring-danger/30", bg: "bg-danger/10",    dotColor: "bg-danger" },
};

type RouteKind = RouteModeV2["kind"];

const ROUTE_STYLE: Record<RouteKind, { color: string; ring: string; bg: string; Icon: typeof Shield }> = {
  default: { color: "text-accent",  ring: "ring-accent/30",  bg: "bg-accent/10",  Icon: Shield },
  tunnel:  { color: "text-accent",  ring: "ring-accent/30",  bg: "bg-accent/10",  Icon: Shield },
  direct:  { color: "text-success", ring: "ring-success/30", bg: "bg-success/10", Icon: Zap },
  blocked: { color: "text-danger",  ring: "ring-danger/30",  bg: "bg-danger/10",  Icon: Ban },
};

function routeKey(mode: RouteModeV2): string {
  return mode.kind === "tunnel" ? `tunnel:${mode.profileId}` : mode.kind;
}

function routeLabel(mode: RouteModeV2, defaultChildName: string | undefined, tunnelChildName: string | undefined): string {
  switch (mode.kind) {
    case "default":
      return defaultChildName ? `Default (→ ${defaultChildName})` : "Default";
    case "tunnel":
      return tunnelChildName ? `Tunnel: ${tunnelChildName}` : "Tunnel";
    case "direct":
      return "Direct";
    case "blocked":
      return "Blocked";
  }
}

function sameRoute(a: RouteModeV2, b: RouteModeV2): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === "tunnel" && b.kind === "tunnel") return a.profileId === b.profileId;
  return true;
}

function RouteBadge({
  route,
  children,
  onChange,
}: {
  route: RouteModeV2;
  children: Profile[];
  onChange: (r: RouteModeV2) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  const defaultChild = children[0];
  const tunnelChild =
    route.kind === "tunnel"
      ? children.find((c) => c.id === route.profileId)
      : undefined;

  const style = ROUTE_STYLE[route.kind];
  const label = routeLabel(route, defaultChild?.name, tunnelChild?.name);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  // Build the option list: Default, one Tunnel per child, Direct, Blocked.
  const options: RouteModeV2[] = useMemo(() => {
    const opts: RouteModeV2[] = [{ kind: "default" }];
    for (const child of children) {
      opts.push({ kind: "tunnel", profileId: child.id });
    }
    opts.push({ kind: "direct" });
    opts.push({ kind: "blocked" });
    return opts;
  }, [children]);

  return (
    <div ref={ref} className="relative">
      <button
        onClick={(e) => { e.stopPropagation(); setOpen(!open); }}
        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 transition-all hover:brightness-125 ${style.bg} ${style.color} ${style.ring}`}
      >
        <style.Icon size={11} />
        {label}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 w-56 rounded-lg border border-bdr bg-elevated p-1 shadow-xl">
          {options.map((mode) => {
            const m = ROUTE_STYLE[mode.kind];
            const active = sameRoute(mode, route);
            const tunnelName =
              mode.kind === "tunnel"
                ? children.find((c) => c.id === mode.profileId)?.name
                : undefined;
            const optLabel = routeLabel(mode, defaultChild?.name, tunnelName);
            return (
              <button
                key={routeKey(mode)}
                onClick={(e) => { e.stopPropagation(); onChange(mode); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-t2 hover:bg-ov-6"
                }`}
              >
                <m.Icon size={13} className={active ? m.color : "text-t3"} />
                <span className="truncate">{optLabel}</span>
                {active && <span className="ml-auto text-[10px]">✓</span>}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function PriorityBadge({ priority, onChange }: { priority: number; onChange: (p: number) => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const meta = PRIORITY_META[priority] ?? PRIORITY_META[3];

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  return (
    <div ref={ref} className="relative">
      <button
        onClick={(e) => { e.stopPropagation(); setOpen(!open); }}
        className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium ring-1 transition-all hover:brightness-125 ${meta.bg} ${meta.color} ${meta.ring}`}
      >
        <Gauge size={11} />
        {meta.label}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 w-32 rounded-lg border border-bdr bg-elevated p-1 shadow-xl">
          {[1, 2, 3, 4, 5].map((p) => {
            const m = PRIORITY_META[p];
            const active = p === priority;
            return (
              <button
                key={p}
                onClick={(e) => { e.stopPropagation(); onChange(p); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-t2 hover:bg-ov-6"
                }`}
              >
                <span className={`h-2 w-2 rounded-full ${m.dotColor}`} />
                {m.label}
                {active && <span className="ml-auto text-[10px]">✓</span>}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

/**
 * Standalone Destinations page accessible from the main nav bar.
 * Shows all destinations with non-default routes or priorities.
 * Rules are read from and written to the shared ruleStore, which mirrors
 * the active ProfileGroup's `rules` map.
 */
export function DestinationsPage() {
  const { t } = useTranslation();
  const { priorities, routes, setPriority, setRule, activeGroup } = useRuleStore();
  const { fetch: fetchGroups } = useGroupStore();
  const { profiles, loadProfiles } = useProfileStore();
  const { loadSettings } = useSettingsStore();
  // Live-session observed hosts; empty when disconnected.
  const liveDestinations = useConnectionStore((s) => s.destinations);
  const [filter, setFilter] = useState("");

  // Ensure groups + profiles + settings are loaded so we can join ids → Profile[].
  useEffect(() => {
    fetchGroups();
    loadProfiles();
    loadSettings();
  }, [fetchGroups, loadProfiles, loadSettings]);

  const children = useMemo<Profile[]>(
    () => (activeGroup ? joinGroupProfiles(activeGroup, profiles) : []),
    [activeGroup, profiles],
  );

  const isMultiProfile = children.length > 1;

  // Index live destinations by host for O(1) lookup so per-row render can
  // surface "currently routing via X profile" without scanning the array.
  const liveDestMap = useMemo(() => {
    const m = new Map<string, DestinationSnapshot>();
    for (const d of liveDestinations) m.set(d.host, d);
    return m;
  }, [liveDestinations]);

  // Union of hosts that have any configured rule, hosts observed in the live
  // session, and hosts accumulated across sessions (`activeGroup.knownHosts`).
  // `knownHosts` is what gives this list continuity after disconnect.
  const sorted = useMemo(() => {
    const all = new Set<string>();
    for (const h of Object.keys(priorities)) all.add(h);
    for (const h of Object.keys(routes)) all.add(h);
    for (const d of liveDestinations) if (d.host) all.add(d.host);
    for (const h of activeGroup?.knownHosts ?? []) if (h) all.add(h);
    return [...all].sort((a, b) => a.localeCompare(b));
  }, [priorities, routes, liveDestinations, activeGroup]);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return sorted;
    return sorted.filter((h) => h.toLowerCase().includes(q));
  }, [sorted, filter]);

  const noGroup = !activeGroup;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex h-[52px] items-center px-6">
        <h1 className="text-[15px] font-semibold text-t1">{t("destinationsPage.title")}</h1>
        <span className="ml-2 text-xs text-t4">{t("destinationsPage.subtitle")}</span>
      </div>

      {/* Filter bar */}
      {!noGroup && sorted.length > 0 && (
        <div className="px-4 pt-2 pb-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-t4" />
            <input
              type="text"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder={t("destinationsPage.filterPlaceholder")}
              className="w-full rounded-lg border border-bdr bg-surface py-2 pl-9 pr-9 text-sm text-t1 outline-none placeholder:text-t5 focus:border-accent"
            />
            {filter && (
              <button
                type="button"
                onClick={() => setFilter("")}
                className="absolute right-2 top-1/2 -translate-y-1/2 rounded-md p-1 text-t4 transition-colors hover:bg-ov-6 hover:text-t2"
                title={t("nav.cancel")}
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          <p className="mt-1.5 text-[11px] text-t4">
            {t("destinationsPage.countLabel", { shown: filtered.length, total: sorted.length })}
          </p>
        </div>
      )}

      {/* Content */}
      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 font-mono text-xs">
        {noGroup ? (
          <p className="text-t4">{t("destinationsPage.noGroup")}</p>
        ) : sorted.length === 0 ? (
          <p className="text-t4">{t("destinationsPage.noHosts")}</p>
        ) : filtered.length === 0 ? (
          <p className="text-t4">{t("destinationsPage.noMatches")}</p>
        ) : (
          filtered.map((host) => {
            const priority = priorities[host] ?? 3;
            const route: RouteModeV2 = routes[host] ?? { kind: "default" };
            const isBlocked = route.kind === "blocked";
            const isDirect = route.kind === "direct";

            // Live attribution: only show in multi-profile mode, only for
            // hosts that are currently routing through the tunnel (skip
            // direct/blocked since those bypass the profile dispatcher).
            // The pinned-profile case is already covered by RouteBadge's
            // "Tunnel: X" label, so this badge is purely a "where is traffic
            // *actually* going right now" hint.
            let liveProfileBadge: { name: string; color: ReturnType<typeof tunnelColor> } | null = null;
            if (isMultiProfile && !isBlocked && !isDirect) {
              const live = liveDestMap.get(host);
              const pid = live?.activeProfileId;
              const idx = pid ? children.findIndex((c) => c.id === pid) : -1;
              if (idx >= 0) {
                liveProfileBadge = {
                  name: children[idx].name,
                  color: tunnelColor(idx + 1),
                };
              }
            }

            return (
              <div
                key={host}
                className={`mb-1.5 rounded-xl border px-3 py-2.5 ${
                  isBlocked
                    ? "border-danger/15 bg-danger/[0.04] opacity-60"
                    : isDirect
                      ? "border-success/15 bg-success/[0.04]"
                      : "border-sep bg-ov-2"
                }`}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 min-w-0">
                    {isBlocked ? (
                      <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-danger" />
                    ) : isDirect ? (
                      <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
                    ) : null}
                    <span className={`truncate text-sm font-medium ${
                      isBlocked ? "text-t4 line-through" : "text-t3"
                    }`}>
                      {host}
                    </span>
                    {liveProfileBadge && (
                      <span
                        className={`shrink-0 truncate max-w-[10rem] rounded px-1.5 py-0.5 text-[10px] font-semibold ${liveProfileBadge.color.bg} ${liveProfileBadge.color.text}`}
                        title={t("destinationsPage.liveVia", { name: liveProfileBadge.name })}
                      >
                        {t("destinationsPage.liveVia", { name: liveProfileBadge.name })}
                      </span>
                    )}
                  </div>
                  <div className="flex items-center gap-2 shrink-0 ml-3">
                    <RouteBadge
                      route={route}
                      children={children}
                      onChange={(r) => setRule(host, r)}
                    />
                    <PriorityBadge priority={priority} onChange={(p) => setPriority(host, p)} />
                  </div>
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

