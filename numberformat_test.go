package xls

import "testing"

// TestNumberFormatFixture reads testdata/numberformat.xls end to end, so the
// format classification runs through the record parser that builds the XF
// table and the FORMAT records rather than through a hand-built workbook.
//
// Column A names each case and column B carries the value; testdata's
// generator script documents the encoding each row is built to produce. Every
// value in the fixture is invented.
func TestNumberFormatFixture(t *testing.T) {
	want := []struct{ label, value string }{
		// Under a user-defined numeric format these three rendered as
		// RFC3339 timestamps -- "1714-11-10T02:07:11Z" and the like. The
		// negative one rendered as a date in 2167, the unsigned shift having
		// turned it into a large positive value.
		{"rk_int_money", "9999011"},
		{"rk_int_x100_money", "9999.25"},
		{"rk_int_x100_negative_money", "-9999.75"},
		// A NUMBER record reaches no format lookup at all, so this one was
		// never misrendered -- and is still not format-aware. Rendering
		// numbers per their format string is a separate change.
		{"number_money", "0.9999000000099991"},
		// Built-in formats carry no FORMAT record, so these two never
		// rendered as timestamps. They were simply decoded wrong: 0.99 read
		// as 99, and -0.99 as 1073741725.
		{"rk_int_x100_builtin_percent", "0.99"},
		{"rk_int_x100_negative_builtin_percent", "-0.99"},
		// A date under a user-defined date format still renders as an ISO
		// timestamp, unchanged. Consumers read .xls dates in this form.
		{"rk_int_date", "2024-01-15T00:00:00Z"},
	}

	wb, err := Open("testdata/numberformat.xls", "utf-8")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sheet := wb.GetSheet(0)
	if sheet == nil {
		t.Fatal("GetSheet(0) returned nil")
	}
	if got := int(sheet.MaxRow) + 1; got != len(want) {
		t.Fatalf("sheet has %d rows, want %d", got, len(want))
	}
	for i, tc := range want {
		row := sheet.Row(i)
		if row == nil {
			t.Errorf("row %d is missing", i)
			continue
		}
		if got := row.Col(0); got != tc.label {
			t.Errorf("row %d label = %q, want %q", i, got, tc.label)
			continue
		}
		if got := row.Col(1); got != tc.value {
			t.Errorf("%s = %q, want %q", tc.label, got, tc.value)
		}
	}
}
