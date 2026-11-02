package productdocs

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// RenderHTML turns a page body into HTML, with the same Markdown flavour the
// app renders (GitHub tables, task lists, strikethrough, autolinks). Raw HTML
// in the source is dropped. href maps every link and image address before it
// is written; nil keeps them as they are.
func RenderHTML(body string, href func(string) string) (string, error) {
	opts := []goldmark.Option{
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	}
	if href != nil {
		opts = append(opts, goldmark.WithParserOptions(
			parser.WithASTTransformers(util.Prioritized(linkRewriter(href), 100)),
		))
	}
	var out bytes.Buffer
	if err := goldmark.New(opts...).Convert([]byte(body), &out); err != nil {
		return "", err
	}
	return out.String(), nil
}

// linkRewriter applies href to every link and image destination.
type linkRewriter func(string) string

func (f linkRewriter) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Link:
			n.Destination = []byte(f(string(n.Destination)))
		case *ast.Image:
			n.Destination = []byte(f(string(n.Destination)))
		}
		return ast.WalkContinue, nil
	})
}
