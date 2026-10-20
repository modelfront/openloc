import { Copy } from 'lucide-react'
import { Markdown } from '@/components/markdown/Markdown'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

export type CodeSample = { id: string; label: string; language: string; code: string }

// A card of code samples, one tab each, with a copy button for the open one.
export function CodePanel({ title, samples, testId }: { title: string; samples: CodeSample[]; testId?: string }) {
  if (samples.length === 0) return null
  return (
    <section className="rounded-lg border border-gray-200 bg-card dark:border-gray-800" data-testid={testId}>
      <Tabs defaultValue={samples[0].id}>
        <div className="flex items-center justify-between gap-2 border-b border-gray-200 px-3 py-2 dark:border-gray-800">
          <span className="text-xs font-medium text-gray-500 dark:text-gray-400">{title}</span>
          <TabsList variant="line">
            {samples.map(s => (
              <TabsTrigger key={s.id} value={s.id}>
                {s.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
        {samples.map(s => (
          <TabsContent key={s.id} value={s.id} className="relative px-3 pb-1">
            <CopyLinkButton
              url={s.code}
              icon={Copy}
              iconOnly
              label="Copy"
              copiedLabel="Copied"
              toastMessage="Copied"
              errorMessage="Could not copy the code"
              className="absolute right-4 top-4 z-10"
            />
            <Markdown variant="docs">{'```' + s.language + '\n' + s.code + '\n```'}</Markdown>
          </TabsContent>
        ))}
      </Tabs>
    </section>
  )
}
