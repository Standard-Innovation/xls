package xls

import "testing"

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

// A "%" is the percent operator only outside literals, escapes and bracketed
// groups, and only in the first (positive) section; the decimals are the digit
// placeholders after the decimal point.
func TestPercentDecimals(t *testing.T) {
	tests := []struct {
		format   string
		decimals int
		ok       bool
	}{
		{"0%", 0, true},
		{"0.00%", 2, true},
		{"0.0###%", 4, true},
		{"#,##0.00%;[Red]-#,##0.00%", 2, true},
		{"[$-409]0.00%", 2, true},
		{"0.00", 0, false},
		{`0.00"%"`, 2, false},
		{`0.00\%`, 2, false},
		{"General", 0, false},
		{"@", 0, false},
		{`0.00;"%"`, 2, false},
	}
	for _, tt := range tests {
		d, ok := percentDecimals(tt.format)
		if ok != tt.ok || (ok && d != tt.decimals) {
			t.Errorf("percentDecimals(%q) = (%d, %v), want (%d, %v)", tt.format, d, ok, tt.decimals, tt.ok)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		v        float64
		decimals int
		want     string
	}{
		{0.8, 2, "80.00%"},
		{0.5, 0, "50%"},
		{0.1875, 2, "18.75%"},
		{1.0 / 3, 2, "33.33%"},
		{-0.99, 2, "-99.00%"},
		{0, 2, "0.00%"},
	}
	for _, tt := range tests {
		if got := formatPercent(tt.v, tt.decimals); got != tt.want {
			t.Errorf("formatPercent(%v, %d) = %q, want %q", tt.v, tt.decimals, got, tt.want)
		}
	}
}
