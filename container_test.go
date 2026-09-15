package xls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"runtime"
	"testing"
)

// containerFixture reads the committed compound-file fixture so a test can
// patch one header or directory field in a copy of it. Every shape a test
// here needs is a one-field edit away from a file that reads normally, which
// keeps the difference under test to that one field.
func containerFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/numberformat.xls")
	if err != nil {
		t.Fatalf("read container fixture: %v", err)
	}
	if len(b) < cfbHeaderLen {
		t.Fatalf("container fixture is %d bytes, shorter than a header", len(b))
	}
	return b
}

// sheetShape renders a workbook down to a value the test can compare without
// naming what is in it.
func sheetShape(t *testing.T, wb *WorkBook) []int {
	t.Helper()
	shape := make([]int, 0, wb.NumSheets())
	for i := 0; i < wb.NumSheets(); i++ {
		sheet := wb.GetSheet(i)
		if sheet == nil {
			shape = append(shape, -1)
			continue
		}
		shape = append(shape, int(sheet.MaxRow))
	}
	return shape
}

// TestOpenReader_DeclaredDirectorySectorCountDoesNotSizeAnAllocation pins the
// clamp in boundDirectorySectorCount. The container reader underneath sizes
// its directory slice straight from the header's directory-sector count
// before reading any directory sector, so a header declaring the maximum
// uint32 reserves ~34 GB for a file of a few KB. The field is unvalidated
// there and, at major version 3, the format requires it to be zero and gives
// it no meaning — so the file must still read, and read identically, while
// the allocation stays proportionate to the container.
//
// The bound is asserted on TotalAlloc, which is cumulative and so cannot be
// flattered by a collection landing between the two readings.
func TestOpenReader_DeclaredDirectorySectorCountDoesNotSizeAnAllocation(t *testing.T) {
	const allocBound = 32 << 20

	clean := containerFixture(t)
	patched := bytes.Clone(clean)
	binary.LittleEndian.PutUint32(patched[cfbNumDirectorySectorsOffset:cfbNumDirectorySectorsOffset+4], ^uint32(0))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	wb, err := OpenReader(bytes.NewReader(patched), "utf-8")
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("OpenReader on a container declaring the maximum directory-sector count: %v", err)
	}

	if grew := after.TotalAlloc - before.TotalAlloc; grew > allocBound {
		t.Errorf("open allocated %d bytes, want at most %d: the declared directory-sector count is sizing an allocation", grew, allocBound)
	}

	reference, err := OpenReader(bytes.NewReader(clean), "utf-8")
	if err != nil {
		t.Fatalf("OpenReader on the unpatched container: %v", err)
	}
	got, want := sheetShape(t, wb), sheetShape(t, reference)
	if len(got) != len(want) {
		t.Fatalf("patched container yielded %d sheets, unpatched yielded %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sheet %d: patched container yielded max row %d, unpatched yielded %d", i, got[i], want[i])
		}
	}
}

// TestOpenReader_UnreachableWorkbookEntry_ReturnsError covers a container
// whose workbook directory entry is present but linked into nothing: the root
// storage's child pointer is NOSTREAM, so a reader that walks the directory
// tree — rather than scanning the sector's fixed-size slots linearly — never
// reaches the entry. OpenReader has to say so, because a nil workbook and a
// nil error would be indistinguishable from success at every call site.
func TestOpenReader_UnreachableWorkbookEntry_ReturnsError(t *testing.T) {
	const (
		directorySectorLocOffset = 48
		rootChildIDOffset        = 76
		noStream                 = 0xFFFFFFFF
	)

	patched := containerFixture(t)
	sectorSize := int64(1) << binary.LittleEndian.Uint16(patched[cfbSectorShiftOffset:cfbSectorShiftOffset+2])
	directorySector := int64(binary.LittleEndian.Uint32(patched[directorySectorLocOffset : directorySectorLocOffset+4]))
	rootChild := (directorySector+1)*sectorSize + rootChildIDOffset
	if rootChild+4 > int64(len(patched)) {
		t.Fatalf("container fixture has no directory sector at index %d", directorySector)
	}
	binary.LittleEndian.PutUint32(patched[rootChild:rootChild+4], noStream)

	wb, err := OpenReader(bytes.NewReader(patched), "utf-8")
	if !errors.Is(err, ErrNoWorkbookStream) {
		t.Fatalf("OpenReader error = %v, want %v", err, ErrNoWorkbookStream)
	}
	if wb != nil {
		t.Errorf("OpenReader returned a workbook alongside its error")
	}
}

// synthHeader is a bare compound-file header — enough for
// boundDirectorySectorCount, which reads nothing else — inside a container of
// the given total size.
func synthHeader(t *testing.T, majorVersion, sectorShift uint16, declared uint32, totalSize int) []byte {
	t.Helper()
	if totalSize < cfbHeaderLen {
		t.Fatalf("synthHeader: totalSize %d is shorter than a header", totalSize)
	}
	b := make([]byte, totalSize)
	binary.LittleEndian.PutUint16(b[cfbMajorVersionOffset:cfbMajorVersionOffset+2], majorVersion)
	binary.LittleEndian.PutUint16(b[cfbSectorShiftOffset:cfbSectorShiftOffset+2], sectorShift)
	binary.LittleEndian.PutUint32(b[cfbNumDirectorySectorsOffset:cfbNumDirectorySectorsOffset+4], declared)
	return b
}

// presentedDirectorySectorCount is the directory-sector count a reader sees
// through boundDirectorySectorCount, read back through a request that starts
// mid-field so the overlap arithmetic is exercised rather than assumed.
func presentedDirectorySectorCount(t *testing.T, reader interface {
	ReadAt([]byte, int64) (int, error)
}) uint32 {
	t.Helper()
	var field [4]byte
	if _, err := reader.ReadAt(field[:2], cfbNumDirectorySectorsOffset); err != nil {
		t.Fatalf("read first half of the directory-sector count: %v", err)
	}
	if _, err := reader.ReadAt(field[2:], cfbNumDirectorySectorsOffset+2); err != nil {
		t.Fatalf("read second half of the directory-sector count: %v", err)
	}
	return binary.LittleEndian.Uint32(field[:])
}

// TestBoundDirectorySectorCount_PresentsABoundedField pins what each version
// of the format is given. Version 3 must declare zero and the field means
// nothing there, so it is presented as zero however it was written. Version 4
// does count directory sectors, so a count the container could hold is passed
// through untouched and a larger one is cut to the sector count of the
// container itself.
func TestBoundDirectorySectorCount_PresentsABoundedField(t *testing.T) {
	const (
		v3           = 3
		v4           = 4
		shift512     = 9
		shift4096    = 12
		threeSectors = 3 * 4096
	)

	for _, tc := range []struct {
		name         string
		majorVersion uint16
		sectorShift  uint16
		declared     uint32
		totalSize    int
		want         uint32
	}{
		{"version 3 declaring the maximum", v3, shift512, ^uint32(0), 8 * 512, 0},
		{"version 3 declaring a plausible count", v3, shift512, 2, 8 * 512, 0},
		{"version 4 declaring the maximum", v4, shift4096, ^uint32(0), threeSectors, 3},
		{"version 4 declaring a count the container holds", v4, shift4096, 2, threeSectors, 2},
		{"version 4 with an unreadable sector size", v4, 0, ^uint32(0), threeSectors, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := synthHeader(t, tc.majorVersion, tc.sectorShift, tc.declared, tc.totalSize)
			got := presentedDirectorySectorCount(t, boundDirectorySectorCount(bytes.NewReader(b)))
			if got != tc.want {
				t.Errorf("presented directory-sector count = %d, want %d", got, tc.want)
			}
			if binary.LittleEndian.Uint32(b[cfbNumDirectorySectorsOffset:cfbNumDirectorySectorsOffset+4]) != tc.declared {
				t.Errorf("the underlying container was rewritten; it must be left alone")
			}
		})
	}
}
