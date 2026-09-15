package xls

import (
	"io"
	"os"

	"github.com/richardlehane/mscfb"
)

//Open one xls file
func Open(file string, charset string) (*WorkBook, error) {
	if fi, err := os.Open(file); err == nil {
		return OpenReader(fi, charset)
	} else {
		return nil, err
	}
}

//Open xls file from reader
func OpenReader(reader io.ReaderAt, charset string) (wb *WorkBook, err error) {
	var doc *mscfb.Reader
	if doc, err = mscfb.New(reader); err != nil {
		return nil, err
	}
	for entry, ferr := doc.Next(); ferr == nil; entry, ferr = doc.Next() {
		if entry.Name == "Workbook" || entry.Name == "Book" {
			return newWorkBookFromOle2(entry), nil
		}
	}
	return
}
