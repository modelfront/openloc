package mcp

import (
	"context"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	db "openloc/internal/database/sqlcgen"
	"openloc/internal/file"
	"openloc/internal/parser"
	"openloc/internal/pipeline"
)

// maxTextUpload caps a document sent or returned as text in a tool call. The
// text passes through the model's context, so the cap sits far below the
// 50 MB upload limit; a bigger file goes through the project page.
const maxTextUpload = 1 << 20

// textUploadFormats are the formats a model can send or read as text. Word, PDF and
// Excel files are binary, and a model does not copy binary content reliably.
var textUploadFormats = map[string]bool{
	"txt": true, "html": true, "markdown": true, "json_i18n": true, "xliff": true,
	"po": true, "pot": true, "apple_strings": true, "android_xml": true, "xml": true,
}

func (b *directBackend) UploadTextFile(ctx context.Context, in UploadTextFileIn) (AttachedFileOut, error) {
	fh, err := b.files()
	if err != nil {
		return AttachedFileOut{}, err
	}
	name := uploadBaseName(in.Filename)
	if name == "" {
		return AttachedFileOut{}, invalidf("give the file name with its extension, such as brochure.html")
	}
	if len(in.Content) > maxTextUpload {
		return AttachedFileOut{}, invalidf("the text is larger than the 1 MB limit for a document sent in a tool call; ask the user to upload the file on the project page")
	}
	if strings.TrimSpace(in.Content) == "" {
		return AttachedFileOut{}, invalidf("the content is empty; send the document's full text")
	}
	if !utf8.ValidString(in.Content) {
		return AttachedFileOut{}, invalidf("the content is not valid UTF-8 text")
	}
	content := []byte(in.Content)
	if format := parser.DetectFormat(name, content); format != "" && !textUploadFormats[format] {
		return AttachedFileOut{}, invalidf("%s is not a text format; ask the user to upload the file on the project page", name)
	}
	p, err := b.project(ctx, in.ProjectID)
	if err != nil {
		return AttachedFileOut{}, err
	}
	return ingestIntoProject(ctx, fh, p, name, content, file.IngestOptions{Analyze: in.Analyze})
}

// uploadBaseName is the name a model gave, without any directory: it becomes
// part of the stored object's key and the file's display name.
func uploadBaseName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	if name == "." || name == ".." || name == "/" || strings.ContainsFunc(name, func(r rune) bool { return r < ' ' }) {
		return ""
	}
	return name
}

// exportContent is a file's export as text, for a tool that cannot open a
// download link: a text format only, within the upload's limits. When the
// text cannot be returned, reason says why and the caller keeps the link;
// err is a server failure. A returned export is recorded in the project
// history, like one served by the export route.
func (b *directBackend) exportContent(ctx context.Context, f db.File, sourceLang, lang string, xliff bool) (content, reason string, err error) {
	if b.ops.Exports == nil {
		return "", "", invalidf("export is not available on this server")
	}
	if !xliff && !textUploadFormats[string(f.Format)] {
		return "", f.Name + " is not a text format; give the user the download_path link", nil
	}
	tenantID, _ := scope(ctx)
	var data []byte
	format := "source"
	if xliff {
		format = "xliff"
		data, _, err = b.ops.Exports.ExportBilingualXLIFF(ctx, tenantID, f.ID, sourceLang, lang)
	} else {
		data, _, err = b.ops.Exports.ExportFile(ctx, tenantID, f.ID, lang)
	}
	if err != nil {
		if msg, ok := pipeline.UserExportMessage(err); ok {
			return "", msg, nil
		}
		return "", "", fmt.Errorf("export file: %w", err)
	}
	if len(data) > maxTextUpload {
		return "", "the exported file is larger than the 1 MB limit for a document in a tool call; give the user the download_path link", nil
	}
	if !utf8.Valid(data) {
		return "", "the exported file is not UTF-8 text; give the user the download_path link", nil
	}
	b.ops.Exports.RecordFileExported(ctx, f, lang, format)
	return string(data), "", nil
}
