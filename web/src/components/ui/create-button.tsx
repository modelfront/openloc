import type { ComponentProps } from "react"
import { Plus } from "lucide-react"

import { Button } from "@/components/ui/button"

// The "New …" action in a page or section header: an outline button, the
// default size and a leading "+", so every create button looks the same.
function CreateButton(
  props: Omit<ComponentProps<typeof Button>, "variant" | "size" | "startIcon">,
) {
  return <Button {...props} variant="outline" startIcon={<Plus />} />
}

export { CreateButton }
