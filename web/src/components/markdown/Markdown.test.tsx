import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { halveRasterAsset, Markdown } from './Markdown'

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

describe('Markdown (docs variant)', () => {
  it('renders real h2/h3 headings with anchor ids that match extractHeadings', () => {
    wrap(<Markdown variant="docs">{'## Open the editor\n\ntext\n\n### From the Tasks page\n\n## Open the editor'}</Markdown>)
    const h2s = screen.getAllByRole('heading', { level: 2 })
    expect(h2s[0]).toHaveAttribute('id', 'open-the-editor')
    expect(h2s[1]).toHaveAttribute('id', 'open-the-editor-2')
    expect(screen.getByRole('heading', { level: 3 })).toHaveAttribute('id', 'from-the-tasks-page')
  })

  it('keeps heading ids stable across re-renders so TOC anchors keep resolving', () => {
    const md = '## Confirm a segment\n\ntext\n\n## Tags\n\n## Confirm a segment'
    const { rerender } = wrap(<Markdown variant="docs">{md}</Markdown>)
    const before = screen.getAllByRole('heading', { level: 2 }).map(h => h.id)
    expect(before).toEqual(['confirm-a-segment', 'tags', 'confirm-a-segment-2'])
    // A parent re-render (search opened, query refetched) must not re-suffix.
    rerender(<MemoryRouter><Markdown variant="docs">{md}</Markdown></MemoryRouter>)
    rerender(<MemoryRouter><Markdown variant="docs">{md}</Markdown></MemoryRouter>)
    expect(screen.getAllByRole('heading', { level: 2 }).map(h => h.id)).toEqual(before)
  })

  it('renders a stray # heading at h2 so the outline stays flat', () => {
    wrap(<Markdown variant="docs">{'# Not a title'}</Markdown>)
    expect(screen.queryByRole('heading', { level: 1 })).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2 })).toHaveTextContent('Not a title')
  })

  it('routes in-app links through the router and opens external links in a new tab', () => {
    wrap(<Markdown variant="docs">{'[Roles](/docs/reference/roles) and [Example](https://example.com) and [here](#steps)'}</Markdown>)
    const internal = screen.getByRole('link', { name: 'Roles' })
    expect(internal).toHaveAttribute('href', '/docs/reference/roles')
    expect(internal).not.toHaveAttribute('target')
    const external = screen.getByRole('link', { name: 'Example' })
    expect(external).toHaveAttribute('target', '_blank')
    expect(external).toHaveAttribute('rel', 'noreferrer')
    expect(screen.getByRole('link', { name: 'here' })).toHaveAttribute('href', '#steps')
  })

  it('resolves relative image sources against assetBase and lazy-loads them', () => {
    wrap(
      <Markdown variant="docs" assetBase="/api/docs/assets">
        {'![The editor](assets/cat-editor.png)\n\n![Abs](/static/x.png)\n\n![Ext](https://example.com/y.png)'}
      </Markdown>,
    )
    expect(screen.getByRole('img', { name: 'The editor' })).toHaveAttribute('src', '/api/docs/assets/cat-editor.png')
    expect(screen.getByRole('img', { name: 'The editor' })).toHaveAttribute('loading', 'lazy')
    expect(screen.getByRole('img', { name: 'Abs' })).toHaveAttribute('src', '/static/x.png')
    expect(screen.getByRole('img', { name: 'Ext' })).toHaveAttribute('src', 'https://example.com/y.png')
  })

  it('never injects raw HTML from the source', () => {
    const { container } = wrap(<Markdown variant="docs">{'<img src=x onerror="alert(1)"> **safe**'}</Markdown>)
    expect(container.querySelector('img')).toBeNull()
    expect(container.textContent).toContain('<img src=x onerror="alert(1)">')
    expect(screen.getByText('safe').tagName).toBe('STRONG')
  })

  it('renders GFM tables as real tables', () => {
    wrap(<Markdown variant="docs">{'| Shortcut | Action |\n|---|---|\n| Ctrl+Enter | Confirm |'}</Markdown>)
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Shortcut' })).toBeInTheDocument()
  })
})

// jsdom reports naturalWidth 0 for every image, so the DOM path cannot be
// exercised — the function is tested directly instead. What matters is that a
// 2x screenshot lays out at half size, that vector art is left alone, and that
// a second pass (ref then onLoad) does not halve twice.
describe('halveRasterAsset', () => {
  const fake = (src: string, naturalWidth: number, complete = true) =>
    ({ src, currentSrc: src, naturalWidth, complete, style: {} as CSSStyleDeclaration, dataset: {} as DOMStringMap }) as HTMLImageElement

  it('lays a 2x screenshot out at half its pixel width', () => {
    const el = fake('/api/docs/assets/qa-finding-popover.png', 578)
    halveRasterAsset(el)
    expect(el.style.width).toBe('289px')
    expect(el.dataset.halved).toBe('1')
  })

  it('leaves an SVG alone — it is vector and already sized in CSS pixels', () => {
    const el = fake('/api/docs/assets/segment-status-flow.svg', 760)
    halveRasterAsset(el)
    expect(el.style.width).toBeUndefined()
    expect(el.dataset.halved).toBeUndefined()
  })

  it('is idempotent, so the ref and onLoad passes cannot compound', () => {
    const el = fake('/api/docs/assets/cat-editor.png', 2880)
    halveRasterAsset(el)
    halveRasterAsset(el)
    expect(el.style.width).toBe('1440px')
  })

  it('does nothing before the image has loaded', () => {
    const el = fake('/api/docs/assets/cat-editor.png', 0, false)
    halveRasterAsset(el)
    expect(el.style.width).toBeUndefined()
  })
})

describe('Markdown (chat variant)', () => {
  it('collapses every heading level to h3', () => {
    wrap(<Markdown variant="chat">{'# One\n## Two'}</Markdown>)
    expect(screen.getAllByRole('heading', { level: 3 })).toHaveLength(2)
  })

  it('keeps in-app links in the app and opens external ones in a new tab', () => {
    wrap(<Markdown variant="chat">{'[in](/projects/4) [out](https://example.com) [sneaky](//example.com/x)'}</Markdown>)
    expect(screen.getByRole('link', { name: 'in' })).toHaveAttribute('href', '/projects/4')
    expect(screen.getByRole('link', { name: 'in' })).not.toHaveAttribute('target')
    expect(screen.getByRole('link', { name: 'out' })).toHaveAttribute('target', '_blank')
    expect(screen.getByRole('link', { name: 'sneaky' })).toHaveAttribute('target', '_blank')
  })

  it('renders API paths as downloads, not router links', () => {
    wrap(<Markdown variant="chat">{'[Download the XLIFF](/api/jobs/4/export)'}</Markdown>)
    const link = screen.getByRole('link', { name: 'Download the XLIFF' })
    expect(link).toHaveAttribute('href', '/api/jobs/4/export')
    expect(link).toHaveAttribute('download')
  })

  it('never loads an image from model output, so a prompt-injected URL is not fetched', () => {
    const { container } = wrap(<Markdown variant="chat">{'Look: ![chart](https://evil.example/?d=1)'}</Markdown>)
    expect(container.querySelector('img')).toBeNull()
    expect(container).toHaveTextContent('Look: chart')
  })
})
