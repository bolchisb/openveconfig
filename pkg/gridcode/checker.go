package gridcode

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// The checker words. Reverse-engineered (#9, #27); the rules below are
// restated in this project's own words and none has been checked on a
// device yet.
//
// Each word is 16 bits. The high byte is a generation counter, the same in
// A and B when the grid block is complete. The low byte of A is the grid
// code G; the low byte of B is (G + V) mod 256, where V is the
// "parameter-set version" of grid code G (ParamSetVersion). The grid
// parameter words (129-190) do not enter the words at all.
//
// A program that sends grid data is believed to write A with a new
// generation first, then the parameter words, then B last, so that an
// interrupted send leaves A and B with different generations (INFERRED).
const (
	CheckerWordA uint16 = 128
	CheckerWordB uint16 = 191
)

// Pair is the value of the two checker words.
type Pair struct{ A, B uint16 }

func hi(w uint16) uint16 { return w >> 8 }
func lo(w uint16) uint16 { return w & 0xFF }

// Consistent reports whether p fits grid code g with parameter-set version
// v, in the form checked before a device's pair is replaced: always for
// grid code 0; otherwise A and B must have the same generation, A's low
// byte must be g and B's low byte (g + v) mod 256. Reverse-engineered.
func (p Pair) Consistent(g Code, v uint8) bool {
	if g == 0 {
		return true
	}
	return hi(p.A) == hi(p.B) && lo(p.A) == uint16(g) && lo(p.B) == uint16(uint8(g)+v)
}

// CheckShown checks p against grid code g and parameter-set version v in
// the form used when settings are shown; p is consistent when both parts
// hold. header: A and B have the same generation and A's low byte is g, or
// g is 0 and A is 0xFFFF (never set). version: B's low byte minus A's low
// byte is v, mod 256. Reverse-engineered.
func (p Pair) CheckShown(g Code, v uint8) (header, version bool) {
	header = g == 0 && p.A == 0xFFFF || hi(p.A) == hi(p.B) && lo(p.A) == uint16(g)
	version = uint8(lo(p.B)-lo(p.A)) == v
	return header, version
}

// Stamp returns the pair that replaces old when grid data for grid code g,
// with parameter-set version v, is written. The new generation is one
// above A's, wrapping from 0xFF to 0; if that is B's generation, it is one
// above B's instead, so the new A never shares a generation with the old
// B. A gets the grid code and B (g + v) mod 256 as low byte.
// Reverse-engineered. g must fit in a byte.
func Stamp(old Pair, g Code, v uint8) (Pair, error) {
	if g > 0xFF {
		return old, fmt.Errorf("gridcode: grid code %d does not fit in a checker word", g)
	}
	h := (old.A + 0x100) & 0xFF00
	if h == old.B&0xFF00 {
		h = (old.B + 0x100) & 0xFF00
	}
	return Pair{A: h | uint16(g), B: h | uint16(uint8(g)+v)}, nil
}

// Grid-info table header: a u16 (not used here), the format version, the
// number of grid codes N and the bitmap length, then N u16 offsets from
// the table start, one per grid code, to the code's entry.
const (
	tableOffFormat  = 2
	tableOffCount   = 3
	tableOffOffsets = 5
)

// ParamSetVersion returns the parameter-set version V of grid code g from
// a grid-info table (as rvsc.File.GridInfo returns it): the first byte of
// g's entry, present from table format 1. V is 0 when there is no table
// (nil), when g is outside the table or for format 0. A table too short
// for its own header or offsets is an error. Reverse-engineered; V may
// differ between firmware versions (INFERRED).
func ParamSetVersion(table []byte, g Code) (uint8, error) {
	if table == nil {
		return 0, nil
	}
	if len(table) < tableOffOffsets {
		return 0, fmt.Errorf("gridcode: grid-info table of %d bytes is too short", len(table))
	}
	if int(g) >= int(table[tableOffCount]) || table[tableOffFormat] < 1 {
		return 0, nil
	}
	at := tableOffOffsets + 2*int(g)
	if at+2 > len(table) {
		return 0, fmt.Errorf("gridcode: grid-info table of %d bytes has no offset for grid code %d", len(table), g)
	}
	off := int(binary.LittleEndian.Uint16(table[at:]))
	if off >= len(table) {
		return 0, fmt.Errorf("gridcode: grid-info entry of grid code %d at %d is outside the %d-byte table", g, off, len(table))
	}
	return table[off], nil
}

// ErrDeviceWrite is returned (wrapped) by CheckDeviceWrite.
var ErrDeviceWrite = errors.New("gridcode: the grid code and grid parameters cannot be sent straight to a device yet: " +
	"the device's checker words (settings 128 and 191) must be updated in a fixed order that has not been verified on hardware")

// CheckDeviceWrite checks the IDs of settings about to be written to a
// device. It returns an error wrapping ErrDeviceWrite, naming them, if they
// include the grid code (setting 81) or a grid word (128-191). Saving them
// to a settings file, with Stamp, is not affected.
func CheckDeviceWrite(ids []uint16) error {
	var grid []string
	for _, id := range ids {
		if id == 81 || id >= CheckerWordA && id <= CheckerWordB {
			grid = append(grid, fmt.Sprint(id))
		}
	}
	if len(grid) == 0 {
		return nil
	}
	return fmt.Errorf("%w (settings %s)", ErrDeviceWrite, strings.Join(grid, ", "))
}
