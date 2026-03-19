import type { TextareaHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Textarea({
  className,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        "min-h-20 w-full resize-none rounded-lg border border-white/[0.10] bg-white/[0.06] px-3 py-2 text-sm text-white/90 placeholder:text-white/25 outline-none transition-all duration-150 focus:border-[#0a84ff]/60 focus:bg-white/[0.08] focus:ring-2 focus:ring-[#0a84ff]/15",
        className,
      )}
      {...props}
    />
  );
}
