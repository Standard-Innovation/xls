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
// groups, and only in the first section. Fixed decimals are the '0'
// placeholders after the point; optional ones are '#' and '?'. Formats the
// renderer cannot print as Excel does are not treated as percent formats.
func TestParsePercentFormat(t *testing.T) {
	tests := []struct {
		format string
		want   percentFormat
		ok     bool
	}{
		{"0%", percentFormat{}, true},
		{"0.00%", percentFormat{minDecimals: 2, maxDecimals: 2}, true},
		{"0.0#%", percentFormat{minDecimals: 1, maxDecimals: 2}, true},
		{"0.0###%", percentFormat{minDecimals: 1, maxDecimals: 4}, true},
		{"0.??%", percentFormat{maxDecimals: 2}, true},
		{"#,##0%", percentFormat{grouped: true}, true},
		{"#,##0.00%;[Red]-#,##0.00%", percentFormat{minDecimals: 2, maxDecimals: 2, grouped: true}, true},
		{"[$-409]0.00%", percentFormat{minDecimals: 2, maxDecimals: 2}, true},
		{`0.00"%"`, percentFormat{}, false},
		{`0.00\%`, percentFormat{}, false},
		{`0.00;"%"`, percentFormat{}, false},
		{"0.00", percentFormat{}, false},
		{"General", percentFormat{}, false},
		{"@", percentFormat{}, false},
		// Printable by Excel, but not by this renderer: left as bare values.
		{"0.00E+00%", percentFormat{}, false},
		{"0,%", percentFormat{}, false},
		{"0,.0%", percentFormat{}, false},
		{"0%%", percentFormat{}, false},
	}
	for _, tt := range tests {
		got, ok := parsePercentFormat(tt.format)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parsePercentFormat(%q) = (%+v, %v), want (%+v, %v)", tt.format, got, ok, tt.want, tt.ok)
		}
	}
}

// Rendering must match what Excel prints: half away from zero on the value
// cut to 15 significant digits, optional decimals trimmed, thousands grouped.
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
		{-0.99, "0.00%", "-99.00%"},
		// Ties round half away from zero, after the binary product is cut
		// to 15 significant digits.
		{0.125, "0%", "13%"},
		{-0.125, "0%", "-13%"},
		{0.145, "0%", "15%"},
		{0.285, "0.0%", "28.5%"},
		{0.00125, "0.00%", "0.13%"},
		// Optional decimals print only when significant.
		{0.1, "0.0#%", "10.0%"},
		{0.12345, "0.0#%", "12.35%"},
		{0.5, "0.##%", "50%"},
		// Thousands grouping.
		{12.34, "#,##0%", "1,234%"},
		{1234.5678, "#,##0.00%", "123,456.78%"},
		// A negative value that rounds to zero keeps its sign.
		{-0.00001, "0.00%", "-0.00%"},
	}
	for _, tt := range tests {
		pf, ok := parsePercentFormat(tt.format)
		if !ok {
			t.Fatalf("parsePercentFormat(%q) not a percent format", tt.format)
		}
		if got := pf.render(tt.v); got != tt.want {
			t.Errorf("render(%v) under %q = %q, want %q", tt.v, tt.format, got, tt.want)
		}
	}
}
