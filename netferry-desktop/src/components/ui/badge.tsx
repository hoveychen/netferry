import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

type BadgeVariant = "gray" | "yellow" | "green" | "red";

const styles: Record<BadgeVariant, string> = {
  gray: "bg-slate-100 text-slate-800",
  yellow: "bg-amber-100 text-amber-800",
  green: "bg-emerald-100 text-emerald-800",
  red: "bg-rose-100 text-rose-800",
};

export function Badge({
  className,
  variant = "gray",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: BadgeVariant }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-1 text-xs font-medium",
        styles[variant],
        className,
      )}
      {...props}
    />
  );
}
