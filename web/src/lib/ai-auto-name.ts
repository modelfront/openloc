/**
 * Name for a new AI engine: what it does, with the provider config it runs on in
 * parentheses.
 *
 *   QE (OpenAI GPT-4o mini)
 *   QE+APE (Claude Opus 5)
 *   APE (Claude Opus 5)
 *   MT (Claude Opus 5)
 *
 * `modeLabel` is the *prompt's* output mode (MODE_LABELS), not the engine's
 * coarse purpose — a QE prompt and an APE prompt both make a qe_ape-purpose
 * system, and calling them both "QE+APE" would lose the distinction the author
 * just made. Extension rows carry no prompt; they pass the mode their
 * capabilities give them.
 *
 * The mode leads because it's what the engine *does*, and it's what separates
 * two systems on one provider. Being derived from the prompt, the name follows
 * a prompt swap.
 */
export function engineAutoName(providerName: string, modeLabel: string): string {
  const provider = providerName.trim()
  const mode = modeLabel.trim()
  if (!provider) return ''
  return mode ? `${mode} (${provider})` : provider
}

/**
 * `base`, or `base 2` / `base 3` / … if taken.
 *
 * Legibility, not correctness: migration 083 deliberately dropped
 * UNIQUE(tenant_id, name) on both `ai_providers` and `ai_engines`, so duplicates
 * save fine. But a derived name repeating is the normal case here (two models on
 * one token, two systems on one provider), and two identically-named rows in a
 * list are a puzzle the user has to solve by reading the chips.
 *
 * Counting starts at 2 because the unsuffixed name is the first one.
 */
export function uniqueName(base: string, taken: string[]): string {
  if (!base) return base
  const used = new Set(taken.map(n => n.trim()))
  if (!used.has(base)) return base
  // Bounded rather than while(true): a tenant with 999 identically-derived
  // providers has a different problem, and an unbounded loop here would hang
  // the render if `taken` ever contained something unexpected.
  for (let i = 2; i < 1000; i++) {
    const candidate = `${base} ${i}`
    if (!used.has(candidate)) return candidate
  }
  return base
}
