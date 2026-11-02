import { find } from 'linkifyjs'
import { TextLink } from './text-link'

const WEB_ADDRESS = /^https?:\/\//i

// Chinese, Japanese, Korean, Thai, Lao, Khmer and Myanmar are written with
// no space after an address ("請看https://example.com。謝謝"), so a run of
// their characters, or of full-width punctuation, ends one. An address whose
// host is in these scripts stays text.
const NO_SPACE_RUN =
  /[\p{sc=Han}\p{sc=Hiragana}\p{sc=Katakana}\p{sc=Hangul}\p{sc=Thai}\p{sc=Lao}\p{sc=Khmer}\p{sc=Myanmar}\u3000-\u303F\u30FC\uFF00-\uFFEF]+/gu

type Part = string | { url: string }

/** `text` cut into plain text and the http(s) addresses in it. */
export function splitLinks(text: string): Part[] {
  const parts: Part[] = []
  const addLinks = (piece: string) => {
    let last = 0
    for (const m of find(piece, 'url')) {
      if (!WEB_ADDRESS.test(m.value)) continue
      parts.push(piece.slice(last, m.start), { url: m.value })
      last = m.end
    }
    parts.push(piece.slice(last))
  }
  let last = 0
  for (const run of text.matchAll(NO_SPACE_RUN)) {
    addLinks(text.slice(last, run.index))
    parts.push(run[0])
    last = run.index + run[0].length
  }
  addLinks(text.slice(last))
  return parts.filter(p => p !== '')
}

/**
 * Plain text as the person typed it, with each http(s) address made a link
 * that opens in a new tab. Not Markdown and not HTML: `*`, `[x](y)` and tags
 * stay text.
 */
export function LinkedText({ text }: { text: string }) {
  return (
    <>
      {splitLinks(text).map((p, i) =>
        typeof p === 'string' ? (
          p
        ) : (
          <TextLink key={i} href={p.url} target="_blank" rel="noreferrer" underlined>
            {p.url}
          </TextLink>
        ),
      )}
    </>
  )
}
