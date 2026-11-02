package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A segment row is either shared by every target language of its file
// (target_language NULL) or belongs to one language, made by a split or join.
// A job's rows are the segments that own a translation in its language. So a
// query that reads segments must either reach them through translations of
// one language, or say which rows it wants with target_language. Without
// that, a job sees another language's pieces, or a shared row and the pieces
// that replaced it, and counts both.
//
// segmentScopeExempt lists the queries that read segments by id, or every row
// of a file on purpose, with the reason.
var segmentScopeExempt = map[string]string{
	"GetSegment":                      "one segment by id, reached from a translation",
	"GetSegmentTenant":                "one segment by id",
	"DeleteSegmentsByFile":            "a draft file has no jobs, so no language rows",
	"PurgeDeletedProjectSegmentBatch": "purges every row of a deleted project",
	"ClearTranslationsByProject":      "clears every translation of the project, any language",
	"GetProductivityByTenant":         "per translation, joined by its own segment_id",
	"SetQAResultIgnored":              "one QA result by translation id",
	"GetTranslationTenant":            "one translation by id",
	"GetTranslationInFileTenant":      "one translation by id",
	"GetTranslationConflictDetails":   "one translation by id",
	"GetTranslationFileID":            "one translation by id",
	"DeleteJobLanguageSegments":       "deletes one language's rows by target_language",
	"DeleteLanguageSegments":          "language rows by id",
	"SnapshotHistoryCounts":           "one translation's own segment",
	"MoveTranslationHistory":          "one translation's own segment",
	"RenumberLanguageRows":            "one job's language rows, by target_language",
}

var (
	queryNameRe   = regexp.MustCompile(`(?m)^-- name: (\w+) `)
	readsSegments = regexp.MustCompile(`\b(FROM|JOIN)\s+segments\b`)
	leftJoinTrans = regexp.MustCompile(`LEFT\s+JOIN\s+translations\b`)
)

func TestSegmentQueriesAreLanguageScoped(t *testing.T) {
	files, err := filepath.Glob("../*/queries.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("found %d queries.sql files; the glob no longer reaches them", len(files))
	}
	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range splitQueries(string(raw)) {
			code := stripSQLComments(body)
			if !readsSegments.MatchString(code) {
				continue
			}
			checked++
			if _, ok := segmentScopeExempt[name]; ok {
				continue
			}
			viaTranslations := strings.Contains(code, "translations") && strings.Contains(code, "target_language")
			sharedOnly := strings.Contains(code, "target_language IS NULL")
			switch {
			case leftJoinTrans.MatchString(code):
				t.Errorf("%s (%s) LEFT JOINs translations: a shared row a split replaced in that language still comes back; use JOIN", name, f)
			case !viaTranslations && !sharedOnly:
				t.Errorf("%s (%s) reads segments with no language scope: join translations of one language, filter target_language, or add it to segmentScopeExempt with the reason", name, f)
			}
		}
	}
	if checked < 40 {
		t.Fatalf("checked %d segment queries; the parser no longer finds them", checked)
	}
	for name := range segmentScopeExempt {
		found := false
		for _, f := range files {
			raw, _ := os.ReadFile(f)
			if strings.Contains(string(raw), "-- name: "+name+" ") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("segmentScopeExempt names %s, which no queries.sql defines", name)
		}
	}
}

// splitQueries maps each sqlc query name to its text.
func splitQueries(src string) map[string]string {
	out := map[string]string{}
	locs := queryNameRe.FindAllStringSubmatchIndex(src, -1)
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[src[loc[2]:loc[3]]] = src[loc[1]:end]
	}
	return out
}

func stripSQLComments(body string) string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
