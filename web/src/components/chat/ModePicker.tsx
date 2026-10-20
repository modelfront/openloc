import { ChevronDown, Eye, Pencil } from 'lucide-react'
import { cn } from '@/lib/utils'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

// Mode picker: read-only (default) vs allow-write, styled like its
// ModelPicker neighbor. Write mode is amber wherever it is on, so the user can
// always tell at a glance that the assistant may write.
export function ModePicker({ allowWrites, onChange, disabled }: { allowWrites: boolean; onChange: (write: boolean) => void; disabled?: boolean }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        data-testid="chat-mode-picker"
        disabled={disabled}
        className={cn(
          'inline-flex h-7 shrink-0 items-center gap-1 rounded-md px-1.5 text-xs transition-colors',
          allowWrites
            ? 'text-amber-600 hover:bg-amber-50 dark:text-amber-500 dark:hover:bg-amber-950/40'
            : 'text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200',
        )}
        title={allowWrites ? 'The assistant may make changes; deletes ask you first' : 'The assistant can only read'}
      >
        {allowWrites ? <Pencil className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
        <span>{allowWrites ? 'Allow write' : 'Read-only'}</span>
        <ChevronDown className="h-3 w-3 opacity-60" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuItem data-testid="chat-mode-read" onClick={() => onChange(false)} description="Answers questions; never changes data">
          <Eye className="h-4 w-4 shrink-0" />
          <span className="flex-1">Read-only</span>
          {!allowWrites && <span className="text-[10px] font-normal text-primary">current</span>}
        </DropdownMenuItem>
        <DropdownMenuItem data-testid="chat-mode-write" onClick={() => onChange(true)} description="May make changes; deletes ask you first">
          <Pencil className="h-4 w-4 shrink-0" />
          <span className="flex-1">Allow write</span>
          {allowWrites && <span className="text-[10px] font-normal text-primary">current</span>}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
