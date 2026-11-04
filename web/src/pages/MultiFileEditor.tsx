import { useParams, useSearchParams } from 'react-router-dom'
import { MultiFileWorkspace } from '@/components/editor/MultiFileWorkspace'
import { EmptyState } from '@/components/layout/EmptyState'
import { PageContainer } from '@/components/layout/PageContainer'
import { parseScopeJobs } from '@/lib/editor-link'

/** The CAT editor route for several files of a project: `?jobs=1,2,3`. */
export default function MultiFileEditor() {
  const { projectId } = useParams<{ projectId: string }>()
  const [params] = useSearchParams()
  const jobIds = parseScopeJobs(params.get('jobs'))
  if (!jobIds) {
    return (
      <PageContainer>
        <EmptyState message="This link names no files to open." />
      </PageContainer>
    )
  }
  // Keyed on the files, so another set mounts a fresh editor.
  return <MultiFileWorkspace key={jobIds.join(',')} projectId={Number(projectId)} jobIds={jobIds} />
}
