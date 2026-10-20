// CreatorLabel — read-only display of who created an asset (project, TM, term
// base). Sibling of CustomerLabel, with one deliberate difference: the name
// arrives denormalized on the row (`created_by_name`, resolved by a LEFT JOIN
// on the list query), so there's no cache lookup and no loading state.
//
// Why a component for two lines of JSX: three consumers (ProjectList, TMList,
// TerminologyList) render this, and "how does an unattributed asset look?" is
// exactly the kind of detail that drifts when each page rolls its own em-dash.

interface Props {
  /** Resolved display name. '' / null / undefined all mean "unknown". */
  name?: string | null
  className?: string
  /** Override the unknown-creator placeholder. */
  emptyText?: string
}

export function CreatorLabel({ name, className = '', emptyText = '—' }: Props) {
  const trimmed = name?.trim()
  if (!trimmed) {
    return (
      <span
        className={`italic text-gray-400 dark:text-gray-500 ${className}`}
        title="No creator recorded (created before creator tracking, or the user was removed)"
        data-testid="creator-label-empty"
      >
        {emptyText}
      </span>
    )
  }
  return (
    <span className={`text-gray-700 dark:text-gray-300 ${className}`} data-testid="creator-label">
      {trimmed}
    </span>
  )
}
