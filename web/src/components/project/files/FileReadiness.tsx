// A file's delivery readiness: one status chip and how many of its target
// languages are done. Hover lists the languages by state.

import { FILE_READINESS, FILE_READINESS_DISPLAY, type FileSummary } from '@/lib/file-readiness'
import { Chip } from '@/components/ui/chip'
import { LanguageCode } from '@/components/ui/language-code'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

export function FileReadiness({ file }: { file: FileSummary }) {
  const { label, color } = FILE_READINESS_DISPLAY[file.readiness]
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span className="inline-flex min-w-0 items-center gap-2" data-testid={`file-readiness-${file.fileId}`}>
            <Chip
              color={color}
              className={file.readiness === 'processing' ? 'animate-pulse' : undefined}
              data-state={file.readiness}
            >
              {label}
            </Chip>
            <span className="truncate text-xs text-gray-500 tabular-nums dark:text-gray-400">
              {file.readyCount} of {file.languageCount} ready
            </span>
          </span>
        }
      />
      <TooltipContent className="max-w-sm">
        <span className="flex flex-col gap-1" data-testid={`file-readiness-detail-${file.fileId}`}>
          <span>{FILE_READINESS_DISPLAY[file.readiness].hint}.</span>
          {FILE_READINESS.filter(r => file.languages[r].length > 0).map(r => (
            <span key={r} className="flex flex-wrap items-baseline gap-x-1.5">
              <span className="opacity-70">{FILE_READINESS_DISPLAY[r].label}:</span>
              {file.languages[r].map(code => <LanguageCode key={code} code={code} />)}
            </span>
          ))}
        </span>
      </TooltipContent>
    </Tooltip>
  )
}
