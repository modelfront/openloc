import { FormField } from '@/components/ui/form-field'
import { BlurCommitInput } from '@/components/ui/blur-commit-input'
import { ProjectLabelFields, type LabelPick } from '../project/ProjectLabelFields'
import { Combobox, MultiCombobox } from '@/components/ui/combobox'
import { langPickerProps, toLangOptions } from '../lang-options'
import type { Label, Language } from '../../api'

export interface TemplateMetadata {
  project_name: string
  /** '' = none. */
  source_language: string
  target_languages: string[]
}

interface Props {
  /** The stored values. */
  value: TemplateMetadata
  languages: Language[]
  /** One field changed: text fields on blur, pickers on pick. */
  onChange: (patch: Partial<TemplateMetadata>) => void
  /** Every label the tenant has defined, for the preset pickers. */
  labels: Label[]
  /** The template's current presets, and the setter that saves them. */
  labelPicks: LabelPick[]
  onLabelsChange: (next: LabelPick[]) => void
  disabled?: boolean
}

/**
 * What a project made from the template starts with: project name, languages,
 * domain and customer. Sits at the top of the Project settings card, like the
 * same fields on the New project page, and saves each field as it changes,
 * like the rest of the template page.
 */
export function TemplateMetadataForm({ value, languages, onChange, labels, labelPicks, onLabelsChange, disabled }: Props) {
  return (
    <div className="space-y-4" data-testid="template-metadata-form">
      <FormField label="Project name">
        <BlurCommitInput
          value={value.project_name}
          onCommit={project_name => onChange({ project_name })}
          placeholder="Project name"
          maxLength={200}
          disabled={disabled}
          data-testid="template-project-name-edit"
        />
      </FormField>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <FormField label="Source language">
          <div data-field="source_language">
            <Combobox options={toLangOptions(languages)} value={value.source_language} onChange={source_language => onChange({ source_language })} placeholder="Source language" className="w-full" disabled={disabled} {...langPickerProps} />
          </div>
        </FormField>
        <FormField label="Target languages">
          <div data-field="target_languages">
            <MultiCombobox options={toLangOptions(languages)} value={value.target_languages} onChange={target_languages => onChange({ target_languages })} placeholder="Add target languages..." className="w-full" disabled={disabled} {...langPickerProps} />
          </div>
        </FormField>
      </div>
      {/* Customer and Domain are built-in labels, so the template presets them
          the same way a project picks them. Cells of one grid, so they line up
          with the fields above. */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <ProjectLabelFields
          labels={labels}
          value={labelPicks}
          onChange={onLabelsChange}
          disabled={disabled}
        />
      </div>
    </div>
  )
}
