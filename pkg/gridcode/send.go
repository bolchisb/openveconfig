package gridcode

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Sending grid data straight to a device (#52). A changed grid code (setting
// 81) and changed grid parameters (settings 129-190) are written together
// with the two check words (settings 128 and 191) kept consistent, in a
// fixed order (SendPlan). Send runs the full sequence against any device.
// A real device requires the installer password for the grid words and
// refuses the writes itself (through WriteSetting) when it is not given; the
// grid settings reach a device only through this dedicated path, never with
// the ordinary bulk "Send settings" (CheckDeviceWrite).

// SendWrite is one setting write of a grid send: a setting ID (81, or a grid
// word 128-191) and its new raw value.
type SendWrite struct {
	ID    uint16
	Value uint16
}

// SendPlan builds the ordered writes that send grid code g, with its changed
// parameter words params, to a device whose current check words are old,
// keeping the two check words consistent (Stamp). v is the parameter-set
// version of g (ParamSetVersion). params holds the parameter words to write
// (129-190) with their new raw values; the check words 128 and 191 are
// computed here and must not appear in params.
//
// The order, in this project's own words from #52: setting 81 (the grid
// code), then check word A (128) stamped with the next generation, then the
// parameter words (129-190, in ascending order), then check word B (191)
// last. B is written last so that an interrupted send leaves A and B with
// different generations and the device refuses the grid data rather than
// running with a half-written parameter set. Reverse-engineered (#9, #27);
// the write order is INFERRED and not yet checked on hardware.
func SendPlan(old Pair, g Code, v uint8, params map[uint16]uint16) ([]SendWrite, error) {
	for id := range params {
		if id <= CheckerWordA || id >= CheckerWordB {
			return nil, fmt.Errorf("gridcode: %d is not a grid parameter word (129-190)", id)
		}
	}
	next, err := Stamp(old, g, v)
	if err != nil {
		return nil, err
	}
	ids := make([]uint16, 0, len(params))
	for id := range params {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	plan := make([]SendWrite, 0, len(params)+3)
	plan = append(plan, SendWrite{ID: 81, Value: uint16(g)})
	plan = append(plan, SendWrite{ID: CheckerWordA, Value: next.A})
	for _, id := range ids {
		plan = append(plan, SendWrite{ID: id, Value: params[id]})
	}
	plan = append(plan, SendWrite{ID: CheckerWordB, Value: next.B})
	return plan, nil
}

// PlanIDs returns the setting IDs of plan, in order.
func PlanIDs(plan []SendWrite) []uint16 {
	ids := make([]uint16, len(plan))
	for i, w := range plan {
		ids[i] = w.ID
	}
	return ids
}

// SettingWriter writes one setting to a device. *mk2.Client satisfies it.
type SettingWriter interface {
	WriteSetting(ctx context.Context, id uint16, value uint16) error
}

// SendReport is the outcome of a completed Send: the writes that reached the
// device, in order.
type SendReport struct {
	Done []SendWrite
}

// SendError reports a grid send that was refused up front or stopped partway.
// Done are the writes that reached the device, in order, so the caller can
// tell the user what state the device was left in; Pending are the writes that
// were not attempted. At, if not nil, is the write that failed (nil when the
// send was refused before any write). Err is the cause.
type SendError struct {
	Done    []SendWrite
	Pending []SendWrite
	At      *SendWrite
	Err     error
}

func (e *SendError) Error() string {
	switch {
	case len(e.Done) == 0:
		return fmt.Sprintf("gridcode: grid send refused before any write: %v", e.Err)
	case e.At != nil:
		return fmt.Sprintf("gridcode: grid send stopped at setting %d after writing settings %s: %v",
			e.At.ID, joinIDs(e.Done), e.Err)
	default:
		return fmt.Sprintf("gridcode: grid send stopped after writing settings %s: %v", joinIDs(e.Done), e.Err)
	}
}

func (e *SendError) Unwrap() error { return e.Err }

func joinIDs(ws []SendWrite) string {
	s := make([]string, len(ws))
	for i, w := range ws {
		s[i] = strconv.Itoa(int(w.ID))
	}
	return strings.Join(s, ", ")
}

// Send writes the grid plan to the device behind w, in plan order. A write
// the device refuses (for example a password-required check word) or a
// cancelled context stops the send; the returned *SendError then holds the
// writes that already reached the device (Done) and those that did not
// (Pending, with At the one that failed), so the caller can report the state
// the device was left in. On success it returns a *SendReport and a nil error.
func Send(ctx context.Context, w SettingWriter, plan []SendWrite) (*SendReport, error) {
	var done []SendWrite
	for i, wr := range plan {
		if err := ctx.Err(); err != nil {
			return nil, &SendError{Done: done, Pending: slices.Clone(plan[i:]), Err: err}
		}
		if err := w.WriteSetting(ctx, wr.ID, wr.Value); err != nil {
			failed := wr
			return nil, &SendError{Done: done, Pending: slices.Clone(plan[i+1:]), At: &failed, Err: err}
		}
		done = append(done, wr)
	}
	return &SendReport{Done: done}, nil
}
