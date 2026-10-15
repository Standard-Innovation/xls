package xls

import (
	"math"
	"testing"
)

// The cases the reader has to separate are numeric formats that contain a
// letter Excel also uses as a date code, and date formats whose codes are
// reached only after a construct that must not be read as codes at all. Those
// two families are the whole of the classification risk.
func TestIsDateFormat(t *testing.T) {
	tests := []struct {
		format string
		want   bool
	}{
		// Numeric. The first is the format the defect was found under: read
		// as a date, a numeric cell rendered as an RFC3339 timestamp.
		{`#,##0.0000_);\(#,##0.0000\)`, false},
		{"0.00%", false},
		{"General", false},
		{`"Total: "0.00`, false},
		{"[Red]-0.00", false},
		{`#,##0.00;[Red]"("#,##0.00")"`, false},
		{"0.00E+00", false},
		{"# ?/?", false},
		{`0.00_);\(0.00\)`, false},
		{"$#,##0.00", false},
		// A quoted literal, an escape and a fill each carry a letter that is
		// a date code in isolation.
		{`0.00" months"`, false},
		{`0.00\d`, false},
		{`0.00*d`, false},
		{`[$-409]#,##0.00`, false},
		// Accounting formats stack padding, quoted literals, fills and
		// escapes in one section -- the input where an off-by-one in the
		// consume logic would surface.
		{`_("$"* #,##0.00_);_("$"* \(#,##0.00\);_("$"* "-"??_);_(@_)`, false},
		{`_(* #,##0.00_);_(* \(#,##0.00\);_(* "-"??_);_(@_)`, false},
		// An empty format string is reachable: a FORMAT record may carry a
		// zero-length string.
		{"", false},
		// Text.
		{"@", false},
		{`"n/a"@`, false},
		// Dates and times.
		{"m/d/yyyy", true},
		{"dd-mmm-yy", true},
		{"h:mm AM/PM", true},
		{"[h]:mm:ss", true},
		{"[$-409]d-mmm-yy;@", true},
		{"yyyy-mm-dd", true},
		{"mm:ss.0", true},
		{"yyyy-mm-dd hh:mm:ss", true},
		// The elapsed-time codes, which are date codes even though every
		// other bracketed group is not.
		{"[mm]:ss", true},
		{"[ss]", true},
		// A negative section may be a date while the positive one is not;
		// only the first section decides.
		{"0.00;m/d/yyyy", false},
		// Truncated formats must not be read past their end.
		{`0.00"unterminated`, false},
		{"0.00[unterminated", false},
		{`0.00\`, false},
	}
	for _, tc := range tests {
		if got := isDateFormat(tc.format); got != tc.want {
			t.Errorf("isDateFormat(%q) = %v, want %v", tc.format, got, tc.want)
		}
	}
}

func TestIsElapsedTimeCode(t *testing.T) {
	tests := []struct {
		group string
		want  bool
	}{
		{"h", true},
		{"hh", true},
		{"m", true},
		{"mm", true},
		{"s", true},
		{"ss", true},
		{"H", true},
		{"MM", true},
		{"Red", false},
		{"Magenta", false},
		{"$-409", false},
		{"<=100", false},
		{"", false},
		{"hm", false},
		{"hhh", false},
	}
	for _, tc := range tests {
		if got := isElapsedTimeCode(tc.group); got != tc.want {
			t.Errorf("isElapsedTimeCode(%q) = %v, want %v", tc.group, got, tc.want)
		}
	}
}

// Only formats render reproduces exactly are percent formats; every other
// construct in the first section leaves the cell's bare value.
func TestParsePercentFormat(t *testing.T) {
	two := percentFormat{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 1}
	tests := []struct {
		format string
		want   percentFormat
		ok     bool
	}{
		{"0%", percentFormat{minIntDigits: 1, sections: 1}, true},
		{"0.00%", two, true},
		{"0.0#%", percentFormat{minIntDigits: 1, minDecimals: 1, maxDecimals: 2, point: true, sections: 1}, true},
		{"0.0###%", percentFormat{minIntDigits: 1, minDecimals: 1, maxDecimals: 4, point: true, sections: 1}, true},
		{"00.00%", percentFormat{minIntDigits: 2, minDecimals: 2, maxDecimals: 2, point: true, sections: 1}, true},
		{"#.00%", percentFormat{minDecimals: 2, maxDecimals: 2, point: true, sections: 1}, true},
		{"#,##0%", percentFormat{minIntDigits: 1, grouped: true, sections: 1}, true},
		{"[Red]0.00%", two, true},
		{"[Color10]0.00%", two, true},
		{"[$-409]0.00%", two, true},
		{"0.00%;[Red]-0.00%", percentFormat{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 2}, true},
		{`0.00%;"neg";"zero"`, percentFormat{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 3}, true},
		{`0.00%;"a;b"`, percentFormat{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 2}, true},
		{`0.00%;\;x`, percentFormat{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 2}, true},
		{"0.00%;[Red", percentFormat{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 2}, true},
		// Not percent formats.
		{"0.00", percentFormat{}, false},
		{"General", percentFormat{}, false},
		{"@", percentFormat{}, false},
		{`0.00;"%"`, percentFormat{}, false},
		{"%", percentFormat{}, false},
		// Percent formats render does not reproduce: the bare value is kept.
		{`0.00"%"`, percentFormat{}, false},
		{`0.00\%`, percentFormat{}, false},
		{`"+"0.0%`, percentFormat{}, false},
		{"+0.0%", percentFormat{}, false},
		{`0.0%" up"`, percentFormat{}, false},
		{"0.00 %", percentFormat{}, false},
		{"0.00%_)", percentFormat{}, false},
		{"(0.0%)", percentFormat{}, false},
		{"0.??%", percentFormat{}, false},
		{"# ?/?%", percentFormat{}, false},
		{"0.00E+00%", percentFormat{}, false},
		{"0,%", percentFormat{}, false},
		{"0,.0%", percentFormat{}, false},
		{"0.0,%", percentFormat{}, false},
		{"0.0.0%", percentFormat{}, false},
		{"0%%", percentFormat{}, false},
		{"[<1]0%;0.0%", percentFormat{}, false},
		{"[$€-407]0.00%", percentFormat{}, false},
		{"[Red0.00%", percentFormat{}, false},
	}
	for _, tc := range tests {
		got, ok := parsePercentFormat(tc.format)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parsePercentFormat(%q) = (%+v, %v), want (%+v, %v)", tc.format, got, ok, tc.want, tc.ok)
		}
	}
}

// Rendering must match what Excel prints: half away from zero on the value
// cut to 15 significant digits, optional decimals trimmed, the integer part
// zero-padded and grouped as the format says.
func TestPercentFormatRender(t *testing.T) {
	tests := []struct {
		v      float64
		format string
		want   string
	}{
		{0.8, "0.00%", "80.00%"},
		{0.5, "0%", "50%"},
		{1, "0%", "100%"},
		{0.1875, "0.00%", "18.75%"},
		{1.0 / 3, "0.00%", "33.33%"},
		{0, "0.00%", "0.00%"},
		{math.Copysign(0, -1), "0.00%", "0.00%"},
		{-0.99, "0.00%", "-99.00%"},
		// Ties round half away from zero, after the binary product is cut
		// to 15 significant digits.
		{0.125, "0%", "13%"},
		{-0.125, "0%", "-13%"},
		{0.145, "0%", "15%"},
		{0.285, "0.0%", "28.5%"},
		{0.00125, "0.00%", "0.13%"},
		// Optional decimals print only when significant; the point stays.
		{0.1, "0.0#%", "10.0%"},
		{0.12345, "0.0#%", "12.35%"},
		{0.5, "0.##%", "50.%"},
		// Integer placeholders.
		{0.005, "00.00%", "00.50%"},
		{0.005, "#.00%", ".50%"},
		{0.001, "#%", "%"},
		// Thousands grouping.
		{0.5, "#,##0%", "50%"},
		{12.34, "#,##0%", "1,234%"},
		{1234.5678, "#,##0.00%", "123,456.78%"},
		// Colour and locale tags do not change the text.
		{0.05, "[Red]0.0%", "5.0%"},
		// A negative value that rounds to zero keeps its sign.
		{-0.00001, "0.00%", "-0.00%"},
		// The first section covers positives and, with two sections, zero.
		{0.05, "0.0%;0.0%", "5.0%"},
		{0, "0.0%;0.0%", "0.0%"},
	}
	for _, tc := range tests {
		pf, ok := parsePercentFormat(tc.format)
		if !ok {
			t.Errorf("parsePercentFormat(%q) not a percent format", tc.format)
			continue
		}
		if got, ok := pf.render(tc.v); !ok || got != tc.want {
			t.Errorf("render(%v) under %q = (%q, %v), want (%q, true)", tc.v, tc.format, got, ok, tc.want)
		}
	}
}

// A value the first section does not cover, or with no finite percentage,
// is not rendered, so the caller keeps the bare value.
func TestPercentFormatRenderDeclines(t *testing.T) {
	tests := []struct {
		v      float64
		format string
	}{
		{-0.05, "0.0%;0.0%"},
		{-0.05, "0.0%;(0.0%)"},
		{0, `0.0%;-0.0%;"-"`},
		{math.NaN(), "0%"},
		{math.Inf(1), "0%"},
		{math.Inf(-1), "0%"},
		{math.MaxFloat64, "0%"},
	}
	for _, tc := range tests {
		pf, ok := parsePercentFormat(tc.format)
		if !ok {
			t.Errorf("parsePercentFormat(%q) not a percent format", tc.format)
			continue
		}
		if got, ok := pf.render(tc.v); ok {
			t.Errorf("render(%v) under %q = %q, want no rendering", tc.v, tc.format, got)
		}
	}
}
