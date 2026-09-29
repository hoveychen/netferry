import type { FinalRoute, RouteRule, RuleGroup } from "@/types";
import { getDomain } from "tldts";

/** Normalize one user-entered scope. Empty/invalid entries are discarded. */
export function normalizeDomain(input: string): string | null {
  const raw = input.trim().toLowerCase().replace(/\.$/, "");
  const exact = raw.startsWith("=");
  const host = exact ? raw.slice(1) : raw;
  if (!host || host.length > 253 || host.includes("..") || /[^a-z0-9.-]/.test(host)) return null;
  if (host.split(".").some((label) => !label || label.length > 63 || label.startsWith("-") || label.endsWith("-"))) return null;
  if (!exact && !getDomain(host, { allowPrivateDomains: true })) return null;
  return exact ? `=${host}` : host;
}

export function matchesDomain(host: string, domain: string): boolean {
  const normalized = normalizeDomain(domain);
  if (!normalized) return false;
  const value = normalized.startsWith("=") ? normalized.slice(1) : normalized;
  const key = host.toLowerCase().replace(/\.$/, "");
  return key === value || (!normalized.startsWith("=") && key.endsWith(`.${value}`));
}

/** The first rule group (in list order) with a domain covering host. */
export function ruleGroupFor(groups: RuleGroup[], host: string): RuleGroup | undefined {
  return groups.find((group) => group.domains.some((domain) => matchesDomain(host, domain)));
}

/** The per-host override key that applies to host: exact, then the most
 *  specific `*.suffix`. */
export function overrideKeyFor(overrides: Record<string, RouteRule>, host: string): string | undefined {
  const key = host.toLowerCase().replace(/\.$/, "");
  if (overrides[key]) return key;
  const labels = key.split(".");
  for (let i = 1; i < labels.length; i++) {
    const wildcard = `*.${labels.slice(i).join(".")}`;
    if (overrides[wildcard]) return wildcard;
  }
  return undefined;
}

export type RouteSource =
  | { type: "override"; key: string }
  | { type: "group"; group: RuleGroup }
  | { type: "final" };

/**
 * Resolve a host for display, mirroring the tunnel's matcher
 * (netferry-relay/internal/stats): per-host override > first matching rule
 * group > final route.
 */
export function resolveRoute(
  host: string,
  overrides: Record<string, RouteRule>,
  groups: RuleGroup[],
  finalRoute: FinalRoute,
): { route: RouteRule; source: RouteSource } {
  const key = overrideKeyFor(overrides, host);
  if (key) return { route: overrides[key], source: { type: "override", key } };
  const group = ruleGroupFor(groups, host);
  if (group) return { route: group.route, source: { type: "group", group } };
  return { route: finalRoute, source: { type: "final" } };
}

/** Body for the tunnel's POST /routes. The tunnel does the matching. */
export function routesPayload(
  overrides: Record<string, RouteRule>,
  groups: RuleGroup[],
  finalRoute: FinalRoute,
) {
  return { overrides, groups, final: finalRoute };
}
