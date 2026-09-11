package xls

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

//content type
type contentHandler interface {
	String(*WorkBook) []string
	FirstCol() uint16
	LastCol() uint16
}

type Col struct {
	RowB      uint16
	FirstColB uint16
}

type Coler interface {
	Row() uint16
}

func (c *Col) Row() uint16 {
	return c.RowB
}

func (c *Col) FirstCol() uint16 {
	return c.FirstColB
}

func (c *Col) LastCol() uint16 {
	return c.FirstColB
}

func (c *Col) String(wb *WorkBook) []string {
	return []string{"default"}
}

type XfRk struct {
	Index uint16
	Rk    RK
}

func (xf *XfRk) String(wb *WorkBook) string {
	idx := int(xf.Index)
	if len(wb.Xfs) > idx {
		fNo := wb.Xfs[idx].formatNo()
		if fNo >= 164 { // user defined format
			if fmt := wb.Formats[fNo]; fmt != nil {
				if !isDateFormat(fmt.str) {
					return xf.Rk.String()
				}
				i, f, isFloat := xf.Rk.number()
				if !isFloat {
					f = float64(i)
				}
				t := timeFromExcelTime(f, wb.dateMode == 1)

				// RFC3339 regardless of the format string, deliberately:
				// consumers parse .xls dates in this one shape. Upstream's
				// master branch renders this path through the format string
				// instead (goyymmdd), and classifies formats by substring --
				// which reads plain "0.0" as a date. Do not sync either from
				// upstream without a consumer migration.
				return t.Format(time.RFC3339)
			}
			// see http://www.openoffice.org/sc/excelfileformat.pdf
		} else if 14 <= fNo && fNo <= 17 || fNo == 22 || 27 <= fNo && fNo <= 36 || 50 <= fNo && fNo <= 58 { // jp. date format
			i, f, isFloat := xf.Rk.number()
			if !isFloat {
				f = float64(i)
			}
			t := timeFromExcelTime(f, wb.dateMode == 1)
			return t.Format("2006.01") //TODO it should be international
		}
	}
	return xf.Rk.String()
}

type RK uint32

func (rk RK) number() (intNum int64, floatNum float64, isFloat bool) {
	multiplied := rk&1 != 0
	if rk&2 == 0 {
		// Float variant: the payload is the high 30 bits of an IEEE-754
		// double whose low 34 bits are zero.
		floatNum = math.Float64frombits(uint64(rk>>2) << 34)
		if multiplied {
			floatNum = floatNum / 100
		}
		return 0, floatNum, true
	}
	// Integer variant: the payload is a two's-complement signed 30-bit
	// integer, so it needs an arithmetic shift. An unsigned shift decodes
	// every negative value as a large positive one near 2^30.
	val := int32(rk) >> 2
	if multiplied {
		// fX100 applies to the integer variant too, and Excel prefers that
		// encoding for any value with at most two decimals that fits 30
		// bits -- most money. The quotient is not integral, so it is
		// returned through floatNum.
		return 0, float64(val) / 100, true
	}
	return int64(val), 0, false
}

func (rk RK) String() string {
	i, f, isFloat := rk.number()
	if isFloat {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatInt(i, 10)
}

var ErrIsInt = fmt.Errorf("is int")

func (rk RK) Float() (float64, error) {
	_, f, isFloat := rk.number()
	if !isFloat {
		return 0, ErrIsInt
	}
	return f, nil
}

type MulrkCol struct {
	Col
	Xfrks    []XfRk
	LastColB uint16
}

func (c *MulrkCol) LastCol() uint16 {
	return c.LastColB
}

func (c *MulrkCol) String(wb *WorkBook) []string {
	var res = make([]string, len(c.Xfrks))
	for i := 0; i < len(c.Xfrks); i++ {
		xfrk := c.Xfrks[i]
		res[i] = xfrk.String(wb)
	}
	return res
}

type MulBlankCol struct {
	Col
	Xfs      []uint16
	LastColB uint16
}

func (c *MulBlankCol) LastCol() uint16 {
	return c.LastColB
}

func (c *MulBlankCol) String(wb *WorkBook) []string {
	return make([]string, len(c.Xfs))
}

type NumberCol struct {
	Col
	Index uint16
	Float float64
}

func (c *NumberCol) String(wb *WorkBook) []string {
	return []string{strconv.FormatFloat(c.Float, 'f', -1, 64)}
}

type FormulaCol struct {
	Header struct {
		Col
		IndexXf uint16
		Result  [8]byte
		Flags   uint16
		_       uint32
	}
	Bts []byte
}

func (c *FormulaCol) String(wb *WorkBook) []string {
	return []string{"FormulaCol"}
}

type RkCol struct {
	Col
	Xfrk XfRk
}

func (c *RkCol) String(wb *WorkBook) []string {
	return []string{c.Xfrk.String(wb)}
}

type LabelsstCol struct {
	Col
	Xf  uint16
	Sst uint32
}

func (c *LabelsstCol) String(wb *WorkBook) []string {
	return []string{wb.sst[int(c.Sst)]}
}

type labelCol struct {
	BlankCol
	Str string
}

func (c *labelCol) String(wb *WorkBook) []string {
	return []string{c.Str}
}

type BlankCol struct {
	Col
	Xf uint16
}

func (c *BlankCol) String(wb *WorkBook) []string {
	return []string{""}
}
