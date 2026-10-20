// Job-scoped registry of in-flight auto-propagations, keyed by source text.
//
// When a segment is confirmed, its translation is propagated to identical-source
// siblings server-side, which bumps THEIR optimistic-lock `updated_at`.
// `patchPropagatedSiblings` refreshes the cache — but only on the propagate
// RESPONSE. Between the server committing the bump and that response arriving,
// a sibling that gets activated + saved reads a stale baseline and 409s against
// the user's OWN propagation (a false conflict).
//
// A segment's save awaits any propagation targeting its own source, then adopts
// the post-propagation `updated_at` returned here. A row activated while the
// propagation was in flight, and typed into before its response, keeps the
// older version as its baseline (the row never replaces unsaved typing), so the
// fresh `updated_at` is also kept once the propagation settles, until that
// row's next save takes it. Real cross-user conflicts are untouched: only a
// version the user's own propagation wrote is adopted.

const inFlight = new Map<string, Promise<Map<number, string>>>();
/**
 * Sibling row → the `updated_at` the user's own settled propagation gave it.
 * Kept for SETTLED_TTL_MS: it only matters until the row's next save, which a
 * user typing into the row makes within seconds; older entries are dropped, so
 * the map stays small however long the session.
 */
const settled = new Map<string, { at: string; until: number }>();
const SETTLED_TTL_MS = 60_000;

function pruneSettled(now: number) {
  for (const [k, v] of settled) if (v.until <= now) settled.delete(k);
}

const keyFor = (jobId: number, sourceText: string) => `${jobId}\u0000${sourceText}`;
const rowKey = (jobId: number, translationId: number) => `${jobId}\u0000#${translationId}`;

/**
 * Register an in-flight propagation. `result` resolves (after the cache patch)
 * to a map of sibling translation id → its fresh server `updated_at`. Errors
 * resolve to an empty map so a failed propagation never blocks sibling saves.
 */
export function trackPropagation(
  jobId: number,
  sourceText: string,
  result: Promise<Map<number, string>>,
): void {
  const k = keyFor(jobId, sourceText);
  inFlight.set(k, result);
  void result
    .then(fresh => {
      const now = Date.now();
      pruneSettled(now);
      for (const [id, at] of fresh) settled.set(rowKey(jobId, id), { at, until: now + SETTLED_TTL_MS });
    })
    .catch(() => {})
  void result
    .finally(() => {
      if (inFlight.get(k) === result) inFlight.delete(k);
    })
    .catch(() => {});
}

/**
 * Resolves once any in-flight propagation to `sourceText` has settled (cache
 * patched), yielding the fresh sibling `updated_at`s. Resolves to an empty map
 * immediately when nothing is in flight — so normal saves pay no cost.
 */
export function awaitPropagation(
  jobId: number,
  sourceText: string,
): Promise<Map<number, string>> {
  const pending = inFlight.get(keyFor(jobId, sourceText));
  // Defensive: a rejected propagation must never fail the awaiting save.
  return pending ? pending.catch(() => new Map<number, string>()) : Promise.resolve(new Map());
}

/**
 * Wait for any in-flight propagation to `sourceText` to settle, then adopt the
 * post-propagation `updated_at` into `baselineRef` so the save can't 409
 * against the user's own propagation; after it settled, adopt the version it
 * gave this row once, over an older baseline. No-op (and effectively free) when
 * there is neither. Used by BOTH the confirm (saveMut) and draft (saveDraft)
 * paths.
 */
export async function reconcilePropagationBaseline(
  jobId: number,
  sourceText: string,
  translationId: number | undefined,
  baselineRef: { current: string | null },
): Promise<void> {
  const propagated = await awaitPropagation(jobId, sourceText);
  if (translationId == null) return;
  const fresh = propagated.get(translationId);
  if (fresh) {
    baselineRef.current = fresh;
    settled.delete(rowKey(jobId, translationId));
    return;
  }
  // A propagation that settled before this save: adopt its version only over
  // an older baseline, and only once.
  const key = rowKey(jobId, translationId);
  const entry = settled.get(key);
  if (!entry) return;
  settled.delete(key);
  if (entry.until <= Date.now()) return;
  const after = entry.at;
  const base = baselineRef.current ? Date.parse(baselineRef.current) : NaN;
  if (Number.isNaN(base) || base < Date.parse(after)) baselineRef.current = after;
}
