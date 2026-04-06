import type { SelectHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Select({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        "h-9 w-full rounded-lg border border-bdr bg-sf-input px-3 py-2 text-sm text-t1 outline-none transition-all duration-150 focus:border-accent/60 focus:bg-sf-input-focus focus:ring-2 focus:ring-accent/15 cursor-pointer",
        className,
      )}
      {...props}
    />
  );
}
