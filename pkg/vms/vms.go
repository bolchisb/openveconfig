// Package vms reads and writes the .vms files in which the VE.Bus System
// Configurator saves a system layout.
//
// Victron has published no specification of this format. Its layout is
// reverse-engineered (#56) and restated here in our own words; the choices
// marked "ours" are this project's, not facts about the original.
//
// Layout (integers little-endian; a short string is a u8 length and that many
// bytes):
//
//	file    := header | unit* | trailer
//	header  := u8 setup type | u8 L2 option | u32 AC inputs | u32 group mask |
//	           u32 selected cell
//	unit    := record | [record of the second-input pseudo unit] |
//	           u32 cell | [u32 cell of the second input]
//	record  := u32 long ID | u32 version word | short string system record (9) |
//	           u8 response char | u8 letter index | u8 config-protocol byte |
//	           u8 external BF | u32 icon top | u32 icon left | short string name |
//	           u32 icon snap step | u32 icon zoom percent | u8 display state |
//	           u8 has second input
//	trailer := u32 version | "VE.Bus configuration file" | u8 checksum
//
// The bracketed parts are present when the unit's "has second input" byte is
// 1. The checksum byte makes the sum of all bytes of the file 0 modulo 256.
// Units follow one another until the trailer, which is the last 30 bytes.
//
// Version 4 is current. Files of versions 2 and 3 are read: version 3 has no
// external-BF byte (it is derived from the version word), version 2 also has
// no letter index and config-protocol byte. Older files are refused (ours:
// their record layout is not known well enough). Parse followed by
// MarshalBinary reproduces every version-4 file byte for byte; MarshalBinary
// always writes version 4.
//
// The icon fields and the display state are only used by the original's user
// interface. They are kept as loaded; Arrange sets them for a new file.
package vms

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Format constants (reverse-engineered, #56).
const (
	// Version is the format version this package writes and the newest it
	// reads.
	Version = 4
	// Tag identifies the file type; it is stored in the trailer without a
	// length byte.
	Tag = "VE.Bus configuration file"
	// RecordLen is the length of a unit's system record.
	RecordLen = 9
	// NoCell is the selected-cell code of a layout with no cell selected.
	NoCell = 0xFF
)

// Setup types (the header's first byte).
const (
	SetupSingle byte = iota
	SetupDual120
	SetupDual180
	SetupDual240
	SetupThreePhase
)

// L2 options (the header's second byte).
const (
	L2Fixed byte = iota
	L2Floating
	L2FloatingWithReturn
)

// Errors returned by Parse and MarshalBinary wrap one of these.
var (
	// ErrCorrupt means the checksum does not match: the file is damaged.
	ErrCorrupt = errors.New("vms: checksum mismatch, the file is damaged")
	// ErrNotVMS means the trailer does not identify a system file.
	ErrNotVMS = errors.New("vms: not a VE.Bus system configuration file")
	// ErrNewer means the file has a format version newer than Version.
	ErrNewer = errors.New("vms: file written by a newer program version")
	// ErrTooOld means the file has a format version older than 2.
	ErrTooOld = errors.New("vms: file format too old to read")
	// ErrFormat means the content does not follow the layout.
	ErrFormat = errors.New("vms: malformed file")
)

const (
	headerLen  = 1 + 1 + 4 + 4 + 4
	trailerLen = 4 + len(Tag) + 1
	minVersion = 2
	maxNameLen = 255
	// externalBFMark is the version word's high byte for a unit with an
	// external BF (reverse-engineered, #56).
	externalBFMark = 0xC7
	// Ours: the nominal icon pitch in pixels and the icons per row used by
	// Arrange. The original's icon geometry is not known.
	iconPitch   = 100
	iconsPerRow = 5
)

// File is a parsed .vms file.
type File struct {
	SetupType byte // Setup*
	L2Option  byte // L2*
	// ACInputs is stored as a u32; whether it is a count (1-4) or an index
	// is still to be confirmed on a real file.
	ACInputs uint32
	// GroupMask has bit i set when AC input i is switched as a group
	// (bits 0-3).
	GroupMask uint32
	// SelectedCell is the cell code selected in the user interface, or
	// NoCell.
	SelectedCell uint32
	Units        []*Unit
	// Version is the format version the file was read with. MarshalBinary
	// ignores it and writes Version.
	Version uint32
}

// Unit is one unit's record. For a top-level unit, Second is the pseudo
// unit of its second AC input (nil when it has none), Cell is its grid cell
// and SecondCell the second input's cell. For a pseudo unit, Second, Cell
// and SecondCell are not used.
type Unit struct {
	LongID       uint32
	VersionWord  uint32
	SystemRecord [RecordLen]byte // undecoded
	ResponseChar byte
	// Letter is the compatibility letter index (0xFF in files older than
	// version 3); ConfigProtocol is the config-protocol byte (0 there).
	Letter         byte
	ConfigProtocol byte
	// ExternalBF is stored from version 4 on; older files derive it from
	// the version word.
	ExternalBF bool
	// Display fields: the icon position in pixels, the icon snap step and
	// zoom percent, and a display state. They never reach a device.
	Top, Left    uint32
	Name         string
	Snap, Zoom   uint32
	DisplayState byte

	Second     *Unit
	Cell       uint32
	SecondCell uint32
}

// CellCode returns the code of the grid cell of phase (0-based) and AC
// input index: 10 × (phase+1) + input (reverse-engineered, #56).
func CellCode(phase, input int) uint32 { return uint32(10*(phase+1) + input) }

// Parse decodes a file. It checks, in this order, the checksum (ErrCorrupt),
// the trailer tag (ErrNotVMS) and the version (ErrNewer, ErrTooOld), then
// the layout (ErrFormat).
func Parse(data []byte) (*File, error) {
	var sum byte
	for _, c := range data {
		sum += c
	}
	if sum != 0 {
		return nil, ErrCorrupt
	}
	if len(data) < trailerLen || string(data[len(data)-1-len(Tag):len(data)-1]) != Tag {
		return nil, ErrNotVMS
	}
	trailerAt := len(data) - trailerLen
	f := &File{Version: binary.LittleEndian.Uint32(data[trailerAt:])}
	switch {
	case f.Version > Version:
		return nil, fmt.Errorf("%w (version %d)", ErrNewer, f.Version)
	case f.Version < minVersion:
		return nil, fmt.Errorf("%w (version %d)", ErrTooOld, f.Version)
	}

	r := &reader{b: data[:trailerAt], version: f.Version}
	f.SetupType = r.u8()
	f.L2Option = r.u8()
	f.ACInputs = r.u32()
	f.GroupMask = r.u32()
	f.SelectedCell = r.u32()
	if r.err == nil {
		r.err = checkHeader(f)
	}
	for r.err == nil && r.pos < len(r.b) {
		u, second := r.record()
		if second {
			var nested bool
			u.Second, nested = r.record()
			if nested && r.err == nil {
				r.fail("the pseudo unit of unit %d has a second input itself", len(f.Units))
			}
		}
		u.Cell = r.u32()
		if second {
			u.SecondCell = r.u32()
		}
		f.Units = append(f.Units, u)
	}
	if r.err != nil {
		return nil, r.err
	}
	return f, nil
}

func checkHeader(f *File) error {
	if f.SetupType > SetupThreePhase {
		return fmt.Errorf("%w: unknown setup type %d", ErrFormat, f.SetupType)
	}
	if f.L2Option > L2FloatingWithReturn {
		return fmt.Errorf("%w: unknown L2 option %d", ErrFormat, f.L2Option)
	}
	return nil
}

// reader reads the body sequentially. After the first error every read
// returns zero.
type reader struct {
	b       []byte
	pos     int
	version uint32
	err     error
}

func (r *reader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: "+format, append([]any{ErrFormat}, args...)...)
	}
}

func (r *reader) next(n int) []byte {
	if r.err != nil {
		return make([]byte, n)
	}
	if len(r.b)-r.pos < n {
		r.fail("data at %#x runs into the trailer", r.pos)
		return make([]byte, n)
	}
	r.pos += n
	return r.b[r.pos-n : r.pos]
}

func (r *reader) u8() byte    { return r.next(1)[0] }
func (r *reader) u32() uint32 { return binary.LittleEndian.Uint32(r.next(4)) }

func (r *reader) bool() bool {
	at := r.pos
	switch v := r.u8(); v {
	case 0, 1:
		return v == 1
	default:
		r.fail("byte %#x at %#x is neither 0 nor 1", v, at)
		return false
	}
}

func (r *reader) shortString() []byte { return r.next(int(r.u8())) }

// record reads one unit record and returns it with its "has second input"
// flag.
func (r *reader) record() (*Unit, bool) {
	u := &Unit{LongID: r.u32(), VersionWord: r.u32()}
	at := r.pos
	if rec := r.shortString(); r.err == nil && len(rec) != RecordLen {
		r.fail("system record at %#x has %d bytes", at, len(rec))
	} else {
		copy(u.SystemRecord[:], rec)
	}
	u.ResponseChar = r.u8()
	u.Letter, u.ConfigProtocol = 0xFF, 0
	if r.version >= 3 {
		u.Letter, u.ConfigProtocol = r.u8(), r.u8()
	}
	if r.version >= 4 {
		u.ExternalBF = r.bool()
	} else {
		u.ExternalBF = u.VersionWord>>24 == externalBFMark
	}
	u.Top, u.Left = r.u32(), r.u32()
	u.Name = decodeName(r.shortString())
	u.Snap, u.Zoom = r.u32(), r.u32()
	u.DisplayState = r.u8()
	return u, r.bool()
}

// MarshalBinary writes the file in format version 4.
func (f *File) MarshalBinary() ([]byte, error) {
	if err := checkHeader(f); err != nil {
		return nil, err
	}
	out := []byte{f.SetupType, f.L2Option}
	out = binary.LittleEndian.AppendUint32(out, f.ACInputs)
	out = binary.LittleEndian.AppendUint32(out, f.GroupMask)
	out = binary.LittleEndian.AppendUint32(out, f.SelectedCell)
	var err error
	for i, u := range f.Units {
		if u == nil {
			return nil, fmt.Errorf("%w: unit %d is nil", ErrFormat, i)
		}
		if out, err = appendRecord(out, u, u.Second != nil); err != nil {
			return nil, fmt.Errorf("unit %d: %w", i, err)
		}
		if u.Second != nil {
			if u.Second.Second != nil {
				return nil, fmt.Errorf("%w: the pseudo unit of unit %d has a second input itself", ErrFormat, i)
			}
			if out, err = appendRecord(out, u.Second, false); err != nil {
				return nil, fmt.Errorf("unit %d second input: %w", i, err)
			}
		}
		out = binary.LittleEndian.AppendUint32(out, u.Cell)
		if u.Second != nil {
			out = binary.LittleEndian.AppendUint32(out, u.SecondCell)
		}
	}
	out = binary.LittleEndian.AppendUint32(out, Version)
	out = append(out, Tag...)
	var sum byte
	for _, c := range out {
		sum += c
	}
	return append(out, -sum), nil
}

func appendRecord(out []byte, u *Unit, second bool) ([]byte, error) {
	name := encodeName(u.Name)
	if len(name) > maxNameLen {
		return nil, fmt.Errorf("%w: name longer than %d bytes", ErrFormat, maxNameLen)
	}
	out = binary.LittleEndian.AppendUint32(out, u.LongID)
	out = binary.LittleEndian.AppendUint32(out, u.VersionWord)
	out = append(out, RecordLen)
	out = append(out, u.SystemRecord[:]...)
	out = append(out, u.ResponseChar, u.Letter, u.ConfigProtocol, b2u(u.ExternalBF))
	out = binary.LittleEndian.AppendUint32(out, u.Top)
	out = binary.LittleEndian.AppendUint32(out, u.Left)
	out = append(out, byte(len(name)))
	out = append(out, name...)
	out = binary.LittleEndian.AppendUint32(out, u.Snap)
	out = binary.LittleEndian.AppendUint32(out, u.Zoom)
	return append(out, u.DisplayState, b2u(second)), nil
}

func b2u(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// Names are Windows-1252 text (ours). The five bytes Windows-1252 leaves
// undefined decode to the C1 control code points of the same value, so
// every byte decodes to one rune and back and a loaded name is written back
// unchanged. A rune Windows-1252 cannot represent is written as '?'.
func decodeByte(c byte) rune {
	if r := charmap.Windows1252.DecodeByte(c); r != utf8.RuneError {
		return r
	}
	return rune(c)
}

func decodeName(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = decodeByte(c)
	}
	return string(r)
}

func encodeName(s string) []byte {
	var b bytes.Buffer
	for _, r := range s {
		c, ok := charmap.Windows1252.EncodeRune(r)
		if !ok {
			c = '?'
			if r < 0x100 && decodeByte(byte(r)) == r {
				c = byte(r)
			}
		}
		b.WriteByte(c)
	}
	return b.Bytes()
}

// Slot is one icon in a grid cell: a unit, or the pseudo unit of a unit's
// second AC input.
type Slot struct {
	Unit   *Unit
	Second bool // the slot is Unit.Second
}

// Record returns the record that the slot shows: Unit, or Unit.Second.
func (s Slot) Record() *Unit {
	if s.Second {
		return s.Unit.Second
	}
	return s.Unit
}

// slots returns every slot in file order (a pseudo unit right after its
// unit) with its cell code.
func (f *File) slots() ([]Slot, []uint32) {
	var slots []Slot
	var cells []uint32
	for _, u := range f.Units {
		slots, cells = append(slots, Slot{Unit: u}), append(cells, u.Cell)
		if u.Second != nil {
			slots, cells = append(slots, Slot{Unit: u, Second: true}), append(cells, u.SecondCell)
		}
	}
	return slots, cells
}

// Cells returns the slots of every cell, keyed by cell code. Within a cell
// they are ordered row by row by icon position (top, then left); slots at
// the same position keep file order.
func (f *File) Cells() map[uint32][]Slot {
	slots, codes := f.slots()
	cells := map[uint32][]Slot{}
	for i, s := range slots {
		cells[codes[i]] = append(cells[codes[i]], s)
	}
	for _, c := range cells {
		slices.SortStableFunc(c, func(a, b Slot) int {
			ra, rb := a.Record(), b.Record()
			if ra.Top != rb.Top {
				return cmp.Compare(ra.Top, rb.Top)
			}
			return cmp.Compare(ra.Left, rb.Left)
		})
	}
	return cells
}

// Arrange prepares the units for a new file (ours: the original's icon
// geometry is not known). It sorts the units by cell, keeping their order
// within a cell, and sets the display fields of every unit and pseudo unit:
// snap step 1, zoom 100 %, display state 0, and an icon position from the
// slot's place in its cell, five per row on a 100-pixel pitch. Cells then
// returns each cell's slots in that same order.
func (f *File) Arrange() {
	slices.SortStableFunc(f.Units, func(a, b *Unit) int { return cmp.Compare(a.Cell, b.Cell) })
	slots, codes := f.slots()
	next := map[uint32]int{}
	for i, s := range slots {
		pos := next[codes[i]]
		next[codes[i]]++
		u := s.Record()
		u.Top = uint32(pos/iconsPerRow) * iconPitch
		u.Left = uint32(pos%iconsPerRow) * iconPitch
		u.Snap, u.Zoom, u.DisplayState = 1, 100, 0
	}
}
