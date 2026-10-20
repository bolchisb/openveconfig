package bus

import (
	"context"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// WriteOptions are the confirmations of a writing operation. The zero
// value is a dry run.
type WriteOptions struct {
	// Confirm performs the operation. Without it the operation only reads
	// (the snapshot) and reports what it would send; nothing is written
	// and the system is not reset. Front ends set it after the user has
	// reviewed the records and accepted that the whole system switches
	// off for a moment.
	Confirm bool
	// RemoveAssistants confirms, separately, that the write may remove the
	// assistants of WriteReport.AssistantsRemoved. A confirmed write that
	// would remove assistants stops before writing without it
	// (KindAssistants).
	RemoveAssistants bool
}

// Status is the outcome for one unit of a write.
type Status int

const (
	// StatusPlanned: a dry run, or the operation stopped before writing
	// (a check or the snapshot failed).
	StatusPlanned Status = iota + 1
	// StatusVerified: the new record was written and read back.
	StatusVerified
	// StatusNotWritten: writing stopped before this unit; nothing was sent
	// to it.
	StatusNotWritten
	// StatusRestored: the unit was written, then its previous record was
	// written back and read back.
	StatusRestored
	// StatusRestoreFailed: the previous record could not be written back
	// and read back. UnitResult.Holds is the last record read from the
	// unit, if any.
	StatusRestoreFailed
)

func (s Status) String() string {
	switch s {
	case StatusPlanned:
		return "planned"
	case StatusVerified:
		return "verified"
	case StatusNotWritten:
		return "not written"
	case StatusRestored:
		return "restored"
	case StatusRestoreFailed:
		return "restore failed"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// UnitResult is what happened to one unit.
type UnitResult struct {
	Unit *system.Unit
	Cell system.Cell
	// Old is the unit's record before writing: the snapshot once it was
	// read, before that the unit's last known record. New is the record to
	// write.
	Old, New system.Record
	Status   Status
	// Writes counts the write frames sent to the unit, including the
	// rollback (at most 2 × WriteAttempts).
	Writes int
	// Holds is the last record read back from the unit after a write, if
	// HoldsKnown.
	Holds      system.Record
	HoldsKnown bool
	// Err says why writing the unit, or restoring it, failed.
	Err error
}

// WriteReport says what a writing operation did, or in a dry run would do.
type WriteReport struct {
	DryRun bool
	// Units are in write order (system.Layout.Records order).
	Units []UnitResult
	// Issues are the system check and bus check results, warnings
	// included.
	Issues []system.Issue
	// AssistantsRemoved are the units from which the write removes the
	// assistants: the assistant-reset flag is set in the new record and
	// clear in the snapshot (rule W4).
	AssistantsRemoved []*system.Unit
	// Ops lists the steps in order, for a dry run's output.
	Ops []string
	// Failed is the unit whose write could not be verified, if any.
	Failed *system.Unit
	// Reset reports that the system reset was sent.
	Reset bool
	// After is the last scan after the reset.
	After *ScanReport
	// NotBack are the units that were not found after the reset, or do
	// not hold, or do not use, the record that was written.
	NotBack []*system.Unit
}

// Write sends the configuration of layout l to the bus ("Send
// configuration"). The steps, and what happens on failure, are in the
// package documentation. Without opts.Confirm it is a dry run.
//
// It refuses before sending anything when the system check of l, or the
// bus check against the session's last scan, has an error (KindBlocked).
// The report is returned in every case where one was started.
func (s *Session) Write(ctx context.Context, l *system.Layout, opts WriteOptions, p Progress) (*WriteReport, error) {
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	return s.write(ctx, l, l.Records(true), opts, p)
}

// WriteQuick sends the Quick Configure layout q with units in switch-on
// order (see system.QuickLayout.Records), as Write does.
func (s *Session) WriteQuick(ctx context.Context, q system.QuickLayout, units []*system.Unit, opts WriteOptions, p Progress) (*WriteReport, error) {
	l, err := q.Layout(units)
	if err != nil {
		return nil, err
	}
	recs, err := q.Records(units, true)
	if err != nil {
		return nil, err
	}
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	return s.write(ctx, l, recs, opts, p)
}

// ResetStandalone makes the one unit that is switched on a unit on its own
// ("Reset a unit to standalone"). The user first switches every unit off
// and then only the unit to reset on. It scans; exactly one unit must
// answer (otherwise KindUnitCount and nothing is written). It then writes
// the standalone record (system.Standalone) as Write does: snapshot,
// verify, rollback, reset and check. Only the system record changes; no
// other setting is touched (reverse-engineered (#53)).
func (s *Session) ResetStandalone(ctx context.Context, opts WriteOptions, p Progress) (*WriteReport, error) {
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if err := s.prepare(ctx, p); err != nil {
		return nil, err
	}
	sr, err := s.scan(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(sr.Units) != 1 {
		return nil, &Error{Kind: KindUnitCount, Found: len(sr.Units)}
	}
	l, err := system.QuickStandalone.Layout(sr.Units)
	if err != nil {
		return nil, err
	}
	recs, err := system.QuickStandalone.Records(sr.Units, true)
	if err != nil {
		return nil, err
	}
	return s.write(ctx, l, recs, opts, p)
}

// Reset restarts every unit with the system reset, waits and finds the
// units again; each unit of the last scan must come back with its record
// and use the short address in it (otherwise KindNotBack). All units switch
// off for a moment, so loads lose power. Without opts.Confirm it only
// reports the steps.
func (s *Session) Reset(ctx context.Context, opts WriteOptions, p Progress) (*WriteReport, error) {
	if err := s.lock(); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	rep := &WriteReport{DryRun: !opts.Confirm, Ops: s.resetOps()}
	if !opts.Confirm {
		return rep, nil
	}
	if err := s.prepare(ctx, p); err != nil {
		return rep, err
	}
	want := make([]UnitResult, len(s.units))
	for i, u := range s.units {
		want[i] = UnitResult{Unit: u, New: u.Record}
	}
	return rep, s.resetAndCheck(ctx, rep, want, p)
}

// write runs a write of recs, built from l. s.mu is held.
func (s *Session) write(ctx context.Context, l *system.Layout, recs []system.UnitRecord, opts WriteOptions, p Progress) (*WriteReport, error) {
	rep := &WriteReport{DryRun: !opts.Confirm, Issues: l.Check()}
	if is := l.CheckBus(s.longIDs()); is != nil {
		rep.Issues = append(rep.Issues, *is)
	}
	for _, r := range recs {
		rep.Units = append(rep.Units, UnitResult{Unit: r.Unit, Cell: r.Cell, Old: r.Unit.Record, New: r.Record, Status: StatusPlanned})
	}
	rep.Ops = s.writeOps(rep.Units)
	if system.HasErrors(rep.Issues) {
		var errs []system.Issue
		for _, is := range rep.Issues {
			if is.Severity == system.Error {
				errs = append(errs, is)
			}
		}
		return rep, &Error{Kind: KindBlocked, Issues: errs}
	}
	if err := s.prepare(ctx, p); err != nil {
		return rep, err
	}

	// The snapshot: what a rollback restores.
	for i := range rep.Units {
		r := &rep.Units[i]
		p.send(Event{Step: StepSnapshot, Unit: r.Unit, Done: i + 1, Total: len(rep.Units)})
		sc, err := s.read(ctx, r.Unit.LongID)
		if err != nil {
			if perUnit(err) {
				return rep, &Error{Kind: KindSnapshot, Units: []*system.Unit{r.Unit}, Err: err}
			}
			return rep, stopErr("read record", err)
		}
		r.Old = record(r.Unit, sc)
		if r.New.AssistantResetFlag() && !r.Old.AssistantResetFlag() {
			rep.AssistantsRemoved = append(rep.AssistantsRemoved, r.Unit)
		}
	}
	if !opts.Confirm {
		return rep, nil
	}
	if len(rep.AssistantsRemoved) > 0 && !opts.RemoveAssistants {
		return rep, &Error{Kind: KindAssistants, Units: rep.AssistantsRemoved}
	}

	// Write. The frames go out with a context that is not cancelled, so
	// an attempt always finishes; cancellation is checked between
	// attempts and leads to the rollback.
	io := context.WithoutCancel(ctx)
	for i := range rep.Units {
		r := &rep.Units[i]
		err := s.writeVerify(io, ctx, r, r.New, Event{Step: StepWrite, Unit: r.Unit, Done: i + 1, Total: len(rep.Units)}, p)
		if err != nil {
			if ctx.Err() == nil || err != ctx.Err() {
				rep.Failed, r.Err = r.Unit, err
			}
			return rep, s.rollback(io, rep, err, p)
		}
		r.Status = StatusVerified
	}
	if err := ctx.Err(); err != nil {
		return rep, s.rollback(io, rep, err, p)
	}
	return rep, s.resetAndCheck(ctx, rep, rep.Units, p)
}

// writeVerify writes rec to the unit of r, reads it back and compares, up
// to WriteAttempts times. Before each attempt it checks stop (nil: never
// stop) and returns its error when it is done. ctx is used for the frames.
func (s *Session) writeVerify(ctx, stop context.Context, r *UnitResult, rec system.Record, e Event, p Progress) error {
	var err error
	for a := 1; a <= WriteAttempts; a++ {
		if stop != nil && stop.Err() != nil {
			return stop.Err()
		}
		e.Attempt = a
		p.send(e)
		r.Writes++
		if err = s.c.WriteSystemConfig(ctx, r.Unit.LongID, rec); err != nil {
			if !perUnit(err) {
				return err
			}
			continue
		}
		var sc mk2.SystemConfig
		if sc, err = s.c.ReadSystemConfig(ctx, r.Unit.LongID); err != nil {
			if !perUnit(err) {
				return err
			}
			continue
		}
		r.Holds, r.HoldsKnown = system.Record(sc.Record), true
		if r.Holds == rec {
			return nil
		}
		err = &MismatchError{Wrote: rec, Read: r.Holds}
	}
	return err
}

// rollback writes the snapshot back to every unit that was written, with
// the same verification, and does not reset. cause is why writing stopped.
func (s *Session) rollback(ctx context.Context, rep *WriteReport, cause error, p Progress) error {
	var written []*UnitResult
	for i := range rep.Units {
		if r := &rep.Units[i]; r.Writes > 0 {
			written = append(written, r)
		} else {
			r.Status = StatusNotWritten
		}
	}
	var bad []*system.Unit
	for i, r := range written {
		err := s.writeVerify(ctx, nil, r, r.Old, Event{Step: StepRollback, Unit: r.Unit, Done: i + 1, Total: len(written)}, p)
		if err != nil {
			r.Status = StatusRestoreFailed
			if r.Err == nil {
				r.Err = err
			}
			bad = append(bad, r.Unit)
			continue
		}
		r.Status = StatusRestored
	}
	if len(bad) > 0 {
		return &Error{Kind: KindRollbackFailed, Units: bad, Err: cause}
	}
	e := &Error{Kind: KindRolledBack, Err: cause}
	if rep.Failed != nil {
		e.Units = []*system.Unit{rep.Failed}
	}
	return e
}

// resetAndCheck sends the system reset, waits, and finds the units again
// until every unit of want is back or Options.PostResetWindow has passed.
// Each unit of want must hold its New record and answer with its short
// address. s.mu is held.
func (s *Session) resetAndCheck(ctx context.Context, rep *WriteReport, want []UnitResult, p Progress) error {
	io := context.WithoutCancel(ctx)
	for i := range s.opts.ResetCount {
		p.send(Event{Step: StepReset, Done: i + 1, Total: s.opts.ResetCount})
		if err := s.c.ResetSystem(io); err != nil {
			return &Error{Kind: KindNotBack, Err: stopErr("reset", err)}
		}
		rep.Reset = true
		clear(s.flashing) // a restart ends flashing (inferred, #53)
	}
	p.send(Event{Step: StepWait})
	if err := sleep(ctx, s.opts.ResetWait); err != nil {
		return &Error{Kind: KindNotBack, Err: err}
	}
	p.send(Event{Step: StepVerify})
	if err := s.prepare(ctx, nil); err != nil {
		return &Error{Kind: KindNotBack, Err: err}
	}
	deadline := time.Now().Add(s.opts.PostResetWindow)
	base := s.units
	for {
		s.units = base // every pass knows the units from before the reset
		sr, err := s.scan(ctx, p)
		if err != nil {
			return &Error{Kind: KindNotBack, Err: err}
		}
		rep.After = sr
		if allFound(sr, want) || !time.Now().Before(deadline) {
			break
		}
	}
	for _, w := range want {
		if !s.holds(w.Unit.LongID, w.New) {
			rep.NotBack = append(rep.NotBack, w.Unit)
		}
	}
	if len(rep.NotBack) > 0 {
		return &Error{Kind: KindNotBack, Units: rep.NotBack}
	}
	return nil
}

func allFound(sr *ScanReport, want []UnitResult) bool {
	have := map[uint32]bool{}
	for _, u := range sr.Units {
		have[u.LongID] = true
	}
	for _, w := range want {
		if !have[w.Unit.LongID] {
			return false
		}
	}
	return true
}

// holds reports whether the last scan found the unit longID with record
// rec, answering with the short address in it (so it restarted with it).
// A unit that still reports no system configuration does not hold rec:
// the scan gives it the factory record.
func (s *Session) holds(longID uint32, rec system.Record) bool {
	for _, u := range s.units {
		if u.LongID == longID {
			a, ok := s.active[longID]
			return u.Record == rec && ok && a == rec.ShortAddress()
		}
	}
	return false
}

func (s *Session) longIDs() []uint32 {
	ids := make([]uint32, len(s.units))
	for i, u := range s.units {
		ids[i] = u.LongID
	}
	return ids
}

// writeOps describes the steps of a write, for a dry run.
func (s *Session) writeOps(rs []UnitResult) []string {
	var ops []string
	for _, r := range rs {
		ops = append(ops, fmt.Sprintf("read the record of %08X (snapshot)", r.Unit.LongID))
	}
	for _, r := range rs {
		ops = append(ops, fmt.Sprintf("write %s to %08X, read back and compare (up to %d attempts)", r.New.Hex(), r.Unit.LongID, WriteAttempts))
	}
	return append(ops, s.resetOps()...)
}

func (s *Session) resetOps() []string {
	var ops []string
	for range s.opts.ResetCount {
		ops = append(ops, "reset all units")
	}
	return append(ops,
		fmt.Sprintf("wait %v", s.opts.ResetWait),
		fmt.Sprintf("find the units again and compare their records (up to %v)", s.opts.PostResetWindow))
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
