/**
 * A labelled page-level band marking a change of topic between groups of
 * sections — deliberately heavier and wider than the per-section hairlines,
 * which only delimit one section's header.
 *
 * Only for a genuine topic change. Two groups of the same kind of thing need no
 * divider at all: a `SlimCollapsibleSection` header already carries its own
 * hairline floor, so putting a rule between two of them draws two.
 *
 * Two segments either side of the label, rather than one rule with a
 * background-coloured label sitting on top of it — that makes no assumption
 * about what colour the page behind the divider happens to be.
 *
 * `-mx-3` matches the sections' own bleed so it lines up with the section rows
 * rather than overshooting them.
 *
 * Margins are set by eye, not symmetry: an open section's body ends with its
 * own `pb-4`, so 16px of the gap above is already spent inside the section box.
 * mt-4 + that padding = 32px above, mb-6 = 24px below.
 */
export function SectionDivider({ label }: { label: string }) {
  return (
    <div role="separator" aria-label={label} className="-mx-3 mt-4 mb-6 flex items-center gap-3">
      <span aria-hidden className="h-1 flex-1 bg-gray-200 dark:bg-gray-800" />
      <span className="shrink-0 text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
      <span aria-hidden className="h-1 flex-1 bg-gray-200 dark:bg-gray-800" />
    </div>
  )
}
