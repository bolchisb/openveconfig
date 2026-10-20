package system

import "math/bits"

// UnitRecord is a unit with the record a layout gives it.
type UnitRecord struct {
	Unit     *Unit
	Cell     Cell
	Position int // position in the cell, counting second inputs
	Record   Record
}

// Records builds the record of every placed unit, in sort order (phase,
// AC input, position in the cell); that is also the order in which units
// are written. Units in the Unconfigured area get no record (rule E12
// refuses sending while there are any) and are not counted in the
// presence mask. Second inputs only take positions.
//
// With send false the records are a preview: byte 7 bit 4 keeps the
// unit's current value where the assistant-reset flag applies. With send
// true it is set there. Where the flag does not apply it is always clear.
//
// The rules, reverse-engineered (#10):
//   - byte 7: phase index, number of phases - 1, marker set, bit 7 = the
//     group flag of the unit's AC input, bit 5 = 1 for a single-input unit
//     and for a dual-input unit the group flag of the AC input holding its
//     second input (0 when that is unused);
//   - byte 6: AC input index; on L2 and L3 the phase angle (Setup), bit 7
//     unless the L2 option is Fixed and bit 6 for FloatingReturn;
//   - short addresses and presence mask: allocate;
//   - bytes 0 and 1: the number of units in the unit's phase when the unit
//     is at position 0 of its cell and (it is not ExternalBFSystem or it is
//     on AC input 0); NoLeader otherwise. So without external BF a phase
//     with units on several AC inputs has one leader per AC input, and a
//     cell whose position 0 is a second input has none.
func (l *Layout) Records(send bool) []UnitRecord {
	var out []UnitRecord
	var phases []int
	l.each(func(c Cell, list []Entry) {
		if !c.Configured() {
			return
		}
		for pos, e := range list {
			if e.Second {
				continue
			}
			out = append(out, UnitRecord{Unit: e.Unit, Cell: c, Position: pos, Record: l.baseRecord(e.Unit, c, send)})
			phases = append(phases, c.Phase)
		}
	})
	addrs, mask, counts := allocate(phases)
	for i := range out {
		r := &out[i]
		r.Record.SetShortAddress(addrs[i])
		r.Record.SetPresenceMask(mask)
		r.Record.SetRedundancy(NoLeader)
		if r.Position == 0 && (!r.Unit.ExternalBFSystem() || r.Cell.AC == 0) {
			r.Record.SetRedundancy(byte(counts[r.Cell.Phase]))
		}
	}
	return out
}

// baseRecord returns bytes 6 and 7 of u's record in cell c.
func (l *Layout) baseRecord(u *Unit, c Cell, send bool) Record {
	var r Record
	r.SetPhase(c.Phase)
	r.SetPhases(l.Setup.Phases())
	r.SetMarker(true)
	r.SetGrouped(l.Grouped[c.AC])
	second := true
	if u.DualInput() {
		sc := l.SecondCell(u)
		second = sc.Configured() && l.Grouped[sc.AC]
	}
	r.SetSecondInputGrouped(second)
	if u.AssistantFlagApplies() {
		r.SetAssistantResetFlag(send || u.Record.AssistantResetFlag())
	}
	r.SetACInput(c.AC)
	if c.Phase > 0 {
		r.SetPhaseAngle(l.Setup.angle(c.Phase))
		r.SetFloating(l.L2 != Fixed)
		r.SetFloatingReturn(l.L2 == FloatingReturn)
	}
	return r
}

// allocate gives short addresses to units of the given phase indexes (in
// sort order) and returns them with the presence mask and the number of
// units per phase. Reverse-engineered (#10):
//
// Each phase counts up in steps of 3 from its index (L1 0, 3, …, 30;
// L2 1, …, 31; L3 2, …, 29). A phase that runs past 31 gets NoAddress for
// its further units and then borrows: the other phase with more free
// addresses (free = (31 - counter) / 3; the lower phase on a tie; a phase
// that ran out has none) continues its own count and each address it
// gives is inserted into the borrowing phase's list in sorted order,
// pushing a NoAddress off the end; then the third phase gives the same
// way if NoAddress entries remain. Units take their phase's list in order.
func allocate(phases []int) (addrs []byte, mask uint32, counts [maxPhases]int) {
	counter := [maxPhases]int{-3, -2, -1}
	var over [maxPhases]bool
	var lists [maxPhases][]byte
	for _, p := range phases {
		counts[p]++
		if !over[p] {
			counter[p] += 3
			over[p] = counter[p] > 31
		}
		if over[p] {
			lists[p] = append(lists[p], NoAddress)
		} else {
			lists[p] = append(lists[p], byte(counter[p]))
		}
	}
	free := func(p int) int {
		if over[p] {
			return 0
		}
		return (31 - counter[p]) / 3
	}
	donate := func(to, from int) {
		lst := lists[to]
		for len(lst) > 0 && lst[len(lst)-1] == NoAddress && !over[from] && counter[from]+3 <= 31 {
			counter[from] += 3
			a := byte(counter[from])
			i := 0
			for i < len(lst) && lst[i] < a {
				i++
			}
			copy(lst[i+1:], lst[i:len(lst)-1])
			lst[i] = a
		}
	}
	for p := range maxPhases {
		if !over[p] {
			continue
		}
		a, b := (p+1)%maxPhases, (p+2)%maxPhases
		if a > b {
			a, b = b, a
		}
		if free(b) > free(a) {
			a, b = b, a
		}
		donate(p, a)
		donate(p, b)
	}
	for _, lst := range lists {
		for _, a := range lst {
			if a != NoAddress {
				mask |= 1 << a
			}
		}
	}
	var next [maxPhases]int
	addrs = make([]byte, len(phases))
	for i, p := range phases {
		addrs[i] = lists[p][next[p]]
		next[p]++
	}
	return addrs, mask, counts
}

// maskCount returns the number of addresses in a presence mask.
func maskCount(m uint32) int { return bits.OnesCount32(m) }
