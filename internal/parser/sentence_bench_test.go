package parser

import (
	"strings"
	"testing"

	"openloc/internal/segmentation"
)

// benchProse is about 1 MB of prose built from the oracle seeds and the XML
// reference fixture's text, as paragraphs of a few sentences each.
func benchProse(b *testing.B) []string {
	b.Helper()
	src := string(readFixture(b, st4SourcePath))
	var paras []string
	size := 0
	for size < 1<<20 {
		for _, line := range strings.Split(src, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				paras = append(paras, line)
				size += len(line)
			}
		}
		for _, s := range oracleSeeds {
			paras = append(paras, s)
			size += len(s)
		}
	}
	return paras
}

func benchSplit(b *testing.B, split func(string) ([]TextRange, error)) {
	paras := benchProse(b)
	b.ResetTimer()
	for b.Loop() {
		for _, p := range paras {
			if _, err := split(p); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// Run with: go test ./internal/parser -run '^$' -bench Split -benchmem
func BenchmarkSplit_Oracle(b *testing.B) {
	benchSplit(b, func(s string) ([]TextRange, error) { return oracleSplitSentences(s, nil), nil })
}

func BenchmarkSplit_Conservative(b *testing.B) {
	benchSplit(b, func(s string) ([]TextRange, error) { return segmentation.Conservative().Split(s, nil) })
}

func BenchmarkSplit_LargestBuiltin(b *testing.B) {
	var largest segmentation.Builtin
	for _, bi := range segmentation.Builtins() {
		if len(bi.RuleSet.Abbreviations)+len(bi.RuleSet.Rules) > len(largest.RuleSet.Abbreviations)+len(largest.RuleSet.Rules) {
			largest = bi
		}
	}
	seg := segmentation.MustCompile(largest.RuleSet)
	benchSplit(b, func(s string) ([]TextRange, error) { return seg.Split(s, nil) })
}

func BenchmarkSplit_OneMegabyteBlock(b *testing.B) {
	text := strings.Join(benchProse(b), " ")
	seg := segmentation.Conservative()
	b.ResetTimer()
	for b.Loop() {
		if _, err := seg.Split(text, nil); err != nil {
			b.Fatal(err)
		}
	}
}
