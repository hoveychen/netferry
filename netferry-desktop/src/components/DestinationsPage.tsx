import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Shield, Zap, Ban, Gauge } from "lucide-react";
import type { RouteMode } from "@/types";
import { useRuleStore } from "@/stores/ruleStore";

const PRIORITY_META: Record<number, { label: string; color: string; ring: string; bg: string; dotColor: string }> = {
  1: { label: "Low",  color: "text-t3",   ring: "ring-sep",     bg: "bg-ov-6",    dotColor: "bg-t4" },
  2: { label: "Low+", color: "text-t3",   ring: "ring-bdr",     bg: "bg-ov-8",    dotColor: "bg-t3" },
  3: { label: "Norm", color: "text-accent",  ring: "ring-accent/30", bg: "bg-accent/10",    dotColor: "bg-accent" },
  4: { label: "High", color: "text-warning",  ring: "ring-warning/30", bg: "bg-warning/10",    dotColor: "bg-warning" },
  5: { label: "Crit", color: "text-danger",  ring: "ring-danger/30", bg: "bg-danger/10",    dotColor: "bg-danger" },
};

const ROUTE_META: Record<RouteMode, { label: string; color: string; ring: string; bg: string; Icon: typeof Shield }> = {
  tunnel:  { label: "Tunnel",  color: "text-accent", ring: "ring-accent/30", bg: "bg-accent/10", Icon: Shield },
  direct:  { label: "Direct",  color: "text-success", ring: "ring-success/30", bg: "bg-success/10", Icon: Zap },
  blocked: { label: "Blocked", color: "text-danger", ring: "ring-danger/30", bg: "bg-danger/10", Icon: Ban },
};

function RouteBadge({ route, onChange }: { route: RouteMode; onChange: (r: RouteMode) => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const meta = ROUTE_META[route];

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
        <meta.Icon size={11} />
        {meta.label}
      </button>
      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 w-32 rounded-lg border border-bdr bg-elevated p-1 shadow-xl">
          {(["tunnel", "direct", "blocked"] as RouteMode[]).map((mode) => {
            const m = ROUTE_META[mode];
            const active = mode === route;
            return (
              <button
                key={mode}
                onClick={(e) => { e.stopPropagation(); onChange(mode); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-t2 hover:bg-ov-6"
                }`}
              >
                <m.Icon size={13} className={active ? m.color : "text-t3"} />
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
 * Rules are read from and written to the shared ruleStore.
 */
export function DestinationsPage() {
  const { t } = useTranslation();
  const { priorities, routes, setPriority, setRoute } = useRuleStore();

  // Build a deduplicated list of hosts that have any non-default rule.
  const allHosts = new Set([...Object.keys(priorities), ...Object.keys(routes)]);
  const sorted = [...allHosts].sort((a, b) => a.localeCompare(b));

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="px-6 py-3">
        <h1 className="text-[15px] font-semibold text-t1">{t("destinationsPage.title")}</h1>
        <p className="mt-1 text-xs text-t4">{t("destinationsPage.subtitle")}</p>
      </div>

      {/* Content */}
      <div className="min-h-0 flex-1 overflow-y-auto p-4 font-mono text-xs">
        {sorted.length === 0 ? (
          <p className="text-t4">{t("destinationsPage.noRules")}</p>
        ) : (
          sorted.map((host) => {
            const priority = priorities[host] ?? 3;
            const route = (routes[host] ?? "tunnel") as RouteMode;
            const isBlocked = route === "blocked";
            const isDirect = route === "direct";

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
                    <RouteBadge route={route} onChange={(r) => setRoute(host, r)} />
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
