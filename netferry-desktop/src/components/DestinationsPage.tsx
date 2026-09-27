import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Shield, Zap, Ban, Gauge, Search, X, Plus, ArrowLeft, Pencil, Trash2 } from "lucide-react";
import { parse } from "tldts";
import type { DestinationSnapshot, Profile, RouteModeV2, RuleGroup } from "@/types";
import { useConnectionStore } from "@/stores/connectionStore";
import { useRuleStore } from "@/stores/ruleStore";
import { joinGroupProfiles, useGroupStore } from "@/stores/groupStore";
import { useProfileStore } from "@/stores/profileStore";
import { useSettingsStore } from "@/stores/settingsStore";
import { tunnelColor } from "@/lib/tunnelColor";
import { compileRoutes, matchesDomain, normalizeDomain } from "@/lib/ruleGroups";
import { catalogRevision, suggestServiceGroups, type ServiceSuggestion } from "@/lib/serviceCatalog";
import { suggestRoutingScopes, type RoutingSuggestion } from "@/lib/routingCatalog";

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

/**
 * Wildcard rule keys that could match `host`, most-specific first. Mirrors the
 * relay's `wildcardCandidates` (netferry-relay/internal/stats/stats.go): a key
 * `*.suffix` matches any host ending in `.suffix` across sub-levels; the apex
 * is not matched. Returns [] for empty/single-label hosts or wildcard patterns.
 */
function wildcardCandidates(host: string): string[] {
  if (!host || host.startsWith("*.")) return [];
  const labels = host.split(".");
  if (labels.length < 2) return [];
  const out: string[] = [];
  for (let i = 1; i < labels.length; i++) out.push("*." + labels.slice(i).join("."));
  return out;
}

/** Effective route for a host: exact rule, else the most specific wildcard. */
function resolveRoute(
  host: string,
  routes: Record<string, RouteModeV2>,
): { mode: RouteModeV2; via: string | null } {
  const exact = routes[host];
  if (exact) return { mode: exact, via: null };
  for (const c of wildcardCandidates(host)) {
    if (routes[c]) return { mode: routes[c], via: c };
  }
  return { mode: { kind: "default" }, via: null };
}

/** Effective priority for a host: exact rule, else the most specific wildcard. */
function resolvePriority(
  host: string,
  priorities: Record<string, number>,
): { value: number; via: string | null } {
  if (priorities[host] != null) return { value: priorities[host], via: null };
  for (const c of wildcardCandidates(host)) {
    if (priorities[c] != null) return { value: priorities[c], via: c };
  }
  return { value: 3, via: null };
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
  const { t, i18n } = useTranslation();
  const { priorities, routes, setPriority, setRule, deleteRule, activeGroup, saveRuleGroup, deleteRuleGroup } = useRuleStore();
  const { fetch: fetchGroups } = useGroupStore();
  const { profiles, loadProfiles } = useProfileStore();
  const { loadSettings } = useSettingsStore();
  // Live-session observed hosts; empty when disconnected.
  const liveDestinations = useConnectionStore((s) => s.destinations);
  const [filter, setFilter] = useState("");
  const [showAllSites, setShowAllSites] = useState(false);
  const [selectedScope, setSelectedScope] = useState<string | null>(null);
  const [editingGroup, setEditingGroup] = useState<RuleGroup | null>(null);
  const [draftName, setDraftName] = useState("");
  const [draftDomains, setDraftDomains] = useState("");
  const [draftRoute, setDraftRoute] = useState("default");
  const ruleGroups = useMemo(() => activeGroup?.ruleGroups ?? [], [activeGroup]);
  const effectiveRoutes = useMemo(() => compileRoutes(ruleGroups, routes), [ruleGroups, routes]);
  const suggestionName = (suggestion: ServiceSuggestion | RoutingSuggestion) => i18n.language.startsWith("zh") ? suggestion.nameZh ?? suggestion.name : suggestion.name;

  const openEditor = (group?: RuleGroup, suggestedDomain?: string) => {
    const suggestedScope = suggestedDomain && !normalizeDomain(suggestedDomain) ? `=${suggestedDomain}` : suggestedDomain;
    setEditingGroup(group ?? { id: crypto.randomUUID(), name: "", domains: [], route: { kind: "default" } });
    setDraftName(group?.name ?? suggestedDomain ?? "");
    setDraftDomains(group?.domains.join("\n") ?? suggestedScope ?? "");
    setDraftRoute(group?.route.kind === "tunnel" ? `tunnel:${group.route.profileId}` : group?.route.kind ?? "default");
  };
  const openSuggestion = (suggestion: ServiceSuggestion) => {
    const group = ruleGroups.find((item) => item.name === suggestion.name || item.name === suggestion.nameZh);
    openEditor(group);
    setDraftName(group?.name ?? suggestionName(suggestion));
    setDraftDomains([...new Set([...(group?.domains ?? []), ...suggestion.domains])].join("\n"));
  };
  const openRoutingSuggestion = (suggestion: RoutingSuggestion) => {
    const group = ruleGroups.find((item) => item.name === suggestion.name || item.name === suggestion.nameZh);
    openEditor(group);
    setDraftName(group?.name ?? suggestionName(suggestion));
    setDraftDomains([...new Set([...(group?.domains ?? []), ...suggestion.domains])].join("\n"));
    if (!group) setDraftRoute(suggestion.suggestedRoute);
  };

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

  const siteGroups = useMemo(() => {
    const groups = new Map<string, string[]>();
    for (const host of sorted) {
      if (ruleGroups.some((group) => group.domains.some((domain) => matchesDomain(host, domain)))) continue;
      const parsed = parse(host, { allowPrivateDomains: true });
      if (parsed.isIp) continue;
      const site = parsed.domain ?? host;
      const entries = groups.get(site) ?? [];
      entries.push(host);
      groups.set(site, entries);
    }
    return [...groups.entries()].sort((a, b) => b[1].length - a[1].length || a[0].localeCompare(b[0]));
  }, [sorted, ruleGroups]);
  const addressHosts = useMemo(() => sorted.filter((host) => parse(host).isIp && !ruleGroups.some((group) => group.domains.some((domain) => matchesDomain(host, domain)))), [sorted, ruleGroups]);
  const serviceSuggestions = useMemo(() => suggestServiceGroups(sorted, ruleGroups), [sorted, ruleGroups]);
  const routingSuggestions = useMemo(() => suggestRoutingScopes(sorted, ruleGroups), [sorted, ruleGroups]);

  const scopeHosts = useMemo(() => {
    if (!selectedScope) return [];
    let entries = sorted;
    if (selectedScope.startsWith("group:")) {
      const group = ruleGroups.find((item) => item.id === selectedScope.slice(6));
      entries = group ? entries.filter((host) => group.domains.some((domain) => matchesDomain(host, domain))) : [];
    } else if (selectedScope.startsWith("site:")) {
      const site = selectedScope.slice(5);
      entries = siteGroups.find(([key]) => key === site)?.[1] ?? [];
    } else if (selectedScope === "ip") {
      entries = addressHosts;
    }
    return entries;
  }, [sorted, selectedScope, ruleGroups, siteGroups, addressHosts]);

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return scopeHosts;
    return scopeHosts.filter((h) => h.toLowerCase().includes(q));
  }, [scopeHosts, filter]);

  const domainInputs = useMemo(() => draftDomains.split(/[\n,]/).map((input) => input.trim()).filter(Boolean), [draftDomains]);
  const invalidDomains = useMemo(() => domainInputs.filter((input) => !normalizeDomain(input)), [domainInputs]);
  const normalizedDraftDomains = useMemo(() => [...new Set(domainInputs.map(normalizeDomain).filter((d): d is string => !!d))], [domainInputs]);
  const previewHosts = useMemo(() => sorted.filter((host) => normalizedDraftDomains.some((domain) => matchesDomain(host, domain))), [sorted, normalizedDraftDomains]);
  const previewGroupOverlap = useMemo(() => previewHosts.filter((host) => ruleGroups.some((group) => group.id !== editingGroup?.id && group.domains.some((domain) => matchesDomain(host, domain)))).length, [previewHosts, ruleGroups, editingGroup]);
  const previewRuleOverrides = useMemo(() => previewHosts.filter((host) => !!routes[host]).length, [previewHosts, routes]);

  const saveEditor = () => {
    if (!editingGroup || !draftName.trim() || normalizedDraftDomains.length === 0 || invalidDomains.length > 0) return;
    const route: RouteModeV2 = draftRoute.startsWith("tunnel:")
      ? { kind: "tunnel", profileId: draftRoute.slice(7) }
      : { kind: draftRoute as "default" | "direct" | "blocked" };
    saveRuleGroup({ ...editingGroup, name: draftName.trim(), domains: normalizedDraftDomains, route });
    setEditingGroup(null);
  };

  const noGroup = !activeGroup;

  // Draft-rule creation: when the user types something that looks like a rule
  // target (a hostname or a `*.x` wildcard) that isn't already in the list,
  // surface a row that lets them assign a route/priority to it directly.
  const query = filter.trim();
  const knownSet = useMemo(() => new Set(sorted), [sorted]);
  const looksLikeTarget = query.startsWith("*.") || query.includes(".");
  const showDraft = !noGroup && selectedScope !== null && query !== "" && looksLikeTarget && !knownSet.has(query);
  // Nudge a bare domain toward its wildcard form (wildcard the typed host's parent).
  const wildcardSuggestion = useMemo(() => {
    if (!query || query.startsWith("*.")) return null;
    const labels = query.split(".");
    if (labels.length < 2) return null;
    const suffix = labels.length >= 3 ? labels.slice(1).join(".") : query;
    const candidate = "*." + suffix;
    return candidate !== query && !knownSet.has(candidate) ? candidate : null;
  }, [query, knownSet]);

  const renderRow = (host: string, draft: boolean) => {
    const { mode: route, via: routeVia } = resolveRoute(host, effectiveRoutes);
    const groupVia = ruleGroups.find((group) => group.domains.some((domain) => matchesDomain(host, domain)));
    const { value: priority } = resolvePriority(host, priorities);
    const isBlocked = route.kind === "blocked";
    const isDirect = route.kind === "direct";

    // Live attribution: only show in multi-profile mode, only for hosts that
    // are currently routing through the tunnel (skip direct/blocked since those
    // bypass the profile dispatcher). The pinned-profile case is already
    // covered by RouteBadge's "Tunnel: X" label, so this badge is purely a
    // "where is traffic *actually* going right now" hint.
    let liveProfileBadge: { name: string; color: ReturnType<typeof tunnelColor> } | null = null;
    if (!draft && isMultiProfile && !isBlocked && !isDirect) {
      const live = liveDestMap.get(host);
      const pid = live?.activeProfileId;
      const idx = pid ? children.findIndex((c) => c.id === pid) : -1;
      if (idx >= 0) {
        liveProfileBadge = { name: children[idx].name, color: tunnelColor(idx + 1) };
      }
    }

    return (
      <div
        key={draft ? `draft:${host}` : host}
        data-destrow
        className={`mb-1.5 rounded-xl border px-3 py-2.5 ${
          draft
            ? "border-accent/40 bg-accent/[0.06]"
            : isBlocked
              ? "border-danger/15 bg-danger/[0.04] opacity-60"
              : isDirect
                ? "border-success/15 bg-success/[0.04]"
                : "border-sep bg-ov-2"
        }`}
      >
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 min-w-0">
            {!draft && isBlocked ? (
              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-danger" />
            ) : !draft && isDirect ? (
              <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-success" />
            ) : null}
            <span className={`truncate text-sm font-medium ${
              !draft && isBlocked ? "text-t4 line-through" : "text-t3"
            }`}>
              {host}
            </span>
            {draft ? (
              <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-semibold bg-accent/15 text-accent">
                {t("destinationsPage.draftBadge")}
              </span>
            ) : routeVia ? (
              <span
                className="shrink-0 truncate max-w-[12rem] rounded px-1.5 py-0.5 text-[10px] font-medium bg-ov-8 text-t4"
                title={t("destinationsPage.inheritedVia", { pattern: routeVia })}
              >
                {t("destinationsPage.inheritedVia", { pattern: routeVia })}
              </span>
            ) : groupVia && !routes[host] ? (
              <span className="shrink-0 truncate max-w-[12rem] rounded px-1.5 py-0.5 text-[10px] font-medium bg-ov-8 text-t4">{groupVia.name}</span>
            ) : null}
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
            {routes[host] && <button type="button" onClick={() => deleteRule(host)} title={t("destinationsPage.resetOverride")} className="rounded p-1 text-t4 hover:bg-ov-8 hover:text-t1"><X className="h-3.5 w-3.5" /></button>}
          </div>
        </div>
      </div>
    );
  };

  // ── Windowed rendering ──────────────────────────────────────────────────
  // The row list is the union of every configured rule, live destination, and
  // cross-session observed host — potentially thousands of entries. Each row
  // mounts two interactive dropdowns (RouteBadge + PriorityBadge) with their own
  // hooks, so rendering the whole list synchronously pins the WebView main
  // thread and freezes the app. We render only the rows in (or near) the
  // viewport, padding the scroll container above and below to preserve the
  // scrollbar geometry. Rows are uniform height, so a single measured row height
  // drives the math.
  const rows = useMemo(() => {
    const arr: { host: string; draft: boolean }[] = [];
    if (showDraft) arr.push({ host: query, draft: true });
    for (const h of filtered) arr.push({ host: h, draft: false });
    return arr;
  }, [showDraft, query, filtered]);

  const scrollRef = useRef<HTMLDivElement>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportH, setViewportH] = useState(0);
  const [rowH, setRowH] = useState(48); // measured on first paint; 48 ≈ estimate

  // Track the scroll viewport height (also handles window/pane resizes).
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const update = () => setViewportH(el.clientHeight);
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, [noGroup]);

  // Measure the real row height once rows are on screen (offsetHeight excludes
  // the mb-1.5 = 6px margin, so add it back). Self-corrects any estimate drift.
  useEffect(() => {
    const el = scrollRef.current?.querySelector<HTMLElement>("[data-destrow]");
    if (!el) return;
    const h = el.offsetHeight + 6;
    if (Math.abs(h - rowH) > 1) setRowH(h);
  }, [rows, rowH]);

  const OVERSCAN = 8;
  const total = rows.length;
  const startIdx = Math.max(0, Math.floor(scrollTop / rowH) - OVERSCAN);
  const visibleCount =
    viewportH > 0 ? Math.ceil(viewportH / rowH) + OVERSCAN * 2 : total;
  const endIdx = Math.min(total, startIdx + visibleCount);
  const topPad = startIdx * rowH;
  const bottomPad = Math.max(0, (total - endIdx) * rowH);

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex h-[52px] items-center gap-2 px-6">
        {selectedScope && <button type="button" onClick={() => { setSelectedScope(null); setFilter(""); }} className="rounded p-1 text-t3 hover:bg-ov-6" aria-label={t("destinationsPage.back")}><ArrowLeft className="h-4 w-4" /></button>}
        <h1 className="text-[15px] font-semibold text-t1">{selectedScope?.startsWith("group:") ? ruleGroups.find((g) => g.id === selectedScope.slice(6))?.name : selectedScope?.startsWith("site:") ? selectedScope.slice(5) : selectedScope === "ip" ? t("destinationsPage.ipAddresses") : t("destinationsPage.title")}</h1>
        {!selectedScope && <span className="ml-2 text-xs text-t4">{t("destinationsPage.subtitle")}</span>}
        {!noGroup && !selectedScope && <button type="button" onClick={() => openEditor()} className="ml-auto flex items-center gap-1 rounded-md bg-accent px-2.5 py-1.5 text-xs font-medium text-white hover:opacity-90"><Plus className="h-3.5 w-3.5" />{t("destinationsPage.newGroup")}</button>}
      </div>

      {/* Filter bar */}
      {!noGroup && (
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
          <div className="mt-1.5 flex items-center gap-2 flex-wrap text-[11px] text-t4">
            {selectedScope && sorted.length > 0 && (
              <span>{t("destinationsPage.countLabel", { shown: filtered.length, total: scopeHosts.length })}</span>
            )}
            {selectedScope && wildcardSuggestion && (
              <button
                type="button"
                onClick={() => setFilter(wildcardSuggestion)}
                className="rounded px-1.5 py-0.5 font-mono text-accent ring-1 ring-accent/30 transition-colors hover:bg-accent/10"
              >
                {t("destinationsPage.wildcardSuggest", { pattern: wildcardSuggestion })}
              </button>
            )}
            {selectedScope && !wildcardSuggestion && sorted.length === 0 && (
              <span>{t("destinationsPage.wildcardHint")}</span>
            )}
          </div>
        </div>
      )}

      {/* Content */}
      <div
        ref={scrollRef}
        onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}
        className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 font-mono text-xs"
      >
        {noGroup ? (
          <p className="text-t4">{t("destinationsPage.noGroup")}</p>
        ) : !selectedScope ? (
          <div className="space-y-5 font-sans">
            <section>
              <div className="mb-2 flex items-center justify-between border-b border-sep pb-2 text-[11px] font-semibold tracking-wide text-t4">
                <span>{t("destinationsPage.namedGroups")} · {ruleGroups.length}</span>
              </div>
              {ruleGroups.length === 0 && <p className="py-5 text-sm text-t4">{t("destinationsPage.noNamedGroups")}</p>}
              <div className="space-y-1">
                {ruleGroups.filter((g) => !filter || g.name.toLowerCase().includes(filter.toLowerCase()) || g.domains.some((d) => d.includes(filter.toLowerCase()))).map((group) => {
                  const matched = sorted.filter((host) => group.domains.some((domain) => matchesDomain(host, domain)));
                  return <div key={group.id} className="group flex items-center gap-3 rounded-lg border border-sep bg-ov-2 px-3 py-2.5">
                    <button type="button" onClick={() => { setSelectedScope(`group:${group.id}`); setFilter(""); setScrollTop(0); }} className="min-w-0 flex-1 text-left">
                      <div className="truncate text-sm font-medium text-t1">{group.name}</div>
                      <div className="mt-0.5 truncate text-[11px] text-t4">{group.domains.join(" · ")}</div>
                    </button>
                    <span className="shrink-0 text-xs text-t3">{routeLabel(group.route, children[0]?.name, children.find((p) => group.route.kind === "tunnel" && p.id === group.route.profileId)?.name)}</span>
                    <span className="w-12 shrink-0 text-right font-mono text-xs text-t4">{matched.length}</span>
                    <button type="button" onClick={() => openEditor(group)} className="rounded p-1 text-t4 hover:bg-ov-8 hover:text-t1" aria-label={t("destinationsPage.editGroup")}><Pencil className="h-3.5 w-3.5" /></button>
                  </div>;
                })}
              </div>
            </section>
            {routingSuggestions.length > 0 && <section>
              <div className="mb-2 flex items-center justify-between border-b border-sep pb-2 text-[11px] font-semibold tracking-wide text-t4"><span>{t("destinationsPage.routingScopes")} · {routingSuggestions.length}</span><span>{t("destinationsPage.routingSourceSummary")}</span></div>
              <p className="mb-2 text-xs text-t4">{t("destinationsPage.routingScopeHint")}</p>
              <div className="space-y-1">
                {routingSuggestions.filter((s) => !filter || suggestionName(s).toLowerCase().includes(filter.toLowerCase()) || s.name.toLowerCase().includes(filter.toLowerCase()) || s.hosts.some((host) => host.includes(filter.toLowerCase()))).map((suggestion) => <div key={suggestion.id} className="flex items-center gap-3 rounded-lg border border-sep bg-ov-2 px-3 py-2.5">
                  <div className="min-w-0 flex-1"><div className="text-sm font-medium text-t1">{suggestionName(suggestion)}</div><div className="mt-0.5 text-[11px] text-t4">{suggestion.source} · {suggestion.suggestedRoute === "direct" ? t("destinationsPage.routeDirect") : t("destinationsPage.routeDefault")}{suggestion.coveredHosts > 0 && ` · ${t("destinationsPage.alreadyGrouped", { count: suggestion.coveredHosts })}`}</div></div>
                  <span className="shrink-0 font-mono text-xs text-t4">{suggestion.hosts.length}</span>
                  <button type="button" onClick={() => openRoutingSuggestion(suggestion)} className="shrink-0 rounded-md border border-bdr px-2 py-1 text-xs text-t2 hover:border-accent hover:text-accent">{t("destinationsPage.reviewSuggestion")}</button>
                </div>)}
              </div>
            </section>}
            {serviceSuggestions.length > 0 && <section>
              <div className="mb-2 flex items-center justify-between border-b border-sep pb-2 text-[11px] font-semibold tracking-wide text-t4"><span>{t("destinationsPage.suggestedServices")} · {serviceSuggestions.length}</span><span title="https://github.com/v2fly/domain-list-community">V2Fly · {catalogRevision.slice(0, 7)}</span></div>
              <p className="mb-2 text-xs text-t4">{t("destinationsPage.suggestionHint")}</p>
              <div className="space-y-1">
                {serviceSuggestions.filter((s) => !filter || suggestionName(s).toLowerCase().includes(filter.toLowerCase()) || s.name.toLowerCase().includes(filter.toLowerCase()) || s.hosts.some((host) => host.includes(filter.toLowerCase()))).map((suggestion) => <div key={suggestion.id} className="flex items-center gap-3 rounded-lg border border-sep bg-ov-2 px-3 py-2.5">
                  <div className="min-w-0 flex-1"><div className="text-sm font-medium text-t1">{suggestionName(suggestion)}</div><div className="mt-0.5 truncate font-mono text-[11px] text-t4" title={suggestion.hosts.join("\n")}>{suggestion.hosts.slice(0, 3).join(" · ")}</div></div>
                  <span className="shrink-0 font-mono text-xs text-t4">{suggestion.hosts.length}</span>
                  <button type="button" onClick={() => openSuggestion(suggestion)} className="shrink-0 rounded-md border border-bdr px-2 py-1 text-xs text-t2 hover:border-accent hover:text-accent">{t("destinationsPage.reviewSuggestion")}</button>
                </div>)}
              </div>
            </section>}
            {addressHosts.length > 0 && (!filter || addressHosts.some((host) => host.includes(filter))) && <section>
              <div className="mb-2 border-b border-sep pb-2 text-[11px] font-semibold tracking-wide text-t4">{t("destinationsPage.unknownDestinations")}</div>
              <button type="button" onClick={() => { setSelectedScope("ip"); setScrollTop(0); }} className="flex w-full items-center gap-3 rounded-lg border border-sep bg-ov-2 px-3 py-2.5 text-left hover:border-bdr">
                <span className="flex-1 text-sm font-medium text-t2">{t("destinationsPage.ipAddresses")}</span><span className="font-mono text-xs text-t4">{addressHosts.length}</span>
              </button>
              <p className="mt-1.5 text-xs text-t4">{t("destinationsPage.ipHint")}</p>
            </section>}
            <section>
              <div className="mb-2 border-b border-sep pb-2 text-[11px] font-semibold tracking-wide text-t4">{t("destinationsPage.unclassifiedSites")} · {siteGroups.length}</div>
              <div className="space-y-1">
                {siteGroups.filter(([site, hosts]) => !filter || site.includes(filter.toLowerCase()) || hosts.some((host) => host.includes(filter.toLowerCase()))).slice(0, filter || showAllSites ? undefined : 20).map(([site, hosts]) => <div key={site} className="flex items-center gap-3 border-b border-sep/70 px-3 py-2">
                  <button type="button" onClick={() => { setSelectedScope(`site:${site}`); setFilter(""); setScrollTop(0); }} className="min-w-0 flex-1 text-left text-sm text-t2 hover:text-accent">{site}</button>
                  <span className="w-12 text-right font-mono text-xs text-t4">{hosts.length}</span>
                  <button type="button" onClick={() => openEditor(undefined, site)} className="rounded p-1 text-t4 hover:bg-ov-8 hover:text-t1" aria-label={t("destinationsPage.makeGroup")}><Plus className="h-3.5 w-3.5" /></button>
                </div>)}
                {!filter && siteGroups.length > 20 && <button type="button" onClick={() => setShowAllSites((value) => !value)} className="w-full border-b border-sep px-3 py-2 text-left text-xs text-accent hover:bg-ov-2">{showAllSites ? t("destinationsPage.showFewerSites") : t("destinationsPage.showAllSites", { count: siteGroups.length })}</button>}
                {siteGroups.length === 0 && sorted.length === 0 && <p className="py-5 text-sm text-t4">{t("destinationsPage.noHosts")}</p>}
              </div>
            </section>
          </div>
        ) : total === 0 ? (
          <>
            {sorted.length === 0 && (
              <p className="text-t4">{t("destinationsPage.noHosts")}</p>
            )}
            {sorted.length > 0 && (
              <p className="text-t4">{t("destinationsPage.noMatches")}</p>
            )}
          </>
        ) : (
          <div style={{ paddingTop: topPad, paddingBottom: bottomPad }}>
            {rows
              .slice(startIdx, endIdx)
              .map((r) => renderRow(r.host, r.draft))}
          </div>
        )}
      </div>
      {editingGroup && <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 p-4" role="dialog" aria-modal="true" aria-label={t("destinationsPage.editGroup")}>
        <div className="w-full max-w-lg rounded-xl border border-bdr bg-surface p-5 shadow-xl">
          <div className="mb-4 flex items-center justify-between"><h2 className="text-base font-semibold text-t1">{t("destinationsPage.editGroup")}</h2><button type="button" onClick={() => setEditingGroup(null)} className="text-t4 hover:text-t1"><X className="h-4 w-4" /></button></div>
          <label className="mb-3 block text-xs font-medium text-t3">{t("destinationsPage.groupName")}<input value={draftName} onChange={(e) => setDraftName(e.target.value)} className="mt-1 w-full rounded-md border border-bdr bg-ov-2 px-3 py-2 text-sm text-t1 outline-none focus:border-accent" /></label>
          <label className="mb-3 block text-xs font-medium text-t3">{t("destinationsPage.groupDomains")}<textarea value={draftDomains} onChange={(e) => setDraftDomains(e.target.value)} rows={5} className="mt-1 w-full resize-y rounded-md border border-bdr bg-ov-2 px-3 py-2 font-mono text-sm text-t1 outline-none focus:border-accent" /></label>
          <p className="mb-3 text-xs text-t4">{t("destinationsPage.domainHint")}</p>
          {invalidDomains.length > 0 && <p className="mb-3 text-xs text-danger">{t("destinationsPage.invalidDomains", { domains: invalidDomains.join(", ") })}</p>}
          <label className="mb-4 block text-xs font-medium text-t3">{t("destinationsPage.route")}<select value={draftRoute} onChange={(e) => setDraftRoute(e.target.value)} className="mt-1 w-full rounded-md border border-bdr bg-ov-2 px-3 py-2 text-sm text-t1"><option value="default">{t("destinationsPage.routeDefault")}</option>{children.map((p) => <option key={p.id} value={`tunnel:${p.id}`}>{p.name}</option>)}<option value="direct">{t("destinationsPage.routeDirect")}</option><option value="blocked">{t("destinationsPage.routeBlocked")}</option></select></label>
          <div className="mb-4 border-t border-sep pt-3 text-xs text-t3"><div className="font-medium">{t("destinationsPage.previewCount", { count: previewHosts.length })}</div><div className="mt-1 max-h-20 overflow-y-auto font-mono text-t4">{previewHosts.slice(0, 8).join(" · ")}</div></div>
          {(previewGroupOverlap > 0 || previewRuleOverrides > 0) && <p className="mb-4 text-xs text-warning">{t("destinationsPage.previewOverlap", { groups: previewGroupOverlap, rules: previewRuleOverrides })}</p>}
          <div className="flex items-center justify-between">
            {ruleGroups.some((g) => g.id === editingGroup.id) ? <button type="button" onClick={() => { if (window.confirm(t("destinationsPage.deleteConfirm"))) { deleteRuleGroup(editingGroup.id); setEditingGroup(null); } }} className="flex items-center gap-1 text-xs text-danger"><Trash2 className="h-3.5 w-3.5" />{t("destinationsPage.deleteGroup")}</button> : <span />}
            <div className="flex gap-2"><button type="button" onClick={() => setEditingGroup(null)} className="rounded-md px-3 py-1.5 text-xs text-t3 hover:bg-ov-6">{t("destinationsPage.cancel")}</button><button type="button" disabled={!draftName.trim() || normalizedDraftDomains.length === 0 || invalidDomains.length > 0} onClick={saveEditor} className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-40">{t("destinationsPage.saveGroup")}</button></div>
          </div>
        </div>
      </div>}
    </div>
  );
}
