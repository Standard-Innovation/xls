package xls

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
	"testing"

	"github.com/richardlehane/mscfb"
)

// containerFixture reads the committed compound-file fixture so a test can
// patch one header or directory field in a copy of it. Every shape a test
// here needs is a one-field edit away from a container that reads normally,
// which keeps the difference under test to that one field.
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

// maxDirectorySectorCount returns the fixture with its directory-sector count
// set to the largest uint32 — the value that sizes a ~34 GB slice in the
// container reader if it reaches it.
func maxDirectorySectorCount(t *testing.T) []byte {
	t.Helper()
	b := bytes.Clone(containerFixture(t))
	binary.LittleEndian.PutUint32(b[cfbNumDirectorySectorsOffset:cfbNumDirectorySectorsOffset+4], ^uint32(0))
	return b
}

// openAllocation is the number of bytes allocated across one OpenReader call.
// TotalAlloc is cumulative, so a collection landing between the two readings
// cannot flatter the result.
func openAllocation(t *testing.T, reader io.ReaderAt) (*WorkBook, uint64, error) {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	wb, err := OpenReader(reader)
	runtime.ReadMemStats(&after)
	return wb, after.TotalAlloc - before.TotalAlloc, err
}

// allocationSlack is what one open may exceed another by before the
// difference is a bound failing rather than noise. Observed difference
// between a clean open and a maxed-out one, with the bound working: under a
// kilobyte, against a ~70 KiB open.
//
// The ceiling is deliberately derived from the container's own clean open
// rather than written down as an absolute. An absolute has to be picked above
// every *other* allocation the reader can be talked into, and that figure is
// larger than it looks: the DIFAT preallocation reaches 32,515,992 B on a
// 512-byte-sector header declaring enough FAT sectors, and 261,896,136 B
// (249.76 MiB) once the same header also declares 4096-byte sectors, which
// takes the per-sector DIFAT entry count from 127 to 1023 — eight times, not
// four. Any flat ceiling in that range would pass on a bound that had stopped
// working.
const allocationSlack = 1 << 20

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

func requireSameShape(t *testing.T, got, want *WorkBook) {
	t.Helper()
	g, w := sheetShape(t, got), sheetShape(t, want)
	if len(g) != len(w) {
		t.Fatalf("got %d sheets, want %d", len(g), len(w))
	}
	for i := range w {
		if g[i] != w[i] {
			t.Errorf("sheet %d: got max row %d, want %d", i, g[i], w[i])
		}
	}
}

// TestOpenReader_DeclaredDirectorySectorCountDoesNotSizeAnAllocation pins the
// bound in boundedContainerReader. The container reader underneath sizes its
// directory slice straight from the header's directory-sector count before
// reading any directory sector, so a header declaring the maximum uint32
// reserves ~34 GB for a container of a few KB. The field is unvalidated there
// and, at major version 3, the format requires it to be zero and gives it no
// meaning — so the container must still read, and read identically, while the
// allocation stays proportionate.
func TestOpenReader_DeclaredDirectorySectorCountDoesNotSizeAnAllocation(t *testing.T) {
	clean := containerFixture(t)
	reference, referenceAlloc, err := openAllocation(t, bytes.NewReader(clean))
	if err != nil {
		t.Fatalf("OpenReader on the unpatched container: %v", err)
	}

	wb, alloc, err := openAllocation(t, bytes.NewReader(maxDirectorySectorCount(t)))
	if err != nil {
		t.Fatalf("OpenReader on a container declaring the maximum directory-sector count: %v", err)
	}
	if alloc > referenceAlloc+allocationSlack {
		t.Errorf("open allocated %d bytes against %d for the same container unpatched: the declared directory-sector count is sizing an allocation", alloc, referenceAlloc)
	}
	requireSameShape(t, wb, reference)
}

// errTransient is the failure flakyReaderAt injects, named so a test can
// assert that this error reached the caller rather than merely that some
// error did.
var errTransient = errors.New("transient read failure")

// flakyReaderAt fails its first ReadAt and delegates every one after it — the
// shape a retrying transport, a range reader over object storage, or a
// container still being written presents.
type flakyReaderAt struct {
	reader io.ReaderAt
	failed bool
}

func (f *flakyReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if !f.failed {
		f.failed = true
		return 0, errTransient
	}
	return f.reader.ReadAt(p, off)
}

// TestOpenReader_UnreadableHeader_DoesNotOpenUnbounded is the test for the
// way a bound like this fails in practice: not by rejecting something it
// should have accepted, but by quietly not applying.
//
// Deciding the bound from one read of the header and letting the container
// reader take its own leaves two independent reads that can disagree. When
// the first fails and the second succeeds — permitted for any io.ReaderAt,
// and ordinary for a retrying transport — the bound is skipped, the container
// reader sees the raw field, and the open reports success while allocating
// ~34 GB. Nothing in the return values says the mitigation was absent.
//
// Reading the header once and serving that copy removes the disagreement.
// A header that cannot be read is an error rather than an unbounded read.
func TestOpenReader_UnreadableHeader_DoesNotOpenUnbounded(t *testing.T) {
	_, referenceAlloc, err := openAllocation(t, bytes.NewReader(containerFixture(t)))
	if err != nil {
		t.Fatalf("OpenReader on the unpatched container: %v", err)
	}

	flaky := &flakyReaderAt{reader: bytes.NewReader(maxDirectorySectorCount(t))}
	wb, alloc, err := openAllocation(t, flaky)
	if alloc > referenceAlloc+allocationSlack {
		t.Errorf("open allocated %d bytes against %d for the same container read cleanly: a failed header read dropped the bound", alloc, referenceAlloc)
	}
	if !errors.Is(err, errTransient) {
		t.Errorf("OpenReader error = %v, want the transport's own %v — a read failure must reach the caller, not be relabelled", err, errTransient)
	}
	if wb != nil {
		t.Errorf("OpenReader returned a workbook alongside its error")
	}
}

// eofReaderAt returns io.EOF alongside a complete read of the header, which
// io.ReaderAt explicitly permits ("ReadAt may return either err == EOF or
// err == nil" when it reads exactly len(p) bytes at end of input).
type eofReaderAt struct{ reader io.ReaderAt }

func (e *eofReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := e.reader.ReadAt(p, off)
	if off == 0 && n == len(p) {
		return n, io.EOF
	}
	return n, err
}

// TestOpenReader_HeaderReadReportingEOF_IsStillBounded covers the second way
// two reads of the same header disagree: a legal (n == len(p), io.EOF) for
// the header and a clean read for everything else. The byte count decides, so
// the container reads normally and the bound still applies.
func TestOpenReader_HeaderReadReportingEOF_IsStillBounded(t *testing.T) {
	reference, referenceAlloc, err := openAllocation(t, bytes.NewReader(containerFixture(t)))
	if err != nil {
		t.Fatalf("OpenReader on the unpatched container: %v", err)
	}

	wb, alloc, err := openAllocation(t, &eofReaderAt{reader: bytes.NewReader(maxDirectorySectorCount(t))})
	if err != nil {
		t.Fatalf("OpenReader on a container whose header read reports io.EOF: %v", err)
	}
	if alloc > referenceAlloc+allocationSlack {
		t.Errorf("open allocated %d bytes against %d for the same container read cleanly: io.EOF alongside a complete read dropped the bound", alloc, referenceAlloc)
	}
	requireSameShape(t, wb, reference)
}

// TestHeaderServingReader_ServesEveryHeaderReadUntilTheReaderMovesOn pins
// which reads get the substituted bytes.
//
// Every read of the header does, however many there are and whatever their
// length: io.ReaderAt places no constraint on either, and documents parallel
// use as supported, so answering only the first would hand the bounded copy to
// one caller and the raw field to all the others. A zero-length read must not
// consume the answer either.
//
// Reads stop being substituted once a sector has been read. A sector's file
// offset is computed in unsigned 32-bit arithmetic by the reader underneath
// and wraps, so a malformed sector id produces a read of the header's own
// bytes — by then the reader is past its header, and that read is a sector
// read which must see the container.
func TestHeaderServingReader_ServesEveryHeaderReadUntilTheReaderMovesOn(t *testing.T) {
	raw := maxDirectorySectorCount(t)
	bounded, err := boundedContainerReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("boundedContainerReader: %v", err)
	}

	readHeader := func() uint32 {
		t.Helper()
		buf := make([]byte, cfbHeaderLen)
		if _, err := bounded.ReadAt(buf, 0); err != nil {
			t.Fatalf("read from offset 0: %v", err)
		}
		return binary.LittleEndian.Uint32(buf[cfbNumDirectorySectorsOffset : cfbNumDirectorySectorsOffset+4])
	}

	if _, err := bounded.ReadAt(nil, 0); err != nil {
		t.Fatalf("zero-length read from offset 0: %v", err)
	}

	// A read short enough to stop before the field, and one that reaches it:
	// both are header reads and both must come from the copy. Asserting only
	// that they do not error would pass on a reader that delegated them raw.
	if _, err := bounded.ReadAt(make([]byte, 8), 0); err != nil {
		t.Fatalf("short read from offset 0: %v", err)
	}
	shortOverField := make([]byte, cfbNumDirectorySectorsOffset+4)
	if _, err := bounded.ReadAt(shortOverField, 0); err != nil {
		t.Fatalf("short read covering the field: %v", err)
	}
	if got := binary.LittleEndian.Uint32(shortOverField[cfbNumDirectorySectorsOffset:]); got != 0 {
		t.Errorf("a %d-byte read from offset 0 presented %d, want the bounded 0", len(shortOverField), got)
	}

	// A read starting inside the header rather than at its start.
	midField := make([]byte, 4)
	if _, err := bounded.ReadAt(midField, cfbNumDirectorySectorsOffset); err != nil {
		t.Fatalf("read of the field alone: %v", err)
	}
	if got := binary.LittleEndian.Uint32(midField); got != 0 {
		t.Errorf("a read of the field alone presented %d, want the bounded 0", got)
	}

	// A read running past the end of the header: the container supplies the
	// tail, the copy still supplies the part inside the header.
	straddle := make([]byte, cfbHeaderLen+16)
	if _, err := bounded.ReadAt(straddle, 0); err != nil {
		t.Fatalf("read running past the header: %v", err)
	}
	if got := binary.LittleEndian.Uint32(straddle[cfbNumDirectorySectorsOffset:]); got != 0 {
		t.Errorf("a read running past the header presented %d for the field, want the bounded 0", got)
	}
	if !bytes.Equal(straddle[cfbHeaderLen:], raw[cfbHeaderLen:cfbHeaderLen+16]) {
		t.Errorf("a read running past the header did not return the container's own bytes beyond it")
	}
	for i := 0; i < 3; i++ {
		if got := readHeader(); got != 0 {
			t.Errorf("header read %d presented %d, want the bounded 0", i, got)
		}
	}

	// A sector read: from here the reader is past its header.
	if _, err := bounded.ReadAt(make([]byte, cfbHeaderLen), cfbHeaderLen); err != nil {
		t.Fatalf("sector read: %v", err)
	}
	if got := readHeader(); got != ^uint32(0) {
		t.Errorf("read from offset 0 after a sector read presented %d, want the container's own %d", got, ^uint32(0))
	}
}

// TestHeaderServingReader_ConcurrentHeaderReadsAllSeeTheBound covers the same
// idempotence under the parallel use io.ReaderAt documents. A race detector
// cannot see this failing: handing one caller the bounded copy and the rest
// the raw field is not a data race, just the wrong answer.
func TestHeaderServingReader_ConcurrentHeaderReadsAllSeeTheBound(t *testing.T) {
	const readers = 32

	bounded, err := boundedContainerReader(bytes.NewReader(maxDirectorySectorCount(t)))
	if err != nil {
		t.Fatalf("boundedContainerReader: %v", err)
	}

	got := make([]uint32, readers)
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(readers)
	for i := range got {
		go func() {
			defer done.Done()
			start.Wait()
			buf := make([]byte, cfbHeaderLen)
			if _, err := bounded.ReadAt(buf, 0); err != nil {
				return
			}
			got[i] = binary.LittleEndian.Uint32(buf[cfbNumDirectorySectorsOffset : cfbNumDirectorySectorsOffset+4])
		}()
	}
	start.Done()
	done.Wait()

	unbounded := 0
	for _, v := range got {
		if v != 0 {
			unbounded++
		}
	}
	if unbounded != 0 {
		t.Errorf("%d of %d concurrent header reads saw the container's raw field instead of the bound", unbounded, readers)
	}
}

// synthHeader is a bare compound-file header — enough for
// boundedContainerReader, which reads nothing else — inside a container of
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

// TestBoundedContainerReader_ServesABoundedField pins what each version of
// the format is given. Version 3 must declare zero and the field means
// nothing there, so zero is served however it was written. Version 4 does
// count directory sectors, so a count the container could hold is served
// untouched and a larger one is cut to the sector count of the container.
func TestBoundedContainerReader_ServesABoundedField(t *testing.T) {
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
			bounded, err := boundedContainerReader(bytes.NewReader(b))
			if err != nil {
				t.Fatalf("boundedContainerReader: %v", err)
			}
			buf := make([]byte, cfbHeaderLen)
			if _, err := bounded.ReadAt(buf, 0); err != nil {
				t.Fatalf("read the served header: %v", err)
			}
			got := binary.LittleEndian.Uint32(buf[cfbNumDirectorySectorsOffset : cfbNumDirectorySectorsOffset+4])
			if got != tc.want {
				t.Errorf("served directory-sector count = %d, want %d", got, tc.want)
			}
			if binary.LittleEndian.Uint32(b[cfbNumDirectorySectorsOffset:cfbNumDirectorySectorsOffset+4]) != tc.declared {
				t.Errorf("the underlying container was rewritten; it must be left alone")
			}
		})
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

	patched := bytes.Clone(containerFixture(t))
	sectorSize := int64(1) << binary.LittleEndian.Uint16(patched[cfbSectorShiftOffset:cfbSectorShiftOffset+2])
	directorySector := int64(binary.LittleEndian.Uint32(patched[directorySectorLocOffset : directorySectorLocOffset+4]))
	rootChild := (directorySector+1)*sectorSize + rootChildIDOffset
	if rootChild+4 > int64(len(patched)) {
		t.Fatalf("container fixture has no directory sector at index %d", directorySector)
	}
	binary.LittleEndian.PutUint32(patched[rootChild:rootChild+4], noStream)

	wb, err := OpenReader(bytes.NewReader(patched))
	if !errors.Is(err, ErrNoWorkbookStream) {
		t.Fatalf("OpenReader error = %v, want %v", err, ErrNoWorkbookStream)
	}
	if wb != nil {
		t.Errorf("OpenReader returned a workbook alongside its error")
	}
}

// TestOpenReader_HeaderDeclaringNoDirectory_ReturnsError covers a header whose
// directory stream begins at the end-of-chain sentinel — a four-byte edit to
// an otherwise valid container.
//
// The container reader builds its directory entries by walking that chain and
// then indexes the first entry unconditionally, so an empty walk leaves it
// indexing an empty slice: the read does not fail, the process does. A
// consumer cannot defend against that from the outside, and this module is
// what sits between arbitrary bytes and a reader it does not own.
func TestOpenReader_HeaderDeclaringNoDirectory_ReturnsError(t *testing.T) {
	const directorySectorLocOffset = 48

	patched := bytes.Clone(containerFixture(t))
	binary.LittleEndian.PutUint32(patched[directorySectorLocOffset:directorySectorLocOffset+4], cfbEndOfChain)

	wb, err := OpenReader(bytes.NewReader(patched))
	if !errors.Is(err, ErrNoDirectory) {
		t.Fatalf("OpenReader error = %v, want %v", err, ErrNoDirectory)
	}
	if wb != nil {
		t.Errorf("OpenReader returned a workbook alongside its error")
	}
}

// TestOpenReader_ShortInput_ReportsAPermanentError covers input too small to
// hold a header. The distinction that matters is not that it fails but how: a
// caller retrying on io.EOF, which is the idiom for a truncated read from a
// transport, must not retry a file that will never be long enough.
func TestOpenReader_ShortInput_ReportsAPermanentError(t *testing.T) {
	wb, err := OpenReader(bytes.NewReader([]byte("not a container")))
	if !errors.Is(err, ErrShortHeader) {
		t.Fatalf("OpenReader error = %v, want %v", err, ErrShortHeader)
	}
	if errors.Is(err, io.EOF) {
		t.Errorf("OpenReader error unwraps to io.EOF, which invites a caller to retry input that can never succeed")
	}
	if wb != nil {
		t.Errorf("OpenReader returned a workbook alongside its error")
	}
}

// failingIterator yields entries until it has none left to give and then
// fails, standing in for a container reader that cannot produce an entry it
// believes exists.
type failingIterator struct{ err error }

func (f *failingIterator) Next() (*mscfb.File, error) { return nil, f.err }

// TestWorkbookStream_IterationError_IsNotReportedAsAMissingStream pins the
// difference between the two ways the directory walk can end. io.EOF means the
// container held no workbook stream; anything else means the reader failed,
// and collapsing the two would report a read failure as a verdict about the
// container's contents.
func TestWorkbookStream_IterationError_IsNotReportedAsAMissingStream(t *testing.T) {
	want := errors.New("directory entry read failed")

	stream, err := workbookStream(&failingIterator{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("workbookStream error = %v, want %v", err, want)
	}
	if errors.Is(err, ErrNoWorkbookStream) {
		t.Errorf("a failed iteration was reported as a missing workbook stream")
	}
	if stream != nil {
		t.Errorf("workbookStream returned a stream alongside its error")
	}
}

// recordingReaderAt records the offset and length of every read made through
// it.
type recordingReaderAt struct {
	reader io.ReaderAt
	mu     sync.Mutex
	reads  [][2]int64
}

func (r *recordingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.mu.Lock()
	r.reads = append(r.reads, [2]int64{off, int64(len(p))})
	r.mu.Unlock()
	return r.reader.ReadAt(p, off)
}

// TestContainerReaderReadsTheWholeHeaderFirst pins the access pattern the
// bound depends on.
//
// boundedContainerReader answers reads of the header and delegates the rest,
// which only bounds anything while the container reader actually asks for its
// header in one read from offset 0. Nothing in go.mod can express that, and
// the effective version of the container reader is the maximum across a
// five-module graph — another consumer of it already pins a different one — so
// any dependency update anywhere can float it. If the access pattern ever
// shifts, the bound stops applying while every open still reports success,
// which is a failure with no symptom. This test is the alarm for it.
func TestContainerReaderReadsTheWholeHeaderFirst(t *testing.T) {
	recorder := &recordingReaderAt{reader: bytes.NewReader(containerFixture(t))}
	if _, err := mscfb.New(recorder); err != nil {
		t.Fatalf("open the container fixture: %v", err)
	}

	if len(recorder.reads) == 0 {
		t.Fatal("the container reader made no reads")
	}
	if off, length := recorder.reads[0][0], recorder.reads[0][1]; off != 0 || length != cfbHeaderLen {
		t.Errorf("first read was %d bytes at offset %d, want %d bytes at offset 0: the header is no longer read in one piece from the start, so serving it no longer bounds anything", length, off, cfbHeaderLen)
	}
	for i, read := range recorder.reads[1:] {
		if read[0] < cfbHeaderLen {
			t.Errorf("read %d was %d bytes at offset %d, inside the header: only the first read may be", i+1, read[1], read[0])
		}
	}
}

// panicAfterHeaderReaderAt delegates the header and then panics, standing in
// for a container reader that indexes something it did not check.
type panicAfterHeaderReaderAt struct {
	reader io.ReaderAt
	reads  int
}

func (p *panicAfterHeaderReaderAt) ReadAt(b []byte, off int64) (int, error) {
	p.reads++
	if p.reads > 1 {
		panic("read past the header")
	}
	return p.reader.ReadAt(b, off)
}

// TestOpenReader_PanicWhileOpeningTheContainer_BecomesAnError pins the
// conversion of a panic into a read failure.
//
// Two ways this can rot, and both must fail here. Removing the recover takes
// the calling process down, which is what the recover exists to stop.
// Recovering but returning a nil error is worse than either: the caller gets
// no container and no error, and the next call made on the nil container
// panics somewhere no recover covers at all.
func TestOpenReader_PanicWhileOpeningTheContainer_BecomesAnError(t *testing.T) {
	reader := &panicAfterHeaderReaderAt{reader: bytes.NewReader(containerFixture(t))}

	wb, err := OpenReader(reader)
	if err == nil {
		t.Fatalf("OpenReader returned no error for a container reader that panicked")
	}
	if wb != nil {
		t.Errorf("OpenReader returned a workbook alongside its error")
	}
}

// TestOpenContainer_Panic_ReturnsNilAndAnError pins the same two properties on
// the returned pair directly, so a recover that produced a nil container with
// a nil error cannot pass by being caught later.
func TestOpenContainer_Panic_ReturnsNilAndAnError(t *testing.T) {
	doc, err := openContainer(&panicAfterHeaderReaderAt{reader: bytes.NewReader(containerFixture(t))})
	if err == nil {
		t.Fatalf("openContainer returned a nil error after a panic")
	}
	if doc != nil {
		t.Errorf("openContainer returned a container alongside its error")
	}
}

// TestOpenReader_BIFF5StreamName_IsRead covers the "Book" stream name.
//
// The corpus this change was measured against is BIFF8 throughout, so nothing
// else here exercises the other arm — and an untested arm in a published
// reader is a claim of support rather than support. The fixture is the BIFF8
// one with its stream renamed: the container and the record stream are
// identical, so what is under test is the name match alone.
func TestOpenReader_BIFF5StreamName_IsRead(t *testing.T) {
	const (
		directorySectorLocOffset = 48
		dirEntrySize             = 128
		nameLengthOffset         = 64
		workbookEntry            = 1
	)

	patched := bytes.Clone(containerFixture(t))
	sectorSize := int64(1) << binary.LittleEndian.Uint16(patched[cfbSectorShiftOffset:cfbSectorShiftOffset+2])
	directorySector := int64(binary.LittleEndian.Uint32(patched[directorySectorLocOffset : directorySectorLocOffset+4]))
	entry := (directorySector+1)*sectorSize + workbookEntry*dirEntrySize
	if entry+dirEntrySize > int64(len(patched)) {
		t.Fatalf("container fixture has no directory entry at index %d", workbookEntry)
	}

	name := "Book"
	for i := range make([]byte, nameLengthOffset) {
		patched[entry+int64(i)] = 0
	}
	for i, r := range name {
		binary.LittleEndian.PutUint16(patched[entry+int64(i)*2:], uint16(r))
	}
	binary.LittleEndian.PutUint16(patched[entry+nameLengthOffset:], uint16((len(name)+1)*2))

	wb, err := OpenReader(bytes.NewReader(patched))
	if err != nil {
		t.Fatalf("OpenReader on a container whose stream is named %q: %v", name, err)
	}
	reference, err := OpenReader(bytes.NewReader(containerFixture(t)))
	if err != nil {
		t.Fatalf("OpenReader on the unrenamed container: %v", err)
	}
	requireSameShape(t, wb, reference)
}
