import type { RouteModeV2, RuleGroup } from "@/types";

/** Normalize one user-entered scope. Empty/invalid entries are discarded. */
export function normalizeDomain(input: string): string | null {
  const raw = input.trim().toLowerCase().replace(/\.$/, "");
  const exact = raw.startsWith("=");
  const host = exact ? raw.slice(1) : raw;
  if (!host || host.length > 253 || host.includes("..") || /[^a-z0-9.-]/.test(host)) return null;
  if (host.split(".").some((label) => !label || label.length > 63 || label.startsWith("-") || label.endsWith("-"))) return null;
  return exact ? `=${host}` : host;
}

export function matchesDomain(host: string, domain: string): boolean {
  const normalized = normalizeDomain(domain);
  if (!normalized) return false;
  const value = normalized.startsWith("=") ? normalized.slice(1) : normalized;
  const key = host.toLowerCase().replace(/\.$/, "");
  return key === value || (!normalized.startsWith("=") && key.endsWith(`.${value}`));
}

export function compileRoutes(groups: RuleGroup[], overrides: Record<string, RouteModeV2>): Record<string, RouteModeV2> {
  const compiled: Record<string, RouteModeV2> = {};
  for (const group of groups) {
    for (const input of group.domains) {
      const domain = normalizeDomain(input);
      if (!domain) continue;
      if (domain.startsWith("=")) {
        compiled[domain.slice(1)] = group.route;
      } else {
        compiled[domain] = group.route;
        compiled[`*.${domain}`] = group.route;
      }
    }
  }
  return { ...compiled, ...overrides };
}
