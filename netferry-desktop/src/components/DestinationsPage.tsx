import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Shield, Zap, Ban, Gauge } from "lucide-react";
import type { Profile, RouteModeV2 } from "@/types";
import { useRuleStore } from "@/stores/ruleStore";
import { useGroupStore } from "@/stores/groupStore";
import { useSettingsStore } from "@/stores/settingsStore";

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
  const { priorities, routes, setPriority, setRule } = useRuleStore();
  const { groups, fetch: fetchGroups } = useGroupStore();
  const { settings, loadSettings } = useSettingsStore();

  // Ensure groups + settings are loaded so we can find the active group's children.
  useEffect(() => {
    fetchGroups();
    loadSettings();
  }, [fetchGroups, loadSettings]);

  const activeGroup = useMemo(
    () => groups.find((g) => g.id === settings.activeGroupId) ?? null,
    [groups, settings.activeGroupId],
  );
  const children = activeGroup?.children ?? [];

  // Build a deduplicated list of hosts that have any non-default rule.
  const allHosts = new Set([...Object.keys(priorities), ...Object.keys(routes)]);
  const sorted = [...allHosts].sort((a, b) => a.localeCompare(b));

  const noGroup = !activeGroup;

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex h-[52px] items-center px-6">
        <h1 className="text-[15px] font-semibold text-t1">{t("destinationsPage.title")}</h1>
        <span className="ml-2 text-xs text-t4">{t("destinationsPage.subtitle")}</span>
      </div>

      {/* Content */}
      <div className="min-h-0 flex-1 overflow-y-auto p-4 font-mono text-xs">
        {noGroup ? (
          <p className="text-t4">
            No active profile group. Rules cannot be edited until a group is selected.
          </p>
        ) : sorted.length === 0 ? (
          <p className="text-t4">{t("destinationsPage.noRules")}</p>
        ) : (
          sorted.map((host) => {
            const priority = priorities[host] ?? 3;
            const route: RouteModeV2 = routes[host] ?? { kind: "default" };
            const isBlocked = route.kind === "blocked";
            const isDirect = route.kind === "direct";

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

