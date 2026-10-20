package bus_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// The tests run real mk2.Clients against simulated buses, with short
// timeouts: units restart in 50 ms, replies time out after 100 ms and the
// session waits 100 ms after a reset. Identify still listens 500 ms before
// asking (mk2), so a scan takes about 0.7 s per unit plus 1.4 s.

type env struct {
	sim *simulator.Simulator
	c   *mk2.Client
	s   *bus.Session
	ids []uint32 // long IDs of the units, ascending (the order a scan finds them)
}

func start(t *testing.T, devs []simulator.Device, opts bus.Options) *env {
	t.Helper()
	sim := simulator.New(simulator.Config{Devices: devs, VersionInterval: -1, ResetDuration: 50 * time.Millisecond})
	c := mk2.NewClient(sim.Port(), &mk2.Options{Timeout: 100 * time.Millisecond, RawTimeout: 100 * time.Millisecond})
	t.Cleanup(func() { c.Close() })
	if opts.ResetWait == 0 {
		opts.ResetWait = 100 * time.Millisecond
	}
	if opts.PostResetWindow == 0 {
		opts.PostResetWindow = 5 * time.Second
	}
	s, err := bus.Open(context.Background(), c, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	e := &env{sim: sim, c: c, s: s}
	for _, u := range sim.Units() {
		e.ids = append(e.ids, u.LongID)
	}
	return e
}

// scan scans and checks that n units were found.
func (e *env) scan(t *testing.T, n int) *bus.ScanReport {
	t.Helper()
	rep, err := e.s.Scan(context.Background(), nil)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(rep.Units) != n {
		t.Fatalf("found %d units, want %d", len(rep.Units), n)
	}
	return rep
}

func (e *env) state(t *testing.T, i int) simulator.UnitState {
	t.Helper()
	u, ok := e.sim.Unit(e.ids[i])
	if !ok {
		t.Fatalf("no unit %d", i)
	}
	return u
}

// records returns every unit's stored record.
func (e *env) records() []system.Record {
	var rs []system.Record
	for _, u := range e.sim.Units() {
		rs = append(rs, system.Record(u.Record))
	}
	return rs
}

// noWrites fails if any unit got a write or a reset.
func (e *env) noWrites(t *testing.T) {
	t.Helper()
	for _, u := range e.sim.Units() {
		if u.Writes != 0 || u.Resets != 0 {
			t.Errorf("unit %08X: %d writes, %d resets", u.LongID, u.Writes, u.Resets)
		}
	}
}

func rec(t testing.TB, s string) system.Record {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil || len(b) != 9 {
		t.Fatalf("bad record %q", s)
	}
	return system.Record(b)
}

func recs(t testing.TB, ss ...string) []system.Record {
	var rs []system.Record
	for _, s := range ss {
		rs = append(rs, rec(t, s))
	}
	return rs
}

func kind(t *testing.T, err error, k bus.Kind) *bus.Error {
	t.Helper()
	var be *bus.Error
	if !errors.As(err, &be) || be.Kind != k {
		t.Fatalf("error %v, want kind %v", err, k)
	}
	if be.Guidance() == "" {
		t.Errorf("no guidance for %v", k)
	}
	return be
}

// layout places units in cells, in order.
func layout(t *testing.T, s system.Setup, opt system.L2Option, units []*system.Unit, cells ...system.Cell) *system.Layout {
	t.Helper()
	l := system.NewLayout()
	if err := l.Reshape(s, 1); err != nil {
		t.Fatal(err)
	}
	l.L2 = opt
	for i, u := range units {
		if err := l.Add(u); err != nil {
			t.Fatal(err)
		}
		if err := l.Place(u, cells[i]); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func l1(n int) []system.Cell {
	cs := make([]system.Cell, n)
	return cs // zero Cell is L1, AC input 1
}

var (
	confirm    = bus.WriteOptions{Confirm: true}
	confirmAll = bus.WriteOptions{Confirm: true, RemoveAssistants: true}
)

// applied checks that a write ended with every unit holding and using
// want, and that the scan after the reset rebuilt a consistent layout.
func (e *env) applied(t *testing.T, rep *bus.WriteReport, want []system.Record) {
	t.Helper()
	if !rep.Reset || rep.After == nil || rep.After.Inconsistent != nil || len(rep.NotBack) != 0 {
		t.Fatalf("reset %v, after %+v, not back %v", rep.Reset, rep.After, rep.NotBack)
	}
	if system.HasErrors(rep.After.Issues) {
		t.Fatalf("check after write: %v", rep.After.Issues)
	}
	for i, w := range want {
		u := e.state(t, i)
		if system.Record(u.Record) != w || system.Record(u.ActiveRecord) != w || u.Address != w.ShortAddress() {
			t.Errorf("unit %d: stored %X, active %X, address %d; want %s", i, u.Record, u.ActiveRecord, u.Address, w)
		}
		if u.Resets == 0 {
			t.Errorf("unit %d was not reset", i)
		}
	}
	for _, r := range rep.Units {
		if r.Status != bus.StatusVerified {
			t.Errorf("unit %s: %v (%v)", r.Unit.Label(), r.Status, r.Err)
		}
	}
}

func TestScan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		devs       []simulator.Device
		consistent bool
	}{
		{"factory1", simulator.SystemFactory(1), true},
		{"factory2", simulator.SystemFactory(2), false},
		{"factory3", simulator.SystemFactory(3), false},
		{"factory6", simulator.SystemFactory(6), false},
		{"parallel2", simulator.SystemParallel2(), true},
		{"threephase", simulator.SystemThreePhase(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var steps []bus.Step
			e := start(t, tc.devs, bus.Options{NoKeepAlive: true})
			rep, err := e.s.Scan(context.Background(), func(ev bus.Event) {
				steps = append(steps, ev.Step)
				_ = ev.String()
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Units) != len(tc.devs) || len(rep.Lost) != 0 {
				t.Fatalf("found %d, lost %d", len(rep.Units), len(rep.Lost))
			}
			for i, u := range rep.Units {
				st := e.state(t, i)
				if u.LongID != st.LongID || u.Record != system.Record(st.Record) || u.ResponseChar != 0x02 {
					t.Errorf("unit %d: %08X %s, sim %08X %X", i, u.LongID, u.Record, st.LongID, st.Record)
				}
				if a, ok := e.s.ActiveAddress(u.LongID); !ok || a != st.Address {
					t.Errorf("unit %d: active address %d %v, sim %d", i, a, ok, st.Address)
				}
				if st.Identification {
					t.Errorf("unit %d: identification left on", i)
				}
				if u.ExternalBF {
					t.Errorf("unit %d: external BF", i)
				}
			}
			if (rep.Inconsistent == nil) != tc.consistent {
				t.Errorf("inconsistent: %v", rep.Inconsistent)
			}
			if len(steps) == 0 || steps[0] != bus.StepPrepare {
				t.Errorf("steps %v", steps)
			}
			if len(e.s.Units()) != len(tc.devs) {
				t.Errorf("session has %d units", len(e.s.Units()))
			}
			e.noWrites(t)
		})
	}
}

func TestScanFailures(t *testing.T) {
	t.Parallel()

	t.Run("NoUnits", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(2), bus.Options{})
		for _, id := range e.ids {
			e.sim.SetPowered(id, false)
		}
		rep, err := e.s.Scan(context.Background(), nil)
		kind(t, err, bus.KindNoUnits)
		if rep == nil || len(rep.Units) != 0 {
			t.Fatalf("report %+v", rep)
		}
	})

	t.Run("NoMute", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(3), bus.Options{})
		e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{IgnoreMute: true})
		_, err := e.s.Scan(context.Background(), nil)
		be := kind(t, err, bus.KindNoMute)
		if len(be.Units) != 1 || be.Units[0].LongID != e.ids[1] {
			t.Fatalf("units %v", be.Units)
		}
		e.noWrites(t)
	})

	t.Run("NewerTool", func(t *testing.T) {
		t.Parallel()
		devs := simulator.SystemFactory(2)
		devs[1].System.ConfigProtocol = 2
		e := start(t, devs, bus.Options{})
		_, err := e.s.Scan(context.Background(), nil)
		kind(t, err, bus.KindNewerTool)
	})

	t.Run("LostAfterIdentify", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(3), bus.Options{})
		e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{PowerOffOnMute: true})
		rep, err := e.s.Scan(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Units) != 2 || len(rep.Lost) != 1 || rep.Lost[0].LongID != e.ids[1] {
			t.Fatalf("units %d, lost %v", len(rep.Units), rep.Lost)
		}
	})

	t.Run("KnownUnitLost", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		first := e.scan(t, 2)
		first.Units[1].Name = "slave"
		e.sim.SetPowered(e.ids[1], false)
		rep := e.scan(t, 1)
		if len(rep.Lost) != 1 || rep.Lost[0] != first.Units[1] || rep.Units[0] != first.Units[0] {
			t.Fatalf("lost %v", rep.Lost)
		}
		e.sim.SetPowered(e.ids[1], true)
		rep = e.scan(t, 2)
		if rep.Units[1].Name != "" {
			t.Fatal("a removed unit came back with its old *Unit")
		}
	})

	t.Run("Cancelled", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(2), bus.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		_, err := e.s.Scan(ctx, func(ev bus.Event) {
			if ev.Step == bus.StepIdentify && ev.Done == 1 {
				cancel()
			}
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error %v", err)
		}
	})
}

func TestScanUnitDetails(t *testing.T) {
	t.Parallel()
	devs := simulator.SystemParallel2()
	devs[1].System.ExternalBF = true
	devs = append(devs, simulator.SystemDevice(simulator.SystemUnit{
		LongID: 0x20000001, ResponseChar: system.ResponseNoConfig,
		Record: [9]byte{0x01, 0x01, 0x55, 0x55, 0x55, 0x55, 0x55, 0x10, 0x07},
	}))
	e := start(t, devs, bus.Options{})
	rep := e.scan(t, 3)
	if rep.Units[0].ExternalBF || !rep.Units[1].ExternalBF {
		t.Errorf("external BF %v %v", rep.Units[0].ExternalBF, rep.Units[1].ExternalBF)
	}
	nc := rep.Units[2]
	if !nc.NoConfig() || nc.Record != rec(t, "01 01 01 00 00 00 00 E0 00") {
		t.Errorf("no-configuration unit: %#x %s", nc.ResponseChar, nc.Record)
	}
}

func TestWriteWorkedExamples(t *testing.T) {
	t.Parallel()

	t.Run("Factory2Parallel", func(t *testing.T) { // spec example E2
		t.Parallel()
		e := start(t, simulator.SystemFactory(2), bus.Options{})
		sr := e.scan(t, 2)
		l := layout(t, system.SinglePhase, system.Fixed, sr.Units, l1(2)...)
		var events []bus.Event
		rep, err := e.s.Write(context.Background(), l, confirmAll, func(ev bus.Event) { events = append(events, ev) })
		if err != nil {
			t.Fatal(err)
		}
		e.applied(t, rep, recs(t, "02 02 09 00 00 00 00 F0 00", "FF FF 09 00 00 00 00 F0 03"))
		if len(rep.AssistantsRemoved) != 2 {
			t.Errorf("assistants removed from %v", rep.AssistantsRemoved)
		}
		for _, r := range rep.Units {
			if r.Writes != 1 || r.Old != rec(t, "01 01 01 00 00 00 00 E0 00") {
				t.Errorf("unit %s: %d writes, old %s", r.Unit.Label(), r.Writes, r.Old)
			}
		}
		want := []bus.Step{bus.StepPrepare, bus.StepSnapshot, bus.StepSnapshot, bus.StepWrite, bus.StepWrite, bus.StepReset, bus.StepReset, bus.StepWait, bus.StepVerify}
		for i, s := range want {
			if i >= len(events) || events[i].Step != s {
				t.Fatalf("events %v, want %v first", events, want)
			}
		}
	})

	t.Run("Factory3ThreePhase", func(t *testing.T) { // E6
		t.Parallel()
		e := start(t, simulator.SystemFactory(3), bus.Options{})
		sr := e.scan(t, 3)
		rep, err := e.s.WriteQuick(context.Background(), system.QuickThreePhase, sr.Units, confirmAll, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.applied(t, rep, recs(t, "01 01 07 00 00 00 00 F8 00", "01 01 07 00 00 00 04 F9 01", "01 01 07 00 00 00 08 FA 02"))
		if rep.After.Layout.Setup != system.ThreePhase {
			t.Errorf("setup after write: %v", rep.After.Layout.Setup)
		}
	})

	t.Run("Factory6ThreePhase2PerPhase", func(t *testing.T) { // E7
		t.Parallel()
		e := start(t, simulator.SystemFactory(6), bus.Options{})
		sr := e.scan(t, 6)
		c := func(p int) system.Cell { return system.Cell{Phase: p} }
		l := layout(t, system.ThreePhase, system.Fixed, sr.Units, c(0), c(0), c(1), c(1), c(2), c(2))
		rep, err := e.s.Write(context.Background(), l, confirmAll, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.applied(t, rep, recs(t,
			"02 02 3F 00 00 00 00 F8 00", "FF FF 3F 00 00 00 00 F8 03",
			"02 02 3F 00 00 00 04 F9 01", "FF FF 3F 00 00 00 04 F9 04",
			"02 02 3F 00 00 00 08 FA 02", "FF FF 3F 00 00 00 08 FA 05"))
	})

	t.Run("Parallel2ToSplit180Auto", func(t *testing.T) { // E5, no assistants to remove
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		sr := e.scan(t, 2)
		rep, err := e.s.WriteQuick(context.Background(), system.QuickSplit180Auto, sr.Units, confirm, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.applied(t, rep, recs(t, "01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 C6 F5 01"))
		if len(rep.AssistantsRemoved) != 0 {
			t.Errorf("assistants removed from %v", rep.AssistantsRemoved)
		}
	})

	// A unit that reports no configuration is written and verified like
	// any other. The simulated unit keeps reporting no configuration after
	// the reset, which the check after the reset flags (a real unit is
	// expected to report its kind once configured; not checked on
	// hardware).
	t.Run("NoConfigUnit", func(t *testing.T) {
		t.Parallel()
		devs := simulator.SystemFactory(2)
		devs[1].System.ResponseChar = system.ResponseNoConfig
		e := start(t, devs, bus.Options{})
		sr := e.scan(t, 2)
		want := recs(t, "02 02 09 00 00 00 00 F0 00", "FF FF 09 00 00 00 00 F0 03")
		rep, err := e.s.WriteQuick(context.Background(), system.QuickParallel2, sr.Units, confirmAll, nil)
		be := kind(t, err, bus.KindNotBack)
		if len(be.Units) != 1 || be.Units[0].LongID != e.ids[1] || !rep.Reset {
			t.Fatalf("not back %v", be.Units)
		}
		for i, r := range rep.Units {
			u := e.state(t, i)
			if r.Status != bus.StatusVerified || system.Record(u.Record) != want[i] || u.Address != want[i].ShortAddress() {
				t.Errorf("unit %d: %v, sim %+v", i, r.Status, u)
			}
		}
	})
}

func TestWriteRefused(t *testing.T) {
	t.Parallel()

	t.Run("DryRun", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(2), bus.Options{})
		sr := e.scan(t, 2)
		rep, err := e.s.WriteQuick(context.Background(), system.QuickParallel2, sr.Units, bus.WriteOptions{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !rep.DryRun || rep.Reset || len(rep.Ops) != 2+2+2+2 || len(rep.AssistantsRemoved) != 2 {
			t.Fatalf("report %+v", rep)
		}
		if rep.Units[1].New != rec(t, "FF FF 09 00 00 00 00 F0 03") || rep.Units[1].Old != rec(t, "01 01 01 00 00 00 00 E0 00") ||
			rep.Units[1].Status != bus.StatusPlanned {
			t.Errorf("unit 1: %+v", rep.Units[1])
		}
		e.noWrites(t)
	})

	t.Run("MixedFirmware", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemMixedFirmware(), bus.Options{})
		sr := e.scan(t, 2)
		_, err := e.s.Write(context.Background(), sr.Layout, confirmAll, nil)
		be := kind(t, err, bus.KindBlocked)
		if len(be.Issues) == 0 || be.Issues[0].Rule != system.RuleFirmware {
			t.Fatalf("issues %v", be.Issues)
		}
		e.noWrites(t)
	})

	t.Run("BusMismatch", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(3), bus.Options{})
		sr := e.scan(t, 3)
		_, err := e.s.WriteQuick(context.Background(), system.QuickParallel2, sr.Units[:2], confirmAll, nil)
		be := kind(t, err, bus.KindBlocked)
		if be.Issues[0].Rule != system.RuleBusMismatch {
			t.Fatalf("issues %v", be.Issues)
		}
		e.noWrites(t)
	})

	t.Run("NoScan", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(1), bus.Options{})
		u := &system.Unit{LongID: e.ids[0], VersionWord: 2699481, ResponseChar: 2}
		_, err := e.s.WriteQuick(context.Background(), system.QuickStandalone, []*system.Unit{u}, confirmAll, nil)
		kind(t, err, bus.KindBlocked)
		e.noWrites(t)
	})

	t.Run("AssistantsNotConfirmed", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(2), bus.Options{})
		sr := e.scan(t, 2)
		rep, err := e.s.WriteQuick(context.Background(), system.QuickParallel2, sr.Units, confirm, nil)
		be := kind(t, err, bus.KindAssistants)
		if len(be.Units) != 2 || len(rep.AssistantsRemoved) != 2 {
			t.Fatalf("units %v", be.Units)
		}
		e.noWrites(t)
	})

	t.Run("SnapshotFails", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		sr := e.scan(t, 2)
		e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{NoReadReply: true})
		_, err := e.s.WriteQuick(context.Background(), system.QuickSplit180, sr.Units, confirm, nil)
		be := kind(t, err, bus.KindSnapshot)
		if be.Units[0].LongID != e.ids[1] {
			t.Fatalf("units %v", be.Units)
		}
		e.noWrites(t)
	})
}

// TestRollback writes the parallel-3 layout to a three-phase system whose
// second unit fails, and checks that every unit keeps its record.
func TestRollback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		b       simulator.Behaviour
		at      int
		hook    func(e *env, ev bus.Event) // fix the unit during the rollback
		kind    bus.Kind
		writes  []int
		status  []bus.Status
		restore bool // every unit holds its old record afterwards
	}{
		{
			name: "IgnoreWrites", b: simulator.Behaviour{IgnoreWrites: true}, at: 1, kind: bus.KindRolledBack,
			writes: []int{2, 4, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusRestored, bus.StatusNotWritten}, restore: true,
		},
		{
			name: "IgnoreWritesFirst", b: simulator.Behaviour{IgnoreWrites: true}, at: 0, kind: bus.KindRolledBack,
			writes: []int{4, 0, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusNotWritten, bus.StatusNotWritten}, restore: true,
		},
		{
			name: "CorruptWrites", b: simulator.Behaviour{CorruptWrites: true}, at: 1, kind: bus.KindRollbackFailed,
			writes: []int{2, 6, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusRestoreFailed, bus.StatusNotWritten},
		},
		{
			name: "CorruptWritesTransient", b: simulator.Behaviour{CorruptWrites: true}, at: 1, kind: bus.KindRolledBack,
			hook: func(e *env, ev bus.Event) {
				if ev.Step == bus.StepRollback {
					e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{})
				}
			},
			writes: []int{2, 4, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusRestored, bus.StatusNotWritten}, restore: true,
		},
		{
			name: "PowerOffOnWrite", b: simulator.Behaviour{PowerOffOnWrite: true}, at: 1, kind: bus.KindRollbackFailed,
			writes: []int{2, 6, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusRestoreFailed, bus.StatusNotWritten}, restore: true,
		},
		{
			name: "PowerOffOnWriteBackOn", b: simulator.Behaviour{PowerOffOnWrite: true}, at: 1, kind: bus.KindRolledBack,
			hook: func(e *env, ev bus.Event) {
				if ev.Step == bus.StepRollback && ev.Unit.LongID == e.ids[1] && ev.Attempt == 1 {
					e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{})
					e.sim.SetPowered(e.ids[1], true)
				}
			},
			writes: []int{2, 4, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusRestored, bus.StatusNotWritten}, restore: true,
		},
		{
			name: "StaleReads3", b: simulator.Behaviour{StaleReads: 3}, at: 1, kind: bus.KindRolledBack,
			writes: []int{2, 4, 0}, status: []bus.Status{bus.StatusRestored, bus.StatusRestored, bus.StatusNotWritten}, restore: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := start(t, simulator.SystemThreePhase(), bus.Options{})
			sr := e.scan(t, 3)
			before := e.records()
			e.sim.SetBehaviour(e.ids[tc.at], tc.b)
			rep, err := e.s.WriteQuick(context.Background(), system.QuickParallel3, sr.Units, confirm, func(ev bus.Event) {
				if tc.hook != nil {
					tc.hook(e, ev)
				}
			})
			be := kind(t, err, tc.kind)
			if rep.Failed == nil || rep.Failed.LongID != e.ids[tc.at] || rep.Reset {
				t.Fatalf("failed %v, reset %v", rep.Failed, rep.Reset)
			}
			if tc.kind == bus.KindRolledBack && (len(be.Units) != 1 || be.Units[0] != rep.Failed) {
				t.Errorf("error names %v", be.Units)
			}
			if tc.kind == bus.KindRollbackFailed && (len(be.Units) != 1 || be.Units[0].LongID != e.ids[tc.at]) {
				t.Errorf("error names %v", be.Units)
			}
			if rep.Units[tc.at].Err == nil {
				t.Error("the failing unit has no error")
			}
			for i, r := range rep.Units {
				if r.Writes != tc.writes[i] || r.Status != tc.status[i] {
					t.Errorf("unit %d: %d writes, %v; want %d, %v", i, r.Writes, r.Status, tc.writes[i], tc.status[i])
				}
				if r.Writes > 2*bus.WriteAttempts {
					t.Errorf("unit %d: %d writes", i, r.Writes)
				}
			}
			after := e.records()
			for i := range after {
				u := e.state(t, i)
				if u.Resets != 0 {
					t.Errorf("unit %d was reset", i)
				}
				if (tc.restore || i != tc.at) && after[i] != before[i] {
					t.Errorf("unit %d holds %s, had %s", i, after[i], before[i])
				}
			}
			if tc.name == "CorruptWrites" {
				r := rep.Units[tc.at]
				if !r.HoldsKnown || r.Holds == before[tc.at] {
					t.Errorf("holds %s (%v)", r.Holds, r.HoldsKnown)
				}
			}
		})
	}
}

func TestWriteStaleReads(t *testing.T) {
	t.Parallel()
	for _, n := range []int{1, 2} {
		t.Run(strings.Repeat("stale", n), func(t *testing.T) {
			t.Parallel()
			e := start(t, simulator.SystemParallel2(), bus.Options{})
			sr := e.scan(t, 2)
			e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{StaleReads: n})
			rep, err := e.s.WriteQuick(context.Background(), system.QuickSplit180, sr.Units, confirm, nil)
			if err != nil {
				t.Fatal(err)
			}
			e.applied(t, rep, recs(t, "01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 06 F5 01"))
			if rep.Units[1].Writes != n+1 {
				t.Errorf("unit 1: %d writes, want %d", rep.Units[1].Writes, n+1)
			}
		})
	}
}

func TestWriteReset(t *testing.T) {
	t.Parallel()
	split := func(t *testing.T) []system.Record {
		return recs(t, "01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 06 F5 01")
	}

	t.Run("IgnoreReset", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{PostResetWindow: time.Second})
		sr := e.scan(t, 2)
		e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{IgnoreReset: true})
		rep, err := e.s.WriteQuick(context.Background(), system.QuickSplit180, sr.Units, confirm, nil)
		be := kind(t, err, bus.KindNotBack)
		if len(be.Units) != 1 || be.Units[0].LongID != e.ids[1] || !rep.Reset {
			t.Fatalf("not back %v", be.Units)
		}
		// The record was written; the unit just does not use it yet.
		if got := system.Record(e.state(t, 1).Record); got != split(t)[1] {
			t.Errorf("stored %s", got)
		}
	})

	t.Run("SlowRestart", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		sr := e.scan(t, 2)
		e.sim.SetBehaviour(e.ids[0], simulator.Behaviour{ResetDuration: 1500 * time.Millisecond})
		rep, err := e.s.WriteQuick(context.Background(), system.QuickSplit180, sr.Units, confirm, nil)
		if err != nil {
			t.Fatal(err)
		}
		e.applied(t, rep, split(t))
	})

	t.Run("TooSlowRestart", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{PostResetWindow: 200 * time.Millisecond})
		sr := e.scan(t, 2)
		e.sim.SetBehaviour(e.ids[1], simulator.Behaviour{ResetDuration: 5 * time.Second})
		rep, err := e.s.WriteQuick(context.Background(), system.QuickSplit180, sr.Units, confirm, nil)
		be := kind(t, err, bus.KindNotBack)
		if len(be.Units) != 1 || be.Units[0].LongID != e.ids[1] || len(rep.After.Units) != 1 {
			t.Fatalf("not back %v, after %d units", be.Units, len(rep.After.Units))
		}
	})
}

func TestWriteCancel(t *testing.T) {
	t.Parallel()

	t.Run("DuringWrite", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemThreePhase(), bus.Options{})
		sr := e.scan(t, 3)
		before := e.records()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rep, err := e.s.WriteQuick(ctx, system.QuickParallel3, sr.Units, confirm, func(ev bus.Event) {
			if ev.Step == bus.StepWrite && ev.Done == 2 {
				cancel() // the attempt on unit 2 still finishes
			}
		})
		kind(t, err, bus.KindRolledBack)
		if !errors.Is(err, context.Canceled) || rep.Failed != nil || rep.Reset {
			t.Fatalf("error %v, failed %v, reset %v", err, rep.Failed, rep.Reset)
		}
		want := []bus.Status{bus.StatusRestored, bus.StatusRestored, bus.StatusNotWritten}
		for i, r := range rep.Units {
			if r.Status != want[i] {
				t.Errorf("unit %d: %v", i, r.Status)
			}
		}
		if rep.Units[1].Writes != 2 {
			t.Errorf("unit 1: %d writes", rep.Units[1].Writes)
		}
		for i, r := range e.records() {
			if r != before[i] || e.state(t, i).Resets != 0 {
				t.Errorf("unit %d: %s", i, r)
			}
		}
	})

	t.Run("AfterLastUnit", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		sr := e.scan(t, 2)
		before := e.records()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rep, err := e.s.WriteQuick(ctx, system.QuickSplit180, sr.Units, confirm, func(ev bus.Event) {
			if ev.Step == bus.StepWrite && ev.Done == 2 {
				cancel()
			}
		})
		kind(t, err, bus.KindRolledBack)
		if rep.Reset || e.records()[1] != before[1] {
			t.Fatalf("reset %v, records %v", rep.Reset, e.records())
		}
	})

	t.Run("DuringWait", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{ResetWait: time.Second})
		sr := e.scan(t, 2)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		rep, err := e.s.WriteQuick(ctx, system.QuickSplit180, sr.Units, confirm, func(ev bus.Event) {
			if ev.Step == bus.StepWait {
				cancel()
			}
		})
		kind(t, err, bus.KindNotBack)
		if !errors.Is(err, context.Canceled) || !rep.Reset {
			t.Fatalf("error %v, reset %v", err, rep.Reset)
		}
	})
}

func TestResetStandalone(t *testing.T) {
	t.Parallel()

	t.Run("OneUnitOn", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		e.sim.SetPowered(e.ids[0], false)
		other := e.state(t, 0)
		rep, err := e.s.ResetStandalone(context.Background(), confirm, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Units) != 1 || rep.Units[0].Unit.LongID != e.ids[1] || rep.Units[0].Status != bus.StatusVerified {
			t.Fatalf("units %+v", rep.Units)
		}
		u := e.state(t, 1)
		want := rec(t, "01 01 01 00 00 00 00 F0 00")
		if system.Record(u.Record) != want || system.Record(u.ActiveRecord) != want || u.Address != 0 || u.Writes != 1 {
			t.Errorf("unit: %+v", u)
		}
		if got := e.state(t, 0); got.Record != other.Record || got.Writes != 0 {
			t.Errorf("the other unit changed: %+v", got)
		}
		if len(rep.NotBack) != 0 || len(rep.After.Units) != 1 {
			t.Errorf("after: %+v", rep.After)
		}
	})

	t.Run("TwoUnitsOn", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{})
		_, err := e.s.ResetStandalone(context.Background(), confirm, nil)
		if be := kind(t, err, bus.KindUnitCount); be.Found != 2 {
			t.Fatalf("found %d", be.Found)
		}
		e.noWrites(t)
	})

	t.Run("DryRun", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(1), bus.Options{})
		rep, err := e.s.ResetStandalone(context.Background(), bus.WriteOptions{}, nil)
		if err != nil || !rep.DryRun || rep.Units[0].New != rec(t, "01 01 01 00 00 00 00 F0 00") {
			t.Fatalf("report %+v, error %v", rep, err)
		}
		e.noWrites(t)
	})
}

func TestReset(t *testing.T) {
	t.Parallel()
	e := start(t, simulator.SystemParallel2(), bus.Options{})
	e.scan(t, 2)
	rep, err := e.s.Reset(context.Background(), bus.WriteOptions{}, nil)
	if err != nil || !rep.DryRun || len(rep.Ops) != 4 {
		t.Fatalf("dry run: %+v, %v", rep, err)
	}
	e.noWrites(t)
	rep, err = e.s.Reset(context.Background(), confirm, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Reset || len(rep.After.Units) != 2 || e.state(t, 0).Resets == 0 || e.state(t, 0).Writes != 0 {
		t.Fatalf("report %+v", rep)
	}
}

func TestSessionLinkKeepAliveFlash(t *testing.T) {
	t.Parallel()

	t.Run("NineBit", func(t *testing.T) {
		t.Parallel()
		sim := simulator.New(simulator.Config{Devices: simulator.SystemFactory(1), VersionInterval: -1, NineBit: true})
		c := mk2.NewClient(sim.Port(), &mk2.Options{Timeout: 100 * time.Millisecond})
		defer c.Close()
		_, err := bus.Open(context.Background(), c, bus.Options{})
		kind(t, err, bus.KindLink)
	})

	t.Run("KeepAliveAndFlash", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemParallel2(), bus.Options{KeepAlivePeriod: 50 * time.Millisecond})
		ctx := context.Background()
		if err := e.s.FlashLEDs(ctx, e.ids[1], true); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		u := e.state(t, 1)
		if !u.Flashing || u.KeepAlives < 2 {
			t.Fatalf("flashing %v, keep-alives %d", u.Flashing, u.KeepAlives)
		}
		if err := e.s.Close(); err != nil {
			t.Fatal(err)
		}
		u = e.state(t, 1)
		time.Sleep(200 * time.Millisecond)
		if after := e.state(t, 1); after.Flashing || after.KeepAlives != u.KeepAlives {
			t.Fatalf("after Close: flashing %v, keep-alives %d → %d", after.Flashing, u.KeepAlives, after.KeepAlives)
		}
		if _, err := e.s.Scan(ctx, nil); err == nil {
			t.Fatal("Scan on a closed session")
		}
	})

	t.Run("NoKeepAlive", func(t *testing.T) {
		t.Parallel()
		e := start(t, simulator.SystemFactory(1), bus.Options{NoKeepAlive: true, KeepAlivePeriod: 20 * time.Millisecond})
		time.Sleep(100 * time.Millisecond)
		if n := e.state(t, 0).KeepAlives; n != 0 {
			t.Fatalf("%d keep-alives", n)
		}
	})
}
