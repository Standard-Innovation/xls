package xls

import (
	"encoding/binary"
	"io"
	"math"
	"os"
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

	cfbMajorVersion3 = 3
	// cfbSectorShift512 and cfbSectorShift4096 are the only two sector
	// shifts the format defines, and the only two the container reader
	// accepts; any other value is its error to report, not ours.
	cfbSectorShift512  = 9
	cfbSectorShift4096 = 12
)

// boundDirectorySectorCount presents reader's compound-file header with its
// directory-sector count clamped to what the container could actually hold,
// returning reader unchanged whenever the declared count already is.
//
// The field is a raw uint32 that nothing validates, and the container reader
// sizes its directory slice from it before reading a single directory sector
// — so a header declaring 0xFFFFFFFF reserves tens of gigabytes for a file of
// any size. Clamping cannot change what a file decodes to: the count is used
// as a capacity hint and nothing else, with the slice grown from the sectors
// actually walked, so a file whose count is consistent with its own size is
// handed through byte for byte and every other file decodes exactly as it
// would have.
//
// A major-version-3 container must declare zero here — the format defines no
// meaning for the field at that version — so it is presented as zero. At
// version 4 the field does count directory sectors, so it is presented as
// itself, clamped to the number of sectors in the container. When the
// container's size is not discoverable the clamp falls back to zero, which
// costs the reader a preallocation and nothing else.
func boundDirectorySectorCount(reader io.ReaderAt) io.ReaderAt {
	var header [cfbHeaderLen]byte
	if _, err := reader.ReadAt(header[:], 0); err != nil {
		return reader
	}

	declared := binary.LittleEndian.Uint32(header[cfbNumDirectorySectorsOffset : cfbNumDirectorySectorsOffset+4])
	if declared == 0 {
		return reader
	}

	var bound uint32
	if binary.LittleEndian.Uint16(header[cfbMajorVersionOffset:cfbMajorVersionOffset+2]) > cfbMajorVersion3 {
		bound = containerSectors(reader, &header)
	}
	if declared <= bound {
		return reader
	}
	return &maskedHeaderReader{reader: reader, value: bound}
}

// containerSectors is the number of whole sectors the container holds, which
// is an upper bound on the number of any one kind of sector in it. It reports
// zero when either the sector size or the container size is unknown.
func containerSectors(reader io.ReaderAt, header *[cfbHeaderLen]byte) uint32 {
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

// maskedHeaderReader delegates to reader, substituting value for the
// directory-sector count in whatever bytes it hands back. It deliberately
// does not implement the container reader's zero-copy slicing interface: a
// reader that returned its own backing array here could not have the field
// rewritten, and the copy only applies to headers already found malformed.
type maskedHeaderReader struct {
	reader io.ReaderAt
	value  uint32
}

func (m *maskedHeaderReader) ReadAt(p []byte, off int64) (int, error) {
	n, err := m.reader.ReadAt(p, off)

	const lo, hi = int64(cfbNumDirectorySectorsOffset), int64(cfbNumDirectorySectorsOffset) + 4
	if off < hi && off+int64(n) > lo {
		var field [4]byte
		binary.LittleEndian.PutUint32(field[:], m.value)
		for i := max(lo, off); i < min(hi, off+int64(n)); i++ {
			p[i-off] = field[i-lo]
		}
	}
	return n, err
}
