import * as React from "react"

import { cn } from "@/lib/utils"
import { controlFrameClasses, controlTextClasses } from "./control-box"
import { focusControlOnFrame } from "./focus-control-on-frame"

export interface TextareaProps extends React.ComponentProps<"textarea"> {
  /** Trailing content, on its own row along the bottom edge. */
  action?: React.ReactNode
  /** `seamless` drops the box entirely. */
  variant?: "default" | "seamless"
  /** For classes that must land on the `<textarea>` rather than the box — `resize-*`, `max-h-*`. */
  inputClassName?: string
  /** Files are dragged over the field and it accepts them: the box shows the focus ring. */
  dropTarget?: boolean
}

/**
 * The multi-line counterpart of `Input`. `action` sits on a row along the
 * bottom edge rather than inline, so it never crowds the text. `className`
 * styles the BOX; `inputClassName` reaches the `<textarea>` for the things only
 * it understands (`resize-*`, `max-h-*`).
 *
 * There is no clear X here, unlike `Input`: wiping a paragraph in one click is
 * a bigger loss than the keystrokes it saves, and the button would have to live
 * on the action row, far from the text.
 */
function Textarea({
  className,
  inputClassName,
  action,
  variant = "default",
  dropTarget = false,
  ...props
}: TextareaProps) {
  const seamless = variant === "seamless"

  return (
    <div
      data-slot="textarea"
      onMouseDown={focusControlOnFrame}
      className={cn(
        "flex w-full min-w-0 flex-col py-2",
        seamless
          ? "bg-transparent"
          : [controlFrameClasses, controlTextClasses, "cursor-text"],
        "h-auto",
        dropTarget && "border-ring ring-3 ring-ring/50",
        className
      )}
    >
      <div className="flex w-full min-w-0 flex-1">
        {/* eslint-disable-next-line no-restricted-syntax -- the Textarea primitive itself */}
        <textarea
          {...props}
          data-slot="input-control"
          className={cn(
            // The minimum lives HERE, not on the box: the drag handle resizes
            // this element, and the browser only clamps the drag against the
            // element's own min-height. On the box it leaves the field
            // collapsible to nothing inside a full-height border.
            // min-h-12 + the box's py-2 is the 64px default field.
            "field-sizing-content min-h-12 min-w-0 flex-1 border-0 bg-transparent p-0 outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed",
            inputClassName
          )}
        />
      </div>
      {action && (
        <div data-slot="textarea-action" className="flex w-full items-center justify-end gap-1 pt-1 [&>svg]:size-4">
          {action}
        </div>
      )}
    </div>
  )
}

export { Textarea }
