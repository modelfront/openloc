package segmentationapi

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"openloc/internal/httputil"
	"openloc/internal/segmentation"
)

// Preview splits in.Text with the real engine. Invalid rules are a client
// error naming the rule.
func (s *Service) Preview(ctx context.Context, tenantID int64, in PreviewInput) (Preview, error) {
	if utf8.RuneCountInString(in.Text) > segmentation.MaxPreviewText {
		return Preview{}, httputil.NewClientError(fmt.Sprintf("The sample text can be at most %d characters.", segmentation.MaxPreviewText))
	}
	rs := in.RuleSet
	if rs == nil {
		if in.ID == nil {
			return Preview{}, httputil.NewClientError("Send the rules or the id of a segmentation profile.")
		}
		set, err := s.Get(ctx, tenantID, *in.ID)
		if err != nil {
			return Preview{}, err
		}
		rs = &set.Definition
	}
	seg, err := segmentation.Compile(*rs)
	if err != nil {
		var ie *segmentation.InvalidError
		if errors.As(err, &ie) {
			return Preview{}, httputil.NewClientError(ie.Message)
		}
		return Preview{}, err
	}
	decisions, err := seg.Explain(in.Text)
	if err != nil {
		return Preview{}, tooCostly(err)
	}
	ranges := segmentation.Ranges(in.Text, decisions)
	out := Preview{Segments: []string{}, Pieces: []PreviewPiece{}}
	for _, r := range ranges {
		out.Segments = append(out.Segments, in.Text[r.Start:r.End])
	}
	prev := 0
	for _, d := range decisions {
		pd := &PreviewDecision{Break: d.Break, Source: d.Source, Word: d.Word}
		if d.Source == segmentation.SourceRule {
			n := d.Rule + 1
			pd.Rule = &n
		}
		out.Pieces = append(out.Pieces, PreviewPiece{Text: in.Text[prev:d.Pos], Decision: pd})
		prev = d.Pos
	}
	out.Pieces = append(out.Pieces, PreviewPiece{Text: in.Text[prev:]})
	return out, nil
}

// tooCostly turns the engine's work-budget error into a client error.
func tooCostly(err error) error {
	if errors.Is(err, segmentation.ErrTooCostly) {
		return httputil.NewClientError("These rules take too long to run on the sample text. Make the before and after patterns more specific.")
	}
	return err
}

// ParseSRX reads an uploaded SRX file into unsaved drafts; nothing is stored.
func ParseSRX(data []byte) (segmentation.SRXImport, error) {
	imp, err := segmentation.ParseSRX(data)
	var ie *segmentation.InvalidError
	if errors.As(err, &ie) {
		return segmentation.SRXImport{}, httputil.NewClientError(ie.Message)
	}
	return imp, err
}

// ExportSRX returns a set as an SRX file and its file name.
func (s *Service) ExportSRX(ctx context.Context, tenantID, id int64) (string, []byte, error) {
	set, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return "", nil, err
	}
	data, err := segmentation.ExportSRX(set.Name, set.SourceLanguages, set.Definition)
	if err != nil {
		return "", nil, err
	}
	return srxFileName(set.Name), data, nil
}

var unsafeFileChars = regexp.MustCompile(`[^\p{L}\p{N}._ -]+`)

func srxFileName(name string) string {
	base := strings.TrimSpace(unsafeFileChars.ReplaceAllString(name, "-"))
	if base == "" {
		base = "segmentation"
	}
	return base + ".srx"
}
