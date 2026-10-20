package system

import (
	"go/parser"
	"go/token"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRecordDecode(t *testing.T) {
	type fields struct {
		red, red1       byte
		leader          bool
		mask            uint32
		angle, ac       int
		float, ret      bool
		phase, phases   int
		assist, second  bool
		marker, grouped bool
		addr            byte
	}
	for _, tc := range []struct {
		rec  string
		want fields
	}{
		{"01 01 01 00 00 00 00 E0 00", fields{1, 1, true, 1, 0, 0, false, false, 0, 1, false, true, true, true, 0}},
		{"FF FF 3F 00 00 00 08 FA 05", fields{0xFF, 0xFF, false, 0x3F, 240, 0, false, false, 2, 3, true, true, true, true, 5}},
		{"01 01 03 00 00 00 C6 F5 01", fields{1, 1, true, 3, 180, 0, true, true, 1, 2, true, true, true, true, 1}},
		{"01 01 03 00 00 00 86 E4 01", fields{1, 1, true, 3, 180, 0, true, false, 0, 2, false, true, true, true, 1}},
		{"0C 0C 4B 92 24 49 00 F0 00", fields{12, 12, true, 0x4924924B, 0, 0, false, false, 0, 1, true, true, true, true, 0}},
		{"FF FF 49 00 00 00 10 F0 06", fields{0xFF, 0xFF, false, 0x49, 0, 1, false, false, 0, 1, true, true, true, true, 6}},
		{"01 01 01 00 00 00 00 D0 00", fields{1, 1, true, 1, 0, 0, false, false, 0, 1, true, false, true, true, 0}},
		{"02 03 00 00 00 80 34 3B 1F", fields{2, 3, true, 0x80000000, 120, 3, false, false, 3, 3, true, true, false, false, 31}},
	} {
		r := hexRecord(t, tc.rec)
		got := fields{r.Redundancy(), r.RedundancyCopy(), r.Leader(), r.PresenceMask(), r.PhaseAngle(), r.ACInput(),
			r.Floating(), r.FloatingReturn(), r.Phase(), r.Phases(), r.AssistantResetFlag(), r.SecondInputGrouped(),
			r.Marker(), r.Grouped(), r.ShortAddress()}
		if got != tc.want {
			t.Errorf("%s:\n got %+v\nwant %+v", tc.rec, got, tc.want)
		}
	}
}

// encode builds a record from another one's decoded fields with the
// setters only.
func encode(r Record) Record {
	var e Record
	e.SetRedundancy(r.Redundancy())
	e.SetPresenceMask(r.PresenceMask())
	e.SetPhaseAngle(r.PhaseAngle())
	e.SetACInput(r.ACInput())
	e.SetFloatingReturn(r.FloatingReturn())
	e.SetFloating(r.Floating())
	e.SetPhase(r.Phase())
	e.SetPhases(r.Phases())
	e.SetAssistantResetFlag(r.AssistantResetFlag())
	e.SetSecondInputGrouped(r.SecondInputGrouped())
	e.SetMarker(r.Marker())
	e.SetGrouped(r.Grouped())
	e.SetShortAddress(r.ShortAddress())
	return e
}

// TestRecordRoundTrip: decode → encode reproduces every worked example
// record and any record whose bytes 0 and 1 are equal.
func TestRecordRoundTrip(t *testing.T) {
	for _, ex := range workedExamples() {
		for _, s := range ex.want {
			if r := hexRecord(t, s); encode(r) != r {
				t.Errorf("%s: round trip gives %s", s, encode(r).Hex())
			}
		}
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for range 1000 {
		var r Record
		for i := range r {
			r[i] = byte(rng.UintN(256))
		}
		r[1] = r[0]
		if encode(r) != r {
			t.Fatalf("%s: round trip gives %s", r.Hex(), encode(r).Hex())
		}
	}
}

// TestRecordSettersClearBits: setters clear what they set.
func TestRecordSettersClearBits(t *testing.T) {
	r := Record{0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0}
	r.SetFloating(false)
	r.SetFloatingReturn(false)
	r.SetACInput(0)
	r.SetPhaseAngle(0)
	if r[6] != 0 {
		t.Errorf("byte 6 = %02X", r[6])
	}
	r.SetPhase(0)
	r.SetPhases(1)
	r.SetAssistantResetFlag(false)
	r.SetSecondInputGrouped(false)
	r.SetMarker(false)
	r.SetGrouped(false)
	if r[7] != 0 {
		t.Errorf("byte 7 = %02X", r[7])
	}
}

func TestFactoryDefault(t *testing.T) {
	var withFlag Record
	withFlag.SetAssistantResetFlag(true)
	if got, want := FactoryDefault(withFlag), hexRecord(t, "01 01 01 00 00 00 00 F0 00"); got != want {
		t.Errorf("FactoryDefault(bit 4 set) = %s", got.Hex())
	}
	if got, want := FactoryDefault(Record{}), hexRecord(t, "01 01 01 00 00 00 00 E0 00"); got != want {
		t.Errorf("FactoryDefault(bit 4 clear) = %s", got.Hex())
	}
}

func TestRecordString(t *testing.T) {
	for rec, want := range map[string]string{
		"02 02 09 00 00 00 00 F0 00": "02 02 09 00 00 00 00 F0 00 (L1 of 1, AC1, 0°, addr 0, leads 2, mask 00000009, grouped, 2nd grouped, reset-assistants, marker)",
		"FF FF 03 00 00 00 C6 E5 01": "FF FF 03 00 00 00 C6 E5 01 (L2 of 2, AC1, 180° floating with return, addr 1, mask 00000003, grouped, 2nd grouped, marker)",
		"FF FF 03 00 00 00 94 05 01": "FF FF 03 00 00 00 94 05 01 (L2 of 2, AC2, 120° floating, addr 1, mask 00000003)",
	} {
		if got := hexRecord(t, rec).String(); got != want {
			t.Errorf("String()\n got %s\nwant %s", got, want)
		}
	}
}

func TestUnitDerived(t *testing.T) {
	u := &Unit{VersionWord: 0x12000000 | 2623456, Letter: 1}
	if u.Firmware() != 2623456 || u.Family() != 2623 || u.Revision() != 456 || u.SubVersion() != 0x12 {
		t.Errorf("firmware %d family %d revision %d sub %02X", u.Firmware(), u.Family(), u.Revision(), u.SubVersion())
	}
	for _, tc := range []struct {
		fw     uint32
		letter byte
		want   string
	}{
		{2623456, 1, "2623_B"},
		{2000251, 0, "2700_A"}, // revision > 250 and family < 2100: +700 for display
		{2000250, 0, "2000_A"},
		{2100251, 25, "2100_Z"},
		{2623456, LetterUnknown, "2623_?"},
		{2623456, 26, "2623_?"},
	} {
		u := &Unit{VersionWord: tc.fw, Letter: tc.letter}
		if got := u.CompatibilityID(); got != tc.want {
			t.Errorf("CompatibilityID(%d, %d) = %s, want %s", tc.fw, tc.letter, got, tc.want)
		}
	}
	for rev, want := range map[uint32]bool{0: true, 1: false, 204: false, 205: true, 999: true} {
		u := &Unit{VersionWord: 2623000 + rev}
		if u.AssistantFlagApplies() != want {
			t.Errorf("AssistantFlagApplies(revision %d) = %v", rev, !want)
		}
	}
	for v, want := range map[uint32]bool{2060209: true, 2062100: true, 2071000: true, 2072209: true, 2072210: false, 2061100: false, 2623100: false} {
		u := &Unit{VersionWord: v}
		if u.SplitPhaseModel() != want {
			t.Errorf("SplitPhaseModel(%d) = %v", v, !want)
		}
	}
	for _, tc := range []struct {
		u    Unit
		want bool
	}{
		{Unit{}, false},
		{Unit{ExternalBF: true}, true},
		{Unit{VersionWord: 0xC7000000 | 2623456}, true},
		{Unit{VersionWord: 0xC6000000 | 2623456}, false},
	} {
		if tc.u.ExternalBFSystem() != tc.want {
			t.Errorf("ExternalBFSystem(%+v) = %v", tc.u, !tc.want)
		}
	}
	kinds := map[byte][4]bool{ // dual, inverter, no config, MP2
		0x02: {}, 0x20: {}, 0x2E: {},
		0x03: {true, false, false, false},
		0x04: {false, true, false, false},
		0x23: {false, false, true, false},
		0x2F: {false, false, false, true},
	}
	for rc, want := range kinds {
		u := &Unit{ResponseChar: rc}
		if got := [4]bool{u.DualInput(), u.Inverter(), u.NoConfig(), u.MP2Family()}; got != want {
			t.Errorf("response char %02X: kinds %v, want %v", rc, got, want)
		}
	}
	if got := (&Unit{LongID: 0xAB}).Label(); got != "000000AB" {
		t.Errorf("Label() = %s", got)
	}
	if got := (&Unit{Name: "north"}).Label(); got != "north" {
		t.Errorf("Label() = %s", got)
	}
}

// TestNoIO: the package imports nothing that does I/O, and does not depend
// on the protocol package.
func TestNoIO(t *testing.T) {
	allowed := map[string]bool{
		"cmp": true, "encoding/binary": true, "errors": true, "fmt": true,
		"math/bits": true, "slices": true, "strconv": true, "strings": true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !allowed[p] {
				t.Errorf("%s imports %s", f, p)
			}
		}
	}
}
