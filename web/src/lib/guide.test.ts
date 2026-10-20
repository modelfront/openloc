import { GUIDE_REVEAL_EVENT } from './guide-reveal'
import { describe, it, expect, vi, afterEach } from 'vitest'
import { runGuide, stopGuide, visibleAnchors, anchorSelector, attachReplies } from './guide'

afterEach(() => {
  stopGuide()
  document.body.innerHTML = ''
  window.history.replaceState({}, '', '/')
})

describe('visibleAnchors', () => {
  it('reports only rendered, visible data-guide ids, once each', () => {
    document.body.innerHTML = `
      <button data-guide="projects.new">New</button>
      <div data-guide="projects.filters"></div>
      <div data-guide="projects.new"></div>
      <div data-guide="tasks.list" hidden></div>`
    // jsdom has no layout: an element "is visible" when it has client rects.
    const visible = new Set(['projects.new', 'projects.filters'])
    vi.spyOn(HTMLElement.prototype, 'getClientRects').mockImplementation(function (this: HTMLElement) {
      return (visible.has(this.dataset.guide ?? '') && !this.hidden ? [{}] : []) as unknown as DOMRectList
    })
    expect(visibleAnchors().sort()).toEqual(['projects.filters', 'projects.new'])
  })
})

describe('runGuide', () => {
  it("opens the first step's route when it differs from the current page", () => {
    const navigate = vi.fn()
    window.history.replaceState({}, '', '/tasks')
    runGuide([{ path: '/projects', anchor: 'projects.new', body: 'Start here.' }], navigate)
    expect(navigate).toHaveBeenCalledWith('/projects')
  })

  it('stays put when already on the route, or when the step has none', () => {
    const navigate = vi.fn()
    window.history.replaceState({}, '', '/projects')
    runGuide([{ path: '/projects', anchor: 'projects.new', body: 'Here.' }], navigate)
    runGuide([{ anchor: 'nav.tasks', body: 'Your work.' }], navigate)
    expect(navigate).not.toHaveBeenCalled()
  })

  it('escapes anchor ids in the selector', () => {
    expect(anchorSelector('a"b')).toBe('[data-guide="a\\"b"]')
  })

  it('a missing element on the same page shows its caption at once, not after a silent wait', async () => {
    // Step 2's element is absent (a collapsed panel); step 3's is present.
    document.body.innerHTML = '<button data-guide="projects.new">New</button><div data-guide="projects.list">List</div>'
    runGuide(
      [
        { anchor: 'projects.new', title: 'One', body: 'a' },
        { anchor: 'editor.sidebar', title: 'Two', body: 'b' },
        { anchor: 'projects.list', title: 'Three', body: 'c' },
      ],
      vi.fn(),
    )
    const progress = () => document.querySelector('[data-testid="guide-card-label"]')?.textContent?.match(/\d+ of \d+/)?.[0]
    await vi.waitFor(() => expect(progress()).toBe('1 of 3'))
    document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')!.click()
    await vi.waitFor(() => expect(progress()).toBe('2 of 3'), { timeout: 800 })
    document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')!.click()
    await vi.waitFor(() => expect(progress()).toBe('3 of 3'), { timeout: 800 })
  })

  it('follows the element when the page re-renders it after loading', async () => {
    document.body.innerHTML = '<div id="grid"><div data-guide="editor.segments">loading…</div></div>'
    runGuide([{ anchor: 'editor.segments', body: 'Segments.' }], vi.fn())
    await vi.waitFor(() =>
      expect(document.querySelector('[data-guide="editor.segments"]')?.classList.contains('driver-active-element')).toBe(true),
    )
    // The page swaps the node for a new one, as a data load does.
    const fresh = document.createElement('div')
    fresh.dataset.guide = 'editor.segments'
    fresh.textContent = 'segments'
    document.getElementById('grid')!.replaceChildren(fresh)
    await vi.waitFor(() => expect(fresh.classList.contains('driver-active-element')).toBe(true), { timeout: 1500 })
  })

  it('still follows the element when the page re-renders it late', async () => {
    // Fake timers cover the six-second wait; the watcher's polling interval
    // runs on the same fake clock.
    vi.useFakeTimers()
    try {
      document.body.innerHTML = '<div id="grid"><div data-guide="editor.segments">loading…</div></div>'
      runGuide([{ anchor: 'editor.segments', body: 'Segments.' }], vi.fn())
      await vi.waitFor(() =>
        expect(document.querySelector('[data-guide="editor.segments"]')?.classList.contains('driver-active-element')).toBe(true),
      )
      // A slow first visit: the data arrives after several seconds.
      await vi.advanceTimersByTimeAsync(6000)
      const fresh = document.createElement('div')
      fresh.dataset.guide = 'editor.segments'
      document.getElementById('grid')!.replaceChildren(fresh)
      await vi.waitFor(() => expect(fresh.classList.contains('driver-active-element')).toBe(true), { timeout: 1500 })
    } finally {
      vi.useRealTimers()
    }
  })

  // A DOMRect at `top`; it serializes like a real one, as the watcher compares
  // rects by their JSON.
  const rectAt = (top: number, height = 50): DOMRect => {
    const r = { top, bottom: top + height, left: 0, right: 200, width: 200, height, x: 0, y: top }
    return { ...r, toJSON: () => r }
  }

  it('scrolls back to the element when content above pushes it off the screen', async () => {
    document.body.innerHTML = '<div data-guide="settings.connected_apps">Connected apps</div>'
    const el = document.querySelector<HTMLElement>('[data-guide="settings.connected_apps"]')!
    let top = 100
    el.getBoundingClientRect = () => rectAt(top)
    const scroll = vi.fn()
    el.scrollIntoView = scroll
    runGuide([{ anchor: 'settings.connected_apps', body: 'Apps.' }], vi.fn())
    await vi.waitFor(() => expect(el.classList.contains('driver-active-element')).toBe(true))
    // The highlight settles (driver's animation) before the page moves.
    await new Promise(r => setTimeout(r, 600))
    scroll.mockClear()
    // A section above loads and pushes the element below the fold.
    top = window.innerHeight + 400
    await vi.waitFor(() => expect(scroll).toHaveBeenCalled(), { timeout: 1500 })
  })

  it('scrolls to the element when it left the screen before the step settled', async () => {
    document.body.innerHTML = '<div data-guide="settings.connected_apps">Connected apps</div>'
    const el = document.querySelector<HTMLElement>('[data-guide="settings.connected_apps"]')!
    let top = 100
    el.getBoundingClientRect = () => rectAt(top)
    const scroll = vi.fn()
    el.scrollIntoView = scroll
    runGuide([{ anchor: 'settings.connected_apps', body: 'Apps.' }], vi.fn())
    await vi.waitFor(() => expect(el.classList.contains('driver-active-element')).toBe(true))
    // Pushed down during the highlight animation, and it stays there.
    top = window.innerHeight + 400
    scroll.mockClear()
    await new Promise(r => setTimeout(r, 600))
    scroll.mockClear()
    await vi.waitFor(() => expect(scroll).toHaveBeenCalled(), { timeout: 1500 })
  })

  it('scrolls when the element grows past the bottom of the screen', async () => {
    document.body.innerHTML = '<div data-guide="ai_prompt.context">Context</div>'
    const el = document.querySelector<HTMLElement>('[data-guide="ai_prompt.context"]')!
    let height = 40
    const top = window.innerHeight - 140
    el.getBoundingClientRect = () => rectAt(top, height)
    const scroll = vi.fn()
    el.scrollIntoView = scroll
    runGuide([{ anchor: 'ai_prompt.context', body: 'Context.' }], vi.fn())
    await vi.waitFor(() => expect(el.classList.contains('driver-active-element')).toBe(true))
    await new Promise(r => setTimeout(r, 600))
    scroll.mockClear()
    // A collapsed section opens: its top stays on screen, its body does not.
    height = 400
    await vi.waitFor(() => expect(scroll).toHaveBeenCalled(), { timeout: 1500 })
  })

  it('leaves the page alone once the user scrolls it', async () => {
    document.body.innerHTML = '<div data-guide="settings.connected_apps">Connected apps</div>'
    const el = document.querySelector<HTMLElement>('[data-guide="settings.connected_apps"]')!
    let top = 100
    el.getBoundingClientRect = () => rectAt(top)
    const scroll = vi.fn()
    el.scrollIntoView = scroll
    runGuide([{ anchor: 'settings.connected_apps', body: 'Apps.' }], vi.fn())
    await vi.waitFor(() => expect(el.classList.contains('driver-active-element')).toBe(true))
    // The highlight settles (driver's animation) before the page moves.
    await new Promise(r => setTimeout(r, 600))
    scroll.mockClear()
    window.dispatchEvent(new WheelEvent('wheel', { deltaY: 300 }))
    top = window.innerHeight + 400
    await new Promise(r => setTimeout(r, 600))
    expect(scroll).not.toHaveBeenCalled()
  })

  it('opens what a step points into, and waits for it to render', async () => {
    document.body.innerHTML = '<button data-guide="dashboard.new_chart">New chart</button>'
    // A dialog that opens when the tour asks for its metric list.
    const open = (e: Event) => {
      if ((e as CustomEvent<{ anchor: string | null }>).detail.anchor !== 'chart_builder.metrics') return
      setTimeout(() => {
        const list = document.createElement('div')
        list.dataset.guide = 'chart_builder.metrics'
        document.body.appendChild(list)
      }, 300)
    }
    window.addEventListener(GUIDE_REVEAL_EVENT, open)
    try {
      runGuide([
        { anchor: 'dashboard.new_chart', title: 'Button', body: 'Here.' },
        { anchor: 'chart_builder.metrics', title: 'Metrics', body: 'Pick one.' },
      ], vi.fn())
      const next = await vi.waitFor(() => {
        const b = document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')
        if (!b) throw new Error('popover not rendered yet')
        return b
      })
      next.click()
      await vi.waitFor(() => {
        const list = document.querySelector('[data-guide="chart_builder.metrics"]')
        expect(list?.classList.contains('driver-active-element')).toBe(true)
      }, { timeout: 2000 })
    } finally {
      window.removeEventListener(GUIDE_REVEAL_EVENT, open)
    }
  })

  it('reaches a step on a slow page, not back to the step whose page it left', async () => {
    document.body.innerHTML = '<div id="page"><div data-guide="projects.list">Project</div></div>'
    // The router swaps the page at once; the editor renders a second later.
    const navigate = vi.fn(() => {
      document.getElementById('page')!.replaceChildren()
      setTimeout(() => {
        const grid = document.createElement('div')
        grid.dataset.guide = 'editor.segments'
        document.getElementById('page')!.appendChild(grid)
      }, 1000)
    })
    runGuide([
      { anchor: 'projects.list', title: 'Your projects', body: 'Here.' },
      { anchor: 'editor.segments', title: 'The editor', body: 'There.', path: '/projects/1/jobs/1/edit' },
    ], navigate)
    const next = await vi.waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')
      if (!b) throw new Error('popover not rendered yet')
      return b
    })
    // The step settles first, so its watcher runs.
    await new Promise(r => setTimeout(r, 600))
    next.click()
    await vi.waitFor(() => {
      expect(document.querySelector('.driver-popover-title')?.textContent).toBe('The editor')
      expect(document.querySelector('[data-guide="editor.segments"]')?.classList.contains('driver-active-element')).toBe(true)
    }, { timeout: 3000 })
  })

  it('shows Next, not Done, when the next step is on another page', async () => {
    // Only step 1's element is on this page; step 2 lives on another route.
    document.body.innerHTML = '<button data-guide="projects.new">New</button>'
    runGuide(
      [
        { anchor: 'projects.new', title: 'Start', body: 'Here.' },
        { anchor: 'tms.new', title: 'Memories', body: 'There.', path: '/memories' },
      ],
      vi.fn(),
    )
    const next = await vi.waitFor(() => {
      const b = document.querySelector('.driver-popover-next-btn')
      if (!b) throw new Error('popover not rendered yet')
      return b
    })
    expect(next.textContent).toContain('Next')
  })
})

describe('attachReplies', () => {
  const popover = () =>
    vi.waitFor(() => {
      const p = document.querySelector('.driver-popover')
      if (!p) throw new Error('popover not rendered yet')
      return p as HTMLElement
    })

  it('does nothing when no highlight is showing', () => {
    expect(attachReplies({ labels: ['Next'], onPick: vi.fn(), onDismiss: vi.fn() })).toBe(false)
  })

  it('puts the replies on the highlight card; a click closes it and sends', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button>'
    runGuide([{ anchor: 'projects.new', body: 'Start here.' }], vi.fn())
    await popover()
    const onPick = vi.fn()
    const onDismiss = vi.fn()
    expect(attachReplies({ labels: ['Next', 'Skip'], onPick, onDismiss })).toBe(true)

    const buttons = await vi.waitFor(() => {
      const bs = [...document.querySelectorAll<HTMLButtonElement>('.openloc-guide-replies button')]
      if (bs.length !== 2) throw new Error('replies not rendered yet')
      return bs
    })
    expect(document.querySelector('.driver-popover')?.textContent).toContain('Start here.')
    buttons[0].click()
    expect(onPick).toHaveBeenCalledWith('Next')
    expect(onDismiss).not.toHaveBeenCalled()
    await vi.waitFor(() => expect(document.querySelector('.driver-popover')).toBeNull())
  })

  it('a user closing the card without a pick hands the replies back', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button>'
    runGuide([{ anchor: 'projects.new', body: 'Start here.' }], vi.fn())
    await popover()
    const onDismiss = vi.fn()
    attachReplies({ labels: ['Next'], onPick: vi.fn(), onDismiss })
    const close = await vi.waitFor(() => {
      if (!document.querySelector('.openloc-guide-replies')) throw new Error('not yet')
      return document.querySelector<HTMLButtonElement>('.driver-popover-close-btn')!
    })
    close.click()
    expect(onDismiss).toHaveBeenCalled()
    await vi.waitFor(() => expect(document.querySelector('.driver-popover')).toBeNull())
  })

  it('a card replaced by a new highlight keeps its replies quiet', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button><button data-guide="tms.new">TM</button>'
    runGuide([{ anchor: 'projects.new', body: 'Start here.' }], vi.fn())
    await popover()
    const onDismiss = vi.fn()
    attachReplies({ labels: ['Next'], onPick: vi.fn(), onDismiss })
    runGuide([{ anchor: 'tms.new', body: 'Now here.' }], vi.fn())
    expect(onDismiss).not.toHaveBeenCalled()
  })

  it('is not used during a tour', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button>'
    runGuide(
      [
        { anchor: 'projects.new', title: 'A', body: 'a' },
        { anchor: 'tms.new', title: 'B', body: 'b', path: '/memories' },
      ],
      vi.fn(),
    )
    await popover()
    expect(attachReplies({ labels: ['Next'], onPick: vi.fn(), onDismiss: vi.fn() })).toBe(false)
  })
})


describe('guide text', () => {
  it('shows a caption as text, never as HTML', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button>'
    runGuide(
      [{ anchor: 'projects.new', title: '<b>Bold</b>', body: '<img src=x onerror="window.__pwned=1">Hi' }],
      vi.fn(),
    )
    await vi.waitFor(() => expect(document.querySelector('.driver-popover-description')).not.toBeNull())
    const popover = document.querySelector('.driver-popover')!
    expect(popover.querySelector('img')).toBeNull()
    expect(popover.querySelector('.driver-popover-title b')).toBeNull()
    expect(document.querySelector('.driver-popover-description')!.textContent).toBe('<img src=x onerror="window.__pwned=1">Hi')
    expect(document.querySelector('.driver-popover-title')!.textContent).toBe('<b>Bold</b>')
  })
})

describe('steps inside the assistant panel', () => {
  it('open the panel first and wait for it', async () => {
    const opened = vi.fn(() => {
      setTimeout(() => {
        const el = document.createElement('div')
        el.dataset.guide = 'chat.mode'
        document.body.appendChild(el)
      }, 50)
    })
    window.addEventListener('openloc:chat-open', opened)
    try {
      document.body.innerHTML = '<button data-guide="chat.launcher">Ask</button>'
      runGuide(
        [
          { anchor: 'chat.launcher', title: 'The assistant', body: 'Bottom right.' },
          { anchor: 'chat.mode', title: 'Mode', body: 'Read-only first.' },
        ],
        vi.fn(),
      )
      const progress = () => document.querySelector('[data-testid="guide-card-label"]')?.textContent?.match(/\d+ of \d+/)?.[0]
      await vi.waitFor(() => expect(progress()).toBe('1 of 2'))
      expect(opened).not.toHaveBeenCalled()
      document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')!.click()
      await vi.waitFor(() => expect(opened).toHaveBeenCalledTimes(1))
      await vi.waitFor(() => expect(progress()).toBe('2 of 2'), { timeout: 3000 })
      expect(document.querySelector('[data-guide="chat.mode"]')!.classList.contains('driver-active-element')).toBe(true)
    } finally {
      window.removeEventListener('openloc:chat-open', opened)
    }
  })

  it('put the panel away when the tour moves on to the page', async () => {
    const closed = vi.fn()
    window.addEventListener('openloc:chat-close', closed)
    try {
      document.body.innerHTML = '<div data-guide="chat.mode">Mode</div><button data-guide="projects.new">New</button>'
      runGuide(
        [
          { anchor: 'chat.mode', title: 'Mode', body: 'Read-only first.' },
          { anchor: 'projects.new', title: 'New', body: 'Start here.' },
        ],
        vi.fn(),
      )
      const progress = () => document.querySelector('[data-testid="guide-card-label"]')?.textContent?.match(/\d+ of \d+/)?.[0]
      await vi.waitFor(() => expect(progress()).toBe('1 of 2'))
      expect(closed).not.toHaveBeenCalled()
      document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')!.click()
      await vi.waitFor(() => expect(closed).toHaveBeenCalledTimes(1))
    } finally {
      window.removeEventListener('openloc:chat-close', closed)
    }
  })
})

describe('tour behavior', () => {
  const card = () => document.querySelector<HTMLElement>('.driver-popover')
  const nextBtn = () => vi.waitFor(() => {
    const b = document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')
    if (!b) throw new Error('card not rendered yet')
    return b
  })

  it('reports how the user ended a tour: Done on the last step, or ×', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button>'
    const onEnd = vi.fn()
    const steps = [{ anchor: 'projects.new', title: 'A', body: 'a' }, { anchor: 'projects.new', title: 'B', body: 'b' }]
    runGuide(steps, vi.fn(), undefined, { onEnd })
    ;(await nextBtn()).click()
    await vi.waitFor(() => expect(document.querySelector('[data-testid="guide-card-label"]')?.textContent).toContain('2 of 2'))
    ;(await nextBtn()).click()
    await vi.waitFor(() => expect(onEnd).toHaveBeenCalledWith(true))

    onEnd.mockClear()
    runGuide(steps, vi.fn(), undefined, { onEnd })
    await nextBtn()
    document.querySelector<HTMLButtonElement>('.driver-popover-close-btn')!.click()
    await vi.waitFor(() => expect(onEnd).toHaveBeenCalledWith(false))
  })

  it('a click beside a tour card asks before it ends the tour', async () => {
    document.body.innerHTML = '<button data-guide="projects.new">New</button><div data-guide="projects.list">List</div>'
    runGuide([
      { anchor: 'projects.new', title: 'New', body: 'Start here.' },
      { anchor: 'projects.list', title: 'List', body: 'Your projects.' },
    ], vi.fn())
    await nextBtn()
    const overlay = await vi.waitFor(() => {
      const o = document.querySelector('.driver-overlay path')
      if (!o) throw new Error('overlay not drawn yet')
      return o
    })
    const clickBeside = () => overlay.dispatchEvent(new MouseEvent('click', { bubbles: true }))

    clickBeside()
    await vi.waitFor(() => expect(document.querySelector('[data-testid="guide-confirm-end"]')).not.toBeNull())
    ;[...document.querySelectorAll<HTMLButtonElement>('.openloc-guide-confirm button')].find(b => b.textContent === 'Keep going')!.click()
    expect(document.querySelector('[data-testid="guide-confirm-end"]')).toBeNull()
    expect(card()).not.toBeNull()

    clickBeside()
    await vi.waitFor(() => expect(document.querySelector('[data-testid="guide-confirm-end-yes"]')).not.toBeNull())
    document.querySelector<HTMLButtonElement>('[data-testid="guide-confirm-end-yes"]')!.click()
    await vi.waitFor(() => expect(card()).toBeNull())
  })

  it('keeps the card hidden while the page replaces its element, then shows it for the new one', async () => {
    document.body.innerHTML = '<div id="page"><div data-guide="editor.segments">loading…</div></div>'
    runGuide([
      { anchor: 'editor.segments', title: 'Segments', body: 'The grid.' },
      { anchor: 'editor.filters', title: 'Filters', body: 'Filter.' },
    ], vi.fn())
    // The page renders again with its data, right after the element was found.
    await vi.waitFor(() => expect(card()).not.toBeNull())
    const fresh = document.createElement('div')
    fresh.dataset.guide = 'editor.segments'
    document.getElementById('page')!.replaceChildren(fresh)
    // The card must never show while it still points at the removed element.
    let shownTooEarly = false
    const watch = window.setInterval(() => {
      const c = card()
      if (c && c.style.visibility === '' && !fresh.classList.contains('driver-active-element')) shownTooEarly = true
    }, 5)
    try {
      await vi.waitFor(() => {
        expect(fresh.classList.contains('driver-active-element')).toBe(true)
        expect(card()!.style.visibility).toBe('')
      }, { timeout: 3000 })
    } finally {
      window.clearInterval(watch)
    }
    expect(shownTooEarly).toBe(false)
  })

  it('back to a step on another page waits for that page to render its element', async () => {
    window.history.replaceState({}, '', '/b')
    document.body.innerHTML = '<div id="page"><div data-guide="ai_routers.list">Routers</div></div>'
    // The editor opens and renders its filters half a second later.
    const navigate = vi.fn((path: string) => {
      window.history.replaceState({}, '', path)
      document.getElementById('page')!.replaceChildren()
      setTimeout(() => {
        const filters = document.createElement('div')
        filters.dataset.guide = 'editor.filters'
        document.getElementById('page')!.appendChild(filters)
      }, 500)
    })
    runGuide([
      { anchor: 'editor.segments', title: 'Segments', body: 'The grid.', path: '/a' },
      { anchor: 'editor.filters', title: 'Filters', body: 'Filter.', path: '/a' },
      { anchor: 'ai_routers.list', title: 'Routers', body: 'Per language.', path: '/b' },
    ], navigate, undefined, { start: 2 })
    const back = await vi.waitFor(() => {
      const b = document.querySelector<HTMLButtonElement>('.driver-popover-prev-btn')
      if (!b) throw new Error('card not rendered yet')
      return b
    })
    await new Promise(r => setTimeout(r, 400))
    back.click()
    await vi.waitFor(() => {
      expect(document.querySelector('[data-guide="editor.filters"]')?.classList.contains('driver-active-element')).toBe(true)
    }, { timeout: 3000 })
  })
})
