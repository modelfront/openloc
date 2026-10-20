package mt

import (
	"context"
	"slices"

	"openloc/internal/logging"
)

// chineseScripts lists the codes that name each Chinese script: the script
// subtag first, then the regions that write it, then the vendor code for it
// ("zt" is LibreTranslate's Traditional Chinese).
var chineseScripts = []struct {
	codes       []string
	traditional bool
}{
	{codes: []string{"zh-hant", "zh-tw", "zh-hk", "zh-mo", "zt"}, traditional: true},
	{codes: []string{"zh-hans", "zh-cn", "zh-sg", "zh-my"}},
}

// anyLanguage is an engine that takes every language pair as named, so no
// locale is resolved for it.
type anyLanguage interface{ takesAnyLanguage() }

// resolveCode maps a locale onto the closest code a set contains:
//
//  1. Direct match — the set has "es-mx", use "es-mx".
//  2. Script match — a Chinese code takes the set's code for the same script
//     (zh-tw → zh-hant, zh-hant → zh-tw). A Traditional code stops here: the
//     bare "zh" means Simplified to every vendor.
//  3. Base-code fallback — strip the region, and use "es" if the set has it.
//  4. Passthrough — return the original and let the engine produce its own
//     error, which is more informative than one we invent.
//
// ok is false only for the passthrough case.
func resolveCode(supported map[string]bool, code string) (resolved string, ok bool) {
	if supported[code] {
		return code, true
	}
	for _, script := range chineseScripts {
		if !slices.Contains(script.codes, code) {
			continue
		}
		for _, alt := range script.codes {
			if supported[alt] {
				return alt, true
			}
		}
		if script.traditional {
			return code, false
		}
	}
	if base := baseCode(code); base != "" && supported[base] {
		return base, true
	}
	return code, false
}

// codeSet indexes a language list.
func codeSet(langs []LanguageInfo) map[string]bool {
	out := make(map[string]bool, len(langs))
	for _, l := range langs {
		out[l.Code] = true
	}
	return out
}

// targetSet indexes the targets one source language reaches: the catalog's
// target list, unless the engine publishes a list of its own for that source.
// nil when the source is not in the catalog at all.
func targetSet(cat Catalog, source string) map[string]bool {
	if cat.PerSource != nil {
		targets, ok := cat.PerSource[source]
		if !ok {
			return nil
		}
		out := make(map[string]bool, len(targets))
		for _, t := range targets {
			out[t] = true
		}
		// A per-source list still gets base-code fallback, the same as a
		// regional code in the catalog-wide list.
		for t := range out {
			if base := baseCode(t); base != "" {
				out[base] = true
			}
		}
		return out
	}
	if !codeSet(cat.Sources)[source] {
		return nil
	}
	return codeSet(cat.Targets)
}

// ResolveLocale maps a target locale onto the closest one an engine writes.
//
// Prefer ResolveLocalePair when both sides are known: it resolves the target
// against the targets the resolved SOURCE reaches, which is stricter for an
// engine that publishes per-source lists.
func ResolveLocale(ctx context.Context, targetLang string, engine Engine) string {
	if _, ok := engine.(anyLanguage); ok {
		return targetLang
	}
	cat := SupportedLanguages(ctx, engine)
	if cat.Empty() {
		return targetLang
	}
	resolved, ok := resolveCode(codeSet(cat.Targets), targetLang)
	return logResolved(ctx, engine, targetLang, resolved, ok)
}

// logResolved records any locale the engine could not take as asked. Silence
// would hide a project quietly translating into a neighbouring language.
func logResolved(ctx context.Context, engine Engine, want string, got string, ok bool) string {
	if ok && want == got {
		return got
	}
	msg := "engine does not support " + want + ", falling back to " + got
	if !ok {
		msg = "engine does not support " + want + " and has no base-code fallback, passing it through"
	}
	name := ""
	if engine != nil {
		name = engine.Name()
	}
	logging.Info(ctx, msg, "mt-locale-resolved",
		logging.Engine(name), logging.Debugf("requested", want), logging.Debugf("resolved", got))
	return got
}

// ResolveLocalePair maps a job's source and target locales onto codes the
// engine accepts, each against its own side of the catalog: the source against
// the engine's source languages, the target against the targets that source
// reaches.
//
// The two sides are resolved separately because a catalog can be asymmetric.
// DeepL is the case in hand: it lists EN and PT as sources, and EN-GB, EN-US,
// PT-BR and PT-PT as targets, never the reverse. Resolving both sides against
// one merged set would send source_lang=EN-GB, which DeepL does not accept.
func ResolveLocalePair(ctx context.Context, sourceLang, targetLang string, engine Engine) (string, string) {
	if _, ok := engine.(anyLanguage); ok {
		return sourceLang, targetLang
	}
	cat := SupportedLanguages(ctx, engine)
	if cat.Empty() {
		return sourceLang, targetLang
	}

	source, srcOK := resolveCode(codeSet(cat.Sources), sourceLang)
	src := logResolved(ctx, engine, sourceLang, source, srcOK)

	targets := targetSet(cat, source)
	if targets == nil {
		// The source is not one the catalog lists, so it names no target set to
		// check against. Pass the target through.
		return src, targetLang
	}
	tgt, tgtOK := resolveCode(targets, targetLang)
	return src, logResolved(ctx, engine, targetLang, tgt, tgtOK)
}
