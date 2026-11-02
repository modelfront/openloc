import { Toaster as Sonner, type ToasterProps } from "sonner"
import { CircleCheckIcon, InfoIcon, TriangleAlertIcon, OctagonXIcon, Loader2Icon } from "lucide-react"

const Toaster = ({ ...props }: ToasterProps) => {
  return (
    <Sonner
      theme="light"
      className="toaster group"
      closeButton
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      toastOptions={{
        // The `!` utilities override Sonner's own close-button styles: a
        // plain X inside the top-right corner, with room kept for it.
        classNames: {
          toast: "cn-toast pr-10!",
          closeButton:
            "left-auto! right-1! top-1! transform-none! size-6! rounded-md! border-0! bg-transparent! text-muted-foreground! hover:bg-muted! hover:text-foreground!",
        },
      }}
      {...props}
    />
  )
}

export { Toaster }
