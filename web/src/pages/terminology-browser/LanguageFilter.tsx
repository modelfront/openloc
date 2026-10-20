import { Globe } from 'lucide-react'

import { MultiCombobox } from '@/components/ui/combobox'
import { toLangOptions } from '@/components/lang-options'

// LanguageFilter: multi-select language filter for the concept browser and the
// concept editor dialog. Empty selection = all languages, so the pinned "All
// languages" action is a reset. `MultiCombobox` keeps the popover open while
// toggling, which is what a filter wants.
export function LanguageFilter({
  languages,
  selected,
  onChange,
  nameOf,
}: {
  languages: string[]
  selected: string[]
  onChange: (next: string[]) => void
  nameOf: (code: string) => string
}) {
  if (languages.length === 0) return null

  return (
    <MultiCombobox
      value={selected}
      onChange={onChange}
      options={toLangOptions(languages.map((code) => ({ code, name: nameOf(code) })))}
      actions={[
        {
          key: 'all',
          label: 'All languages',
          icon: <Globe className="size-3.5" aria-hidden />,
          onSelect: () => onChange([]),
        },
      ]}
      placeholder="All languages"
      searchPlaceholder="Search languages..."
      emptyText="No matching languages"
      aria-label="Filter by language"
      className="w-64"
      data-testid="lang"
    />
  )
}
