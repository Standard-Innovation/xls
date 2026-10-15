package xls

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// isDateFormat reports whether an Excel number-format string renders its value
// as a date or a time rather than as a number or as text.
//
// Only the first section is classified: a format's sections are
// positive;negative;zero;text, and the positive section decides the kind.
//
// The scan is a single pass rather than a sequence of strips because every
// construct it has to ignore -- quoted literals, escaped characters, padding,
// fill, and bracketed groups -- can hold a character that would otherwise read
// as a date code, and can itself hold a section separator. Stripping them in
// stages leaves the order of the stages load-bearing; consuming them as they
// are met does not.
func isDateFormat(s string) bool {
	dated := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';':
			return dated
		case '"':
			// Quoted literal: consume through the closing quote, or to the
			// end of a truncated format.
			for i++; i < len(s) && s[i] != '"'; i++ {
			}
		case '\\', '_', '*':
			// An escaped literal, a padding width and a fill character each
			// consume the byte that follows them.
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return dated
			}
			if isElapsedTimeCode(s[i+1 : i+end]) {
				dated = true
			}
			i += end
		case '@':
			// A text section never renders a date.
			return false
		case 'y', 'm', 'd', 'h', 's', 'Y', 'M', 'D', 'H', 'S':
			dated = true
		}
	}
	return dated
}

// isElapsedTimeCode reports whether the contents of a bracketed format group
// are one of Excel's elapsed-time codes. Those are date codes; every other
// bracketed group -- a colour, a condition, a locale such as [$-409], a
// currency symbol -- is not, and its letters must not be read as date codes.
func isElapsedTimeCode(group string) bool {
	switch strings.ToLower(group) {
	case "h", "hh", "m", "mm", "s", "ss":
		return true
	}
	return false
}

// percentFormat is how a percent number format prints a value. Like Excel, it
// applies its second section to negative values and its third to zero; the
// first section takes every value no other section covers.
type percentFormat struct {
	positive percentSection
	negative *percentSection
	zero     *percentSection
}

// percentSection is how one section of a percent format prints its value.
type percentSection struct {
	minIntDigits int  // placeholders from the leftmost integer '0': zero-padding
	minDecimals  int  // placeholders through the rightmost decimal '0': always printed
	maxDecimals  int  // all decimal placeholders
	point        bool // the section has a decimal point, printed even with no decimals
	grouped      bool // a ',' between integer placeholders groups thousands
	minus        bool // a literal '-' before the number (negative section only)
}

// parsePercentFormat reports whether an Excel number-format string is a
// percent format this package renders exactly as Excel does, and how.
//
// Every numeric section must be made of '0' and '#' placeholders, one '.',
// ',' grouping between integer placeholders, and one trailing '%', with
// colour or plain locale tags such as [Red] or [$-409]; the negative section
// may also start its number with a literal '-'. A fourth section applies only
// to text and is not read. Anything else -- literal text, quotes, escapes,
// padding, fill, '?', a fraction, an exponent, a scaling ',', a '%' before a
// placeholder, a condition, a currency tag, an empty section -- would print
// differently from Excel, so the whole format is reported as not a percent
// format and every cell under it keeps its bare value. Rejecting the whole
// format, rather than one section, keeps a column from mixing percentages
// with bare fractions.
func parsePercentFormat(s string) (percentFormat, bool) {
	sections := splitSections(s)
	if len(sections) > 4 {
		return percentFormat{}, false
	}
	if len(sections) == 4 {
		sections = sections[:3]
	}
	var pf percentFormat
	for i, sec := range sections {
		ps, ok := parsePercentSection(sec, i == 1)
		if !ok {
			return percentFormat{}, false
		}
		switch i {
		case 0:
			pf.positive = ps
		case 1:
			pf.negative = &ps
		case 2:
			pf.zero = &ps
		}
	}
	return pf, true
}

// parsePercentSection parses one section of a percent format; allowMinus
// admits a literal '-' before the number, as a negative section may print.
func parsePercentSection(s string, allowMinus bool) (percentSection, bool) {
	var ps percentSection
	var intPlaceholders, decPlaceholders []byte
	percent, minus, pendingComma := false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if percent && c != '[' {
			// Only tags may follow the '%': render prints it last.
			return ps, false
		}
		switch c {
		case '0', '#':
			if ps.point {
				decPlaceholders = append(decPlaceholders, c)
				continue
			}
			if pendingComma {
				ps.grouped = true
				pendingComma = false
			}
			intPlaceholders = append(intPlaceholders, c)
		case ',':
			if ps.point || pendingComma || len(intPlaceholders) == 0 {
				return ps, false
			}
			pendingComma = true
		case '.':
			if ps.point || pendingComma {
				return ps, false
			}
			ps.point = true
		case '%':
			if pendingComma {
				return ps, false
			}
			percent = true
		case '-':
			if !allowMinus || minus || ps.point || len(intPlaceholders) > 0 {
				return ps, false
			}
			minus = true
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 || !isDisplayOnlyTag(s[i+1:i+end]) {
				return ps, false
			}
			i += end
		default:
			return ps, false
		}
	}
	if !percent || len(intPlaceholders)+len(decPlaceholders) == 0 {
		return ps, false
	}
	if j := strings.IndexByte(string(intPlaceholders), '0'); j >= 0 {
		ps.minIntDigits = len(intPlaceholders) - j
	}
	ps.minDecimals = strings.LastIndexByte(string(decPlaceholders), '0') + 1
	ps.maxDecimals = len(decPlaceholders)
	ps.minus = minus
	return ps, true
}

// isDisplayOnlyTag reports whether a bracketed format group changes nothing in
// the printed text: one of Excel's colours, or a locale tag that names no
// currency symbol and no native digit set.
func isDisplayOnlyTag(group string) bool {
	g := strings.ToLower(group)
	switch g {
	case "black", "blue", "cyan", "green", "magenta", "red", "white", "yellow":
		return true
	}
	if n, ok := strings.CutPrefix(g, "color"); ok {
		v, err := strconv.Atoi(n)
		return isDigits(n) && n[0] != '0' && err == nil && v >= 1 && v <= 56
	}
	if lcid, ok := strings.CutPrefix(g, "$-"); ok {
		// The high byte of a locale tag selects a digit set; zero keeps the
		// ASCII digits this package prints. F400 and F800 are the system time
		// and long-date formats, not locales.
		v, err := strconv.ParseUint(lcid, 16, 32)
		return err == nil && v>>24 == 0 && v != 0xF400 && v != 0xF800
	}
	return false
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// splitSections splits a format string on the ';' that separate its sections,
// leaving those inside quoted literals, escapes and bracketed groups.
func splitSections(s string) []string {
	var sections []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';':
			sections = append(sections, s[start:i])
			start = i + 1
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
			}
		case '\\', '_', '*':
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				i = len(s)
				continue
			}
			i += end
		}
	}
	return append(sections, s[start:])
}

// builtinPercentFormats are the built-in percent formats: 9 is "0%" and 10 is
// "0.00%".
var builtinPercentFormats = map[uint16]percentFormat{
	9:  {positive: percentSection{minIntDigits: 1}},
	10: {positive: percentSection{minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true}},
}

// percentFormatOf classifies a cell's number format as a percent format. A
// FORMAT record, when the workbook has one for the index, decides; otherwise
// only the built-in percent indices do.
func percentFormatOf(formatNo uint16, f *Format) (percentFormat, bool) {
	if f != nil {
		return parsePercentFormat(f.str)
	}
	pf, ok := builtinPercentFormats[formatNo]
	return pf, ok
}

// render prints a stored fraction as Excel does under this format, and reports
// false for NaN and the infinities, which have no percentage to print.
//
// The stored value is first cut to 15 significant digits, Excel's display
// precision, then scaled by 100 in exact decimal arithmetic, so 0.145 rounds as
// the 14.5 it stands for rather than as 0.145*100 = 14.499999999999998. The
// section for the value's sign then rounds it half away from zero, trims
// optional decimals, zero-pads and groups the integer part, and appends "%".
func (pf percentFormat) render(v float64) (string, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", false
	}
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(v, 'g', 15, 64))
	r.Mul(r, big.NewRat(100, 1))

	sec, signed := pf.positive, r.Sign() < 0
	switch {
	case v < 0 && pf.negative != nil:
		sec, signed = *pf.negative, pf.negative.minus
	case v == 0 && pf.zero != nil:
		sec, signed = *pf.zero, false
	}
	return sec.render(r.Abs(r), signed), true
}

// render prints a non-negative percentage under this section, preceded by a
// '-' if signed.
func (ps percentSection) render(r *big.Rat, signed bool) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(ps.maxDecimals)), nil)
	r = new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	r.Add(r, big.NewRat(1, 2))
	digits := new(big.Int).Quo(r.Num(), r.Denom()).String()
	if len(digits) < ps.maxDecimals {
		digits = strings.Repeat("0", ps.maxDecimals-len(digits)) + digits
	}

	intPart := strings.TrimLeft(digits[:len(digits)-ps.maxDecimals], "0")
	if len(intPart) < ps.minIntDigits {
		intPart = strings.Repeat("0", ps.minIntDigits-len(intPart)) + intPart
	}
	if ps.grouped {
		intPart = groupThousands(intPart)
	}
	frac := digits[len(digits)-ps.maxDecimals:]
	for len(frac) > ps.minDecimals && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}

	var b strings.Builder
	if signed {
		b.WriteByte('-')
	}
	b.WriteString(intPart)
	if ps.point {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	b.WriteByte('%')
	return b.String()
}

// groupThousands inserts a "," every three digits from the right.
func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	head := len(digits) % 3
	if head == 0 {
		head = 3
	}
	var b strings.Builder
	b.WriteString(digits[:head])
	for i := head; i < len(digits); i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// cellFormat resolves the cell style at xfIndex to its number-format index and,
// if the workbook carries one, that index's FORMAT record. ok is false for an
// XF index past the end of the workbook's XF table.
func (wb *WorkBook) cellFormat(xfIndex uint16) (formatNo uint16, f *Format, ok bool) {
	idx := int(xfIndex)
	if idx >= len(wb.Xfs) {
		return 0, nil, false
	}
	formatNo = wb.Xfs[idx].formatNo()
	return formatNo, wb.Formats[formatNo], true
}
