# xls

A maintained fork of [extrame/xls](https://github.com/extrame/xls) at tag
`v0.0.1` — upstream commit
[`f02a04c`](https://github.com/extrame/xls/commit/f02a04cbdd7aa643a55358e986ab58959ce2cc11)
— re-moduled as `github.com/Standard-Innovation/xls` and carrying number-format
fixes. Apache-2.0, as upstream: see [LICENSE](LICENSE) and [NOTICE](NOTICE).

This is not original work. Every file outside `numfmt.go`, one block of
`col.go`, and the tests is upstream's, unmodified — including `LICENSE`, which
is upstream's verbatim. `NOTICE` lists exactly which files differ from
`v0.0.1`.

What changed and why is summarised below. The change-by-change record lives in
this repository's commit and pull-request history rather than in a design
document checked in here, so there is one place to read it and it cannot drift
from the code.

## Why this fork exists

`v0.0.1` decodes some numeric cells wrongly, and there is no seam to correct it
from outside the package: `Row.cols` and `WorkSheet.rows` are unexported,
`contentHandler` is an unexported interface, and although `WorkBook.Formats` is
exported, `Format.str` is not — so a consumer can learn a cell's number-format
index but never its format string. The rendering has to be fixed in the reader.

Two defects are fixed here, and they have to be fixed together: correcting only
the first turns a money cell that rendered as a timestamp into one that renders
100x too large.

1. `XfRk.String` rendered *every* RK cell carrying a user-defined number format
   (index >= 164, with a FORMAT record) as an RFC3339 timestamp, whatever the
   format actually was — so a cell formatted as money rendered as a date. It now
   classifies the format string and takes the timestamp path only for a real
   date format. Date-formatted cells still render as RFC3339 timestamps.

   Their values are unchanged for the plain-integer and float encodings —
   between them, every whole-day date. A date *time* stored in the
   integer/hundredths encoding does change, and is corrected: Excel uses that
   encoding whenever the serial times 100 is integral (06:00, 12:00, 18:00 and
   every 14.4-minute multiple), and such a cell used to render as a plausible
   but wrong date, because `timeFromExcelTime` overflows int64 on the
   hundred-fold serial. A serial for 2024-01-15 12:00 rendered as
   `2028-11-09T08:54:12Z`.
2. `RK.number` decoded the RK payload with an unsigned shift, so negative
   30-bit integers decoded as large positive values near 2^30; and it ignored
   the record's fX100 flag on the integer variant, so a cell holding 9999.25
   decoded as 999925. Excel prefers that encoding for any value with at most
   two decimals that fits 30 bits, which is most money. Upstream's master
   branch carries an equivalent fix.

Not fixed here: `NumberCol.String` still ignores number formats entirely, so a
NUMBER-encoded cell renders as its full stored precision rather than as
printed. Rendering numbers per their format string needs the whole numeric
format grammar and changes the output of every formatted numeric cell, so it is
deliberately separate work.

## Known limitation of the format classification

`isDateFormat` recognises Excel's ASCII date codes (`y m d h s`). Excel
normally writes those codes even for a localised display format, but a file
written by some localised builds can carry native codes instead — a Cyrillic
`дд.мм.гггг`, say. Such a format is classified as numeric, so the cell renders
as its serial number rather than as a date. `v0.0.1` rendered it as a date by
rendering *everything* as a date, which is the defect being fixed here. The
failure mode is a visibly wrong cell rather than a silently wrong amount, and
covering it properly means a per-locale code table, so it is left open.

## Known issues inherited from upstream

- `worksheet.go` prints parse errors to stdout with `fmt.Println`.
- `addFormat` calls `os.Exit(1)` when the format map is nil.
- `extrame/ole2`'s DIFAT walk does not terminate on a FREESECT terminator, so
  some containers allocate without bound. Callers guard this today.
- The BIFF5 FORMAT record has a one-byte length, which `Head.Size uint16`
  misreads.
- The tree predates modern gofmt comment spacing and is left as upstream had it
  so the fix stays reviewable against `v0.0.1`.

## Do not sync `XfRk.String` from upstream

Upstream's master branch has since rewritten the same block, and its version
must not be taken:

- it renders the date path through the format string (`goyymmdd`) rather than
  RFC3339, which breaks the ISO-timestamp shape consumers parse dates in;
- it classifies formats by substring (`"#"`, `".00"`, `"m/y"`, `"h:"`), which
  reads a plain `0.0` as a date; and
- its `NumberCol.String` dereferences `wb.Formats[fNo].str` for any non-zero
  format index, without checking that a FORMAT record exists — so it
  nil-panics on any built-in format other than index 0, which its own guard
  happens to exclude.

Its `RK.number` fix is sound, and equivalent to this one: it shifts
`int32(rk) >> 2` in both branches and relies on the sign-extended high bits
being shifted out past bit 63, where this one shifts the unsigned word in the
float branch. Same values, different text.

## Upstream README

The text below is upstream's, and describes the package under its original
module path.

Pure Golang xls library writen by [MinkTech(chinese)](http://www.mink-tech.com). 

Thanks for contributions from Tamás Gulácsi. 

**English User please mailto** [Liu Ming](mailto:liuming@mink-tech.com)

This is a xls library writen in pure Golang. Almostly it is translated from the libxls library in c.

It has just the reading function without the format.

# Basic Usage

* Use **Open** function for open file
* Use **OpenReader** function for open xls from a reader

These methods will open a workbook object for reading, like

	func (w *WorkBook) ReadAllCells() (res [][]string) {
		for _, sheet := range w.Sheets {
			w.PrepareSheet(sheet)
			if sheet.MaxRow != 0 {
				temp := make([][]string, sheet.MaxRow+1)
				for k, row := range sheet.Rows {
					data := make([]string, 0)
					if len(row.Cols) > 0 {
						for _, col := range row.Cols {
							if uint16(len(data)) <= col.LastCol() {
								data = append(data, make([]string, col.LastCol()-uint16(len(data))+1)...)
							}
							str := col.String(w)
							for i := uint16(0); i < col.LastCol()-col.FirstCol()+1; i++ {
								data[col.FirstCol()+i] = str[i]
							}
						}
						temp[k] = data
					}
				}
				res = append(res, temp...)
			}
		}
		return
	}

