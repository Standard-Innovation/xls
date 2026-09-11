#!/usr/bin/env python3
"""Generate testdata/numberformat.xls, the fixture for the number-format tests.

    pip install xlwt==1.3.0 && python3 gen_numberformat.py

Every value here is invented, and deliberately sits in an obviously-synthetic
9999 placeholder space, so that no fixture value can be mistaken for one
captured from a real file.

The cases matter because BIFF8 stores a number in one of several encodings and
the reader's behaviour differs across all of them:

  * RK "integer"     -- a signed 30-bit int in the RK word's high bits
  * RK "integer/100" -- the same, with the fX100 flag set. Excel and xlwt both
                        prefer this encoding for any value with at most two
                        decimals that fits 30 bits, which covers most
                        two-decimal values.
  * RK "float"       -- the high 30 bits of an IEEE-754 double, low 34 zero
  * NUMBER           -- a full 8-byte double, used when no RK form fits

and in one of two format classes: a built-in format index (< 164, carrying no
FORMAT record) or a user-defined one (>= 164, whose FORMAT record holds the
format string).

xlwt never emits the RK float variant -- it uses an RK integer form where one
fits and a NUMBER record otherwise -- so that encoding is covered by the
package's own unit tests, which build RK words directly, rather than here.
"""

import datetime

import xlwt

NUMERIC = r'#,##0.0000_);\(#,##0.0000\)'  # user-defined: >= 164
DATE = 'yyyy-mm-dd'                     # user-defined: >= 164
PERCENT = '0.00%'                       # built-in index 10, carries no FORMAT record

# (row label, value, number format, intended encoding)
CASES = [
    ('rk_int_userfmt', 9999011, NUMERIC, 'RK integer'),
    ('rk_int_x100_userfmt', 9999.25, NUMERIC, 'RK integer/100'),
    ('rk_int_x100_negative_userfmt', -9999.75, NUMERIC, 'RK integer/100, negative'),
    ('number_userfmt', 0.9999000000099991, NUMERIC, 'NUMBER: too many decimals for any RK form'),
    ('rk_int_x100_builtin_percent', 0.99, PERCENT, 'RK integer/100, built-in format'),
    ('rk_int_x100_negative_builtin_percent', -0.99, PERCENT, 'RK integer/100, negative, built-in format'),
    ('rk_int_date', datetime.date(2024, 1, 15), DATE, 'RK integer holding a date serial'),
]


def main():
    book = xlwt.Workbook()
    sheet = book.add_sheet('Sheet1')
    styles = {}
    for row, (label, value, fmt, _) in enumerate(CASES):
        style = styles.get(fmt)
        if style is None:
            style = xlwt.XFStyle()
            style.num_format_str = fmt
            styles[fmt] = style
        sheet.write(row, 0, label)
        sheet.write(row, 1, value, style)
    book.save('numberformat.xls')


if __name__ == '__main__':
    main()
