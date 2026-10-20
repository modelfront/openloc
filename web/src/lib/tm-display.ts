/**
 * Display policy for a TM's language coverage, shared by the TM list and the
 * project's TM assignment panel so the same TM summarises identically in both.
 */

/**
 * Target languages shown before the rest collapse into a `+N` chip. Passed as
 * `max` to `<LanguagePair>`. A TM may declare dozens of locales; without a cap
 * one wide TM pushes the customer and entry count off the end of its row.
 */
export const TM_LANGS_SHOWN = 5
