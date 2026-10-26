package parsers

import (
	"openloc/internal/parsers/common"
	"openloc/internal/parsers/xml"
	"os"
	"testing"
)

// TestDetectFormatDisambiguatesXML covers the one extension that can't be
// resolved by name. Getting this wrong is silent: the Android parser
// finds no <string> elements in a generic XML file and produces zero segments,
// which looks like an empty upload rather than an error.
const st4SourcePath = "xml/testdata/st4_source.xml"

func TestDetectFormatDisambiguatesXML(t *testing.T) {
	st4, err := os.ReadFile(st4SourcePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	tests := []struct {
		name    string
		file    string
		content []byte
		want    string
	}{
		{
			name:    "android resources root",
			file:    "strings.xml",
			content: []byte(`<?xml version="1.0"?><resources><string name="hello">Hello</string></resources>`),
			want:    "android_xml",
		},
		{
			name:    "android resources with a comment first",
			file:    "strings.xml",
			content: []byte("<?xml version=\"1.0\"?>\n<!-- generated -->\n<resources><string name=\"a\">A</string></resources>"),
			want:    "android_xml",
		},
		{
			name:    "ST4 tms root",
			file:    "export.xml",
			content: st4,
			want:    "xml",
		},
		{
			name:    "arbitrary root",
			file:    "book.xml",
			content: []byte(`<book><title>A Title</title></book>`),
			want:    "xml",
		},
		{
			name: "unreadable xml keeps the historical mapping",
			file: "broken.xml",
			// Not well-formed. Routing on a guess would be worse than letting
			// the existing parser report the failure.
			content: []byte(`<resources`),
			want:    "android_xml",
		},
		{
			name:    "non-xml extensions are unaffected",
			file:    "messages.json",
			content: []byte(`{"a":"b"}`),
			want:    "json_i18n",
		},
		{
			name:    "unknown extension stays unsupported",
			file:    "notes.rtf",
			content: []byte("junk"),
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := common.DetectFormat(tt.file, tt.content); got != tt.want {
				t.Errorf("DetectFormat(%q) = %q, want %q", tt.file, got, tt.want)
			}
		})
	}
}

// TestGetParserResolvesXML — the format string has to reach a real parser, or
// upload fails with "not supported" after detection said otherwise.
func TestGetParserResolvesXML(t *testing.T) {
	p, err := GetParser("xml")
	if err != nil {
		t.Fatalf("GetParser(\"xml\"): %v", err)
	}
	if _, ok := p.(xml.XML); !ok {
		t.Errorf("GetParser(\"xml\") returned %T, want parser.XML", p)
	}
}

// TestAndroidXMLStillRoutesCorrectly guards the regression that matters most
// here: adding generic XML must not break existing Android uploads.
func TestAndroidXMLStillRoutesCorrectly(t *testing.T) {
	content := []byte(`<?xml version="1.0" encoding="utf-8"?>
<resources>
    <string name="app_name">My App</string>
    <string name="greeting">Hello <b>there</b></string>
</resources>`)

	format := common.DetectFormat("strings.xml", content)
	if format != "android_xml" {
		t.Fatalf("format = %q, want android_xml", format)
	}

	p, err := GetParser(format)
	if err != nil {
		t.Fatalf("GetParser: %v", err)
	}
	segments, err := p.Parse(content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(segments))
	}
	if segments[0].Key != "app_name" {
		t.Errorf("first key = %q, want app_name", segments[0].Key)
	}
}
