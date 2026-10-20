// Voice for guided tours: each card read aloud by the
// server's text-to-speech route. One clip plays at a time; clips are kept for
// Back and fetched one step ahead so Next starts speaking at once.
import { fetchTourSpeech, getTourSpeechStatus } from '../api'

const VOICE_KEY = 'openloc.guide.voice'
const AUTO_KEY = 'openloc.guide.autoplay'

function readPref(key: string): boolean {
  try {
    return localStorage.getItem(key) === 'on'
  } catch {
    return false
  }
}

function writePref(key: string, on: boolean) {
  try {
    localStorage.setItem(key, on ? 'on' : 'off')
  } catch {
    // Storage blocked: the choice lasts until the page reloads.
    memory[key] = on
  }
}

const memory: Record<string, boolean> = {}
const pref = (key: string) => memory[key] ?? readPref(key)

export const voiceOn = () => pref(VOICE_KEY)
export const setVoiceOn = (on: boolean) => {
  memory[VOICE_KEY] = on
  writePref(VOICE_KEY, on)
}
export const autoPlayOn = () => pref(AUTO_KEY)
export const setAutoPlayOn = (on: boolean) => {
  memory[AUTO_KEY] = on
  writePref(AUTO_KEY, on)
}

// Re-checked after a minute, so a key added in AI settings shows up without a
// reload.
const STATUS_TTL_MS = 60_000
// Set when a clip cannot be made (a refused key, the provider down): tours go
// silent, without voice controls, until the page loads again.
let broken = false

export function markVoiceBroken() {
  broken = true
}

let status: { at: number; available: Promise<boolean> } | null = null

export function voiceAvailable(): Promise<boolean> {
  if (broken) return Promise.resolve(false)
  if (!status || Date.now() - status.at > STATUS_TTL_MS) {
    status = { at: Date.now(), available: getTourSpeechStatus().catch(() => false) }
  }
  return status.available
}

const MAX_CLIPS = 40
const clips = new Map<string, Promise<string>>()

/** The part of a step the voice reads. */
export interface Spoken {
  body: string
  say?: string
}

function clip(step: Spoken): Promise<string> {
  const key = `${step.say ?? ''}\n${step.body}`
  let url = clips.get(key)
  if (!url) {
    url = fetchTourSpeech(step.body, step.say).then(blob => URL.createObjectURL(blob))
    url.catch(() => clips.delete(key))
    clips.set(key, url)
    if (clips.size > MAX_CLIPS) {
      const [oldest, oldUrl] = clips.entries().next().value!
      clips.delete(oldest)
      oldUrl.then(u => URL.revokeObjectURL(u), () => {})
    }
  }
  return url
}

export function prefetchSpeech(step: Spoken) {
  clip(step).catch(() => {})
}

let playing: HTMLAudioElement | null = null
let turn = 0

/**
 * Reads one step, stopping whatever was playing. `onEnd` runs when the clip
 * plays to the end, never when it is stopped. Rejects when the clip cannot be
 * made or the browser refuses to play it.
 */
export async function speak(step: Spoken, onEnd?: () => void): Promise<void> {
  stopSpeech()
  const mine = ++turn
  const url = await clip(step)
  if (mine !== turn) return
  const el = new Audio(url)
  playing = el
  el.onended = () => {
    if (playing !== el) return
    playing = null
    onEnd?.()
  }
  await el.play()
}

export function stopSpeech() {
  turn++
  playing?.pause()
  playing = null
}

/**
 * Drops every kept clip. Called when a tour ends, so the next tour asks the
 * server again and hears a changed voice or style.
 */
export function forgetSpeech() {
  stopSpeech()
  for (const url of clips.values()) url.then(u => URL.revokeObjectURL(u), () => {})
  clips.clear()
}

/** Test hook: forget the cached status and clips. */
export function resetVoiceForTests() {
  stopSpeech()
  status = null
  broken = false
  clips.clear()
  for (const k of Object.keys(memory)) delete memory[k]
}
