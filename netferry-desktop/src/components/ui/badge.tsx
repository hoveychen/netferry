import type { HTMLAttributes } from "react";
import { cn } from "@/lib/utils";

type BadgeVariant = "gray" | "yellow" | "green" | "red";

const styles: Record<BadgeVariant, string> = {
  gray: "bg-white/[0.08] text-white/50 ring-1 ring-white/[0.08]",
  yellow: "bg-[#ffd60a]/[0.12] text-[#ffd60a] ring-1 ring-[#ffd60a]/25",
  green: "bg-[#30d158]/[0.12] text-[#30d158] ring-1 ring-[#30d158]/25",
  red: "bg-[#ff453a]/[0.12] text-[#ff453a] ring-1 ring-[#ff453a]/25",
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
