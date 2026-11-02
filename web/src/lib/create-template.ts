import { createTemplate, setTemplateLabels, type Template } from '../api'
import type { SettingsValues } from '../components/SettingsEditor'
import type { LabelPick } from '../components/project/ProjectLabelFields'
import { buildProjectSetupPayload } from './project-create-payload'

export interface TemplateFormInput {
  name: string
  /** The name a project made from the template starts with. */
  projectName: string
  sourceLanguage: string
  targetLanguages: string[]
  /** '' when the steps follow no library workflow. */
  workflowId: string
  settings: SettingsValues
  labelPicks: LabelPick[]
}

/**
 * Creates a template from a filled-in setup form: the New template page and
 * Save as template on the New project page. Labels have their own endpoint, so
 * the picks are saved onto the new template after it exists.
 */
export async function createTemplateFromForm(input: TemplateFormInput): Promise<Template> {
  const tmpl = await createTemplate({
    name: input.name.trim(),
    project_name: input.projectName.trim(),
    source_language: input.sourceLanguage,
    target_languages: input.targetLanguages,
    ...buildProjectSetupPayload(input.workflowId, input.settings),
  })
  const picks = input.labelPicks
    .filter(p => p.value_id != null)
    .map(p => ({ label_definition_id: p.definition_id, label_value_id: p.value_id! }))
  if (picks.length > 0) await setTemplateLabels(tmpl.id, picks)
  return tmpl
}
