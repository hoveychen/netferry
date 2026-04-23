// Color palette for tunnel-index / profile-index badges.
// Indexes are 1-based; the same index always maps to the same color so
// per-tunnel cards, per-profile cards, and per-connection badges align.

export const TUNNEL_COLORS = [
  { bg: "bg-accent/20", text: "text-accent", dot: "bg-accent" },
  { bg: "bg-success/20", text: "text-success", dot: "bg-success" },
  { bg: "bg-c-purple/20", text: "text-c-purple", dot: "bg-c-purple" },
  { bg: "bg-warning/20", text: "text-warning", dot: "bg-warning" },
  { bg: "bg-danger/20", text: "text-danger", dot: "bg-danger" },
  { bg: "bg-c-yellow/20", text: "text-c-yellow", dot: "bg-c-yellow" },
];

export function tunnelColor(idx: number) {
  return TUNNEL_COLORS[(idx - 1) % TUNNEL_COLORS.length];
}
