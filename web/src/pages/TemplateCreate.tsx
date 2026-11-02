// New template: the setup a project made from the template starts with,
// filled in on one page — the same blocks as the New project form, without its
// draft, its files or a project. Create template saves it and opens it.
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { toast } from 'sonner'
import { listLabels, listLanguages, listMTRouters, listQAProfiles, listTermBases, listTMCollections, listTMProfiles } from '../api'
import { queryKeys } from '../lib/query-keys'
import { toLangResources, toNamedOptions } from '../lib/settings-editor-options'
import { createTemplateFromForm } from '../lib/create-template'
import { useProjectCreateSettings } from '../hooks/useProjectCreateSettings'
import { useFormSegmentation } from '../hooks/useSegmentationProfiles'
import { useScrollToError } from '../hooks/useScrollToError'
import { SettingsEditor } from '../components/SettingsEditor'
import { ProjectCreateWorkflowCard } from '../components/project/ProjectCreateWorkflowCard'
import type { LabelPick } from '../components/project/ProjectLabelFields'
import { TemplateMetadataForm, type TemplateMetadata } from '../components/template/TemplateMetadataForm'
import { PageContainer } from '@/components/layout/PageContainer'
import { PageHeader } from '@/components/layout/PageHeader'
import { TextLink } from '@/components/ui/text-link'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FormField } from '@/components/ui/form-field'
import { errorText } from '@/lib/error-text'

const schema = z.object({ name: z.string().trim().min(1, 'Template name is required') })
type FormValues = z.infer<typeof schema>

export default function TemplateCreate() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { register, handleSubmit, formState: { errors, isSubmitting } } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { name: '' },
  })
  useScrollToError(errors)

  const [metadata, setMetadata] = useState<TemplateMetadata>({ project_name: '', source_language: '', target_languages: [] })
  const [labelPicks, setLabelPicks] = useState<LabelPick[]>([])

  const { data: labels } = useQuery({ queryKey: queryKeys.labels.all, queryFn: listLabels })
  const { data: languages } = useQuery({ queryKey: queryKeys.languages.all, queryFn: listLanguages })
  const { data: tmCollections } = useQuery({ queryKey: queryKeys.tms.all, queryFn: listTMCollections })
  const { data: mtRouters } = useQuery({ queryKey: queryKeys.mtRouters.all, queryFn: listMTRouters })
  const { data: termBases } = useQuery({ queryKey: queryKeys.terminology.all, queryFn: listTermBases })
  const { data: qaProfiles } = useQuery({ queryKey: queryKeys.qaProfiles.all, queryFn: listQAProfiles })
  const { data: tmProfiles } = useQuery({ queryKey: queryKeys.tmProfiles.all, queryFn: listTMProfiles })

  // The settings and workflow state of the New project form. Nothing is
  // prefilled here, so the prefill callbacks have nothing to apply.
  const {
    settings, currentSettings, workflows, workflowId,
    handleWorkflowChange, setWorkflowSteps, handleSettingsChange, workflowInvalid, requireWorkflow,
  } = useProjectCreateSettings({
    ready: tmCollections !== undefined && termBases !== undefined,
    tmCollections,
    termBases,
    applyLanguages: () => {},
    applyMetadata: () => {},
  })

  const { withSegmentation, missingReason } = useFormSegmentation(metadata.source_language)
  const noSegmentationProfile = missingReason(currentSettings().segmentationProfileId)

  const onSubmit = async ({ name }: FormValues) => {
    if (!requireWorkflow({ optional: true })) return
    try {
      const tmpl = await createTemplateFromForm({
        name,
        projectName: metadata.project_name,
        sourceLanguage: metadata.source_language,
        targetLanguages: metadata.target_languages,
        workflowId,
        settings: withSegmentation(currentSettings()),
        labelPicks,
      })
      queryClient.invalidateQueries({ queryKey: queryKeys.templates.all })
      toast.success(`Template “${tmpl.name}” created`)
      navigate(`/templates/${tmpl.id}`)
    } catch (err) {
      toast.error(`Create template failed: ${errorText(err, String(err))}`)
    }
  }

  return (
    <PageContainer>
      <TextLink to="/templates" className="text-sm mb-4 inline-block">
        &larr; Templates
      </TextLink>
      <PageHeader title="New template" />

      <form onSubmit={handleSubmit(onSubmit)} className="space-y-6" data-testid="template-create-form">
        <SettingsEditor
          mode="create"
          entity="template"
          projectFields={
            <div className="space-y-4">
              <FormField label="Template name" error={errors.name?.message} required>
                <Input {...register('name')} placeholder="Template name" autoFocus data-testid="new-template-name" />
              </FormField>
              <TemplateMetadataForm
                value={metadata}
                languages={languages ?? []}
                onChange={patch => setMetadata(m => ({ ...m, ...patch }))}
                labels={labels ?? []}
                labelPicks={labelPicks}
                onLabelsChange={setLabelPicks}
              />
            </div>
          }
          afterProjectSettings={({ mtRouterName, mtOnUpload }) => (
            <ProjectCreateWorkflowCard
              workflows={workflows ?? []}
              workflowId={workflowId}
              steps={settings.workflowSteps}
              onPickWorkflow={handleWorkflowChange}
              targetLanguages={metadata.target_languages}
              onStepsChange={setWorkflowSteps}
              mtRouterName={mtRouterName}
              mtOnUpload={mtOnUpload}
              invalid={workflowInvalid}
            />
          )}
          values={settings}
          tmCollections={toLangResources(tmCollections)}
          mtRouters={mtRouters ?? []}
          termBases={toLangResources(termBases)}
          qaProfiles={toNamedOptions(qaProfiles)}
          tmProfiles={toNamedOptions(tmProfiles)}
          languages={languages}
          sourceLanguage={metadata.source_language}
          targetLanguages={metadata.target_languages}
          onChange={handleSettingsChange}
        />

        <div className="flex justify-end">
          <Button type="submit" disabled={isSubmitting || !!noSegmentationProfile}
            disabledReason={isSubmitting ? undefined : noSegmentationProfile} data-guide="template_create.submit">
            {isSubmitting ? 'Creating...' : 'Create template'}
          </Button>
        </div>
      </form>
    </PageContainer>
  )
}
