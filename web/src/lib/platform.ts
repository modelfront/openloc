// Platform detection for keyboard-shortcut hints. `navigator.platform` is
// deprecated but is the only synchronous signal available at module scope;
// userAgentData is async and unavailable on Safari and Firefox.
export const isMac =
  typeof navigator !== 'undefined' && navigator.platform.toUpperCase().indexOf('MAC') >= 0

// Modifier glyph for a shortcut label. Mac glyphs concatenate (`⌘Enter`);
// word forms need a separator (`Ctrl+Enter`), so callers rendering a word
// form must add the `+` themselves.
export const modKey = isMac ? '⌘' : 'Ctrl'
