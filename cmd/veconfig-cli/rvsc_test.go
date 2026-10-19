package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bolchisb/victron-veconfigure-ported/internal/rvsctest"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/rvsc"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
)

// rvscSpec is a synthetic one-unit file with the settings of the
// simulator's device 0 at their defaults, plus an invented grid-code
// setting 81.
func rvscSpec() rvsctest.File {
	f := rvsctest.FromProfile(simulator.Synthetic12V230(), 192, 0x0A0B0C0D, "ZZ0001TEST")
	f.Info[81] = mk2.SettingInfo{Sc: 1, Max: 20}
	f.Units[0].Settings[81] = 3
	return f
}

func writeSpec(t *testing.T, name string, spec rvsctest.File) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, spec.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func fileSetting(t *testing.T, path string, id int) uint16 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rvsc.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, err := f.Units()
	if err != nil {
		t.Fatal(err)
	}
	v, err := units[0].Setting(id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRVSCShow(t *testing.T) {
	p := writeSpec(t, "a.rvsc", rvscSpec())
	code, out, errOut := cli(t, "rvsc-show", p)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "tool:     0.01", "info:     firmware 2699481, 192 settings", "units:    1",
		`unit "ZZ0001TEST" (long ID 0x0A0B0C0D)`, "firmware 2699481, index 0", "device form",
		"UBatAbsorption", "14.4 V", "1440", "Grid code [grid code]", "60Hz")
}

func TestRVSCSet(t *testing.T) {
	in := writeSpec(t, "a.rvsc", rvscSpec())
	out := filepath.Join(t.TempDir(), "b.rvsc")

	code, stdout, errOut := cli(t, "rvsc-set", in, "2", "14.6", "-o", out)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, stdout, "setting 2 (UBatAbsorption): 14.4 V -> 14.6 V", "wrote "+out)
	if v := fileSetting(t, out, 2); v != 1460 {
		t.Errorf("setting 2 = %d in output", v)
	}
	if v := fileSetting(t, in, 2); v != 1440 {
		t.Errorf("input changed: setting 2 = %d", v)
	}

	// Raw and flag forms.
	if code, _, errOut := cli(t, "rvsc-set", in, "--raw", "2", "0x5B4", "-o", out); code != exitOK || fileSetting(t, out, 2) != 1460 {
		t.Errorf("--raw: exit %d: %s", code, errOut)
	}
	if code, _, errOut := cli(t, "rvsc-set", in, "--flag", "2", "1", "-o", out); code != exitOK || fileSetting(t, out, 0)&4 == 0 {
		t.Errorf("--flag: exit %d: %s", code, errOut)
	}
}

func TestRVSCSetRefusals(t *testing.T) {
	in := writeSpec(t, "a.rvsc", rvscSpec())
	out := filepath.Join(t.TempDir(), "b.rvsc")
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"2", "14.6"}, exitUsage, "no output file"},
		{[]string{"2", "30", "-o", out}, exitError, "outside the allowed range"},
		{[]string{"7", "1", "-o", out}, exitError, "not supported by unit"},
		{[]string{"3", "15", "-o", out}, exitError, "validation failed"},
		{[]string{"81", "4", "-o", out}, exitError, "refusing to change grid-code setting(s) 81"},
	} {
		code, _, errOut := cli(t, append([]string{"rvsc-set", in}, tc.args...)...)
		if code != tc.code {
			t.Errorf("%v: exit %d, want %d: %s", tc.args, code, tc.code, errOut)
		}
		wantContains(t, errOut, tc.want)
		if _, err := os.Stat(out); err == nil {
			t.Fatalf("%v: output written despite refusal", tc.args)
		}
	}
	// The overrides.
	if code, _, errOut := cli(t, "rvsc-set", in, "3", "15", "--force", "-o", out); code != exitOK {
		t.Errorf("--force: exit %d: %s", code, errOut)
	}
	if code, _, errOut := cli(t, "rvsc-set", in, "81", "4", "--allow-grid-code", "-o", out); code != exitOK || fileSetting(t, out, 81) != 4 {
		t.Errorf("--allow-grid-code: exit %d: %s", code, errOut)
	}
}

func TestRVSCUnitSelection(t *testing.T) {
	spec := rvscSpec()
	second := spec.Units[0]
	second.LongID, second.Serial, second.Index = 0x01020304, "ZZ0002TEST", 1
	second.Settings = append([]uint16(nil), second.Settings...)
	second.Settings[2] = 1450
	spec.Units = append(spec.Units, second)
	in := writeSpec(t, "two.rvms", spec)
	out := filepath.Join(t.TempDir(), "b.rvms")

	code, _, errOut := cli(t, "rvsc-set", in, "2", "14.6", "-o", out)
	if code != exitError {
		t.Errorf("no --unit: exit %d", code)
	}
	wantContains(t, errOut, "has 2 units; choose one with --unit", `"ZZ0002TEST" (long ID 0x01020304)`)

	for _, sel := range []string{"ZZ0002TEST", "0x01020304"} {
		if code, _, errOut := cli(t, "rvsc-set", in, "--unit", sel, "2", "14.6", "-o", out); code != exitOK {
			t.Errorf("--unit %s: exit %d: %s", sel, code, errOut)
		}
	}
	code, out2, _ := cli(t, "rvsc-show", in, "--unit", "ZZ0002TEST")
	if code != exitOK {
		t.Fatal(code)
	}
	wantContains(t, out2, "units:    2", "14.5 V")
}

func TestRVSCDiffFiles(t *testing.T) {
	a := writeSpec(t, "a.rvsc", rvscSpec())
	spec := rvscSpec()
	spec.Units[0].Settings[2] = 1460
	spec.Units[0].Settings[0] |= 4
	b := writeSpec(t, "b.rvsc", spec)

	code, out, errOut := cli(t, "rvsc-diff", a, b)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "2 differences", "flag 2", "60Hz", "off", "on", "setting 2", "UBatAbsorption", "14.4 V", "14.6 V")

	code, out, _ = cli(t, "rvsc-diff", a, a)
	if code != exitOK {
		t.Fatal(code)
	}
	wantContains(t, out, "no differences")
}

func TestRVSCDiffDevice(t *testing.T) {
	orig := newSimulator
	t.Cleanup(func() { newSimulator = orig })
	newSimulator = func() *simulator.Simulator {
		return simulator.New(simulator.Config{Devices: []simulator.Device{{Address: 0, Profile: simulator.Synthetic12V230()}}})
	}
	spec := rvscSpec()
	spec.Units[0].Settings[2] = 1460
	p := writeSpec(t, "a.rvsc", spec)

	code, out, errOut := cli(t, args("rvsc-diff", p)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "ok       firmware: file unit 2699481, device 2699481", "ok       setting info",
		"ok       unit count: file 1, bus 1", "not compared: serial number and long ID",
		"result: the file matches the device", "1 differences", "UBatAbsorption", "14.6 V", "14.4 V",
		"settings file only (not read from the device)")
}

func TestRVSCDiffDeviceMismatch(t *testing.T) {
	p := writeSpec(t, "a.rvsc", rvscSpec())
	code, out, errOut := cli(t, args("rvsc-diff", p, "--address", "1")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "MISMATCH firmware: file unit 2699481, device 2799482", "MISMATCH setting info",
		"MISMATCH unit count: file 1, bus 2", "result: the file does NOT match the device")
}

func TestRVSCDiffUsage(t *testing.T) {
	p := writeSpec(t, "a.rvsc", rvscSpec())
	if code, _, errOut := cli(t, "rvsc-diff", p); code != exitUsage {
		t.Errorf("one file without interface: exit %d: %s", code, errOut)
	}
	if code, _, _ := cli(t, "rvsc-diff"); code != exitUsage {
		t.Errorf("no args: exit %d", code)
	}
}

// oneDevice makes --simulator use a bus with only device 0, with profile p.
func oneDevice(t *testing.T, p simulator.Profile) {
	t.Helper()
	orig := newSimulator
	t.Cleanup(func() { newSimulator = orig })
	newSimulator = func() *simulator.Simulator {
		return simulator.New(simulator.Config{Devices: []simulator.Device{{Address: 0, Profile: p}}})
	}
}

func TestRVSCApply(t *testing.T) {
	oneDevice(t, simulator.Synthetic12V230())
	spec := rvscSpec()
	spec.Units[0].Settings[2] = 1460
	spec.Units[0].Settings[0] |= 1 << 4 // flag 4
	p := writeSpec(t, "a.rvsc", spec)

	code, out, errOut := cli(t, args("rvsc-apply", "--verbose", p)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "ok       firmware", "ok       setting info", "ok       unit count: file 1, bus 1",
		"2 differences", "UBatAbsorption          14.6 V  14.4 V",
		"planned   setting 0 (Flags0): 0x0080 -> 0x0090", "flag 4 (DoNotStopAfter10HrBulk): off -> on",
		"planned   setting 2 (UBatAbsorption): 14.4 V (raw 1440) -> 14.6 V (raw 1460)",
		"dry run: nothing was written", "assistant area was not applied")
	if n := writeFrames(errOut); n != 0 {
		t.Errorf("%d writes in a dry run", n)
	}

	code, out, errOut = cli(t, args("rvsc-apply", "--yes", "--verbose", p)...)
	if code != exitOK {
		t.Fatalf("--yes: exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "verified  setting 0 (Flags0)", "verified  setting 2 (UBatAbsorption)", "all writes verified")
	if n := writeFrames(errOut); n != 2 {
		t.Errorf("%d writes, want 2", n)
	}
}

func TestRVSCApplyRefusesMismatch(t *testing.T) {
	p := writeSpec(t, "a.rvsc", rvscSpec())
	for _, tc := range []struct {
		args []string
		want []string
	}{
		// Device 1 has other firmware and limits.
		{[]string{"--address", "1"}, []string{"refusing to apply the file", "firmware: file unit 2699481, device 2799482"}},
		// Two devices on the bus, one unit in the file.
		{nil, []string{"refusing to apply the file", "unit count: file 1, bus 2"}},
	} {
		a := args(append([]string{"rvsc-apply", "--yes", "--verbose", p}, tc.args...)...)
		code, out, errOut := cli(t, a...)
		if code != exitError {
			t.Errorf("%v: exit %d, want %d\n%s", tc.args, code, exitError, out)
		}
		wantContains(t, errOut, tc.want...)
		wantContains(t, out, "MISMATCH")
		if n := writeFrames(errOut); n != 0 {
			t.Errorf("%v: %d writes despite the refusal", tc.args, n)
		}
	}
	// --single-unit accepts one unit for one of several devices.
	code, out, errOut := cli(t, args("rvsc-apply", "--single-unit", p)...)
	if code != exitOK {
		t.Errorf("--single-unit: exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "single unit", "no changes")
}

func TestRVSCApplyGridCode(t *testing.T) {
	prof := simulator.Synthetic12V230()
	prof.Settings = append(prof.Settings, simulator.Setting{ID: 81, Name: "invented", Sc: 1, Max: 20}) // value 0
	oneDevice(t, prof)
	spec := rvscSpec() // the same info for setting 81
	spec.Units[0].Settings[81] = 4
	p := writeSpec(t, "a.rvsc", spec)

	code, _, errOut := cli(t, args("rvsc-apply", "--yes", "--verbose", p)...)
	if code != exitError || writeFrames(errOut) != 0 {
		t.Errorf("exit %d, %d writes", code, writeFrames(errOut))
	}
	wantContains(t, errOut, "refusing to change grid-code setting(s) 81")

	code, out, errOut := cli(t, args("rvsc-apply", "--allow-grid-code", p)...)
	if code != exitOK {
		t.Fatalf("--allow-grid-code: exit %d: %s", code, errOut)
	}
	wantContains(t, out, "planned   setting 81 (Grid code): 0 (raw 0) -> 4 (raw 4)")
}
