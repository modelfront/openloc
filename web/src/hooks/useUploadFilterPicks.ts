import { useState } from 'react'

import type { FilterProfile, FilterProfileChoice } from '@/api'
import { defaultFilterProfile, useFilterProfiles } from '@/hooks/useFilterProfiles'
import { useOnChange } from '@/hooks/useOnChange'
import { filterFormatOfFilename, type FilterFormat } from '@/lib/filter-formats'

export interface UploadFilterPick {
  file: File
  /** Stable per file of the batch, for keys and test ids. */
  key: string
  format: FilterFormat
  /** The format's profiles, plus the project's pick when the list lacks it. */
  profiles: Pick<FilterProfile, 'id' | 'name'>[]
  /** What the file is parsed with when left alone: the project's profile for
   *  the format, else the format's Default. Undefined while profiles load. */
  preselected: number | undefined
  value: number | undefined
  setValue: (id: number) => void
}

/** The profile a file of `format` starts from: the project's choice, else the Default. */
export function preselectedFilterProfile(
  profiles: FilterProfile[],
  choice: FilterProfileChoice | undefined,
): number | undefined {
  return choice?.id ?? defaultFilterProfile(profiles)?.id
}

/** The picks an upload sends: only those that differ from the preselection. */
export function filterProfileOverrides(picks: UploadFilterPick[]): Map<File, number> {
  const out = new Map<File, number>()
  for (const p of picks) {
    if (p.value !== undefined && p.value !== p.preselected) out.set(p.file, p.value)
  }
  return out
}

/**
 * The filter profile of each file of an upload batch that has a filter
 * format, starting from the project's `choices`. `overrides` is what the
 * upload queue takes as `filterProfiles`.
 */
export function useUploadFilterPicks(files: File[], choices: FilterProfileChoice[] | null | undefined, enabled: boolean) {
  const { byFormat } = useFilterProfiles()
  const keys = files.map((f, i) => `${i}-${f.name}-${f.size}-${f.lastModified}`)
  const [edits, setEdits] = useState<Record<string, number>>({})
  // Each opening starts from the project's choices. Keyed on the files' names,
  // not the array: a closed dialog passes a fresh empty one on every render.
  useOnChange([keys.join('|'), enabled], () => setEdits({}))

  const picks: UploadFilterPick[] = []
  files.forEach((file, i) => {
    const format = filterFormatOfFilename(file.name)
    if (!format) return
    const key = keys[i]
    const choice = choices?.find(c => c.format === format)
    const listed = byFormat[format]
    const profiles = choice && !listed.some(p => p.id === choice.id) ? [...listed, choice] : listed
    const preselected = preselectedFilterProfile(listed, choice)
    picks.push({
      file,
      key,
      format,
      profiles,
      preselected,
      value: edits[key] ?? preselected,
      setValue: id => setEdits(prev => ({ ...prev, [key]: id })),
    })
  })

  const overrides = () => filterProfileOverrides(picks)
  /** The override of one file, for reading an .xlsx with it. */
  const overrideOf = (file: File) => overrides().get(file)

  return { picks, overrides, overrideOf }
}
