package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/ptr"
)

// A JSON file whose strings are HTML exports a confirmed translation's chips
// as the source's tags; the same file parsed as plain text exports the
// translation as is.
func TestExportFile_JSONStringsInTheirParsedMode(t *testing.T) {
	const source = `{"k": "Click <a href=\"https://example.com/\">here</a> now"}`
	const rich = `Klick <g id="1" ctype="x-html-a">hier</g> jetzt`
	plain := "Klick hier jetzt"
	literal := "Nutze <b> für fett"

	for _, c := range []struct {
		name       string
		inlineHTML bool
		text, rich *string
		want       string
	}{
		{"html", true, &plain, ptr.To(rich), `Klick <a href="https://example.com/">hier</a> jetzt`},
		{"plain text", false, &literal, nil, literal},
	} {
		t.Run(c.name, func(t *testing.T) {
			mq := newMockQuerier()
			mq.addFileProject(1, 1, nil, "en", []string{"de"})
			file := mq.files[1]
			file.InlineHtml = c.inlineHTML
			mq.files[1] = file
			key := "k"
			mq.approvedForExport[fileLangKey(1, "de")] = []db.ListApprovedForExportRow{
				{SegmentKey: &key, ActiveText: c.text, ActiveRichText: c.rich, Status: "approved"},
			}
			svc := NewService(mq, &passthroughTxRunner{q: mq}, memStorage(source))
			out, _, err := svc.ExportFile(context.Background(), 1, 1, "de")
			if err != nil {
				t.Fatalf("ExportFile: %v", err)
			}
			var got map[string]string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode export: %v\n%s", err, out)
			}
			if got["k"] != c.want {
				t.Errorf("exported %q, want %q", got["k"], c.want)
			}
		})
	}
}
