// Human names for the metrics vocabulary — measures, dimensions and value
// columns — used by the chart builder.
//
// The names come from the Go registry as snake_case slugs, and the builder
// shows a few dozen of them. A hand-written map for all of them would go stale
// the day a measure is added, so the default is to derive the label and keep
// overrides only for the words derivation gets wrong: acronyms it would
// lower-case, and a handful of slugs whose plain reading is unclear.

const ACRONYMS: Record<string, string> = { mt: 'MT', ai: 'AI', qa: 'QA', tm: 'TM' }

const OVERRIDES: Record<string, string> = {
  post_edit_effort: 'Post-editing effort',
  editing_time: 'Post-editing time',
  avg_edit_distance: 'Average edit distance',
  // "kind" is the history event's type of text change (tm, mt, human…); "origin"
  // is how a translation was first filled.
  kind: 'Change type',
  predict_outcome: 'Quality outcome',
  // What the two history measures count, not their keys' wording.
  words_processed: 'Post-editing volume',
  segments_by_origin: 'Translation activity by type',
}

/** `ai_token_usage` → `AI token usage`. */
export function metricLabel(slug: string): string {
  if (OVERRIDES[slug]) return OVERRIDES[slug]
  return slug
    .split('_')
    .map((w, i) => ACRONYMS[w] ?? (i === 0 ? w.charAt(0).toUpperCase() + w.slice(1) : w))
    .join(' ')
}
