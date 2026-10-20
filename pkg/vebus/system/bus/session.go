package bus

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus/system"
)

// Try counts and timing. Reverse-engineered (#53) unless marked as ours.
const (
	// WriteAttempts is how often a record is written to one unit (write,
	// read back, compare) before the unit counts as failed; it bounds the
	// EEPROM writes per unit to 2 × WriteAttempts with a rollback.
	WriteAttempts = 3
	// ReadTries is how often a record read is tried.
	ReadTries = 2
	// DefaultResetCount is how often the system reset is sent (our choice,
	// as the original Quick Configure; the broadcast has no reply).
	DefaultResetCount = 2
	// DefaultResetWait is the wait after the reset before the units are
	// looked for again (the original Quick Configure's wait).
	DefaultResetWait = 2500 * time.Millisecond
	// DefaultPostResetWindow is how long, in total, the units are looked
	// for after a reset (our choice, the time the settings tool gives a
	// device to answer after a reset).
	DefaultPostResetWindow = 10 * time.Second
)

// Options configures a Session. The zero value is the default.
type Options struct {
	// NoKeepAlive: do not send the keep-alive frame (front ends offer this
	// as an opt-out).
	NoKeepAlive bool
	// KeepAlivePeriod is the keep-alive period; 0 means
	// mk2.DefaultKeepAlivePeriod.
	KeepAlivePeriod time.Duration
	// ResetCount is how often the system reset is sent; 0 means
	// DefaultResetCount.
	ResetCount int
	// ResetWait is the wait after the reset; 0 means DefaultResetWait.
	ResetWait time.Duration
	// PostResetWindow bounds the search for the units after a reset; 0
	// means DefaultPostResetWindow.
	PostResetWindow time.Duration
}

// Step names a stage of an operation, for progress display.
type Step int

const (
	StepPrepare  Step = iota + 1 // checking the interface (PrepareSystemLink)
	StepIdentify                 // looking for units; Done = units found so far
	StepRead                     // reading each unit's record
	StepSnapshot                 // reading each unit's record just before writing
	StepWrite                    // writing a unit's record; Attempt counts from 1
	StepRollback                 // writing a unit's previous record back
	StepReset                    // sending the system reset; Done counts the frames
	StepWait                     // waiting for the units to restart
	StepVerify                   // looking for the units after the reset
)

func (s Step) String() string {
	switch s {
	case StepPrepare:
		return "prepare"
	case StepIdentify:
		return "identify"
	case StepRead:
		return "read"
	case StepSnapshot:
		return "snapshot"
	case StepWrite:
		return "write"
	case StepRollback:
		return "rollback"
	case StepReset:
		return "reset"
	case StepWait:
		return "wait"
	case StepVerify:
		return "verify"
	}
	return fmt.Sprintf("Step(%d)", int(s))
}

// Event is a progress report. It is sent when a step (or a unit within it)
// starts.
type Event struct {
	Step Step
	// Unit is the unit concerned, nil when the step is not about one.
	Unit *system.Unit
	// Attempt is the attempt number for StepWrite and StepRollback.
	Attempt int
	// Done and Total count the units (or frames) of the step; Done is
	// the 1-based number of the one starting. Total is 0 when not known.
	Done, Total int
}

// String returns a one-line English description, for logs and the CLI.
func (e Event) String() string {
	unit := ""
	if e.Unit != nil {
		unit = " " + e.Unit.Label()
	}
	switch e.Step {
	case StepPrepare:
		return "checking the interface"
	case StepIdentify:
		return fmt.Sprintf("looking for units (%d found)", e.Done)
	case StepRead:
		return fmt.Sprintf("reading unit%s (%d of %d)", unit, e.Done, e.Total)
	case StepSnapshot:
		return fmt.Sprintf("reading the current record of unit%s (%d of %d)", unit, e.Done, e.Total)
	case StepWrite:
		return fmt.Sprintf("writing unit%s (%d of %d), attempt %d", unit, e.Done, e.Total, e.Attempt)
	case StepRollback:
		return fmt.Sprintf("restoring unit%s (%d of %d), attempt %d", unit, e.Done, e.Total, e.Attempt)
	case StepReset:
		return fmt.Sprintf("resetting the system (%d of %d)", e.Done, e.Total)
	case StepWait:
		return "waiting for the units to restart"
	case StepVerify:
		return "checking the units after the reset"
	}
	return e.Step.String()
}

// Progress receives progress events. It runs on the goroutine of the
// operation and must not call Session methods. It may be nil.
type Progress func(Event)

func (p Progress) send(e Event) {
	if p != nil {
		p(e)
	}
}

// Session runs system operations through one mk2.Client. It holds the
// units found by the last scan. Operations run one at a time; the units
// (and layouts built from them) must not be read or changed while an
// operation runs.
type Session struct {
	c    *mk2.Client
	opts Options

	mu       sync.Mutex
	stopKA   func()
	closed   bool
	scanned  bool
	units    []*system.Unit  // last scan, in the order found
	active   map[uint32]byte // short address each unit answered with
	flashing map[uint32]bool // units whose LEDs this session set flashing
}

// Open starts a system session on c: it prepares the link and starts the
// keep-alive unless opts.NoKeepAlive. c stays owned by the caller; Close
// the Session before closing c.
func Open(ctx context.Context, c *mk2.Client, opts Options) (*Session, error) {
	if opts.KeepAlivePeriod <= 0 {
		opts.KeepAlivePeriod = mk2.DefaultKeepAlivePeriod
	}
	if opts.ResetCount <= 0 {
		opts.ResetCount = DefaultResetCount
	}
	if opts.ResetWait <= 0 {
		opts.ResetWait = DefaultResetWait
	}
	if opts.PostResetWindow <= 0 {
		opts.PostResetWindow = DefaultPostResetWindow
	}
	s := &Session{c: c, opts: opts, active: map[uint32]byte{}, flashing: map[uint32]bool{}}
	if err := s.prepare(ctx, nil); err != nil {
		return nil, err
	}
	if !opts.NoKeepAlive {
		s.stopKA = c.StartKeepAlive(opts.KeepAlivePeriod)
	}
	return s, nil
}

// errClosed is returned by operations on a closed Session.
var errClosed = errors.New("system: session closed")

// lock takes the session lock for an operation.
func (s *Session) lock() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errClosed
	}
	return nil
}

// Close stops the keep-alive and stops the LED flashing this session
// started. It does not close the Client. Calling it again does nothing.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.stopKA != nil {
		s.stopKA()
	}
	var errs []error
	if len(s.flashing) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, id := range sortedIDs(s.flashing) {
			if err := s.c.FlashLEDs(ctx, id, false); err != nil {
				errs = append(errs, fmt.Errorf("system: stop flashing %08X: %w", id, err))
			}
		}
		clear(s.flashing)
	}
	return errors.Join(errs...)
}

func sortedIDs(m map[uint32]bool) []uint32 {
	var ids []uint32
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Units returns the units found by the last scan, in the order found.
func (s *Session) Units() []*system.Unit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.units)
}

// ActiveAddress returns the short address the unit longID answered with
// in the last scan. It equals byte 8 of its record unless the record was
// written and the unit not yet reset.
func (s *Session) ActiveAddress(longID uint32) (byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.active[longID]
	return a, ok
}

// FlashLEDs starts or stops flashing the LEDs of the unit longID. It
// changes only volatile state, so it needs no confirmation. Close stops
// any flashing the session started.
func (s *Session) FlashLEDs(ctx context.Context, longID uint32, on bool) error {
	if err := s.lock(); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.prepare(ctx, nil); err != nil {
		return err
	}
	if err := s.c.FlashLEDs(ctx, longID, on); err != nil {
		return fmt.Errorf("system: flash LEDs of %08X: %w", longID, err)
	}
	if on {
		s.flashing[longID] = true
	} else {
		delete(s.flashing, longID)
	}
	return nil
}

// prepare checks the link again at the start of an operation.
func (s *Session) prepare(ctx context.Context, p Progress) error {
	p.send(Event{Step: StepPrepare})
	if err := s.c.PrepareSystemLink(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Kind: KindLink, Err: err}
	}
	return nil
}

// perUnit reports whether err concerns only the unit being handled (no
// answer, or an answer that could not be accepted), so the operation can
// go on. Anything else (closed or broken link, cancelled context) stops it.
func perUnit(err error) bool {
	var uc *mk2.UnknownCommandError
	return errors.Is(err, mk2.ErrTimeout) || errors.Is(err, mk2.ErrUnexpectedReply) ||
		errors.Is(err, mk2.ErrShortReply) || errors.As(err, &uc)
}
