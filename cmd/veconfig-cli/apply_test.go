package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
)

// writeDumpFile writes a dump of device addr of the simulator, with the raw
// values in set changed, and returns its path.
func writeDumpFile(t *testing.T, addr string, set map[uint16]uint16) string {
	t.Helper()
	code, out, errOut := cli(t, args("dump", "--address", addr)...)
	if code != exitOK {
		t.Fatalf("dump: exit %d: %s", code, errOut)
	}
	var d dumpOutput
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err)
	}
	for i, s := range d.Settings {
		if v, ok := set[s.ID]; ok {
			d.Settings[i].Raw = v
		}
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, string(b))
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// writeFrames counts the CommandWriteData (0x34) frames in a --verbose log.
func writeFrames(log string) int {
	return len(regexp.MustCompile(`(?m) TX [0-9A-F]{2} FF 57 34 `).FindAllString(log, -1))
}

func TestApplyDryRun(t *testing.T) {
	file := writeDumpFile(t, "0", map[uint16]uint16{0: 0x0090, 2: 1460})
	code, out, errOut := cli(t, args("apply", "--verbose", file)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "device 0 (firmware 2699481):",
		"planned   setting 0 (Flags0): 0x0080 -> 0x0090",
		"flag 4 (DoNotStopAfter10HrBulk): off -> on",
		"planned   setting 2 (",
		"14.4 V (raw 1440) -> 14.6 V (raw 1460)",
		"dry run: nothing written")
	if n := writeFrames(errOut); n != 0 {
		t.Errorf("%d writes in a dry run", n)
	}
}

func TestApplyWritesOnlyChanged(t *testing.T) {
	file := writeDumpFile(t, "0", map[uint16]uint16{0: 0x0090, 2: 1460})
	code, out, errOut := cli(t, args("apply", "--yes", "--verbose", file)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "writing device 0:", "verified  setting 0 (Flags0): 0x0080 -> 0x0090", "verified  setting 2 (")
	if n := writeFrames(errOut); n != 2 {
		t.Errorf("%d writes, want 2", n)
	}

	// --all writes every setting of the device.
	code, out, errOut = cli(t, args("apply", "--yes", "--all", "--verbose", file)...)
	if code != exitOK {
		t.Fatalf("--all: exit %d: %s\n%s", code, out, errOut)
	}
	if n, want := writeFrames(errOut), len(simulator.Synthetic12V230().Settings); n != want {
		t.Errorf("--all: %d writes, want %d", n, want)
	}
}

func TestApplyMultiUnit(t *testing.T) {
	// Setting 64 has the same scale on both synthetic devices.
	file := writeTemp(t, `{"address": 0, "settings": [
	  {"id": 64, "raw": 500, "info": {"sc": 1, "offset": 0, "default": 200, "min": 1, "max": 3000}},
	  {"id": 7, "raw": 5, "info": {"sc": 1, "offset": 0, "default": 0, "min": 0, "max": 10}}]}`)
	code, out, errOut := cli(t, args("apply", "--yes", "--verbose", "--address", "0,1", file)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "writing device 0:", "writing device 1:",
		"verified  setting 64 (", "200 Ah (raw 200) -> 500 Ah (raw 500)", "400 Ah (raw 400) -> 500 Ah (raw 500)",
		"note: setting 7 (", "not supported by the device; left out")
	if n := writeFrames(errOut); n != 2 {
		t.Errorf("%d writes, want 2", n)
	}
}

func TestApplyRefusesBadFiles(t *testing.T) {
	// The 12 V dump does not fit the 48 V device's limits: nothing is
	// written to either device.
	file := writeDumpFile(t, "0", map[uint16]uint16{2: 1460})
	code, out, errOut := cli(t, args("apply", "--yes", "--verbose", "--address", "1", "--address", "0", file)...)
	if code != exitError || writeFrames(errOut) != 0 {
		t.Fatalf("exit %d, %d writes: %s", code, writeFrames(errOut), out)
	}
	wantContains(t, out, "error: ", "outside the allowed range")
	wantContains(t, errOut, "nothing written")

	// Float above absorption fails validation; --force writes anyway.
	file = writeDumpFile(t, "0", map[uint16]uint16{3: 1500})
	code, out, errOut = cli(t, args("apply", "--yes", "--verbose", file)...)
	if code != exitError || writeFrames(errOut) != 0 {
		t.Fatalf("validation: exit %d, %d writes: %s", code, writeFrames(errOut), out)
	}
	wantContains(t, out, "invalid: ", "skipped   setting 3 (")
	code, out, errOut = cli(t, args("apply", "--yes", "--force", "--verbose", file)...)
	if code != exitOK || writeFrames(errOut) != 1 {
		t.Fatalf("--force: exit %d, %d writes: %s\n%s", code, writeFrames(errOut), out, errOut)
	}
}

func TestApplyWriteFailures(t *testing.T) {
	file := writeDumpFile(t, "0", map[uint16]uint16{2: 1460, 4: 60})
	// Both attempts of the first write are lost: it fails after one retry
	// and the second setting is skipped.
	withSimulator(t, func(s *simulator.Simulator) {
		s.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdWriteData, Fault: simulator.Drop, Count: 2})
	})
	code, out, errOut := cli(t, args("apply", "--yes", "--verbose", file)...)
	if code != exitError || writeFrames(errOut) != 2 {
		t.Fatalf("exit %d, %d writes: %s", code, writeFrames(errOut), out)
	}
	wantContains(t, out, "failed    setting 2 (", "wrote raw 1460 but read back raw 1440", "skipped   setting 4 (")
	wantContains(t, errOut, "some settings were not written")

	code, out, errOut = cli(t, args("apply", "--yes", "--continue-on-error", "--verbose", file)...)
	if code != exitError || writeFrames(errOut) != 3 {
		t.Fatalf("continue: exit %d, %d writes: %s", code, writeFrames(errOut), out)
	}
	wantContains(t, out, "failed    setting 2 (", "verified  setting 4 (")
}

func TestApplyResetRequired(t *testing.T) {
	orig := newSimulator
	t.Cleanup(func() { newSimulator = orig })
	newSimulator = func() *simulator.Simulator {
		p := simulator.Synthetic12V230()
		p.Version = 2699300 // revision 300: setting 64 needs a reset
		return simulator.New(simulator.Config{Devices: []simulator.Device{{Address: 0, Profile: p}}})
	}
	file := writeTemp(t, `{"address": 0, "settings": [
	  {"id": 64, "raw": 500, "info": {"sc": 1, "offset": 0, "default": 200, "min": 1, "max": 3000}}]}`)
	code, out, errOut := cli(t, args("apply", "--yes", file)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	wantContains(t, out, "[reset required]", "device 0 needs a reset for these changes to take effect: setting 64 (")
}

func TestApplyUsage(t *testing.T) {
	for _, a := range [][]string{
		args("apply"),
		args("apply", "--address", "32", "x.json"),
		args("apply", "--address", "1,1", "x.json"),
		{"apply", "x.json"}, // no --port or --simulator
	} {
		if code, _, errOut := cli(t, a...); code != exitUsage {
			t.Errorf("%v: exit %d: %s", a, code, errOut)
		}
	}
	if code, _, errOut := cli(t, args("apply", "missing.json")...); code != exitError || !strings.Contains(errOut, "missing.json") {
		t.Errorf("missing file: exit %d: %s", code, errOut)
	}
}
