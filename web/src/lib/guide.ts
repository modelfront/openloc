// UI guide: the assistant points at elements marked
// data-guide="<id>". One highlight or a multi-page tour runs on driver.js;
// a step with a path opens that page first, and driver.js waits for the
// element to mount. Anchor ids and paths are validated server-side against
// the page registry (internal/chat/pages_registry.go).
import { driver, type Driver, type DriveStep, type PopoverDOM } from 'driver.js'
import 'driver.js/dist/driver.css'
import { closeChat, openChat } from './chat-bus'
import { endReveal, revealAnchor } from './guide-reveal'
import { clearTourHash, setTourHash } from './demo'
import { autoPlayOn, forgetSpeech, markVoiceBroken, prefetchSpeech, setAutoPlayOn, setVoiceOn, speak, stopSpeech, voiceAvailable, voiceOn } from './guide-voice'

export interface GuideStep {
  /** Route to open before this step; empty = stay on the current page. */
  path?: string
  anchor: string
  title?: string
  body: string
  /** What the voice reads instead of body; never shown. */
  say?: string
}

/**
 * How long a step waits for its element after opening a page. The wait ends
 * as soon as the element renders; the long ceiling covers a lazily loaded
 * page such as the CAT editor on a first visit.
 */
const WAIT_FOR_ELEMENT_MS = 10_000

let active: Driver | null = null
// The single highlight on screen, so attachReplies can show it again with
// reply buttons. Null during a tour and when nothing is highlighted.
let single: { step: GuideStep; navigate: (path: string) => void } | null = null

/** One-click answers shown in the highlight card (suggest_replies). */
export interface GuideReplies {
  labels: string[]
  /** A reply was clicked; the card is already closed. */
  onPick: (label: string) => void
  /** The user closed the card (× or Esc) without a pick. */
  onDismiss: () => void
}

// driver.js writes title and description as HTML. Tour text comes from the
// model, which can repeat text it read in a document, so it is escaped here
// and always shows as plain text.
function escapeHTML(text: string): string {
  return text.replace(/[&<>"']/g, c => `&#${c.charCodeAt(0)};`)
}

export const anchorSelector = (anchor: string) => `[data-guide="${CSS.escape(anchor)}"]`

// Anchors inside the assistant panel: the step opens the panel first.
const inChatPanel = (step: GuideStep | undefined) =>
  !!step && step.anchor.startsWith('chat.') && step.anchor !== 'chat.launcher'

// Anchors inside a dialog that opens when the tour reveals them (the chart
// builder): the step waits for the dialog to render.
const inRevealedDialog = (step: GuideStep) => step.anchor.startsWith('chart_builder.')

// A step reached from another page (or in the assistant panel, which opens
// for it) waits for its element to render: from the step before it going
// forward, from the step after it going back. A step that shares its page with
// both neighbors does not wait, so a missing element shows its caption at once
// instead of a silent pause.
function toDriveStep(step: GuideStep, prev: GuideStep | undefined, next: GuideStep | undefined): DriveStep {
  const opensPage = !!step.path && (step.path !== prev?.path || (!!next && step.path !== next.path))
  return {
    element: anchorSelector(step.anchor),
    popover: {
      title: step.title && escapeHTML(step.title),
      description: escapeHTML(step.body),
      // The launcher sits in the bottom-right corner: its card opens above it.
      ...(step.anchor === 'chat.launcher' ? { side: 'top' as const, align: 'end' as const } : {}),
    },
    waitForElement: opensPage || !prev || inChatPanel(step) || inRevealedDialog(step) ? WAIT_FOR_ELEMENT_MS : 0,
  }
}

/** How a tour starts: the saved tour it is (for its link), and at which step. */
export interface GuideOptions {
  /** The saved tour's name; a tour the assistant wrote has none. */
  name?: string
  /** The step to start at, 0-based. */
  start?: number
  /**
   * Called once the user ends a tour: `finished` after Done on the last step,
   * false after ×, Esc or End tour. Not called when a new guide replaces it.
   */
  onEnd?: (finished: boolean) => void
}

/**
 * Starts a guide, replacing any running one. `navigate` opens a route the
 * way the app's router does; a step's path is opened only when it differs
 * from the current location.
 */
export function runGuide(steps: GuideStep[], navigate: (path: string) => void, replies?: GuideReplies, opts: GuideOptions = {}): void {
  if (steps.length === 0) return
  stopGuide()
  const start = Math.min(Math.max(opts.start ?? 0, 0), steps.length - 1)
  let picked = false
  let moving = false
  // The tour opens the assistant panel for steps inside it and puts it away
  // for the next step outside, where it would cover the page.
  let chatOpened = false
  const go = (step: GuideStep | undefined) => {
    if (step?.path && step.path !== window.location.pathname) navigate(step.path)
    // Before the step looks for its element, so a dialog it points into opens.
    if (step) revealAnchor(step.anchor)
    if (inChatPanel(step)) {
      openChat()
      chatOpened = true
    } else if (step && chatOpened) {
      closeChat()
      chatOpened = false
    }
  }
  const tour = steps.length > 1
  // The card of the step being left goes at once; the next one shows once
  // its element has settled (showWhenSettled).
  const hideShownCard = () => {
    if (shown) shown.popover.wrapper.style.visibility = 'hidden'
  }
  const advance = (dr: Driver) => {
    if (moving) return
    const next = steps[(dr.getActiveIndex() ?? 0) + 1]
    stopSpeech()
    if (!next) {
      forgetSpeech()
      dr.destroy()
      opts.onEnd?.(true)
      return
    }
    moving = true
    hideShownCard()
    go(next)
    dr.moveNext()
  }

  // Voice: known once the status call answers, which can be after the first
  // card shows; the card on screen then gets its controls late.
  let voiceReady = false
  let shown: { popover: PopoverDOM; index: number } | null = null
  let spoken = -1
  const speakStep = (index: number, controls: VoiceControls) => {
    if (!voiceReady || !voiceOn() || index === spoken) return
    spoken = index
    const s = steps[index]
    controls.setHint('')
    speak(s, () => {
      if (tour && autoPlayOn() && active === d) advance(d)
    }).catch((err: unknown) => {
      if (active !== d) return
      if (err instanceof Error && err.name === 'NotAllowedError') {
        // The browser waits for a click before it plays sound.
        controls.setHint('Click Voice to play.')
      } else {
        // No voice for the rest of the visit, and no controls that cannot work.
        markVoiceBroken()
        voiceReady = false
        controls.el.remove()
      }
      // The card changed size: place it again, so it does not cover its element.
      d.refresh()
    })
    const next = steps[index + 1]
    if (next) prefetchSpeech(next)
  }
  const decorate = (popover: PopoverDOM, index: number) => {
    const controls = voiceControls(tour, on => {
      setVoiceOn(on)
      spoken = -1
      if (on) speakStep(index, controls)
      else stopSpeech()
    })
    popover.footer.before(controls.el)
    speakStep(index, controls)
  }

  // A close the user chose: the reading stops and pending replies are dismissed.
  const close = (dr: Driver) => {
    forgetSpeech()
    if (replies && !picked) replies.onDismiss()
    dr.destroy()
    if (tour) opts.onEnd?.(false)
  }

  const d = driver({
    // The step the tour starts at waits for its element like a new page: its
    // page may still be loading, as it is after a reload or from a link.
    steps: steps.map((s, i) => toDriveStep(s, i === start ? undefined : steps[i - 1], steps[i + 1])),
    popoverClass: 'openloc-guide',
    // Lighter than driver.js's 0.7, so the page stays readable behind the card.
    overlayOpacity: 0.5,
    // The highlighted element is shown, not used: a click on it would open
    // another page in the middle of a step.
    disableActiveInteraction: true,
    // Room for the large arrow between the card and its element.
    popoverOffset: 18,
    // A click on the dimmed page asks before it ends a tour; × and Esc end
    // it at once. A single highlight closes.
    overlayClickBehavior: (_el, _step, { driver: dr }) => {
      if (!tour) {
        close(dr)
        return
      }
      if (shown) askToEnd(shown.popover, () => close(dr))
    },
    // A step whose element never appears shows its caption centered. Not
    // skipMissingElement: driver.js also counts steps on other pages as
    // missing, so a cross-page tour would end with Done on its first page.
    waitForElement: WAIT_FOR_ELEMENT_MS,
    // The step count is in the card's label (cardLabel).
    showProgress: false,
    showButtons: tour ? ['next', 'previous', 'close'] : ['close'],
    nextBtnText: 'Next',
    prevBtnText: 'Back',
    doneBtnText: 'Done',
    // Clicks while a step is still waiting for its page are ignored; driver.js
    // would otherwise queue them and jump several steps at once.
    onNextClick: (_el, _step, { driver: dr }) => advance(dr),
    onPrevClick: (_el, _step, { driver: dr }) => {
      if (moving) return
      stopSpeech()
      moving = true
      hideShownCard()
      go(steps[(dr.getActiveIndex() ?? 0) - 1])
      dr.movePrevious()
    },

    // The element found: ask it to show itself (a collapsed section opens).
    onHighlightStarted: (_el, _step, { driver: dr }) => {
      const s = steps[dr.getActiveIndex() ?? 0]
      if (s) revealAnchor(s.anchor)
    },
    // A page that re-renders after its data loads can swap the highlighted
    // element for a new one; show the step again so it finds the new node.
    onHighlighted: (el, _step, { driver: dr }) => {
      if (el) watchReplacement(el, dr, () => moving)
    },
    // Rendered for every step, including a centered card with no element
    // (onHighlighted is not), so it is where a pending move ends.
    onPopoverRender: (popover, { driver: dr }) => {
      // The card waits until its element has settled: a page that renders
      // again once its data arrives replaces or moves the element right after
      // it is found.
      showWhenSettled(popover, dr)
      moving = false
      if (replies) {
        popover.description.after(
          replyButtons(replies.labels, label => {
            picked = true
            stopGuide()
            replies.onPick(label)
          }),
        )
      }
      const index = dr.getActiveIndex() ?? 0
      popover.wrapper.prepend(guideAvatar())
      popover.title.before(cardLabel(tour ? `Guided tour · ${index + 1} of ${steps.length}` : 'Tip'))
      if (tour && opts.name) setTourHash(opts.name, index)
      shown = { popover, index }
      if (voiceReady) decorate(popover, index)
    },
    // Only a user close (× or Esc) reaches this hook; a guide replaced by a
    // new one is closed from code and keeps its replies quiet.
    onDestroyStarted: (_el, _step, { driver: dr }) => close(dr),
    onDestroyed: () => {
      stopSpeech()
      endReveal()
      if (tour && opts.name) clearTourHash()
      if (active === d) {
        active = null
        single = null
      }
    },
  })
  active = d
  single = tour ? null : { step: steps[0], navigate }
  void voiceAvailable().then(ok => {
    voiceReady = ok
    if (ok && active === d && shown) {
      decorate(shown.popover, shown.index)
      d.refresh()
    }
  })
  go(steps[start])
  d.drive(start)
}


// Keeps a card hidden until its element stays put: on the page and not moved
// for two checks in a row. A replaced element is looked up again first; a page
// that never settles shows the card after SETTLE_MAX_MS. driver.js's
// placeholder for a missing element stays on the page, so that card shows.
const SETTLE_CHECK_MS = 80
const SETTLE_MAX_MS = 1500

function showWhenSettled(popover: PopoverDOM, dr: Driver) {
  const index = dr.getActiveIndex()
  const el = dr.getActiveElement()
  popover.wrapper.style.visibility = 'hidden'
  let last = ''
  let still = 0
  let waited = 0
  const timer = window.setInterval(() => {
    waited += SETTLE_CHECK_MS
    if (!dr.isActive() || dr.getActiveIndex() !== index || index === undefined) {
      window.clearInterval(timer)
      return
    }
    if (!el?.isConnected) {
      window.clearInterval(timer)
      dr.moveTo(index)
      return
    }
    const now = JSON.stringify(el.getBoundingClientRect())
    still = now === last ? still + 1 : 0
    last = now
    if (still >= 2 || waited >= SETTLE_MAX_MS) {
      window.clearInterval(timer)
      dr.refresh()
      popover.wrapper.style.visibility = ''
    }
  }, SETTLE_CHECK_MS)
}

// The label above a card's title that marks it as a guide, not page content.
function cardLabel(text: string): HTMLElement {
  const el = document.createElement('div')
  el.className = 'openloc-guide-label'
  el.dataset.testid = 'guide-card-label'
  el.textContent = text
  return el
}

// The round compass at a card's top left: the guide showing the way.
function guideAvatar(): HTMLElement {
  const el = document.createElement('div')
  el.className = 'openloc-guide-avatar'
  el.setAttribute('aria-hidden', 'true')
  el.innerHTML = COMPASS_SVG
  return el
}

// Lucide's compass, static markup (no user text).
const COMPASS_SVG =
  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
  '<circle cx="12" cy="12" r="10"/><path d="m16.24 7.76-1.804 5.411a2 2 0 0 1-1.265 1.265L7.76 16.24l1.804-5.411a2 2 0 0 1 1.265-1.265z"/></svg>'

// The question a click beside a tour's card asks. It takes the place of the
// Back / Next row until it is answered.
function askToEnd(popover: PopoverDOM, end: () => void) {
  if (popover.wrapper.querySelector('.openloc-guide-confirm')) return
  const box = document.createElement('div')
  box.className = 'openloc-guide-confirm'
  box.dataset.testid = 'guide-confirm-end'
  const text = document.createElement('span')
  text.textContent = 'End the tour?'
  const keep = document.createElement('button')
  keep.type = 'button'
  keep.textContent = 'Keep going'
  keep.addEventListener('click', () => {
    box.remove()
    popover.footer.style.display = ''
  })
  const stop = document.createElement('button')
  stop.type = 'button'
  stop.textContent = 'End tour'
  stop.dataset.testid = 'guide-confirm-end-yes'
  stop.addEventListener('click', end)
  box.append(text, keep, stop)
  popover.footer.style.display = 'none'
  popover.footer.after(box)
}

interface VoiceControls {
  el: HTMLElement
  setHint: (message: string) => void
}

// The Voice and Auto-play toggles under the card text. Auto-play (tours only)
// moves to the next step when the reading ends, and shows only while the
// voice is on.
function voiceControls(tour: boolean, onVoice: (on: boolean) => void): VoiceControls {
  const el = document.createElement('div')
  el.className = 'openloc-guide-voice'
  const hint = document.createElement('span')
  hint.className = 'openloc-guide-voice-hint'
  const toggle = (label: string, pressed: () => boolean, onClick: () => void, testid: string) => {
    const b = document.createElement('button')
    b.type = 'button'
    b.textContent = label
    b.dataset.testid = testid
    const sync = () => b.setAttribute('aria-pressed', String(pressed()))
    sync()
    b.addEventListener('click', () => {
      onClick()
      sync()
      render()
    })
    return b
  }
  const voice = toggle('🔊 Voice', voiceOn, () => onVoice(!voiceOn()), 'guide-voice-toggle')
  const auto = toggle('Auto-play', autoPlayOn, () => setAutoPlayOn(!autoPlayOn()), 'guide-autoplay-toggle')
  const render = () => {
    voice.textContent = voiceOn() ? '🔊 Voice' : '🔇 Voice'
    auto.hidden = !tour || !voiceOn()
  }
  el.append(voice, auto, hint)
  render()
  return { el, setHint: message => (hint.textContent = message) }
}

/**
 * Shows the highlight on screen again with reply buttons. Returns false when
 * no single highlight is showing, so the caller puts the replies elsewhere.
 */
export function attachReplies(replies: GuideReplies): boolean {
  if (!active || !single) return false
  const { step, navigate } = single
  runGuide([{ ...step, path: undefined }], navigate, replies)
  return true
}

// While a step shows, its element may be replaced (a page that renders again
// once its data arrives, which can take a while on a first visit) or moved (a
// panel opening beside it, a section above it loading). The step follows it
// until the tour moves on, and scrolls to it when a move or a change of size
// leaves it cut off, unless the user has scrolled the page themselves.
// `leaving` is true once the tour is on its way to another step: the element
// may then vanish with its page, and the step must not be shown again.
function watchReplacement(el: Element, dr: Driver, leaving: () => boolean) {
  const index = dr.getActiveIndex()
  let last = JSON.stringify(el.getBoundingClientRect())
  let fixedAt = ''
  let userScrolled = false
  const onUserScroll = () => { userScrolled = true }
  const userEvents = ['wheel', 'touchmove', 'keydown'] as const
  for (const e of userEvents) window.addEventListener(e, onUserScroll, { passive: true })
  const stop = () => {
    window.clearInterval(timer)
    for (const e of userEvents) window.removeEventListener(e, onUserScroll)
  }
  const timer = window.setInterval(() => {
    if (leaving() || !dr.isActive() || dr.getActiveIndex() !== index || index === undefined) {
      stop()
      return
    }
    if (!el.isConnected) {
      stop()
      dr.moveTo(index)
      return
    }
    const rect = el.getBoundingClientRect()
    const block = userScrolled ? null : scrollNeeded(rect)
    if (block) el.scrollIntoView({ block })
    const now = JSON.stringify(rect)
    if (now !== last) {
      last = now
      dr.refresh()
    } else if (coversElement(rect) && now !== fixedAt) {
      // driver.js can place the card before the page settles; it must never
      // sit on its own element. One re-placement per position, no loop.
      fixedAt = now
      dr.refresh()
    }
  }, 250)
}

// Whether the tour card overlaps the element it points at.
function coversElement(rect: DOMRect): boolean {
  const card = document.querySelector('.driver-popover')?.getBoundingClientRect()
  if (!card) return false
  return card.left < rect.right && card.right > rect.left && card.top < rect.bottom && card.bottom > rect.top
}

// How to scroll a step's element into view, or null when it is fine as it
// is. One that fits the screen must show whole; a taller one shows its top.
function scrollNeeded(rect: DOMRect): ScrollLogicalPosition | null {
  const h = window.innerHeight
  if (rect.height <= h) return rect.top < 0 || rect.bottom > h ? 'center' : null
  return rect.bottom <= 0 || rect.top > h / 3 ? 'start' : null
}

function replyButtons(labels: string[], onPick: (label: string) => void): HTMLElement {
  const box = document.createElement('div')
  box.className = 'openloc-guide-replies'
  for (const label of labels) {
    const b = document.createElement('button')
    b.type = 'button'
    b.className = 'driver-popover-footer-btn'
    b.textContent = label
    b.addEventListener('click', () => onPick(label))
    box.appendChild(b)
  }
  return box
}

export function stopGuide(): void {
  stopSpeech()
  active?.destroy()
  active = null
  single = null
}

/** data-guide ids currently rendered and visible, for the assistant's prompt. */
export function visibleAnchors(root: ParentNode = document): string[] {
  const ids = new Set<string>()
  root.querySelectorAll<HTMLElement>('[data-guide]').forEach(el => {
    if (el.getClientRects().length > 0 && el.dataset.guide) ids.add(el.dataset.guide)
  })
  return [...ids]
}
