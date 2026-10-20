/** Escapes every RegExp metacharacter, so `s` matches itself literally. */
export const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
