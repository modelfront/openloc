import { useEffect, useState } from 'react'
import { Download } from 'lucide-react'
import { toast } from 'sonner'
import { fetchIssueAttachment, type IssueAttachment, type IssueScope } from '@/api'
import { triggerDownload } from '@/api/download'
import { Button } from '@/components/ui/button'
import { formatFileSize } from '@/lib/file-size'

/**
 * The files of an issue or a comment: images as thumbnails that open full size,
 * every other file as a download. Files need the session, so they are fetched
 * with auth.
 */
export function IssueAttachments({ scope, issueId, files }: { scope: IssueScope; issueId: number; files: IssueAttachment[] }) {
  if (files.length === 0) return null
  const images = files.filter(f => f.is_image)
  const others = files.filter(f => !f.is_image)
  return (
    <div className="space-y-2" data-testid="issue-attachments">
      {images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {images.map(f => <IssueImage key={f.id} scope={scope} issueId={issueId} file={f} />)}
        </div>
      )}
      {others.length > 0 && (
        <ul className="flex flex-wrap gap-1.5">
          {others.map(f => <li key={f.id}><IssueFileDownload scope={scope} issueId={issueId} file={f} /></li>)}
        </ul>
      )}
    </div>
  )
}

export function IssueImage({ scope, issueId, file }: { scope: IssueScope; issueId: number; file: IssueAttachment }) {
  const [url, setUrl] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let objectUrl: string | null = null
    let cancelled = false
    fetchIssueAttachment(scope, issueId, file.id)
      .then(blob => {
        if (cancelled) return
        objectUrl = URL.createObjectURL(blob)
        setUrl(objectUrl)
      })
      .catch(() => { if (!cancelled) setFailed(true) })
    return () => {
      cancelled = true
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [scope, issueId, file.id])

  if (failed) return <p className="text-xs text-muted-foreground">{file.filename} could not be loaded.</p>
  if (!url) return <div className="h-24 w-40 animate-pulse rounded border border-border bg-muted" />
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" className="inline-block" title={file.filename} data-testid="issue-image">
      <img src={url} alt={file.filename} className="max-h-48 max-w-full rounded border border-border" />
    </a>
  )
}

function IssueFileDownload({ scope, issueId, file }: { scope: IssueScope; issueId: number; file: IssueAttachment }) {
  const [busy, setBusy] = useState(false)
  const download = () => {
    setBusy(true)
    fetchIssueAttachment(scope, issueId, file.id)
      .then(blob => triggerDownload(blob, file.filename))
      .catch((err: Error) => toast.error(err.message))
      .finally(() => setBusy(false))
  }
  return (
    <Button variant="outline" size="sm" onClick={download} loading={busy} title={`Download ${file.filename}`} data-testid="issue-file">
      <Download />
      <span className="max-w-56 truncate">{file.filename}</span>
      <span className="text-muted-foreground">{formatFileSize(file.size_bytes)}</span>
    </Button>
  )
}
