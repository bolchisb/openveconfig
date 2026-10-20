package system

import (
	"cmp"
	"fmt"
	"slices"
)

// RuleInconsistent identifies the finding of FromRecords that the records
// read from the units do not form a valid system.
const RuleInconsistent Rule = "C"

// FromRecords rebuilds a layout from the records the units hold, after a
// scan or when a file is loaded.
//
// The records are consistent only if all of these hold (reverse-engineered
// (#10)); the Issue's Detail names the first one that fails:
//  1. every unit has the same presence mask;
//  2. the mask has as many addresses as there are units;
//  3. every unit's short address is in the mask;
//  4. the phases used are L1 up to some Ln without a gap, n is at most 3,
//     and every unit says the system has n phases;
//  5. units on the same AC input agree on its group flag; units in the same
//     phase agree on the phase angle and floating bits; L1 has angle 0 and
//     no floating bits;
//  6. no leader's unit count is larger than the number of units in its
//     phase;
//  7. every record has the marker bit;
//  8. the assistant-reset flag is clear where it does not apply.
//
// When they are not, FromRecords returns an Error issue and a layout with
// the default choices of NewLayout and every unit unplaced (our design).
//
// Otherwise it returns the layout and no issue, rebuilt as follows:
//   - setup from the number of phases; with two phases from the L2 angle
//     (180° Dual180, 240° Dual240, otherwise Dual120);
//   - AC inputs = highest AC input index of any unit + 1; a second input
//     does not add a column (our design);
//   - the L2 option from the floating bits of L2 units;
//   - group flags from byte 7 bit 7 of the units on each AC input; an AC
//     input with no unit takes bit 5 of the dual-input units whose second
//     input it holds, and is not grouped when it has neither (our design);
//   - each unit in the cell of its phase and AC input; a dual-input unit's
//     second input in the next AC column when the layout has it, else
//     unused (our design: bit 5 alone cannot tell "unused" from "used but
//     not grouped");
//   - in a cell: dual-input units, second inputs, single-input units; in
//     each class the leader first, then ascending short address (our
//     design).
//
// The layout's Records(false) can differ from what the units hold (for
// example in byte 7 bit 5); Differing lists those units. That is not an
// error.
func FromRecords(units []*Unit) (*Layout, *Issue) {
	if cond, detail := inconsistency(units); cond != 0 {
		l := NewLayout()
		for _, u := range units {
			l.insert(Unconfigured, Entry{u, false})
			l.placeSecond(u)
		}
		return l, &Issue{
			Rule:     RuleInconsistent,
			Severity: Error,
			Units:    slices.Clone(units),
			Message:  "The configuration stored in the units does not form a valid system. Arrange the units and send a new configuration.",
			Detail:   fmt.Sprintf("condition %d: %s", cond, detail),
		}
	}
	l := NewLayout()
	if len(units) == 0 {
		return l, nil
	}
	phases := units[0].Record.Phases()
	switch phases {
	case 1:
		l.Setup = SinglePhase
	case 3:
		l.Setup = ThreePhase
	default:
		l.Setup = Dual120
		for _, u := range units {
			if u.Record.Phase() == 1 {
				switch u.Record.PhaseAngle() {
				case 180:
					l.Setup = Dual180
				case 240:
					l.Setup = Dual240
				}
				break
			}
		}
	}
	l.ACInputs = 1
	for _, u := range units {
		l.ACInputs = max(l.ACInputs, u.Record.ACInput()+1)
	}
	if phases > 1 {
		for _, u := range units {
			if r := u.Record; r.Phase() == 1 {
				switch {
				case r.Floating() && r.FloatingReturn():
					l.L2 = FloatingReturn
				case r.Floating():
					l.L2 = Floating
				}
				break
			}
		}
	}
	var hasUnits [MaxACInputs]bool
	for _, u := range units {
		a := u.Record.ACInput()
		hasUnits[a] = true
		l.Grouped[a] = u.Record.Grouped()
	}
	for a := range l.ACInputs {
		if hasUnits[a] {
			continue
		}
		l.Grouped[a] = false
		for _, u := range units {
			if u.DualInput() && u.Record.ACInput()+1 == a {
				l.Grouped[a] = l.Grouped[a] || u.Record.SecondInputGrouped()
			}
		}
	}

	type item struct {
		e      Entry
		cell   Cell
		leader bool
		addr   byte
	}
	var items []item
	for _, u := range units {
		r := u.Record
		c := Cell{r.Phase(), r.ACInput()}
		items = append(items, item{Entry{u, false}, c, r.Leader(), r.ShortAddress()})
		if u.DualInput() {
			sc := Unconfigured
			if c.AC+1 < l.ACInputs {
				sc = Cell{c.Phase, c.AC + 1}
			}
			items = append(items, item{Entry{u, true}, sc, r.Leader(), r.ShortAddress()})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		if c := cmp.Compare(a.e.class(), b.e.class()); c != 0 {
			return c
		}
		if a.leader != b.leader {
			if a.leader {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.addr, b.addr)
	})
	for _, it := range items {
		lst := l.list(it.cell)
		*lst = append(*lst, it.e)
	}
	return l, nil
}

// inconsistency returns the number (1-8) of the first consistency
// condition of FromRecords that fails, with a description, or 0.
func inconsistency(units []*Unit) (int, string) {
	if len(units) == 0 {
		return 0, ""
	}
	mask := units[0].Record.PresenceMask()
	for _, u := range units {
		if m := u.Record.PresenceMask(); m != mask {
			return 1, fmt.Sprintf("unit %s has presence mask %08X, unit %s %08X", u.Label(), m, units[0].Label(), mask)
		}
	}
	if n := maskCount(mask); n != len(units) {
		return 2, fmt.Sprintf("presence mask %08X has %d addresses for %d units", mask, n, len(units))
	}
	for _, u := range units {
		if a := u.Record.ShortAddress(); a > 31 || mask&(1<<a) == 0 {
			return 3, fmt.Sprintf("unit %s has short address %d, which is not in the presence mask", u.Label(), a)
		}
	}
	var perPhase [4]int
	for _, u := range units {
		perPhase[u.Record.Phase()]++
	}
	used := 0
	for used < len(perPhase) && perPhase[used] > 0 {
		used++
	}
	for p := used; p < len(perPhase); p++ {
		if perPhase[p] > 0 {
			return 4, fmt.Sprintf("phase L%d is used but L%d is not", p+1, used+1)
		}
	}
	if used > maxPhases {
		return 4, fmt.Sprintf("phase index %d is used", used-1)
	}
	for _, u := range units {
		if n := u.Record.Phases(); n != used {
			return 4, fmt.Sprintf("unit %s says %d phases, %d are used", u.Label(), n, used)
		}
	}
	var group [MaxACInputs]*Unit
	var phase [maxPhases]*Unit
	const angleFloat = b6AngleMask | b6Return | b6Floating
	for _, u := range units {
		r := u.Record
		if g := group[r.ACInput()]; g == nil {
			group[r.ACInput()] = u
		} else if g.Record.Grouped() != r.Grouped() {
			return 5, fmt.Sprintf("units %s and %s disagree on the group flag of AC input %d", g.Label(), u.Label(), r.ACInput()+1)
		}
		if p := phase[r.Phase()]; p == nil {
			phase[r.Phase()] = u
		} else if p.Record[6]&angleFloat != r[6]&angleFloat {
			return 5, fmt.Sprintf("units %s and %s disagree on the angle or floating bits of L%d", p.Label(), u.Label(), r.Phase()+1)
		}
		if r.Phase() == 0 && r[6]&angleFloat != 0 {
			return 5, fmt.Sprintf("unit %s on L1 has angle or floating bits", u.Label())
		}
	}
	for _, u := range units {
		if r := u.Record; r.Leader() && int(r.Redundancy()) > perPhase[r.Phase()] {
			return 6, fmt.Sprintf("unit %s leads %d units, L%d has %d", u.Label(), r.Redundancy(), r.Phase()+1, perPhase[r.Phase()])
		}
	}
	for _, u := range units {
		if !u.Record.Marker() {
			return 7, fmt.Sprintf("unit %s has no marker bit", u.Label())
		}
	}
	for _, u := range units {
		if !u.AssistantFlagApplies() && u.Record.AssistantResetFlag() {
			return 8, fmt.Sprintf("unit %s has the assistant-reset flag set, which does not apply to its firmware", u.Label())
		}
	}
	return 0, ""
}

// Differing returns the placed units whose current record differs from the
// one the layout gives them in preview (Records(false)).
func (l *Layout) Differing() []*Unit {
	var out []*Unit
	for _, r := range l.Records(false) {
		if r.Record != r.Unit.Record {
			out = append(out, r.Unit)
		}
	}
	return out
}
