package system

import (
	"slices"
	"testing"
)

func TestCellCode(t *testing.T) {
	for p := range maxPhases {
		for a := range MaxACInputs {
			c := cell(p, a)
			got, err := CellFromCode(c.Code())
			if err != nil || got != c {
				t.Errorf("%s: code %d decodes to %v, %v", c, c.Code(), got, err)
			}
		}
	}
	if Unconfigured.Code() != 0xFF || cell(0, 0).Code() != 10 || cell(2, 3).Code() != 33 {
		t.Errorf("codes %d %d %d", Unconfigured.Code(), cell(0, 0).Code(), cell(2, 3).Code())
	}
	if c, err := CellFromCode(0xFF); err != nil || c != Unconfigured {
		t.Errorf("CellFromCode(0xFF) = %v, %v", c, err)
	}
	for _, bad := range []byte{0, 9, 14, 40, 0xFE} {
		if _, err := CellFromCode(bad); err == nil {
			t.Errorf("CellFromCode(%d) accepted", bad)
		}
	}
}

func entries(es ...any) []Entry {
	var out []Entry
	for i := 0; i < len(es); i++ {
		e := Entry{Unit: es[i].(*Unit)}
		if i+1 < len(es) {
			if s, ok := es[i+1].(bool); ok {
				e.Second = s
				i++
			}
		}
		out = append(out, e)
	}
	return out
}

// TestCellOrder: dual-input units, then second inputs, then single-input
// units; each appended within its class.
func TestCellOrder(t *testing.T) {
	m1, d1, m2, d2, d3 := multi(1), dual(2), multi(3), dual(4), dual(5)
	l := newLayout(t, SinglePhase, 2, Fixed,
		placement{m1, cell(0, 1)}, placement{d1, cell(0, 0)}, placement{m2, cell(0, 1)},
		placement{d2, cell(0, 1)}, placement{d3, cell(0, 0)})
	if got, want := l.Entries(cell(0, 0)), entries(d1, d3); !slices.Equal(got, want) {
		t.Errorf("AC1: %v, want %v", got, want)
	}
	// d2 is a dual unit (first), then the second inputs of d1 and d3, then
	// the Multis; d2's own second input has no third column: unused.
	if got, want := l.Entries(cell(0, 1)), entries(d2, d1, true, d3, true, m1, m2); !slices.Equal(got, want) {
		t.Errorf("AC2: %v, want %v", got, want)
	}
	if got, want := l.Entries(Unconfigured), entries(d2, true); !slices.Equal(got, want) {
		t.Errorf("unconfigured: %v, want %v", got, want)
	}
	if c, pos, ok := l.CellOf(m2); !ok || c != cell(0, 1) || pos != 4 {
		t.Errorf("CellOf(m2) = %v %d %v", c, pos, ok)
	}
	if got := l.Units(); !slices.Equal(got, []*Unit{d1, d3, d2, m1, m2}) {
		t.Errorf("Units() = %v", got)
	}
}

// TestSecondInputFollowsOwner: moving a dual-input unit moves its second
// input to the next column, or to Unconfigured when there is none.
func TestSecondInputFollowsOwner(t *testing.T) {
	d := dual(1)
	l := newLayout(t, Dual180, 3, Fixed)
	if err := l.Add(d); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ to, second Cell }{
		{Unconfigured, Unconfigured},
		{cell(0, 0), cell(0, 1)},
		{cell(1, 1), cell(1, 2)},
		{cell(1, 2), Unconfigured},
		{Unconfigured, Unconfigured},
	} {
		if err := l.Place(d, tc.to); err != nil {
			t.Fatal(err)
		}
		if got := l.SecondCell(d); got != tc.second {
			t.Errorf("owner in %s: second input in %s, want %s", tc.to, got, tc.second)
		}
	}
	if got := l.SecondCell(multi(9)); got != Unconfigured {
		t.Errorf("SecondCell of a unit not in the layout = %s", got)
	}
}

func TestPlaceErrors(t *testing.T) {
	m := multi(1)
	l := newLayout(t, Dual120, 2, Fixed, placement{m, cell(0, 0)})
	if err := l.Add(m); err == nil {
		t.Error("Add accepted a unit twice")
	}
	if err := l.Place(multi(2), cell(0, 0)); err == nil {
		t.Error("Place accepted a unit not in the layout")
	}
	for _, c := range []Cell{cell(2, 0), cell(0, 2), cell(0, -1), cell(3, 0)} {
		if err := l.Place(m, c); err == nil {
			t.Errorf("Place accepted unusable cell %v", c)
		}
	}
	if err := l.Place(m, cell(0, 0)); err != nil {
		t.Errorf("Place in the same cell: %v", err)
	}
	if err := l.Reshape(Setup(5), 1); err == nil {
		t.Error("Reshape accepted setup 5")
	}
	for _, n := range []int{0, 5} {
		if err := l.Reshape(SinglePhase, n); err == nil {
			t.Errorf("Reshape accepted %d AC inputs", n)
		}
	}
}

func TestRemove(t *testing.T) {
	d, m := dual(1), multi(2)
	l := newLayout(t, SinglePhase, 2, Fixed, placement{d, cell(0, 0)}, placement{m, cell(0, 1)})
	if !l.Remove(d) {
		t.Fatal("Remove(d) = false")
	}
	if l.Remove(d) {
		t.Error("second Remove(d) = true")
	}
	if got, want := l.Entries(cell(0, 1)), entries(m); !slices.Equal(got, want) {
		t.Errorf("AC2 after removing d: %v", got)
	}
	if len(l.Entries(cell(0, 0))) != 0 || len(l.Entries(Unconfigured)) != 0 {
		t.Error("removed unit left entries")
	}
}

// TestReshape: shrinking moves units of cells that are no longer usable to
// Unconfigured; second inputs follow the rule both ways.
func TestReshape(t *testing.T) {
	d, m1, m2, m3 := dual(1), multi(2), multi(3), multi(4)
	l := newLayout(t, ThreePhase, 3, Fixed,
		placement{d, cell(0, 0)}, placement{m1, cell(1, 0)}, placement{m2, cell(2, 0)}, placement{m3, cell(0, 2)})
	if l.SecondCell(d) != cell(0, 1) {
		t.Fatalf("second input in %s", l.SecondCell(d))
	}
	if err := l.Reshape(Dual120, 1); err != nil {
		t.Fatal(err)
	}
	// Grid order: L1 AC3 (m3) before L3 AC1 (m2).
	if got, want := l.Entries(Unconfigured), entries(m3, m2, d, true); !slices.Equal(got, want) {
		t.Errorf("unconfigured after shrinking: %v, want %v", got, want)
	}
	for _, c := range []Cell{cell(0, 1), cell(0, 2), cell(2, 0)} {
		if len(l.Entries(c)) != 0 {
			t.Errorf("cell %s not emptied: %v", c, l.Entries(c))
		}
	}
	if c, _, _ := l.CellOf(m1); c != cell(1, 0) {
		t.Errorf("m1 moved to %s", c)
	}
	if err := l.Reshape(Dual120, 2); err != nil {
		t.Fatal(err)
	}
	if l.SecondCell(d) != cell(0, 1) {
		t.Errorf("after growing, second input in %s", l.SecondCell(d))
	}
}

// TestUnplacedGetNoRecord: units in Unconfigured get no record and are not
// in the presence mask.
func TestUnplacedGetNoRecord(t *testing.T) {
	us := multis(3)
	l := newLayout(t, SinglePhase, 1, Fixed, placement{us[0], cell(0, 0)}, placement{us[1], Unconfigured}, placement{us[2], cell(0, 0)})
	recs := l.Records(true)
	if len(recs) != 2 || recs[0].Unit != us[0] || recs[1].Unit != us[2] {
		t.Fatalf("records %v", recs)
	}
	if m := recs[0].Record.PresenceMask(); m != 0x09 {
		t.Errorf("mask %08X", m)
	}
}
