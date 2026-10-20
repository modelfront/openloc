/**
 * TM/resource assignment mode is stored as one of 'read' | 'write' | 'read_write',
 * but the UI presents it as two independent Read / Write checkboxes. These
 * helpers convert between the two representations.
 * At least one flag is always set — 'read_write' means both boxes ticked.
 */
export const modeHasRead = (mode?: string) => mode === 'read' || mode === 'read_write'
export const modeHasWrite = (mode?: string) => mode === 'write' || mode === 'read_write'

/** Combine the two checkboxes back into the stored enum. Falls back to 'read'
 *  if somehow neither is set (callers prevent that — a TM with no mode is
 *  meaningless). */
export const flagsToMode = (read: boolean, write: boolean): string =>
  read && write ? 'read_write' : write ? 'write' : 'read'

/**
 * Toggle one checkbox for a given current mode, keeping at least one checked.
 * Returns the new mode string, or null if the toggle would clear both (caller
 * should ignore the change so a TM is never left doing nothing).
 */
export function toggleMode(mode: string | undefined, which: 'read' | 'write'): string | null {
  let read = modeHasRead(mode)
  let write = modeHasWrite(mode)
  if (which === 'read') read = !read
  else write = !write
  if (!read && !write) return null
  return flagsToMode(read, write)
}
