// Gaps in a project's or template's setup, shown as a warning beside the card
// title they belong to. A TM lookup matches the same base language (a de TM
// serves de-de, with the sublanguage penalty), so a target is covered by a TM
// of its base language.

import { baseLanguage, normalizeLanguage } from './lang-pair'

/** Tooltip of the Translation memory warning, or null when every target
 *  language has a TM. */
export function missingTMWarning({ sourceLanguage, targetLanguages, tmTargetLanguages }: {
  sourceLanguage: string
  targetLanguages: string[]
  /** Target languages of the TMs the project reads from. */
  tmTargetLanguages: string[]
}): string | null {
  const covered = new Set(tmTargetLanguages.map(baseLanguage))
  const missing = targetLanguages.filter(t => !covered.has(baseLanguage(t)))
  if (missing.length === 0) return null
  return `No translation memory for ${missing.map(t => `${sourceLanguage}→${t}`).join(', ')}.`
}

/** Tooltip of the Machine translation warning. */
export const MISSING_MT_ROUTER_WARNING = 'No MT router.'

/** What decides which target languages an MT router translates: the target
 *  language of each route, and the default engine that takes every other one. */
export interface RouterCoverage {
  rules: { target_lang: string }[]
  default_mt_engine_id?: number | null
  default_ai_engine_id?: number | null
}

/** The target languages the router sends to no engine, as given. Matches the
 *  server's routing: a route serves its own code and every code of the same
 *  base language, in either direction (a `fr` route serves `fr-ca`, an
 *  `es-es` route serves `es-mx`), and a default engine serves the rest. */
export function uncoveredRouterTargets(router: RouterCoverage, targetLanguages: string[]): string[] {
  if (router.default_mt_engine_id != null || router.default_ai_engine_id != null) return []
  const routed = new Set(router.rules.map(r => baseLanguage(normalizeLanguage(r.target_lang))))
  return targetLanguages.filter(t => {
    const code = normalizeLanguage(t)
    return code !== '' && !routed.has(baseLanguage(code))
  })
}

/** Tooltip of the warning beside the MT router picker, or null when the
 *  router has an engine for every target language. */
export function mtRouterCoverageWarning(router: RouterCoverage, targetLanguages: string[]): string | null {
  const missing = uncoveredRouterTargets(router, targetLanguages)
  if (missing.length === 0) return null
  const codes = missing.map(t => t.toUpperCase()).join(', ')
  const which = missing.length === 1 ? 'this language' : 'these languages'
  return `This router has no engine for ${codes}. Files in ${which} are not machine translated.`
}
