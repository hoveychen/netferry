import catalog from "@/data/routingDomains.json";
import type { RuleGroup } from "@/types";
import { matchesDomain, normalizeDomain } from "@/lib/ruleGroups";

export interface RoutingSuggestion {
  id: string;
  name: string;
  nameZh: string;
  source: string;
  suggestedRoute: "default" | "direct";
  domains: string[];
  hosts: string[];
}

type Match = { scope: string };
type Index = { exact: Map<string, Match>; suffix: Map<string, Match> };

const indexes: Index[] = catalog.scopes.map((entry) => {
  const exact = new Map<string, Match>();
  const suffix = new Map<string, Match>();
  for (const input of entry.domains) {
    const scope = normalizeDomain(input);
    if (!scope) continue;
    const isExact = scope.startsWith("=");
    const key = isExact ? scope.slice(1) : scope;
    const map = isExact ? exact : suffix;
    if (!map.has(key)) map.set(key, { scope });
  }
  return { exact, suffix };
});

function identify(host: string, index: Index): Match | null {
  const key = host.toLowerCase().replace(/\.$/, "");
  if (key.startsWith("*.")) return null;
  const full = index.exact.get(key);
  if (full) return full;
  const labels = key.split(".");
  for (let i = 0; i < labels.length; i++) {
    const match = index.suffix.get(labels.slice(i).join("."));
    if (match) return match;
  }
  return null;
}

export function suggestRoutingScopes(hosts: string[], existing: RuleGroup[]): RoutingSuggestion[] {
  const found = catalog.scopes.map(() => ({ domains: new Set<string>(), hosts: [] as string[] }));
  for (const host of hosts) {
    if (existing.some((group) => group.domains.some((scope) => matchesDomain(host, scope)))) continue;
    indexes.forEach((index, i) => {
      const match = identify(host, index);
      if (!match) return;
      found[i].domains.add(match.scope);
      found[i].hosts.push(host);
    });
  }
  return catalog.scopes.flatMap((entry, i) => found[i].hosts.length ? [{
    id: entry.id,
    name: entry.name,
    nameZh: entry.nameZh,
    source: entry.source,
    suggestedRoute: entry.suggestedRoute as "default" | "direct",
    domains: [...found[i].domains].sort(),
    hosts: found[i].hosts,
  }] : []);
}

export const routingCatalogSources = catalog.sources;
