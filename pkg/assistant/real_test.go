package assistant_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
)

// essAnswers are the ESS wizard values of the unit whose backup
// ASSISTANT_RVSC names (a MultiPlus-II 48/10000/140-100, from its
// VEConfigure screenshots), raw: volts and hertz × 100, the battery system
// as the index of its button (the 5th of 6). Steps with no input take no
// entry.
var essAnswers = [][]int64{
	{4},                      // battery system: LiFePo4 with other type BMS
	{200},                    // capacity, Ah
	{5000},                   // sustain voltage 50.00 V
	{5200, 5000, 4920, 4800}, // dynamic cut-off 0.005C, 0.25C, 0.7C, 2C
	{120},                    // restart offset 1.20 V
	{1},                      // PV inverters on AC out: yes
	{4000},                   // restart PV below 40.00 V
	{5020, 5270, 5300},       // frequencies, Hz
	{5000, 5000},             // PV inverter power, PV panel power, W
}

// settingNames are the names the sources use for the unit's settings, as
// far as the spec gives them (addendum §8.4); the original's full table of
// names is not in it.
var settingNames = map[int]string{64: "eestBatteryCapacity", 81: "eecvGridCode"}

// essDevice describes the unit for the composer (spec addendum §8.4): the
// firmware version from the file, the settings with their metadata from
// the file, the package folder's index, and the values the spec gives.
// Choices made where the spec says UNKNOWN or INFERRED:
//   - cDeviceInfo = 1 (HasNTC, bit 5, unknown: no NTC assumed);
//   - cVLLVersion = 7, cVLLSystem = 0 (the version-reply bytes are
//     unknown; any version >= 7 with system 0 should give the same bytes);
//   - cRatedPower = 10000 (inferred from the product name);
//   - cMultiSubVersion is left undefined (unknown).
func essDevice(t *testing.T, dir string, f *rvsc.File, u *rvsc.Unit) assistant.Device {
	d := assistant.Device{
		MultiVersion:   int64(u.Firmware()),
		PNominal:       8300,
		DCCode:         24,
		ACVolts:        230,
		WinmonIDOffset: 4,
		Extra: map[string]asm.Value{
			"cDeviceInfo": asm.IntVal(1),
			"cVLLVersion": asm.IntVal(7),
			"cVLLSystem":  asm.IntVal(0),
			"cRatedPower": asm.IntVal(10000),
		},
	}
	var err error
	if d.PackageVersion, d.DownloadedBy, err = vap.ReadIndex(dir); err != nil {
		t.Logf("package index: %v", err)
	}
	info, err := f.SettingInfo()
	if err != nil {
		t.Fatal(err)
	}
	for id, raw := range u.Settings() {
		name, ok := settingNames[id]
		if !ok || id >= len(info.Settings) {
			continue
		}
		si := info.Settings[id]
		d.Settings = append(d.Settings, assistant.DeviceSetting{
			ID: id, Name: name, Scale: int64(si.Sc), Offset: int64(si.Offset),
			Default: int64(si.Default), Min: int64(si.Min), Max: int64(si.Max), Value: int64(raw),
		})
	}
	return d
}

// realImage returns the assistant image of the first unit in
// $ASSISTANT_RVSC and the composer for its assistants from
// $ASSISTANT_PACKAGES. Neither file is committed (they hold Victron's
// content and a user's configuration).
func realImage(t *testing.T) ([]byte, *assistant.Composer) {
	dir, file := os.Getenv("ASSISTANT_PACKAGES"), os.Getenv("ASSISTANT_RVSC")
	if dir == "" || file == "" {
		t.Skip("ASSISTANT_PACKAGES and ASSISTANT_RVSC not set")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rvsc.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil || len(units) == 0 {
		t.Fatalf("units: %v", err)
	}
	img, err := units[0].AssistantImage()
	if err != nil {
		t.Fatal(err)
	}
	l, err := assistant.ParseLayout(img)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image: tail %04X, program %d, used %d, free %d", l.IDs, l.Program, l.Used, l.Free)
	var ids []uint16
	for i := len(l.IDs) - 2; i >= 1; i-- { // the users, in program order
		ids = append(ids, l.IDs[i])
	}
	c, err := assistant.Load(dir, "en", ids...)
	if err != nil {
		t.Fatal(err)
	}
	c.Labels = assistant.Labels(essDevice(t, dir, f, units[0]))
	return img, c
}

// logErrors logs the errors of a failed assembly without the source's own
// texts: the labels it found unknown, and the position and kind of the
// other errors.
func logErrors(t *testing.T, run *wizard.Run, err error) {
	t.Helper()
	if run == nil || run.Result == nil {
		t.Logf("assembly failed: %v", err)
		return
	}
	var unknown []string
	seen := map[string]bool{}
	other := 0
	for _, e := range run.Result.Errors {
		var pe *asm.PosError
		var ue *asm.UserError
		switch {
		case errors.Is(e, asm.ErrUnknown):
			name := e.Error()[strings.LastIndex(e.Error(), " ")+1:]
			if !seen[name] {
				seen[name] = true
				unknown = append(unknown, name)
			}
		case other < 12 && errors.As(e, &ue) && errors.As(e, &pe):
			t.Logf("  %s: error raised by the source", pe.Pos)
			other++
		case other < 12:
			t.Logf("  %v", e)
			other++
		}
	}
	t.Logf("%d errors; unknown labels: %v", len(run.Result.Errors), unknown)
}

// TestRealESS compiles ESS with the unit's wizard values and compares the
// result with the unit's image, byte for byte.
func TestRealESS(t *testing.T) {
	img, c := realImage(t)
	p := c.Program()
	s := p.NewSession(nil)
	s.Edit = c.Users[0].Name
	st, err := s.Start()
	next := 0
	for err == nil && st != nil {
		t.Logf("step %s %s type %c, %d labels, offered %v", st.Name, st.Directive, st.Type, len(st.Labels), st.Values)
		var vals []asm.Value
		if len(st.Labels) > 0 {
			if next == len(essAnswers) {
				t.Fatalf("more input steps than the wizard values")
			}
			for _, v := range essAnswers[next] {
				vals = append(vals, asm.IntVal(v))
			}
			next++
		}
		st, err = s.Next(vals)
	}
	if err != nil {
		run, aerr := p.Assemble(wizard.Options{State: wizard.UserEntry, Answers: s.Answers(), Edit: s.Edit})
		logErrors(t, run, aerr)
		t.Fatalf("wizard: %v", err)
	}
	im, err := c.Build(s.Answers())
	if err != nil {
		run, aerr := p.Assemble(wizard.Options{State: wizard.Build, Answers: s.Answers()})
		logErrors(t, run, aerr)
		t.Fatalf("build: %v", err)
	}
	t.Logf("built: sizes %v, used %d, free %d", im.Sizes, im.Used, im.Free)
	if bytes.Equal(im.Bytes, img) {
		return
	}
	var diffs []int
	for i := range img {
		if im.Bytes[i] != img[i] {
			diffs = append(diffs, i)
		}
	}
	t.Errorf("%d bytes differ; first at %#x", len(diffs), diffs[0])
	for _, d := range diffs[:min(len(diffs), 16)] {
		t.Logf("  %#05x: built %02x, unit %02x", d, im.Bytes[d], img[d])
	}
}

// TestRealReadBack recovers the wizard values from the unit's image.
func TestRealReadBack(t *testing.T) {
	img, c := realImage(t)
	ans, run, err := c.ReadBack(img)
	if err != nil {
		logErrors(t, run, err)
		t.Fatalf("read-back: %v", err)
	}
	next := 0
	for _, st := range run.Steps {
		if st.Source != c.Users[0].Name || len(st.Labels) == 0 {
			continue
		}
		got := ans[st.Key]
		t.Logf("step %s: %v", st.Name, got)
		if next < len(essAnswers) {
			want := essAnswers[next]
			ok := len(got) == len(want)
			for i := 0; ok && i < len(want); i++ {
				ok = got[i].I == want[i]
			}
			if !ok {
				t.Errorf("step %s: read back %v, want %v", st.Name, got, want)
			}
		}
		next++
	}
	if !bytes.Equal(run.Code, img[:len(run.Code)]) {
		t.Errorf("read-back build differs from the image")
	}
}
