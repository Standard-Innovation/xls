package xls

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/richardlehane/mscfb"
)

// boundsheetFilepos finds the first BOUNDSHEET record in the fixture's
// workbook stream and returns the position it declares for that sheet, which
// is an offset into the same stream.
//
// The stream begins at the first sector and the fixture's sectors are
// contiguous, so a scan of the file's own bytes reaches the same records the
// reader does. The record is located rather than hardcoded so the helper fails
// loudly, rather than returning a stale number, if the fixture is regenerated.
func boundsheetFilepos(t *testing.T, container []byte) int64 {
	t.Helper()

	const (
		boundsheetRecordID = 0x85
		firstSectorOffset  = 512
		streamSize         = 4096
	)

	for offset := firstSectorOffset; offset+8 < firstSectorOffset+streamSize; {
		id := binary.LittleEndian.Uint16(container[offset:])
		length := int(binary.LittleEndian.Uint16(container[offset+2:]))
		if id == boundsheetRecordID {
			return int64(binary.LittleEndian.Uint32(container[offset+4:]))
		}
		if id == 0 && length == 0 {
			break
		}
		offset += 4 + length
	}
	t.Fatalf("no BOUNDSHEET record in the container fixture's workbook stream")
	return 0
}

// openWorkbookStream returns the fixture's workbook stream, read through the
// real container reader so the seek semantics under test are the ones that
// ship rather than a stand-in for them.
func openWorkbookStream(t *testing.T) io.ReadSeeker {
	t.Helper()
	doc, err := mscfb.New(bytes.NewReader(containerFixture(t)))
	if err != nil {
		t.Fatalf("open the container fixture: %v", err)
	}
	stream, err := workbookStream(doc)
	if err != nil {
		t.Fatalf("find the workbook stream: %v", err)
	}
	return stream
}

// TestPrepareSheet_PositionOutsideTheStream_YieldsAnEmptySheet covers a
// BOUNDSHEET whose declared position lies beyond the end of the workbook
// stream.
//
// The container layer under this reader reports a seek past end-of-stream as
// an error and leaves the stream offset untouched, where the layer it replaced
// clamped to the end and reported io.EOF. prepareSheet discarded that error,
// so the sheet was parsed from wherever the offset happened to be and returned
// as that sheet's content, with nothing reporting that the position the file
// gave had been ignored.
//
// The offset is placed deliberately here, because where it happens to sit is
// the whole point. A workbook that has just been opened leaves it at
// end-of-stream, so the bug yields an empty sheet by luck and hides; after any
// sheet has been read it sits at the end of that sheet's records, which is
// where the *next* sheet's records begin. This test puts it at the start of
// real records — the state a multi-sheet workbook is in for every sheet after
// the first — and asserts the sheet comes back empty rather than populated
// from bytes the file did not point at.
func TestPrepareSheet_PositionOutsideTheStream_YieldsAnEmptySheet(t *testing.T) {
	// Any position at or past the declared stream size; the container reader
	// refuses to seek there.
	const beyondTheStream = 0x00100000

	container := containerFixture(t)
	realSheetStart := boundsheetFilepos(t, container)

	stream := openWorkbookStream(t)
	wb := newWorkBookFromStream(stream)

	// A sheet whose records genuinely are where it says: the harness has to be
	// able to produce rows at all, or the assertion below proves nothing.
	present := &WorkSheet{bs: &boundsheet{Filepos: uint32(realSheetStart)}, wb: wb}
	wb.prepareSheet(present)
	if len(present.rows) == 0 {
		t.Fatalf("a sheet at its declared position returned no rows; the fixture proves nothing")
	}

	// Now the same stream, positioned at those same real records, and a sheet
	// that declares a position outside the stream entirely.
	if _, err := stream.Seek(realSheetStart, io.SeekStart); err != nil {
		t.Fatalf("position the stream at the fixture's sheet records: %v", err)
	}
	absent := &WorkSheet{bs: &boundsheet{Filepos: beyondTheStream}, wb: wb}
	wb.prepareSheet(absent)

	if got := len(absent.rows); got != 0 {
		t.Errorf("a sheet declared outside the stream returned %d rows, want none: its records came from the offset the stream was already at, not the one the file named", got)
	}
	if absent.MaxRow != 0 {
		t.Errorf("a sheet declared outside the stream reports max row %d, want 0", absent.MaxRow)
	}
	if !absent.parsed {
		t.Error("a sheet declared outside the stream was left unparsed, so every read of it would try again")
	}
}
