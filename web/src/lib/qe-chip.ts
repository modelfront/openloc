/** The QE chip's text for a stored note: the score when it is short, else "QE". */
export function qeChipLabel(note: string | null | undefined): string | null {
  if (note == null || note === '') return null
  return note.length <= 5 ? `QE ${note}` : 'QE'
}
