// Command cldrgen writes internal/langcode/cldr_table.go from a CLDR JSON
// release (the cldr-<version>-json-full.zip asset of
// github.com/unicode-org/cldr-json). The zip is read in place and never
// committed; the table records its version and SHA-256.
//
//	CLDR_ZIP=/path/to/cldr-48.2.3-json-full.zip go generate ./internal/langcode
//
// Only languages at CLDR's "moderate" coverage or above are written, with
// their regional locales; a locale whose data equals its parent's is left out
// (the lookup falls back).
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io"
	"log"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// version is the CLDR release the table is generated from. Changing it means
// downloading that release and regenerating.
const version = "48.2.3"

type locale struct {
	Decimal, Group rune
	NativeDigits   bool
	MinGrouping    int
	DateOrder      string
	Hour12         bool
	AM, PM         []string
	Months         [12][]string
}

func main() {
	zipPath := flag.String("zip", "", "path to cldr-"+version+"-json-full.zip")
	out := flag.String("out", "cldr_table.go", "output file")
	flag.Parse()
	if *zipPath == "" {
		log.Fatal("cldrgen: -zip is required (set CLDR_ZIP for go generate)")
	}
	raw, err := os.ReadFile(*zipPath)
	if err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		log.Fatal(err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	src := &source{files: files}

	parents := src.parents()
	codes := src.coveredLocales()
	resolved := map[string]locale{}
	for _, code := range codes {
		if l, ok := src.locale(code); ok {
			resolved[strings.ToLower(code)] = l
		}
	}

	// Keep a locale only when it differs from what its parent resolves to.
	kept := map[string]locale{}
	for code, l := range resolved {
		if p, ok := resolveParent(code, parents, resolved); ok && reflect.DeepEqual(p, l) {
			continue
		}
		kept[code] = l
	}
	if err := write(*out, kept, parents, hex.EncodeToString(sum[:])); err != nil {
		log.Fatal(err)
	}
	log.Printf("cldrgen: %d locales (%d covered) from CLDR %s", len(kept), len(resolved), version)
}

func resolveParent(code string, parents map[string]string, all map[string]locale) (locale, bool) {
	for {
		p, ok := parents[code]
		if !ok {
			i := strings.LastIndexByte(code, '-')
			if i <= 0 {
				return locale{}, false
			}
			p = code[:i]
		}
		if p == "root" {
			return locale{}, false
		}
		if l, ok := all[p]; ok {
			return l, true
		}
		code = p
	}
}

type source struct{ files map[string]*zip.File }

// find returns the zip entry whose path ends in suffix (the release may nest
// the packages under a top folder).
func (s *source) find(suffix string) *zip.File {
	if f, ok := s.files[suffix]; ok {
		return f
	}
	for name, f := range s.files {
		if strings.HasSuffix(name, "/"+suffix) {
			return f
		}
	}
	return nil
}

func (s *source) json(suffix string) (map[string]any, bool) {
	f := s.find(suffix)
	if f == nil {
		return nil, false
	}
	rc, err := f.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		log.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		log.Fatalf("%s: %v", suffix, err)
	}
	return v, true
}

func (s *source) parents() map[string]string {
	v, ok := s.json("cldr-core/supplemental/parentLocales.json")
	if !ok {
		log.Fatal("cldrgen: parentLocales.json not found")
	}
	m := dig(v, "supplemental", "parentLocales", "parentLocale")
	out := map[string]string{}
	for k, p := range m.(map[string]any) {
		out[strings.ToLower(k)] = strings.ToLower(p.(string))
	}
	return out
}

func (s *source) coveredLocales() []string {
	v, ok := s.json("cldr-core/coverageLevels.json")
	if !ok {
		log.Fatal("cldrgen: coverageLevels.json not found")
	}
	levels, _ := v["coverageLevels"].(map[string]any)
	covered := map[string]bool{}
	for code, lvl := range levels {
		switch lvl {
		case "modern", "moderate":
			covered[code] = true
		}
	}
	// The levels name languages; their regional locales ("de-CH", "es-MX")
	// are in the dates package under the language's code.
	const prefix = "cldr-dates-full/main/"
	seen := map[string]bool{}
	var out []string
	for name := range s.files {
		rest, ok := strings.CutPrefix(name, prefix)
		if !ok {
			continue
		}
		code, _, _ := strings.Cut(rest, "/")
		base, _, _ := strings.Cut(code, "-")
		if (covered[code] || covered[base]) && !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}

// quoted matches literal text in a CLDR date or time pattern.
var quoted = regexp.MustCompile(`'[^']*'`)

// dateOrder returns the order of the year, month and day fields of a CLDR
// date pattern: "M/d/yy" → "mdy".
func dateOrder(pattern string) string {
	pattern = quoted.ReplaceAllString(pattern, "")
	var order []byte
	seen := map[byte]bool{}
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		var f byte
		switch c {
		case 'y', 'Y', 'u', 'r':
			f = 'y'
		case 'M', 'L':
			f = 'm'
		case 'd':
			f = 'd'
		default:
			continue
		}
		if !seen[f] {
			seen[f] = true
			order = append(order, f)
		}
	}
	return string(order)
}

func (s *source) locale(code string) (locale, bool) {
	dates, ok := s.json("cldr-dates-full/main/" + code + "/ca-gregorian.json")
	if !ok {
		return locale{}, false
	}
	nums, ok := s.json("cldr-numbers-full/main/" + code + "/numbers.json")
	if !ok {
		return locale{}, false
	}
	greg := dig(dates, "main", code, "dates", "calendars", "gregorian")
	n := dig(nums, "main", code, "numbers")

	var l locale
	sym, _ := dig(n, "symbols-numberSystem-latn").(map[string]any)
	l.Decimal = firstRune(sym["decimal"])
	l.Group = firstRune(sym["group"])
	l.NativeDigits = dig(n, "defaultNumberingSystem") != "latn"
	if g, ok := dig(n, "minimumGroupingDigits").(string); ok && g == "2" {
		l.MinGrouping = 2
	} else {
		l.MinGrouping = 1
	}

	l.DateOrder = dateOrder(str(dig(greg, "dateFormats", "short")))
	switch l.DateOrder {
	case "mdy", "dmy", "ymd":
	default:
		log.Fatalf("cldrgen: %s: short date %q has no day, month and year", code, str(dig(greg, "dateFormats", "short")))
	}
	timeShort := quoted.ReplaceAllString(str(dig(greg, "timeFormats", "short")), "")
	l.Hour12 = strings.ContainsAny(timeShort, "hK")

	for _, width := range []string{"abbreviated", "wide", "narrow"} {
		periods, _ := dig(greg, "dayPeriods", "format", width).(map[string]any)
		for key, v := range periods {
			switch strings.TrimSuffix(key, "-alt-variant") {
			case "am":
				l.AM = addUnique(l.AM, str(v))
			case "pm":
				l.PM = addUnique(l.PM, str(v))
			}
		}
	}
	sort.Strings(l.AM)
	sort.Strings(l.PM)

	for _, ctx := range []string{"format", "stand-alone"} {
		for _, width := range []string{"wide", "abbreviated"} {
			months, _ := dig(greg, "months", ctx, width).(map[string]any)
			for i := 1; i <= 12; i++ {
				name := str(months[fmt.Sprint(i)])
				if utf8.RuneCountInString(name) < 2 {
					continue
				}
				l.Months[i-1] = addUnique(l.Months[i-1], name)
			}
		}
	}
	for i := range l.Months {
		sort.Strings(l.Months[i])
	}
	return l, true
}

func dig(v any, path ...string) any {
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func firstRune(v any) rune {
	// Not str(v): a separator may be a space (U+202F), which trimming drops.
	// CLDR wraps some symbols in direction marks (U+200E, U+200F, U+061C).
	s, _ := v.(string)
	for _, r := range s {
		switch r {
		case '\u200e', '\u200f', '\u061c':
			continue
		}
		return r
	}
	return 0
}

func addUnique(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func write(path string, locales map[string]locale, parents map[string]string, sum string) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by cldrgen from CLDR %s (sha256 %s). DO NOT EDIT.\n\n", version, sum)
	b.WriteString("package langcode\n\n")
	fmt.Fprintf(&b, "// CLDRVersion is the CLDR release cldr_table.go is generated from.\nconst CLDRVersion = %q\n\n", version)

	b.WriteString("var cldrParents = map[string]string{\n")
	for _, k := range sortedKeys(parents) {
		fmt.Fprintf(&b, "%q: %q,\n", k, parents[k])
	}
	b.WriteString("}\n\nvar cldrLocales = map[string]Locale{\n")
	for _, code := range sortedKeys(locales) {
		l := locales[code]
		fmt.Fprintf(&b, "%q: {Decimal: %q, Group: %q, NativeDigits: %t, MinGrouping: %d, DateOrder: %q, Hour12: %t, AM: %#v, PM: %#v, Months: [12][]string{",
			code, l.Decimal, l.Group, l.NativeDigits, l.MinGrouping, l.DateOrder, l.Hour12, l.AM, l.PM)
		for _, m := range l.Months {
			fmt.Fprintf(&b, "%#v, ", m)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n")
	src, err := format.Source(b.Bytes())
	if err != nil {
		return fmt.Errorf("format: %w", err)
	}
	return os.WriteFile(path, src, 0o644)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
