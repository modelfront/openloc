import type { ReactNode } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

/** Prints the current query string, so a test can assert what the URL holds. */
export function Where() {
  const loc = useLocation()
  return <p data-testid="where">{loc.search}</p>
}

/** Renders `element` at `url`, matched by the route `path`, with a fresh query client and `<Where />` beside it. */
export function renderAtRoute(element: ReactNode, { url, path }: { url: string; path: string }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path={path} element={<>{element}<Where /></>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}
