import type { ChipColor } from '@/components/ui/chip';

/**
 * Format a deadline for display in the user's locale + browser timezone, with
 * the zone's offset so nobody has to guess it: "Sep 15, 2026, 5:00 PM GMT+2".
 * Empty string for null/invalid.
 */
export function formatDeadline(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZoneName: 'shortOffset',
  });
}

// --- Deadline chip ---------------------------------------------------------
//
// Red for overdue, yellow for ≤24h, blue for 1–7 days, grey later. The yellow
// band lines up with the deadline cron's 24h notification window — when the
// chip turns yellow, the cron has fired (or fires at the next hourly tick), so
// visual and email signals stay in lockstep. The blue band lets a PM scanning
// the list see which deadlines are next without any of them reading as urgent.

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

/**
 * The five visual bands any deadline collapses into. Exported so downstream
 * consumers (tests, list filters that match by band) reference one type and
 * the band names can be renamed under a compiler check rather than a grep.
 */
export type DeadlineBand = 'overdue' | 'approaching' | 'soon' | 'far' | 'done' | 'none';

export interface DeadlineChip {
  band: DeadlineBand;
  text: string;
  color: ChipColor;
  /** Long-form tooltip (full local datetime). */
  title: string;
}

/**
 * Band + text + colour + tooltip for a deadline at a given reference time.
 * `now` is injectable for deterministic tests; production callers omit it.
 * `done` is finished work (a completed project or job): it is never late or
 * urgent, so it shows its due date in grey.
 */
export function computeDeadlinePill(
  deadline: string | null | undefined,
  now: Date = new Date(),
  done = false,
): DeadlineChip {
  if (!deadline) {
    return { band: 'none', text: 'No due date', color: 'grey', title: 'No due date' };
  }

  const target = new Date(deadline);
  if (isNaN(target.getTime())) {
    return { band: 'none', text: '—', color: 'grey', title: 'Invalid deadline' };
  }

  const diffMs = target.getTime() - now.getTime();
  const fullLocal = formatDeadline(deadline);

  if (done) {
    return { band: 'done', text: `Due ${shortDate(target, now)}`, color: 'grey', title: `Deadline ${fullLocal}` };
  }

  if (diffMs < 0) {
    // Overdue. Render magnitude in the largest sensible unit.
    return {
      band: 'overdue',
      text: `Overdue ${formatRelativeMagnitude(-diffMs)}`,
      color: 'red',
      title: `Deadline was ${fullLocal}`,
    };
  }
  if (diffMs <= DAY_MS) {
    // Within 24h — matches the deadline cron's notification window.
    return {
      band: 'approaching',
      text: diffMs < 60 * 1000 ? 'Due now' : `Due in ${formatApproachingMagnitude(diffMs)}`,
      color: 'yellow',
      title: `Deadline ${fullLocal}`,
    };
  }
  if (diffMs <= 7 * DAY_MS) {
    // 1–7 days out: visible but not urgent. Always "in Nd" — day precision
    // only, since anything under 24h is the approaching band's job.
    const days = Math.ceil(diffMs / DAY_MS);
    return { band: 'soon', text: `Due in ${days}d`, color: 'blue', title: `Deadline ${fullLocal}` };
  }
  // > 7 days out: show the exact date, neutral colour.
  return { band: 'far', text: `Due ${shortDate(target, now)}`, color: 'grey', title: `Deadline ${fullLocal}` };
}

/** "Sep 18", with the year when it is not this year's. */
function shortDate(target: Date, now: Date): string {
  return target.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: target.getFullYear() !== now.getFullYear() ? 'numeric' : undefined,
  });
}

export interface DeadlineChipOptions {
  /**
   * Extended text: the relative band plus the exact local date, time and
   * zone ("Due in 3d · Oct 1, 5:00 PM GMT+2"). For views where the reader
   * plans around the hour and cannot hover for the tooltip — the Tasks page.
   */
  exact?: boolean;
  /** Injectable for deterministic tests; production callers omit it. */
  now?: Date;
  /** Injectable for deterministic tests; defaults to the browser's zone. */
  timeZone?: string;
  /** Finished work (a completed project or job): never late or urgent. */
  done?: boolean;
}

/**
 * Spreadable <Chip> props for a deadline: `<Chip {...deadlineChipProps(iso)} />`.
 *
 * Band, colour, text and tooltip are one unit — a call site that picked them
 * off `computeDeadlinePill` by hand would either repeat the call per prop or
 * need a statement inside JSX.
 */
export function deadlineChipProps(
  deadline: string | null | undefined,
  { exact = false, now = new Date(), timeZone, done = false }: DeadlineChipOptions = {},
): {
  color: ChipColor;
  title: string;
  children: string;
  'data-band': DeadlineBand;
  'data-testid': string;
} {
  const r = computeDeadlinePill(deadline, now, done);
  return {
    color: r.color,
    title: r.title,
    children: exact && deadline ? exactChipText(r, new Date(deadline), now, timeZone) : r.text,
    'data-band': r.band,
    'data-testid': 'deadline-pill',
  };
}

/**
 * The extended chip text. The far and done bands' text is already a date, so
 * it gains only the time; the other bands keep their relative text and add
 * the date.
 */
function exactChipText(r: DeadlineChip, target: Date, now: Date, timeZone?: string): string {
  if (r.band === 'none') return r.text;
  const sameYear = target.getFullYear() === now.getFullYear();
  const exact = target.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    year: sameYear ? undefined : 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    timeZoneName: 'short',
    timeZone,
  });
  return r.band === 'far' || r.band === 'done' ? `Due ${exact}` : `${r.text} · ${exact}`;
}

/**
 * Largest sensible unit, always positive; the caller adds the "Overdue " /
 * "Due in " prefix.
 *
 * Rounding asymmetry, intentional: this uses `Math.round` (closest unit) while
 * the soon band's day count uses `Math.ceil` (always up).
 *   - Overdue/approaching answers "how stale is this?" / "how soon must I
 *     act?", where closest-unit reads naturally (5h 29m → "5h", not "6h").
 *   - The soon band answers "how much headroom do I have?", where rounding up
 *     errs toward more buffer — how PMs reason about deliverables.
 */
function formatRelativeMagnitude(ms: number): string {
  if (ms < HOUR_MS) {
    const mins = Math.max(1, Math.round(ms / (60 * 1000)));
    return `${mins}m`;
  }
  if (ms < DAY_MS) {
    const hours = Math.round(ms / HOUR_MS);
    return `${hours}h`;
  }
  const days = Math.round(ms / DAY_MS);
  return `${days}d`;
}

/**
 * Approaching-band variant — caps at hour precision even at ms close to 24h.
 * Without the cap the user sees "Due in 23h" → "Due in 1d" → "Due in 2d" in
 * adjacent minutes. Capped, the approaching band always reads in hours (or
 * minutes under 1h) and the soon band always in days, so the text and the
 * colour change together at exactly 24h.
 */
function formatApproachingMagnitude(ms: number): string {
  if (ms < HOUR_MS) {
    const mins = Math.max(1, Math.round(ms / (60 * 1000)));
    return `${mins}m`;
  }
  const hours = Math.round(ms / HOUR_MS);
  return `${hours}h`;
}

