import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// driver.js as it is, with refresh() observed: a card that grows after it is
// placed must be placed again.
const { refreshSpy } = vi.hoisted(() => ({ refreshSpy: vi.fn() }))
vi.mock('driver.js', async () => {
  const actual = await vi.importActual<typeof import('driver.js')>('driver.js')
  return {
    ...actual,
    driver: (config: Parameters<typeof actual.driver>[0]) => {
      const d = actual.driver(config)
      const refresh = d.refresh
      d.refresh = () => { refreshSpy(); refresh() }
      return d
    },
  }
})

vi.mock('../api', () => ({
  getTourSpeechStatus: vi.fn(),
  fetchTourSpeech: vi.fn(),
}))

import { fetchTourSpeech, getTourSpeechStatus } from '../api'
import { runGuide, stopGuide } from './guide'
import { resetVoiceForTests, setAutoPlayOn, setVoiceOn } from './guide-voice'

const status = vi.mocked(getTourSpeechStatus)
const fetchSpeech = vi.mocked(fetchTourSpeech)
let played: HTMLAudioElement[] = []
const pause = vi.fn()

beforeEach(() => {
  refreshSpy.mockClear()
  played = []
  pause.mockClear()
  URL.createObjectURL = vi.fn(() => 'blob:clip')
  URL.revokeObjectURL = vi.fn()
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(function (this: HTMLMediaElement) {
    played.push(this as HTMLAudioElement)
    return Promise.resolve()
  })
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(pause)
  fetchSpeech.mockResolvedValue(new Blob(['mp3']))
  document.body.innerHTML =
    '<button data-guide="projects.new">New</button><div data-guide="projects.list">List</div>'
})

afterEach(() => {
  stopGuide()
  resetVoiceForTests()
  vi.restoreAllMocks()
  fetchSpeech.mockReset()
  status.mockReset()
  try {
    localStorage.clear()
  } catch {
    // jsdom always has storage; the guard mirrors the app code.
  }
})

const steps = [
  { anchor: 'projects.new', title: 'One', body: 'First card.', say: 'Welcome, this is the first card.' },
  { anchor: 'projects.list', title: 'Two', body: 'Second card.' },
]
const progress = () => document.querySelector('[data-testid="guide-card-label"]')?.textContent?.match(/\d+ of \d+/)?.[0]
const voiceButton = () => document.querySelector<HTMLButtonElement>('[data-testid="guide-voice-toggle"]')
const autoButton = () => document.querySelector<HTMLButtonElement>('[data-testid="guide-autoplay-toggle"]')

describe('tour voice', () => {
  it('offers no voice when the organization has no speech key', async () => {
    status.mockResolvedValue(false)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(progress()).toBe('1 of 2'))
    await Promise.resolve()
    expect(voiceButton()).toBeNull()
    expect(fetchSpeech).not.toHaveBeenCalled()
  })

  it('stays quiet until the user turns the voice on, then reads the card and fetches the next', async () => {
    status.mockResolvedValue(true)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(voiceButton()).not.toBeNull())
    expect(voiceButton()!.getAttribute('aria-pressed')).toBe('false')
    expect(autoButton()!.hidden).toBe(true)
    expect(fetchSpeech).not.toHaveBeenCalled()

    voiceButton()!.click()
    await vi.waitFor(() => expect(played).toHaveLength(1))
    expect(fetchSpeech).toHaveBeenCalledWith('First card.', 'Welcome, this is the first card.')
    expect(fetchSpeech).toHaveBeenCalledWith('Second card.', undefined)
    expect(voiceButton()!.getAttribute('aria-pressed')).toBe('true')
    expect(autoButton()!.hidden).toBe(false)
  })

  it('with Auto-play, the next step comes when the reading ends', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    setAutoPlayOn(true)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(played).toHaveLength(1))
    expect(progress()).toBe('1 of 2')
    played[0].dispatchEvent(new Event('ended'))
    await vi.waitFor(() => expect(progress()).toBe('2 of 2'))
    await vi.waitFor(() => expect(played).toHaveLength(2))
  })

  it('without Auto-play, the tour waits for Next; Next cuts the reading off', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(played).toHaveLength(1))
    document.querySelector<HTMLButtonElement>('.driver-popover-next-btn')!.click()
    expect(pause).toHaveBeenCalled()
    await vi.waitFor(() => expect(played).toHaveLength(2))
    expect(progress()).toBe('2 of 2')

    played[1].dispatchEvent(new Event('ended'))
    await new Promise(r => setTimeout(r, 50))
    expect(progress()).toBe('2 of 2')
  })

  it('without Auto-play, a reading that ends leaves the card where it is', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(played).toHaveLength(1))
    played[0].dispatchEvent(new Event('ended'))
    await new Promise(r => setTimeout(r, 50))
    expect(progress()).toBe('1 of 2')
  })

  it('a clip that cannot be made removes the voice controls, for the rest of the visit', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    setAutoPlayOn(true)
    fetchSpeech.mockImplementation(() => new Promise((_, reject) => setTimeout(() => reject(new Error('Your OpenAI API key was rejected')), 50)))
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(voiceButton()).not.toBeNull())
    await vi.waitFor(() => expect(voiceButton()).toBeNull())
    expect(document.querySelector('.driver-popover')!.textContent).not.toContain('rejected')
    expect(progress()).toBe('1 of 2')
    // The next tour starts silent: no controls, no new attempt.
    stopGuide()
    fetchSpeech.mockClear()
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(progress()).toBe('1 of 2'))
    await new Promise(r => setTimeout(r, 50))
    expect(voiceButton()).toBeNull()
    expect(fetchSpeech).not.toHaveBeenCalled()
  })

  it('a tour after a closed one fetches its clips again (a changed voice style is heard)', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(played).toHaveLength(1))
    document.querySelector<HTMLButtonElement>('.driver-popover-close-btn')!.click()
    await vi.waitFor(() => expect(document.querySelector('.driver-popover')).toBeNull())
    fetchSpeech.mockClear()

    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(played).toHaveLength(2))
    expect(fetchSpeech).toHaveBeenCalledWith('First card.', 'Welcome, this is the first card.')
  })

  it('closing the tour stops the voice', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(played).toHaveLength(1))
    stopGuide()
    expect(pause).toHaveBeenCalled()
  })
})

describe('tour voice and card placement', () => {
  it('places the card again when the voice controls go away', async () => {
    status.mockResolvedValue(true)
    setVoiceOn(true)
    fetchSpeech.mockImplementation(() => new Promise((_, reject) => setTimeout(() => reject(new Error('Your key was refused')), 50)))
    runGuide(steps, vi.fn())
    await vi.waitFor(() => expect(voiceButton()).not.toBeNull())
    refreshSpy.mockClear()
    await vi.waitFor(() => expect(voiceButton()).toBeNull())
    expect(refreshSpy).toHaveBeenCalled()
  })
})
