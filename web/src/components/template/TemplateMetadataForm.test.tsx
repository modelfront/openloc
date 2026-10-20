import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TemplateMetadataForm } from './TemplateMetadataForm'
import type { Language } from '../../api'

// The pickers are cmdk comboboxes (hostile in jsdom); plain inputs standing in
// for them are enough to drive the per-field saves.
// Customer and Domain are built-in labels, rendered by ProjectLabelFields
// and covered by its own tests. Stubbed so these stay about the form's own
// fields — project name and languages.
vi.mock('../project/ProjectLabelFields', () => ({
  ProjectLabelFields: () => <div data-testid="label-fields-stub" />,
}))
vi.mock('@/components/ui/combobox', () => ({
  Combobox: ({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) => (
    <input aria-label={placeholder} value={value} onChange={e => onChange(e.target.value)} />
  ),
  MultiCombobox: ({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) => (
    <input aria-label={placeholder} value={(value ?? []).join(',')} onChange={e => onChange(e.target.value ? e.target.value.split(',') : [])} />
  ),
}))

const stored = { project_name: 'Summer campaign', source_language: 'en', target_languages: ['de', 'fr'] }
const labelProps = { labels: [], labelPicks: [], onLabelsChange: vi.fn() }
const languages: Language[] = [{ code: 'en', name: 'English' }, { code: 'de', name: 'German' }, { code: 'fr', name: 'French' }]

describe('TemplateMetadataForm', () => {
  it('shows the stored values', () => {
    render(<TemplateMetadataForm value={stored} languages={languages} onChange={vi.fn()} {...labelProps} />)
    expect(screen.getByTestId('template-project-name-edit')).toHaveValue('Summer campaign')
    expect(screen.getByLabelText('Source language')).toHaveValue('en')
    expect(screen.getByLabelText('Add target languages...')).toHaveValue('de,fr')
    expect(screen.getByTestId('label-fields-stub')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /save/i })).not.toBeInTheDocument()
  })

  it('a text field saves once, trimmed, when it loses focus', async () => {
    const onChange = vi.fn()
    render(<TemplateMetadataForm value={stored} languages={languages} onChange={onChange} {...labelProps} />)
    const name = screen.getByTestId('template-project-name-edit')
    await userEvent.clear(name)
    await userEvent.type(name, '  Winter campaign ')
    expect(onChange).not.toHaveBeenCalled()
    await userEvent.tab()
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange).toHaveBeenCalledWith({ project_name: 'Winter campaign' })
  })

  it('an unchanged text field does not save on blur', async () => {
    const onChange = vi.fn()
    render(<TemplateMetadataForm value={stored} languages={languages} onChange={onChange} {...labelProps} />)
    await userEvent.click(screen.getByTestId('template-project-name-edit'))
    await userEvent.tab()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('pickers save on pick', async () => {
    const onChange = vi.fn()
    render(<TemplateMetadataForm value={stored} languages={languages} onChange={onChange} {...labelProps} />)
    // The stand-in is controlled by the stored value, so one change event
    // stands for one pick in the real multi-combobox.
    fireEvent.change(screen.getByLabelText('Add target languages...'), { target: { value: 'it' } })
    expect(onChange).toHaveBeenLastCalledWith({ target_languages: ['it'] })
    fireEvent.change(screen.getByLabelText('Source language'), { target: { value: 'de' } })
    expect(onChange).toHaveBeenLastCalledWith({ source_language: 'de' })
  })
})
