import { useState } from 'react'
import { useOnChange } from '@/hooks/useOnChange'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { ChoiceToggle, type ChoiceToggleOption } from '@/components/ui/choice-toggle'
import {
  ANCHOR_ROLES,
  BUILTIN_PALETTES,
  DEFAULT_DARK_ID,
  DEFAULT_LIGHT_ID,
  type Palette,
  type PaletteAnchors,
  type PaletteMode,
} from '../../lib/palettes'
import { PalettePreview } from './PalettePreview'

const HEX_RE = /^#[0-9a-fA-F]{6}$/

const MODE_OPTIONS: ChoiceToggleOption<PaletteMode>[] = [
  { value: 'light', label: 'Light' },
  { value: 'dark', label: 'Dark' },
]

/** What the editor was opened for: a new palette in a mode (optionally seeded
 *  from an existing one — "clone to modify"), or editing an existing custom. */
export type EditorTarget =
  | { kind: 'new'; mode: PaletteMode; seed?: Palette }
  | { kind: 'edit'; palette: Palette }
  | null

function seedAnchors(mode: PaletteMode): PaletteAnchors {
  const base = BUILTIN_PALETTES.find(p => p.id === (mode === 'dark' ? DEFAULT_DARK_ID : DEFAULT_LIGHT_ID))!
  return { ...base.anchors }
}

interface Props {
  target: EditorTarget
  onClose: () => void
  onSave: (p: Palette) => void
}

/** The form's starting fields for a target; a closed dialog starts blank, dark. */
function fieldsFor(target: Props['target']): { name: string; mode: PaletteMode; anchors: PaletteAnchors } {
  if (!target) return { name: '', mode: 'dark', anchors: seedAnchors('dark') }
  if (target.kind === 'edit') {
    return { name: target.palette.name, mode: target.palette.mode, anchors: { ...target.palette.anchors } }
  }
  if (target.seed) {
    // Clone: pre-fill from the source palette's colors as a new custom.
    return { name: `${target.seed.name} (copy)`, mode: target.seed.mode, anchors: { ...target.seed.anchors } }
  }
  return { name: '', mode: target.mode, anchors: seedAnchors(target.mode) }
}

/**
 * Create/edit a custom palette by picking its 7 key colors. Everything
 * else (the full gray ramp + shadcn tokens) is derived from these. A live
 * preview shows the result before saving.
 */
export function PaletteEditorDialog({ target, onClose, onSave }: Props) {
  const initial = fieldsFor(target)
  const [name, setName] = useState(initial.name)
  const [mode, setMode] = useState<PaletteMode>(initial.mode)
  const [anchors, setAnchors] = useState<PaletteAnchors>(initial.anchors)

  useOnChange([target], () => {
    if (!target) return
    const f = fieldsFor(target)
    setName(f.name)
    setMode(f.mode)
    setAnchors(f.anchors)
  })

  const setAnchor = (key: keyof PaletteAnchors, value: string) =>
    setAnchors(a => ({ ...a, [key]: value }))

  const switchMode = (m: PaletteMode) => { setMode(m); setAnchors(seedAnchors(m)) }

  const valid =
    name.trim().length > 0 &&
    ANCHOR_ROLES.every(r => HEX_RE.test(anchors[r.key]))

  const submit = () => {
    if (!valid || !target) return
    const id = target.kind === 'edit' ? target.palette.id : `custom-${crypto.randomUUID()}`
    onSave({ id, name: name.trim(), mode, anchors })
  }

  const editing = target?.kind === 'edit'

  return (
    <Dialog open={!!target} onOpenChange={v => { if (!v) onClose() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{editing ? 'Edit palette' : 'Create palette'}</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="grid grid-cols-[1fr_auto] gap-3 items-end">
            <FormField label="Name" required>
              <Input value={name} onChange={e => setName(e.target.value)} placeholder="My palette" maxLength={40} autoFocus />
            </FormField>
            {/* Centred on the Name input's line. */}
            <div className="flex h-input items-center">
              <ChoiceToggle size="sm" aria-label="Mode" options={MODE_OPTIONS} value={mode} onChange={switchMode} />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-x-4 gap-y-3">
            {ANCHOR_ROLES.map(role => (
              <div key={role.key} className="flex items-center gap-2.5">
                <input
                  type="color"
                  value={anchors[role.key]}
                  onChange={e => setAnchor(role.key, e.target.value)}
                  aria-label={role.label}
                  className="h-8 w-8 shrink-0 cursor-pointer rounded-md border border-gray-200 dark:border-gray-700 bg-transparent p-0.5"
                />
                <div className="min-w-0 flex-1">
                  <div className="text-sm text-gray-900 dark:text-gray-100 leading-tight">{role.label}</div>
                  <Input
                    variant="seamless"
                    value={anchors[role.key]}
                    onChange={e => setAnchor(role.key, e.target.value)}
                    data-testid={`palette-hex-${role.key}`}
                    className="font-mono text-xs text-gray-500 dark:text-gray-400 uppercase"
                    spellCheck={false}
                  />
                </div>
              </div>
            ))}
          </div>

          <div>
            <div className="text-xs text-gray-500 dark:text-gray-400 mb-1.5">Preview</div>
            <PalettePreview anchors={anchors} className="h-24" />
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="button" onClick={submit} disabled={!valid}>
              {editing ? 'Save' : 'Create'}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}
