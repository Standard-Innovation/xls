package xls

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/richardlehane/mscfb"
)

// ErrNoWorkbookStream reports a compound file that holds no stream a workbook
// can be read from: no directory entry reachable from the root is named
// "Workbook" (BIFF8) or "Book" (BIFF5). A container whose directory entries
// exist but are not linked into its directory tree reaches this.
var ErrNoWorkbookStream = errors.New("xls: compound file holds no workbook stream")

// Open one xls file
func Open(file string, charset string) (*WorkBook, error) {
	if fi, err := os.Open(file); err == nil {
		return OpenReader(fi, charset)
	} else {
		return nil, err
	}
}

// Open xls file from reader
func OpenReader(reader io.ReaderAt, charset string) (*WorkBook, error) {
	bounded, err := boundedContainerReader(reader)
	if err != nil {
		return nil, err
	}
	doc, err := openContainer(bounded)
	if err != nil {
		return nil, err
	}
	stream, err := workbookStream(doc)
	if err != nil {
		return nil, err
	}
	return newWorkBookFromStream(stream), nil
}

// openContainer opens the compound file, reporting a panic from the container
// reader as an error.
//
// This package's input is untrusted bytes and its container reader is a
// dependency, not something this module owns or patches. A header field it
// indexes without checking — a directory stream declared empty, say — takes
// down the calling process rather than failing the read, which no consumer can
// defend against from the outside. Known cases are rejected before they get
// here, in boundedContainerReader; this catches the ones not yet found.
func openContainer(reader io.ReaderAt) (doc *mscfb.Reader, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("xls: malformed compound file: %v", r)
		}
	}()
	return mscfb.New(reader)
}

// entryIterator is the container reader's directory walk, named so the walk
// below can be exercised against an iterator that fails. *mscfb.Reader
// satisfies it.
type entryIterator interface {
	Next() (*mscfb.File, error)
}

// workbookStream returns the first stream a workbook can be read from.
//
// Only io.EOF ends the walk. Any other error is the container reader failing
// to produce an entry it believes exists, which is not the same thing as the
// container holding no workbook stream, and reporting one as the other would
// turn a read failure into a missing-stream verdict.
func workbookStream(entries entryIterator) (io.ReadSeeker, error) {
	for {
		entry, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil, ErrNoWorkbookStream
		}
		if err != nil {
			return nil, err
		}
		if entry.Name == "Workbook" || entry.Name == "Book" {
			return entry, nil
		}
	}
}
