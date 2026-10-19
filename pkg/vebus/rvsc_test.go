package vebus

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bolchisb/victron-veconfigure-ported/internal/rvsctest"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/rvsc"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
)

// testSpec is a synthetic one-unit file with the settings of the
// simulator's device 0, plus invented grid-code settings 81 and 130.
func testSpec() rvsctest.File {
	f := rvsctest.FromProfile(simulator.Synthetic12V230(), 192, 0x0A0B0C0D, "ZZ0001TEST")
	f.Info[81] = mk2.SettingInfo{Sc: 1, Max: 20}
	f.Info[130] = mk2.SettingInfo{Sc: 1, Max: 1000}
	f.Units[0].Settings[81] = 3
	f.Units[0].Settings[130] = 500
	return f
}

func parseSpec(t *testing.T, spec rvsctest.File) (*rvsc.File, *rvsc.SettingInfoTable, []*rvsc.Unit) {
	t.Helper()
	f, err := rvsc.Parse(spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.SettingInfo()
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	return f, info, units
}

func TestFromRVSC(t *testing.T) {
	_, info, units := parseSpec(t, testSpec())
	d, err := FromRVSC(info, units[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Firmware != 2699481 {
		t.Errorf("Firmware = %d", d.Firmware)
	}
	want := len(simulator.Synthetic12V230().Settings) + 2
	if got := len(d.SettingIDs()); got != want {
		t.Errorf("%d settings, want %d (Sc 0 left out)", got, want)
	}
	if v, err := d.Display(SettingID(2)); err != nil || v != 14.4 {
		t.Errorf("setting 2 = %v, %v; want 14.4", v, err)
	}
	if got := d.ValueText(SettingID(2)); got != "14.4 V" {
		t.Errorf("ValueText = %q", got)
	}
	if len(d.ChangedIDs()) != 0 {
		t.Errorf("fresh model has changes: %v", d.ChangedIDs())
	}
	if err := d.SetDisplay(SettingID(2), 14.6); err != nil {
		t.Fatal(err)
	}
	if ids := d.ChangedIDs(); len(ids) != 1 || ids[0] != SettingID(2) {
		t.Errorf("ChangedIDs = %v", ids)
	}
}

func TestFromRVSCCountMismatch(t *testing.T) {
	spec := testSpec()
	spec.Info = spec.Info[:100]
	_, info, units := parseSpec(t, spec)
	if _, err := FromRVSC(info, units[0], nil); err == nil {
		t.Fatal("info/value count mismatch accepted")
	}
}

func TestSaveRVSCByteIdentical(t *testing.T) {
	spec := testSpec()
	orig := spec.Bytes()
	f, info, units := parseSpec(t, spec)
	d, err := FromRVSC(info, units[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetDisplay(SettingID(2), 14.6); err != nil {
		t.Fatal(err)
	}
	if err := d.SetFlag(2, true); err != nil { // 60 Hz, bit 2 of setting 0
		t.Fatal(err)
	}
	changed, err := SaveRVSC(d, units[0], SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 || changed[0] != 0 || changed[1] != 2 {
		t.Errorf("changed = %v", changed)
	}
	out, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	// Built independently: the same file with the two values changed.
	spec.Units[0].Settings[2] = 1460
	spec.Units[0].Settings[0] |= 1 << 2
	if want := spec.Bytes(); !bytes.Equal(out, want) {
		t.Fatal("saved file differs from the expected bytes")
	}
	// Only the two values (one byte each here) and the section checksum
	// (at most 4 bytes) differ from the original.
	n := 0
	for i := range orig {
		if orig[i] != out[i] {
			n++
		}
	}
	if len(out) != len(orig) || n == 0 || n > 2+4 {
		t.Errorf("%d bytes differ, length %d -> %d", n, len(orig), len(out))
	}
	// Saving again changes nothing.
	if changed, err := SaveRVSC(d, units[0], SaveOptions{}); err != nil || len(changed) != 0 {
		t.Errorf("second save: %v, %v", changed, err)
	}
}

func TestSaveRVSCRefusals(t *testing.T) {
	load := func() (*rvsc.File, *rvsc.Unit, *Device, []byte) {
		f, info, units := parseSpec(t, testSpec())
		d, err := FromRVSC(info, units[0], nil)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := f.MarshalBinary()
		return f, units[0], d, before
	}
	unchanged := func(f *rvsc.File, before []byte) {
		t.Helper()
		if after, _ := f.MarshalBinary(); !bytes.Equal(after, before) {
			t.Error("refused save changed the file")
		}
	}

	// Grid code: refused without the override, for 81 and the 128-191 block.
	for _, id := range []uint16{81, 130} {
		f, u, d, before := load()
		if err := d.SetRaw(SettingID(id), 1); err != nil {
			t.Fatal(err)
		}
		_, err := SaveRVSC(d, u, SaveOptions{Override: true})
		var ge *GridCodeError
		if !errors.As(err, &ge) || len(ge.IDs) != 1 || ge.IDs[0] != id {
			t.Errorf("setting %d: err = %v, want GridCodeError", id, err)
		}
		unchanged(f, before)
		if _, err := SaveRVSC(d, u, SaveOptions{AllowGridCode: true}); err != nil {
			t.Errorf("setting %d with AllowGridCode: %v", id, err)
		}
		if v, _ := u.Setting(int(id)); v != 1 {
			t.Errorf("setting %d = %d after allowed save", id, v)
		}
	}

	// Validation: float above absorption is an error for a changed value.
	f, u, d, before := load()
	if err := d.SetDisplay(SettingID(3), 15); err != nil {
		t.Fatal(err)
	}
	var ve *ValidationError
	if _, err := SaveRVSC(d, u, SaveOptions{}); !errors.As(err, &ve) {
		t.Errorf("err = %v, want ValidationError", err)
	}
	unchanged(f, before)
	if _, err := SaveRVSC(d, u, SaveOptions{Override: true}); err != nil {
		t.Errorf("with Override: %v", err)
	}

	// A model of another firmware is refused.
	f, u, d, before = load()
	d.Firmware++
	if _, err := SaveRVSC(d, u, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "firmware") {
		t.Errorf("err = %v, want firmware mismatch", err)
	}
	unchanged(f, before)
}

func TestGridCodeSetting(t *testing.T) {
	for id, want := range map[uint16]bool{80: false, 81: true, 82: false, 127: false, 128: true, 191: true, 192: false} {
		if GridCodeSetting(id) != want {
			t.Errorf("GridCodeSetting(%d) = %v", id, !want)
		}
	}
}

func TestMatchRVSC(t *testing.T) {
	_, info, units := parseSpec(t, testSpec())
	u := units[0]

	m := MatchRVSC(info, u, loadSim(t, 0))
	if !m.OK() || m.Err() != nil {
		t.Fatalf("device 0 does not match:\n%s", m)
	}
	rep := m.String()
	for _, w := range []string{"ok       firmware: file unit 2699481, device 2699481", "not compared: serial number and long ID"} {
		if !strings.Contains(rep, w) {
			t.Errorf("report lacks %q:\n%s", w, rep)
		}
	}

	m = MatchRVSC(info, u, loadSim(t, 1))
	err := m.Err()
	if m.OK() || !errors.Is(err, ErrMismatch) || !strings.Contains(err.Error(), "firmware: file unit 2699481, device 2799482") ||
		!strings.Contains(err.Error(), "setting info") {
		t.Errorf("device 1: err = %v\n%s", err, m)
	}

	// Same firmware, one limit changed in the file.
	spec := testSpec()
	spec.Info[4].Max = 50
	_, info2, units2 := parseSpec(t, spec)
	m = MatchRVSC(info2, units2[0], loadSim(t, 0))
	if err := m.Err(); err == nil || !strings.Contains(err.Error(), "4 (file Sc 1 Offset 0 Default 70 Min 0 Max 50") {
		t.Errorf("err = %v", err)
	}

	m = MatchRVSC(info, u, loadSim(t, 0))
	m.CheckUnitCount(1, 2, false)
	if m.OK() {
		t.Error("unit count 1 vs 2 accepted")
	}
	m = MatchRVSC(info, u, loadSim(t, 0))
	m.CheckUnitCount(1, 2, true)
	if !m.OK() {
		t.Errorf("single-unit replace refused:\n%s", m)
	}
}

func TestDiffDevices(t *testing.T) {
	_, info, units := parseSpec(t, testSpec())
	a, _ := FromRVSC(info, units[0], nil)
	b, _ := FromRVSC(info, units[0], nil)
	if d := DiffDevices(a, b); len(d) != 0 {
		t.Fatalf("identical models differ: %v", d)
	}
	if err := b.SetDisplay(SettingID(2), 14.6); err != nil {
		t.Fatal(err)
	}
	if err := b.SetFlag(2, true); err != nil {
		t.Fatal(err)
	}
	b.AddSetting(200, mk2.SettingInfo{Sc: 1, Max: 9}, 4)
	got := DiffDevices(a, b)
	want := []Difference{
		{ID: FlagID(2), Name: a.Name(FlagID(2)), InA: true, InB: true, A: "off", B: "on"},
		{ID: SettingID(2), Name: a.Name(SettingID(2)), InA: true, InB: true, A: "14.4 V", B: "14.6 V"},
		{ID: SettingID(200), Name: "setting 200", InB: true, B: "4"},
	}
	if len(got) != len(want) {
		t.Fatalf("diff = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("diff[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRealFiles loads every file in $RVSC_TESTDATA, if set, into the model
// and saves it back unchanged. Real files are not part of the repository.
func TestRealFiles(t *testing.T) {
	dir := os.Getenv("RVSC_TESTDATA")
	if dir == "" {
		t.Skip("RVSC_TESTDATA not set")
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "*.rv[sm][sc]"))
	if len(paths) == 0 {
		t.Fatalf("no .rvsc/.rvms files in %s", dir)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		f, err := rvsc.Parse(data)
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		info, err := f.SettingInfo()
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		units, err := f.Units()
		if err != nil {
			t.Errorf("%s: %v", filepath.Base(p), err)
			continue
		}
		for _, u := range units {
			d, err := FromRVSC(info, u, nil)
			if err != nil {
				t.Errorf("%s: %v", filepath.Base(p), err)
				continue
			}
			if _, err := SaveRVSC(d, u, SaveOptions{}); err != nil {
				t.Errorf("%s unit %s: save unchanged: %v", filepath.Base(p), UnitLabel(u), err)
			}
		}
		if out, _ := f.MarshalBinary(); !bytes.Equal(out, data) {
			t.Errorf("%s: unchanged save is not byte-identical", filepath.Base(p))
		}
	}
}
