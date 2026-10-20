package vebus

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// countingPort records the Winmon commands the host sends, so tests can
// count write frames.
type countingPort struct {
	*simulator.Port
	mu   sync.Mutex
	cmds []mk2.WinmonRequest
}

func (p *countingPort) Write(b []byte) (int, error) {
	if f, err := mk2.Decode(b); err == nil {
		if r, err := mk2.ParseWinmonRequest(f); err == nil {
			p.mu.Lock()
			p.cmds = append(p.cmds, r)
			p.mu.Unlock()
		}
	}
	return p.Port.Write(b)
}

// count returns how many cmd requests were sent, and resets the log.
func (p *countingPort) count(cmd byte) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, r := range p.cmds {
		if r.Cmd == cmd {
			n++
		}
	}
	return n
}

func (p *countingPort) reset() {
	p.mu.Lock()
	p.cmds = nil
	p.mu.Unlock()
}

type syncEnv struct {
	sim  *simulator.Simulator
	port *countingPort
	c    *mk2.Client
	ctx  context.Context
}

// newSyncEnv starts the two-device simulator with a short reply timeout.
// With long, the link is switched to Winmon mode 2.
func newSyncEnv(t *testing.T, long bool, devs ...simulator.Device) *syncEnv {
	t.Helper()
	if devs == nil {
		devs = simDevices()
	}
	sim := simulator.New(simulator.Config{Devices: devs, VersionInterval: -1})
	p := &countingPort{Port: sim.Port()}
	c := mk2.NewClient(p, &mk2.Options{Timeout: 100 * time.Millisecond})
	t.Cleanup(func() { c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	if long {
		st := mk2.State{Switch: mk2.SwitchOn, Flags: mk2.StateFlagNoSend | mk2.StateFlagLimitDeciAmps, Limit: 0x8000, Ext: &mk2.StateExt{}}
		if err := c.SetState(ctx, st); err != nil {
			t.Fatal(err)
		}
	}
	return &syncEnv{sim: sim, port: p, c: c, ctx: ctx}
}

func (e *syncEnv) read(t *testing.T, addr byte) *Device {
	t.Helper()
	d, _, err := ReadAll(e.ctx, e.c, addr, nil, ReadOptions{RAM: []uint16{}})
	if err != nil {
		t.Fatal(err)
	}
	e.port.reset()
	return d
}

// deviceRaw reads setting id from the simulator, bypassing the model.
func (e *syncEnv) deviceRaw(t *testing.T, addr byte, id uint16) uint16 {
	t.Helper()
	if err := e.c.SetAddress(e.ctx, addr); err != nil {
		t.Fatal(err)
	}
	v, err := e.c.ReadSetting(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestReadAll(t *testing.T) {
	e := newSyncEnv(t, false)
	var calls, total int
	d, rep, err := ReadAll(e.ctx, e.c, 1, nil, ReadOptions{Progress: func(done, n int) { calls, total = done, n }})
	if err != nil {
		t.Fatal(err)
	}
	if calls != total || total != len(readList(DefaultCatalogue(), ReadOptions{})) {
		t.Errorf("progress %d of %d", calls, total)
	}
	p := simulator.Synthetic48V120()
	if len(rep.Read) != len(p.Settings)+len(p.RAMVars) || len(rep.Failed) != 0 ||
		len(rep.Read)+len(rep.Unsupported) != total {
		t.Errorf("read %d, unsupported %d, failed %v", len(rep.Read), len(rep.Unsupported), rep.Failed)
	}
	if d.Firmware != p.Version || len(d.SettingIDs()) != len(p.Settings) {
		t.Errorf("firmware %d, settings %v", d.Firmware, d.SettingIDs())
	}

	// IDs outside the catalogue are read when listed.
	d, rep, err = ReadAll(e.ctx, e.c, 0, nil, ReadOptions{Settings: []uint16{2, 200}, RAM: []uint16{}})
	if err != nil || !slices.Equal(d.SettingIDs(), []uint16{2}) || !slices.Equal(rep.Unsupported, []ID{SettingID(200)}) {
		t.Errorf("listed IDs: %v, %+v, %v", d.SettingIDs(), rep, err)
	}
}

func TestReadAllRetriesTimeouts(t *testing.T) {
	e := newSyncEnv(t, false)
	only := ReadOptions{Settings: []uint16{2, 3}, RAM: []uint16{}}

	// One lost reply: the retry reads it.
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdGetSettingInfo, Fault: simulator.NoReply, Count: 1})
	if _, rep, err := ReadAll(e.ctx, e.c, 0, nil, only); err != nil || len(rep.Read) != 2 {
		t.Fatalf("one timeout: %+v, %v", rep, err)
	}

	// Two lost replies: setting 2 fails and, by default, ends the read.
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdGetSettingInfo, Fault: simulator.NoReply, Count: 2})
	d, rep, err := ReadAll(e.ctx, e.c, 0, nil, only)
	var ie *IDError
	if !errors.As(err, &ie) || ie.ID != SettingID(2) || !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if len(rep.Failed) != 1 || len(rep.Read) != 0 || d.Supported(SettingID(3)) {
		t.Errorf("stop on error: %+v", rep)
	}
	if e.port.count(mk2.WCmdGetSettingInfo) != 3+2 { // 3 above, 2 here
		t.Errorf("info requests: %d", e.port.count(mk2.WCmdGetSettingInfo))
	}

	// With ContinueOnError the read goes on.
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdGetSettingInfo, Fault: simulator.NoReply, Count: 2})
	only.ContinueOnError = true
	d, rep, err = ReadAll(e.ctx, e.c, 0, nil, only)
	if !errors.Is(err, mk2.ErrTimeout) || len(rep.Failed) != 1 || !slices.Equal(rep.Read, []ID{SettingID(3)}) || !d.Supported(SettingID(3)) {
		t.Errorf("continue: %+v, %v", rep, err)
	}

	// Without retries one lost reply fails, as Load does.
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdGetSettingInfo, Fault: simulator.NoReply, Count: 1})
	if _, err := Load(e.ctx, e.c, 0, nil); !errors.Is(err, mk2.ErrTimeout) {
		t.Errorf("Load: %v", err)
	}
}

func TestReadAllCancel(t *testing.T) {
	e := newSyncEnv(t, false)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	last := 0
	_, rep, err := ReadAll(ctx, e.c, 0, nil, ReadOptions{ContinueOnError: true, Progress: func(done, _ int) {
		last = done
		if done == 3 {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) || last != 3 || len(rep.Failed) != 0 {
		t.Errorf("cancel: %v after %d, %+v", err, last, rep.Failed)
	}
}

func TestWriteChangedSendsOnlyModified(t *testing.T) {
	e := newSyncEnv(t, false)
	d := e.read(t, 0)
	if err := d.SetDisplay(SettingID(2), 14.6); err != nil {
		t.Fatal(err)
	}
	if err := d.SetFlag(4, true); err != nil {
		t.Fatal(err)
	}
	var progress int
	rep, err := Write(e.ctx, e.c, d, WriteOptions{Progress: func(done, _ int) { progress = done }})
	if err != nil {
		t.Fatal(err)
	}
	// The flag word goes first.
	if len(rep.Results) != 2 || rep.Results[0].ID != SettingID(0) || rep.Results[1].ID != SettingID(2) || progress != 2 {
		t.Fatalf("results %+v", rep.Results)
	}
	for _, r := range rep.Results {
		if r.Status != StatusVerified || r.Err != nil || !r.OldKnown {
			t.Errorf("%+v", r)
		}
	}
	if r := rep.Results[0]; r.Old != 0x0080 || r.New != 0x0090 {
		t.Errorf("flag word %#x -> %#x", r.Old, r.New)
	}
	if r := rep.Results[1]; r.Old != 1440 || r.New != 1460 {
		t.Errorf("setting 2: %d -> %d", r.Old, r.New)
	}
	// Mode 1: CommandWriteSetting + CommandWriteData, once per changed ID.
	if w, data, via := e.port.count(mk2.WCmdWriteSetting), e.port.count(mk2.WCmdWriteData), e.port.count(mk2.WCmdWriteViaID); w != 2 || data != 2 || via != 0 {
		t.Errorf("write frames: 0x33 %d, 0x34 %d, 0x37 %d", w, data, via)
	}
	if len(d.ChangedIDs()) != 0 {
		t.Errorf("still changed: %v", d.ChangedIDs())
	}
	if e.deviceRaw(t, 0, 2) != 1460 || e.deviceRaw(t, 0, 0) != 0x0090 {
		t.Error("device not written")
	}

	// Nothing changed: nothing is written.
	e.port.reset()
	if rep, err := Write(e.ctx, e.c, d, WriteOptions{}); err != nil || len(rep.Results) != 0 || e.port.count(mk2.WCmdWriteData) != 0 {
		t.Errorf("no changes: %+v, %v", rep, err)
	}
}

func TestWriteAll(t *testing.T) {
	e := newSyncEnv(t, false)
	d := e.read(t, 1)
	rep, err := Write(e.ctx, e.c, d, WriteOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	n := len(simulator.Synthetic48V120().Settings)
	if len(rep.Results) != n || e.port.count(mk2.WCmdWriteData) != n {
		t.Errorf("%d results, %d writes, want %d", len(rep.Results), e.port.count(mk2.WCmdWriteData), n)
	}
	for _, r := range rep.Results {
		if r.Status != StatusVerified || r.New != r.Old {
			t.Errorf("%+v", r)
		}
	}
}

func TestWriteDryRunAndValidation(t *testing.T) {
	e := newSyncEnv(t, false)
	d := e.read(t, 0)
	if err := d.SetDisplay(SettingID(2), 14.6); err != nil {
		t.Fatal(err)
	}
	rep, err := Write(e.ctx, nil, d, WriteOptions{DryRun: true})
	if err != nil || len(rep.Results) != 1 || rep.Results[0].Status != StatusPlanned || rep.Results[0].New != 1460 {
		t.Fatalf("dry run: %+v, %v", rep, err)
	}

	// Float above absorption is a validation error: nothing is sent.
	if err := d.SetDisplay(SettingID(3), 15); err != nil {
		t.Fatal(err)
	}
	rep, err = Write(e.ctx, e.c, d, WriteOptions{})
	var ve *ValidationError
	if !errors.As(err, &ve) || len(rep.Results) != 2 || rep.Results[0].Status != StatusSkipped {
		t.Fatalf("validation: %+v, %v", rep, err)
	}
	if e.port.count(mk2.WCmdWriteData)+e.port.count(mk2.WCmdWriteSetting) != 0 {
		t.Error("a write was sent")
	}
	// The override writes anyway.
	if _, err := Write(e.ctx, e.c, d, WriteOptions{Override: true}); err != nil {
		t.Fatal(err)
	}
	if e.port.count(mk2.WCmdWriteData) != 2 {
		t.Errorf("override: %d writes", e.port.count(mk2.WCmdWriteData))
	}
}

func TestWriteTimeoutRetry(t *testing.T) {
	e := newSyncEnv(t, false)
	d := e.read(t, 0)
	_ = d.SetDisplay(SettingID(2), 14.6)

	// One lost write: retried after the read-back shows the old value.
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdWriteData, Fault: simulator.Drop, Count: 1})
	rep, err := Write(e.ctx, e.c, d, WriteOptions{})
	if err != nil || rep.Results[0].Status != StatusVerified || e.port.count(mk2.WCmdWriteData) != 2 {
		t.Fatalf("one drop: %+v, %v, %d writes", rep, err, e.port.count(mk2.WCmdWriteData))
	}

	// A lost acknowledgement: the write arrived, so it is not repeated.
	e.port.reset()
	_ = d.SetDisplay(SettingID(2), 14.7)
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdWriteData, Fault: simulator.NoReply, Count: 1})
	rep, err = Write(e.ctx, e.c, d, WriteOptions{})
	if err != nil || rep.Results[0].Status != StatusVerified || e.port.count(mk2.WCmdWriteData) != 1 {
		t.Fatalf("lost ack: %+v, %v, %d writes", rep, err, e.port.count(mk2.WCmdWriteData))
	}

	// Two lost writes: bounded, the setting fails and stays dirty; the
	// model knows the device still has the old value. Setting 3 is skipped.
	e.port.reset()
	_ = d.SetDisplay(SettingID(2), 14.8)
	_ = d.SetDisplay(SettingID(3), 13.9)
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdWriteData, Fault: simulator.Drop, Count: 2})
	rep, err = Write(e.ctx, e.c, d, WriteOptions{})
	if !errors.Is(err, mk2.ErrTimeout) || e.port.count(mk2.WCmdWriteData) != 2 {
		t.Fatalf("two drops: %v, %d writes", err, e.port.count(mk2.WCmdWriteData))
	}
	if r := rep.Results; r[0].Status != StatusFailed || r[1].Status != StatusSkipped {
		t.Errorf("results %+v", r)
	}
	s := d.settings[2]
	if !d.Dirty(SettingID(2)) || s.value != 1480 || !s.hasRead || s.read != 1470 {
		t.Errorf("model after failure: %+v", *s)
	}
	if e.deviceRaw(t, 0, 2) != 1470 {
		t.Error("device changed")
	}
}

func TestWriteReadBackFails(t *testing.T) {
	e := newSyncEnv(t, false)
	d := e.read(t, 0)
	_ = d.SetDisplay(SettingID(2), 14.6)
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdReadSetting, Fault: simulator.NoReply, Count: 2})
	rep, err := Write(e.ctx, e.c, d, WriteOptions{})
	if !errors.Is(err, mk2.ErrTimeout) || rep.Results[0].Status != StatusFailed {
		t.Fatalf("%+v, %v", rep, err)
	}
	// The write arrived but could not be confirmed: the device value is
	// unknown and the setting stays dirty.
	if s := d.settings[2]; s.hasRead || !d.Dirty(SettingID(2)) || s.value != 1460 {
		t.Errorf("model: %+v", *s)
	}
	// The next write sends it again and confirms it.
	if rep, err := Write(e.ctx, e.c, d, WriteOptions{}); err != nil || rep.Results[0].Status != StatusVerified || d.Dirty(SettingID(2)) {
		t.Errorf("rewrite: %+v, %v", rep, err)
	}
}

func TestWriteMode2AccessLevelAndUnsupported(t *testing.T) {
	e := newSyncEnv(t, true)
	d := e.read(t, 0)
	_ = d.SetDisplay(SettingID(2), 14.6)
	_ = d.SetDisplay(SettingID(3), 13.9)

	// "Access level required" is an answer: reported, not retried, and the
	// run stops.
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdWriteViaID, Fault: simulator.AccessLevel, Count: 1})
	rep, err := Write(e.ctx, e.c, d, WriteOptions{})
	if !errors.Is(err, mk2.ErrAccessLevel) || e.port.count(mk2.WCmdWriteViaID) != 1 || e.port.count(mk2.WCmdWriteData) != 0 {
		t.Fatalf("access: %v, 0x37 %d", err, e.port.count(mk2.WCmdWriteViaID))
	}
	if r := rep.Results; r[0].Status != StatusFailed || !errors.Is(r[0].Err, mk2.ErrAccessLevel) || r[1].Status != StatusSkipped {
		t.Errorf("results %+v", r)
	}
	if s := d.settings[2]; !d.Dirty(SettingID(2)) || s.read != 1440 || !s.hasRead {
		t.Errorf("model: %+v", *s)
	}

	// "Unknown command": not retried either; ContinueOnError goes on.
	e.port.reset()
	e.sim.InjectFault(simulator.FaultRule{WCmd: mk2.WCmdWriteViaID, Fault: simulator.Unsupported, Count: 1})
	rep, err = Write(e.ctx, e.c, d, WriteOptions{ContinueOnError: true})
	var uc *mk2.UnknownCommandError
	if !errors.As(err, &uc) || e.port.count(mk2.WCmdWriteViaID) != 2 {
		t.Fatalf("unsupported: %v, 0x37 %d", err, e.port.count(mk2.WCmdWriteViaID))
	}
	if r := rep.Results; r[0].Status != StatusFailed || r[1].Status != StatusVerified {
		t.Errorf("results %+v", r)
	}

	// The device's own access check (setting 13 needs level 2).
	_ = d.SetRaw(SettingID(13), 1)
	rep, err = Write(e.ctx, e.c, d, WriteOptions{ContinueOnError: true})
	if !errors.Is(err, mk2.ErrAccessLevel) {
		t.Errorf("setting 13: %+v, %v", rep, err)
	}
	if r := rep.Results; len(r) != 2 || r[0].Status != StatusVerified || r[1].ID != SettingID(13) || r[1].Status != StatusFailed {
		t.Errorf("results %+v", r)
	}
}

func TestWriteFlagWordAlreadyOnDevice(t *testing.T) {
	e := newSyncEnv(t, false)
	d := e.read(t, 0)
	// Someone else sets flag 4 and flag 13 on the device after the read.
	if err := e.c.WriteSetting(e.ctx, 0, 0x2090); err != nil {
		t.Fatal(err)
	}
	e.port.reset()
	_ = d.SetFlag(4, true)
	rep, err := Write(e.ctx, e.c, d, WriteOptions{})
	if err != nil || rep.Results[0].Status != StatusUnchanged || e.port.count(mk2.WCmdWriteData) != 0 {
		t.Fatalf("%+v, %v", rep, err)
	}
	// The model now has the device's word, flag 13 included.
	if on, _ := d.Flag(13); !on || d.Dirty(SettingID(0)) {
		t.Error("model not updated from the device")
	}

	// A change keeps the device's other bits (read-modify-write).
	_ = d.SetFlag(4, false)
	if _, err := Write(e.ctx, e.c, d, WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := e.deviceRaw(t, 0, 0); got != 0x2080 {
		t.Errorf("device word %#x", got)
	}
}

func TestWriteResetRequired(t *testing.T) {
	p := simulator.Synthetic12V230()
	p.Version = 2699300 // revision 300: setting 64 needs a reset
	e := newSyncEnv(t, false, simulator.Device{Address: 0, Profile: p})
	d := e.read(t, 0)
	_ = d.SetRaw(SettingID(64), 300)
	_ = d.SetDisplay(SettingID(2), 14.6)
	rep, err := Write(e.ctx, e.c, d, WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.ResetRequired(); !slices.Equal(got, []ID{SettingID(64)}) {
		t.Errorf("ResetRequired = %v", got)
	}
}

func TestUnits(t *testing.T) {
	e := newSyncEnv(t, false)
	devs, reps, err := ReadUnits(e.ctx, e.c, []byte{0, 1}, nil, ReadOptions{RAM: []uint16{}})
	if err != nil || len(devs) != 2 || reps[1].Address != 1 || devs[1].Address != 1 {
		t.Fatalf("%v", err)
	}
	_ = devs[0].SetRaw(SettingID(64), 210)
	_ = devs[1].SetRaw(SettingID(64), 410)
	wreps, err := WriteUnits(e.ctx, e.c, devs, WriteOptions{})
	if err != nil || len(wreps) != 2 {
		t.Fatal(err)
	}
	if e.deviceRaw(t, 0, 64) != 210 || e.deviceRaw(t, 1, 64) != 410 {
		t.Error("units not written")
	}
	// A unit that does not answer ends the read.
	if _, _, err := ReadUnits(e.ctx, e.c, []byte{0, 5}, nil, ReadOptions{RAM: []uint16{}, Retries: -1}); !errors.Is(err, mk2.ErrTimeout) {
		t.Errorf("missing unit: %v", err)
	}
}
