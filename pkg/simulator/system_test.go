package simulator

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus/system"
)

type record = [mk2.SystemRecordLen]byte

// startSystem runs a simulator and a Client whose system link is prepared.
// Raw replies time out after 100 ms and units restart in 100 ms unless cfg
// or opts say otherwise.
func startSystem(t *testing.T, cfg Config, opts *mk2.Options) (*Simulator, *mk2.Client) {
	t.Helper()
	if cfg.ResetDuration == 0 {
		cfg.ResetDuration = 100 * time.Millisecond
	}
	o := mk2.Options{}
	if opts != nil {
		o = *opts
	}
	if o.RawTimeout == 0 {
		o.RawTimeout = 100 * time.Millisecond
	}
	s, c := start(t, cfg, &o)
	check(t, "PrepareSystemLink", c.PrepareSystemLink(context.Background()))
	return s, c
}

// scan finds the units as a host does: identification on for all, then
// Identify and switch identification off for each until none answers.
func scan(t *testing.T, c *mk2.Client) []mk2.Identity {
	t.Helper()
	ctx := context.Background()
	check(t, "SetIdentification(all, on)", c.SetIdentification(ctx, mk2.LongIDAll, true))
	var ids []mk2.Identity
	for {
		id, err := c.Identify(ctx)
		if errors.Is(err, mk2.ErrTimeout) {
			return ids
		}
		check(t, "Identify", err)
		if slices.ContainsFunc(ids, func(o mk2.Identity) bool { return o.LongID == id.LongID }) {
			t.Fatalf("unit 0x%08X answered twice", id.LongID)
		}
		ids = append(ids, id)
		check(t, "SetIdentification(off)", c.SetIdentification(ctx, id.LongID, false))
	}
}

func longIDs(ids []mk2.Identity) []uint32 {
	var ls []uint32
	for _, id := range ids {
		ls = append(ls, id.LongID)
	}
	return ls
}

func read(t *testing.T, c *mk2.Client, longID uint32) mk2.SystemConfig {
	t.Helper()
	sc, err := c.ReadSystemConfig(context.Background(), longID)
	check(t, "ReadSystemConfig", err)
	return sc
}

// waitRestart waits until no unit is restarting.
func waitRestart(t *testing.T, s *Simulator) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for slices.ContainsFunc(s.Units(), func(u UnitState) bool { return u.Restarting }) {
		if time.Now().After(deadline) {
			t.Fatal("units still restarting")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func unitState(t *testing.T, s *Simulator, longID uint32) UnitState {
	t.Helper()
	u, ok := s.Unit(longID)
	if !ok {
		t.Fatalf("no unit 0x%08X", longID)
	}
	return u
}

func hexRec(t *testing.T, s string) record {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil || len(b) != mk2.SystemRecordLen {
		t.Fatalf("bad record %q", s)
	}
	return record(b)
}

// first is the long ID of the first ready-made unit.
const first = firstLongID

func TestSystemDiscoverAndRead(t *testing.T) {
	for _, n := range []int{2, 3, 6} {
		t.Run(fmt.Sprintf("%d units", n), func(t *testing.T) {
			t.Parallel()
			s, c := startSystem(t, Config{Devices: SystemFactory(n)}, nil)
			ids := scan(t, c)
			if len(ids) != n {
				t.Fatalf("found %d units, want %d", len(ids), n)
			}
			for i, id := range ids {
				want := mk2.Identity{LongID: first + uint32(i), Flag: mk2.IdentifyFlag, VersionWord: Synthetic12V230().Version}
				if id != want {
					t.Errorf("unit %d: %+v, want %+v", i, id, want)
				}
				sc := read(t, c, id.LongID)
				if sc.ResponseChar != 0x02 || sc.Record != factoryRecord {
					t.Errorf("unit %d: %+v", i, sc)
				}
				if unitState(t, s, id.LongID).Identification {
					t.Errorf("unit %d still has identification on", i)
				}
			}
			// Every unit is muted: nobody answers.
			if _, err := c.Identify(context.Background()); !errors.Is(err, mk2.ErrTimeout) {
				t.Fatalf("Identify after the scan: %v", err)
			}
		})
	}
}

func TestSystemIdentificationMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := startSystem(t, Config{Devices: SystemFactory(3)}, nil)

	check(t, "off", c.SetIdentification(ctx, mk2.LongIDAll, false))
	if _, err := c.Identify(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("Identify with identification off: %v", err)
	}
	// Only the unit switched on answers, however often it is asked.
	check(t, "on", c.SetIdentification(ctx, first+2, true))
	for range 2 {
		if id, err := c.Identify(ctx); err != nil || id.LongID != first+2 {
			t.Fatalf("Identify = %+v, %v", id, err)
		}
	}
	// The lowest long ID with identification on answers first.
	check(t, "on", c.SetIdentification(ctx, first+1, true))
	if id, err := c.Identify(ctx); err != nil || id.LongID != first+1 {
		t.Fatalf("Identify = %+v, %v", id, err)
	}
	if u := unitState(t, s, first); u.Identification {
		t.Fatal("unit 0 switched on by a frame for another unit")
	}

	// IdentifyOff: identification starts off.
	devs := SystemFactory(2)
	devs[0].System.IdentifyOff = true
	_, c = startSystem(t, Config{Devices: devs}, nil)
	if id, err := c.Identify(ctx); err != nil || id.LongID != first+1 {
		t.Fatalf("Identify with IdentifyOff = %+v, %v", id, err)
	}
}

// TestSystemWriteResetDiscover writes records, checks that they are stored
// but not active until the reset, and finds the units at their new
// addresses afterwards.
func TestSystemWriteResetDiscover(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := startSystem(t, Config{Devices: SystemFactory(3), ResetDuration: 300 * time.Millisecond}, nil)
	recs := []record{
		hexRec(t, "01 01 07 00 00 00 00 F8 00"),
		hexRec(t, "01 01 07 00 00 00 04 F9 01"),
		hexRec(t, "01 01 07 00 00 00 08 FA 02"),
	}
	check(t, "FlashLEDs", c.FlashLEDs(ctx, first+1, true))
	for i, r := range recs {
		check(t, "WriteSystemConfig", c.WriteSystemConfig(ctx, first+uint32(i), r))
		if sc := read(t, c, first+uint32(i)); sc.Record != r {
			t.Fatalf("unit %d read back % X", i, sc.Record)
		}
		if u := unitState(t, s, first+uint32(i)); u.Record != r || u.ActiveRecord != factoryRecord || u.Address != 0 || u.Writes != 1 {
			t.Fatalf("unit %d before reset: %+v", i, u)
		}
	}
	check(t, "SetIdentification", c.SetIdentification(ctx, mk2.LongIDAll, false))

	check(t, "ResetSystem", c.ResetSystem(ctx))
	// Restarting units answer nothing.
	if !unitState(t, s, first).Restarting {
		t.Fatal("unit not restarting after the reset")
	}
	if _, err := c.ReadSystemConfig(ctx, first); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("read while restarting: %v", err)
	}
	waitRestart(t, s)
	for i, r := range recs {
		u := unitState(t, s, first+uint32(i))
		if u.ActiveRecord != r || u.Address != r[8] || u.Resets != 1 || u.Flashing || !u.Identification {
			t.Fatalf("unit %d after reset: %+v", i, u)
		}
	}
	ids := scan(t, c)
	if len(ids) != 3 {
		t.Fatalf("found %d units after the reset", len(ids))
	}
	for i, id := range ids {
		if id.ShortAddress != byte(i) || read(t, c, id.LongID).Record != recs[i] {
			t.Fatalf("unit %d after the reset: %+v", i, id)
		}
	}

	// Winmon requests now reach each unit at its own address.
	for a := range byte(3) {
		check(t, "SetAddress", c.SetAddress(ctx, a))
		if _, err := c.SoftwareVersion(ctx); err != nil {
			t.Fatalf("SoftwareVersion at %d: %v", a, err)
		}
	}
}

// quick returns a function that builds the records of the units in
// unit order with q.
func quick(q system.QuickLayout) func(*testing.T, []*system.Unit) []system.UnitRecord {
	return func(t *testing.T, us []*system.Unit) []system.UnitRecord {
		recs, err := q.Records(us, true)
		check(t, "Records", err)
		return recs
	}
}

// TestSystemScenarios builds records for the worked examples of the spec
// with pkg/vebus/system, writes them through the simulator, resets, scans
// again and rebuilds the layout from what the units report.
func TestSystemScenarios(t *testing.T) {
	sixPerPhase := func(t *testing.T, us []*system.Unit) []system.UnitRecord {
		l := system.NewLayout()
		check(t, "Reshape", l.Reshape(system.ThreePhase, 1))
		for i, u := range us {
			check(t, "Add", l.Add(u))
			check(t, "Place", l.Place(u, system.Cell{Phase: i / 2, AC: 0}))
		}
		return l.Records(true)
	}
	for _, tc := range []struct {
		name  string
		build func(*testing.T, []*system.Unit) []system.UnitRecord
		quick bool
		want  []string // per unit, in long ID order
	}{
		{"E1 standalone", quick(system.QuickStandalone), true, []string{"01 01 01 00 00 00 00 F0 00"}},
		{"E2 parallel", quick(system.QuickParallel2), true, []string{
			"02 02 09 00 00 00 00 F0 00", "FF FF 09 00 00 00 00 F0 03"}},
		{"E3 parallel 3", quick(system.QuickParallel3), true, []string{
			"03 03 49 00 00 00 00 F0 00", "FF FF 49 00 00 00 00 F0 03", "FF FF 49 00 00 00 00 F0 06"}},
		{"E4 split 120", quick(system.QuickSplit120), true, []string{
			"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 04 F5 01"}},
		{"E6 three phase", quick(system.QuickThreePhase), true, []string{
			"01 01 07 00 00 00 00 F8 00", "01 01 07 00 00 00 04 F9 01", "01 01 07 00 00 00 08 FA 02"}},
		{"E7 three phase 2 per phase", sixPerPhase, false, []string{
			"02 02 3F 00 00 00 00 F8 00", "FF FF 3F 00 00 00 00 F8 03",
			"02 02 3F 00 00 00 04 F9 01", "FF FF 3F 00 00 00 04 F9 04",
			"02 02 3F 00 00 00 08 FA 02", "FF FF 3F 00 00 00 08 FA 05"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			n := len(tc.want)
			s, c := startSystem(t, Config{Devices: SystemFactory(n)}, nil)
			units := discover(t, c, n)
			for _, r := range tc.build(t, units) {
				check(t, "WriteSystemConfig", c.WriteSystemConfig(ctx, r.Unit.LongID, r.Record))
				if got := read(t, c, r.Unit.LongID).Record; got != r.Record {
					t.Fatalf("unit 0x%08X read back % X, wrote % X", r.Unit.LongID, got, r.Record)
				}
			}
			check(t, "ResetSystem", c.ResetSystem(ctx))
			waitRestart(t, s)

			units = discover(t, c, n)
			for i, u := range units {
				if want := hexRec(t, tc.want[i]); record(u.Record) != want {
					t.Errorf("unit %d: % X, want % X", i, u.Record, want)
				}
			}
			l, issue := system.FromRecords(units)
			if issue != nil {
				t.Fatalf("FromRecords: %v", issue)
			}
			if issues := l.Check(); system.HasErrors(issues) {
				t.Fatalf("Check: %v", issues)
			}
			if _, _, ok := system.RecognizeQuick(units); ok != tc.quick {
				t.Fatalf("RecognizeQuick = %v", ok)
			}
		})
	}
}

// discover scans and reads every unit, sorted by long ID, as system.Units.
func discover(t *testing.T, c *mk2.Client, n int) []*system.Unit {
	t.Helper()
	ids := scan(t, c)
	if len(ids) != n {
		t.Fatalf("found %d units, want %d", len(ids), n)
	}
	var us []*system.Unit
	for _, id := range ids {
		sc := read(t, c, id.LongID)
		if id.ShortAddress != sc.Record[8] {
			t.Fatalf("unit 0x%08X: address %d, record % X", id.LongID, id.ShortAddress, sc.Record)
		}
		us = append(us, &system.Unit{
			LongID: id.LongID, VersionWord: id.VersionWord, Letter: id.Letter, ConfigProtocol: id.ConfigProtocol,
			IdentifyFlag: id.Flag, ResponseChar: sc.ResponseChar, Record: system.Record(sc.Record),
		})
	}
	slices.SortFunc(us, func(a, b *system.Unit) int { return cmp.Compare(a.LongID, b.LongID) })
	return us
}

func TestSystemPresets(t *testing.T) {
	for _, tc := range []struct {
		name  string
		devs  []Device
		rules []system.Rule
	}{
		{"parallel2", SystemParallel2(), nil},
		{"three phase", SystemThreePhase(), nil},
		{"mixed firmware", SystemMixedFirmware(), []system.Rule{system.RuleFirmware}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, c := startSystem(t, Config{Devices: tc.devs}, nil)
			units := discover(t, c, len(tc.devs))
			l, issue := system.FromRecords(units)
			if issue != nil {
				t.Fatalf("FromRecords: %v", issue)
			}
			var rules []system.Rule
			for _, is := range l.Check() {
				if is.Severity == system.Error {
					rules = append(rules, is.Rule)
				}
			}
			if !slices.Equal(rules, tc.rules) {
				t.Fatalf("errors %v, want %v", rules, tc.rules)
			}
		})
	}
	if ds := SystemFactory(4); len(ds) != 4 || ds[3].System.LongID != first+3 || ds[3].Address != 0 {
		t.Fatalf("SystemFactory(4) = %+v", ds)
	}
}

func TestSystemFlashFreezeKeepAlive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
	check(t, "FlashLEDs", c.FlashLEDs(ctx, first, true))
	if !unitState(t, s, first).Flashing || unitState(t, s, first+1).Flashing {
		t.Fatal("flashing on")
	}
	check(t, "FlashLEDs", c.FlashLEDs(ctx, first, false))
	if unitState(t, s, first).Flashing {
		t.Fatal("flashing off")
	}
	check(t, "Freeze", c.Freeze(ctx, first+1))
	if !unitState(t, s, first+1).Frozen || unitState(t, s, first).Frozen {
		t.Fatal("frozen")
	}
	// A frozen unit still answers (the effect of freeze is unknown).
	read(t, c, first+1)

	s.SetPowered(first+1, false)
	before := time.Now()
	check(t, "KeepAlive", c.KeepAlive(ctx))
	check(t, "KeepAlive", c.KeepAlive(ctx))
	if u := unitState(t, s, first); u.KeepAlives != 2 || u.LastKeepAlive.Before(before) {
		t.Fatalf("keep-alive: %+v", u)
	}
	if u := unitState(t, s, first+1); u.KeepAlives != 0 {
		t.Fatalf("switched-off unit counted keep-alives: %+v", u)
	}
	// Powering on ends flashing and freeze.
	s.SetPowered(first+1, true)
	if u := unitState(t, s, first+1); u.Frozen || !u.Powered {
		t.Fatalf("after power-on: %+v", u)
	}
}

func TestSystemLinkMode(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// The 7-byte 'S' of PrepareSystemLink selects Winmon mode 2.
	s, _ := startSystem(t, Config{Devices: SystemFactory(1)}, nil)
	s.mu.Lock()
	mode := s.mode
	s.mu.Unlock()
	if mode != mk2.WinmonLong {
		t.Fatalf("Winmon mode %v after PrepareSystemLink", mode)
	}

	// 9-bit mode: the host refuses raw frames, and the MK ignores them.
	_, c := start(t, Config{Devices: SystemFactory(1), NineBit: true}, nil)
	if err := c.PrepareSystemLink(ctx); !errors.Is(err, mk2.ErrLinkMode) {
		t.Fatalf("PrepareSystemLink in 9-bit mode: %v", err)
	}
	r := newRawLine(t, Config{Devices: SystemFactory(1), NineBit: true})
	if f := r.next(); f.Command != mk2.CmdVersion || f.Data[4] != mk2.VersionModeRS485 {
		t.Fatalf("power-up frame = %+v", f)
	}
	b, err := mk2.EncodeRaw(mk2.SystemPayload(mk2.LongIDAll, mk2.SysIdentify))
	check(t, "EncodeRaw", err)
	if _, err := r.p.Write(b); err != nil {
		t.Fatal(err)
	}
	r.send(mk2.Frame{Command: mk2.CmdVersion})
	if f := r.next(); f.VEBus || f.Command != mk2.CmdVersion {
		t.Fatalf("frame after a raw identify in 9-bit mode = %+v", f)
	}

	// A raw frame with a bad checksum is dropped; the next one is handled.
	r = newRawLine(t, Config{Devices: SystemFactory(1)})
	r.next() // power-up 'V'
	bad := slices.Clone(b)
	bad[len(bad)-1]++
	v, err := mk2.Frame{Command: mk2.CmdVersion}.Encode()
	check(t, "Encode", err)
	if _, err := r.p.Write(slices.Concat(bad, b, v)); err != nil {
		t.Fatal(err)
	}
	if f := r.next(); !f.VEBus || f.Command != mk2.FrameSystem {
		t.Fatalf("reply = %+v", f)
	}
	if f := r.next(); f.VEBus || f.Command != mk2.CmdVersion {
		t.Fatalf("second reply = %+v", f)
	}
}

func TestSystemFaultRules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
	rec := hexRec(t, "01 01 01 00 00 00 00 F0 00")

	// Drop: the write never arrives.
	s.InjectFault(FaultRule{Raw: true, SysCmd: mk2.SysWriteConfig, Fault: Drop, Count: 1})
	check(t, "WriteSystemConfig", c.WriteSystemConfig(ctx, first, rec))
	if u := unitState(t, s, first); u.Record != factoryRecord || u.Writes != 0 {
		t.Fatalf("dropped write: %+v", u)
	}
	check(t, "WriteSystemConfig", c.WriteSystemConfig(ctx, first, rec))
	if u := unitState(t, s, first); u.Record != rec {
		t.Fatalf("write: %+v", u)
	}

	// NoReply: the read is handled, the reply lost.
	s.InjectFault(FaultRule{Raw: true, SysCmd: mk2.SysReadConfig, Fault: NoReply, Count: 1})
	if _, err := c.ReadSystemConfig(ctx, first); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("NoReply read: %v", err)
	}
	read(t, c, first)

	// Corrupt: the reply arrives garbled.
	s.InjectFault(FaultRule{Raw: true, SysCmd: mk2.SysReadConfig, Fault: Corrupt, Count: 1})
	if _, err := c.ReadSystemConfig(ctx, first); !errors.Is(err, mk2.ErrTimeout) || !errors.Is(err, mk2.ErrChecksum) {
		t.Fatalf("Corrupt read: %v", err)
	}
	read(t, c, first)

	// SysCmd 0 is SysIdentify, not "any".
	s.InjectFault(FaultRule{Raw: true, SysCmd: mk2.SysIdentify, Fault: Drop, Count: 1})
	read(t, c, first)
	if _, err := c.Identify(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("dropped identify: %v", err)
	}
	if id, err := c.Identify(ctx); err != nil || id.LongID != first {
		t.Fatalf("Identify = %+v, %v", id, err)
	}

	// Raw rules do not match MK2 frames, other rules not raw frames.
	s.InjectFault(FaultRule{Raw: true, SysCmd: mk2.CmdVersion, Fault: Drop})
	s.InjectFault(FaultRule{Fault: Drop})
	read(t, c, first)
	if _, err := c.Version(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("Version with every MK2 frame dropped: %v", err)
	}
	s.ClearFaults()
	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("Version: %v", err)
	}
}

func TestSystemBehaviours(t *testing.T) {
	ctx := context.Background()
	newRec := hexRec(t, "02 02 09 00 00 00 00 F0 00")

	t.Run("NoReadReply", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
		s.SetBehaviour(first, Behaviour{NoReadReply: true})
		if _, err := c.ReadSystemConfig(ctx, first); !errors.Is(err, mk2.ErrTimeout) {
			t.Fatalf("read: %v", err)
		}
		read(t, c, first+1)
	})

	t.Run("IgnoreWrites", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(1)}, nil)
		s.SetBehaviour(first, Behaviour{IgnoreWrites: true})
		check(t, "write", c.WriteSystemConfig(ctx, first, newRec))
		if got := read(t, c, first).Record; got != factoryRecord {
			t.Fatalf("read back % X", got)
		}
		if u := unitState(t, s, first); u.Writes != 1 {
			t.Fatalf("writes %d", u.Writes)
		}
	})

	t.Run("CorruptWrites", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(1)}, nil)
		s.SetBehaviour(first, Behaviour{CorruptWrites: true})
		check(t, "write", c.WriteSystemConfig(ctx, first, newRec))
		got := read(t, c, first).Record
		if got == newRec || got[0] != ^newRec[0] || !slices.Equal(got[1:], newRec[1:]) {
			t.Fatalf("read back % X", got)
		}
	})

	t.Run("StaleReads", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(1)}, nil)
		s.SetBehaviour(first, Behaviour{StaleReads: 2})
		read(t, c, first) // before any write: not counted
		check(t, "write", c.WriteSystemConfig(ctx, first, newRec))
		var got []record
		for range 2 {
			got = append(got, read(t, c, first).Record)
			// The host writes again after a mismatch; the same record
			// does not restart the count.
			check(t, "write", c.WriteSystemConfig(ctx, first, newRec))
		}
		got = append(got, read(t, c, first).Record)
		if !slices.Equal(got, []record{factoryRecord, factoryRecord, newRec}) {
			t.Fatalf("reads % X", got)
		}
	})

	t.Run("IgnoreMute", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
		s.SetBehaviour(first, Behaviour{IgnoreMute: true})
		for range 2 {
			id, err := c.Identify(ctx)
			if err != nil || id.LongID != first {
				t.Fatalf("Identify = %+v, %v", id, err)
			}
			check(t, "mute", c.SetIdentification(ctx, id.LongID, false))
		}
	})

	t.Run("UnsolicitedIdentify", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
		check(t, "off", c.SetIdentification(ctx, mk2.LongIDAll, false))
		s.SetBehaviour(first+1, Behaviour{UnsolicitedIdentify: true})
		// Without identify requests: only the unsolicited reply can arrive.
		s.InjectFault(FaultRule{Raw: true, SysCmd: mk2.SysIdentify, Fault: Drop})
		check(t, "on", c.SetIdentification(ctx, mk2.LongIDAll, true))
		if id, err := c.Identify(ctx); err != nil || id.LongID != first+1 {
			t.Fatalf("Identify = %+v, %v", id, err)
		}
	})

	t.Run("PowerOffOnMute", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
		s.SetBehaviour(first, Behaviour{PowerOffOnMute: true})
		if ids := scan(t, c); !slices.Equal(longIDs(ids), []uint32{first, first + 1}) {
			t.Fatalf("scan = %v", ids)
		}
		if _, err := c.ReadSystemConfig(ctx, first); !errors.Is(err, mk2.ErrTimeout) {
			t.Fatalf("read of the lost unit: %v", err)
		}
		if unitState(t, s, first).Powered {
			t.Fatal("unit still on")
		}
	})

	t.Run("PowerOffOnWrite", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(2)}, nil)
		s.SetBehaviour(first+1, Behaviour{PowerOffOnWrite: true})
		check(t, "write", c.WriteSystemConfig(ctx, first+1, newRec))
		if _, err := c.ReadSystemConfig(ctx, first+1); !errors.Is(err, mk2.ErrTimeout) {
			t.Fatalf("read after drop-off: %v", err)
		}
		if u := unitState(t, s, first+1); u.Powered || u.Record != factoryRecord || u.Writes != 1 {
			t.Fatalf("after drop-off: %+v", u)
		}
		s.SetPowered(first+1, true)
		if got := read(t, c, first+1).Record; got != factoryRecord {
			t.Fatalf("after power-on: % X", got)
		}
	})

	t.Run("IgnoreReset and ResetDuration", func(t *testing.T) {
		t.Parallel()
		s, c := startSystem(t, Config{Devices: SystemFactory(3), ResetDuration: 50 * time.Millisecond}, nil)
		s.SetBehaviour(first, Behaviour{IgnoreReset: true})
		s.SetBehaviour(first+2, Behaviour{ResetDuration: time.Hour})
		check(t, "write", c.WriteSystemConfig(ctx, first, newRec))
		check(t, "SetIdentification", c.SetIdentification(ctx, mk2.LongIDAll, false))
		check(t, "ResetSystem", c.ResetSystem(ctx))
		time.Sleep(100 * time.Millisecond)
		if u := unitState(t, s, first); u.Resets != 0 || u.ActiveRecord != factoryRecord || u.Restarting || u.Identification {
			t.Fatalf("unit ignoring reset: %+v", u)
		}
		if u := unitState(t, s, first+1); u.Resets != 1 || u.Restarting || !u.Identification {
			t.Fatalf("unit with the bus reset time: %+v", u)
		}
		if u := unitState(t, s, first+2); !u.Restarting {
			t.Fatalf("unit with a long reset time: %+v", u)
		}
		// The restarting unit is missing; the one that ignored the reset
		// answers too.
		if ids := scan(t, c); !slices.Equal(longIDs(ids), []uint32{first, first + 1}) {
			t.Fatalf("scan after reset = %v", ids)
		}
	})

	t.Run("identity variants", func(t *testing.T) {
		t.Parallel()
		p := Synthetic12V230()
		p.Version = 2699553 // ends in 553: the host reads configuration protocol 0
		devs := []Device{
			SystemDevice(SystemUnit{LongID: 1, ResponseChar: mk2.ResponseCharNoConfig, Letter: 3, ConfigProtocol: 2}),
			{Profile: p, System: &SystemUnit{LongID: 2, VersionWord: 0x07000000 | 2699553, ConfigProtocol: 2}},
			SystemDevice(SystemUnit{LongID: 3, IdentifyFlag: mk2.IdentifyFlagAlt, ResponseChar: mk2.ResponseCharDualInput}),
		}
		_, c := startSystem(t, Config{Devices: devs}, nil)
		check(t, "off", c.SetIdentification(ctx, mk2.LongIDAll, false))
		identify := func(c *mk2.Client, id uint32) (mk2.Identity, error) {
			check(t, "on", c.SetIdentification(ctx, id, true))
			defer c.SetIdentification(ctx, id, false)
			return c.Identify(ctx)
		}
		if id, err := identify(c, 1); err != nil || id.ConfigProtocol != 2 || id.Letter != 3 {
			t.Fatalf("unit 1: %+v, %v", id, err)
		}
		if sc := read(t, c, 1); sc.ResponseChar != mk2.ResponseCharNoConfig {
			t.Fatalf("unit 1 response char 0x%02X", sc.ResponseChar)
		}
		if id, err := identify(c, 2); err != nil || id.ConfigProtocol != 0 || id.VersionWord != 0x07000000|2699553 {
			t.Fatalf("unit 2: %+v, %v", id, err)
		}
		if _, err := identify(c, 3); !errors.Is(err, mk2.ErrTimeout) || !errors.Is(err, mk2.ErrUnexpectedReply) {
			t.Fatalf("unit 3 (flag 0x11): %v", err)
		}
		if sc := read(t, c, 3); sc.ResponseChar != mk2.ResponseCharDualInput {
			t.Fatalf("unit 3 response char 0x%02X", sc.ResponseChar)
		}
		_, c = startSystem(t, Config{Devices: devs}, &mk2.Options{AcceptIdentify11: true})
		check(t, "off", c.SetIdentification(ctx, mk2.LongIDAll, false))
		if id, err := identify(c, 3); err != nil || id.Flag != mk2.IdentifyFlagAlt {
			t.Fatalf("unit 3 with AcceptIdentify11: %+v, %v", id, err)
		}
	})
}

// TestSystemSharedAddress reads setting 85 as a scan does: units sharing
// an address collide until only one is switched on.
func TestSystemSharedAddress(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	devs := SystemFactory(2)
	devs[1].System.ExternalBF = true
	s, c := startSystem(t, Config{Devices: devs}, nil)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	if _, err := c.SettingInfo(ctx, setting85); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("SettingInfo with two units at address 0: %v", err)
	}
	for _, tc := range []struct {
		on  uint32
		max uint16
	}{{first, 1}, {first + 1, 0}} {
		s.SetPowered(first, tc.on == first)
		s.SetPowered(first+1, tc.on == first+1)
		info, err := c.SettingInfo(ctx, setting85)
		if err != nil || info.Sc == 0 || info.Max&1 != tc.max {
			t.Fatalf("unit 0x%08X: %+v, %v", tc.on, info, err)
		}
	}
	// A switched-off unit answers nothing.
	s.SetPowered(first+1, false)
	if _, err := c.SoftwareVersion(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("SoftwareVersion with every unit off: %v", err)
	}
}

// TestSystemSwitchOnOneAtATime scripts the Quick Configure procedure: the
// units are switched on one at a time and a scan after each finds one more.
func TestSystemSwitchOnOneAtATime(t *testing.T) {
	t.Parallel()
	devs := SystemFactory(3)
	for i := range devs {
		devs[i].System.Off = true
	}
	s, c := startSystem(t, Config{Devices: devs}, nil)
	if ids := scan(t, c); len(ids) != 0 {
		t.Fatalf("scan with every unit off = %v", ids)
	}
	var want []uint32
	for i := range uint32(3) {
		if !s.SetPowered(first+i, true) {
			t.Fatal("SetPowered")
		}
		want = append(want, first+i)
		if ids := scan(t, c); !slices.Equal(longIDs(ids), want) {
			t.Fatalf("after switching on %d units: %v", i+1, longIDs(ids))
		}
	}
	if s.SetPowered(0x12345678, true) || s.SetBehaviour(0x12345678, Behaviour{}) {
		t.Fatal("unknown unit accepted")
	}
	if _, ok := s.Unit(0x12345678); ok {
		t.Fatal("Unit(unknown) found")
	}
}

func TestNewRejectsBadSystemUnits(t *testing.T) {
	bad := SystemDevice(SystemUnit{LongID: 1})
	bad.Address = 1
	wrongVersion := SystemDevice(SystemUnit{LongID: 1})
	wrongVersion.System.VersionWord = 1234567
	for i, devs := range [][]Device{
		{SystemDevice(SystemUnit{LongID: 1}), SystemDevice(SystemUnit{LongID: 1})},
		{SystemDevice(SystemUnit{})},
		{SystemDevice(SystemUnit{LongID: 0xFFFFFFFF})},
		{bad},
		{wrongVersion},
		{SystemDevice(SystemUnit{LongID: 1}), {Address: 0, Profile: Synthetic12V230()}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("case %d: New did not panic", i)
				}
			}()
			New(Config{Devices: devs, VersionInterval: -1})
		}()
	}
}
