import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

type BadgeVariant = "gray" | "yellow" | "green" | "red";

const styles: Record<BadgeVariant, string> = {
  gray: "bg-ov-8 text-t3 ring-1 ring-bdr",
  yellow: "bg-c-yellow/[0.12] text-c-yellow ring-1 ring-c-yellow/25",
  green: "bg-success/[0.12] text-success ring-1 ring-success/25",
  red: "bg-danger/[0.12] text-danger ring-1 ring-danger/25",
};

export function Badge({
  className,
  variant = "gray",
  ...props
}: HTMLAttributes<HTMLSpanElement> & { variant?: BadgeVariant }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium",
        styles[variant],
        className,
      )}
      {...props}
    />
  );
}
