package transfer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// The unit of these tests: a simulated system unit at address 0 with the
// factory record (byte 7 0xE0: configured for assistants). Firmware and
// contents are made up.
const (
	unitID    = 0x10000001
	fwKnown   = 2627001 // a family of AreaSize
	fwUnknown = 2699481 // the simulator's default, a made-up family
	essRecord = 0x0052  // assistant 5 with 2 words
)

var unit = Unit{Address: 0, LongID: unitID}

var fast = Options{Confirm: true, ResetWait: 20 * time.Millisecond, BackWindow: 300 * time.Millisecond}

type rig struct {
	sim *simulator.Simulator
	c   *mk2.Client
	fw  uint32
}

func newRig(t *testing.T, fw uint32, mem simulator.AssistantMemory, rec ...byte) *rig {
	t.Helper()
	su := simulator.SystemUnit{LongID: unitID, VersionWord: fw}
	if rec != nil {
		su.Record = [mk2.SystemRecordLen]byte(rec)
	}
	d := simulator.SystemDevice(su)
	d.Assistants = &mem
	sim := simulator.New(simulator.Config{Devices: []simulator.Device{d}, VersionInterval: -1, ResetDuration: 10 * time.Millisecond})
	c := mk2.NewClient(sim.Port(), &mk2.Options{Timeout: 50 * time.Millisecond, RawTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { c.Close() })
	return &rig{sim: sim, c: c, fw: fw}
}

func (r *rig) area(t *testing.T) []byte {
	t.Helper()
	st, ok := r.sim.Assistants(0)
	if !ok {
		t.Fatal("no area")
	}
	return st.Image
}

func (r *rig) writes() int {
	st, _ := r.sim.Assistants(0)
	return st.Writes
}

func (r *rig) resets() int {
	u, _ := r.sim.Unit(unitID)
	return u.Resets
}

func (r *rig) want(img []byte) *Area {
	return &Area{LongID: unitID, Firmware: r.fw, Image: img}
}

func (r *rig) write(t *testing.T, want *Area, opts Options) (*Report, error) {
	t.Helper()
	return Write(context.Background(), r.c, simulator.BlockCodec{}, unit, want, opts)
}

func kindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return 0
}

// TestReadWriteReadAgain: read, write back, read again gives the same
// image (#80 acceptance), with an image of another program in between.
func TestReadWriteReadAgain(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, fwKnown, simulator.AssistantMemory{Image: simulator.ProgramArea(900), RAM: []uint16{essRecord, 1, 2}})
	a, err := Read(ctx, r.c, simulator.BlockCodec{}, unit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Image, simulator.ProgramArea(900)) || !a.ChecksumOK || a.Firmware != fwKnown || a.LongID != unitID {
		t.Fatalf("read: %d bytes, checksum %v, firmware %d", len(a.Image), a.ChecksumOK, a.Firmware)
	}
	if !a.RunningKnown || !slices.Equal(a.Running, []uint16{5}) {
		t.Fatalf("running %v", a.Running)
	}

	// Writing back what the unit holds writes nothing.
	rep, err := r.write(t, a, fast)
	if err != nil || !rep.Unchanged || rep.Writes != 0 || rep.Reset || r.writes() != 0 {
		t.Fatalf("same image: %+v, %v", rep, err)
	}

	other := r.want(simulator.ProgramArea(1500))
	var steps []Step
	opts := fast
	opts.Progress = func(e Event) { steps = append(steps, e.Step) }
	rep, err = r.write(t, other, opts)
	if err != nil || rep.Unchanged || rep.RolledBack || !rep.Reset || rep.Blocks != 44 || rep.Writes != 44 {
		t.Fatalf("other image: %+v, %v", rep, err)
	}
	if !bytes.Equal(r.area(t), other.Image) || r.resets() != 1 {
		t.Fatalf("area not written, or %d resets", r.resets())
	}
	if !slices.Equal(rep.Running, []uint16{5}) || !bytes.Equal(rep.Snapshot.Image, a.Image) {
		t.Fatalf("report: running %v", rep.Running)
	}
	if steps[0] != StepWrite || steps[43] != StepWrite || !slices.Equal(steps[44:], []Step{StepVerify, StepReset, StepWait}) {
		t.Fatalf("steps %v", steps)
	}

	if _, err := r.write(t, a, fast); err != nil {
		t.Fatal(err)
	}
	b, err := Read(ctx, r.c, simulator.BlockCodec{}, unit)
	if err != nil || !bytes.Equal(b.Image, a.Image) {
		t.Fatalf("read again: %v", err)
	}
}

// TestReadWholeArea: the whole area is read, erased blocks and the tail at
// the end included; a unit of an unknown family is read up to its end.
func TestReadWholeArea(t *testing.T) {
	ctx := context.Background()
	for name, tt := range map[string]struct {
		fw  uint32
		img []byte
	}{
		"erased, known size":    {fwKnown, simulator.ErasedArea()},
		"program, known size":   {fwKnown, simulator.ProgramArea(100)},
		"program, found by end": {fwUnknown, simulator.ProgramArea(100)},
		"short area, by end":    {fwUnknown, checksummed(bytes.Repeat([]byte{0xFF, 0x11}, 512))},
	} {
		r := newRig(t, tt.fw, simulator.AssistantMemory{Image: tt.img})
		a, err := Read(ctx, r.c, simulator.BlockCodec{}, unit)
		if err != nil || !bytes.Equal(a.Image, tt.img) || !a.ChecksumOK {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !a.RunningKnown || len(a.Running) != 0 {
			t.Errorf("%s: running %v", name, a.Running)
		}
	}

	// A known family whose area is shorter than expected is an error.
	r := newRig(t, fwKnown, simulator.AssistantMemory{Image: make([]byte, 1024)})
	if _, err := Read(ctx, r.c, simulator.BlockCodec{}, unit); !errors.Is(err, mk2.ErrBlockRange) {
		t.Errorf("short known area: %v", err)
	}
	// A unit without an assistant area does not know the request.
	sim := simulator.New(simulator.Config{VersionInterval: -1})
	c := mk2.NewClient(sim.Port(), &mk2.Options{Timeout: 50 * time.Millisecond})
	defer c.Close()
	var uc *mk2.UnknownCommandError
	if _, err := Read(ctx, c, simulator.BlockCodec{}, unit); !errors.As(err, &uc) {
		t.Errorf("no area: %v", err)
	}
	if n, ok := AreaSize(2729999); !ok || n != assistant.ImageSize {
		t.Error("AreaSize of a known family")
	}
	if _, ok := AreaSize(fwUnknown); ok {
		t.Error("AreaSize of an unknown family")
	}
}

func checksummed(img []byte) []byte {
	binary.LittleEndian.PutUint32(img[len(img)-4:], assistant.Checksum(img))
	return img
}

// TestNoAssistantShapes: the shapes of a unit without assistants are read
// and restored like any other area.
func TestNoAssistantShapes(t *testing.T) {
	ctx := context.Background()
	for name, img := range map[string][]byte{
		"bare":      simulator.ErasedArea(),
		"container": simulator.HeaderArea(6),
		"stub":      simulator.HeaderArea(64),
	} {
		r := newRig(t, fwKnown, simulator.AssistantMemory{Image: img})
		a, err := Read(ctx, r.c, simulator.BlockCodec{}, unit)
		if err != nil || !bytes.Equal(a.Image, img) || !a.ChecksumOK {
			t.Fatalf("%s: read: %v", name, err)
		}
		if _, err := r.write(t, r.want(simulator.ProgramArea(700)), fast); err != nil {
			t.Fatalf("%s: write program: %v", name, err)
		}
		if _, err := r.write(t, a, fast); err != nil {
			t.Fatalf("%s: write back: %v", name, err)
		}
		if !bytes.Equal(r.area(t), img) {
			t.Fatalf("%s: not restored", name)
		}
	}
}

// TestDryRunWritesNothing: without Confirm only reads happen.
func TestDryRunWritesNothing(t *testing.T) {
	r := newRig(t, fwKnown, simulator.AssistantMemory{})
	opts := fast
	opts.Confirm = false
	rep, err := r.write(t, r.want(simulator.ProgramArea(300)), opts)
	if err != nil || !rep.DryRun || rep.Unchanged || rep.Snapshot == nil || rep.Writes != 0 || rep.Reset {
		t.Fatalf("dry run: %+v, %v", rep, err)
	}
	if r.writes() != 0 || r.resets() != 0 || !bytes.Equal(r.area(t), simulator.ErasedArea()) {
		t.Fatal("dry run wrote")
	}
}

// hardwareCodec is the simulator's codec claiming to be for real devices.
type hardwareCodec struct{ simulator.BlockCodec }

func (hardwareCodec) Hardware() bool { return true }

// TestHardwareGate: writes through a codec for real devices are refused
// before anything is sent, also when the codec could write.
func TestHardwareGate(t *testing.T) {
	ctx := context.Background()
	r := newRig(t, fwKnown, simulator.AssistantMemory{})
	want := r.want(simulator.ProgramArea(300))
	for _, codec := range []mk2.BlockCodec{mk2.DeviceBlockCodec{}, hardwareCodec{}} {
		rep, err := Write(ctx, r.c, codec, unit, want, fast)
		if kindOf(err) != KindGated || !errors.Is(err, ErrHardwareGate) || rep.Snapshot != nil {
			t.Fatalf("%T: %v", codec, err)
		}
	}
	if r.writes() != 0 || r.resets() != 0 {
		t.Fatal("gated write sent something")
	}
	// The device codec cannot even read.
	if _, err := Read(ctx, r.c, mk2.DeviceBlockCodec{}, unit); !errors.Is(err, mk2.ErrBlockTransferUnspecified) {
		t.Fatalf("read with the device codec: %v", err)
	}
	opts := fast
	opts.Confirm = false
	if _, err := Write(ctx, r.c, mk2.DeviceBlockCodec{}, unit, want, opts); kindOf(err) != KindSnapshot || !errors.Is(err, mk2.ErrBlockTransferUnspecified) {
		t.Fatalf("dry run with the device codec: %v", err)
	}
}

// TestRefusals: the checks before writing.
func TestRefusals(t *testing.T) {
	r := newRig(t, fwKnown, simulator.AssistantMemory{})
	good := simulator.ProgramArea(300)
	bad := bytes.Clone(good)
	bad[0] ^= 1
	for name, tt := range map[string]struct {
		u    Unit
		want *Area
		err  error
	}{
		"no long ID":           {Unit{}, r.want(good), ErrNoLongID},
		"no image":             {unit, nil, ErrNoImage},
		"other unit":           {unit, &Area{LongID: unitID + 1, Firmware: fwKnown, Image: good}, ErrOtherUnit},
		"checksum":             {unit, r.want(bad), ErrChecksum},
		"firmware":             {unit, &Area{LongID: unitID, Firmware: fwKnown + 1, Image: good}, ErrFirmware},
		"size":                 {unit, r.want(checksummed(make([]byte, 1024))), ErrSize},
		"area without long ID": {unit, &Area{Firmware: fwKnown, Image: good}, ErrOtherUnit},
	} {
		_, err := Write(context.Background(), r.c, simulator.BlockCodec{}, tt.u, tt.want, fast)
		if kindOf(err) != KindRefused || !errors.Is(err, tt.err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if r.writes() != 0 {
		t.Fatal("a refused write sent blocks")
	}

	// A unit whose record says "no assistants" (byte 7 0xF0).
	r = newRig(t, fwKnown, simulator.AssistantMemory{}, 0x01, 0x01, 0x01, 0, 0, 0, 0, 0xF0, 0x00)
	opts := fast
	for _, confirm := range []bool{false, true} {
		opts.Confirm = confirm
		if _, err := r.write(t, r.want(good), opts); kindOf(err) != KindRefused || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("not configured, confirm %v: %v", confirm, err)
		}
	}
	if r.writes() != 0 {
		t.Fatal("wrote to a unit not configured for assistants")
	}
}

// TestRefusedWrite: the unit refuses the blocks; the area does not change,
// nothing needs writing back, and the units restart.
func TestRefusedWrite(t *testing.T) {
	r := newRig(t, fwKnown, simulator.AssistantMemory{RefuseWrites: true})
	rep, err := r.write(t, r.want(simulator.ProgramArea(300)), fast)
	if kindOf(err) != KindRolledBack || !errors.Is(err, mk2.ErrBlockRefused) || !rep.RolledBack {
		t.Fatalf("%+v, %v", rep, err)
	}
	if rep.Writes != 2 || !bytes.Equal(r.area(t), simulator.ErasedArea()) || r.resets() != 1 {
		t.Fatalf("%d writes, %d resets", rep.Writes, r.resets())
	}
}

// TestInterruptedWrite: a write that stops in the middle is detected and
// the previous area is restored (#80 acceptance).
func TestInterruptedWrite(t *testing.T) {
	old := simulator.ProgramArea(900)
	want := simulator.ProgramArea(1800)

	// The link drops two requests at block 20: the block and its retry.
	r := newRig(t, fwKnown, simulator.AssistantMemory{Image: old})
	r.sim.InjectFault(simulator.FaultRule{WCmd: simulator.WCmdBlockWrite, Fault: simulator.Drop, Skip: 20, Count: 2})
	rep, err := r.write(t, r.want(want), fast)
	if kindOf(err) != KindRolledBack || !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("dropped blocks: %v", err)
	}
	if !bytes.Equal(r.area(t), old) || rep.Writes != 22+44 || r.resets() != 1 {
		t.Fatalf("not restored: %d writes, %d resets", rep.Writes, r.resets())
	}

	// The user cancels in the middle.
	r = newRig(t, fwKnown, simulator.AssistantMemory{Image: old})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := fast
	opts.Progress = func(e Event) {
		if e.Step == StepWrite && e.Done == 10 {
			cancel()
		}
	}
	_, err = Write(ctx, r.c, simulator.BlockCodec{}, unit, r.want(want), opts)
	if kindOf(err) != KindRolledBack || !errors.Is(err, context.Canceled) || !bytes.Equal(r.area(t), old) {
		t.Fatalf("cancelled: %v", err)
	}

	// A run that died in the middle shows on the next read: the checksum
	// block was not written yet.
	r = newRig(t, fwKnown, simulator.AssistantMemory{Image: old})
	bg := context.Background()
	check(t, r.c.SetAddress(bg, 0))
	half := simulator.HeaderArea(640)
	for n := range 10 {
		check(t, r.c.WriteBlock(bg, simulator.BlockCodec{}, n, half[n*64:(n+1)*64]))
	}
	a, err := Read(bg, r.c, simulator.BlockCodec{}, unit)
	if err != nil || a.ChecksumOK {
		t.Fatalf("half-written area: checksum OK %v, %v", a != nil && a.ChecksumOK, err)
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestWrongReadBack: the area read back differs; the snapshot is written
// back. When it cannot be, the state is unknown.
func TestWrongReadBack(t *testing.T) {
	old := simulator.ProgramArea(900)
	r := newRig(t, fwKnown, simulator.AssistantMemory{Image: old, CorruptWrites: 1})
	_, err := r.write(t, r.want(simulator.ProgramArea(1800)), fast)
	if kindOf(err) != KindRolledBack || !errors.Is(err, ErrMismatch) || !bytes.Equal(r.area(t), old) {
		t.Fatalf("one corrupt block: %v", err)
	}

	r = newRig(t, fwKnown, simulator.AssistantMemory{Image: old, CorruptWrites: 1000})
	rep, err := r.write(t, r.want(simulator.ProgramArea(1800)), fast)
	if kindOf(err) != KindRollbackFailed || !errors.Is(err, ErrMismatch) || !rep.RolledBack || rep.Reset {
		t.Fatalf("every block corrupt: %+v, %v", rep, err)
	}
	if (&Error{Kind: KindRollbackFailed}).Guidance() == "" {
		t.Error("no guidance")
	}
}

// TestNotBack: the unit does not answer after the reset.
func TestNotBack(t *testing.T) {
	r := newRig(t, fwKnown, simulator.AssistantMemory{})
	r.sim.SetBehaviour(unitID, simulator.Behaviour{ResetDuration: time.Hour})
	want := r.want(simulator.ProgramArea(300))
	rep, err := r.write(t, want, fast)
	if kindOf(err) != KindNotBack || !errors.Is(err, mk2.ErrTimeout) || !rep.Reset || rep.RolledBack {
		t.Fatalf("%+v, %v", rep, err)
	}
	if !bytes.Equal(r.area(t), want.Image) {
		t.Fatal("area not written")
	}
}

// TestRunningAfterReset: other assistants run after the reset than the
// area was read with; the snapshot is restored.
func TestRunningAfterReset(t *testing.T) {
	old := simulator.ProgramArea(900)
	r := newRig(t, fwKnown, simulator.AssistantMemory{Image: old, RAM: []uint16{essRecord, 1, 2}})
	want := r.want(simulator.ProgramArea(1800))
	want.Running, want.RunningKnown = []uint16{7}, true
	rep, err := r.write(t, want, fast)
	if kindOf(err) != KindRolledBack || !errors.Is(err, ErrRunning) || !bytes.Equal(r.area(t), old) || r.resets() != 2 {
		t.Fatalf("%+v, %v; %d resets", rep, err, r.resets())
	}
	if !slices.Equal(rep.Running, []uint16{5}) {
		t.Fatalf("running %v", rep.Running)
	}
}
