import type { TextareaHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export function Textarea({
  className,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        "min-h-20 w-full resize-none rounded-lg border border-bdr bg-ov-6 px-3 py-2 text-sm text-t1 placeholder:text-t4 outline-none transition-all duration-150 focus:border-accent/60 focus:bg-ov-8 focus:ring-2 focus:ring-accent/15",
        className,
      )}
      {...props}
    />
  );
}
