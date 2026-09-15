package xls

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sync/atomic"
)

// Compound-file header fields this package has to look at itself. The
// container reader underneath parses the whole header; these offsets are
// only what bounding one unvalidated field needs.
const (
	cfbHeaderLen = 512
	// cfbMajorVersionOffset holds the version that decides whether the
	// directory-sector count means anything: 3 or 4.
	cfbMajorVersionOffset = 26
	// cfbSectorShiftOffset holds the sector size as a power of two.
	cfbSectorShiftOffset = 30
	// cfbNumDirectorySectorsOffset holds the count of directory sectors.
	cfbNumDirectorySectorsOffset = 40
	// cfbDirectorySectorLocOffset holds the first sector of the directory
	// stream.
	cfbDirectorySectorLocOffset = 48

	cfbMajorVersion3 = 3
	// cfbSectorShift512 and cfbSectorShift4096 are the only two sector
	// shifts the format defines, and the only two the container reader
	// accepts; any other value is its error to report, not ours.
	cfbSectorShift512  = 9
	cfbSectorShift4096 = 12

	// cfbEndOfChain terminates a sector chain.
	cfbEndOfChain = 0xFFFFFFFE
)

// ErrShortHeader reports input too short to hold a compound file header. It is
// a permanent property of the input, not a transient read failure: a caller
// that retries on io.EOF must not retry this, so it deliberately does not wrap
// io.EOF.
var ErrShortHeader = errors.New("xls: input is shorter than a compound file header")

// ErrNoDirectory reports a header whose directory stream begins at the
// end-of-chain sentinel, so the container declares no directory at all.
//
// The container reader underneath builds its directory entries by walking that
// chain and then indexes the first entry unconditionally, so an empty walk
// leaves it indexing an empty slice. Rejecting here converts a panic into an
// error, and cannot reject anything that reads: a container with no directory
// has nowhere to keep a workbook stream.
var ErrNoDirectory = errors.New("xls: compound file header declares no directory stream")

// boundedContainerReader reads the container's header exactly once, bounds the
// directory-sector count in that copy, and returns a reader that hands the
// container reader the same copy rather than letting it read the header again.
//
// The field is a raw uint32 that nothing validates, and the container reader
// sizes its directory slice from it before reading a single directory sector
// — so a header declaring 0xFFFFFFFF reserves tens of gigabytes for a
// container of any size. Bounding it cannot change what a container decodes
// to: the count is used as a slice capacity and nothing else, with the slice
// grown from the sectors actually walked, so a header whose count is
// consistent with its own container is served back byte for byte and every
// other container decodes exactly as it would have.
//
// A major-version-3 container must declare zero here — the format defines no
// meaning for the field at that version — so zero is what is served. At
// version 4 the field does count directory sectors, so it is served as itself,
// clamped to the number of sectors in the container. When the container's size
// is not discoverable the clamp falls back to zero, which costs the reader a
// preallocation and nothing else.
//
// Serving one read rather than inspecting the header and stepping aside is
// what makes the bound hold. Deciding from one read and letting the container
// reader take another leaves two independent reads that can disagree — a
// retrying transport, a container still being written, or a reader returning
// io.EOF alongside a complete read, all of which the io.ReaderAt contract
// permits — and every disagreement drops the bound silently while reporting
// success. A header that cannot be read at all is an error here rather than an
// unbounded read there.
func boundedContainerReader(reader io.ReaderAt) (io.ReaderAt, error) {
	header := make([]byte, cfbHeaderLen)
	// io.ReaderAt may return io.EOF alongside a complete read, so the byte
	// count decides whether the read succeeded, not the error.
	if n, err := reader.ReadAt(header, 0); n < cfbHeaderLen {
		if err == nil || errors.Is(err, io.EOF) {
			return nil, ErrShortHeader
		}
		return nil, err
	}

	if binary.LittleEndian.Uint32(header[cfbDirectorySectorLocOffset:cfbDirectorySectorLocOffset+4]) == cfbEndOfChain {
		return nil, ErrNoDirectory
	}

	declared := binary.LittleEndian.Uint32(header[cfbNumDirectorySectorsOffset : cfbNumDirectorySectorsOffset+4])
	if declared != 0 {
		var bound uint32
		if binary.LittleEndian.Uint16(header[cfbMajorVersionOffset:cfbMajorVersionOffset+2]) > cfbMajorVersion3 {
			bound = containerSectors(reader, header)
		}
		if declared > bound {
			binary.LittleEndian.PutUint32(header[cfbNumDirectorySectorsOffset:cfbNumDirectorySectorsOffset+4], bound)
		}
	}
	return &headerServingReader{reader: reader, header: header}, nil
}

// containerSectors is the number of whole sectors the container holds, which
// is an upper bound on the number of any one kind of sector in it. It reports
// zero when either the sector size or the container size is unknown.
func containerSectors(reader io.ReaderAt, header []byte) uint32 {
	shift := binary.LittleEndian.Uint16(header[cfbSectorShiftOffset : cfbSectorShiftOffset+2])
	if shift != cfbSectorShift512 && shift != cfbSectorShift4096 {
		return 0
	}
	size, ok := readerAtSize(reader)
	if !ok || size < 0 {
		return 0
	}
	sectors := size >> shift
	if sectors > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(sectors)
}

// readerAtSize reports the size of reader when it can say so without moving a
// file offset: *os.File, and the Size() readers in the standard library
// (bytes.Reader, strings.Reader, io.SectionReader). io.Seeker is deliberately
// not consulted — an io.ReaderAt may be read concurrently, and seeking it to
// measure it is not safe.
func readerAtSize(reader io.ReaderAt) (int64, bool) {
	switch v := reader.(type) {
	case interface{ Size() int64 }:
		return v.Size(), true
	case interface{ Stat() (os.FileInfo, error) }:
		info, err := v.Stat()
		if err != nil {
			return 0, false
		}
		return info.Size(), true
	}
	return 0, false
}

// headerServingReader answers reads of the header with the copy already read
// and bounded, and delegates everything else untouched.
//
// The substitution is confined to the header-reading phase rather than applied
// to every read that overlaps the field. A sector's file offset is computed in
// unsigned 32-bit arithmetic by the reader underneath and can wrap back to
// offset 0, so a malformed sector id produces a read of the header's own
// bytes; once any sector has been read, the reader is past its header and gets
// the container verbatim.
//
// Within that phase every read that touches the header is answered from the
// copy, at any offset inside it and any length, and the answer is idempotent
// rather than one-shot. io.ReaderAt documents parallel use as supported and
// places no constraint on how many reads a client makes, where they start or
// how long they are, so a one-shot answer hands the bounded copy to one caller
// and the raw field to every other — failing in the direction that drops the
// bound. Serving them all costs nothing and fails the other way.
//
// It deliberately does not implement the container reader's zero-copy slicing
// interface: a reader that returned its own backing array could not be served
// a bounded header at all.
type headerServingReader struct {
	reader     io.ReaderAt
	header     []byte
	pastHeader atomic.Bool
}

func (h *headerServingReader) ReadAt(p []byte, off int64) (int, error) {
	headerLen := int64(len(h.header))
	if off >= headerLen {
		h.pastHeader.Store(true)
		return h.reader.ReadAt(p, off)
	}
	if off < 0 || h.pastHeader.Load() {
		return h.reader.ReadAt(p, off)
	}
	if off+int64(len(p)) <= headerLen {
		return copy(p, h.header[off:]), nil
	}
	// A read running past the header: the container supplies the tail, the
	// served copy still supplies the part inside the header.
	n, err := h.reader.ReadAt(p, off)
	copy(p[:min(int64(n), headerLen-off)], h.header[off:])
	return n, err
}
