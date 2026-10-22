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
	// The bytes after the version in the firmware-version reply, which the
	// file keeps: they give ConnectionBoardWithPIC, which some assistants
	// read. The chosen cVLLVersion and cVLLSystem above still win, being
	// in Extra.
	d.VersionExtra = u.VersionExtra
	var err error
	if d.PackageVersion, d.DownloadedBy, err = vap.ReadIndex(dir); err != nil {
		t.Logf("package index: %v", err)
	}
	info, err := f.SettingInfo()
	if err != nil {
		t.Fatal(err)
	}
	for id, raw := range u.Settings() {
		name, ok := assistant.SettingName(id)
		if !ok || id >= len(info.Settings) {
			continue
		}
		si := info.Settings[id]
		d.Settings = append(d.Settings, assistant.DeviceSetting{
			ID: id, Name: name, Scale: int64(si.Sc), Offset: int64(si.Offset),
			Default: int64(si.Default), Min: int64(si.Min), Max: int64(si.Max), Value: int64(raw),
		})
	}
	// The flags, which the sources read as <name>_VEC just as a setting's
	// value is read: flag n is bit n%16 of the words below, which is how
	// the user interface fills them from a loaded model.
	flagWords := [4]int{0, 1, 60, 61}
	for n := range assistant.FlagCount {
		name, ok := assistant.FlagName(n)
		if !ok {
			continue
		}
		w := flagWords[n/16]
		raw, err := u.Setting(w)
		if err != nil || w >= len(info.Settings) {
			continue
		}
		bit := uint(n % 16)
		d.Flags = append(d.Flags, assistant.DeviceFlag{Name: name,
			Default: info.Settings[w].Default>>bit&1 == 1, Value: raw>>bit&1 == 1})
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
	if len(ids) == 0 {
		t.Skip("the backup in ASSISTANT_RVSC holds no assistants")
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
		// A step asks for something when it has buttons or fields; one
		// with labels but neither — the welcome page, whose label is the
		// two ends of a link its text puts in its places — takes none of
		// the wizard values. A step still wants one value per label, so
		// the labels that are not asked for go back as they came.
		var ans []int64
		if st.Type != 'E' || len(st.Formats) > 0 {
			if next == len(essAnswers) {
				t.Fatalf("more input steps than the wizard values")
			}
			ans, next = essAnswers[next], next+1
		}
		vals := make([]asm.Value, len(st.Labels))
		k := 0
		for i := range st.Labels {
			fi := i
			if st.Type != 'E' {
				fi = i - 1
			}
			if (st.Type != 'E' && i == 0 || fi >= 0 && fi < len(st.Formats)) && k < len(ans) {
				vals[i], k = asm.IntVal(ans[k]), k+1
				continue
			}
			if i < len(st.Values) {
				vals[i] = st.Values[i]
			}
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
	// As runs, so that a shifted program reads as one run and a handful
	// of wrong values as a handful.
	runs := 0
	for i := 0; i < len(diffs) && runs < 20; runs++ {
		j := i
		for j+1 < len(diffs) && diffs[j+1] == diffs[j]+1 {
			j++
		}
		n := diffs[j] - diffs[i] + 1
		t.Logf("  %#05x..%#05x (%d): built % x | unit % x", diffs[i], diffs[j], n,
			im.Bytes[diffs[i]:diffs[i]+min(n, 8)], img[diffs[i]:diffs[i]+min(n, 8)])
		i = j + 1
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
		// Only a step that asks for something holds a wizard value: one
		// with labels but no buttons and no fields carries what its text
		// puts in its places, such as the two ends of a link.
		if st.Source != c.Users[0].Name || st.Type == 'E' && len(st.Formats) == 0 {
			continue
		}
		got := ans[st.Key]
		t.Logf("step %s: %v", st.Name, got)
		if next < len(essAnswers) {
			want := essAnswers[next]
			// The values of a step are its fields followed by whatever
			// its text uses; only the fields are wizard values.
			ok := len(got) >= len(want)
			for i := 0; ok && i < len(want); i++ {
				ok = got[i].I == want[i]
			}
			if !ok {
				t.Errorf("step %s: read back %v, want %v", st.Name, got, want)
			}
		}
		next++
	}
	if next != len(essAnswers) {
		t.Errorf("%d steps asked for values, %d wizard values", next, len(essAnswers))
	}
	// Whether the code the read-back assembles is the unit's byte for
	// byte is the same question TestRealESS asks, and is open there.
}
