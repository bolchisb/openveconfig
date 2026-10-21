package firmware

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/internal/vfftest"
	"github.com/bolchisb/openveconfig/pkg/vff"
)

func testFile(t *testing.T) *vff.File {
	t.Helper()
	f, err := vff.Parse(vfftest.File7())
	if err != nil {
		t.Fatal(err)
	}
	return f
}

var fastOpts = &Options{AckTimeout: 200 * time.Millisecond, EntryTimeout: 200 * time.Millisecond}

func TestCheckHardwareGate(t *testing.T) {
	if err := CheckHardwareGate(true, false); err != nil {
		t.Errorf("simulator gate: %v", err)
	}
	if err := CheckHardwareGate(false, true); err != nil {
		t.Errorf("opt-in gate: %v", err)
	}
	if err := CheckHardwareGate(false, false); !errors.Is(err, ErrNotHardwareValidated) {
		t.Errorf("real without opt-in: err = %v, want ErrNotHardwareValidated", err)
	}
}

func TestReprogramSuccess(t *testing.T) {
	f := testFile(t)
	sim := NewBootSim(BootConfig{})
	defer sim.Close()
	rp := NewReprogrammer(sim, fastOpts)
	defer rp.Close()

	ctx := context.Background()
	if err := rp.EnterBootloader(ctx); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := rp.Program(ctx, f, Target{FirmwareNumber: 2003456}); err != nil {
		t.Fatalf("program: %v", err)
	}
	if rp.State() != StateDone {
		t.Errorf("state = %v, want done", rp.State())
	}
	want := len(Records(f))
	if got := sim.Written(); len(got) != want {
		t.Fatalf("wrote %d blocks, want %d", len(got), want)
	}
	for i, idx := range sim.Written() {
		if int(idx) != i {
			t.Errorf("block %d has index %d", i, idx)
		}
	}
}

func TestReprogramInterrupted(t *testing.T) {
	f := testFile(t)
	sim := NewBootSim(BootConfig{InterruptAfter: 2})
	defer sim.Close()
	rp := NewReprogrammer(sim, fastOpts)
	defer rp.Close()

	ctx := context.Background()
	if err := rp.EnterBootloader(ctx); err != nil {
		t.Fatalf("enter: %v", err)
	}
	err := rp.Program(ctx, f, Target{FirmwareNumber: 2003456})
	if !errors.Is(err, ErrNoResponse) {
		t.Fatalf("program: err = %v, want ErrNoResponse", err)
	}
	if rp.State() != StateError {
		t.Errorf("state = %v, want error", rp.State())
	}
}

func TestReprogramRecovery(t *testing.T) {
	f := testFile(t)
	// Interrupt the first stream after two blocks, then let a restart finish.
	sim := NewBootSim(BootConfig{InterruptAfter: 2, InterruptOnce: true})
	defer sim.Close()
	rp := NewReprogrammer(sim, fastOpts)
	defer rp.Close()

	ctx := context.Background()
	if err := rp.EnterBootloader(ctx); err != nil {
		t.Fatalf("enter: %v", err)
	}
	if err := rp.Program(ctx, f, Target{FirmwareNumber: 2003456}); !errors.Is(err, ErrNoResponse) {
		t.Fatalf("first program: err = %v, want ErrNoResponse", err)
	}

	// Restart: re-enter the bootloader and re-stream from scratch.
	if err := rp.EnterBootloader(ctx); err != nil {
		t.Fatalf("re-enter: %v", err)
	}
	if err := rp.Program(ctx, f, Target{FirmwareNumber: 2003456}); err != nil {
		t.Fatalf("recovery program: %v", err)
	}
	if rp.State() != StateDone {
		t.Errorf("state = %v, want done", rp.State())
	}
	if got, want := len(sim.Written()), len(Records(f)); got != want {
		t.Errorf("recovery wrote %d blocks, want %d", got, want)
	}
}

func TestReprogramProgress(t *testing.T) {
	f := testFile(t)
	sim := NewBootSim(BootConfig{})
	defer sim.Close()
	var phases []Phase
	opts := *fastOpts
	opts.Progress = func(p Progress) { phases = append(phases, p.Phase) }
	rp := NewReprogrammer(sim, &opts)
	defer rp.Close()

	ctx := context.Background()
	if err := rp.EnterBootloader(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rp.Program(ctx, f, Target{FirmwareNumber: 2003456}); err != nil {
		t.Fatal(err)
	}
	// Entering, erasing, one programming report per block, then done.
	if len(phases) < 3 || phases[0] != PhaseEntering || phases[len(phases)-1] != PhaseDone {
		t.Errorf("phases = %v", phases)
	}
}

func TestBootSimRejectsCorruptFrame(t *testing.T) {
	f := testFile(t)
	payload := Records(f)[0].payload()
	// Build a frame with a deliberately wrong checksum: the body no longer
	// sums to 0, so the target must answer with the failure code.
	bad := append(append([]byte(nil), payload...), checksum(payload)+1)
	n := len(bad)
	frame := append([]byte{sof, byte((n - 1) | lenHighBit)}, escape(bad)...)
	sim := NewBootSim(BootConfig{})
	defer sim.Close()
	if _, err := sim.Write(frame); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	rn, _ := sim.Read(buf)
	// Response is a plain ack frame [L][payload][cks]; its payload must be 'F'.
	if err := validateAck(buf[1:rn]); !errors.Is(err, ErrTargetRefused) {
		t.Errorf("corrupt frame response: %X, validate = %v", buf[:rn], err)
	}
}
