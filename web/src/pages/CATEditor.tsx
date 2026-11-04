import { useParams } from 'react-router-dom'
import { EditorWorkspace } from '@/components/editor/EditorWorkspace'

/** The CAT editor route: one job of a project, by the ids in the URL. */
export default function CATEditor() {
  const { projectId, jobId } = useParams<{ projectId: string; jobId: string }>()
  return <EditorWorkspace projectId={Number(projectId)} jobId={Number(jobId)} />
}
