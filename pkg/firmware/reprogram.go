package firmware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bolchisb/openveconfig/pkg/vff"
)

// Reprogrammer errors.
var (
	// ErrNoResponse: the target did not answer within the timeout ("No
	// response from target!"). reverse-engineered (#12), spec Q3.
	ErrNoResponse = errors.New("firmware: no response from target")
	// ErrClosed: the Reprogrammer was closed or its transport failed.
	ErrClosed = errors.New("firmware: reprogrammer closed")
	// ErrNotHardwareValidated gates the real-hardware write path; see
	// CheckHardwareGate.
	ErrNotHardwareValidated = errors.New(
		"firmware: reprogramming is not yet validated on real hardware (#38); refusing to write to a real device")
)

// CheckHardwareGate enforces the safety gate (#38). Writing firmware is only
// allowed against the simulator, or against a real device when the caller
// opts in explicitly (optIn, from the CLI's
// --i-understand-this-can-brick-my-device flag). Any other case returns
// ErrNotHardwareValidated. It must be called before anything destructive is
// sent, and the opt-in must never be set in tests or default code paths.
func CheckHardwareGate(isSimulator, optIn bool) error {
	if isSimulator || optIn {
		return nil
	}
	return ErrNotHardwareValidated
}

// Phase is a step of the reprogram sequence.
type Phase int

const (
	PhaseEntering Phase = iota
	PhaseErasing
	PhaseProgramming
	PhaseDone
)

func (p Phase) String() string {
	switch p {
	case PhaseEntering:
		return "entering bootloader"
	case PhaseErasing:
		return "erasing"
	case PhaseProgramming:
		return "programming"
	case PhaseDone:
		return "done"
	default:
		return "unknown"
	}
}

// State is the reprogram state. It mirrors the device state byte of the
// original (busy/error/done). reverse-engineered (#12), spec Q2.
type State int

const (
	StateIdle State = iota
	StateBusy
	StateError
	StateDone
)

// Progress reports how far the reprogram has got. Block and Total count data
// blocks during PhaseProgramming.
type Progress struct {
	Phase        Phase
	Block, Total int
}

// Options configures a Reprogrammer. The zero value uses the defaults.
type Options struct {
	// AckTimeout is how long to wait for a per-frame acknowledgement
	// (default 500 ms, the original's ack timeout). reverse-engineered (#12).
	AckTimeout time.Duration
	// EntryTimeout is how long to wait for the bootloader to answer the
	// autobaud sync (default 2 s).
	EntryTimeout time.Duration
	// Progress, if set, is called as the reprogram advances.
	Progress func(Progress)
}

// Reprogrammer drives the bootloader transport over a raw serial line. It
// does NOT use the MK2 frame layer. A background goroutine reads the line;
// call Close to stop it.
type Reprogrammer struct {
	rw   io.ReadWriter
	opts Options

	rx      chan byte
	readErr error
	done    chan struct{}

	state State
}

// NewReprogrammer starts a Reprogrammer on rw. opts may be nil.
func NewReprogrammer(rw io.ReadWriter, opts *Options) *Reprogrammer {
	rp := &Reprogrammer{rw: rw, rx: make(chan byte, 256), done: make(chan struct{})}
	if opts != nil {
		rp.opts = *opts
	}
	if rp.opts.AckTimeout <= 0 {
		rp.opts.AckTimeout = 500 * time.Millisecond
	}
	if rp.opts.EntryTimeout <= 0 {
		rp.opts.EntryTimeout = 2 * time.Second
	}
	go rp.readLoop()
	return rp
}

// Close stops the Reprogrammer's reader. It does not close the transport.
func (rp *Reprogrammer) Close() error {
	select {
	case <-rp.done:
	default:
		close(rp.done)
	}
	return nil
}

// State returns the current state.
func (rp *Reprogrammer) State() State { return rp.state }

func (rp *Reprogrammer) readLoop() {
	buf := make([]byte, 64)
	for {
		n, err := rp.rw.Read(buf)
		for i := 0; i < n; i++ {
			select {
			case rp.rx <- buf[i]:
			case <-rp.done:
				return
			}
		}
		if err != nil {
			rp.readErr = err
			close(rp.rx)
			return
		}
	}
}

// readByte returns the next byte from the line, or an error on timeout,
// cancellation or close.
func (rp *Reprogrammer) readByte(ctx context.Context, timeout time.Duration) (byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case b, ok := <-rp.rx:
		if !ok {
			if rp.readErr != nil {
				return 0, fmt.Errorf("firmware: read: %w", rp.readErr)
			}
			return 0, ErrClosed
		}
		return b, nil
	case <-timer.C:
		return 0, ErrNoResponse
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-rp.done:
		return 0, ErrClosed
	}
}

// readAck reads one plain acknowledgement frame ([length] then length+1
// bytes) and validates it against expect. reverse-engineered (#12), spec Q3:
// a block is "verified" by a checksum-correct ack whose leading byte is the
// expected code; there is no separate read-back.
func (rp *Reprogrammer) readAck(ctx context.Context, expect ...byte) error {
	l, err := rp.readByte(ctx, rp.opts.AckTimeout)
	if err != nil {
		return err
	}
	buf := make([]byte, int(l)+1) // payload + checksum
	for i := range buf {
		if buf[i], err = rp.readByte(ctx, rp.opts.AckTimeout); err != nil {
			return err
		}
	}
	return validateAck(buf, expect...)
}

// EnterBootloader performs the autobaud sync and reads the bootloader's
// acknowledgement. reverse-engineered (#12), spec Q1: twelve 'U' bytes are
// written raw to let the bootloader lock onto the line, then the first
// response byte classifies the outcome ('V'/'R' alive, 'F' failure).
func (rp *Reprogrammer) EnterBootloader(ctx context.Context) error {
	rp.report(PhaseEntering, 0, 0)
	// TODO(#38): the real entry toggles the line speed (115200/19200) per the
	// file's connection-method byte before the sync; the simulator does not
	// need it and the exact choreography is unconfirmed.
	if _, err := rp.rw.Write(bytes.Repeat([]byte{autobaudByte}, autobaudCount)); err != nil {
		rp.state = StateError
		return fmt.Errorf("firmware: autobaud: %w", err)
	}
	b, err := rp.readByte(ctx, rp.opts.EntryTimeout)
	if err != nil {
		rp.state = StateError
		return fmt.Errorf("firmware: bootloader did not answer the autobaud sync: %w", err)
	}
	switch b {
	case ackAlive, ackReady:
		rp.state = StateBusy
		return nil
	case ackFail:
		rp.state = StateError
		return ErrTargetRefused
	default:
		rp.state = StateError
		return &FrameError{fmt.Sprintf("unexpected entry response %#02x", b)}
	}
}

// Program streams the firmware file to the target in two phases, erase then
// program, verifying each block by its acknowledgement, and finishes with an
// execute command. reverse-engineered (#12), spec Q2/Q3. EnterBootloader
// must have been called. On any failure the state becomes StateError and the
// error is returned; the recovery path is to re-run EnterBootloader and
// Program, which re-streams every block from the start (spec Q5).
func (rp *Reprogrammer) Program(ctx context.Context, f *vff.File, target Target) error {
	recs := Records(f)
	if len(recs) == 0 {
		rp.state = StateError
		return ErrUnsupportedTarget
	}
	version := uint16(target.FirmwareNumber)

	// Erase phase.
	rp.report(PhaseErasing, 0, len(recs))
	if err := rp.command(ctx, cmdErase, version); err != nil {
		rp.state = StateError
		return fmt.Errorf("firmware: erase: %w", err)
	}

	// Program phase: one frame per block, each verified by its ack.
	for i, rec := range recs {
		frame, err := rec.Frame()
		if err != nil {
			rp.state = StateError
			return err
		}
		if _, err := rp.rw.Write(frame); err != nil {
			rp.state = StateError
			return fmt.Errorf("firmware: write block %d: %w", i, err)
		}
		if err := rp.readAck(ctx, ackAlive, ackReady); err != nil {
			rp.state = StateError
			return fmt.Errorf("firmware: block %d: %w", i, err)
		}
		rp.report(PhaseProgramming, i+1, len(recs))
	}

	// Finalize / execute.
	if err := rp.command(ctx, cmdExecute, version); err != nil {
		rp.state = StateError
		return fmt.Errorf("firmware: finalize: %w", err)
	}
	rp.state = StateDone
	rp.report(PhaseDone, len(recs), len(recs))
	return nil
}

// command sends a command frame (command byte, 2-byte version, 4-byte
// address 0) and waits for its acknowledgement. reverse-engineered (#12),
// spec Q2.
func (rp *Reprogrammer) command(ctx context.Context, cmd byte, version uint16) error {
	payload := []byte{cmd, byte(version), byte(version >> 8), 0, 0, 0, 0}
	frame, err := buildFrame(payload)
	if err != nil {
		return err
	}
	if _, err := rp.rw.Write(frame); err != nil {
		return err
	}
	return rp.readAck(ctx, ackAlive, ackReady)
}

func (rp *Reprogrammer) report(p Phase, block, total int) {
	if rp.opts.Progress != nil {
		rp.opts.Progress(Progress{Phase: p, Block: block, Total: total})
	}
}
