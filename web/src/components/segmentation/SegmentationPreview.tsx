import { Link2, Scissors } from 'lucide-react'
import type { SegmentationPreview as Preview, SegmentationRule } from '@/api'
import { Textarea } from '@/components/ui/textarea'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { decisionLabel, MAX_PREVIEW_TEXT } from '@/lib/segmentation'
import { cn } from '@/lib/utils'

export const SAMPLE_TEXT =
  'Dr. Smith arrived at 5 p.m. and met Mr. Jones. See Fig. 3 for details, e.g. the totals! Revised: the new specs are ready.'

/**
 * Sample text split with the rules as they are on screen, by the server's
 * engine. With `showDecisions` the text shows a marker at every decided
 * position: scissors where it breaks, a link where it does not; hovering one
 * names the rule or word list. Paragraph mode decides nothing, so it shows
 * only the segments.
 */
export function SegmentationPreview({ text, onTextChange, preview, rules, error, loading, showDecisions = true }: {
  text: string
  onTextChange: (text: string) => void
  preview: Preview | undefined
  rules: SegmentationRule[]
  error?: string
  loading: boolean
  showDecisions?: boolean
}) {
  return (
    <div className="flex flex-col gap-3" data-guide="segmentation.preview" data-testid="segmentation-preview">
      <Textarea value={text} onChange={e => onTextChange(e.target.value)} rows={5} maxLength={MAX_PREVIEW_TEXT} aria-label="Sample text"
        placeholder="Type or paste sample text" data-testid="segmentation-preview-text" />
      {error ? (
        <p className="text-sm text-destructive" role="alert" data-testid="segmentation-preview-error">{error}</p>
      ) : preview && (
        <div className={loading ? 'opacity-60' : undefined}>
          {showDecisions && (
            <>
              <p className="mb-1 text-xs font-medium text-muted-foreground">Breaks and exceptions</p>
              <p className="whitespace-pre-wrap rounded-lg bg-muted p-2 text-sm leading-6" data-testid="segmentation-preview-marked">
                {preview.pieces.map((piece, i) => (
                  <span key={i}>
                    {piece.text}
                    {piece.decision && (
                      <Tooltip>
                        <TooltipTrigger render={
                          <span tabIndex={0} role="img" aria-label={decisionLabel(piece.decision, rules)}
                            className={`mx-0.5 inline-flex align-middle ${piece.decision.break ? 'text-primary' : 'text-muted-foreground'}`}
                            data-testid={piece.decision.break ? 'segmentation-preview-break' : 'segmentation-preview-nobreak'} />
                        }>
                          {piece.decision.break ? <Scissors className="size-3.5" /> : <Link2 className="size-3.5" />}
                        </TooltipTrigger>
                        <TooltipContent className="max-w-xs">{decisionLabel(piece.decision, rules)}</TooltipContent>
                      </Tooltip>
                    )}
                  </span>
                ))}
              </p>
            </>
          )}
          <p className={cn('mb-1 text-xs font-medium text-muted-foreground', showDecisions && 'mt-3')}>
            {preview.segments.length === 1 ? '1 segment' : `${preview.segments.length} segments`}
          </p>
          <ol className="flex flex-col gap-1 text-sm" data-testid="segmentation-preview-segments">
            {preview.segments.map((s, i) => (
              <li key={i} className="flex gap-2 rounded-md border border-border px-2 py-1">
                <span className="tabular-nums text-muted-foreground">{i + 1}</span>
                <span className="min-w-0 break-words">{s}</span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  )
}
