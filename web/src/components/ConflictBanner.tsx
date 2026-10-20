import { TriangleAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Alert } from '@/components/ui/alert'
import { RichSourceText, hasXliffTags } from './SegmentRow.shared'

/**
 * Identity context that drives the banner heading copy. Three branches:
 *   1. Same user (different tab/session) — `editedByUserId === currentUserId`.
 *   2. Different user with a name — generic "by your name" reads weird when
 *      YOU are that name; this branch names them so the translator knows
 *      who to coordinate with.
 *   3. Unknown — auth/me still loading, or the server row has no
 *      resolvable user (NULL `edited_by`/`updated_by_user_id` from a
 *      machine-edited row, or a label that didn't JOIN to a user).
 */
interface ConflictIdentity {
  /** Server row's editor ID. Null when unresolvable (machine writers). */
  editedByUserId: number | null | undefined
  /** Server row's editor display name. Empty string allowed. */
  editedByName: string
  /** Current user's ID — null while auth/me is still resolving. */
  currentUserId?: number | null
}

interface Props extends ConflictIdentity {
  /** Server's text for the field at conflict time. Rendered as plain unless
   *  `theirRichText` carries XLIFF tags (then chips win). */
  theirText: string
  theirRichText?: string | null
  /** Live editor text. Rendered as plain unless `myEditRichText` has tags. */
  myEditText: string
  myEditRichText?: string | null
  /** Field label shown above each diff column. Defaults to TRANSLATION (CAT
   *  case); TM cell-edit passes "SOURCE" or "TARGET". */
  fieldLabel?: string
  /** Discard local edits, refetch the server's version. */
  onUseTheirs: () => void
  /** Overwrite the server with my edits (bypass optimistic-lock check). */
  onUseMine: () => void
}

interface BannerCopy {
  title: string
  subtitle: string
}

/**
 * Pick the banner heading + subtitle from identity context. Exported for
 * unit tests so each branch can be pinned without rendering the component.
 */
export function getBannerCopy({ editedByUserId, editedByName, currentUserId }: ConflictIdentity): BannerCopy {
  const userId = currentUserId ?? null
  const editor = editedByUserId ?? null
  const isSameUser = userId !== null && editor !== null && editor === userId
  if (isSameUser) {
    return {
      title: 'Saved from another tab or session',
      subtitle: 'You already saved this somewhere else. Pick which version to keep.',
    }
  }
  const name = editedByName?.trim()
  if (name) {
    return {
      title: `${name} saved a different version`,
      subtitle: 'Pick which version to keep — both are shown below.',
    }
  }
  return {
    title: 'Server has a different version',
    subtitle: 'Another version was saved. Pick which one to keep.',
  }
}

/**
 * Inline conflict banner shown when an optimistic-lock save returns 409.
 * Used by the CAT segment editor (single field: translation) AND the TM
 * Browser cell editor (single field: source OR target).
 *
 * Side-by-side: both versions are visible; the user picks one explicitly with no dismiss / "Hide" / "Keep editing" trap. The
 * editor below stays interactive — typing keeps mutating "my edit" until
 * the user picks an action.
 *
 * Both sides render with XLIFF tag chips when rich text is available, so
 * inline tags show the same in "My edit" and "Their version".
 *
 * Caller-driven via primitive props (no payload abstraction) so CAT and TM
 * each map their own conflict-response shape onto the same diff.
 */
export default function ConflictBanner({
  editedByUserId,
  editedByName,
  currentUserId,
  theirText,
  theirRichText,
  myEditText,
  myEditRichText,
  fieldLabel = 'TRANSLATION',
  onUseTheirs,
  onUseMine,
}: Props) {
  const { title, subtitle } = getBannerCopy({ editedByUserId, editedByName, currentUserId })
  const theirRich = theirRichText && hasXliffTags(theirRichText) ? theirRichText : null
  const mineRich = myEditRichText && hasXliffTags(myEditRichText) ? myEditRichText : null

  return (
    // role="alert": the conflict blocks the save until the user picks a side.
    <Alert
      tone="warning"
      role="alert"
      data-testid="conflict-banner"
      className="mb-2"
      icon={<TriangleAlert />}
      title={<span data-testid="conflict-banner-title">{title}</span>}
    >
      <div className="text-xs">
        {subtitle}
      </div>

      <div className="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-2 mb-3">
        <div data-testid="conflict-banner-theirs"
             className="rounded border border-border bg-background text-foreground p-2">
          <div className="text-xs uppercase tracking-wide text-muted-foreground mb-1">
            Their {fieldLabel.toLowerCase()}
          </div>
          <div className="whitespace-pre-wrap break-words">
            {theirRich ? <RichSourceText richText={theirRich} /> : theirText}
          </div>
        </div>
        <div data-testid="conflict-banner-mine"
             className="rounded border border-border bg-background text-foreground p-2">
          <div className="text-xs uppercase tracking-wide text-muted-foreground mb-1">
            My edit
          </div>
          <div className="whitespace-pre-wrap break-words">
            {mineRich ? <RichSourceText richText={mineRich} /> : myEditText}
          </div>
        </div>
      </div>

      <div className="flex flex-col sm:flex-row gap-2 sm:justify-end">
        <Button
          size="sm"
          variant="outline"
          data-testid="conflict-banner-use-theirs"
          onClick={onUseTheirs}
        >
          Use theirs (discard mine)
        </Button>
        <Button
          size="sm"
          data-testid="conflict-banner-use-mine"
          onClick={onUseMine}
        >
          Use mine (overwrite theirs)
        </Button>
      </div>
    </Alert>
  )
}
