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

// percentFormat is how a percent number format prints its value.
type percentFormat struct {
	minIntDigits int  // '0' placeholders before the point: zero-padding
	minDecimals  int  // '0' placeholders after the point: always printed
	maxDecimals  int  // all digit placeholders after the point
	point        bool // the format has a decimal point, printed even with no decimals
	grouped      bool // a ',' between integer placeholders groups thousands
	sections     int  // number of ';'-separated sections
}

// parsePercentFormat reports whether an Excel number-format string is a
// percent format this package renders, and how it prints.
//
// It accepts only what render reproduces exactly: a first section made of
// '0' and '#' placeholders, one '.', ',' grouping between integer placeholders,
// one '%', and colour or plain locale tags such as [Red] or [$-409]. Anything
// else in the first section -- literal text, quotes, escapes, padding, fill,
// '?', a fraction, an exponent, a scaling ',', a condition, a currency tag --
// would print differently from Excel, so such a format is reported as not a
// percent format and its cell keeps the bare stored value.
func parsePercentFormat(s string) (percentFormat, bool) {
	var pf percentFormat
	percents, digits := 0, 0
	intDigit, pendingComma := false, false
	i := 0
first:
	for ; i < len(s); i++ {
		switch c := s[i]; c {
		case ';':
			break first
		case '0', '#':
			digits++
			if pf.point {
				pf.maxDecimals++
				if c == '0' {
					pf.minDecimals++
				}
				continue
			}
			if c == '0' {
				pf.minIntDigits++
			}
			if pendingComma {
				pf.grouped = true
				pendingComma = false
			}
			intDigit = true
		case ',':
			if pf.point || !intDigit {
				return pf, false
			}
			pendingComma = true
		case '.':
			if pf.point || pendingComma {
				return pf, false
			}
			pf.point = true
		case '%':
			if pendingComma {
				return pf, false
			}
			percents++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 || !isDisplayOnlyTag(s[i+1:i+end]) {
				return pf, false
			}
			i += end
		default:
			return pf, false
		}
	}
	if percents != 1 || digits == 0 || pendingComma {
		return pf, false
	}
	pf.sections = 1 + countSectionSeparators(s[i:])
	return pf, true
}

// isDisplayOnlyTag reports whether a bracketed format group changes nothing in
// the printed text: a colour, or a locale tag with no currency symbol.
func isDisplayOnlyTag(group string) bool {
	switch g := strings.ToLower(group); {
	case g == "black", g == "blue", g == "cyan", g == "green",
		g == "magenta", g == "red", g == "white", g == "yellow":
		return true
	case strings.HasPrefix(g, "color"):
		return true
	case strings.HasPrefix(g, "$-"):
		return true
	}
	return false
}

// countSectionSeparators counts the ';' that separate sections, skipping
// those inside quoted literals, escapes and bracketed groups.
func countSectionSeparators(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';':
			n++
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
			}
		case '\\', '_', '*':
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return n
			}
			i += end
		}
	}
	return n
}

// builtinPercentFormats are the built-in percent formats: 9 is "0%" and 10 is
// "0.00%".
var builtinPercentFormats = map[uint16]percentFormat{
	9:  {minIntDigits: 1, sections: 1},
	10: {minIntDigits: 1, minDecimals: 2, maxDecimals: 2, point: true, sections: 1},
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
// false when it cannot, so the caller keeps the bare value: a negative value
// under a format with a negative section, zero under a format with a zero
// section, or a value with no finite percentage.
//
// The value is scaled by 100 and cut to 15 significant digits, Excel's display
// precision, so a product such as 0.145*100 = 14.499999999999998 rounds as the
// 14.5 it stands for. It is then rounded half away from zero to maxDecimals in
// exact decimal arithmetic, trailing zeros are trimmed down to minDecimals, the
// integer part is zero-padded to minIntDigits and grouped if the format groups
// thousands, and "%" is appended.
func (pf percentFormat) render(v float64) (string, bool) {
	if pf.sections >= 2 && v < 0 || pf.sections >= 3 && v == 0 {
		return "", false
	}
	scaled := v * 100
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) {
		return "", false
	}
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(scaled, 'g', 15, 64))
	if !ok {
		return "", false
	}
	negative := r.Sign() < 0
	r.Abs(r)

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pf.maxDecimals)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	r.Add(r, big.NewRat(1, 2))
	digits := new(big.Int).Quo(r.Num(), r.Denom()).String()
	if len(digits) < pf.maxDecimals {
		digits = strings.Repeat("0", pf.maxDecimals-len(digits)) + digits
	}

	intPart := strings.TrimLeft(digits[:len(digits)-pf.maxDecimals], "0")
	if len(intPart) < pf.minIntDigits {
		intPart = strings.Repeat("0", pf.minIntDigits-len(intPart)) + intPart
	}
	if pf.grouped {
		intPart = groupThousands(intPart)
	}
	frac := digits[len(digits)-pf.maxDecimals:]
	for len(frac) > pf.minDecimals && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteString(intPart)
	if pf.point {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	b.WriteByte('%')
	return b.String(), true
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
