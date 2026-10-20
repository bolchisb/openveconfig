package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
	"github.com/bolchisb/openveconfig/pkg/vms"
)

// The system tests run on simulated buses with short timeouts: units
// restart in 50 ms, system replies time out after 100 ms and the session
// waits 100 ms after a reset. A scan still takes about 0.6 s per unit plus
// 1.2 s (Identify listens 500 ms before asking).

// systemBus makes "system --simulator" use a fresh bus of devs, set up by
// setup (may be nil). It returns a pointer to the simulator of the last run.
func systemBus(t *testing.T, devs func() []simulator.Device, setup func(*simulator.Simulator)) **simulator.Simulator {
	t.Helper()
	origSim, origClient, origSession := newSystemSimulator, clientOptions, sessionOptions
	t.Cleanup(func() { newSystemSimulator, clientOptions, sessionOptions = origSim, origClient, origSession })
	clientOptions.RawTimeout = 100 * time.Millisecond
	sessionOptions = bus.Options{ResetWait: 100 * time.Millisecond, PostResetWindow: 5 * time.Second}
	var last *simulator.Simulator
	newSystemSimulator = func() *simulator.Simulator {
		last = simulator.New(simulator.Config{Devices: devs(), VersionInterval: -1, ResetDuration: 50 * time.Millisecond})
		if setup != nil {
			setup(last)
		}
		return last
	}
	return &last
}

// records returns every simulated unit's stored record.
func records(s *simulator.Simulator) []system.Record {
	var rs []system.Record
	for _, u := range s.Units() {
		rs = append(rs, system.Record(u.Record))
	}
	return rs
}

// splitPhase is a .vms file that puts the two units of
// simulator.SystemParallel2 on L1 and L2 of a split-phase system. It is
// saved to a temporary file whose path is returned with the file.
func splitPhase(t *testing.T, change func(*vms.File)) (string, *vms.File) {
	t.Helper()
	unit := func(id uint32, cell uint32, name string) *vms.Unit {
		return &vms.Unit{LongID: id, VersionWord: 2699481, ResponseChar: 0x02, Letter: 0, ConfigProtocol: 1,
			SystemRecord: [vms.RecordLen]byte{0x01, 0x01, 0x01, 0, 0, 0, 0, 0xE0, 0}, Name: name, Cell: cell}
	}
	f := &vms.File{SetupType: vms.SetupDual180, L2Option: vms.L2Fixed, ACInputs: 1, GroupMask: 0xF, SelectedCell: vms.NoCell,
		Units: []*vms.Unit{unit(0x10000001, vms.CellCode(0, 0), "left"), unit(0x10000002, vms.CellCode(1, 0), "right")}}
	if change != nil {
		change(f)
	}
	f.Arrange()
	b, err := f.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "split.vms")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, f
}

// wantRecords returns the records the layout of f gives its units when
// sent, in long-ID order.
func wantRecords(t *testing.T, f *vms.File) []system.Record {
	t.Helper()
	l, err := f.Layout(nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uint32]system.Record{}
	for _, r := range l.Records(true) {
		byID[r.Unit.LongID] = r.Record
	}
	return []system.Record{byID[0x10000001], byID[0x10000002]}
}

func TestSystemShow(t *testing.T) {
	systemBus(t, simulator.SystemParallel2, nil)
	code, out, errOut := cli(t, args("system", "show")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "2 unit(s) found", "10000001", "10000002", "2699481", "leader of 2", "follower",
		"FF FF 09 00 00 00 00 F0 03", "records agree: single phase, 1 AC input(s)", "system check: no issues")
	wantContains(t, errOut, "looking for units")

	code, out, errOut = cli(t, args("system", "show", "--json")...)
	if code != exitOK {
		t.Fatalf("json: exit %d: %s", code, errOut)
	}
	var got struct {
		Units []struct {
			ShortAddress int    `json:"short_address"`
			LongID       string `json:"long_id"`
			Phase        int    `json:"phase"`
			Leader       bool   `json:"leader"`
		} `json:"units"`
		Consistent bool `json:"consistent"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	if !got.Consistent || len(got.Units) != 2 || got.Units[1].ShortAddress != 3 || got.Units[1].LongID != "10000002" ||
		got.Units[1].Phase != 1 || !got.Units[0].Leader || got.Units[1].Leader {
		t.Errorf("json: %+v", got)
	}
}

func TestSystemShowInconsistent(t *testing.T) {
	systemBus(t, func() []simulator.Device {
		ds := simulator.SystemParallel2()
		ds[1].System.Record[2] = 0x01 // the presence masks differ
		return ds
	}, nil)
	code, out, errOut := cli(t, args("system", "show")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "records do not agree", "condition 1", "E12 error")
}

func TestSystemShowNoUnits(t *testing.T) {
	systemBus(t, func() []simulator.Device { return nil }, nil)
	code, _, errOut := cli(t, args("system", "show")...)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	wantContains(t, errOut, "no units", "No unit answered")
}

func TestSystemWriteDryRun(t *testing.T) {
	sim := systemBus(t, simulator.SystemParallel2, nil)
	path, f := splitPhase(t, nil)
	code, out, errOut := cli(t, args("system", "write", path)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "checks: no issues", "left", "right", "L2 AC1", "changes", "planned",
		"write "+wantRecords(t, f)[1].Hex()+" to 10000002", "reset all units", "dry run: nothing written")
	before := records(simulator.New(simulator.Config{Devices: simulator.SystemParallel2()}))
	if got := records(*sim); got[0] != before[0] || got[1] != before[1] {
		t.Errorf("dry run changed the records: %v", got)
	}
}

func TestSystemWrite(t *testing.T) {
	sim := systemBus(t, simulator.SystemParallel2, nil)
	path, f := splitPhase(t, nil)
	code, out, errOut := cli(t, args("system", "write", "--yes", path)...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	wantContains(t, out, "verified (1 write(s))", "the system was reset", "Every unit holds and uses the new configuration.")
	want := wantRecords(t, f)
	for i, u := range (*sim).Units() {
		if system.Record(u.Record) != want[i] || u.Address != want[i].ShortAddress() {
			t.Errorf("unit %d holds %X at address %d, want %s", i, u.Record, u.Address, want[i].Hex())
		}
	}
}

func TestSystemWriteRefusesIncompatibleLayout(t *testing.T) {
	sim := systemBus(t, simulator.SystemMixedFirmware, nil)
	path, _ := splitPhase(t, nil)
	code, out, errOut := cli(t, args("system", "write", "--yes", path)...)
	if code != exitError {
		t.Fatalf("exit %d, want %d: %s", code, exitError, errOut)
	}
	wantContains(t, out, "E2 error")
	wantContains(t, errOut, "blocked by system check", "Nothing was written.")
	for _, u := range (*sim).Units() {
		if u.Writes != 0 {
			t.Errorf("unit %08X got %d writes", u.LongID, u.Writes)
		}
	}

	// A layout without one of the bus units (rule E14).
	systemBus(t, simulator.SystemParallel2, nil)
	path, _ = splitPhase(t, func(f *vms.File) { f.Units = f.Units[:1]; f.SetupType = vms.SetupSingle })
	code, out, errOut = cli(t, args("system", "write", "--yes", path)...)
	if code != exitError {
		t.Fatalf("E14: exit %d, want %d: %s", code, exitError, errOut)
	}
	wantContains(t, out, "E14 error")
	wantContains(t, errOut, "Nothing was written.")
}

func TestSystemWriteRollback(t *testing.T) {
	// The second unit reads back its old record after every write attempt.
	sim := systemBus(t, simulator.SystemParallel2, func(s *simulator.Simulator) {
		s.SetBehaviour(0x10000002, simulator.Behaviour{StaleReads: bus.WriteAttempts})
	})
	path, _ := splitPhase(t, nil)
	code, out, errOut := cli(t, args("system", "write", "--yes", path)...)
	if code != exitError {
		t.Fatalf("exit %d, want %d: %s", code, exitError, errOut)
	}
	wantContains(t, out, "restored (2 write(s))", "restored (4 write(s))", "but read back FF FF 09")
	wantContains(t, errOut, "rolled back (right)", "Sending failed at unit right. The previous configuration was restored on all units",
		"Run 'openveconfig-cli system show'")
	if strings.Contains(out, "the system was reset") {
		t.Error("the system was reset after a rollback")
	}
	before := records(simulator.New(simulator.Config{Devices: simulator.SystemParallel2()}))
	if got := records(*sim); got[0] != before[0] || got[1] != before[1] {
		t.Errorf("records after the rollback: %v, want %v", got, before)
	}
}

func TestSystemWriteRollbackFailed(t *testing.T) {
	// The second unit stores every record with byte 0 inverted, also the
	// one written back.
	systemBus(t, simulator.SystemParallel2, func(s *simulator.Simulator) {
		s.SetBehaviour(0x10000002, simulator.Behaviour{CorruptWrites: true})
	})
	path, _ := splitPhase(t, nil)
	code, out, errOut := cli(t, args("system", "write", "--yes", path)...)
	if code != exitError {
		t.Fatalf("exit %d, want %d: %s", code, exitError, errOut)
	}
	wantContains(t, out, "restored (2 write(s))", "restore failed (6 write(s)), holds 00 FF 09")
	wantContains(t, errOut, "rollback failed (right)", "Do not switch the system on for use", "run 'openveconfig-cli system show'")
}

func TestSystemWriteAssistants(t *testing.T) {
	// Both units have assistants: the flag (byte 7 bit 4) is clear.
	devs := func() []simulator.Device {
		ds := simulator.SystemParallel2()
		for _, d := range ds {
			d.System.Record[7] &^= 0x10
		}
		return ds
	}
	sim := systemBus(t, devs, nil)
	path, f := splitPhase(t, nil)
	code, out, errOut := cli(t, args("system", "write", path)...)
	if code != exitOK {
		t.Fatalf("dry run: exit %d: %s", code, errOut)
	}
	wantContains(t, out, "writing removes the assistants of left", "pass --remove-assistants as well")

	code, _, errOut = cli(t, args("system", "write", "--yes", path)...)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	wantContains(t, errOut, "assistant removal not confirmed", "Pass --remove-assistants to confirm.", "Nothing was written.")

	code, out, errOut = cli(t, args("system", "write", "--yes", "--remove-assistants", path)...)
	if code != exitOK {
		t.Fatalf("confirmed: exit %d: %s\n%s", code, errOut, out)
	}
	want := wantRecords(t, f)
	if got := records(*sim); got[0] != want[0] || got[1] != want[1] {
		t.Errorf("records %v, want %v", got, want)
	}
}

func TestSystemWriteBadFile(t *testing.T) {
	systemBus(t, simulator.SystemParallel2, nil)
	bad := filepath.Join(t.TempDir(), "bad.vms")
	os.WriteFile(bad, []byte("not a file"), 0o644)
	code, _, errOut := cli(t, args("system", "write", bad)...)
	if code != exitError || !strings.Contains(errOut, "damaged") {
		t.Errorf("exit %d: %s", code, errOut)
	}
	// A unit in a cell the setup does not have.
	path, _ := splitPhase(t, func(f *vms.File) { f.SetupType = vms.SetupSingle })
	code, _, errOut = cli(t, args("system", "write", path)...)
	if code != exitError || !strings.Contains(errOut, "malformed") || !strings.Contains(errOut, "Nothing was written") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestSystemReset(t *testing.T) {
	sim := systemBus(t, simulator.SystemParallel2, nil)
	code, _, errOut := cli(t, args("system", "reset")...)
	if code != exitUsage || !strings.Contains(errOut, "without --yes") || !strings.Contains(errOut, "Nothing was sent") {
		t.Fatalf("without --yes: exit %d: %s", code, errOut)
	}
	if *sim != nil {
		t.Error("the bus was opened without --yes")
	}
	code, out, errOut := cli(t, args("system", "reset", "--yes")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	wantContains(t, out, "the system was reset", "Every unit came back with its configuration.")
	wantContains(t, errOut, "resetting the system (2 of 2)")
	for _, u := range (*sim).Units() {
		if u.Resets == 0 {
			t.Errorf("unit %08X was not reset", u.LongID)
		}
	}
}

func TestSystemResetNotBack(t *testing.T) {
	systemBus(t, simulator.SystemParallel2, func(s *simulator.Simulator) {
		s.SetBehaviour(0x10000002, simulator.Behaviour{ResetDuration: time.Hour})
	})
	sessionOptions.PostResetWindow = 500 * time.Millisecond
	code, out, errOut := cli(t, args("system", "reset", "--yes")...)
	if code != exitError {
		t.Fatalf("exit %d, want %d: %s", code, exitError, out)
	}
	wantContains(t, out, "not back: 10000002")
	wantContains(t, errOut, "not back after reset", "Not every unit came back")
}

func TestSystemStandalone(t *testing.T) {
	code, _, errOut := cli(t, args("system", "standalone")...)
	if code != exitUsage || !strings.Contains(errOut, "without --yes") {
		t.Fatalf("without --yes: exit %d: %s", code, errOut)
	}

	// Two units answer.
	sim := systemBus(t, simulator.SystemParallel2, nil)
	code, _, errOut = cli(t, args("system", "standalone", "--yes")...)
	if code != exitError {
		t.Fatalf("two units: exit %d, want %d", code, exitError)
	}
	wantContains(t, errOut, "found 2 units", "Exactly one unit must be switched on", "Nothing was written")
	for _, u := range (*sim).Units() {
		if u.Writes != 0 {
			t.Errorf("unit %08X got %d writes", u.LongID, u.Writes)
		}
	}

	// Only the second unit is on.
	sim = systemBus(t, simulator.SystemParallel2, func(s *simulator.Simulator) { s.SetPowered(0x10000001, false) })
	code, out, errOut := cli(t, args("system", "standalone", "--yes")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	wantContains(t, out, "The unit is now standalone.")
	u, _ := (*sim).Unit(0x10000002)
	if want := system.Standalone(&system.Unit{VersionWord: 2699481}, true); system.Record(u.Record) != want {
		t.Errorf("record %X, want %s", u.Record, want.Hex())
	}
}

func TestSystemUsage(t *testing.T) {
	systemBus(t, simulator.SystemParallel2, nil)
	path, _ := splitPhase(t, nil)
	for _, a := range [][]string{
		{"system"},
		{"system", "list"},
		{"system", "show", "extra"},
		{"system", "write"},
		{"system", "write", path, "--json"},
		{"system", "show", "--yes"},
		{"system", "reset", "--yes", "--remove-assistants"},
	} {
		if code, _, errOut := cli(t, args(a...)...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d: %s", a, code, exitUsage, errOut)
		}
	}
	if code, _, _ := cli(t, "system", "show"); code != exitUsage {
		t.Errorf("no interface: exit %d", code)
	}
	_, out, _ := cli(t, "help")
	wantContains(t, out, "system", "VE.Bus system")
	_, _, errOut := cli(t, "system", "--help")
	wantContains(t, errOut, "WARNING", "system write <file.vms>", "--remove-assistants", "two units in parallel")
}
