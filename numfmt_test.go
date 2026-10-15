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

// Only formats render reproduces exactly are percent formats. Anything else in
// any numeric section leaves every cell under the format with its bare value.
func TestParsePercentFormat(t *testing.T) {
	tests := []struct {
		format string
		ok     bool
	}{
		{"0%", true},
		{"0.00%", true},
		{"0.0#%", true},
		{"00.00%", true},
		{"#.00%", true},
		{"#,##0.00%", true},
		{"[Red]0.00%", true},
		{"[Color10]0.00%", true},
		{"[$-409]0.00%", true},
		{"0.00%[Red]", true},
		{"0.00%;-0.00%", true},
		{"0.00%;[Red]-0.00%", true},
		{"0.0%;0.0%", true},
		{"0%;-0%;0.0%", true},
		{"0.00%;-0.00%;0.00%;@", true},
		// Not percent formats.
		{"0.00", false},
		{"General", false},
		{"@", false},
		{`0.00;"%"`, false},
		{"%", false},
		// Percent formats render does not reproduce exactly.
		{`0.00"%"`, false},
		{`0.00\%`, false},
		{`"+"0.0%`, false},
		{"+0.0%", false},
		{"-0.0%", false},
		{`0.0%" up"`, false},
		{"0.00 %", false},
		{"0.00%_)", false},
		{"(0.0%)", false},
		{"0.??%", false},
		{"# ?/?%", false},
		{"0.00E+00%", false},
		{"0,%", false},
		{"0,.0%", false},
		{"0.0,%", false},
		{"0,,0%", false},
		{"0.0.0%", false},
		{"0%%", false},
		{"%0", false},
		{"%0.00", false},
		{"0%.00", false},
		{"0.0%0", false},
		{"[<1]0%;0.0%", false},
		{"0%;[>1]0.0%", false},
		{"[$€-407]0.00%", false},
		{"[$-D000409]0.00%", false},
		{"[$-]0.00%", false},
		{"[Color]0.00%", false},
		{"[Color57]0.00%", false},
		{"[Color+5]0.00%", false},
		{"[$-F800]0.00%", false},
		{"[$-F400]0.00%", false},
		{"[Red0.00%", false},
		// A section the format cannot render rejects the whole format.
		{"0.0%;(0.0%)", false},
		{`0.00%;-0.00%;"-"`, false},
		{"0%;", false},
		{"0%;--0%", false},
		{"0%;0-%", false},
		{"0%;0%;0%;@;x", false},
	}
	for _, tc := range tests {
		if _, ok := parsePercentFormat(tc.format); ok != tc.ok {
			t.Errorf("parsePercentFormat(%q) ok = %v, want %v", tc.format, ok, tc.ok)
		}
	}
}

// Rendering must match what Excel prints: the stored value cut to 15
// significant digits, scaled exactly, rounded half away from zero, decimals
// and integer digits padded from the outermost '0', grouped as the format
// says, under the section for the value's sign.
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
		{1e15, "0%", "100000000000000000%"},
		// Ties round half away from zero, on the value cut to 15
		// significant digits.
		{0.125, "0%", "13%"},
		{-0.125, "0%", "-13%"},
		{0.145, "0%", "15%"},
		{0.285, "0.0%", "28.5%"},
		{0.00125, "0.00%", "0.13%"},
		{0.1212121212121205, "0.0000000000000%", "12.1212121212120%"},
		// Optional decimals print only when significant; the point stays.
		{0.1, "0.0#%", "10.0%"},
		{0.12345, "0.0#%", "12.35%"},
		{0.5, "0.##%", "50.%"},
		{0.1, "0.#0%", "10.00%"},
		{0.1, "0.0#0%", "10.000%"},
		// Integer placeholders pad from the leftmost '0'.
		{0.005, "00.00%", "00.50%"},
		{0.005, "#.00%", ".50%"},
		{0.001, "#%", "%"},
		{0.05, "0#%", "05%"},
		{0.05, "#0#%", "05%"},
		// Thousands grouping.
		{0.5, "#,##0%", "50%"},
		{12.34, "#,##0%", "1,234%"},
		{1234.5678, "#,##0.00%", "123,456.78%"},
		// Tags do not change the text.
		{0.05, "[Red]0.0%", "5.0%"},
		{0.05, "0.0%[Red]", "5.0%"},
		// A negative value that rounds to zero keeps its sign.
		{-0.00001, "0.00%", "-0.00%"},
		// Sections: positive and zero take the first, negatives the second,
		// which prints a sign only if it has a literal '-'; zero the third.
		{0.05, "0.00%;[Red]-0.00%", "5.00%"},
		{-0.05, "0.00%;[Red]-0.00%", "-5.00%"},
		{-0.05, "0.0%;0.0%", "5.0%"},
		{0, "0.0%;-0.0%", "0.0%"},
		{0, "0%;-0%;0.0%", "0.0%"},
		{-0.05, "0%;-0.0%;0.00%", "-5.0%"},
		// The section follows the value, not its rounding.
		{-0.0001, "0%;-0%", "-0%"},
		{-0.0001, "0%;0%", "0%"},
		{math.Copysign(0, -1), "0%;-0%;0.0%", "0.0%"},
		{1e-6, "0%;-0%;0.0%", "0%"},
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

// NaN and the infinities have no percentage to print, so the caller keeps the
// bare value.
func TestPercentFormatRenderDeclines(t *testing.T) {
	pf, _ := parsePercentFormat("0%")
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got, ok := pf.render(v); ok {
			t.Errorf("render(%v) = %q, want no rendering", v, got)
		}
	}
}

func TestSplitSections(t *testing.T) {
	tests := []struct {
		format string
		want   []string
	}{
		{"0%", []string{"0%"}},
		{"0%;-0%", []string{"0%", "-0%"}},
		{`0%;"a;b"`, []string{"0%", `"a;b"`}},
		{`0%;\;x`, []string{"0%", `\;x`}},
		{"0%;[a;b]x", []string{"0%", "[a;b]x"}},
		{"0%;[Red", []string{"0%", "[Red"}},
		{"0%;", []string{"0%", ""}},
	}
	for _, tc := range tests {
		got := splitSections(tc.format)
		if len(got) != len(tc.want) {
			t.Errorf("splitSections(%q) = %q, want %q", tc.format, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitSections(%q) = %q, want %q", tc.format, got, tc.want)
				break
			}
		}
	}
}
