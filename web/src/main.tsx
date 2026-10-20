import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider, QueryCache, MutationCache } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { AttentionProvider } from './hooks/useAttention'
import { ThemeProvider } from 'next-themes'
import './index.css'
import App from './App'
import { ThemeFavicon } from './components/ThemeFavicon'
import { Toaster } from '@/components/ui/sonner'
import { ActionTrailRecorder } from '@/components/issues/ActionTrailRecorder'
import { applyCachedPalette } from './lib/palettes'
import { initErrtrack, notify, shouldReport } from './lib/errtrack'
import { toast } from 'sonner'
import { notifyDemoReadOnly } from './lib/demo'

// Error reporting → Honeybadger via the backend proxy. Fire-and-forget: the
// SDK turns on once /api/config confirms reporting is enabled; errors in the
// few ms before that are lost by design (fail-safe off, see lib/errtrack).
void initErrtrack()

// A Playground refusal nobody caught (a direct API call outside React Query)
// still gets the one warning, and the browser does not log it as an error.
window.addEventListener('unhandledrejection', event => {
  if (notifyDemoReadOnly(event.reason, m => toast.info(m, { id: 'demo-read-only' }))) event.preventDefault()
})

// Apply the last-known palette synchronously, before the tree renders, so a
// reload doesn't flash the default colors. PaletteProvider then
// reconciles with the server.
applyCachedPalette()

// Query keys can embed customer segment text (tmMatches/mtPreview carry the
// source string; tm.entries carries the user's search) — content that must
// never reach a Honeybadger notice. Keep the namespace element and
// numeric IDs; replace every other element with a placeholder.
function safeQueryKey(key: readonly unknown[]): string {
  return JSON.stringify(key.map((part, i) => {
    if (i === 0 && typeof part === 'string') return part
    if (typeof part === 'number') return part
    return '…'
  }))
}

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false } },
  // Cache-level onError sees EVERY query/mutation failure — including
  // mutations with their own onError (useMutationWithToast keeps its toast
  // job) and ones that bypass the shared hooks. shouldReport filters out
  // expected failures (401/409/5xx-already-reported-by-backend/aborts).
  queryCache: new QueryCache({
    onError: (err, query) => {
      if (!shouldReport(err)) return
      notify(err, {
        name: 'QueryError',
        context: { queryKey: safeQueryKey(query.queryKey) },
      })
    },
  }),
  mutationCache: new MutationCache({
    onError: (err, _vars, _ctx, mutation) => {
      // Every change the demo organization refuses gets the same single
      // notice; useMutationWithToast leaves it to this.
      if (notifyDemoReadOnly(err, m => toast.info(m, { id: 'demo-read-only' }))) return
      if (!shouldReport(err)) return
      notify(err, {
        name: 'MutationError',
        context: {
          mutationKey: mutation.options.mutationKey ? safeQueryKey(mutation.options.mutationKey) : null,
        },
      })
    },
  }),
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider attribute="class" defaultTheme="system" storageKey="openloc-theme">
      <ThemeFavicon />
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <AttentionProvider>
            <App />
          </AttentionProvider>
          <Toaster position="bottom-right" />
          {/* The last clicks, pages and errors, for an issue report. */}
          <ActionTrailRecorder />
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
