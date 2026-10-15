package xls

import (
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

// percentFormat is how a percent number format prints its value: the decimals
// it always prints ('0' placeholders), the decimals it may print (every digit
// placeholder), and whether it groups thousands.
type percentFormat struct {
	minDecimals int
	maxDecimals int
	grouped     bool
}

// parsePercentFormat reports whether an Excel number-format string renders its
// value as a percentage, and how.
//
// Like isDateFormat it classifies the first section only, and consumes quoted
// literals, escapes, padding, fill and bracketed groups as it meets them, so a
// "%" inside any of those is text, not the percent operator.
//
// A percent format this renderer cannot print as Excel does is reported as not
// a percent format, so its cell keeps the bare stored value rather than a
// wrong rendering: an exponent, a "," that scales by 1000 rather than grouping
// thousands, or more than one "%".
func parsePercentFormat(s string) (percentFormat, bool) {
	var pf percentFormat
	percents := 0
	inFraction, intDigit, pendingComma := false, false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';':
			return pf, percents == 1 && !pendingComma
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
			}
		case '\\', '_', '*':
			i++
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return pf, false
			}
			i += end
		case '@', 'E', 'e':
			return pf, false
		case '0', '#', '?':
			if inFraction {
				pf.maxDecimals++
				if s[i] == '0' {
					pf.minDecimals++
				}
				continue
			}
			if pendingComma {
				pf.grouped = true
				pendingComma = false
			}
			intDigit = true
		case ',':
			if !inFraction && intDigit {
				pendingComma = true
			}
		case '.':
			if pendingComma {
				return pf, false
			}
			inFraction = true
		case '%':
			if pendingComma {
				return pf, false
			}
			percents++
		}
	}
	return pf, percents == 1 && !pendingComma
}

// builtinPercentFormats are the built-in percent formats: 9 is "0%" and 10 is
// "0.00%".
var builtinPercentFormats = map[uint16]percentFormat{
	9:  {},
	10: {minDecimals: 2, maxDecimals: 2},
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

// render prints a stored fraction as Excel does under this format: scaled by
// 100, rounded half away from zero to maxDecimals, trailing zeros trimmed down
// to minDecimals, thousands grouped if the format groups them, then "%".
//
// The scaled value is first cut to 15 significant digits, Excel's display
// precision, so a product such as 0.145*100 = 14.499999999999998 rounds as the
// 14.5 it stands for. Rounding is then done in exact decimal arithmetic, which
// strconv's half-to-even float rounding is not.
func (pf percentFormat) render(v float64) string {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v*100, 'g', 15, 64))
	if !ok {
		// NaN and the infinities have no percentage to print.
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	negative := r.Sign() < 0
	r.Abs(r)

	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(pf.maxDecimals)), nil)
	r.Mul(r, new(big.Rat).SetInt(scale))
	r.Add(r, big.NewRat(1, 2))
	digits := new(big.Int).Quo(r.Num(), r.Denom()).String()

	if len(digits) <= pf.maxDecimals {
		digits = strings.Repeat("0", pf.maxDecimals-len(digits)+1) + digits
	}
	intPart, frac := digits[:len(digits)-pf.maxDecimals], digits[len(digits)-pf.maxDecimals:]
	for len(frac) > pf.minDecimals && frac[len(frac)-1] == '0' {
		frac = frac[:len(frac)-1]
	}
	if pf.grouped {
		intPart = groupThousands(intPart)
	}

	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	b.WriteString(intPart)
	if frac != "" {
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
