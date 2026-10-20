import type { ComponentProps } from "react"
import { Trash2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { hoverActionClass } from "@/components/list/hover-action"
import { cn } from "@/lib/utils"

// The standard delete/remove affordance: a square, icon-only ghost Button with a
// trash icon, muted gray at rest, red on hover (shared danger styling from
// hoverActionClass). Always the trash icon, never a label — use it everywhere a row
// or entity can be deleted so deletes look and behave the same. Forwards Button props
// (size, onClick, disabled, loading, aria-label…). Wrap in a Tooltip at the call site
// for a label popover.
function DeleteButton({
  className,
  size = "icon",
  ...props
}: Omit<ComponentProps<typeof Button>, "children" | "variant">) {
  return (
    <Button
      variant="ghost"
      size={size}
      aria-label="Delete"
      className={cn(hoverActionClass("danger"), className)}
      {...props}
    >
      <Trash2 />
    </Button>
  )
}

export { DeleteButton }
