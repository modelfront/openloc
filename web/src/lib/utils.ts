import { clsx, type ClassValue } from "clsx"
import { extendTailwindMerge } from "tailwind-merge"

// `--spacing-input` (index.css) is a custom scale value, so tailwind-merge has
// no way to know `h-input` belongs to the height group. Without registering it,
// `cn('h-input', 'h-7')` keeps BOTH and the caller's override silently loses to
// the cascade.
const twMerge = extendTailwindMerge({
  extend: { theme: { spacing: ["input"] } },
})

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}
