package system

import (
	"fmt"
)

// QuickLayout is one of the fixed layouts of Quick Configure. Each is a
// Layout with one AC input switched as a group and the units at positions
// 0..n-1 of their cells in switch-on order (reverse-engineered (#10)):
// parallel layouts put every unit in L1, the others put unit i in phase i.
type QuickLayout uint8

const (
	QuickParallel2    QuickLayout = iota // two units in parallel
	QuickParallel3                       // three units in parallel
	QuickSplit120                        // dual phase 120°, fixed
	QuickSplit180                        // split phase 180°, fixed
	QuickSplit240                        // dual phase 240°, fixed
	QuickSplit180Auto                    // split phase 180°, floating with return (automatic detection)
	QuickThreePhase                      // three phase, fixed
	QuickStandalone                      // one unit on its own
)

// QuickLayouts lists every Quick Configure layout.
var QuickLayouts = []QuickLayout{
	QuickParallel2, QuickParallel3, QuickSplit120, QuickSplit180, QuickSplit240,
	QuickSplit180Auto, QuickThreePhase, QuickStandalone,
}

func (q QuickLayout) String() string {
	switch q {
	case QuickParallel2:
		return "parallel2"
	case QuickParallel3:
		return "parallel3"
	case QuickSplit120:
		return "split120"
	case QuickSplit180:
		return "split180"
	case QuickSplit240:
		return "split240"
	case QuickSplit180Auto:
		return "split180-auto"
	case QuickThreePhase:
		return "three-phase"
	case QuickStandalone:
		return "standalone"
	}
	return fmt.Sprintf("QuickLayout(%d)", uint8(q))
}

// setup returns the setup and L2 option of q.
func (q QuickLayout) setup() (Setup, L2Option) {
	switch q {
	case QuickSplit120:
		return Dual120, Fixed
	case QuickSplit180:
		return Dual180, Fixed
	case QuickSplit240:
		return Dual240, Fixed
	case QuickSplit180Auto:
		return Dual180, FloatingReturn
	case QuickThreePhase:
		return ThreePhase, Fixed
	}
	return SinglePhase, Fixed
}

// Parallel reports a layout whose units all share L1.
func (q QuickLayout) Parallel() bool {
	s, _ := q.setup()
	return s == SinglePhase
}

// Units returns the number of units of the layout.
func (q QuickLayout) Units() int {
	switch q {
	case QuickParallel2:
		return 2
	case QuickParallel3:
		return 3
	case QuickStandalone:
		return 1
	}
	s, _ := q.setup()
	return s.Phases()
}

// Layout returns the layout of q with units, given in switch-on order, at
// their roles: in L1 at positions 0..n-1 for the parallel layouts and the
// standalone one, unit i in phase i otherwise.
func (q QuickLayout) Layout(units []*Unit) (*Layout, error) {
	if q > QuickStandalone {
		return nil, fmt.Errorf("system: unknown Quick Configure layout %d", uint8(q))
	}
	if len(units) != q.Units() {
		return nil, fmt.Errorf("system: layout %s needs %d units, got %d", q, q.Units(), len(units))
	}
	s, opt := q.setup()
	l := NewLayout()
	if err := l.Reshape(s, 1); err != nil {
		return nil, err
	}
	l.L2 = opt
	for i, u := range units {
		c := Cell{Phase: i, AC: 0}
		if q.Parallel() {
			c.Phase = 0
		}
		if err := l.Add(u); err != nil {
			return nil, err
		}
		if err := l.Place(u, c); err != nil {
			return nil, err
		}
	}
	return l, nil
}

// Records builds the records of q for units in switch-on order, as Layout
// and Layout.Records do, and then sets byte 7 bit 5 on every unit: Quick
// Configure writes its records that way, also for dual-input units
// (reverse-engineered (#10)). The result is in role order.
func (q QuickLayout) Records(units []*Unit, send bool) ([]UnitRecord, error) {
	l, err := q.Layout(units)
	if err != nil {
		return nil, err
	}
	recs := l.Records(send)
	for i := range recs {
		recs[i].Record.SetSecondInputGrouped(true)
	}
	return recs, nil
}

// Standalone returns the record that makes u a unit on its own: the
// QuickStandalone record, 01 01 01 00 00 00 00 E0 00 with byte 7 bit 4 per
// the rule of Layout.Records (so … F0 00 when sending to a unit the flag
// applies to). It equals the factory record.
func Standalone(u *Unit, send bool) Record {
	recs, err := QuickStandalone.Records([]*Unit{u}, send)
	if err != nil {
		panic(err) // cannot happen: one unit for a one-unit layout
	}
	return recs[0].Record
}

// RecognizeQuick reports which Quick Configure layout the units' records
// form, and returns the units in role order. Byte 7 bit 4 is ignored (the
// units' records are not changed); otherwise each unit's record must equal
// the layout's record for the role with the same short address, all nine
// bytes (reverse-engineered (#10); pairing by short address is our design).
// The layouts' records are built for single-input units without external
// BF; with one AC input, Records gives the same bytes for any other unit.
func RecognizeQuick(units []*Unit) (QuickLayout, []*Unit, bool) {
	for _, q := range QuickLayouts {
		if len(units) != q.Units() {
			continue
		}
		roles := make([]*Unit, q.Units())
		for i := range roles {
			roles[i] = &Unit{}
		}
		recs, err := q.Records(roles, false)
		if err != nil {
			continue
		}
		ordered := make([]*Unit, len(recs))
		ok := true
		for _, u := range units {
			got := u.Record
			got.SetAssistantResetFlag(false)
			i := roleWithAddress(recs, got.ShortAddress())
			if i < 0 || ordered[i] != nil {
				ok = false
				break
			}
			want := recs[i].Record
			want.SetAssistantResetFlag(false)
			if got != want {
				ok = false
				break
			}
			ordered[i] = u
		}
		if ok {
			return q, ordered, true
		}
	}
	return 0, nil, false
}

func roleWithAddress(recs []UnitRecord, a byte) int {
	for i, r := range recs {
		if r.Record.ShortAddress() == a {
			return i
		}
	}
	return -1
}

// Quick Configure's checks of each newly switched-on unit (CheckQuickUnit).
const (
	RuleQuickFirmware   Rule = "QC1" // firmware differs from the first unit's
	RuleQuickLetter     Rule = "QC2" // compatibility letter differs (parallel layouts)
	RuleQuickSplitModel Rule = "QC3" // 120/240 V split-phase model in a multi-phase layout
)

// CheckQuickUnit runs Quick Configure's checks on a newly switched-on unit
// u against the first unit of layout q (reverse-engineered (#10)). For the
// first unit itself pass it as both first and u. All findings are errors:
//   - the full firmware number must equal the first unit's;
//   - in parallel layouts the compatibility letter must equal the first
//     unit's;
//   - in multi-phase layouts the unit must not be a 120/240 V split-phase
//     model (Unit.SplitPhaseModel).
func CheckQuickUnit(q QuickLayout, first, u *Unit) []Issue {
	var out []Issue
	if u.Firmware() != first.Firmware() {
		out = append(out, errorIssue(RuleQuickFirmware, []*Unit{u}, "Only units of the same type and firmware version can be combined."))
	}
	if q.Parallel() && u.Letter != first.Letter {
		out = append(out, errorIssue(RuleQuickLetter, []*Unit{u}, "This unit's compatibility letter (%s) differs from the first unit's (%s); units in one phase need the same letter.",
			u.LetterString(), first.LetterString()))
	}
	if !q.Parallel() && u.SplitPhaseModel() {
		out = append(out, errorIssue(RuleQuickSplitModel, []*Unit{u}, "120/240 V split-phase models cannot be used in a multi-phase system."))
	}
	return out
}
