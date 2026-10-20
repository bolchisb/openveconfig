package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/internal/rvsctest"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vms"
)

// systemBackup writes a synthetic .rvms of three standalone units.
func systemBackup(t *testing.T) string {
	t.Helper()
	f := rvsctest.File{ToolVersion: "0.01", InfoFirmware: 2699481}
	for i := range 3 {
		f.Units = append(f.Units, rvsctest.Unit{
			LongID: 0x10000001 + uint32(i), Firmware: 2699481, AssistantFlag: 0xf0,
			Serial: "HQ000000" + string(rune('A'+i)), SaveTime: 1700000000, Settings: []uint16{1, 2},
			Identity: &rvsctest.Identity{ConfigProtocol: 1, ResponseChar: 0x02},
		})
	}
	return writeSpec(t, "backup.rvms", f)
}

// threePhaseVMS writes a .vms that puts the units of the backup at path on
// L1, L2 and L3 (n of them; the rest unplaced).
func threePhaseVMS(t *testing.T, path string, n int) string {
	t.Helper()
	data, _ := os.ReadFile(path)
	f, err := rvsc.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, _ := f.SystemUnits()
	l := system.NewLayout()
	l.Reshape(system.ThreePhase, 1)
	for i, u := range units {
		l.Add(u)
		if i < n {
			l.Place(u, system.Cell{Phase: i})
		}
	}
	b, err := vms.FromLayout(l).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "site.vms")
	os.WriteFile(p, b, 0o644)
	return p
}

func TestRVSCSystem(t *testing.T) {
	backup := systemBackup(t)
	out := filepath.Join(t.TempDir(), "out.rvms")
	code, stdout, stderr := cli(t, "rvsc-system", backup, threePhaseVMS(t, backup, 3), "-o", out)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	wantContains(t, stdout, "HQ000000B (10000002): L2 AC1", "wrote "+out)
	wantContains(t, stderr, "settings 76-80 were not changed")
	data, _ := os.ReadFile(out)
	f, err := rvsc.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	units, _ := f.SystemUnits()
	l, bad := system.FromRecords(units)
	if bad != nil || l.Setup != system.ThreePhase {
		t.Fatalf("the output does not load as three phase: %v", bad)
	}
	for i, u := range units {
		if c, _, _ := l.CellOf(u); c != (system.Cell{Phase: i}) {
			t.Errorf("unit %d in %v", i, c)
		}
	}
}

func TestRVSCSystemRefuses(t *testing.T) {
	backup := systemBackup(t)
	before, _ := os.ReadFile(backup)
	out := filepath.Join(t.TempDir(), "out.rvms")
	// A unit is not placed: the layout has an error.
	code, _, stderr := cli(t, "rvsc-system", backup, threePhaseVMS(t, backup, 2), "-o", out)
	if code != exitError || !strings.Contains(stderr, "nothing was written") {
		t.Errorf("unplaced unit: exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an output file was written")
	}
	// Never over the backup itself.
	if code, _, _ := cli(t, "rvsc-system", backup, threePhaseVMS(t, backup, 3), "-o", backup); code != exitUsage {
		t.Errorf("-o backup: exit %d, want %d", code, exitUsage)
	}
	if after, _ := os.ReadFile(backup); string(after) != string(before) {
		t.Error("the backup changed")
	}
	if code, _, _ := cli(t, "rvsc-system", backup); code != exitUsage {
		t.Errorf("one argument: exit %d, want %d", code, exitUsage)
	}
}
