package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bolchisb/openveconfig/internal/vfftest"
	fw "github.com/bolchisb/openveconfig/pkg/firmware"
)

func TestFirmwareInfo(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.vff")
	os.WriteFile(good, vfftest.File7(), 0o644)
	code, out, errOut := cli(t, "firmware", "info", good)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "version   2623457", "format    7", "parts     3 in 2 group(s)", "all checks passed",
		"part 2: Bootblock", "records: 0 ML, 2 EEPROM, 3 code; descriptor 33 bytes", "Multi Test 12V", "Foo", "bar")

	bad := vfftest.File7()
	bad[len(bad)-5]++
	corrupt := filepath.Join(dir, "bad.vff")
	os.WriteFile(corrupt, bad, 0o644)
	code, _, errOut = cli(t, "firmware", "info", corrupt)
	if code != exitError {
		t.Errorf("corrupt file: exit %d", code)
	}
	wantContains(t, errOut, "part 3")

	for _, a := range [][]string{{"firmware"}, {"firmware", "flash", good}, {"firmware", "info"}} {
		if code, _, _ := cli(t, a...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", a, code, exitUsage)
		}
	}
}

// flashFile writes a synthetic .vff whose firmware number matches the
// simulator's device 0 (2699481) so pre-flight passes.
func flashFile(t *testing.T) string {
	t.Helper()
	data := vfftest.Part3(vfftest.Spec3{Format: 7, Meta: "Part=Main,Version=2699481,CL=3,EL=0",
		Desc: []byte("C"), CL: vfftest.Records(3, 80, 5)})
	p := filepath.Join(t.TempDir(), "fw.vff")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFirmwareFlashSimulator(t *testing.T) {
	var sim *fw.BootSim
	orig := newReprogramSim
	t.Cleanup(func() { newReprogramSim = orig })
	newReprogramSim = func() reprogramTransport {
		sim = fw.NewBootSim(fw.BootConfig{})
		return sim
	}
	code, out, errOut := cli(t, args("firmware", "flash", flashFile(t))...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "pre-flight OK: device firmware 2699481", "erasing firmware",
		"programmed", "firmware flashed successfully")
	if n := len(sim.Written()); n == 0 {
		t.Errorf("no blocks written")
	}
}

func TestFirmwareFlashRefusesRealPort(t *testing.T) {
	// A real --port without the opt-in flag is refused before any device is
	// touched.
	code, out, errOut := cli(t, "firmware", "flash", "--port", "COM-NOPE", flashFile(t))
	if code != exitError {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	wantContains(t, errOut, "not yet validated on real hardware")
}

func TestFirmwareFlashMismatch(t *testing.T) {
	// A file for another device family is refused after pre-flight.
	data := vfftest.Part3(vfftest.Spec3{Format: 7, Meta: "Part=Main,Version=2101000,CL=1,EL=0",
		Desc: []byte("C"), CL: vfftest.Records(1, 64, 5)})
	p := filepath.Join(t.TempDir(), "other.vff")
	os.WriteFile(p, data, 0o644)
	code, _, errOut := cli(t, args("firmware", "flash", p)...)
	if code != exitError {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, errOut, "does not match the device family")
}
