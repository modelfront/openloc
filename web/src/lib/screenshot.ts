// A screenshot of the page as the user sees it, taken in the browser with
// html-to-image: nothing leaves the browser until the user approves it. The
// assistant panel and anything marked data-screenshot="exclude" stay out.
import { toJpeg } from 'html-to-image'
import { onSupportPage } from './issue-context'

/** The server's cap for one screenshot. */
const MAX_SCREENSHOT_BYTES = 5 * 1024 * 1024

/** A capture that takes longer has hung: the library can wait forever on an image it cannot decode. */
const CAPTURE_TIMEOUT_MS = 15_000

/** Tried in turn until the image fits the cap. */
const QUALITIES = [0.85, 0.6, 0.4]

/** Keeps a node in the capture unless it is marked data-screenshot="exclude". */
export const keptInCapture = (node: HTMLElement) => !(node instanceof HTMLElement && node.dataset?.screenshot === 'exclude')

async function dataUrlToBlob(url: string): Promise<Blob> {
  return (await fetch(url)).blob()
}

/** The visible page as a JPEG file, under MAX_SCREENSHOT_BYTES; fails after CAPTURE_TIMEOUT_MS. */
export function capturePage(): Promise<File> {
  if (onSupportPage()) {
    return Promise.reject(new Error('Screenshots are not taken on the support team’s pages.'))
  }
  let timer: ReturnType<typeof setTimeout> | undefined
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error('Taking the screenshot took too long.')), CAPTURE_TIMEOUT_MS)
  })
  return Promise.race([render(), timeout]).finally(() => clearTimeout(timer))
}

async function render(): Promise<File> {
  const background = getComputedStyle(document.body).backgroundColor
  for (const quality of QUALITIES) {
    // The app's web fonts are left out: embedding them is most of the work,
    // and the browser's fallback fonts show the problem as well.
    const url = await toJpeg(document.body, {
      quality, pixelRatio: 1, width: window.innerWidth, height: window.innerHeight,
      backgroundColor: background, filter: keptInCapture, skipFonts: true,
    })
    const blob = await dataUrlToBlob(url)
    if (blob.size <= MAX_SCREENSHOT_BYTES) return new File([blob], 'screenshot.jpg', { type: 'image/jpeg' })
  }
  throw new Error('The page is too large for a screenshot.')
}
