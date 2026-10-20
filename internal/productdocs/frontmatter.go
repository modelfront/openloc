package productdocs

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// frontmatter is the YAML block at the top of every page. `sources` names the
// code paths the page describes — the docs-sync agent uses it to map a diff
// back to the pages it invalidates. `updated_at` is bumped only when the body
// changes; a gratuitous bump destroys the signal.
//
// `limitations` names the entries in the known-limitations register (kept
// outside this repository) whose
// topic belongs to this page. It is metadata, invisible to readers: the pages
// describe how the product works and do not disclose defects. An id means
// either that this page is knowingly silent about the topic (`undocumented` in
// the register) or that it states the behaviour plainly because a user
// collides with it (`documented-behaviour`). Either way the register's `On fix`
// says what this page owes once the defect is gone, so a position cannot
// outlive its reason.
type frontmatter struct {
	Title       string   `yaml:"title"`
	Summary     string   `yaml:"summary"`
	Order       int      `yaml:"order"`
	Roles       []string `yaml:"roles"`
	UpdatedAt   string   `yaml:"updated_at"`
	Sources     []string `yaml:"sources"`
	Limitations []string `yaml:"limitations"`
	Related     []string `yaml:"related"`
	Screenshots []string `yaml:"screenshots"`
}

var fence = []byte("---")

// splitFrontmatter separates the leading `---` block from the Markdown body.
// The block is mandatory: a page without one is a mistake, not a page. Unknown
// keys are rejected so a typo (`update_at`) fails loudly instead of silently
// dropping the field.
func splitFrontmatter(raw []byte) (frontmatter, string, error) {
	var fm frontmatter
	raw = bytes.TrimPrefix(raw, []byte("\xEF\xBB\xBF")) // UTF-8 BOM from some editors
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) == 0 || !bytes.Equal(bytes.TrimSpace(lines[0]), fence) {
		return fm, "", fmt.Errorf("missing frontmatter (file must start with a --- line)")
	}
	for i := 1; i < len(lines); i++ {
		if !bytes.Equal(bytes.TrimSpace(lines[i]), fence) {
			continue
		}
		block := bytes.Join(lines[1:i], []byte("\n"))
		body := bytes.Join(lines[i+1:], []byte("\n"))
		dec := yaml.NewDecoder(bytes.NewReader(block))
		dec.KnownFields(true)
		if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
			return fm, "", fmt.Errorf("frontmatter: %w", err)
		}
		return fm, string(body), nil
	}
	return fm, "", fmt.Errorf("frontmatter is not closed with a --- line")
}
