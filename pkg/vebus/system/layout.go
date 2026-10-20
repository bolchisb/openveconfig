package system

import (
	"errors"
	"fmt"
	"slices"
)

// Setup is the system's phase arrangement. The values are those the System
// Configurator stores (reverse-engineered (#10)).
type Setup uint8

const (
	SinglePhase Setup = iota
	Dual120           // two phases 120° apart
	Dual180           // split phase: two phases 180° apart
	Dual240           // two phases 240° apart
	ThreePhase
)

// Phases returns the number of phases of the setup, 0 for an unknown value.
func (s Setup) Phases() int {
	switch s {
	case SinglePhase:
		return 1
	case Dual120, Dual180, Dual240:
		return 2
	case ThreePhase:
		return 3
	}
	return 0
}

func (s Setup) String() string {
	switch s {
	case SinglePhase:
		return "single phase"
	case Dual120:
		return "dual phase 120°"
	case Dual180:
		return "dual phase 180°"
	case Dual240:
		return "dual phase 240°"
	case ThreePhase:
		return "three phase"
	}
	return fmt.Sprintf("Setup(%d)", uint8(s))
}

// angle returns the phase angle in degrees of phase index p: L1 0°; L2
// 180° for Dual180, 240° for Dual240, otherwise 120°; L3 240°.
func (s Setup) angle(p int) int {
	switch {
	case p == 0:
		return 0
	case p == 1 && s == Dual180:
		return 180
	case p == 1 && s != Dual240:
		return 120
	}
	return 240
}

// L2Option says how L2 (and in a three-phase system also L3) relates to
// L1. Stored values as in Setup.
type L2Option uint8

const (
	Fixed          L2Option = iota
	Floating                // record byte 6 bit 7
	FloatingReturn          // floating with return to the original phase: byte 6 bits 7 and 6
)

func (o L2Option) String() string {
	switch o {
	case Fixed:
		return "fixed"
	case Floating:
		return "floating"
	case FloatingReturn:
		return "floating with return"
	}
	return fmt.Sprintf("L2Option(%d)", uint8(o))
}

// MaxACInputs is the number of AC-input columns of the grid.
const MaxACInputs = 4

const maxPhases = 3

// Cell is a position in the grid: phase index 0-2 (L1-L3) and AC input
// index 0-3, or Unconfigured.
type Cell struct{ Phase, AC int }

// Unconfigured is the area for units that are not placed yet and for
// second AC inputs that are not used.
var Unconfigured = Cell{Phase: -1, AC: -1}

// Configured reports whether c is a grid cell.
func (c Cell) Configured() bool { return c.Phase >= 0 }

// Code returns the cell code of c: 10 × (phase + 1) + AC index, 0xFF for
// Unconfigured (reverse-engineered (#10); used by .vms files).
func (c Cell) Code() byte {
	if !c.Configured() {
		return 0xFF
	}
	return byte(10*(c.Phase+1) + c.AC)
}

// CellFromCode is the inverse of Cell.Code.
func CellFromCode(code byte) (Cell, error) {
	if code == 0xFF {
		return Unconfigured, nil
	}
	c := Cell{Phase: int(code)/10 - 1, AC: int(code) % 10}
	if c.Phase < 0 || c.Phase >= maxPhases || c.AC >= MaxACInputs {
		return Unconfigured, fmt.Errorf("system: invalid cell code %d", code)
	}
	return c, nil
}

func (c Cell) String() string {
	if !c.Configured() {
		return "unconfigured"
	}
	return fmt.Sprintf("L%d AC%d", c.Phase+1, c.AC+1)
}

// Entry is one occupant of a cell: a unit, or with Second the pseudo unit
// that stands for the second AC input of a dual-input unit. A pseudo unit
// has no record of its own; it only takes a position in a cell.
type Entry struct {
	Unit   *Unit
	Second bool
}

// class orders entries inside a cell: dual-input units first, then second
// inputs, then single-input units (reverse-engineered (#10)).
func (e Entry) class() int {
	switch {
	case e.Second:
		return 1
	case e.Unit.DualInput():
		return 0
	}
	return 2
}

// Layout is a system layout: the setup choices and the units placed in a
// grid of phases × AC inputs. Each cell is an ordered list; the unit at
// position 0 is the first one. Only cells with Phase < Setup.Phases() and
// AC < ACInputs are usable. Change the setup with Reshape and move units
// with Add, Place and Remove, which keep the ordering and second-input
// rules.
type Layout struct {
	Setup    Setup
	ACInputs int // 1-MaxACInputs
	// L2 applies to dual and three-phase setups.
	L2 L2Option
	// Grouped[i]: AC input i is switched as a group.
	Grouped [MaxACInputs]bool

	cells  [maxPhases][MaxACInputs][]Entry
	unconf []Entry
}

// NewLayout returns an empty layout with the default choices: single
// phase, one AC input, fixed, every AC input switched as a group (as in
// the factory record). The defaults are our design.
func NewLayout() *Layout {
	return &Layout{Setup: SinglePhase, ACInputs: 1, Grouped: [MaxACInputs]bool{true, true, true, true}}
}

// Usable reports whether c is a cell of the current setup.
func (l *Layout) Usable(c Cell) bool {
	return c.Configured() && c.Phase < l.Setup.Phases() && c.AC >= 0 && c.AC < l.ACInputs && c.AC < MaxACInputs
}

func (l *Layout) list(c Cell) *[]Entry {
	if !c.Configured() {
		return &l.unconf
	}
	return &l.cells[c.Phase][c.AC]
}

// Entries returns a copy of the occupants of cell c (or of the
// Unconfigured area) in order.
func (l *Layout) Entries(c Cell) []Entry {
	if c.Configured() && (c.Phase >= maxPhases || c.AC < 0 || c.AC >= MaxACInputs) {
		return nil
	}
	return slices.Clone(*l.list(c))
}

// each calls f for every cell, grid cells in sort order (L1 AC1, L1 AC2,
// …, L3 AC4) and then Unconfigured.
func (l *Layout) each(f func(c Cell, list []Entry)) {
	for p := range maxPhases {
		for a := range MaxACInputs {
			f(Cell{p, a}, l.cells[p][a])
		}
	}
	f(Unconfigured, l.unconf)
}

// Units returns the units in the layout: placed units in sort order
// (phase, AC input, position), then unplaced ones.
func (l *Layout) Units() []*Unit {
	var us []*Unit
	l.each(func(_ Cell, list []Entry) {
		for _, e := range list {
			if !e.Second {
				us = append(us, e.Unit)
			}
		}
	})
	return us
}

func (l *Layout) find(u *Unit, second bool) (Cell, int, bool) {
	found, pos, ok := Unconfigured, 0, false
	l.each(func(c Cell, list []Entry) {
		if ok {
			return
		}
		if i := slices.Index(list, Entry{u, second}); i >= 0 {
			found, pos, ok = c, i, true
		}
	})
	return found, pos, ok
}

// CellOf returns the cell of u and its position there. ok is false when u
// is not in the layout.
func (l *Layout) CellOf(u *Unit) (c Cell, pos int, ok bool) { return l.find(u, false) }

// SecondCell returns the cell of u's second AC input, Unconfigured when it
// is unused or u is not a dual-input unit in the layout.
func (l *Layout) SecondCell(u *Unit) Cell {
	c, _, _ := l.find(u, true)
	return c
}

func (l *Layout) remove(u *Unit, second bool) {
	c, i, ok := l.find(u, second)
	if ok {
		lst := l.list(c)
		*lst = slices.Delete(*lst, i, i+1)
	}
}

// insert adds e to cell c after the last entry of the same or an earlier
// class, so new units are appended within their class.
func (l *Layout) insert(c Cell, e Entry) {
	lst := l.list(c)
	i := len(*lst)
	if c.Configured() {
		for i > 0 && (*lst)[i-1].class() > e.class() {
			i--
		}
	}
	*lst = slices.Insert(*lst, i, e)
}

// placeSecond puts u's second input where the rule wants it: the next AC
// column of the owner's cell when that cell is usable, otherwise
// Unconfigured (reverse-engineered (#10)). It stays where it is when it is
// already there.
func (l *Layout) placeSecond(u *Unit) {
	if !u.DualInput() {
		return
	}
	owner, _, _ := l.find(u, false)
	want := Unconfigured
	if next := (Cell{owner.Phase, owner.AC + 1}); owner.Configured() && l.Usable(next) {
		want = next
	}
	if cur, _, ok := l.find(u, true); ok && cur == want {
		return
	}
	l.remove(u, true)
	l.insert(want, Entry{u, true})
}

var errNotInLayout = errors.New("system: unit is not in the layout")

// Add adds u to the Unconfigured area (with its second input when it is a
// dual-input unit).
func (l *Layout) Add(u *Unit) error {
	if _, _, ok := l.find(u, false); ok {
		return fmt.Errorf("system: unit %s is already in the layout", u.Label())
	}
	l.insert(Unconfigured, Entry{u, false})
	l.placeSecond(u)
	return nil
}

// Place moves u to cell c (a usable cell or Unconfigured), after the units
// of its class there. A dual-input unit's second input follows it. Placing
// a unit in the cell it is already in changes nothing.
func (l *Layout) Place(u *Unit, c Cell) error {
	cur, _, ok := l.find(u, false)
	if !ok {
		return errNotInLayout
	}
	if c.Configured() && !l.Usable(c) {
		return fmt.Errorf("system: cell %s is not usable with %s and %d AC inputs", c, l.Setup, l.ACInputs)
	}
	if cur == c {
		return nil
	}
	l.remove(u, false)
	l.insert(c, Entry{u, false})
	l.placeSecond(u)
	return nil
}

// Remove takes u (and its second input) out of the layout. It reports
// whether u was in it.
func (l *Layout) Remove(u *Unit) bool {
	if _, _, ok := l.find(u, false); !ok {
		return false
	}
	l.remove(u, false)
	l.remove(u, true)
	return true
}

// Reshape changes the setup and the number of AC inputs. Units in cells
// that are no longer usable move to Unconfigured, in grid order (our
// design), and second inputs are placed again by the rule of Place.
func (l *Layout) Reshape(s Setup, acInputs int) error {
	if s.Phases() == 0 {
		return fmt.Errorf("system: invalid setup %d", uint8(s))
	}
	if acInputs < 1 || acInputs > MaxACInputs {
		return fmt.Errorf("system: AC inputs must be 1-%d, not %d", MaxACInputs, acInputs)
	}
	l.Setup, l.ACInputs = s, acInputs
	var moved []*Unit
	for p := range maxPhases {
		for a := range MaxACInputs {
			c := Cell{p, a}
			if l.Usable(c) {
				continue
			}
			for _, e := range l.cells[p][a] {
				if !e.Second {
					moved = append(moved, e.Unit)
				}
			}
			l.cells[p][a] = slices.DeleteFunc(l.cells[p][a], func(e Entry) bool { return !e.Second })
		}
	}
	for _, u := range moved {
		l.insert(Unconfigured, Entry{u, false})
	}
	for _, u := range l.Units() {
		l.placeSecond(u)
	}
	return nil
}
