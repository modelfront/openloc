import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest'
import Honeybadger from '@honeybadger-io/js'
import { initErrtrack, isCrawler, notify, resetThrottle } from './errtrack'

vi.mock('@honeybadger-io/js', () => ({
  default: { configure: vi.fn(), beforeNotify: vi.fn(), notify: vi.fn() },
}))

const GOOGLEBOT_MOBILE =
  'Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.8010.52 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)'
const CHROME_DESKTOP =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36'

const chunkError = () =>
  new TypeError('Failed to fetch dynamically imported module: https://docs.example.com/assets/PublicDocsPage-D0dIGBlf.js')

describe('isCrawler', () => {
  it.each([
    ['Googlebot smartphone', GOOGLEBOT_MOBILE, true],
    ['Googlebot desktop', 'Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)', true],
    ['Bingbot', 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm) Chrome/116.0.1938.76 Safari/537.36', true],
    ['GPTBot', 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.1; +https://openai.com/gptbot', true],
    ['ClaudeBot', 'Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)', true],
    ['Google inspection tool', 'Mozilla/5.0 (compatible; Google-InspectionTool/1.0;)', true],
    ['Chrome desktop', CHROME_DESKTOP, false],
    ['Safari iPhone', 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1', false],
    ['a phone brand ending in "bot"', 'Mozilla/5.0 (Linux; Android 12; CUBOT X50 Build/SP1A.210812.016) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36', false],
    ['empty', '', false],
  ])('%s → %s', (_label, ua, want) => {
    expect(isCrawler(ua)).toBe(want)
  })
})

describe('reporting from a crawler', () => {
  let ua = CHROME_DESKTOP
  beforeAll(async () => {
    vi.stubGlobal('fetch', () => Promise.resolve(new Response(JSON.stringify({ error_reporting: true }))))
    await initErrtrack()
    vi.unstubAllGlobals()
  })
  beforeEach(() => {
    resetThrottle()
    vi.mocked(Honeybadger.notify).mockClear()
    vi.spyOn(navigator, 'userAgent', 'get').mockImplementation(() => ua)
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('drops a chunk-load error from a crawler', () => {
    ua = GOOGLEBOT_MOBILE
    notify(chunkError(), { name: 'ChunkLoadError' })
    expect(Honeybadger.notify).not.toHaveBeenCalled()
  })

  it('still reports any other error from a crawler', () => {
    ua = GOOGLEBOT_MOBILE
    notify(new Error('something broke'), { name: 'ReactRenderError' })
    expect(Honeybadger.notify).toHaveBeenCalledTimes(1)
  })

  it('still reports a chunk-load error from a person', () => {
    ua = CHROME_DESKTOP
    notify(chunkError(), { name: 'ChunkLoadError' })
    expect(Honeybadger.notify).toHaveBeenCalledTimes(1)
  })
})
