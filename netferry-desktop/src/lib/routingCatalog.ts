import catalog from "@/data/routingDomains.json";
import type { RuleGroup } from "@/types";
import { matchesDomain, normalizeDomain } from "@/lib/ruleGroups";

export interface RoutingSuggestion {
  id: string;
  name: string;
  nameZh: string;
  source: string;
  sourceZh?: string;
  suggestedRoute: "tunnel" | "direct";
  domains: string[];
  hosts: string[];
  coveredHosts: number;
  evidence: { domain: string; product: string; url: string }[];
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
  const found = catalog.scopes.map(() => ({ domains: new Set<string>(), hosts: [] as string[], coveredHosts: 0 }));
  for (const host of hosts) {
    const covered = existing.some((group) => group.domains.some((scope) => matchesDomain(host, scope)));
    indexes.forEach((index, i) => {
      const match = identify(host, index);
      if (!match) return;
      found[i].domains.add(match.scope);
      found[i].hosts.push(host);
      if (covered) found[i].coveredHosts++;
    });
  }
  return catalog.scopes.flatMap((entry, i) => found[i].hosts.length ? [{
    id: entry.id,
    name: entry.name,
    nameZh: entry.nameZh,
    source: entry.source,
    sourceZh: "sourceZh" in entry ? entry.sourceZh : undefined,
    // The generated catalog predates the tunnel/direct/blocked route set and
    // may still say "default"; anything but direct means tunnel.
    suggestedRoute: entry.suggestedRoute === "direct" ? "direct" : "tunnel",
    domains: [...found[i].domains].sort(),
    hosts: found[i].hosts,
    coveredHosts: found[i].coveredHosts,
    evidence: catalog.regionalRestrictions.filter((item) => found[i].domains.has(item.domain)).map((item) => ({ domain: item.domain, product: item.product, url: item.evidence })),
  }] : []);
}

export const routingCatalogSources = catalog.sources;
