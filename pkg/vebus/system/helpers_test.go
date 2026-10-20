package system

import (
	"encoding/hex"
	"strings"
	"testing"
)

// fw is a firmware number with revision 456: the assistant-reset flag
// applies and the old-firmware rules (E8, E9, W3) do not.
const fw = 2623456

func hexRecord(t testing.TB, s string) Record {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil || len(b) != 9 {
		t.Fatalf("bad record %q", s)
	}
	return Record(b)
}

// multi returns a single-input Multi with letter B.
func multi(id uint32) *Unit {
	return &Unit{LongID: id, VersionWord: fw, Letter: 1, ResponseChar: 0x20, ConfigProtocol: 1, IdentifyFlag: 1}
}

func dual(id uint32) *Unit {
	u := multi(id)
	u.ResponseChar = ResponseDualInput
	return u
}

func inverter(id uint32) *Unit {
	u := multi(id)
	u.ResponseChar = ResponseInverter
	return u
}

func multis(n int) []*Unit {
	us := make([]*Unit, n)
	for i := range us {
		us[i] = multi(uint32(0x100 + i))
	}
	return us
}

type placement struct {
	u *Unit
	c Cell
}

func cell(phase, ac int) Cell { return Cell{Phase: phase, AC: ac} }

// newLayout builds a layout and places the units in order.
func newLayout(t testing.TB, s Setup, acInputs int, opt L2Option, ps ...placement) *Layout {
	t.Helper()
	l := NewLayout()
	if err := l.Reshape(s, acInputs); err != nil {
		t.Fatal(err)
	}
	l.L2 = opt
	for _, p := range ps {
		if err := l.Add(p.u); err != nil {
			t.Fatal(err)
		}
		if err := l.Place(p.u, p.c); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// inPhase places units in one cell, in order.
func inCell(c Cell, us ...*Unit) []placement {
	ps := make([]placement, len(us))
	for i, u := range us {
		ps[i] = placement{u, c}
	}
	return ps
}

// recordOf returns the record built for u.
func recordOf(t testing.TB, recs []UnitRecord, u *Unit) Record {
	t.Helper()
	for _, r := range recs {
		if r.Unit == u {
			return r.Record
		}
	}
	t.Fatalf("no record for unit %s", u.Label())
	return Record{}
}

// apply stores the built records in the units, as a write would.
func apply(recs []UnitRecord) {
	for _, r := range recs {
		r.Unit.Record = r.Record
	}
}

func rules(issues []Issue) []Rule {
	rs := make([]Rule, len(issues))
	for i, is := range issues {
		rs[i] = is.Rule
	}
	return rs
}
