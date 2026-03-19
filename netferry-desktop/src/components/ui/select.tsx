import type { SelectHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "h-9 w-full rounded-lg border border-white/[0.10] bg-[#2c2c2e] px-3 py-2 text-sm text-white/90 outline-none transition-all duration-150 focus:border-[#0a84ff]/60 focus:ring-2 focus:ring-[#0a84ff]/15 cursor-pointer",
        className,
      )}
      {...props}
    />
  );
}
