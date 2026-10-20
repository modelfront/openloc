import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '@/lib/utils'

// PageContainer — the standard page wrapper. Every page picks one `width`
// template so content never hugs the window edge (it owns the horizontal
// padding) and never stretches edge-to-edge on wide monitors (it owns the
// max-width + centering). Top spacing comes from the shared <main> (pt-6).
//
//   default — list/detail pages. Capped + centered on wide screens.
//   full    — fixed-layout pages (CAT editor, browsers) that fill <main>
//             exactly and manage their own internal scroll. No max-width;
//             clips overflow so inner virtualized tables can't double-scroll.
//   wide    — full-bleed width but natural flow: content scrolls in <main>
//             like `default` does (dashboards).
//   docs    — `full`, capped + centered with room at the sides (docs pages).
//   docsBar — the docs header row, lined up with `docs`.
//
// Being a <div>, it forwards refs-free props (onDrop, data-*, etc.) to the
// element, so pages with drag handlers or test ids keep working.
const pageContainerVariants = cva('w-full px-4 md:px-6', {
  variants: {
    width: {
      // pb-24 leaves a comfortable scroll buffer below the content so the last
      // element isn't flush against the window bottom. `full` manages its own
      // height/scroll, so no bottom padding there.
      default: 'mx-auto max-w-4xl pb-24',
      // pt-1 -mt-1 trades 4px of <main>'s pt-6 for padding INSIDE the
      // clipping boundary — layout stays put, but the first focusable
      // element's 3px focus ring (focus-visible:ring-3) renders instead
      // of being cut off at the container's top edge.
      full: 'flex h-full flex-col overflow-hidden pt-1 -mt-1',
      docs: 'mx-auto max-w-[1680px] flex h-full flex-col overflow-hidden pt-1 -mt-1',
      docsBar: 'mx-auto max-w-[1680px]',
      wide: 'pb-24',
    },
  },
  defaultVariants: { width: 'default' },
})

export interface PageContainerProps
  extends React.ComponentProps<'div'>,
    VariantProps<typeof pageContainerVariants> {}

export function PageContainer({ className, width, ...props }: PageContainerProps) {
  return (
    <div data-slot="page-container" className={cn(pageContainerVariants({ width }), className)} {...props} />
  )
}
