package rvsc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// The fixtures below are synthetic: every value is invented. The builder
// assembles the bytes itself, with its own checksum code, so that the tests
// do not only check MarshalBinary against itself.

// refChecksum computes the section checksum byte by byte.
func refChecksum(b []byte) uint32 {
	var sum uint32
	for i, c := range b {
		sum += uint32(c) << (8 * (i % 4))
	}
	return sum
}

type rawSection struct {
	name    string
	payload []byte
}

// buildFile assembles a file and returns it with the file offset of each
// section's payload.
func buildFile(secs ...rawSection) ([]byte, []int) {
	var b []byte
	b = binary.LittleEndian.AppendUint16(b, 29)
	b = append(b, "VEConfig setting section file"...)
	var payloadAt []int
	for _, s := range secs {
		start := len(b)
		b = binary.LittleEndian.AppendUint16(b, uint16(len(s.name)))
		b = append(b, s.name...)
		b = binary.LittleEndian.AppendUint32(b, uint32(start+2+len(s.name)+4+len(s.payload)+4))
		payloadAt = append(payloadAt, len(b))
		b = append(b, s.payload...)
		b = binary.LittleEndian.AppendUint32(b, refChecksum(b[start:]))
	}
	return b, payloadAt
}

func toolInfoPayload(version string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 1)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(version)))
	return append(b, version...)
}

var testInfo = []mk2.SettingInfo{
	{Sc: 1, Offset: 0, Default: 7, Min: 0, Max: 100},
	{Sc: -100, Offset: 0, Default: 2711, Min: 2000, Max: 3200},
	{Sc: -10, Offset: -3, Default: 160, Min: 10, Max: 1000},
	{Sc: 0, Offset: 0, Default: 0, Min: 0, Max: 0},
	{Sc: 15, Offset: 1, Default: 4, Min: 0, Max: 96},
	{Sc: -256, Offset: 1, Default: 255, Min: 0, Max: 255},
}

func settingInfoPayload(fw uint32, recs []mk2.SettingInfo, tail []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 4)
	b = binary.LittleEndian.AppendUint32(b, fw)
	b = append(b, 2)
	b = binary.LittleEndian.AppendUint16(b, uint16(10*len(recs)))
	for _, r := range recs {
		b = binary.LittleEndian.AppendUint16(b, uint16(r.Sc))
		b = binary.LittleEndian.AppendUint16(b, uint16(r.Offset))
		b = binary.LittleEndian.AppendUint16(b, r.Default)
		b = binary.LittleEndian.AppendUint16(b, r.Min)
		b = binary.LittleEndian.AppendUint16(b, r.Max)
	}
	return append(b, tail...)
}

type unitSpec struct {
	longID, firmware   uint32
	phase, flag, index byte
	serial             string
	upload             bool
	saveTime           uint32
	settings           []uint16
	assistant          []byte
}

func unitPayload(u unitSpec) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 3)
	b = binary.LittleEndian.AppendUint32(b, u.longID)
	b = binary.LittleEndian.AppendUint32(b, u.firmware)
	for i := 0x0c; i < 0x22; i++ { // undecoded header bytes: an invented pattern
		b = append(b, byte(0x40+i))
	}
	binary.LittleEndian.PutUint16(b[0x13:], 6) // 6 extra bytes: the system record starts at 0x1c
	b = append(b, u.phase, u.flag, u.index)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(u.serial)))
	serial := make([]byte, 11)
	copy(serial, u.serial)
	b = append(b, serial...)
	if u.upload { // invented 12-byte blob, export time, 4 zero bytes
		b = append(b, 0x5a, 0, 0x33, 0x44, 1, 2, 3, 4, 5, 6, 7, 8)
		b = binary.LittleEndian.AppendUint32(b, u.saveTime-5)
		b = append(b, 0, 0, 0, 0)
	} else {
		b = append(b, make([]byte, 10)...)
	}
	b = binary.LittleEndian.AppendUint64(b, uint64(u.saveTime))
	b = binary.LittleEndian.AppendUint16(b, uint16(2*len(u.settings)))
	for _, v := range u.settings {
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return append(b, u.assistant...)
}

var (
	unitA = unitSpec{
		longID: 0x11223344, firmware: 0x00123456, phase: 0x00, flag: 0xf0, index: 0,
		serial: "ZZ0001TEST1", saveTime: 1700000000,
		settings:  []uint16{5, 2800, 300, 0xffff, 8, 250},
		assistant: []byte{0x00, 0x00, 0xff, 0x00, 0x0b},
	}
	unitB = unitSpec{
		longID: 0x55667788, firmware: 0xab123456, phase: 0x04, flag: 0xe9, index: 1,
		serial: "ZZ0002TEST", upload: true, saveTime: 1700000100,
		settings:  []uint16{9, 2700, 160, 0xffff, 4, 255},
		assistant: []byte{0x07, 0x00, 0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0xff, 0x12, 0x34, 0x00},
	}
	infoTail = []byte{0x80, 0x81, 0xc0, 0x80, 0x81, 0xc0, 0x13, 0x37}
)

// testFile is a two-unit file with an unknown section between the units.
func testFile() ([]byte, []int) {
	return buildFile(
		rawSection{SectionToolInfo, toolInfoPayload("9.87")},
		rawSection{SectionSettingInfo, settingInfoPayload(0x00654321, testInfo, infoTail)},
		rawSection{SectionSettingData, unitPayload(unitA)},
		rawSection{"SomethingNew", []byte{1, 2, 3, 4, 5, 6, 7}},
		rawSection{SectionSettingData, unitPayload(unitB)},
	)
}

func TestChecksum(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want uint32
	}{
		{nil, 0},
		{[]byte{1}, 1},
		{[]byte{1, 0, 0, 0, 2, 0, 0, 0, 3}, 6},
		{[]byte{0, 0, 0, 0, 0, 1}, 0x100},
		{[]byte{0xff, 0xff, 0xff, 0xff, 2, 0, 0, 0}, 1},
		{[]byte{0x0f, 0x00, 0x42, 0x61}, 0x6142000f},
	} {
		if got := checksum(tc.in); got != tc.want || refChecksum(tc.in) != tc.want {
			t.Errorf("checksum(% x) = %08x (ref %08x), want %08x", tc.in, got, refChecksum(tc.in), tc.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	data, _ := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Sections) != 5 || f.Sections[3].Name != "SomethingNew" {
		t.Fatalf("sections = %v", f.Sections)
	}
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Fatal("parse then write is not byte-identical")
	}
}

func TestAccessors(t *testing.T) {
	data, _ := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.ToolVersion(); err != nil || v != "9.87" {
		t.Errorf("ToolVersion = %q, %v", v, err)
	}
	info, err := f.SettingInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.FirmwareWord != 0x00654321 || len(info.Settings) != len(testInfo) {
		t.Fatalf("info = %+v", info)
	}
	for i, want := range testInfo {
		if info.Settings[i] != want {
			t.Errorf("info[%d] = %+v, want %+v", i, info.Settings[i], want)
		}
	}
	if got := info.Settings[1].Value(2800); got != 28 {
		t.Errorf("Value = %v, want 28", got)
	}

	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("%d units", len(units))
	}
	for i, spec := range []unitSpec{unitA, unitB} {
		u := units[i]
		if u.SectionVersion != 3 || u.LongID != spec.longID || u.FirmwareWord != spec.firmware ||
			u.Firmware() != spec.firmware&0xffffff || u.Phase != spec.phase ||
			u.AssistantFlag != spec.flag || u.Index != spec.index || u.Serial != spec.serial ||
			u.UploadForm != spec.upload || !u.SaveTime.Equal(time.Unix(int64(spec.saveTime), 0)) {
			t.Errorf("unit %d = %+v, want %+v", i, u, spec)
		}
		if u.NumSettings() != len(spec.settings) {
			t.Fatalf("unit %d: %d settings", i, u.NumSettings())
		}
		for id, want := range spec.settings {
			if got, err := u.Setting(id); err != nil || got != want {
				t.Errorf("unit %d setting %d = %d, %v; want %d", i, id, got, err, want)
			}
		}
		if got := u.Settings(); !equalU16(got, spec.settings) {
			t.Errorf("unit %d Settings = %v", i, got)
		}
		if !bytes.Equal(u.AssistantArea(), spec.assistant) {
			t.Errorf("unit %d assistant area = % x", i, u.AssistantArea())
		}
		if _, err := u.Setting(len(spec.settings)); err == nil {
			t.Errorf("unit %d: out-of-range setting accepted", i)
		}
		if err := u.SetSetting(-1, 0); err == nil {
			t.Errorf("unit %d: negative setting ID accepted", i)
		}
	}
}

func equalU16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSetSetting(t *testing.T) {
	data, payloadAt := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	// Unit B is in upload form: its settings start 10 bytes later.
	const id = 2
	if err := units[1].SetSetting(id, 0x1234); err != nil {
		t.Fatal(err)
	}
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(data) {
		t.Fatalf("length changed: %d -> %d", len(data), len(out))
	}
	valueAt := payloadAt[4] + unitOffSettings + unitUploadShift + 2*id
	checksumAt := len(data) - 4
	for i := range data {
		changed := data[i] != out[i]
		expected := i == valueAt || i == valueAt+1 || i >= checksumAt
		if changed && !expected {
			t.Errorf("byte %#x changed unexpectedly", i)
		}
	}
	if out[valueAt] != 0x34 || out[valueAt+1] != 0x12 {
		t.Errorf("value bytes = % x", out[valueAt:valueAt+2])
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("edited file does not parse: %v", err)
	}
	if got := refChecksum(out[payloadAt[4]-6-len(SectionSettingData) : checksumAt]); got != binary.LittleEndian.Uint32(out[checksumAt:]) {
		t.Errorf("stored checksum %08x, reference %08x", binary.LittleEndian.Uint32(out[checksumAt:]), got)
	}
	u2, err := f2.Units()
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := u2[1].Setting(id); v != 0x1234 {
		t.Errorf("re-read value = %#x", v)
	}
	if !bytes.Equal(u2[1].AssistantArea(), unitB.assistant) || !bytes.Equal(f2.Sections[3].Payload, f.Sections[3].Payload) {
		t.Error("assistant area or unknown section changed")
	}
	// Writing the old value back restores the original bytes.
	if err := units[1].SetSetting(id, unitB.settings[id]); err != nil {
		t.Fatal(err)
	}
	if out, _ := f.MarshalBinary(); !bytes.Equal(out, data) {
		t.Error("restoring the value does not restore the file")
	}
}

func TestParseCopiesInput(t *testing.T) {
	data, _ := testFile()
	orig := append([]byte(nil), data...)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, _ := f.Units()
	if err := units[0].SetSetting(0, 999); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, orig) {
		t.Error("editing the File changed the caller's input")
	}
}

// gridInfoTail returns invented bytes that follow the setting records:
// undecoded bytes, one access byte per testInfo setting, the u16 length
// of table and table.
func gridInfoTail(undecoded, table []byte) []byte {
	b := append([]byte(nil), undecoded...)
	b = append(b, 0x81, 0x80, 0x00, 0x81, 0x00, 0x7e)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(table)))
	return append(b, table...)
}

func TestGridInfo(t *testing.T) {
	table := []byte{0x09, 0x00, 3, 2, 1, 0x09, 0x00, 0x0a, 0x00, 0x02, 0x01, 0x00, 0x00, 0x00}
	parse := func(tail []byte) *File {
		t.Helper()
		data, _ := buildFile(rawSection{SectionSettingInfo, settingInfoPayload(0x00654321, testInfo, tail)})
		f, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	for _, undecoded := range [][]byte{nil, {0x50, 0, 0, 0, 0, 0, 0x80, 0}} {
		got, err := parse(gridInfoTail(undecoded, table)).GridInfo()
		if err != nil || !bytes.Equal(got, table) {
			t.Errorf("%d undecoded bytes: GridInfo = % x, %v; want % x", len(undecoded), got, err, table)
		}
	}
	// No table: the fixture's short tail, a too short table, no tail.
	for _, tail := range [][]byte{infoTail, gridInfoTail(nil, []byte{1, 2, 3, 4}), nil} {
		if got, err := parse(tail).GridInfo(); err != nil || got != nil {
			t.Errorf("tail % x: GridInfo = % x, %v; want none", tail, got, err)
		}
	}
	// A second place that reads as a table length is ambiguous.
	tail := gridInfoTail([]byte{0, 0, 0, 0, 0, 0, 0, 0}, table)
	binary.LittleEndian.PutUint16(tail[6:], uint16(len(tail)-8))
	if _, err := parse(tail).GridInfo(); !errors.Is(err, ErrFormat) {
		t.Errorf("ambiguous table: %v, want ErrFormat", err)
	}
	// The result is a copy.
	f := parse(gridInfoTail(nil, table))
	got, _ := f.GridInfo()
	got[0] = 0xff
	if again, _ := f.GridInfo(); again[0] != table[0] {
		t.Error("GridInfo returned the section's own bytes")
	}
}

func TestPayloadResize(t *testing.T) {
	data, _ := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	f.Sections[3].Payload = append(f.Sections[3].Payload, 8, 9, 10)
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("resized file does not parse: %v", err)
	}
	if !bytes.Equal(f2.Sections[3].Payload, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) {
		t.Errorf("payload = % x", f2.Sections[3].Payload)
	}
	units, err := f2.Units()
	if err != nil || len(units) != 2 || units[1].Serial != unitB.serial {
		t.Errorf("units after resize: %v, %v", units, err)
	}
}

func TestCorruptChecksum(t *testing.T) {
	data, payloadAt := testFile()
	for i, at := range payloadAt {
		bad := append([]byte(nil), data...)
		bad[at] ^= 0x01
		if _, err := Parse(bad); !errors.Is(err, ErrChecksum) {
			t.Errorf("section %d payload flipped: err = %v", i, err)
		}
	}
	bad := append([]byte(nil), data...)
	bad[len(bad)-1] ^= 0x80
	if _, err := Parse(bad); !errors.Is(err, ErrChecksum) {
		t.Errorf("checksum flipped: err = %v", err)
	}
}

func TestMalformed(t *testing.T) {
	data, _ := testFile()
	// Prefix length -> number of sections, for prefixes that end where a
	// section ends (computed from the next pointers).
	boundaries := map[int]int{headerLen: 0}
	pos := headerLen
	for n := 1; pos < len(data); n++ {
		nameLen := int(binary.LittleEndian.Uint16(data[pos:]))
		pos = int(binary.LittleEndian.Uint32(data[pos+2+nameLen:]))
		boundaries[pos] = n
	}
	// A prefix that ends on a section boundary is a valid file with fewer
	// sections: the format has no section count. Every other prefix fails.
	for n := 0; n < len(data); n++ {
		f, err := Parse(data[:n])
		if want, ok := boundaries[n]; ok {
			if err != nil || len(f.Sections) != want {
				t.Errorf("prefix %d: %v", n, err)
			}
			continue
		}
		if !errors.Is(err, ErrFormat) && !errors.Is(err, ErrChecksum) {
			t.Errorf("prefix %d: err = %v", n, err)
		}
	}

	mutate := func(name string, fn func(b []byte) []byte) {
		t.Helper()
		b := fn(append([]byte(nil), data...))
		if _, err := Parse(b); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	mutate("bad magic", func(b []byte) []byte { b[5] = 'x'; return b })
	mutate("bad magic length", func(b []byte) []byte { b[0] = 28; return b })
	mutate("next beyond end", func(b []byte) []byte {
		binary.LittleEndian.PutUint32(b[headerLen+2+len(SectionToolInfo):], uint32(len(b)+1))
		return b
	})
	mutate("next too small", func(b []byte) []byte {
		binary.LittleEndian.PutUint32(b[headerLen+2+len(SectionToolInfo):], uint32(headerLen+2+len(SectionToolInfo)+7))
		return b
	})
	mutate("empty name", func(b []byte) []byte { b[headerLen], b[headerLen+1] = 0, 0; return b })
	mutate("name not printable", func(b []byte) []byte { b[headerLen+2] = ' '; return b })
	mutate("trailing garbage", func(b []byte) []byte { return append(b, 0) })
}

func TestLayoutErrors(t *testing.T) {
	short := unitA
	short.settings = nil
	short.assistant = nil
	for _, tc := range []struct {
		name string
		secs []rawSection
		call func(*File) error
	}{
		{"no tool info", nil, func(f *File) error { _, err := f.ToolVersion(); return err }},
		{"tool info short", []rawSection{{SectionToolInfo, []byte{1, 0, 0, 0, 9, 0, 'x'}}},
			func(f *File) error { _, err := f.ToolVersion(); return err }},
		{"two tool infos", []rawSection{{SectionToolInfo, toolInfoPayload("1")}, {SectionToolInfo, toolInfoPayload("2")}},
			func(f *File) error { _, err := f.ToolVersion(); return err }},
		{"no setting info", nil, func(f *File) error { _, err := f.SettingInfo(); return err }},
		{"setting info array too long", []rawSection{{SectionSettingInfo, settingInfoPayload(1, testInfo, nil)[:30]}},
			func(f *File) error { _, err := f.SettingInfo(); return err }},
		{"setting info array not whole records", []rawSection{{SectionSettingInfo, func() []byte {
			b := settingInfoPayload(1, testInfo, nil)
			binary.LittleEndian.PutUint16(b[infoOffArrayLen:], 15)
			return b
		}()}}, func(f *File) error { _, err := f.SettingInfo(); return err }},
		{"unit too short", []rawSection{{SectionSettingData, unitPayload(unitA)[:unitOffSettings-1]}},
			func(f *File) error { _, err := f.Units(); return err }},
		{"unit array too long", []rawSection{{SectionSettingData, func() []byte {
			b := unitPayload(short)
			binary.LittleEndian.PutUint16(b[unitOffArrayLen:], 2)
			return b
		}()}}, func(f *File) error { _, err := f.Units(); return err }},
		{"unit array odd length", []rawSection{{SectionSettingData, func() []byte {
			b := unitPayload(unitA)
			binary.LittleEndian.PutUint16(b[unitOffArrayLen:], 3)
			return b
		}()}}, func(f *File) error { _, err := f.Units(); return err }},
	} {
		data, _ := buildFile(tc.secs...)
		f, err := Parse(data)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if err := tc.call(f); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
	}
	// An empty settings array and an empty assistant area are valid.
	data, _ := buildFile(rawSection{SectionSettingData, unitPayload(short)})
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if units, err := f.Units(); err != nil || units[0].NumSettings() != 0 || len(units[0].AssistantArea()) != 0 {
		t.Errorf("empty unit: %v", err)
	}
}

func TestMarshalInvalidName(t *testing.T) {
	for _, name := range []string{"", "has space", "tab\t"} {
		f := &File{Sections: []*Section{{Name: name}}}
		if _, err := f.MarshalBinary(); !errors.Is(err, ErrFormat) {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
}

// TestRealFiles checks files produced by the original tools and by GX
// devices. Real files contain device data and are not committed; set
// RVSC_TESTDATA to a directory of .rvsc/.rvms files to run it.
func TestRealFiles(t *testing.T) {
	dir := os.Getenv("RVSC_TESTDATA")
	if dir == "" {
		t.Skip("RVSC_TESTDATA not set")
	}
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ext := strings.ToLower(filepath.Ext(p)); !d.IsDir() && (ext == ".rvsc" || ext == ".rvms") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no .rvsc/.rvms files under RVSC_TESTDATA")
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
			out, err := f.MarshalBinary()
			if err != nil || !bytes.Equal(out, data) {
				t.Fatalf("not byte-identical after parse and write (%v)", err)
			}
			version, err := f.ToolVersion()
			if err != nil {
				t.Error(err)
			}
			info, err := f.SettingInfo()
			if err != nil {
				t.Fatal(err)
			}
			units, err := f.Units()
			if err != nil {
				t.Fatal(err)
			}
			if len(units) == 0 {
				t.Fatal("no units")
			}
			if table, err := f.GridInfo(); err != nil {
				t.Error(err)
			} else {
				t.Logf("grid-info table: %d bytes", len(table))
			}
			t.Logf("mk2vsc %s, %d setting info records, info firmware %d, %d units",
				version, len(info.Settings), info.FirmwareWord&0xffffff, len(units))
			for i, u := range units {
				t.Logf("unit %d: firmware %d, %d settings, upload form %v, assistant area %d bytes, saved %s",
					i, u.Firmware(), u.NumSettings(), u.UploadForm, len(u.AssistantArea()), u.SaveTime.Format(time.RFC3339))
				if u.NumSettings() != len(info.Settings) {
					t.Errorf("unit %d has %d settings, setting info has %d records", i, u.NumSettings(), len(info.Settings))
				}
			}
			// Edit one value and check that only it and its checksum change.
			old, _ := units[0].Setting(0)
			if err := units[0].SetSetting(0, old^1); err != nil {
				t.Fatal(err)
			}
			edited, err := f.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Parse(edited); err != nil {
				t.Fatalf("edited file does not parse: %v", err)
			}
			diff := 0
			for i := range data {
				if data[i] != edited[i] {
					diff++
				}
			}
			if diff < 2 || diff > 6 {
				t.Errorf("%d bytes changed by a one-bit edit", diff)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	data, _ := testFile()
	f.Add(data)
	f.Add(data[:headerLen])
	empty, _ := buildFile(rawSection{SectionSettingData, nil}, rawSection{SectionSettingInfo, nil}, rawSection{SectionToolInfo, nil})
	f.Add(empty)
	f.Fuzz(func(t *testing.T, b []byte) {
		file, err := Parse(b)
		if err != nil {
			return
		}
		out, err := file.MarshalBinary()
		if err != nil || !bytes.Equal(out, b) {
			t.Fatalf("parsed file does not write back identically (%v)", err)
		}
		_, _ = file.ToolVersion()
		if info, err := file.SettingInfo(); err == nil {
			for _, s := range info.Settings {
				_ = s.Value(s.Default)
			}
		}
		units, err := file.Units()
		if err != nil {
			return
		}
		for _, u := range units {
			_ = u.Settings()
			_ = u.AssistantArea()
			if u.NumSettings() > 0 {
				if err := u.SetSetting(u.NumSettings()-1, 0xa5a5); err != nil {
					t.Fatal(err)
				}
			}
		}
		out, err = file.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(out); err != nil {
			t.Fatalf("edited file does not parse: %v", err)
		}
	})
}

// FuzzPayloads wraps fuzzed payloads in a valid container, so that the
// section accessors see arbitrary content (FuzzParse rarely gets past the
// checksums).
func FuzzPayloads(f *testing.F) {
	f.Add(toolInfoPayload("9.87"), settingInfoPayload(1, testInfo, infoTail), unitPayload(unitA))
	f.Add([]byte{}, []byte{}, unitPayload(unitB))
	f.Fuzz(func(t *testing.T, tool, info, unit []byte) {
		data, _ := buildFile(rawSection{SectionToolInfo, tool}, rawSection{SectionSettingInfo, info},
			rawSection{SectionSettingData, unit})
		file, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.ToolVersion()
		_, _ = file.SettingInfo()
		units, err := file.Units()
		if err != nil {
			return
		}
		u := units[0]
		_ = u.Settings()
		area := u.AssistantArea()
		if u.NumSettings() > 0 {
			if err := u.SetSetting(0, 0x5a5a); err != nil {
				t.Fatal(err)
			}
		}
		out, err := file.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		f2, err := Parse(out)
		if err != nil {
			t.Fatal(err)
		}
		u2, err := f2.Units()
		if err != nil || !bytes.Equal(u2[0].AssistantArea(), area) {
			t.Fatalf("assistant area changed by a setting edit (%v)", err)
		}
	})
}
