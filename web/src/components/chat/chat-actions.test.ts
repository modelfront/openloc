import { afterEach, describe, expect, it, vi } from 'vitest'
import { QueryClient } from '@tanstack/react-query'
import { handleClientAction, type ClientActionContext } from './chat-actions'

function ctx(): ClientActionContext {
  return {
    navigate: vi.fn(),
    docsLink: (path: string) => `https://docs.example.com${path.replace(/^\/docs/, '')}`,
    queryClient: new QueryClient(),
    minimize: vi.fn(),
    patchLast: vi.fn(),
    send: vi.fn(),
    showReplies: vi.fn(),
  }
}

afterEach(() => vi.restoreAllMocks())

describe('handleClientAction navigate', () => {
  it('opens an app page in place', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const c = ctx()
    handleClientAction('navigate', { path: '/projects' }, c)
    expect(c.navigate).toHaveBeenCalledWith('/projects')
    expect(open).not.toHaveBeenCalled()
  })

  it('opens a docs page in a new tab, at its own address, so the conversation stays', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    for (const [path, url] of [['/docs', 'https://docs.example.com'], ['/docs/guides/mcp', 'https://docs.example.com/guides/mcp']]) {
      const c = ctx()
      handleClientAction('navigate', { path }, c)
      expect(open).toHaveBeenLastCalledWith(url, '_blank', 'noopener')
      expect(c.navigate).not.toHaveBeenCalled()
    }
  })

  it('does not treat a page that only starts with "docs" as the docs', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const c = ctx()
    handleClientAction('navigate', { path: '/docsearch' }, c)
    expect(c.navigate).toHaveBeenCalledWith('/docsearch')
    expect(open).not.toHaveBeenCalled()
  })

  it('ignores a path that is not a same-app path', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const c = ctx()
    handleClientAction('navigate', { path: '//evil.example.com/docs' }, c)
    expect(c.navigate).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  })
})

describe('handleClientAction asks', () => {
  it('puts a screenshot or a recording request on the last message', () => {
    for (const [action, kind] of [['ask_screenshot', 'screenshot'], ['ask_repeat', 'repeat']] as const) {
      const c = ctx()
      handleClientAction(action, { reason: 'To see it.' }, c)
      const patch = vi.mocked(c.patchLast).mock.calls[0][0]
      expect(patch({ role: 'assistant', content: '', toolCalls: [], charts: [], confirmations: [] }).ask)
        .toEqual({ kind, reason: 'To see it.' })
    }
  })

  it('ignores an ask without a reason', () => {
    const c = ctx()
    handleClientAction('ask_screenshot', {}, c)
    expect(c.patchLast).not.toHaveBeenCalled()
  })
})
