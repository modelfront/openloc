/**
 * The message to show for a caught error: its own message when it has one,
 * else `fallback` (a thrown string, `undefined` or an Error with no message).
 */
export function errorText(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback
}
