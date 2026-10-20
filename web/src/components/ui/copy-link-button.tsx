import { useEffect, useRef, useState } from "react"
import { Check, Link2, type LucideIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import type { ComponentProps } from "react"

// The standard "copy this URL" affordance: an outline Button that swaps its icon
// and label to a confirmation for two seconds, and toasts once. Use it anywhere a
// page, view or invite is shareable by link, so copying always looks the same.
//
// The clipboard write is guarded: `navigator.clipboard` is undefined on insecure
// origins and rejects when the permission is denied, and neither should take the
// page down. A failure says so rather than showing a silent success;
// `errorMessage` words that toast for whatever is being copied.
//
// `iconOnly` draws it as a ghost icon button for a row of icon actions; the
// label then becomes the tooltip and the accessible name.
const RESET_MS = 2000

function CopyLinkButton({
  url,
  label = "Copy link",
  copiedLabel = "Copied",
  toastMessage = "Link copied",
  errorMessage = "Could not copy the link — copy it from the address bar instead",
  icon: Icon = Link2,
  iconOnly = false,
  ...props
}: Omit<ComponentProps<typeof Button>, "children" | "onClick"> & {
  url: string
  label?: string
  copiedLabel?: string
  toastMessage?: string
  errorMessage?: string
  icon?: LucideIcon
  iconOnly?: boolean
}) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // The reset outlives a fast unmount (navigating away right after copying),
  // which would otherwise set state on a dead component.
  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url)
    } catch {
      toast.error(errorMessage)
      return
    }
    setCopied(true)
    toast.success(toastMessage)
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), RESET_MS)
  }

  if (iconOnly) {
    const name = copied ? copiedLabel : label
    return (
      <Button variant="ghost" size="icon" title={name} aria-label={name} onClick={copy} {...props}>
        {copied ? <Check /> : <Icon />}
      </Button>
    )
  }
  return (
    <Button variant="outline" size="sm" onClick={copy} {...props}>
      {copied ? <Check /> : <Icon />}
      {copied ? copiedLabel : label}
    </Button>
  )
}

export { CopyLinkButton }
