import { useState, type ReactNode } from 'react'
import { CircleDashed, ExternalLink, Layers, Save } from 'lucide-react'
import { Combobox } from '@/components/ui/combobox'
import { Button } from '@/components/ui/button'
import { HelpTip } from '@/components/ui/help-tip'
import ConfirmDialog from '../ConfirmDialog'
import { TMProfileNameDialog } from '../tm/TMProfileNameDialog'
import { TextLink } from '@/components/ui/text-link'

/** Dropdown value for the row's own TMs + scoring (no shared profile). */
const OWN = 'custom'

interface Props {
  profiles: { id: number; name: string }[]
  /** The profile the row follows, null for the project-specific setup, or
   *  undefined while neither has been chosen yet (a fresh form). */
  value: number | null | undefined
  disabled?: boolean
  /** Create mode: choices are a draft and there is no detach copy; save-as
   *  builds the profile from the draft rows. */
  isCreate: boolean
  /** What the settings belong to — only changes the wording. */
  entity?: 'project' | 'template'
  onLink: (id: number) => unknown
  /** Edit mode: copy the profile's TMs + scoring onto the row and unlink. */
  onDetach?: () => unknown
  /** Create mode: drop the draft link and go back to the own setup. */
  onClearDraft?: () => unknown
  /** "From scratch" picked while nothing was chosen yet. Nothing is linked or
   *  copied — it only settles the choice, which reveals the setup below. */
  onChooseOwn?: () => unknown
  /** While project-specific: turn the current TMs + scoring into a new profile;
   *  `link` says whether this row should follow it afterwards. */
  onSaveAsProfile?: (name: string, description: string, link: boolean) => Promise<unknown>
  /** The followed profile has no other followers, so the rows + scoring below
   *  edit the profile directly. */
  editableInPlace?: boolean
  /** Rendered under the dropdown while a profile is chosen: the Show/Hide
   *  toggle (on the status line) and the expanded scoring + rows block. */
  profileSettingsToggle?: ReactNode
  profileSettingsBody?: ReactNode
}

/**
 * Where a project's or template's TM list and match scoring come from: one
 * dropdown whose first, visually distinct entry is the project-specific setup
 * ("From scratch"), followed by the shared TM profiles, with "Save as TM profile…"
 * beside it while the own setup is active. Leaving a profile copies its values first,
 * so the project keeps translating the same way.
 *
 * A form that has neither (`value === undefined`) shows an empty dropdown: the
 * question is still open, and the setup below stays hidden until it is answered.
 */
export function TMProfilePicker({
  profiles, value, disabled = false, isCreate, entity = 'project', onLink, onDetach, onClearDraft, onChooseOwn, onSaveAsProfile,
  editableInPlace = false, profileSettingsToggle, profileSettingsBody,
}: Props) {
  const [confirmDetach, setConfirmDetach] = useState(false)
  const [saveAsOpen, setSaveAsOpen] = useState(false)
  const linked = value != null ? profiles.find(p => p.id === value) : undefined
  const noProfiles = profiles.length === 0
  const ownLabel = 'From scratch'

  const handleChange = (next: string) => {
    if (next === OWN) {
      // Nothing was chosen yet: settle on the own setup, no confirm — there is
      // no profile to leave and nothing to copy.
      if (value === undefined) {
        onChooseOwn?.()
        return
      }
      if (value === null) return
      if (isCreate) onClearDraft?.()
      else setConfirmDetach(true)
      return
    }
    const id = Number(next)
    if (!id || id === value) return
    onLink(id)
  }

  const profileLink = value != null && (
    <TextLink to={`/memories/profiles/${value}`} className="inline-flex items-center gap-1" target="_blank" rel="noopener noreferrer">
      {linked?.name ?? `profile #${value}`} <ExternalLink className="size-3" aria-hidden />
    </TextLink>
  )

  return (
    <div data-testid="tm-profile-picker" className="space-y-2">
      <label htmlFor="tm-setup" className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        TM profile
        <HelpTip text={`A shared bundle of translation memories and match scoring. Follow one and every project using it changes together when the profile is edited; "From scratch" keeps a list that belongs to this ${entity} alone.`} />
      </label>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <Combobox
          id="tm-setup"
          value={value === undefined ? '' : value === null ? OWN : String(value)}
          onChange={handleChange}
          placeholder="Choose a TM profile, or start from scratch"
          options={[
            {
              value: OWN,
              label: ownLabel,
              icon: <CircleDashed className="size-3.5 text-gray-500" aria-hidden />,
            },
            ...profiles.map(p => ({ value: String(p.id), label: p.name, icon: <Layers className="size-3.5 text-primary" aria-hidden /> })),
          ]}
          className="w-80"
          contentClassName="w-80"
          disabled={disabled}
          data-testid="tm-profile-select"
        />
        {value === null && onSaveAsProfile && (
          <TextLink className="inline-flex items-center gap-1 text-xs" onClick={() => setSaveAsOpen(true)} disabled={disabled} data-testid="tm-profile-save-as">
            <Save className="size-3.5" aria-hidden /> Save as TM profile…
          </TextLink>
        )}
        {/* Paired with the button it mentions: both appear once the own setup
            is the active choice. */}
        {noProfiles && value === null && (
          <span className="text-xs text-gray-500 dark:text-gray-400">
            No TM profiles yet — “Save as TM profile…” turns this setup into one, or create one on the{' '}
            <TextLink to="/memories/profiles">TM profiles</TextLink> page.
          </span>
        )}
      </div>

      {/* Profile chosen: status line (toggle · link/note · detach), then the expanded block. */}
      {value != null && (
        <div className="space-y-3" data-testid="tm-profile-detail">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-gray-500 dark:text-gray-400" data-testid="tm-profile-linked">
            {profileSettingsToggle}
            {/* Just the link either way; the locked inputs below say "read-only" on
                their own. */}
            {editableInPlace ? <span data-testid="tm-profile-editable">{profileLink}</span> : <span>{profileLink}</span>}
            {!isCreate && onDetach && (
              <Button size="sm" variant="outline" onClick={() => setConfirmDetach(true)} disabled={disabled} data-testid="tm-profile-detach">
                Detach and customise
              </Button>
            )}
          </div>
          {profileSettingsBody}
        </div>
      )}

      <ConfirmDialog
        open={confirmDetach}
        onOpenChange={setConfirmDetach}
        title="Stop following the TM profile?"
        description={`The profile's translation memories and match scoring are copied into this ${entity} so it keeps working the same way. Later changes to the profile will no longer apply here.`}
        actionLabel="Detach and customise"
        variant="default"
        onConfirm={() => { setConfirmDetach(false); onDetach?.() }}
      />
      {onSaveAsProfile && (
        <TMProfileNameDialog
          open={saveAsOpen}
          onClose={() => setSaveAsOpen(false)}
          title="Save as TM profile"
          submitLabel="Save"
          hint={`Creates a shared profile from the translation memories and match scoring ${isCreate ? 'chosen below' : `of this ${entity}`}, so other projects can use the same setup.`}
          linkOption={{ label: `Switch this ${entity} to follow the new profile`, defaultChecked: true }}
          onSubmit={onSaveAsProfile}
        />
      )}
    </div>
  )
}
