import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

// shadcn/ui convention: merge conditional classes and let later Tailwind
// utilities win, so a caller can override a component's default spacing.
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
