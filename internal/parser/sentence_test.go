package parser

import "testing"

// sentencesOf is a test helper: SplitSentences returns byte ranges, but the
// assertions read far better against the substrings they select.
func sentencesOf(t *testing.T, text string) []string {
	t.Helper()
	ranges := SplitSentences(text, nil)
	out := make([]string, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, text[r.Start:r.End])
	}
	return out
}

func assertSentences(t *testing.T, text string, want []string) {
	t.Helper()
	got := sentencesOf(t, text)
	if len(got) != len(want) {
		t.Fatalf("split %q into %d sentences, want %d\n got: %q\nwant: %q",
			text, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sentence %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSplitSentencesFromReference locks in the behaviour pinned by the
// reference XLIFF (testdata/st4_reference.xlf). Every case below is a
// string from that file — the "want" values are its trans-unit <source>
// contents, so a regression here means we've drifted from the external CAT
// tool's segmentation, and from any TM built against it.
func TestSplitSentencesFromReference(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "period then capital splits",
			text: "The response time of the measurement system, and the pump flow, influence the flow measurement. The indicated accuracies relate to the steady-state flow rate.",
			want: []string{
				"The response time of the measurement system, and the pump flow, influence the flow measurement.",
				"The indicated accuracies relate to the steady-state flow rate.",
			},
		},
		{
			name: "colon then capital splits",
			text: "Revised Information: F7m Flow Sensor Specifications",
			want: []string{"Revised Information:", "F7m Flow Sensor Specifications"},
		},
		{
			name: "colon then digit splits",
			text: "Ethanol: 0-8.5 l/min",
			want: []string{"Ethanol:", "0-8.5 l/min"},
		},
		{
			name: "colon then lowercase does not split",
			text: "This measurement unit corresponds to the setting: ambient temperature and pressure, standard (NTP)",
			want: []string{"This measurement unit corresponds to the setting: ambient temperature and pressure, standard (NTP)"},
		},
		{
			name: "colons without whitespace do not split",
			text: "The F7m Flow Sensor (100234) complies with ISO 4064-2-55:2018 AMD 1:2023.",
			want: []string{"The F7m Flow Sensor (100234) complies with ISO 4064-2-55:2018 AMD 1:2023."},
		},
		{
			name: "ratio colons do not split",
			text: "±1 for stroke rates 0 to 80 rpm and I:O = 1:2, unspecified if >80 rpm",
			want: []string{"±1 for stroke rates 0 to 80 rpm and I:O = 1:2, unspecified if >80 rpm"},
		},
		{
			name: "identifier colon does not split",
			text: "D0:UI_PUMP_MODE_SEL_ITM0",
			want: []string{"D0:UI_PUMP_MODE_SEL_ITM0"},
		},
		{
			name: "decimal points do not split",
			text: "±(0.43 l/min +8% relative)",
			want: []string{"±(0.43 l/min +8% relative)"},
		},
		{
			name: "version dot does not split",
			text: "This addendum provides additional information for the Water Pump Revision T.xx Instructions for Use for the following pumps:",
			want: []string{"This addendum provides additional information for the Water Pump Revision T.xx Instructions for Use for the following pumps:"},
		},
		{
			name: "trailing colon at end of text does not split",
			text: "Please store this addendum with your pump documentation.",
			want: []string{"Please store this addendum with your pump documentation."},
		},
		{
			name: "formula period then capital splits",
			text: "q [l/min] = k [%]* (p_in - p_H2O)/100. This measurement unit corresponds to the setting: ambient temperature and pressure, standard (NTP)",
			want: []string{
				"q [l/min] = k [%]* (p_in - p_H2O)/100.",
				"This measurement unit corresponds to the setting: ambient temperature and pressure, standard (NTP)",
			},
		},
		{
			name: "unit abbreviation then capital splits",
			text: "The maximum data sample rate that the pump can display for flow curves is one sample per 32 ms. The actual data sample rate displayed depends on the curve speed set at the pump.",
			want: []string{
				"The maximum data sample rate that the pump can display for flow curves is one sample per 32 ms.",
				"The actual data sample rate displayed depends on the curve speed set at the pump.",
			},
		},
		{
			name: "long clause with internal ratios stays whole",
			text: "For H2O, D2O, N2O, and other liquids, the accuracy specifications apply for stroke rates below 60 rpm at an I:O ratio of 1:1.",
			want: []string{"For H2O, D2O, N2O, and other liquids, the accuracy specifications apply for stroke rates below 60 rpm at an I:O ratio of 1:1."},
		},
		{
			name: "no terminator stays whole",
			text: "Maximum 0.4 l/min, except when the first fluid is Toluene",
			want: []string{"Maximum 0.4 l/min, except when the first fluid is Toluene"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSentences(t, tt.text, tt.want)
		})
	}
}

func TestSplitSentencesGuards(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "e.g. is not a sentence end",
			text: "Use a filter, e.g. Type B. The filter is included.",
			want: []string{"Use a filter, e.g. Type B.", "The filter is included."},
		},
		{
			name: "i.e. is not a sentence end",
			text: "Use the primary port, i.e. Port A on the left.",
			want: []string{"Use the primary port, i.e. Port A on the left."},
		},
		{
			// Deliberate trade-off (see endsWithAbbreviation): a single
			// uppercase letter DOES end a sentence, because "Type B." and
			// "Revision T." are common in technical documentation while
			// personal initials are not. The cost is that genuine initials
			// split — accepted.
			name: "single uppercase letter ends a sentence",
			text: "Use the Type B applied part. Then connect the sensor.",
			want: []string{"Use the Type B applied part.", "Then connect the sensor."},
		},
		{
			name: "single uppercase designation before a capital splits",
			text: "Fit applied part Type B. Connect the sensor.",
			want: []string{"Fit applied part Type B.", "Connect the sensor."},
		},
		{
			name: "unit abbreviation before a capital splits",
			text: "Rise time is under 300 ms. The reading stabilizes after that.",
			want: []string{"Rise time is under 300 ms.", "The reading stabilizes after that."},
		},
		{
			name: "etc. is not a sentence end",
			text: "Connect cables, sensors, etc. Then power on the monitor.",
			want: []string{"Connect cables, sensors, etc. Then power on the monitor."},
		},
		{
			name: "opening parenthesis can start a sentence",
			text: "Check the reading. (Values are in mmHg.)",
			want: []string{"Check the reading.", "(Values are in mmHg.)"},
		},
		{
			name: "question mark splits",
			text: "Is the alarm active? Check the display.",
			want: []string{"Is the alarm active?", "Check the display."},
		},
		{
			name: "exclamation splits",
			text: "Warning! Disconnect the power supply.",
			want: []string{"Warning!", "Disconnect the power supply."},
		},
		{
			name: "whitespace is trimmed from both ends",
			text: "  Leading and trailing.  ",
			want: []string{"Leading and trailing."},
		},
		{
			name: "newline counts as the separating whitespace",
			text: "First sentence.\nSecond sentence.",
			want: []string{"First sentence.", "Second sentence."},
		},
		{
			name: "empty text yields nothing",
			text: "   \n  ",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSentences(t, tt.text, tt.want)
		})
	}
}

// TestSplitSentencesCanBreakVeto covers the hook the XML parser uses to stop a
// break landing inside an inline element (which would cut a chip in half).
func TestSplitSentencesCanBreakVeto(t *testing.T) {
	text := "First part. Second part."

	// Without a veto the text splits in two.
	if got := len(SplitSentences(text, nil)); got != 2 {
		t.Fatalf("baseline split produced %d sentences, want 2", got)
	}

	// Vetoing every candidate collapses it to a single range.
	ranges := SplitSentences(text, func(int) bool { return false })
	if len(ranges) != 1 {
		t.Fatalf("veto-all produced %d sentences, want 1", len(ranges))
	}
	if got := text[ranges[0].Start:ranges[0].End]; got != text {
		t.Errorf("veto-all range = %q, want the whole text", got)
	}
}

// TestSplitSentencesRangesAreTight guards the property the XML reconstruct
// path depends on: ranges never include their surrounding whitespace, and they
// never overlap, so splicing translations in leaves the original spacing alone.
func TestSplitSentencesRangesAreTight(t *testing.T) {
	text := "  One.   Two.\n\nThree.  "
	ranges := SplitSentences(text, nil)
	if len(ranges) != 3 {
		t.Fatalf("got %d ranges, want 3", len(ranges))
	}
	prevEnd := 0
	for i, r := range ranges {
		if r.Start < prevEnd {
			t.Errorf("range %d starts at %d, overlapping previous end %d", i, r.Start, prevEnd)
		}
		seg := text[r.Start:r.End]
		if seg != trimSpace(seg) {
			t.Errorf("range %d = %q has untrimmed whitespace", i, seg)
		}
		prevEnd = r.End
	}
}

func trimSpace(s string) string {
	start := skipSpaceForward(s, 0)
	return s[start:trimSpaceBackward(s, start, len(s))]
}
