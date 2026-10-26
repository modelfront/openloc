package pptx

import (
	"context"
	"log/slog"
	"openloc/internal/logging"
	"openloc/internal/parsers/common"
)

// PPTX parses PowerPoint presentations (.pptx).
//
// It translates the text of slides — text boxes and placeholders, tables,
// grouped shapes, SmartArt — the speaker notes, and alt text. Hidden slides
// count like any other. Charts, slide masters and layouts, comments and
// embedded objects are left as they are.
//
// Each paragraph splits into sentences. Export rebuilds a paragraph only
// when one of its sentences has a translation and copies every other part of
// the package byte for byte, so the deck keeps everything this parser does
// not touch. Keys are the part name plus the element path, which are stable
// for the same file.
type PPTX struct{}

// pptxErrors are the PPTX sentinels for the shared OOXML checks.
var pptxErrors = common.OOXMLErrors{
	Encrypted:   common.ErrPPTXEncrypted,
	TooLarge:    common.ErrPPTXTooLarge,
	Malformed:   common.ErrPPTXMalformed,
	PanicAction: "pptx-panic",
}

// recoverPPTX turns a panic into an error wrapping ErrPPTXMalformed and
// reports it with ctx's scope. Use it as `defer recoverPPTX(ctx, op, &err)`
// in a function with a named error result.
func recoverPPTX(ctx context.Context, op string, err *error) {
	if rec := recover(); rec != nil {
		common.OOXMLPanicError(ctx, rec, op, pptxErrors, err)
	}
}

var _ common.LangAwareReconstructor = PPTX{}

func (p PPTX) Parse(content []byte) (_ []common.Segment, err error) {
	defer recoverPPTX(context.Background(), "pptx", &err)
	pkg, err := openPPTX(content)
	if err != nil {
		return nil, err
	}
	units, err := collectPPTX(pkg)
	if err != nil {
		return nil, err
	}
	var segments []common.Segment
	for _, u := range units {
		for i, s := range u.sentences() {
			segments = append(segments, common.Segment{
				Key:      u.sentenceKey(i),
				Text:     s.text,
				RichText: s.rich,
				Position: len(segments),
				Context:  u.context,
			})
		}
	}
	if len(segments) == 0 {
		return nil, common.ErrPPTXEmpty
	}
	return segments, nil
}

// Reconstruct writes translations without knowing the job's languages, so
// run languages stay as they are. The export uses ReconstructForLang.
func (p PPTX) Reconstruct(original []byte, translations map[string]string) ([]byte, error) {
	out, _, err := reconstructPPTX(context.Background(), original, translations, common.ExportLangs{})
	return out, err
}

// ReconstructForLang writes translations and marks translated text that was
// in the source language with the target language.
func (p PPTX) ReconstructForLang(ctx context.Context, original []byte, translations map[string]string, langs common.ExportLangs) ([]byte, error) {
	out, unmirrored, err := reconstructPPTX(ctx, original, translations, langs)
	if err == nil && unmirrored > 0 {
		logging.Warn(ctx, nil, "SmartArt drawing paragraphs kept their source text", "pptx-smartart-unmirrored",
			slog.Int("paragraphs", unmirrored))
	}
	return out, err
}

// reconstructPPTX rebuilds the deck. unmirrored counts the SmartArt drawing
// paragraphs of translated diagrams that keep their source text.
func reconstructPPTX(ctx context.Context, original []byte, translations map[string]string, langs common.ExportLangs) (_ []byte, unmirrored int, err error) {
	defer recoverPPTX(ctx, "pptx reconstruct", &err)
	pkg, err := openPPTX(original)
	if err != nil {
		return nil, 0, err
	}
	units, err := collectPPTX(pkg)
	if err != nil {
		return nil, 0, err
	}

	edits := map[string][]common.SpliceEdit{}
	for _, u := range units {
		sentences := u.sentences()
		trans := make([]string, len(sentences))
		has := make([]bool, len(sentences))
		any := false
		for i := range sentences {
			if t := translations[u.sentenceKey(i)]; t != "" {
				trans[i], has[i], any = t, true, true
			}
		}
		if !any {
			continue
		}
		if u.alt != nil {
			edits[u.part] = append(edits[u.part], common.SpliceEdit{Rng: u.alt.valueRange, Text: u.alt.rebuild(trans, has)})
			continue
		}
		edits[u.part] = append(edits[u.part], paragraphEdit(u.para, trans, has, langs))
		for _, m := range u.mirrors {
			edits[m.part] = append(edits[m.part], paragraphEdit(m.para, trans, has, langs))
		}
		unmirrored += u.unmirrored
	}

	changed := make(map[string][]byte, len(edits))
	for part, e := range edits {
		changed[part] = []byte(common.Splice(pkg.docs[part].Src, e))
	}
	out, err := pkg.rewrite(changed)
	if err != nil {
		return nil, 0, err
	}
	return out, unmirrored, nil
}

func paragraphEdit(p *pptxParagraph, trans []string, has []bool, langs common.ExportLangs) common.SpliceEdit {
	return common.SpliceEdit{
		Rng:  common.TextRange{Start: p.node.OuterStart, End: p.node.OuterEnd},
		Text: p.rebuild(trans, has, langs),
	}
}
