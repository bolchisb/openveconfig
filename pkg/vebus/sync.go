package vebus

import (
	"context"
	"errors"
	"fmt"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// Reading and writing a whole device.
//
// Policy. Facts marked "reverse-engineered (#5)" come from this project's
// behavioural spec of the original tool's MK2 driver:
//
//   - Retries: the original tool repeats an info request, a read and a
//     write once when it gets no answer (reverse-engineered (#5),
//     OBSERVED). DefaultRetries keeps that: one retry, and only after a
//     timeout. "Access level required", "not supported" and "unknown
//     command" are answers, so they are never retried.
//   - Stop or continue: the original stops a read-all at the first variable
//     that fails twice, and a write run at the first write that still fails
//     after its retry; the remaining writes are skipped (reverse-engineered
//     (#5), OBSERVED). ReadAll and Write do the same by default;
//     ContinueOnError records the failure and goes on with the next ID.
//     Errors that are not about one ID (closed link, cancelled context)
//     always stop.
//   - Write order: flag words first, then the other settings (reverse-
//     engineered (#5), OBSERVED). Flag words are written read-modify-write
//     (§7.3.13.3 warnings; COMMUNITY: FINDINGS.md §8; reverse-engineered
//     (#5), OBSERVED).
//   - Write command: CommandWriteViaID (0x37, §7.3.11) when the Client is in
//     Winmon mode 2, else WriteSetting (0x33/0x34 unless the Client's
//     Options.WriteMethod says otherwise). The original uses 0x37 for a
//     variable whose info reply came in the long (mode 2) form, otherwise
//     0x33/0x34 (reverse-engineered (#5), OBSERVED). mk2.Client parses info
//     replies in the long form exactly when it is in mode 2, so the mode
//     stands for the form (INFERRED). 0x37 carries an 8-bit ID; settings
//     above 255 use WriteSetting.
//   - Verification (this project's choice): every write is read back.
//     A missing acknowledgement, or a read-back that still shows the
//     previous value, counts as "did not arrive" and the write is repeated
//     within the retry budget. A read-back of a third value is a failure
//     without retry, so a device that adjusts a value is not written again
//     and again (EEPROM write cycles are limited).
//   - RAM variables are live values, not settings: Write never writes them.

// DefaultRetries is how often ReadAll and Write repeat a request that got
// no answer.
const DefaultRetries = 1

// retries maps an options value to a count: 0 means DefaultRetries, a
// negative value none.
func retries(n int) int {
	switch {
	case n == 0:
		return DefaultRetries
	case n < 0:
		return 0
	}
	return n
}

// withRetry runs f, repeating it up to n times while it times out.
func withRetry[T any](n int, f func() (T, error)) (T, error) {
	v, err := f()
	for i := 0; i < n && errors.Is(err, mk2.ErrTimeout); i++ {
		v, err = f()
	}
	return v, err
}

// perID reports whether err concerns only the variable being handled, so a
// run may go on with the next one. Anything else (closed link, transport
// error, cancelled context) ends the run.
func perID(err error) bool {
	var uc *mk2.UnknownCommandError
	var mm *MismatchError
	return errors.Is(err, mk2.ErrTimeout) || errors.Is(err, mk2.ErrAccessLevel) ||
		errors.Is(err, mk2.ErrNotSupported) || errors.Is(err, mk2.ErrShortReply) ||
		errors.As(err, &uc) || errors.As(err, &mm)
}

// IDError is an error about one variable.
type IDError struct {
	ID  ID
	Err error
}

func (e *IDError) Error() string { return fmt.Sprintf("vebus: %s: %v", e.ID, e.Err) }
func (e *IDError) Unwrap() error { return e.Err }

// MismatchError reports a value read back after a write that is not the
// value written.
type MismatchError struct {
	Wrote, Read uint16
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("wrote raw %d but read back raw %d", e.Wrote, e.Read)
}

// ReadOptions configures ReadAll.
type ReadOptions struct {
	// Settings and RAM list the IDs to try. nil means every setting or RAM
	// variable in the catalogue; an empty, non-nil slice means none. The
	// protocol cannot list the IDs a device has, so IDs outside the
	// catalogue must be given here.
	Settings, RAM []uint16
	// Retries is how often a request that got no answer is repeated: 0
	// means DefaultRetries, a negative value none. It comes on top of the
	// Client's own Options.Retries.
	Retries int
	// ContinueOnError records a variable that cannot be read and goes on
	// with the next one. Without it the first such variable ends the read.
	ContinueOnError bool
	// Progress, if set, is called after each ID with the number done and
	// the total.
	Progress func(done, total int)
}

// ReadReport says what ReadAll found.
type ReadReport struct {
	Address     byte
	Read        []ID       // info and value read
	Unsupported []ID       // reported as not supported by the device
	Failed      []*IDError // could not be read, after retries
}

// ReadAll builds a model of the device at addr by reading, through c, the
// info (options) and value of every setting and RAM variable that opts
// lists (see ReadOptions). It selects addr first. Variables the device
// reports as unsupported are left out of the model and listed in the
// report.
//
// On an error the partial model and the report are returned with it. An
// error about one variable is an *IDError.
func ReadAll(ctx context.Context, c *mk2.Client, addr byte, cat *Catalogue, opts ReadOptions) (*Device, *ReadReport, error) {
	n := retries(opts.Retries)
	d := NewDevice(cat)
	d.Address = addr
	rep := &ReadReport{Address: addr}
	if _, err := withRetry(n, func() (struct{}, error) { return struct{}{}, c.SetAddress(ctx, addr) }); err != nil {
		return d, rep, fmt.Errorf("vebus: select address %d: %w", addr, err)
	}
	fw, err := withRetry(n, func() (uint32, error) { return c.SoftwareVersion(ctx) })
	if err != nil {
		return d, rep, fmt.Errorf("vebus: firmware version: %w", err)
	}
	d.Firmware = fw

	ids := readList(d.cat, opts)
	var errs []error
	for i, id := range ids {
		err := readOne(ctx, c, d, id, n)
		switch {
		case err == nil:
			rep.Read = append(rep.Read, id)
		case errors.Is(err, mk2.ErrNotSupported):
			rep.Unsupported = append(rep.Unsupported, id)
		default:
			ie := &IDError{ID: id, Err: err}
			if !perID(err) {
				return d, rep, ie
			}
			rep.Failed = append(rep.Failed, ie)
			if !opts.ContinueOnError {
				return d, rep, ie
			}
			errs = append(errs, ie)
		}
		if opts.Progress != nil {
			opts.Progress(i+1, len(ids))
		}
	}
	return d, rep, errors.Join(errs...)
}

// readList returns the IDs ReadAll tries.
func readList(cat *Catalogue, opts ReadOptions) []ID {
	var ids []ID
	add := func(list []uint16, kind Kind) {
		if list != nil {
			for _, n := range list {
				ids = append(ids, ID{kind, n})
			}
			return
		}
		for _, e := range cat.entries {
			if e.Kind == kind && (kind != RAM || e.Num <= 0xFF) {
				ids = append(ids, e.ID())
			}
		}
	}
	add(opts.Settings, Setting)
	add(opts.RAM, RAM)
	return ids
}

// readOne reads the info and value of one setting or RAM variable into d.
func readOne(ctx context.Context, c *mk2.Client, d *Device, id ID, n int) error {
	switch id.Kind {
	case Setting:
		info, err := withRetry(n, func() (mk2.SettingInfo, error) { return c.SettingInfo(ctx, id.Num) })
		if err != nil {
			return fmt.Errorf("info: %w", err)
		}
		raw, err := withRetry(n, func() (uint16, error) { return c.ReadSetting(ctx, id.Num) })
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		d.AddSetting(id.Num, info, raw)
	case RAM:
		if id.Num > 0xFF {
			return fmt.Errorf("RAM IDs are 8-bit: %w", mk2.ErrNotSupported)
		}
		info, err := withRetry(n, func() (mk2.RAMInfo, error) { return c.RAMVarInfo(ctx, id.Num) })
		if err != nil {
			return fmt.Errorf("info: %w", err)
		}
		vals, err := withRetry(n, func() ([]uint16, error) { return c.ReadRAMVar(ctx, byte(id.Num)) })
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		d.AddRAM(id.Num, info, vals[0])
	default:
		return fmt.Errorf("cannot read a %s", id.Kind)
	}
	return nil
}

// ReadUnits runs ReadAll for each address in turn, for a multi-unit
// system. It stops at the first unit whose read returns an error and
// returns the models and reports so far, that unit's included.
func ReadUnits(ctx context.Context, c *mk2.Client, addrs []byte, cat *Catalogue, opts ReadOptions) ([]*Device, []*ReadReport, error) {
	var devs []*Device
	var reps []*ReadReport
	for _, a := range addrs {
		d, rep, err := ReadAll(ctx, c, a, cat, opts)
		devs, reps = append(devs, d), append(reps, rep)
		if err != nil {
			return devs, reps, fmt.Errorf("device %d: %w", a, err)
		}
	}
	return devs, reps, nil
}

// WriteStatus is the outcome of writing one setting.
type WriteStatus int

const (
	// StatusPlanned: a dry run; the value would be written.
	StatusPlanned WriteStatus = iota + 1
	// StatusVerified: written and read back with the value written.
	StatusVerified
	// StatusUnchanged: a flag word whose bits the device already holds as
	// wanted; nothing was written.
	StatusUnchanged
	// StatusSkipped: not attempted, because validation failed or an
	// earlier failure ended the run.
	StatusSkipped
	// StatusFailed: the write failed; Err says why.
	StatusFailed
)

func (s WriteStatus) String() string {
	switch s {
	case StatusPlanned:
		return "planned"
	case StatusVerified:
		return "verified"
	case StatusUnchanged:
		return "unchanged"
	case StatusSkipped:
		return "skipped"
	case StatusFailed:
		return "failed"
	}
	return fmt.Sprintf("WriteStatus(%d)", int(s))
}

// WriteResult is the outcome for one setting.
type WriteResult struct {
	ID ID // a setting; flag words stand for their flags
	// Old is the device's value before the write, if known (OldKnown):
	// the last value read, or for a flag word the word read just before
	// writing it.
	Old      uint16
	OldKnown bool
	// New is the value to write. For a flag word it is the merged word:
	// the device's bits with the changed flags applied.
	New    uint16
	Status WriteStatus
	Err    error // StatusFailed only
	// ResetRequired: the change takes effect only after a device reset
	// (Device.ResetRequired of the setting, or of a flag that changes).
	ResetRequired bool
}

// WriteReport lists the outcome of Write, one result per setting, in the
// order they were (or would be) written.
type WriteReport struct {
	Address byte
	Results []WriteResult
}

// ResetRequired returns the settings that were written (or, in a dry run,
// would be) and need a device reset to take effect.
func (r *WriteReport) ResetRequired() []ID {
	var ids []ID
	for _, x := range r.Results {
		if x.ResetRequired && (x.Status == StatusVerified || x.Status == StatusPlanned) {
			ids = append(ids, x.ID)
		}
	}
	return ids
}

// WriteOptions configures Write.
type WriteOptions struct {
	// All writes every supported setting, not only the changed ones.
	// Settings that hold NotApplicable and were not changed are left out.
	// Without All only dirty settings are written, so unchanged values
	// never cost an EEPROM write cycle.
	All bool
	// DryRun validates and plans the writes but sends nothing; c may be
	// nil.
	DryRun bool
	// Override skips validation (Device.CheckWrite). Only an explicit user
	// override should set it.
	Override bool
	// ContinueOnError goes on with the next setting after a failed one.
	// Without it the first failure ends the run and the rest are skipped,
	// as the original tool does.
	ContinueOnError bool
	// Retries is how often a write (or a read for it) that got no answer is
	// repeated: 0 means DefaultRetries, a negative value none. It comes on
	// top of the Client's own Options.Retries.
	Retries int
	// Progress, if set, is called after each setting with the number done
	// and the total.
	Progress func(done, total int)
}

// Write writes the settings of d to the device at d.Address through c and
// reads each one back. It validates first (d.CheckWrite(opts.Override));
// on a validation error nothing is sent, every result is StatusSkipped and
// the error is a *ValidationError.
//
// The model follows the device: a verified setting gets the value read
// back as its current and last-read value. After a failure the setting
// stays dirty with the user's value; its last-read value is what the
// device was read to hold, or unknown (the setting counts as never read)
// when the read-back failed too.
//
// The error is nil when every planned write was verified (or unchanged).
// Otherwise it joins the *IDError of each failed setting, or is the error
// that ended the run. The report is returned in every case.
func Write(ctx context.Context, c *mk2.Client, d *Device, opts WriteOptions) (*WriteReport, error) {
	rep := &WriteReport{Address: d.Address}
	for _, id := range d.writeList(opts.All) {
		rep.Results = append(rep.Results, d.planWrite(id, opts.All))
	}
	setAll := func(s WriteStatus, from int) {
		for i := from; i < len(rep.Results); i++ {
			rep.Results[i].Status = s
		}
	}
	setAll(StatusPlanned, 0)
	if err := d.CheckWrite(opts.Override); err != nil {
		setAll(StatusSkipped, 0)
		return rep, err
	}
	if opts.DryRun || len(rep.Results) == 0 {
		return rep, nil
	}
	n := retries(opts.Retries)
	if _, err := withRetry(n, func() (struct{}, error) { return struct{}{}, c.SetAddress(ctx, d.Address) }); err != nil {
		setAll(StatusSkipped, 0)
		return rep, fmt.Errorf("vebus: select address %d: %w", d.Address, err)
	}
	var errs []error
	for i := range rep.Results {
		r := &rep.Results[i]
		err := d.writeOne(ctx, c, r, opts.All, n)
		if opts.Progress != nil {
			opts.Progress(i+1, len(rep.Results))
		}
		if err == nil {
			continue
		}
		ie := &IDError{ID: r.ID, Err: err}
		r.Status, r.Err = StatusFailed, err
		if !perID(err) {
			setAll(StatusSkipped, i+1)
			return rep, ie
		}
		errs = append(errs, ie)
		if !opts.ContinueOnError {
			setAll(StatusSkipped, i+1)
			break
		}
	}
	return rep, errors.Join(errs...)
}

// WriteUnits runs Write for each model in turn, for a multi-unit system;
// each model carries its device's address. Without opts.ContinueOnError it
// stops after the first unit whose Write returns an error; with it, it goes
// on with the next unit unless ctx has ended.
func WriteUnits(ctx context.Context, c *mk2.Client, devs []*Device, opts WriteOptions) ([]*WriteReport, error) {
	var reps []*WriteReport
	var errs []error
	for _, d := range devs {
		rep, err := Write(ctx, c, d, opts)
		reps = append(reps, rep)
		if err == nil {
			continue
		}
		err = fmt.Errorf("device %d: %w", d.Address, err)
		if !opts.ContinueOnError || ctx.Err() != nil {
			return reps, err
		}
		errs = append(errs, err)
	}
	return reps, errors.Join(errs...)
}

// writeList returns the settings Write handles: flag words first, then the
// others, each in ascending order.
func (d *Device) writeList(all bool) []ID {
	var words, others []ID
	for _, n := range d.SettingIDs() {
		id := SettingID(n)
		dirty := d.Dirty(id)
		if !dirty && (!all || d.NotApplicable(id)) {
			continue
		}
		if d.cat.IsFlagWord(n) {
			words = append(words, id)
		} else {
			others = append(others, id)
		}
	}
	return append(words, others...)
}

// flagMask returns the bits of flag word s that Write sets from the model.
func (d *Device) flagMask(s *settingVar, all bool) uint16 {
	if all || !s.hasRead {
		return s.info.Max
	}
	return (s.value ^ s.read) & s.info.Max
}

// planWrite returns the result for setting id before anything is sent.
func (d *Device) planWrite(id ID, all bool) WriteResult {
	s := d.settings[id.Num]
	r := WriteResult{ID: id, Old: s.read, OldKnown: s.hasRead, New: s.value}
	if d.cat.IsFlagWord(id.Num) && s.hasRead {
		mask := d.flagMask(s, all)
		r.New = s.read&^mask | s.value&mask
	}
	r.ResetRequired = d.writeNeedsReset(id, r.Old, r.New, r.OldKnown)
	return r
}

// writeNeedsReset reports whether changing setting id from old to new needs
// a device reset. For a flag word it asks each changed flag.
func (d *Device) writeNeedsReset(id ID, old, new uint16, oldKnown bool) bool {
	if d.ResetRequired(id) {
		return true
	}
	first, ok := d.cat.flagsIn(id.Num)
	if !ok {
		return false
	}
	changed := old ^ new
	if !oldKnown {
		changed = 0xFFFF
	}
	for bit := uint16(0); bit < 16; bit++ {
		if changed>>bit&1 != 0 && d.ResetRequired(FlagID(first+bit)) {
			return true
		}
	}
	return false
}

// writeOne writes and verifies one setting and updates r and the model.
func (d *Device) writeOne(ctx context.Context, c *mk2.Client, r *WriteResult, all bool, n int) error {
	s := d.settings[r.ID.Num]
	num := r.ID.Num
	flagWord := d.cat.IsFlagWord(num)
	mask := d.flagMask(s, all)
	old, oldKnown := s.read, s.hasRead
	for attempt := 0; ; attempt++ {
		want := s.value
		if flagWord {
			// Read-modify-write: keep the bits the user did not change as
			// the device has them now.
			cur, err := withRetry(n, func() (uint16, error) { return c.ReadSetting(ctx, num) })
			if err != nil {
				return fmt.Errorf("read before write: %w", err)
			}
			want = cur&^mask | s.value&mask
			if attempt == 0 {
				old, oldKnown = cur, true
				r.Old, r.OldKnown, r.New = cur, true, want
				r.ResetRequired = d.writeNeedsReset(r.ID, cur, want, true)
			}
			if want == cur && !all {
				s.value, s.read, s.hasRead = cur, cur, true
				r.Status = StatusUnchanged
				return nil
			}
		}
		werr := writeSetting(ctx, c, num, want)
		if werr != nil && !errors.Is(werr, mk2.ErrTimeout) {
			if !perID(werr) {
				s.hasRead = false // the write may or may not have arrived
			}
			// The device answered with a refusal: its value is unchanged.
			return fmt.Errorf("write: %w", werr)
		}
		got, rerr := withRetry(n, func() (uint16, error) { return c.ReadSetting(ctx, num) })
		if rerr != nil {
			s.hasRead = false // the device's value is unknown
			if werr != nil {
				return fmt.Errorf("write: %w; read back: %w", werr, rerr)
			}
			return fmt.Errorf("read back: %w", rerr)
		}
		if got == want {
			s.value, s.read, s.hasRead = got, got, true
			r.Status = StatusVerified
			return nil
		}
		// The device holds got. Keep the user's changes dirty on top of it.
		if flagWord {
			s.value = got&^mask | s.value&mask
		}
		s.read, s.hasRead = got, true
		lost := werr != nil || !oldKnown || got == old
		if !lost || attempt >= n {
			if werr != nil {
				return fmt.Errorf("write: %w", werr)
			}
			return &MismatchError{Wrote: want, Read: got}
		}
	}
}

// writeSetting writes one setting with the command chosen as described at
// the top of this file.
func writeSetting(ctx context.Context, c *mk2.Client, id, v uint16) error {
	if c.WinmonMode() == mk2.WinmonLong && id <= 0xFF {
		return c.WriteViaID(ctx, mk2.ViaIDSetting, byte(id), v)
	}
	return c.WriteSetting(ctx, id, v)
}
