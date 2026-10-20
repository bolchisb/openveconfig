package rvsc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// header rewrites the sequential part of a synthetic unit payload (0x0c up
// to 0x25, where the fixed-offset serial starts) and its section version.
// A header shorter than that span is padded with zeros after the record.
func header(p []byte, version uint32, letter, config, ident byte, extra []byte, resp byte, rec [SystemRecordLen]byte) []byte {
	p = append([]byte(nil), p...)
	binary.LittleEndian.PutUint32(p, version)
	var h []byte
	if version >= 3 {
		h = append(h, letter, config)
	}
	h = append(h, ident)
	h = binary.LittleEndian.AppendUint32(h, 0x00654321)
	h = binary.LittleEndian.AppendUint16(h, uint16(len(extra)))
	h = append(h, extra...)
	h = append(h, resp)
	h = append(h, rec[:]...)
	span := p[unitOffHeader : unitOffSerial-2]
	if len(h) > len(span) {
		panic("header longer than the fixed span")
	}
	clear(span)
	copy(span, h)
	return p
}

func TestSystemRecord(t *testing.T) {
	data, _ := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	for i, spec := range []unitSpec{unitA, unitB} {
		u := units[i]
		// The sequential parse lands on the fixed offsets in these files.
		if u.record+6 != unitOffPhase || u.record+7 != unitOffAssistantFlag || u.record+8 != unitOffIndex {
			t.Errorf("unit %d: record at %#x", i, u.record)
		}
		want := [SystemRecordLen]byte{0x5c, 0x5d, 0x5e, 0x5f, 0x60, 0x61, spec.phase, spec.flag, spec.index}
		if got := u.SystemRecord(); got != want {
			t.Errorf("unit %d: record % x, want % x", i, got, want)
		}
		if u.Letter != 0x4c || u.ConfigProtocol != 0x4d || u.IdentifyFlag != 0x4e || u.ResponseChar != 0x5b {
			t.Errorf("unit %d: letter %#x config %#x identify %#x response %#x",
				i, u.Letter, u.ConfigProtocol, u.IdentifyFlag, u.ResponseChar)
		}
		if u.ExternalBF() {
			t.Errorf("unit %d: external BF", i)
		}
	}
}

func TestExternalBF(t *testing.T) {
	spec := unitA
	spec.firmware = 0xc7123456
	data, _ := buildFile(rawSection{SectionSettingData, unitPayload(spec)})
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	if !units[0].ExternalBF() || units[0].Firmware() != 0x123456 {
		t.Errorf("external BF %v, firmware %#x", units[0].ExternalBF(), units[0].Firmware())
	}
}

func TestSetSystemRecord(t *testing.T) {
	data, payloadAt := testFile()
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	recs := [][SystemRecordLen]byte{
		{1, 2, 3, 4, 5, 6, 0x08, 0xf1, 2},
		{9, 8, 7, 6, 5, 4, 0x86, 0xe2, 3},
	}
	for i, u := range units {
		u.SetSystemRecord(recs[i])
		if u.Phase != recs[i][6] || u.AssistantFlag != recs[i][7] || u.Index != recs[i][8] {
			t.Errorf("unit %d: fields not updated: %+v", i, u)
		}
	}
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(data) {
		t.Fatalf("length changed: %d -> %d", len(data), len(out))
	}
	// Unit sections are sections 2 and 4; each ends with its checksum.
	sectionEnd := func(i int) int {
		if i+1 < len(payloadAt) {
			return payloadAt[i+1] - 6 - len(f.Sections[i+1].Name)
		}
		return len(data)
	}
	expected := map[int]bool{}
	for _, s := range []int{2, 4} {
		for k := 0; k < SystemRecordLen; k++ {
			expected[payloadAt[s]+0x1c+k] = true
		}
		for k := 1; k <= 4; k++ {
			expected[sectionEnd(s)-k] = true
		}
	}
	for i := range data {
		if data[i] != out[i] && !expected[i] {
			t.Errorf("byte %#x changed unexpectedly", i)
		}
	}
	f2, err := Parse(out)
	if err != nil {
		t.Fatalf("edited file does not parse: %v", err)
	}
	u2, err := f2.Units()
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range u2 {
		if u.SystemRecord() != recs[i] || u.Phase != recs[i][6] || u.Index != recs[i][8] {
			t.Errorf("unit %d re-read: % x", i, u.SystemRecord())
		}
	}
	if !bytes.Equal(u2[1].AssistantArea(), unitB.assistant) || u2[1].Serial != unitB.serial {
		t.Error("assistant area or serial changed")
	}
	// Writing the old records back restores the original bytes.
	units[0].SetSystemRecord([SystemRecordLen]byte{0x5c, 0x5d, 0x5e, 0x5f, 0x60, 0x61, unitA.phase, unitA.flag, unitA.index})
	units[1].SetSystemRecord([SystemRecordLen]byte{0x5c, 0x5d, 0x5e, 0x5f, 0x60, 0x61, unitB.phase, unitB.flag, unitB.index})
	if out, _ := f.MarshalBinary(); !bytes.Equal(out, data) {
		t.Error("restoring the records does not restore the file")
	}
}

func TestHeaderVariants(t *testing.T) {
	rec := [SystemRecordLen]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x04, 0xe7, 0x05}
	for _, tc := range []struct {
		name          string
		version       uint32
		extra         int
		at            int // payload offset of the record
		letter, confg byte
	}{
		{"version 3, 6 extra bytes", 3, 6, 0x1c, 0x21, 0x02},
		{"version 3, 4 extra bytes", 3, 4, 0x1a, 0x21, 0x02},
		{"version 2, 8 extra bytes", 2, 8, 0x1c, 0xff, 0},
		{"version 1, no extra bytes", 1, 0, 0x14, 0xff, 0},
	} {
		p := header(unitPayload(unitA), tc.version, 0x21, 0x02, 0x01, make([]byte, tc.extra), 0x03, rec)
		data, _ := buildFile(rawSection{SectionSettingData, p})
		f, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		units, err := f.Units()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		u := units[0]
		if u.record != tc.at || u.SystemRecord() != rec || u.Phase != 0x04 || u.AssistantFlag != 0xe7 || u.Index != 0x05 {
			t.Errorf("%s: record at %#x = % x", tc.name, u.record, u.SystemRecord())
		}
		if u.Letter != tc.letter || u.ConfigProtocol != tc.confg || u.IdentifyFlag != 1 || u.ResponseChar != 3 {
			t.Errorf("%s: letter %#x config %#x identify %#x response %#x",
				tc.name, u.Letter, u.ConfigProtocol, u.IdentifyFlag, u.ResponseChar)
		}
		// Settings are still found: the fixed tail is unchanged.
		if got := u.Settings(); !equalU16(got, unitA.settings) {
			t.Errorf("%s: settings %v", tc.name, got)
		}
	}

	bad := map[string][]byte{}
	for _, v := range []uint32{0, 4} {
		p := unitPayload(unitA)
		binary.LittleEndian.PutUint32(p, v)
		bad[fmt.Sprintf("version %d", v)] = p
	}
	p := unitPayload(unitA)
	binary.LittleEndian.PutUint16(p[0x13:], uint16(len(p)))
	bad["extra bytes beyond payload"] = p
	for name, p := range bad {
		data, _ := buildFile(rawSection{SectionSettingData, p})
		f, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Units(); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// TestSyntheticSystemLoadsBack builds a three-unit .rvms with a record per
// unit and checks that every unit loads back with its record.
func TestSyntheticSystemLoadsBack(t *testing.T) {
	recs := map[uint32][SystemRecordLen]byte{
		0x0a000001: {0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x00, 0xf0, 0},
		0x0a000002: {0x11, 0x21, 0x31, 0x41, 0x51, 0x61, 0x04, 0xf0, 1},
		0x0a000003: {0x12, 0x22, 0x32, 0x42, 0x52, 0x62, 0x08, 0xf0, 2},
	}
	secs := []rawSection{
		{SectionToolInfo, toolInfoPayload("9.87")},
		{SectionSettingInfo, settingInfoPayload(0x00654321, testInfo, infoTail)},
	}
	for _, id := range []uint32{0x0a000002, 0x0a000003, 0x0a000001} { // not in record order
		spec := unitA
		spec.longID = id
		secs = append(secs, rawSection{SectionSettingData,
			header(unitPayload(spec), 3, 0xff, 0, 1, []byte{1, 2, 3, 4, 5, 6}, 2, recs[id])})
	}
	data, _ := buildFile(secs...)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != len(recs) {
		t.Fatalf("%d units", len(units))
	}
	for _, u := range units {
		if u.SystemRecord() != recs[u.LongID] {
			t.Errorf("unit %#x: record % x, want % x", u.LongID, u.SystemRecord(), recs[u.LongID])
		}
	}
	if out, err := f.MarshalBinary(); err != nil || !bytes.Equal(out, data) {
		t.Errorf("not byte-identical (%v)", err)
	}
}

// TestRealFilesSystemRecord checks the system record on the files in
// $RVSC_TESTDATA, if set (see TestRealFiles). It logs no identifiers.
func TestRealFilesSystemRecord(t *testing.T) {
	dir := os.Getenv("RVSC_TESTDATA")
	if dir == "" {
		t.Skip("RVSC_TESTDATA not set")
	}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ext := strings.ToLower(filepath.Ext(p)); d.IsDir() || (ext != ".rvsc" && ext != ".rvms") {
			return nil
		}
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			units, err := f.Units()
			if err != nil {
				t.Fatal(err)
			}
			for i, u := range units {
				if u.record+6 != unitOffPhase {
					t.Logf("unit %d: section version %d, record at %#x, not at the fixed offset", i, u.SectionVersion, u.record)
				}
				orig := u.SystemRecord()
				r := orig
				r[0] ^= 0x01
				u.SetSystemRecord(r)
				edited, err := f.MarshalBinary()
				if err != nil {
					t.Fatal(err)
				}
				f2, err := Parse(edited)
				if err != nil {
					t.Fatalf("unit %d: edited file does not parse: %v", i, err)
				}
				u2, err := f2.Units()
				if err != nil || u2[i].SystemRecord() != r {
					t.Fatalf("unit %d: edited record not read back (%v)", i, err)
				}
				diff := 0
				for k := range data {
					if data[k] != edited[k] {
						diff++
					}
				}
				if diff < 2 || diff > 1+4 {
					t.Errorf("unit %d: %d bytes changed by a one-bit record edit", i, diff)
				}
				u.SetSystemRecord(orig)
			}
			if out, err := f.MarshalBinary(); err != nil || !bytes.Equal(out, data) {
				t.Errorf("not byte-identical after restoring the records (%v)", err)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
