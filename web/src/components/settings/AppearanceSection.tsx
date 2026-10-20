import { useState } from 'react'
import { useTheme } from 'next-themes'
import { Sun, Moon, Monitor, Plus, Pencil, Check, Copy } from 'lucide-react'
import { toast } from 'sonner'
import { Button } from '../ui/button'
import { DeleteButton } from '../ui/delete-button'
import { ChoiceToggle, type ChoiceToggleOption } from '../ui/choice-toggle'
import { useAppearance } from '../../hooks/useAppearance'
import {
  palettesForMode,
  DEFAULT_DARK_ID,
  DEFAULT_LIGHT_ID,
  type Palette,
  type PaletteMode,
} from '../../lib/palettes'
import { SettingsCard } from '../layout/SettingsCard'
import { PalettePreview } from './PalettePreview'
import { PaletteEditorDialog, type EditorTarget } from './PaletteEditorDialog'

const MODES: ChoiceToggleOption<'light' | 'dark' | 'system'>[] = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
]

export function AppearanceSection() {
  const { theme, setTheme } = useTheme()
  const { appearance, save } = useAppearance()
  const [editor, setEditor] = useState<EditorTarget>(null)

  const selectPalette = (mode: PaletteMode, id: string) =>
    save({
      ...appearance,
      [mode === 'dark' ? 'dark_palette' : 'light_palette']: id,
    })

  const savePalette = (p: Palette) => {
    const exists = appearance.custom_palettes.some(c => c.id === p.id)
    const custom_palettes = exists
      ? appearance.custom_palettes.map(c => (c.id === p.id ? p : c))
      : [...appearance.custom_palettes, p]
    // Newly created palettes also become the active one for their mode.
    const activeKey = p.mode === 'dark' ? 'dark_palette' : 'light_palette'
    save({ ...appearance, custom_palettes, [activeKey]: exists ? appearance[activeKey] : p.id })
    setEditor(null)
    toast.success(exists ? 'Palette updated' : 'Palette created')
  }

  const deletePalette = (p: Palette) => {
    const custom_palettes = appearance.custom_palettes.filter(c => c.id !== p.id)
    // If the deleted palette was active, fall back to the mode's default.
    const next = { ...appearance, custom_palettes }
    if (appearance.dark_palette === p.id) next.dark_palette = DEFAULT_DARK_ID
    if (appearance.light_palette === p.id) next.light_palette = DEFAULT_LIGHT_ID
    save(next)
    toast.success('Palette deleted')
  }

  return (
    <SettingsCard
      title="Appearance"
      className="mb-6"
      description="Your palette choices are saved to your account and follow you across organizations."
    >

      <div className="mb-6">
        <ChoiceToggle
          aria-label="Theme"
          options={MODES}
          value={MODES.find(m => m.value === theme)?.value ?? null}
          onChange={setTheme}
        />
      </div>

      <PaletteGroup
        mode="dark"
        title="Dark palette"
        selectedId={appearance.dark_palette}
        palettes={palettesForMode('dark', appearance.custom_palettes)}
        onSelect={id => selectPalette('dark', id)}
        onCreate={() => setEditor({ kind: 'new', mode: 'dark' })}
        onClone={p => setEditor({ kind: 'new', mode: p.mode, seed: p })}
        onEdit={p => setEditor({ kind: 'edit', palette: p })}
        onDelete={deletePalette}
      />

      <div className="h-6" />

      <PaletteGroup
        mode="light"
        title="Light palette"
        selectedId={appearance.light_palette}
        palettes={palettesForMode('light', appearance.custom_palettes)}
        onSelect={id => selectPalette('light', id)}
        onCreate={() => setEditor({ kind: 'new', mode: 'light' })}
        onClone={p => setEditor({ kind: 'new', mode: p.mode, seed: p })}
        onEdit={p => setEditor({ kind: 'edit', palette: p })}
        onDelete={deletePalette}
      />

      <PaletteEditorDialog target={editor} onClose={() => setEditor(null)} onSave={savePalette} />
    </SettingsCard>
  )
}

interface GroupProps {
  mode: PaletteMode
  title: string
  selectedId: string
  palettes: Palette[]
  onSelect: (id: string) => void
  onCreate: () => void
  onClone: (p: Palette) => void
  onEdit: (p: Palette) => void
  onDelete: (p: Palette) => void
}

function PaletteGroup({ title, selectedId, palettes, onSelect, onCreate, onClone, onEdit, onDelete }: GroupProps) {
  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-medium text-gray-700 dark:text-gray-300">{title}</h3>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onCreate}
          startIcon={<Plus />}
          className="text-xs text-gray-500 dark:text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
        >
          New palette
        </Button>
      </div>
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
        {palettes.map(p => {
          const selected = p.id === selectedId
          return (
            <div
              key={p.id}
              className={`group relative rounded-lg border p-2 cursor-pointer transition-colors ${
                selected
                  ? 'border-primary ring-1 ring-ring'
                  : 'border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600'
              }`}
              onClick={() => onSelect(p.id)}
              data-testid={`palette-${p.id}`}
              data-selected={selected}
            >
              <PalettePreview anchors={p.anchors} className="h-14" />
              <div className="mt-1.5 flex items-center gap-1">
                {selected && <Check className="w-3.5 h-3.5 text-primary shrink-0" />}
                <span className="text-xs font-medium text-gray-900 dark:text-gray-100 truncate">{p.name}</span>
                {/* Clone works on any palette (incl. built-ins) — fork it into an
                    editable custom copy. Only real customs get Edit / Delete. */}
                <span className="ml-auto flex items-center gap-0.5">
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    onClick={e => { e.stopPropagation(); onClone(p) }}
                    className="text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
                    aria-label={`Clone ${p.name}`}
                  >
                    <Copy className="w-3 h-3" />
                  </Button>
                  {!p.builtin && (
                    <>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={e => { e.stopPropagation(); onEdit(p) }}
                        className="text-gray-400 hover:text-gray-900 dark:hover:text-gray-100"
                        aria-label={`Edit ${p.name}`}
                      >
                        <Pencil className="w-3 h-3" />
                      </Button>
                      <DeleteButton
                        size="icon-xs"
                        onClick={e => { e.stopPropagation(); onDelete(p) }}
                        aria-label={`Delete ${p.name}`}
                      />
                    </>
                  )}
                </span>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
