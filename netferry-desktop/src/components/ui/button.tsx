import type { ButtonHTMLAttributes } from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const buttonVariants = cva(
  "inline-flex items-center justify-center rounded-lg text-sm font-medium transition-all duration-150 select-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:pointer-events-none disabled:opacity-35",
  {
    variants: {
      variant: {
        default:
          "bg-accent text-white hover:brightness-115 active:opacity-90 shadow-sm shadow-accent/20",
        secondary:
          "bg-ov-8 text-t1 hover:bg-ov-12 border border-bdr",
        ghost: "text-t2 hover:bg-ov-8 hover:text-t1",
        outline:
          "border border-edge bg-transparent text-t2 hover:bg-ov-8 hover:text-t1",
        danger:
          "bg-danger text-white hover:brightness-115 active:opacity-90 shadow-sm shadow-danger/20",
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
