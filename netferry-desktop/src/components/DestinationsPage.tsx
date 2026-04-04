import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Shield, Zap, Ban, Gauge } from "lucide-react";
import type { RouteMode } from "@/types";
import { useRuleStore } from "@/stores/ruleStore";

const PRIORITY_META: Record<number, { label: string; color: string; ring: string; bg: string; dotColor: string }> = {
  1: { label: "Low",  color: "text-white/40",   ring: "ring-white/10",     bg: "bg-white/[0.06]",    dotColor: "bg-white/30" },
  2: { label: "Low+", color: "text-white/50",   ring: "ring-white/15",     bg: "bg-white/[0.08]",    dotColor: "bg-white/40" },
  3: { label: "Norm", color: "text-[#0a84ff]",  ring: "ring-[#0a84ff]/30", bg: "bg-[#0a84ff]/10",    dotColor: "bg-[#0a84ff]" },
  4: { label: "High", color: "text-[#ff9f0a]",  ring: "ring-[#ff9f0a]/30", bg: "bg-[#ff9f0a]/10",    dotColor: "bg-[#ff9f0a]" },
  5: { label: "Crit", color: "text-[#ff453a]",  ring: "ring-[#ff453a]/30", bg: "bg-[#ff453a]/10",    dotColor: "bg-[#ff453a]" },
};

const ROUTE_META: Record<RouteMode, { label: string; color: string; ring: string; bg: string; Icon: typeof Shield }> = {
  tunnel:  { label: "Tunnel",  color: "text-[#0a84ff]", ring: "ring-[#0a84ff]/30", bg: "bg-[#0a84ff]/10", Icon: Shield },
  direct:  { label: "Direct",  color: "text-[#30d158]", ring: "ring-[#30d158]/30", bg: "bg-[#30d158]/10", Icon: Zap },
  blocked: { label: "Blocked", color: "text-[#ff453a]", ring: "ring-[#ff453a]/30", bg: "bg-[#ff453a]/10", Icon: Ban },
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
        <div className="absolute right-0 top-full z-50 mt-1 w-32 rounded-lg border border-white/[0.08] bg-[#2c2c2e] p-1 shadow-xl">
          {(["tunnel", "direct", "blocked"] as RouteMode[]).map((mode) => {
            const m = ROUTE_META[mode];
            const active = mode === route;
            return (
              <button
                key={mode}
                onClick={(e) => { e.stopPropagation(); onChange(mode); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-white/60 hover:bg-white/[0.06]"
                }`}
              >
                <m.Icon size={13} className={active ? m.color : "text-white/40"} />
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
        <div className="absolute right-0 top-full z-50 mt-1 w-32 rounded-lg border border-white/[0.08] bg-[#2c2c2e] p-1 shadow-xl">
          {[1, 2, 3, 4, 5].map((p) => {
            const m = PRIORITY_META[p];
            const active = p === priority;
            return (
              <button
                key={p}
                onClick={(e) => { e.stopPropagation(); onChange(p); setOpen(false); }}
                className={`flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-[12px] transition-colors ${
                  active ? `${m.bg} ${m.color}` : "text-white/60 hover:bg-white/[0.06]"
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
    <div className="flex h-full flex-col bg-[#1c1c1e]">
      {/* Header */}
      <div className="border-b border-white/[0.06] px-6 py-4">
        <h1 className="text-lg font-semibold text-white/90">{t("destinationsPage.title")}</h1>
        <p className="mt-1 text-xs text-white/30">{t("destinationsPage.subtitle")}</p>
      </div>

      {/* Content */}
      <div className="min-h-0 flex-1 overflow-y-auto bg-[#141416] p-4 font-mono text-xs">
        {sorted.length === 0 ? (
          <p className="text-white/25">{t("destinationsPage.noRules")}</p>
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
                    ? "border-[#ff453a]/15 bg-[#ff453a]/[0.04] opacity-60"
                    : isDirect
                      ? "border-[#30d158]/15 bg-[#30d158]/[0.04]"
                      : "border-white/[0.04] bg-white/[0.02]"
                }`}
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2 min-w-0">
                    {isBlocked ? (
                      <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#ff453a]" />
                    ) : isDirect ? (
                      <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-[#30d158]" />
                    ) : null}
                    <span className={`truncate text-sm font-medium ${
                      isBlocked ? "text-white/30 line-through" : "text-white/40"
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
