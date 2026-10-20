import * as React from "react"
import { Input as InputPrimitive } from "@base-ui/react/input"

import { cn } from "@/lib/utils"
import { ClearButton } from "./clear-button"
import { controlFrameClasses, controlHeightClass, controlTextClasses } from "./control-box"
import { focusControlOnFrame } from "./focus-control-on-frame"
import { useClearable } from "./use-clearable"

export interface InputProps extends React.ComponentProps<"input"> {
  /** Leading content inside the box — usually a lucide icon. */
  icon?: React.ReactNode
  /** Trailing content inside the box — buttons, a spinner, a kbd hint. */
  action?: React.ReactNode
  /** Force the clear X on or off. Defaults to on for text-like types. */
  clearable?: boolean
  /** Replaces the default clear, which empties the field and fires `onChange`. */
  onClear?: () => void
  /** `seamless` drops the box entirely for inline rename-in-place. */
  variant?: "default" | "seamless"
  /** For classes that must land on the `<input>` rather than the box. */
  inputClassName?: string
  /** Lands on the `<input>`; the clear button derives `<testid>-clear` from it. */
  'data-testid'?: string
}

/**
 * A text field: the bordered box plus an optional leading icon, trailing action
 * and clear X. `className` styles the BOX — sizing (`w-*`, `h-*`, `flex-1`) and
 * border overrides belong there, and typography reaches the `<input>` by
 * inheritance. Everything else spreads onto the `<input>`, so `ref`,
 * `data-testid` and `{...register(...)}` behave as they would on a bare tag.
 */
function Input({
  className,
  inputClassName,
  type,
  icon,
  action,
  clearable,
  onClear,
  variant = "default",
  ref,
  ...props
}: InputProps) {
  const { setRef, handleChange, clear, showClear } = useClearable({
    ref,
    value: props.value,
    defaultValue: props.defaultValue,
    onChange: props.onChange,
    onClear,
    // A seamless field has no box, so the X has nothing to sit against — it
    // floats at the far end of whatever width the field spans, detached from
    // the text it clears.
    clearable: clearable ?? (variant === "seamless" ? false : undefined),
    type,
    disabled: props.disabled,
    readOnly: props.readOnly,
  })

  const seamless = variant === "seamless"
  const testId = props["data-testid"]

  return (
    <div
      data-slot="input"
      onMouseDown={focusControlOnFrame}
      className={cn(
        "flex w-full min-w-0 items-center gap-2",
        seamless
          ? "h-auto bg-transparent"
          : [controlHeightClass, controlFrameClasses, controlTextClasses, "cursor-text"],
        className
      )}
    >
      {icon && (
        <span data-slot="input-icon" className="flex shrink-0 items-center text-muted-foreground [&>svg]:size-4">
          {icon}
        </span>
      )}
      <InputPrimitive
        {...props}
        ref={setRef as React.Ref<HTMLElement>}
        type={type}
        onChange={handleChange}
        data-slot="input-control"
        className={cn(
          "h-full min-w-0 flex-1 border-0 bg-transparent p-0 outline-none placeholder:text-muted-foreground disabled:cursor-not-allowed",
          inputClassName
        )}
      />
      {showClear && <ClearButton onClear={clear} data-testid={testId ? `${testId}-clear` : undefined} />}
      {action && (
        <span data-slot="input-action" className="flex shrink-0 items-center gap-0.5 [&>svg]:size-4">
          {action}
        </span>
      )}
    </div>
  )
}

export { Input }
