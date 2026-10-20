// Package vff reads and validates the .vff firmware files of VE.Bus
// products and MK2/MK3 interfaces, so that a file can be checked before
// anything is flashed (#37). It reads the container only: the code and
// EEPROM records are opaque bytes, and nothing is decrypted or interpreted.
//
// Victron has published no description of this format. The layout below is
// reverse-engineered (#12) and restated in our own words; choices marked
// "ours" are this project's.
//
// Layout (integers little-endian). A file is one or more parts back to
// back, nothing after the last one:
//
//	part     := [header] | u32 checksum | body
//	header   := magic record | u16 format
//	record   := length (u8 or u16) | data | u8 check
//
// The check byte of a record is the 8-bit sum of its plain data; a
// zero-length record has check 0. Some records are encoded: byte i of the
// stored data is the plain byte plus 0x0C + i (mod 256). The magic record
// holds the 28 characters of Magic, plain or encoded, behind a u8 length.
//
// Formats 1-7 exist (1, 5, 6 and 7 were seen; 2-4 only in the original's
// code, so those paths are untested). Every part of format 3 and later has
// a header; in formats 1 and 2 only the first part does. The body is:
//
//	formats 3-7: encoded metadata (u16 length) | encoded descriptor
//	             (u16 length) | ML encoded records, then EL and CL records
//	             stored as they are (u16 lengths, counts from the metadata)
//	format 2:    encoded metadata (u16 length) | encoded descriptor (u16
//	             length, Main parts only; otherwise "C") | encoded records
//	             (u16 lengths) up to a zero-length one | 74-byte blocks
//	format 1:    no metadata (it reads as "Part=Main MinBFV=0 CL=-5 EL=4") |
//	             encoded descriptor (u8 length) after an implied "C" |
//	             encoded records (u8 lengths) up to a zero-length one |
//	             74-byte blocks up to the end of the file
//
// The 74-byte blocks have no length and no check byte. In formats 3-7 the
// checksum is the 32-bit sum of every byte of the part except the checksum
// itself; in formats 1 and 2 it is the sum of the 74-byte blocks only.
//
// The metadata is a list of Key=Value items separated by commas or white
// space; an item with spaces is in double quotes, with "" for a quote. Keys
// are compared without case. Unknown keys are kept. A part with Part=Main
// starts a group; the parts after it, such as a Bootblock, belong to it.
package vff

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Magic is the text of the magic record.
const Magic = "Victron Energy firmware file"

// MaxFormat is the newest format this package reads.
const MaxFormat = 7

// BlockLen is the length of the data blocks of formats 1 and 2.
const BlockLen = 74

// Errors returned by Parse wrap one of these, or are a *RecordError or a
// *ChecksumError.
var (
	// ErrNotVFF: the magic record is missing or wrong.
	ErrNotVFF = errors.New("vff: not a firmware file")
	// ErrNewer: the format is newer than MaxFormat.
	ErrNewer = errors.New("vff: file of a newer format; update this program")
	// ErrFormat: format 0, or a body that does not follow the layout
	// (ours: the original reads format 0 as format 1).
	ErrFormat = errors.New("vff: malformed file")
	// ErrTruncated: the file ends inside a field, a record or a block, or
	// before the records the metadata declares.
	ErrTruncated = errors.New("vff: unexpected end of file")
	// ErrFirstNotMain: the first part is not a Main part.
	ErrFirstNotMain = errors.New("vff: the first part is not a Main part")
)

// RecordError is a record whose check byte does not match its data.
type RecordError struct {
	Part   int    // 0-based
	Record string // which record: "magic", "metadata", "descriptor" or "record n"
	Stored byte
	Sum    byte
}

func (e *RecordError) Error() string {
	return fmt.Sprintf("vff: part %d, %s: check byte %02X, data sums to %02X", e.Part+1, e.Record, e.Stored, e.Sum)
}

// ChecksumError is a part whose checksum does not match its bytes.
type ChecksumError struct {
	Part             int // 0-based
	Stored, Computed uint32
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("vff: part %d: checksum %08X, computed %08X", e.Part+1, e.Stored, e.Computed)
}

// Item is one Key=Value item of a part's metadata.
type Item struct{ Key, Value string }

// Metadata is a part's metadata, in file order.
type Metadata []Item

// Get returns the value of key (ignoring case), or "".
func (m Metadata) Get(key string) string {
	for _, it := range m {
		if strings.EqualFold(it.Key, key) {
			return it.Value
		}
	}
	return ""
}

// Has reports whether key is present (ignoring case).
func (m Metadata) Has(key string) bool {
	for _, it := range m {
		if strings.EqualFold(it.Key, key) {
			return true
		}
	}
	return false
}

// Int returns the value of key as a number; 0 when it is missing or not a
// number, as the original reads it.
func (m Metadata) Int(key string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(m.Get(key)))
	return n
}

// Part is one part of a file.
type Part struct {
	// Offset and Length locate the part in the file.
	Offset, Length int
	// Format is the part's format: from its header, or the format of the
	// part before it (formats 1 and 2).
	Format int
	// HasHeader reports a part with its own magic and format; MagicEncoded
	// that the magic is stored encoded.
	HasHeader, MagicEncoded bool
	Checksum                uint32
	// Meta is the metadata; for format 1 the implied text.
	Meta Metadata
	// Descriptor is the plain descriptor record, starting with 'C'. Its
	// other bytes are opaque.
	Descriptor []byte
	// ML are the plain ML records. EEPROM and Code are the EL and CL
	// records (formats 3-7) or 74-byte blocks (formats 1 and 2), opaque.
	ML, EEPROM, Code [][]byte
}

// Name returns the part type: Main or Bootblock.
func (p *Part) Name() string { return p.Meta.Get("Part") }

// Main reports a Main part.
func (p *Part) Main() bool { return strings.EqualFold(p.Name(), "Main") }

// File is a parsed firmware file.
type File struct {
	Parts []*Part
	// Warnings are findings that do not make the file invalid (ours: the
	// original does not report them).
	Warnings []string
}

// Groups returns the parts grouped as the original flashes them: each Main
// part with the parts after it up to the next Main part.
func (f *File) Groups() [][]*Part {
	var g [][]*Part
	for _, p := range f.Parts {
		if p.Main() || len(g) == 0 {
			g = append(g, nil)
		}
		g[len(g)-1] = append(g[len(g)-1], p)
	}
	return g
}

// Version returns the firmware version of the file: the Version of the
// first Main part, with SubVersion as its high byte. ok is false when there
// is none or it is not below 2^24.
func (f *File) Version() (v uint32, ok bool) {
	for _, p := range f.Parts {
		if !p.Main() || !p.Meta.Has("Version") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(p.Meta.Get("Version")), 10, 32)
		if err != nil || n >= 1<<24 {
			return 0, false
		}
		return uint32(n) | uint32(p.Meta.Int("SubVersion"))<<24, true
	}
	return 0, false
}

// implied1 is the metadata of a format-1 part.
const implied1 = "Part=Main MinBFV=0 CL=-5 EL=4"

// Parse reads and validates a file. It stops at the first error. Findings
// that do not make the file invalid are in File.Warnings.
func Parse(data []byte) (*File, error) {
	f := &File{}
	r := &reader{b: data}
	format := 0
	for len(f.Parts) == 0 || r.pos < len(data) {
		at := r.pos
		p, err := r.part(len(f.Parts), format, f)
		if errors.Is(err, ErrNotVFF) && len(f.Parts) > 0 {
			return nil, fmt.Errorf("%w: the %d bytes after part %d do not start a part", ErrNotVFF, len(data)-at, len(f.Parts))
		}
		if err != nil {
			return nil, err
		}
		if len(f.Parts) > 0 && p.HasHeader && p.Format != format {
			f.warn("part %d has format %d, part 1 format %d", len(f.Parts)+1, p.Format, f.Parts[0].Format)
		}
		format = p.Format
		f.Parts = append(f.Parts, p)
	}
	if !f.Parts[0].Main() {
		return nil, ErrFirstNotMain
	}
	return f, nil
}

func (f *File) warn(format string, a ...any) {
	f.Warnings = append(f.Warnings, fmt.Sprintf(format, a...))
}

type reader struct {
	b   []byte
	pos int
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.b) {
		return nil, ErrTruncated
	}
	b := r.b[r.pos : r.pos+n]
	r.pos += n
	return b, nil
}

func (r *reader) u8() (int, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return int(b[0]), nil
}

func (r *reader) u16() (int, error) {
	b, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return int(b[0]) | int(b[1])<<8, nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// decode returns the plain bytes of encoded data.
func decode(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c - byte(0x0C+i)
	}
	return out
}

func sum8(b []byte) byte {
	var s byte
	for _, c := range b {
		s += c
	}
	return s
}

// record reads a record with a u16 (wide) or u8 length. Encoded data is
// returned decoded. The check byte is compared with the plain data.
func (r *reader) record(part int, name string, wide, encoded bool) ([]byte, error) {
	var n int
	var err error
	if wide {
		n, err = r.u16()
	} else {
		n, err = r.u8()
	}
	if err != nil {
		return nil, err
	}
	b, err := r.take(n)
	if err != nil {
		return nil, err
	}
	check, err := r.u8()
	if err != nil {
		return nil, err
	}
	if encoded {
		b = decode(b)
	}
	if s := sum8(b); s != byte(check) {
		return nil, &RecordError{Part: part, Record: name, Stored: byte(check), Sum: s}
	}
	return b, nil
}

// part reads part i at r.pos. format is the format so far (0 before the
// first part).
func (r *reader) part(i, format int, f *File) (*Part, error) {
	p := &Part{Offset: r.pos, Format: format}
	if format == 0 || format >= 3 {
		if err := r.header(p, f); err != nil {
			return nil, err
		}
	}
	csAt := r.pos
	var err error
	if p.Checksum, err = r.u32(); err != nil {
		return nil, err
	}
	var blockSum uint32
	switch {
	case p.Format >= 3:
		err = r.body3(p, i, f)
	default:
		blockSum, err = r.body12(p, i, f)
	}
	if err != nil {
		return nil, err
	}
	p.Length = r.pos - p.Offset
	computed := blockSum
	if p.Format >= 3 {
		computed = 0
		for _, c := range r.b[p.Offset:r.pos] {
			computed += uint32(c)
		}
		for _, c := range r.b[csAt : csAt+4] {
			computed -= uint32(c)
		}
	}
	if computed != p.Checksum {
		return nil, &ChecksumError{Part: i, Stored: p.Checksum, Computed: computed}
	}
	return p, nil
}

// header reads the magic record and the format.
func (r *reader) header(p *Part, f *File) error {
	n, err := r.u8()
	if err != nil || n != len(Magic) {
		return ErrNotVFF
	}
	b, err := r.take(n)
	if err != nil {
		return ErrNotVFF
	}
	check, err := r.u8()
	if err != nil {
		return ErrNotVFF
	}
	switch {
	case string(b) == Magic:
		if byte(check) != sum8(b) {
			return &RecordError{Part: len(f.Parts), Record: "magic", Stored: byte(check), Sum: sum8(b)}
		}
	case string(decode(b)) == Magic:
		p.MagicEncoded = true
		if byte(check) != sum8([]byte(Magic)) {
			f.warn("part %d: the check byte of the encoded magic is %02X, not %02X", len(f.Parts)+1, check, sum8([]byte(Magic)))
		}
	default:
		return ErrNotVFF
	}
	p.HasHeader = true
	if p.Format, err = r.u16(); err != nil {
		return err
	}
	switch {
	case p.Format > MaxFormat:
		return fmt.Errorf("%w (format %d)", ErrNewer, p.Format)
	case p.Format == 0:
		return fmt.Errorf("%w: format 0", ErrFormat)
	}
	return nil
}

// metadata reads and parses the metadata record.
func (r *reader) metadata(p *Part, i int, f *File) error {
	b, err := r.record(i, "metadata", true, true)
	if err != nil {
		return err
	}
	p.Meta = parseMeta(string(b), func(item string) {
		f.warn("part %d: metadata item %q has no '='", i+1, item)
	})
	for _, k := range []string{"Part", "Version", "CL"} {
		if !p.Meta.Has(k) {
			f.warn("part %d: the metadata has no %s", i+1, k)
		}
	}
	return nil
}

// body3 reads the body of a part of format 3-7.
func (r *reader) body3(p *Part, i int, f *File) error {
	if err := r.metadata(p, i, f); err != nil {
		return err
	}
	var err error
	if p.Descriptor, err = r.record(i, "descriptor", true, true); err != nil {
		return err
	}
	ml, el, cl := p.Meta.Int("ML"), p.Meta.Int("EL"), p.Meta.Int("CL")
	if ml < 0 || el < 0 || cl < 0 {
		return fmt.Errorf("%w: part %d declares ML %d, EL %d, CL %d", ErrFormat, i+1, ml, el, cl)
	}
	total := ml + el + cl
	for n := range total {
		if r.pos >= len(r.b) {
			return fmt.Errorf("%w: part %d has only %d of %d records", ErrTruncated, i+1, n, total)
		}
		b, err := r.record(i, fmt.Sprintf("record %d", n+1), true, n < ml)
		if err != nil {
			return err
		}
		switch {
		case n < ml:
			p.ML = append(p.ML, b)
		case n < ml+el:
			p.EEPROM = append(p.EEPROM, b)
		default:
			p.Code = append(p.Code, b)
		}
	}
	return nil
}

// body12 reads the body of a part of format 1 or 2 and returns the sum of
// its blocks.
func (r *reader) body12(p *Part, i int, f *File) (uint32, error) {
	wide := p.Format == 2
	var err error
	if wide {
		if err := r.metadata(p, i, f); err != nil {
			return 0, err
		}
	} else {
		p.Meta = parseMeta(implied1, nil)
	}
	switch {
	case wide && !p.Main():
		p.Descriptor = []byte("C")
	case wide:
		if p.Descriptor, err = r.record(i, "descriptor", true, true); err != nil {
			return 0, err
		}
	default:
		d, err := r.record(i, "descriptor", false, true)
		if err != nil {
			return 0, err
		}
		p.Descriptor = append([]byte("C"), d...)
	}
	for n := 1; ; n++ {
		b, err := r.record(i, fmt.Sprintf("record %d", n), wide, true)
		if err != nil {
			return 0, err
		}
		if len(b) == 0 {
			break
		}
		p.ML = append(p.ML, b)
	}
	// Format 1 reads blocks up to the end of the file; format 2 up to
	// EL+CL blocks or the end.
	want := -1
	if wide {
		want = p.Meta.Int("EL") + p.Meta.Int("CL")
	}
	var blocks [][]byte
	var sum uint32
	for want < 0 || len(blocks) < want {
		if r.pos == len(r.b) {
			break
		}
		b, err := r.take(BlockLen)
		if err != nil {
			return 0, err
		}
		for _, c := range b {
			sum += uint32(c)
		}
		blocks = append(blocks, b)
	}
	el := p.Meta.Int("EL")
	if !wide {
		el = 4 // the original's fixed EEPROM block count for format 1
	}
	el = min(el, len(blocks))
	p.EEPROM, p.Code = blocks[:el], blocks[el:]
	if !wide {
		p.Meta = setItem(p.Meta, "CL", strconv.Itoa(len(p.Code)))
	}
	return sum, nil
}

func setItem(m Metadata, key, value string) Metadata {
	for i := range m {
		if strings.EqualFold(m[i].Key, key) {
			m[i].Value = value
			return m
		}
	}
	return append(m, Item{key, value})
}

// parseMeta splits the metadata text into items. bad, if not nil, is
// called for an item without '='.
func parseMeta(s string, bad func(string)) Metadata {
	var m Metadata
	for i := 0; i < len(s); {
		if c := s[i]; c == ',' || c <= ' ' {
			i++
			continue
		}
		var item strings.Builder
		if s[i] == '"' {
			for i++; i < len(s); i++ {
				if s[i] == '"' {
					if i+1 < len(s) && s[i+1] == '"' {
						item.WriteByte('"')
						i++
						continue
					}
					i++
					break
				}
				item.WriteByte(s[i])
			}
		} else {
			for ; i < len(s) && s[i] != ',' && s[i] > ' '; i++ {
				item.WriteByte(s[i])
			}
		}
		k, v, ok := strings.Cut(item.String(), "=")
		if !ok && bad != nil {
			bad(item.String())
		}
		m = append(m, Item{Key: k, Value: v})
	}
	return m
}
