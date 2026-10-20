import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'

/** A small "?" badge that shows `text` in a tooltip on hover or focus. */
export function HelpTip({ text }: { text: string }) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={<span className="inline-flex items-center justify-center w-4 h-4 rounded-full bg-gray-200 dark:bg-gray-700 text-gray-500 dark:text-gray-400 text-[10px] font-bold cursor-help ml-1" tabIndex={0} />}
      >
        ?
      </TooltipTrigger>
      <TooltipContent className="max-w-64 text-xs leading-relaxed">{text}</TooltipContent>
    </Tooltip>
  )
}
