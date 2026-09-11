package xls

import (
	"errors"
	"math"
	"testing"
)

// rkInt builds an RK word holding the integer variant of v.
func rkInt(v int32) RK { return RK(uint32(v<<2) | 0x02) }

// rkIntX100 builds an RK word holding the integer variant of v, flagged as
// hundredths: the value it denotes is v/100.
func rkIntX100(v int32) RK { return RK(uint32(v<<2) | 0x03) }

// rkFloat builds an RK word holding the float variant of f. Only doubles whose
// low 34 mantissa bits are zero survive the encoding, which is why the format
// falls back to a NUMBER record for anything finer.
func rkFloat(f float64) RK { return RK(uint32(math.Float64bits(f)>>34) << 2) }

// rkFloatX100 builds an RK word holding the float variant of f, flagged as
// hundredths: the value it denotes is f/100.
func rkFloatX100(f float64) RK { return RK(uint32(math.Float64bits(f)>>34)<<2 | 0x01) }

func TestRKNumber(t *testing.T) {
	tests := []struct {
		name        string
		rk          RK
		wantInt     int64
		wantFloat   float64
		wantIsFloat bool
	}{
		{name: "int", rk: rkInt(9999011), wantInt: 9999011},
		{name: "int zero", rk: rkInt(0), wantInt: 0},
		// The unsigned shift this replaces decoded every negative integer as
		// a large positive value near 2^30.
		{name: "int negative", rk: rkInt(-9999011), wantInt: -9999011},
		{name: "int negative one", rk: rkInt(-1), wantInt: -1},
		{name: "int max 30-bit", rk: rkInt(1<<29 - 1), wantInt: 1<<29 - 1},
		{name: "int min 30-bit", rk: rkInt(-(1 << 29)), wantInt: -(1 << 29)},
		// The hundredths flag was ignored on the integer variant, so a cell
		// holding 9999.25 decoded as 999925.
		{name: "int x100", rk: rkIntX100(999925), wantFloat: 9999.25, wantIsFloat: true},
		{name: "int x100 negative", rk: rkIntX100(-999975), wantFloat: -9999.75, wantIsFloat: true},
		{name: "int x100 sub-unit", rk: rkIntX100(99), wantFloat: 0.99, wantIsFloat: true},
		{name: "int x100 negative sub-unit", rk: rkIntX100(-99), wantFloat: -0.99, wantIsFloat: true},
		{name: "int x100 zero", rk: rkIntX100(0), wantFloat: 0, wantIsFloat: true},
		{name: "float", rk: rkFloat(0.5), wantFloat: 0.5, wantIsFloat: true},
		{name: "float negative", rk: rkFloat(-0.5), wantFloat: -0.5, wantIsFloat: true},
		{name: "float x100", rk: rkFloatX100(50), wantFloat: 0.5, wantIsFloat: true},
		{name: "float x100 negative", rk: rkFloatX100(-50), wantFloat: -0.5, wantIsFloat: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotInt, gotFloat, isFloat := tc.rk.number()
			if isFloat != tc.wantIsFloat {
				t.Fatalf("number() isFloat = %v, want %v", isFloat, tc.wantIsFloat)
			}
			if isFloat {
				if gotFloat != tc.wantFloat {
					t.Errorf("number() float = %v, want %v", gotFloat, tc.wantFloat)
				}
				return
			}
			if gotInt != tc.wantInt {
				t.Errorf("number() int = %v, want %v", gotInt, tc.wantInt)
			}
		})
	}
}

func TestRKString(t *testing.T) {
	tests := []struct {
		name string
		rk   RK
		want string
	}{
		{"int", rkInt(9999011), "9999011"},
		{"int negative", rkInt(-9999011), "-9999011"},
		{"int x100", rkIntX100(999925), "9999.25"},
		{"int x100 negative", rkIntX100(-999975), "-9999.75"},
		{"float", rkFloat(0.5), "0.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rk.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// formatWorkBook builds a workbook whose single XF points at one number
// format, so XfRk.String can be driven over the format classes without a file.
// It reaches encodings xlwt cannot emit, notably the RK float variant.
func formatWorkBook(formatNo uint16, formatStr string, hasFormatRecord bool) *WorkBook {
	wb := &WorkBook{
		Xfs:     []st_xf_data{&Xf8{Format: formatNo}},
		Formats: map[uint16]*Format{},
	}
	if hasFormatRecord {
		f := &Format{str: formatStr}
		f.Head.Index = formatNo
		wb.Formats[formatNo] = f
	}
	return wb
}

// The numeric formats below must render as numbers and the date formats as
// timestamps. The timestamp output is not incidental: callers read .xls date
// cells as ISO timestamps, so preserving it is a requirement of this change
// rather than a leftover.
func TestXfRkString(t *testing.T) {
	const numeric = `#,##0.0000_);\(#,##0.0000\)`
	const date = "yyyy-mm-dd"

	tests := []struct {
		name            string
		formatNo        uint16
		formatStr       string
		hasFormatRecord bool
		rk              RK
		want            string
	}{
		// User-defined numeric format: every encoding renders as a number.
		{"user numeric int", 165, numeric, true, rkInt(9999011), "9999011"},
		{"user numeric int x100", 165, numeric, true, rkIntX100(999925), "9999.25"},
		{"user numeric int x100 negative", 165, numeric, true, rkIntX100(-999975), "-9999.75"},
		{"user numeric int negative", 165, numeric, true, rkInt(-9999011), "-9999011"},
		{"user numeric float", 165, numeric, true, rkFloat(0.5), "0.5"},
		{"user numeric float x100", 165, numeric, true, rkFloatX100(50), "0.5"},
		// User-defined date format: renders as a timestamp. All three
		// encodings are pinned, not just the plain integer: the timestamp
		// output is the contract callers read dates through, and the fX100
		// and float branches are the two whose decode changed --
		// so a regression in either would land here and nowhere else.
		{"user date int", 166, date, true, rkInt(45306), "2024-01-15T00:00:00Z"},
		{"user date int x100", 166, date, true, rkIntX100(4530650), "2024-01-15T12:00:00Z"},
		{"user date float", 166, date, true, rkFloat(45306.5), "2024-01-15T12:00:00Z"},
		// A user-defined format index with no FORMAT record carries no format
		// string to classify, so the value renders as a number. A record
		// present but empty is a distinct branch and lands the same way.
		{"user index without format record", 167, "", false, rkIntX100(999925), "9999.25"},
		{"user empty format record", 168, "", true, rkIntX100(999925), "9999.25"},
		// Built-in formats are not classified at all; they render as numbers
		// except in the built-in date range.
		{"builtin percent int x100", 10, "", false, rkIntX100(99), "0.99"},
		{"builtin percent int x100 negative", 10, "", false, rkIntX100(-99), "-0.99"},
		{"builtin general int", 0, "", false, rkInt(9999011), "9999011"},
		{"builtin two-decimal int x100", 2, "", false, rkIntX100(999925), "9999.25"},
		// The built-in date range keeps its own lossy rendering, unchanged.
		{"builtin date int", 14, "", false, rkInt(45306), "2024.01"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wb := formatWorkBook(tc.formatNo, tc.formatStr, tc.hasFormatRecord)
			xf := &XfRk{Index: 0, Rk: tc.rk}
			if got := xf.String(wb); got != tc.want {
				t.Errorf("XfRk.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// An XF index past the end of the workbook's XF table must not panic; the
// value renders unformatted.
func TestXfRkStringOutOfRangeXf(t *testing.T) {
	wb := &WorkBook{Formats: map[uint16]*Format{}}
	xf := &XfRk{Index: 7, Rk: rkIntX100(999925)}
	if got := xf.String(wb); got != "9999.25" {
		t.Errorf("XfRk.String() = %q, want %q", got, "9999.25")
	}
}

// Float reports ErrIsInt only for the encodings that really do hold an
// integer. Honouring fX100 on the integer variant moved the hundredths form
// out of that set: it now yields a value, where it used to report ErrIsInt.
func TestRKFloat(t *testing.T) {
	tests := []struct {
		name    string
		rk      RK
		want    float64
		wantErr error
	}{
		{name: "int", rk: rkInt(9999011), wantErr: ErrIsInt},
		{name: "int negative", rk: rkInt(-9999011), wantErr: ErrIsInt},
		{name: "int x100", rk: rkIntX100(999925), want: 9999.25},
		{name: "int x100 negative", rk: rkIntX100(-999975), want: -9999.75},
		{name: "float", rk: rkFloat(0.5), want: 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.rk.Float()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Float() error = %v, want %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("Float() = %v, want %v", got, tc.want)
			}
		})
	}
}
