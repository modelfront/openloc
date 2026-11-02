package editor

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/inlinetag"
)

// buildIndex turns the index rows into one array per field and marks
// repetitions: two segments repeat when their source text is identical.
func buildIndex(rows []db.ListEditorIndexRow) Index {
	n := len(rows)
	idx := Index{
		SegmentIDs: make([]int64, n), TranslationIDs: make([]int64, n), Positions: make([]int32, n),
		SubPositions: make([]int32, n),
		WordCounts:   make([]int32, n), Statuses: make([]string, n),
		HasTarget: make([]bool, n), StepEdited: make([]bool, n), Predicted: make([]bool, n),
		Origins: make([]string, n), TMMatch: make([]*int16, n), Locked: make([]bool, n),
		InFlight: make([]string, n), ConfirmedVia: make([]string, n), QENoteHeads: make([]string, n),
		Repetition: make([]int8, n), JoinsNext: make([]bool, n),
	}
	first := make(map[string]int, n)
	for i, r := range rows {
		idx.SegmentIDs[i] = r.SegmentID
		idx.Positions[i] = r.Position
		idx.SubPositions[i] = r.SubPosition
		idx.WordCounts[i] = r.WordCount
		idx.TranslationIDs[i] = r.TranslationID
		idx.Statuses[i] = string(r.Status)
		idx.HasTarget[i] = r.HasTarget
		idx.StepEdited[i] = r.StepEdited
		idx.Predicted[i] = r.PredictOutcome.Valid
		if r.Origin.Valid {
			idx.Origins[i] = string(r.Origin.TranslationOrigin)
		}
		idx.TMMatch[i] = r.TmMatchPercent
		idx.Locked[i] = r.Locked
		if r.InFlightPhase.Valid {
			idx.InFlight[i] = string(r.InFlightPhase.TranslationPhase)
		}
		idx.ConfirmedVia[i] = r.ConfirmedVia
		idx.QENoteHeads[i] = r.QeNoteHead

		if j, seen := first[r.SourceText]; seen {
			idx.Repetition[j] = RepetitionSource
			idx.Repetition[i] = RepetitionRepeat
			idx.RepetitionStats.Segments++
			idx.RepetitionStats.Words += len(strings.Fields(r.SourceText))
			// Counted in UTF-16 code units, as the leverage bar counts characters.
			idx.RepetitionStats.Characters += len(utf16.Encode([]rune(r.SourceText)))
		} else {
			first[r.SourceText] = i
		}
	}
	for i := 0; i+1 < n; i++ {
		idx.JoinsNext[i] = rows[i].Position == rows[i+1].Position ||
			sameBlock(rows[i].FileFormat, rows[i].UnitKey, rows[i+1].UnitKey)
	}
	return idx
}

// Sentence and range keys: "<block>#<n>" and "<block>#<n>-<m>" where a
// block splits into sentences, "<trans-unit>:<mid>" and
// "<trans-unit>:<mid>~<mid>" for an XLIFF trans-unit's <mrk> sentences.
var (
	numberedSentenceKey = regexp.MustCompile(`^(.*)#\d+(?:-\d+)?$`)
	mrkSentenceKey      = regexp.MustCompile(`^(.*):[^:~]+(?:~[^:~]+)?$`)
)

// sameBlock reports whether two keys look like sentences of one block in a
// format whose sentences a join can put together. It only decides where the
// editor offers a join; the join reads the file itself.
func sameBlock(format db.FileFormat, a, b string) bool {
	var re *regexp.Regexp
	switch format {
	case db.FileFormatTxt, db.FileFormatHtml, db.FileFormatMarkdown, db.FileFormatJsonI18n,
		db.FileFormatDocx, db.FileFormatPdf, db.FileFormatXml, db.FileFormatPptx:
		re = numberedSentenceKey
	case db.FileFormatXliff:
		re = mrkSentenceKey
	default:
		return false
	}
	ma, mb := re.FindStringSubmatch(a), re.FindStringSubmatch(b)
	return ma != nil && mb != nil && ma[1] == mb[1]
}

var refKeyRe = regexp.MustCompile(`\bref_key="([^"]*)"`)

var refKeyUnescape = strings.NewReplacer("&quot;", `"`, "&lt;", "<", "&gt;", ">", "&amp;", "&")

// refKeys returns every segment key a cross-reference chip in these tagged
// sources points at, decoded, sorted and without duplicates.
func refKeys(richSources []string) []string {
	seen := map[string]bool{}
	for _, rich := range richSources {
		for _, tag := range inlinetag.Tags(rich) {
			if m := refKeyRe.FindStringSubmatch(tag.Raw); m != nil {
				seen[refKeyUnescape.Replace(m[1])] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
