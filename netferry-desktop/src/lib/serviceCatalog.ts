import catalog from "@/data/serviceDomains.json";
import type { RuleGroup } from "@/types";
import { matchesDomain, normalizeDomain } from "@/lib/ruleGroups";

export interface ServiceSuggestion {
  id: string;
  name: string;
  nameZh?: string;
  domains: string[];
  hosts: string[];
}

type Match = { serviceId: string; scope: string };
const exact = new Map<string, Match>();
const suffix = new Map<string, Match>();

// Catalog order resolves equal-scope overlap: specific services precede their
// parent ecosystems (for example YouTube precedes Google).
for (const service of catalog.services) {
  for (const input of service.domains) {
    const scope = normalizeDomain(input);
    if (!scope) continue;
    const map = scope.startsWith("=") ? exact : suffix;
    const key = scope.startsWith("=") ? scope.slice(1) : scope;
    if (!map.has(key)) map.set(key, { serviceId: service.id, scope });
  }
}

function identify(host: string): Match | null {
  const key = host.toLowerCase().replace(/\.$/, "");
  if (key.startsWith("*.")) return null;
  const full = exact.get(key);
  if (full) return full;
  const labels = key.split(".");
  for (let i = 0; i < labels.length; i++) {
    const match = suffix.get(labels.slice(i).join("."));
    if (match) return match;
  }
  return null;
}

export function suggestServiceGroups(hosts: string[], existing: RuleGroup[]): ServiceSuggestion[] {
  const found = new Map<string, { domains: Set<string>; hosts: string[] }>();
  for (const host of hosts) {
    if (existing.some((group) => group.domains.some((scope) => matchesDomain(host, scope)))) continue;
    const match = identify(host);
    if (!match) continue;
    const entry = found.get(match.serviceId) ?? { domains: new Set<string>(), hosts: [] };
    entry.domains.add(match.scope);
    entry.hosts.push(host);
    found.set(match.serviceId, entry);
  }
  return catalog.services.flatMap((service) => {
    const entry = found.get(service.id);
    if (!entry) return [];
    return [{ id: service.id, name: service.name, nameZh: "nameZh" in service ? service.nameZh : undefined, domains: [...entry.domains].sort(), hosts: entry.hosts }];
  }).sort((a, b) => b.hosts.length - a.hosts.length || a.name.localeCompare(b.name));
}

export const catalogRevision = catalog.revision;
export const catalogSource = catalog.source;
