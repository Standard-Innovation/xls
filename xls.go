package xls

import (
	"errors"
	"io"
	"os"

	"github.com/richardlehane/mscfb"
)

// ErrNoWorkbookStream reports a compound file that holds no stream a workbook
// can be read from: no directory entry reachable from the root is named
// "Workbook" (BIFF8) or "Book" (BIFF5). A container whose directory entries
// exist but are not linked into its directory tree reaches this.
var ErrNoWorkbookStream = errors.New("xls: compound file holds no workbook stream")

//Open one xls file
func Open(file string, charset string) (*WorkBook, error) {
	if fi, err := os.Open(file); err == nil {
		return OpenReader(fi, charset)
	} else {
		return nil, err
	}
}

//Open xls file from reader
func OpenReader(reader io.ReaderAt, charset string) (*WorkBook, error) {
	doc, err := mscfb.New(boundDirectorySectorCount(reader))
	if err != nil {
		return nil, err
	}
	for entry, ferr := doc.Next(); ferr == nil; entry, ferr = doc.Next() {
		if entry.Name == "Workbook" || entry.Name == "Book" {
			return newWorkBookFromStream(entry), nil
		}
	}
	return nil, ErrNoWorkbookStream
}
