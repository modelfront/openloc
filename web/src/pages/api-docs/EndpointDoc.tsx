import { Copy, KeyRound } from 'lucide-react'
import { curlExample, javascriptExample, pythonExample, type Endpoint } from '@/lib/openapi'
import { roleLabel } from '@/lib/roles'
import { Breadcrumbs } from '@/components/layout/Breadcrumbs'
import { Markdown } from '@/components/markdown/Markdown'
import { Chip } from '@/components/ui/chip'
import { CopyLinkButton } from '@/components/ui/copy-link-button'
import { InlineCode } from '@/components/ui/inline-code'
import { CodePanel } from './CodePanel'
import { FieldList, SpecText } from './FieldList'
import { methodColor } from './method-color'
import { useDocsSet } from '@/pages/docs/docs-set'
import { usePageTitle } from '../../hooks/usePageTitle'
import { TextLink } from '@/components/ui/text-link'

const LOCATIONS: { location: string; title: string }[] = [
  { location: 'path', title: 'Path parameters' },
  { location: 'query', title: 'Query parameters' },
  { location: 'header', title: 'Header parameters' },
]

// One block of the contract, in the card look of the settings pages.
function Card({ title, aside, children }: { title: string; aside?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="rounded-lg border border-gray-200 bg-card px-4 pt-3 dark:border-gray-800" aria-label={title}>
      <h2 className="flex items-baseline justify-between gap-4 text-sm font-medium text-gray-900 dark:text-gray-100">
        {title}
        {aside && <span className="text-xs font-normal text-gray-500 dark:text-gray-400">{aside}</span>}
      </h2>
      <div className="pb-1">{children}</div>
    </section>
  )
}

type Props = {
  endpoint: Endpoint
  prev?: Endpoint
  next?: Endpoint
  /** Route of the docs home; endpoints live under `<docsBase>/endpoints/`. */
  docsBase: string
  baseURL: string
}

// One endpoint: its contract on the left, request and response samples on the
// right on wide screens.
export function EndpointDoc({ endpoint, prev, next, docsBase, baseURL }: Props) {
  const { label } = useDocsSet()
  usePageTitle(endpoint.summary, label)
  const routeBase = `${docsBase}/endpoints`
  const success = endpoint.responses.find(r => r.status.startsWith('2'))
  const errors = endpoint.responses.filter(r => !r.status.startsWith('2'))
  const url = `${baseURL}/api/v1${endpoint.path}`
  const requestSamples = [
    { id: 'curl', label: 'cURL', language: 'bash', code: curlExample(endpoint, baseURL) },
    { id: 'js', label: 'JavaScript', language: 'javascript', code: javascriptExample(endpoint, baseURL) },
    { id: 'python', label: 'Python', language: 'python', code: pythonExample(endpoint, baseURL) },
  ]
  const responseSamples = endpoint.responses
    .filter(r => r.example != null)
    .map(r => ({ id: r.status, label: r.status, language: 'json', code: JSON.stringify(r.example, null, 2) }))

  return (
    <div className="flex flex-col gap-8 xl:flex-row" data-testid="api-endpoint">
      <article className="min-w-0 max-w-3xl flex-1 space-y-4">
        <div>
          <Breadcrumbs items={[{ label, to: docsBase || '/' }, { label: endpoint.tag }]} />
          <h1 className="text-2xl font-normal text-gray-900 dark:text-gray-100">{endpoint.summary}</h1>
          {endpoint.description && (
            <Markdown variant="docs" className="mt-2">
              {endpoint.description}
            </Markdown>
          )}
        </div>

        <div className="flex items-center gap-3 rounded-lg border border-gray-200 bg-card py-1.5 pl-3 pr-1.5 dark:border-gray-800" data-testid="api-request-bar">
          <Chip color={methodColor(endpoint.method)}>{endpoint.method}</Chip>
          <code className="min-w-0 flex-1 break-all font-mono text-sm text-gray-900 dark:text-gray-100">
            /api/v1{endpoint.path}
          </code>
          <CopyLinkButton url={url} icon={Copy} iconOnly label="Copy URL" toastMessage="URL copied" errorMessage="Could not copy the URL" />
        </div>

        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm text-gray-500 dark:text-gray-400">
          <span className="flex flex-wrap items-center gap-1.5">
            Roles
            {endpoint.roles.map(role => (
              <Chip key={role}>{roleLabel(role)}</Chip>
            ))}
          </span>
          <span className="flex items-center gap-1.5">
            <KeyRound className="size-3.5" aria-hidden />
            Needs an{' '}
            <TextLink to={`${docsBase}/getting-started/authentication`}>
              access key
            </TextLink>
          </span>
        </div>

        {LOCATIONS.map(({ location, title }) => {
          const fields = endpoint.parameters.filter(p => p.location === location)
          return fields.length > 0 ? (
            <Card key={location} title={title}>
              <FieldList fields={fields} />
            </Card>
          ) : null
        })}

        {endpoint.body && (
          <Card title="Body" aside={endpoint.body.contentType}>
            <FieldList fields={endpoint.body.fields} />
          </Card>
        )}

        <Card title="Response" aside={success && `${success.status} · ${success.description}`}>
          {endpoint.result ? (
            <FieldList fields={endpoint.result.fields} side="response" />
          ) : (
            <p className="py-3 text-sm text-gray-700 dark:text-gray-300">
              {endpoint.resultType ? `The file itself (${endpoint.resultType}).` : 'No body.'}
            </p>
          )}
        </Card>

        <Card
          title="Errors"
          aside={
            <TextLink to={`${docsBase}/reference/errors`}>
              Error codes
            </TextLink>
          }
        >
          <ul className="divide-y divide-gray-200 dark:divide-gray-800">
            {errors.map(r => (
              <li key={r.status} className="flex gap-4 py-2.5 text-sm">
                <InlineCode className="h-fit">{r.status}</InlineCode>
                <span className="text-gray-700 dark:text-gray-300">
                  <SpecText text={r.description} />
                </span>
              </li>
            ))}
          </ul>
        </Card>

        <nav aria-label="More endpoints" className="flex justify-between gap-4 pt-4 text-sm">
          {prev ? (
            <TextLink to={`${routeBase}/${prev.id}`}>
              ← {prev.summary}
            </TextLink>
          ) : (
            <span />
          )}
          {next && (
            <TextLink to={`${routeBase}/${next.id}`}>
              {next.summary} →
            </TextLink>
          )}
        </nav>
      </article>

      <aside className="flex w-full min-w-0 flex-col gap-4 xl:sticky xl:top-0 xl:w-[28rem] xl:shrink-0 xl:self-start" data-testid="api-examples">
        <CodePanel title="Request" samples={requestSamples} testId="api-request-samples" />
        <CodePanel title="Response" samples={responseSamples} testId="api-response-samples" />
      </aside>
    </div>
  )
}
