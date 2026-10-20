package issues

import (
	"strings"
	"testing"
)

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"See ![step 1.png](attachment:42) here": "See [image: step 1.png] here",
		"![](upload:0)":                         "[image]",
		"![a](attachment:1)![b](attachment:2)":  "[image: a][image: b]",
		"a [link](https://example.com) stays":   "a [link](https://example.com) stays",
		"![x](https://example.com/x.png) stays": "![x](https://example.com/x.png) stays",
	} {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkUploadRefs(t *testing.T) {
	views := []AttachmentView{{ID: 7, IsImage: false}, {ID: 9, IsImage: true}}
	got, ok, err := linkUploadRefs("a ![x](upload:1) b ![x](upload:1)", views)
	if err != nil || !ok || got != "a ![x](attachment:9) b ![x](attachment:9)" {
		t.Errorf("linked = %q, %v, %v", got, ok, err)
	}
	if got, ok, err := linkUploadRefs("no images, ![x](attachment:9) typed", views); err != nil || ok || got != "no images, ![x](attachment:9) typed" {
		t.Errorf("text without upload refs = %q, %v, %v", got, ok, err)
	}
	if _, _, err := linkUploadRefs("![log](upload:0)", views); err == nil {
		t.Error("a reference to a file that is not an image was linked")
	}
	long := strings.Repeat("x", maxText-len("![x](upload:1)")) + "![x](upload:1)"
	if _, _, err := linkUploadRefs(long, views); err == nil {
		t.Error("a text over the limit once linked was accepted")
	}
}

func TestCheckUploadRefs(t *testing.T) {
	files := []Upload{BytesUpload("a.png", nil)}
	if err := checkUploadRefs("![a](upload:0)", files); err != nil {
		t.Errorf("in range: %v", err)
	}
	if err := checkUploadRefs("![a](upload:1)", files); err == nil {
		t.Error("a reference past the files was accepted")
	}
}
