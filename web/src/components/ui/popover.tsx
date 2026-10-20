import * as React from "react"
import { Popover as PopoverPrimitive } from "@base-ui/react/popover"

import { cn } from "@/lib/utils"
import { useReportPopupOpen } from "@/components/ui/layered-dismiss"

/**
 * Popover wrapper that prevents scroll jumps on open.
 * When a Portal-based popover opens and a child (e.g. cmdk's CommandInput)
 * calls focus() without preventScroll, the browser scrolls to show the
 * focused element inside the Portal. We catch the first scroll event after
 * open and snap back — no monkey-patching, no timing assumptions.
 */
function Popover({ onOpenChange, ...props }: PopoverPrimitive.Root.Props) {
  // Tells an enclosing dialog that a layer is stacked on top of it, so one
  // press on the backdrop closes this popover and not the dialog as well.
  const reportOpen = useReportPopupOpen()
  const handleOpenChange = React.useCallback((...args: Parameters<NonNullable<typeof onOpenChange>>) => {
    const [open] = args
    reportOpen(open)
    if (open) {
      const scrollX = window.scrollX
      const scrollY = window.scrollY
      const handler = () => {
        window.scrollTo(scrollX, scrollY)
      }
      // AbortController rather than removeEventListener: the safety-cleanup
      // timer below can outlive the page (or, in vitest, the jsdom environment
      // of the test file that opened this popover). controller.abort() touches
      // no globals, so it can't throw "window is not defined" after teardown.
      const controller = new AbortController()
      window.addEventListener('scroll', handler, { once: true, signal: controller.signal })
      // Safety cleanup — drop the listener after 500ms so normal scrolling isn't blocked
      setTimeout(() => controller.abort(), 500)
    }
    onOpenChange?.(...args)
  }, [onOpenChange, reportOpen])

  return <PopoverPrimitive.Root data-slot="popover" onOpenChange={handleOpenChange} {...props} />
}

function PopoverTrigger({ ...props }: PopoverPrimitive.Trigger.Props) {
  return <PopoverPrimitive.Trigger data-slot="popover-trigger" {...props} />
}

// `compact` is the dense card for hover/click info bubbles (QA finding, regex
// help, sidebar customizer): tighter padding, 12px text, semibold title.
type PopoverSize = "default" | "compact"

const popoverSizeClasses: Record<PopoverSize, string> = {
  default: "",
  compact: "gap-2 px-3 py-2.5 text-xs",
}

function PopoverContent({
  className,
  align = "center",
  alignOffset = 0,
  side = "bottom",
  sideOffset = 4,
  size = "default",
  anchor,
  ...props
}: PopoverPrimitive.Popup.Props &
  Pick<
    PopoverPrimitive.Positioner.Props,
    "align" | "alignOffset" | "side" | "sideOffset" | "anchor"
  > & { size?: PopoverSize }) {
  return (
    <PopoverPrimitive.Portal>
        <PopoverPrimitive.Positioner
          anchor={anchor}
          align={align}
          alignOffset={alignOffset}
          side={side}
          sideOffset={sideOffset}
          className="isolate z-50"
        >
          <PopoverPrimitive.Popup
            data-slot="popover-content"
            data-size={size}
            className={cn(
              "z-50 flex w-72 origin-(--transform-origin) flex-col gap-2.5 rounded-lg bg-popover p-2.5 text-sm text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-hidden duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
              popoverSizeClasses[size],
              className
            )}
            {...props}
          />
        </PopoverPrimitive.Positioner>
    </PopoverPrimitive.Portal>
  )
}

function PopoverHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="popover-header"
      className={cn("flex flex-col gap-0.5 text-sm", className)}
      {...props}
    />
  )
}

function PopoverTitle({ className, ...props }: PopoverPrimitive.Title.Props) {
  return (
    <PopoverPrimitive.Title
      data-slot="popover-title"
      className={cn("font-medium in-data-[size=compact]:font-semibold", className)}
      {...props}
    />
  )
}

function PopoverDescription({
  className,
  ...props
}: PopoverPrimitive.Description.Props) {
  return (
    <PopoverPrimitive.Description
      data-slot="popover-description"
      className={cn("text-muted-foreground", className)}
      {...props}
    />
  )
}

export {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
}
