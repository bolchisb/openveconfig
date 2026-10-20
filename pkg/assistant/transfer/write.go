package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// WriteOnHardware enables writing assistant areas through a codec for real
// devices (mk2.BlockCodec.Hardware). It stays off until the hardware checks
// of #3 have passed (spec #77 H5-H8); writes through the simulator's codec
// are not affected. Today mk2.DeviceBlockCodec refuses to write anyway.
const WriteOnHardware = false

// Defaults of Options.
const (
	// DefaultResetWait is the wait after the reset before the unit is
	// asked for again: the wait the original tools use after a system
	// reset (reverse-engineered, #53).
	DefaultResetWait = 2500 * time.Millisecond
	// DefaultBackWindow is how long the unit may take to answer after the
	// reset. Community reports say the units stop for one to five minutes
	// after an area change (kylehart/mk2vsc ASSISTANTS.md §3; spec #77
	// T6.4).
	DefaultBackWindow = 5 * time.Minute
	// pollInterval is the pause between the questions after a reset (our
	// choice).
	pollInterval = time.Second
)

// Errors of the checks before writing (KindRefused), and causes of a
// rollback.
var (
	ErrHardwareGate = errors.New("transfer: writing assistants to real devices is off until the hardware checks pass (#3)")
	ErrNoLongID     = errors.New("transfer: the unit's long ID is needed to write its area")
	ErrNoImage      = errors.New("transfer: no image to write")
	ErrOtherUnit    = errors.New("transfer: the area was read from another unit")
	ErrFirmware     = errors.New("transfer: the area was read from other firmware")
	ErrSize         = errors.New("transfer: the image does not have the size of the unit's area")
	ErrChecksum     = errors.New("transfer: the image's checksum is wrong")
	// ErrNotConfigured: the unit's system record does not say that it is
	// configured for assistants. How to enable them is not specified yet
	// (#77).
	ErrNotConfigured = errors.New("transfer: the unit is not configured for assistants (system record byte 7 bit 4 set); enabling them is not specified yet (#77)")
	// ErrMismatch: the area read back is not the image written.
	ErrMismatch = errors.New("transfer: the area read back differs from the image written")
	// ErrRunning: after the reset the unit runs other assistants than the
	// area was read with.
	ErrRunning = errors.New("transfer: other assistants run after the reset")
)

// Kind says what went wrong in a Write, and so what the user should do.
type Kind int

const (
	// KindGated: writing to real devices is off (WriteOnHardware). Nothing
	// was sent.
	KindGated Kind = iota + 1
	// KindRefused: a check before writing failed (Err). Nothing was
	// written.
	KindRefused
	// KindSnapshot: the unit could not be read before writing. Nothing
	// was written.
	KindSnapshot
	// KindRolledBack: writing failed (Err); the snapshot was written back
	// and verified, and the unit came back with it.
	KindRolledBack
	// KindRollbackFailed: writing failed and the snapshot could not be
	// restored, or the unit runs other assistants after it: the unit's
	// state is unknown.
	KindRollbackFailed
	// KindNotBack: the unit did not answer after the reset (Report says
	// whether that was after a rollback).
	KindNotBack
)

func (k Kind) String() string {
	switch k {
	case KindGated:
		return "writing to devices is off"
	case KindRefused:
		return "refused"
	case KindSnapshot:
		return "snapshot failed"
	case KindRolledBack:
		return "rolled back"
	case KindRollbackFailed:
		return "rollback failed"
	case KindNotBack:
		return "not back after reset"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error is a failure of Write.
type Error struct {
	Kind Kind
	// Err is the cause: one of the Err values, an mk2 error or the
	// context's error.
	Err error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return "assistants: " + e.Kind.String()
	}
	return "assistants: " + e.Kind.String() + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Guidance returns what the user should do, as complete English sentences
// in our own words (spec #77 T8.3), for the front ends' translation
// tables.
func (e *Error) Guidance() string {
	switch e.Kind {
	case KindGated:
		return "Writing assistants to devices is not available yet. Nothing was sent."
	case KindRefused, KindSnapshot:
		return "Nothing was written."
	case KindRolledBack:
		return "Writing the assistants failed. The previous assistants were written back and verified, and the unit restarted with them."
	case KindRollbackFailed:
		return "Writing the assistants failed and the previous assistants could not be restored. The unit's state is unknown. Restore the assistants with VEConfigure, or switch the unit off and on."
	case KindNotBack:
		return "The unit did not answer after the restart. Wait a few minutes and read it again. If its LEDs show an error, restore the assistants with VEConfigure."
	}
	return ""
}

// Step names a stage of a Write, for progress display.
type Step int

const (
	StepWrite    Step = iota + 1 // writing a block; Done counts from 1
	StepVerify                   // reading the area back
	StepReset                    // restarting the units
	StepWait                     // waiting for the unit to answer
	StepRollback                 // writing a block of the snapshot back
)

// Event is a progress report, sent when a step (or a block) starts.
type Event struct {
	Step        Step
	Done, Total int // blocks, for StepWrite and StepRollback
}

// String returns a one-line English description, for logs and the CLI.
func (e Event) String() string {
	switch e.Step {
	case StepWrite:
		return fmt.Sprintf("writing block %d of %d", e.Done, e.Total)
	case StepVerify:
		return "reading the area back"
	case StepReset:
		return "restarting the units"
	case StepWait:
		return "waiting for the unit to answer"
	case StepRollback:
		return fmt.Sprintf("writing the previous area back, block %d of %d", e.Done, e.Total)
	}
	return fmt.Sprintf("Step(%d)", int(e.Step))
}

// Options configures a Write. The zero value is a dry run.
type Options struct {
	// Confirm performs the write. Without it Write only reads (the
	// snapshot and the system record) and reports; nothing is written.
	// Front ends set it after the user accepted that the units switch off
	// for a moment.
	Confirm bool
	// ResetWait and BackWindow: 0 means DefaultResetWait and
	// DefaultBackWindow.
	ResetWait, BackWindow time.Duration
	// Progress, if set, receives progress events on the goroutine of the
	// Write.
	Progress func(Event)
}

// Report says what a Write did, or in a dry run would do.
type Report struct {
	DryRun bool
	// Snapshot is the unit's area just before writing; what a rollback
	// restores.
	Snapshot *Area
	// Unchanged: the unit already holds the image, so nothing is written
	// and the unit is not restarted.
	Unchanged bool
	// Blocks is the number of blocks of the area; Writes counts the block
	// write requests sent, the rollback's included.
	Blocks, Writes int
	// RolledBack: writing failed and the snapshot was written back (see
	// the error's Kind for the outcome).
	RolledBack bool
	// Reset: the units were restarted.
	Reset bool
	// Running are the assistant IDs running after the last restart, if
	// RunningKnown.
	Running      []uint16
	RunningKnown bool
}

// Write writes the area want to the unit u with codec ("restore"). Without
// opts.Confirm it is a dry run that only reads. The steps:
//
//  1. Checks, before anything is sent: the hardware gate (KindGated); a
//     long ID, an image with a right checksum, read from this unit
//     (KindRefused).
//  2. The snapshot: Read of the unit's area (KindSnapshot). The firmware
//     and the size must equal want's (KindRefused). When the area already
//     holds want's image, Write stops here: Report.Unchanged.
//  3. The unit's system record must say that it is configured for
//     assistants (KindRefused, ErrNotConfigured). A dry run stops here.
//  4. Every block, in ascending order, so that the checksum block comes
//     last and an interrupted write leaves a wrong checksum; a failed
//     block is tried once more. Then the whole area is read back and
//     compared.
//  5. The units are restarted with the system reset (mk2.ResetSystem; which
//     reset the original sends after an area write is UNKNOWN, spec #77
//     T7.5); then Write waits for the unit to answer (KindNotBack) and
//     compares its running assistants with want's, when known.
//  6. On a failure in 4 or 5 (or cancellation, checked between blocks),
//     the snapshot is written back in the same way unless the area still
//     holds it, read back, and the units are restarted: KindRolledBack,
//     or KindRollbackFailed.
//
// The Client's Options.Retries should be 0: the retries here are the
// spec's. The report is returned in every case.
func Write(ctx context.Context, c *mk2.Client, codec mk2.BlockCodec, u Unit, want *Area, opts Options) (*Report, error) {
	if opts.ResetWait <= 0 {
		opts.ResetWait = DefaultResetWait
	}
	if opts.BackWindow <= 0 {
		opts.BackWindow = DefaultBackWindow
	}
	w := &writer{c: c, codec: codec, u: u, opts: opts, rep: &Report{DryRun: !opts.Confirm}}
	if err := w.write(ctx, want); err != nil {
		return w.rep, err
	}
	return w.rep, nil
}

type writer struct {
	c     *mk2.Client
	codec mk2.BlockCodec
	u     Unit
	opts  Options
	rep   *Report
}

func (w *writer) progress(e Event) {
	if w.opts.Progress != nil {
		w.opts.Progress(e)
	}
}

func refused(err error) error { return &Error{Kind: KindRefused, Err: err} }

func (w *writer) write(ctx context.Context, want *Area) error {
	if w.opts.Confirm && w.codec.Hardware() && !WriteOnHardware {
		return &Error{Kind: KindGated, Err: ErrHardwareGate}
	}
	switch {
	case w.u.LongID == 0:
		return refused(ErrNoLongID)
	case want == nil || len(want.Image) == 0:
		return refused(ErrNoImage)
	case want.LongID != w.u.LongID:
		return refused(fmt.Errorf("%w (%08X, not %08X)", ErrOtherUnit, want.LongID, w.u.LongID))
	case !assistant.ChecksumOK(want.Image):
		return refused(ErrChecksum)
	}

	snap, err := Read(ctx, w.c, w.codec, w.u)
	if err != nil {
		return &Error{Kind: KindSnapshot, Err: err}
	}
	w.rep.Snapshot = snap
	w.rep.Blocks = len(snap.Image) / w.codec.BlockSize()
	switch {
	case snap.Firmware != want.Firmware:
		return refused(fmt.Errorf("%w (%d, the unit has %d)", ErrFirmware, want.Firmware, snap.Firmware))
	case len(snap.Image) != len(want.Image):
		return refused(fmt.Errorf("%w (%d bytes, the unit has %d)", ErrSize, len(want.Image), len(snap.Image)))
	case bytes.Equal(snap.Image, want.Image):
		w.rep.Unchanged = true
		return nil
	}

	// Byte 7 bit 4 (the assistant-reset flag of package system) clear:
	// configured for assistants (observed on our unit, community-reported
	// for others; spec #77 T5.2, T5.3, T5.8).
	if err := w.c.PrepareSystemLink(ctx); err != nil {
		return &Error{Kind: KindSnapshot, Err: err}
	}
	sc, err := w.c.ReadSystemConfig(ctx, w.u.LongID)
	if err != nil {
		return &Error{Kind: KindSnapshot, Err: fmt.Errorf("read the system record: %w", err)}
	}
	if system.Record(sc.Record).AssistantResetFlag() {
		return refused(ErrNotConfigured)
	}
	if !w.opts.Confirm {
		return nil
	}

	// The frames go out with a context that is not cancelled, so a block
	// is never cut in half; cancellation is checked between blocks and
	// leads to the rollback.
	io := context.WithoutCancel(ctx)
	cause := w.writeArea(io, ctx, want.Image, StepWrite)
	if cause == nil {
		if err := w.restart(io, ctx); err != nil {
			return &Error{Kind: KindNotBack, Err: err}
		}
		if cause = w.checkRunning(io, want); cause == nil {
			return nil
		}
	}
	return w.rollback(ctx, cause)
}

// writeArea writes img block by block in ascending order, then reads the
// area back and compares. stop (nil: never) is checked before each block.
func (w *writer) writeArea(io, stop context.Context, img []byte, step Step) error {
	bs := w.codec.BlockSize()
	if err := w.c.SetAddress(io, w.u.Address); err != nil {
		return err
	}
	for n := range w.rep.Blocks {
		if stop != nil && stop.Err() != nil {
			return stop.Err()
		}
		w.progress(Event{Step: step, Done: n + 1, Total: w.rep.Blocks})
		err := try(func() error {
			w.rep.Writes++
			return w.c.WriteBlock(io, w.codec, n, img[n*bs:(n+1)*bs])
		})
		if err != nil {
			return fmt.Errorf("write block %d: %w", n, err)
		}
	}
	w.progress(Event{Step: StepVerify})
	got, err := Read(io, w.c, w.codec, w.u)
	if err != nil {
		return fmt.Errorf("read back: %w", err)
	}
	if i := mismatch(got.Image, img); i >= 0 {
		return fmt.Errorf("%w (from byte %d)", ErrMismatch, i)
	}
	return nil
}

// mismatch returns the first offset where a and b differ, or -1.
func mismatch(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	return -1
}

// restart restarts the units, waits, and asks for the unit's firmware
// version until it answers or Options.BackWindow has passed. io is used
// for the frames; the waits end early when wait is cancelled.
func (w *writer) restart(io, wait context.Context) error {
	w.progress(Event{Step: StepReset})
	if err := w.c.ResetSystem(io); err != nil {
		return fmt.Errorf("reset: %w", err)
	}
	w.rep.Reset = true
	w.progress(Event{Step: StepWait})
	if err := sleep(wait, w.opts.ResetWait); err != nil {
		return err
	}
	deadline := time.Now().Add(w.opts.BackWindow)
	for {
		err := w.c.SetAddress(io, w.u.Address)
		if err == nil {
			if _, err = w.c.SoftwareVersion(io); err == nil {
				return nil
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("no answer from address %d within %v: %w", w.u.Address, w.opts.BackWindow, err)
		}
		if err := sleep(wait, pollInterval); err != nil {
			return err
		}
	}
}

// checkRunning reads the running assistants after a restart and compares
// them with want's, when known (spec #77 T7.6, design).
func (w *writer) checkRunning(ctx context.Context, want *Area) error {
	running, err := ReadRunning(ctx, w.c)
	if err != nil {
		return err
	}
	w.rep.Running, w.rep.RunningKnown = running, true
	if want.RunningKnown && !slices.Equal(running, want.Running) {
		return fmt.Errorf("%w (%v, want %v)", ErrRunning, running, want.Running)
	}
	return nil
}

// rollback restores the snapshot after cause: it writes it back unless the
// area still holds it, reads it back, restarts the units and checks the
// running assistants. It runs to the end even when ctx is cancelled.
func (w *writer) rollback(ctx context.Context, cause error) error {
	io := context.WithoutCancel(ctx)
	w.rep.RolledBack = true
	snap := w.rep.Snapshot
	if cur, err := Read(io, w.c, w.codec, w.u); err != nil || !bytes.Equal(cur.Image, snap.Image) {
		if err := w.writeArea(io, nil, snap.Image, StepRollback); err != nil {
			return &Error{Kind: KindRollbackFailed, Err: fmt.Errorf("%w; restore: %w", cause, err)}
		}
	}
	if err := w.restart(io, io); err != nil {
		return &Error{Kind: KindNotBack, Err: fmt.Errorf("%w; after the restore: %w", cause, err)}
	}
	if err := w.checkRunning(io, snap); err != nil {
		return &Error{Kind: KindRollbackFailed, Err: fmt.Errorf("%w; after the restore: %w", cause, err)}
	}
	return &Error{Kind: KindRolledBack, Err: cause}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
