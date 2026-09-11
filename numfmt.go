package xls

import "strings"

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
