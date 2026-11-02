package mcp

import (
	"context"
	"strings"

	"openloc/internal/logging"
	"openloc/internal/parser"
)

// ReadUploadedFile returns the text of a file the caller attached, read with
// the parser a project upload of the same file would use. The text goes into
// the model's context, so text over maxTextUpload is refused.
func (b *directBackend) ReadUploadedFile(ctx context.Context, in ReadUploadedFileIn) (UploadedFileTextOut, error) {
	upload, content, err := b.readUpload(ctx, in.UploadID)
	if err != nil {
		return UploadedFileTextOut{}, err
	}
	name := upload.Filename
	format := parser.DetectFormat(name, content)
	p, err := parser.GetParser(format)
	if format == "" || err != nil {
		return UploadedFileTextOut{}, invalidf("OpenLoc cannot read %s: it is not a format a project accepts", name)
	}
	if format == "xlsx" {
		// A project reads only the source column of a two-language workbook;
		// a reader wants its every column and sheet.
		p = parser.XLSX{AllCells: true}
	}
	segments, err := p.Parse(content)
	if err != nil {
		logging.Warn(ctx, err, "assistant upload read failed", "mcp-read-upload-parse-failed")
		if msg, ok := parser.ImportMessage(err); ok {
			return UploadedFileTextOut{}, invalidf("%s", msg)
		}
		return UploadedFileTextOut{}, invalidf("could not read %s as a %s file; check that the file is well-formed", name, format)
	}
	var text strings.Builder
	for _, s := range segments {
		text.WriteString(s.Text)
		if s.Target != "" {
			text.WriteByte('\t')
			text.WriteString(s.Target)
		}
		text.WriteByte('\n')
		if text.Len() > maxTextUpload {
			return UploadedFileTextOut{}, invalidf("the text of %s is larger than the 1 MB limit for text read in a tool call; ask the user for a shorter file or the part that matters", name)
		}
	}
	return UploadedFileTextOut{Filename: name, Format: format, Text: text.String()}, nil
}
