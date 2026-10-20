import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ComponentProps } from 'react'
import { RawMTPane } from './RawMTPane'

function renderPane(props: Partial<ComponentProps<typeof RawMTPane>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  qc.setQueryData(['ai-routers'], [])
  qc.setQueryData(['members'], [])
  return render(
    <QueryClientProvider client={qc}>
      <RawMTPane heading={<span>toggle</span>} {...props} />
    </QueryClientProvider>,
  )
}

describe('RawMTPane', () => {
  it('names the MT router and hands the click to onPickRouter', () => {
    const onPickRouter = vi.fn()
    renderPane({ mtRouterName: 'Default MT router', onPickRouter })
    const link = screen.getByTestId('raw-mt-router-link')
    expect(link).toHaveTextContent('Default MT router')
    fireEvent.click(link)
    expect(onPickRouter).toHaveBeenCalledTimes(1)
  })

  it('says so when there is no router yet', () => {
    renderPane({ mtRouterName: null })
    expect(screen.getByTestId('raw-mt-router-link')).toHaveTextContent('No MT router yet')
    expect(screen.getByTestId('raw-mt-pretranslation')).toHaveTextContent('Pre-translation')
  })
})
