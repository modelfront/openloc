package numeric

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// timePatterns are the time patterns of one language: its AM/PM markers and
// the English ones.
type timePatterns struct {
	isPM       map[string]bool // marker → PM
	clock      *regexp.Regexp  // [marker] 15:30[:00] [marker|Uhr|h]
	hourMarker *regexp.Regexp  // 3 PM, 3pm, 午後3時
	hourH      *regexp.Regexp  // 15h30, 15 h, 15 Uhr, 15.30 Uhr
	cjk        *regexp.Regexp  // 午後3時30分, 15点30分, 오후 3시 30분
}

var timeCache sync.Map // locale key → *timePatterns

func timePatternsFor(loc locale) *timePatterns {
	key := strings.Join(loc.am, "|") + "/" + strings.Join(loc.pm, "|")
	if p, ok := timeCache.Load(key); ok {
		return p.(*timePatterns)
	}
	p := &timePatterns{isPM: map[string]bool{}}
	add := func(list []string, pm bool) {
		for _, m := range list {
			// One-letter markers ("a", "p") are words too often.
			if utf8.RuneCountInString(m) >= 2 {
				p.isPM[m] = pm
			}
		}
	}
	add([]string{"AM", "am", "a.m.", "A.M.", "午前", "上午", "오전"}, false)
	add([]string{"PM", "pm", "p.m.", "P.M.", "午後", "下午", "오후"}, true)
	add(loc.am, false)
	add(loc.pm, true)
	markers := make([]string, 0, len(p.isPM))
	for m := range p.isPM {
		markers = append(markers, regexp.QuoteMeta(m))
	}
	sort.Slice(markers, func(i, j int) bool {
		return len(markers[i]) > len(markers[j]) || len(markers[i]) == len(markers[j]) && markers[i] < markers[j]
	})
	marker := `(` + strings.Join(markers, "|") + `)`
	p.clock = regexp.MustCompile(`(?:` + marker + `\s*)?(\d{1,2}):(\d{2})(?::\d{2})?(?:\s*` + marker + `|\s*(?:Uhr|h)\b)?`)
	p.hourMarker = regexp.MustCompile(`(\d{1,2})\s*` + marker)
	p.hourH = regexp.MustCompile(`(\d{1,2})(?:h(\d{2})|[.](\d{2})\s*(?:Uhr|h)\b|\s*Uhr\b|\s?h\b)`)
	p.cjk = regexp.MustCompile(`(?:` + marker + `\s*)?(\d{1,2})\s*[時时点點시](?:\s*(\d{1,2})\s*[分분])?`)
	timeCache.Store(key, p)
	return p
}

// clockMinutes returns the minutes since midnight a time can be. pm is nil
// without a marker: a 12-hour language then means either half of the day.
func clockMinutes(h, m int, pm *bool, hour12 bool) []int {
	if m > 59 || h > 24 || (h == 24 && m > 0) {
		return nil
	}
	if pm != nil {
		if h < 1 || h > 12 {
			return nil
		}
		h %= 12
		if *pm {
			h += 12
		}
		return []int{h*60 + m}
	}
	h %= 24
	if hour12 && h >= 1 && h <= 12 {
		return []int{h*60 + m, (h%12+12)%24*60 + m}
	}
	return []int{h*60 + m}
}

func (t *text) timeCandidates(loc locale) []candidate {
	p := timePatternsFor(loc)
	s := t.norm
	var out []candidate
	group := func(m []int, i int) string {
		if m[2*i] < 0 {
			return ""
		}
		return s[m[2*i]:m[2*i+1]]
	}
	add := func(start, end int, minutes []int, weak bool) {
		if len(minutes) > 0 && t.boundaryOK(start, end) && !followedByNumber(s, end) {
			out = append(out, candidate{kind: kindTime, start: start, end: end, minutes: minutes, weak: weak})
		}
	}
	marker := func(before, after string) *bool {
		for _, m := range []string{before, after} {
			if m != "" {
				pm := p.isPM[m]
				return &pm
			}
		}
		return nil
	}
	for _, m := range t.findAll(p.clock) {
		add(m[0], m[1], clockMinutes(atoi(group(m, 2)), atoi(group(m, 3)), marker(group(m, 1), group(m, 4)), loc.hour12), false)
	}
	for _, m := range t.findAll(p.hourMarker) {
		add(m[0], m[1], clockMinutes(atoi(group(m, 1)), 0, marker("", group(m, 2)), loc.hour12), false)
	}
	for _, m := range t.findAll(p.hourH) {
		min := group(m, 2) + group(m, 3)
		// A bare "15 h" may be hours of duration: it counts only when the
		// other side has the same time.
		weak := min == "" && !strings.Contains(s[m[0]:m[1]], "Uhr")
		add(m[0], m[1], clockMinutes(atoi(group(m, 1)), atoi(min), nil, false), weak)
	}
	for _, m := range t.findAll(p.cjk) {
		add(m[0], m[1], clockMinutes(atoi(group(m, 2)), atoi(group(m, 3)), marker(group(m, 1), ""), loc.hour12), false)
	}
	return out
}
