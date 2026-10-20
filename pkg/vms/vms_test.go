package vms

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The fixtures are synthetic: every value is invented. The builder below
// assembles the bytes with its own code, so the tests do not only check
// MarshalBinary against Parse.

type recSpec struct {
	longID, versionWord uint32
	record              [RecordLen]byte
	response, letter    byte
	config, externalBF  byte
	top, left           uint32
	name                []byte
	snap, zoom          uint32
	state, hasSecond    byte
}

type unitSpec struct {
	rec        recSpec
	second     *recSpec
	cell       uint32
	secondCell uint32
}

func buildRecord(b []byte, r recSpec, version uint32) []byte {
	b = binary.LittleEndian.AppendUint32(b, r.longID)
	b = binary.LittleEndian.AppendUint32(b, r.versionWord)
	b = append(b, RecordLen)
	b = append(b, r.record[:]...)
	b = append(b, r.response)
	if version >= 3 {
		b = append(b, r.letter, r.config)
	}
	if version >= 4 {
		b = append(b, r.externalBF)
	}
	b = binary.LittleEndian.AppendUint32(b, r.top)
	b = binary.LittleEndian.AppendUint32(b, r.left)
	b = append(b, byte(len(r.name)))
	b = append(b, r.name...)
	b = binary.LittleEndian.AppendUint32(b, r.snap)
	b = binary.LittleEndian.AppendUint32(b, r.zoom)
	return append(b, r.state, r.hasSecond)
}

// build assembles a file and seals it with a checksum byte.
func build(version uint32, header []byte, units []unitSpec) []byte {
	b := append([]byte(nil), header...)
	for _, u := range units {
		b = buildRecord(b, u.rec, version)
		if u.second != nil {
			b = buildRecord(b, *u.second, version)
		}
		b = binary.LittleEndian.AppendUint32(b, u.cell)
		if u.second != nil {
			b = binary.LittleEndian.AppendUint32(b, u.secondCell)
		}
	}
	b = binary.LittleEndian.AppendUint32(b, version)
	b = append(b, "VE.Bus configuration file"...)
	return seal(b)
}

// seal appends the byte that makes the byte sum 0.
func seal(b []byte) []byte {
	sum := 0
	for _, c := range b {
		sum += int(c)
	}
	return append(b, byte((256-sum%256)%256))
}

// reseal replaces the checksum byte of a complete file.
func reseal(b []byte) []byte { return seal(append([]byte(nil), b[:len(b)-1]...)) }

var testHeader = []byte{
	4,          // three phase
	0,          // L2 fixed
	2, 0, 0, 0, // AC inputs
	3, 0, 0, 0, // group mask
	21, 0, 0, 0, // selected cell
}

var (
	recA = recSpec{
		longID: 0x0a0b0c01, versionWord: 0x00112233,
		record:   [RecordLen]byte{1, 2, 3, 4, 5, 6, 0x00, 0xf0, 0},
		response: 3, letter: 2, config: 1, externalBF: 0,
		top: 0, left: 100, name: []byte("Unit A"), snap: 1, zoom: 100, state: 0,
	}
	recB = recSpec{
		longID: 0x0a0b0c02, versionWord: 0xc7112233,
		record:   [RecordLen]byte{9, 8, 7, 6, 5, 4, 0x04, 0xe1, 1},
		response: 4, letter: 0xff, config: 0, externalBF: 1,
		top: 37, left: 12, name: []byte("Wechselrichter \xe4\x80\x9f"), snap: 7, zoom: 150, state: 2, hasSecond: 1,
	}
	recB2 = recSpec{
		longID: 0x0a0b0c02, versionWord: 0xc7112233,
		record:   [RecordLen]byte{9, 8, 7, 6, 5, 4, 0x04, 0xe1, 2},
		response: 4, letter: 0xff, config: 0, externalBF: 1,
		top: 300, left: 0, name: nil, snap: 1, zoom: 100, state: 1,
	}
	recC = recSpec{
		longID: 0x0a0b0c03, versionWord: 0x00445566,
		record:   [RecordLen]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x08, 0xf0, 3},
		response: 2, letter: 5, config: 2, externalBF: 0,
		top: 0, left: 0, name: []byte("\x81\x8d\x8f\x90\x9d"), snap: 1, zoom: 100,
	}
	testUnits = []unitSpec{
		{rec: recA, cell: 10},
		{rec: recB, second: &recB2, cell: 20, secondCell: 21},
		{rec: recC, cell: 10},
	}
)

func testFile() []byte { return build(4, testHeader, testUnits) }

func wantUnit(r recSpec, version uint32) *Unit {
	u := &Unit{
		LongID: r.longID, VersionWord: r.versionWord, SystemRecord: r.record,
		ResponseChar: r.response, Letter: r.letter, ConfigProtocol: r.config,
		ExternalBF: r.externalBF == 1,
		Top:        r.top, Left: r.left, Name: decodeName(r.name),
		Snap: r.snap, Zoom: r.zoom, DisplayState: r.state,
	}
	if version < 3 {
		u.Letter, u.ConfigProtocol = 0xff, 0
	}
	if version < 4 {
		u.ExternalBF = r.versionWord>>24 == 0xc7
	}
	return u
}

func wantFile(version uint32) *File {
	f := &File{SetupType: SetupThreePhase, L2Option: L2Fixed, ACInputs: 2, GroupMask: 3, SelectedCell: 21, Version: version}
	for _, s := range testUnits {
		u := wantUnit(s.rec, version)
		if s.second != nil {
			u.Second = wantUnit(*s.second, version)
			u.SecondCell = s.secondCell
		}
		u.Cell = s.cell
		f.Units = append(f.Units, u)
	}
	return f
}

func TestRoundTrip(t *testing.T) {
	data := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if want := wantFile(4); !reflect.DeepEqual(f, want) {
		t.Errorf("parsed\n%+v\nwant\n%+v", f, want)
	}
	if f.Units[1].Name != "Wechselrichter ä€Ÿ" {
		t.Errorf("name = %q", f.Units[1].Name)
	}
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatal("parse then write is not byte-identical")
	}
	// A file with no units round-trips too.
	empty := build(4, testHeader, nil)
	f, err = Parse(empty)
	if err != nil || len(f.Units) != 0 {
		t.Fatalf("empty file: %v", err)
	}
	if out, _ := f.MarshalBinary(); !bytes.Equal(out, empty) {
		t.Error("empty file not byte-identical")
	}
}

func TestEveryBitFlipDetected(t *testing.T) {
	data := testFile()
	for i := range data {
		for bit := 0; bit < 8; bit++ {
			bad := append([]byte(nil), data...)
			bad[i] ^= 1 << bit
			if _, err := Parse(bad); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("byte %#x bit %d flipped: err = %v", i, bit, err)
			}
		}
	}
}

func TestTrailer(t *testing.T) {
	data := testFile()
	tagAt := len(data) - 1 - len(Tag)
	versionAt := tagAt - 4

	bad := append([]byte(nil), data...)
	bad[tagAt+3] = 'b'
	if _, err := Parse(reseal(bad)); !errors.Is(err, ErrNotVMS) {
		t.Errorf("tag changed: err = %v", err)
	}
	for _, b := range [][]byte{nil, {0}, seal([]byte("VE.Bus configuration file"))} {
		if _, err := Parse(b); !errors.Is(err, ErrNotVMS) {
			t.Errorf("% x: err = %v", b, err)
		}
	}
	// Version 5 with an otherwise valid file is refused as newer.
	bad = append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(bad[versionAt:], 5)
	if _, err := Parse(reseal(bad)); !errors.Is(err, ErrNewer) {
		t.Errorf("version 5: err = %v", err)
	}
	if _, err := Parse(build(1, testHeader, testUnits)); !errors.Is(err, ErrTooOld) {
		t.Errorf("version 1: err = %v", err)
	}
	if _, err := Parse(build(0, testHeader, nil)); !errors.Is(err, ErrTooOld) {
		t.Errorf("version 0: err = %v", err)
	}
}

func TestOlderVersions(t *testing.T) {
	for _, v := range []uint32{2, 3} {
		f, err := Parse(build(v, testHeader, testUnits))
		if err != nil {
			t.Fatalf("version %d: %v", v, err)
		}
		if want := wantFile(v); !reflect.DeepEqual(f, want) {
			t.Errorf("version %d parsed\n%+v\nwant\n%+v", v, f, want)
		}
		if !f.Units[1].ExternalBF || f.Units[0].ExternalBF {
			t.Errorf("version %d: external BF not derived from the version word", v)
		}
		// Written back as version 4, it reads back the same.
		out, err := f.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		f2, err := Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		if f2.Version != 4 {
			t.Errorf("version %d written as %d", v, f2.Version)
		}
		f2.Version = v
		if !reflect.DeepEqual(f2, f) {
			t.Errorf("version %d: converted file differs", v)
		}
	}
}

func TestMalformed(t *testing.T) {
	mutate := func(name string, header []byte, units []unitSpec) {
		t.Helper()
		if _, err := Parse(build(4, header, units)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	unitsWith := func(fn func(u *unitSpec)) []unitSpec {
		us := append([]unitSpec(nil), testUnits...)
		second := *us[1].second
		us[1].second = &second
		fn(&us[1])
		return us
	}
	mutate("short header", testHeader[:13], nil)
	mutate("setup type", append([]byte{5}, testHeader[1:]...), nil)
	mutate("L2 option", append([]byte{4, 3}, testHeader[2:]...), nil)
	mutate("external BF 2", testHeader, unitsWith(func(u *unitSpec) { u.rec.externalBF = 2 }))
	mutate("has second 2", testHeader, unitsWith(func(u *unitSpec) { u.rec.hasSecond = 2 }))
	mutate("nested second input", testHeader, unitsWith(func(u *unitSpec) { u.second.hasSecond = 1 }))

	data := testFile()
	// A system record of 8 bytes.
	bad := append([]byte(nil), data...)
	bad[len(testHeader)+8] = 8
	if _, err := Parse(reseal(bad)); !errors.Is(err, ErrFormat) {
		t.Errorf("record length 8: err = %v", err)
	}
	// Units that stop short of, or run into, the trailer.
	trailer := data[len(data)-trailerLen : len(data)-1]
	for _, cut := range []int{1, 4, 20} {
		b := append([]byte(nil), data[:len(data)-trailerLen-cut]...)
		if _, err := Parse(seal(append(b, trailer...))); !errors.Is(err, ErrFormat) {
			t.Errorf("body cut by %d: err = %v", cut, err)
		}
	}
	b := append([]byte(nil), data[:len(data)-trailerLen]...)
	if _, err := Parse(seal(append(append(b, 0), trailer...))); !errors.Is(err, ErrFormat) {
		t.Errorf("trailing byte before the trailer: err = %v", err)
	}
}

func TestMarshalErrors(t *testing.T) {
	for name, f := range map[string]*File{
		"setup type": {SetupType: 5},
		"L2 option":  {L2Option: 3},
		"nil unit":   {Units: []*Unit{nil}},
		"long name":  {Units: []*Unit{{Name: strings.Repeat("x", 256)}}},
		"nested":     {Units: []*Unit{{Second: &Unit{Second: &Unit{}}}}},
	} {
		if _, err := f.MarshalBinary(); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// 255 bytes is the longest name; "€" is one byte in Windows-1252.
	f := &File{Units: []*Unit{{Name: strings.Repeat("€", 255)}}}
	if _, err := f.MarshalBinary(); err != nil {
		t.Errorf("255-byte name: %v", err)
	}
}

func TestNames(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	if got := encodeName(decodeName(all)); !bytes.Equal(got, all) {
		t.Errorf("Windows-1252 round trip: % x", got)
	}
	for in, want := range map[string]string{
		"Multi 1":  "Multi 1",
		"Größe €5": "Gr\xf6\xdfe \x805",
		"Ω 日本":     "? ??",
	} {
		if got := string(encodeName(in)); got != want {
			t.Errorf("encodeName(%q) = % x, want % x", in, got, want)
		}
	}
}

func TestNewFile(t *testing.T) {
	rec := func(id uint32, b8 byte) *Unit {
		return &Unit{LongID: id, VersionWord: 0x00112233, SystemRecord: [RecordLen]byte{8: b8},
			ResponseChar: 3, Letter: 1, ConfigProtocol: 1, Name: "u", Top: 999, Left: 999, Snap: 9, Zoom: 9, DisplayState: 9}
	}
	units := make([]*Unit, 8)
	for i := range units {
		units[i] = rec(uint32(i+1), byte(i))
		units[i].Cell = CellCode(0, 0)
	}
	units[1].Cell = CellCode(1, 0)
	units[2].Second = rec(100, 0x80)
	units[2].SecondCell = CellCode(1, 0)
	f := &File{SetupType: SetupDual120, L2Option: L2Floating, ACInputs: 1, SelectedCell: NoCell, Units: units}
	f.Arrange()

	// Cell 10 holds units 1, 3, 4, ..., 8 in order (7 slots, two rows).
	// Cell 20 holds unit 3's pseudo unit, then unit 2: after sorting by
	// cell, unit 3 and its pseudo unit come before unit 2 in file order.
	if CellCode(0, 0) != 10 || CellCode(1, 0) != 20 || CellCode(2, 3) != 33 {
		t.Fatal("CellCode")
	}
	cells := f.Cells()
	ids := func(code uint32) []uint32 {
		var r []uint32
		for _, s := range cells[code] {
			r = append(r, s.Record().LongID)
		}
		return r
	}
	if got := ids(10); !reflect.DeepEqual(got, []uint32{1, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("cell 10 = %v", got)
	}
	if got := ids(20); !reflect.DeepEqual(got, []uint32{100, 2}) || !cells[20][0].Second || cells[20][0].Unit.LongID != 3 {
		t.Errorf("cell 20 = %v", got)
	}
	for code, want := range map[uint32][][2]uint32{
		10: {{0, 0}, {0, 100}, {0, 200}, {0, 300}, {0, 400}, {100, 0}, {100, 100}},
		20: {{0, 0}, {0, 100}},
	} {
		for i, s := range cells[code] {
			u := s.Record()
			if u.Top != want[i][0] || u.Left != want[i][1] || u.Snap != 1 || u.Zoom != 100 || u.DisplayState != 0 {
				t.Errorf("cell %d slot %d: top %d left %d snap %d zoom %d state %d",
					code, i, u.Top, u.Left, u.Snap, u.Zoom, u.DisplayState)
			}
		}
	}

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	f.Version = Version
	if !reflect.DeepEqual(f2, f) {
		t.Errorf("new file reads back differently:\n%+v\n%+v", f2, f)
	}
	if !reflect.DeepEqual(f2.Cells(), cells) {
		t.Error("cells differ after reload")
	}
}

func TestCellsOrder(t *testing.T) {
	at := func(id, top, left uint32) *Unit { return &Unit{LongID: id, Top: top, Left: left, Cell: 30} }
	f := &File{Units: []*Unit{at(1, 100, 0), at(2, 0, 200), at(3, 0, 100), at(4, 0, 100), at(5, 100, 0)}}
	var got []uint32
	for _, s := range f.Cells()[30] {
		got = append(got, s.Unit.LongID)
	}
	if want := []uint32{3, 4, 2, 1, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestRealFiles round-trips the .vms files in $VMS_TESTDATA, if set. Files
// made by the original tools are not committed. It logs no identifiers.
func TestRealFiles(t *testing.T) {
	dir := os.Getenv("VMS_TESTDATA")
	if dir == "" {
		t.Skip("VMS_TESTDATA not set")
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.vms"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no .vms files under VMS_TESTDATA (%v)", err)
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("version %d, setup %d, %d units", f.Version, f.SetupType, len(f.Units))
			if f.Version != Version {
				return
			}
			if out, err := f.MarshalBinary(); err != nil || !bytes.Equal(out, data) {
				t.Errorf("not byte-identical after parse and write (%v)", err)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	f.Add(testFile())
	f.Add(build(3, testHeader, testUnits))
	f.Add(build(4, testHeader, nil))
	f.Fuzz(func(t *testing.T, b []byte) {
		file, err := Parse(b)
		if err != nil {
			return
		}
		out, err := file.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		if file.Version == Version && !bytes.Equal(out, b) {
			t.Fatal("version-4 file not byte-identical")
		}
		_ = file.Cells()
		file.Arrange()
		if _, err := file.MarshalBinary(); err != nil {
			t.Fatal(err)
		}
	})
}
