package mcp

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/langcode"
	"openloc/internal/sliceutil"
	"openloc/internal/terminology"
)

// Term base tools: the same terminology.Service as /api/terminology. Every
// method resolves the base with the tenant-scoped GetTermBase first; concept
// and term ids are then checked against that base.

func (b *directBackend) termService() *terminology.Service {
	return terminology.NewService(b.q, b.pool)
}

func (b *directBackend) termBase(ctx context.Context, id int64) (db.TermBasis, error) {
	tenantID, _ := scope(ctx)
	base, err := b.q.GetTermBase(ctx, db.GetTermBaseParams{ID: id, TenantID: tenantID})
	if err != nil {
		return db.TermBasis{}, resourceErr(err, "get term base")
	}
	return base, nil
}

func (b *directBackend) termBaseSummary(ctx context.Context, base db.TermBasis) (TermBaseSummary, error) {
	count, err := b.q.CountTermBaseEntries(ctx, base.ID)
	if err != nil {
		return TermBaseSummary{}, fmt.Errorf("count concepts: %w", err)
	}
	langs, err := b.q.ListTermBaseLanguages(ctx, base.ID)
	if err != nil {
		return TermBaseSummary{}, fmt.Errorf("list term base languages: %w", err)
	}
	return TermBaseSummary{
		TermBaseID: base.ID, Name: base.Name, Description: base.Description,
		SourceLanguage: base.SourceLanguage, Languages: sliceutil.NonNil(langs), ConceptCount: count,
	}, nil
}

func (b *directBackend) ListTermBases(ctx context.Context, _ ListTermBasesIn) (TermBaseList, error) {
	tenantID, _ := scope(ctx)
	bases, err := b.termService().ListBases(ctx, tenantID)
	if err != nil {
		return TermBaseList{}, fmt.Errorf("list term bases: %w", err)
	}
	out := make([]TermBaseSummary, 0, len(bases))
	for _, t := range bases {
		out = append(out, TermBaseSummary{
			TermBaseID: t.ID, Name: t.Name, Description: t.Description,
			SourceLanguage: t.SourceLanguage, Languages: sliceutil.NonNil(t.Languages),
			ConceptCount: t.EntryCount, CreatedBy: t.CreatedByName,
		})
	}
	return TermBaseList{TermBases: out, Count: len(out)}, nil
}

func (b *directBackend) GetTermBase(ctx context.Context, in GetTermBaseIn) (TermBaseDetail, error) {
	tenantID, _ := scope(ctx)
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return TermBaseDetail{}, err
	}
	sum, err := b.termBaseSummary(ctx, base)
	if err != nil {
		return TermBaseDetail{}, err
	}
	usage, err := b.q.CountTermBaseUsage(ctx, db.CountTermBaseUsageParams{TermBaseID: base.ID, TenantID: tenantID})
	if err != nil {
		return TermBaseDetail{}, fmt.Errorf("count term base usage: %w", err)
	}
	return TermBaseDetail{TermBaseSummary: sum, Projects: usage.ProjectCount, Templates: usage.TemplateCount}, nil
}

func conceptDetail(c terminology.ConceptDTO) ConceptDetail {
	out := ConceptDetail{ConceptID: c.ID, Definition: c.Definition, Domain: c.Domain, Terms: []ConceptTermDetail{}}
	for _, t := range c.Terms {
		out.Terms = append(out.Terms, ConceptTermDetail{
			TermID: t.ID, Language: t.Language, Text: t.Text, Status: t.Status,
			PartOfSpeech: t.PartOfSpeech, Notes: t.Notes,
		})
	}
	return out
}

func (b *directBackend) ListConcepts(ctx context.Context, in ListConceptsIn) (ConceptPage, error) {
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ConceptPage{}, err
	}
	page, err := b.termService().ListConcepts(ctx, base.ID, terminology.ConceptFilter{
		Search: in.Query, Langs: in.Languages, Cursor: in.Cursor, Limit: int32(clampLimit(in.Limit)),
	})
	if err != nil {
		return ConceptPage{}, fmt.Errorf("list concepts: %w", err)
	}
	out := ConceptPage{Concepts: []ConceptDetail{}, NextCursor: page.NextCursor, HasMore: page.HasMore, Total: page.Total}
	for _, c := range page.Concepts {
		out.Concepts = append(out.Concepts, conceptDetail(c))
	}
	return out, nil
}

func (b *directBackend) GetConcept(ctx context.Context, in GetConceptIn) (ConceptDetail, error) {
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ConceptDetail{}, err
	}
	c, err := b.termService().GetConcept(ctx, base.ID, in.ConceptID)
	if err != nil {
		return ConceptDetail{}, resourceErr(err, "get concept")
	}
	return conceptDetail(c), nil
}

func (b *directBackend) RenameTermBase(ctx context.Context, in RenameTermBaseIn) (TermBaseSummary, error) {
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return TermBaseSummary{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return TermBaseSummary{}, invalidf("name is required")
	}
	description := base.Description
	if in.Description != nil {
		description = strings.TrimSpace(*in.Description)
	}
	// UpdateTermBase is id-scoped; the id comes from the tenant-scoped lookup above.
	updated, err := b.q.UpdateTermBase(ctx, db.UpdateTermBaseParams{ID: base.ID, Name: name, Description: description})
	if err != nil {
		return TermBaseSummary{}, fmt.Errorf("update term base: %w", err)
	}
	return b.termBaseSummary(ctx, updated)
}

// termImportFormat picks csv or tbx from the argument, else the file name.
func termImportFormat(format, filename string) (string, error) {
	switch f := strings.ToLower(strings.TrimSpace(format)); f {
	case "csv", "tbx":
		return f, nil
	case "":
	default:
		return "", invalidf("format must be csv or tbx, got %q", format)
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".csv":
		return "csv", nil
	case ".tbx", ".xml":
		return "tbx", nil
	}
	return "", invalidf("cannot tell the format of %q from its name; set format to csv or tbx", filename)
}

func (b *directBackend) ImportTerms(ctx context.Context, in ImportTermsIn) (ImportTermsResult, error) {
	if err := b.needPool("importing terms"); err != nil {
		return ImportTermsResult{}, err
	}
	_, userID := scope(ctx)
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ImportTermsResult{}, err
	}
	upload, body, err := b.openUpload(ctx, in.UploadID)
	if err != nil {
		return ImportTermsResult{}, err
	}
	defer body.Close()
	format, err := termImportFormat(in.Format, upload.Filename)
	if err != nil {
		return ImportTermsResult{}, err
	}

	var res terminology.ImportResult
	switch format {
	case "csv":
		if upload.SizeBytes > terminology.MaxCSVImportSize {
			return ImportTermsResult{}, invalidf("the CSV file is larger than 10 MB; split it or use TBX")
		}
		res, err = b.termService().ImportCSV(ctx, base, userID, body, in.SourceLanguage, in.TargetLanguage)
	default:
		res, err = b.termService().ImportTBX(ctx, base, userID, body)
	}
	if err != nil {
		return ImportTermsResult{}, resourceErr(err, "import terms")
	}
	return ImportTermsResult{
		TermBaseID: base.ID, Format: format, Imported: res.Imported, Terms: res.Terms, Skipped: res.Skipped,
	}, nil
}

func (b *directBackend) ExportTerms(ctx context.Context, in ExportTermsIn) (ResourceExport, error) {
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ResourceExport{}, err
	}
	switch strings.ToLower(strings.TrimSpace(in.Format)) {
	case "", "tbx":
		if len(in.TargetLanguages) > 0 {
			return ResourceExport{}, invalidf("target_languages applies to CSV only; TBX always holds every language")
		}
		return ResourceExport{
			DownloadPath: fmt.Sprintf("/api/terminology/%d/export-tbx", base.ID),
			Filename:     base.Name + ".tbx",
		}, nil
	case "csv":
		path := fmt.Sprintf("/api/terminology/%d/export", base.ID)
		q := url.Values{}
		for _, t := range in.TargetLanguages {
			if n := langcode.Normalize(t); n != "" {
				q.Add("target", n)
			}
		}
		if len(q) > 0 {
			path += "?" + q.Encode()
		}
		return ResourceExport{DownloadPath: path, Filename: base.Name + ".csv"}, nil
	}
	return ResourceExport{}, invalidf("format must be csv or tbx, got %q", in.Format)
}

func (b *directBackend) PreviewDeleteTermBase(ctx context.Context, in DeleteTermBaseIn) (Preview, error) {
	tenantID, _ := scope(ctx)
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return Preview{}, err
	}
	count, err := b.q.CountTermBaseEntries(ctx, base.ID)
	if err != nil {
		return Preview{}, fmt.Errorf("count concepts: %w", err)
	}
	usage, err := b.q.CountTermBaseUsage(ctx, db.CountTermBaseUsageParams{TermBaseID: base.ID, TenantID: tenantID})
	if err != nil {
		return Preview{}, fmt.Errorf("count term base usage: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Delete the term base %q and its %d concept(s).", base.Name, count)}
	if usage.ProjectCount+usage.TemplateCount > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf(
			"Assigned to %d project(s) and %d template(s); its terms stop showing in their editor and QA.",
			usage.ProjectCount, usage.TemplateCount))
	}
	return p, nil
}

func (b *directBackend) DeleteTermBase(ctx context.Context, in DeleteTermBaseIn) (ResourceDeleted, error) {
	if err := b.needPool("deleting a term base"); err != nil {
		return ResourceDeleted{}, err
	}
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ResourceDeleted{}, err
	}
	if err := b.termService().DeleteBase(ctx, base.ID); err != nil {
		return ResourceDeleted{}, fmt.Errorf("delete term base: %w", err)
	}
	return ResourceDeleted{Kind: "term_base", ID: base.ID, Name: base.Name}, nil
}

func termsLabel(terms []terminology.ConceptTermDTO) string {
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		parts = append(parts, fmt.Sprintf("%s %q", t.Language, t.Text))
	}
	return strings.Join(parts, ", ")
}

func (b *directBackend) PreviewDeleteConcept(ctx context.Context, in DeleteConceptIn) (Preview, error) {
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return Preview{}, err
	}
	c, err := b.termService().GetConcept(ctx, base.ID, in.ConceptID)
	if err != nil {
		return Preview{}, resourceErr(err, "get concept")
	}
	p := Preview{Summary: fmt.Sprintf("Delete concept %d from the term base %q.", c.ID, base.Name)}
	if len(c.Terms) > 0 {
		p.Effects = append(p.Effects, fmt.Sprintf("Its %d term(s) stop matching in the editor and QA: %s.", len(c.Terms), termsLabel(c.Terms)))
	}
	return p, nil
}

func (b *directBackend) DeleteConcept(ctx context.Context, in DeleteConceptIn) (ResourceDeleted, error) {
	_, userID := scope(ctx)
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ResourceDeleted{}, err
	}
	if err := b.termService().DeleteConcept(ctx, base.ID, in.ConceptID, userID); err != nil {
		return ResourceDeleted{}, resourceErr(err, "delete concept")
	}
	return ResourceDeleted{Kind: "concept", ID: in.ConceptID}, nil
}

func (b *directBackend) PreviewDeleteTerm(ctx context.Context, in DeleteTermIn) (Preview, error) {
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return Preview{}, err
	}
	term, err := b.termService().TermInConcept(ctx, base.ID, in.ConceptID, in.TermID)
	if err != nil {
		return Preview{}, resourceErr(err, "get term")
	}
	siblings, err := b.q.ListTermsForConcepts(ctx, []int64{in.ConceptID})
	if err != nil {
		return Preview{}, fmt.Errorf("list concept terms: %w", err)
	}
	p := Preview{Summary: fmt.Sprintf("Delete the %s term %q from concept %d in %q.", term.Language, term.Text, in.ConceptID, base.Name)}
	if term.Status == "forbidden" {
		p.Effects = append(p.Effects, "It is a forbidden term; QA stops flagging it.")
	}
	if len(siblings) == 1 {
		p.Effects = append(p.Effects, "It is the concept's last term; the concept stays, empty.")
	}
	return p, nil
}

func (b *directBackend) DeleteTerm(ctx context.Context, in DeleteTermIn) (ResourceDeleted, error) {
	_, userID := scope(ctx)
	base, err := b.termBase(ctx, in.TermBaseID)
	if err != nil {
		return ResourceDeleted{}, err
	}
	svc := b.termService()
	term, err := svc.TermInConcept(ctx, base.ID, in.ConceptID, in.TermID)
	if err != nil {
		return ResourceDeleted{}, resourceErr(err, "get term")
	}
	if err := svc.DeleteTerm(ctx, base.ID, term.ID, userID); err != nil {
		return ResourceDeleted{}, fmt.Errorf("delete term: %w", err)
	}
	return ResourceDeleted{Kind: "term", ID: term.ID, Name: term.Text}, nil
}
