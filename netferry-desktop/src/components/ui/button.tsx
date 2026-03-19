import type { ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-lg text-sm font-medium transition-all duration-150 select-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#0a84ff]/40 disabled:pointer-events-none disabled:opacity-35",
  {
    variants: {
      variant: {
        default:
          "bg-[#0a84ff] text-white hover:bg-[#409cff] active:opacity-90 shadow-sm shadow-[#0a84ff]/20",
        secondary:
          "bg-white/[0.08] text-white/80 hover:bg-white/[0.12] border border-white/[0.10]",
        ghost: "text-white/55 hover:bg-white/[0.08] hover:text-white/85",
        outline:
          "border border-white/[0.14] bg-transparent text-white/70 hover:bg-white/[0.08] hover:text-white/90",
        danger:
          "bg-[#ff453a] text-white hover:bg-[#ff6b61] active:opacity-90 shadow-sm shadow-[#ff453a]/20",
      },
      size: {
        default: "h-8 px-4 py-1.5",
        sm: "h-7 px-3 text-xs",
        lg: "h-10 px-5",
      },
    },
    defaultVariants: {
      variant: "default",
      size: "default",
    },
  },
);

type Props = ButtonHTMLAttributes<HTMLButtonElement> &
  VariantProps<typeof buttonVariants>;

export function Button({ className, variant, size, ...props }: Props) {
  return (
    <button
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  );
}
