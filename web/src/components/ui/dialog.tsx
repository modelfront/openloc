import * as React from "react"
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { useDialogDrag, type DialogDrag } from "@/components/ui/use-dialog-drag"
import { XIcon } from "lucide-react"

/** Set by a `draggable` DialogContent; makes its DialogHeader the grab area. */
const DialogDragContext = React.createContext<DialogDrag | null>(null)

/**
 * `keepOpen` ignores every request to close (Escape, an outside click, the X)
 * while it is set — pass the save's pending flag, so a dialog whose request
 * then fails still holds what the user typed. Closing by setting `open` still
 * works.
 */
function Dialog({ keepOpen = false, onOpenChange, ...props }: DialogPrimitive.Root.Props & { keepOpen?: boolean }) {
  return (
    <DialogPrimitive.Root
      data-slot="dialog"
      onOpenChange={(open, details) => {
        if (!open && keepOpen) return
        onOpenChange?.(open, details)
      }}
      {...props}
    />
  )
}

function DialogTrigger({ ...props }: DialogPrimitive.Trigger.Props) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />
}

function DialogPortal({ ...props }: DialogPrimitive.Portal.Props) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />
}

function DialogClose({ ...props }: DialogPrimitive.Close.Props) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />
}

function DialogOverlay({
  className,
  ...props
}: DialogPrimitive.Backdrop.Props) {
  return (
    <DialogPrimitive.Backdrop
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-black/10 dark:bg-white/10 duration-100 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className
      )}
      {...props}
    />
  )
}

function DialogContent({
  className,
  children,
  showCloseButton = true,
  draggable = false,
  style,
  ref,
  ...props
}: DialogPrimitive.Popup.Props & {
  showCloseButton?: boolean
  /** Let the user move the dialog by its `DialogHeader`. Position resets on close. */
  draggable?: boolean
}) {
  const drag = useDialogDrag(draggable)
  // Stable: React re-runs a ref callback whose identity changed, which during a
  // drag would detach the popup and reset its position on every move.
  const attachPopup = drag?.attachPopup
  const setPopupRef = React.useCallback(
    (node: HTMLDivElement | null) => {
      attachPopup?.(node)
      if (typeof ref === "function") ref(node)
      else if (ref) ref.current = node
    },
    [attachPopup, ref]
  )
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Popup
        data-slot="dialog-content"
        ref={setPopupRef}
        style={{ ...style, ...drag?.style }}
        className={cn(
          // grid-cols-[minmax(0,1fr)]: without it the implicit column track
          // sizes to the items' min-content, so one unbreakable string (a long
          // file name) widens every child past the dialog box.
          "fixed top-1/2 left-1/2 z-50 grid grid-cols-[minmax(0,1fr)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl bg-background p-4 text-sm ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        {...props}
      >
        <DialogDragContext.Provider value={drag}>
          {children}
        </DialogDragContext.Provider>
        {showCloseButton && (
          <DialogPrimitive.Close
            data-slot="dialog-close"
            render={
              <Button variant="ghost" size="icon"
                className="absolute top-2 right-2"
              />
            }
          >
            <XIcon
            />
            <span className="sr-only">Close</span>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Popup>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  const drag = React.useContext(DialogDragContext)
  return (
    <div
      data-slot="dialog-header"
      data-draggable={drag ? "true" : undefined}
      onPointerDown={drag?.onPointerDown}
      className={cn(
        "flex flex-col gap-2",
        drag && "cursor-grab touch-none select-none active:cursor-grabbing",
        className
      )}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close render={<Button variant="outline" />}>
          Close
        </DialogPrimitive.Close>
      )}
    </div>
  )
}

function DialogTitle({ className, ...props }: DialogPrimitive.Title.Props) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn("text-base leading-none font-medium wrap-anywhere", className)}
      {...props}
    />
  )
}

function DialogDescription({
  className,
  ...props
}: DialogPrimitive.Description.Props) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn(
        "text-sm wrap-anywhere text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}
