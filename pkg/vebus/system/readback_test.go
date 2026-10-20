package system

import (
	"slices"
	"strings"
	"testing"
)

// built returns the units of a worked example with the records it builds
// for sending stored in them.
func built(t *testing.T, name string) []*Unit {
	t.Helper()
	for _, ex := range workedExamples() {
		if strings.HasPrefix(ex.name, name+" ") {
			l, us := ex.build(t)
			apply(l.Records(true))
			return us
		}
	}
	t.Fatalf("no worked example %s", name)
	return nil
}

// fixMask sets every unit's presence mask to the addresses the units have.
func fixMask(us []*Unit) {
	var m uint32
	for _, u := range us {
		m |= 1 << u.Record.ShortAddress()
	}
	for _, u := range us {
		u.Record.SetPresenceMask(m)
	}
}

func TestConsistency(t *testing.T) {
	for _, tc := range []struct {
		name    string
		example string
		break_  func(us []*Unit)
		cond    int
	}{
		{"masks differ", "E7", func(us []*Unit) { us[3].Record.SetPresenceMask(0x7F) }, 1},
		{"mask has an extra address", "E7", func(us []*Unit) {
			for _, u := range us {
				u.Record.SetPresenceMask(0x7F)
			}
		}, 2},
		{"address not in mask", "E7", func(us []*Unit) { us[5].Record.SetShortAddress(9) }, 3},
		{"two factory units", "E2", func(us []*Unit) {
			for _, u := range us {
				u.Record = FactoryDefault(u.Record)
			}
		}, 2},
		{"phase gap", "E6", func(us []*Unit) {
			us[1].Record.SetPhase(2) // L1, L3, L3
			us[1].Record.SetShortAddress(4)
			fixMask(us)
		}, 4},
		{"phase index 3", "E6", func(us []*Unit) {
			for _, u := range us {
				u.Record.SetPhases(4)
			}
			us[2].Record.SetPhase(3)
		}, 4},
		{"phase count disagrees", "E6", func(us []*Unit) { us[2].Record.SetPhases(2) }, 4},
		{"too few phases used", "E4", func(us []*Unit) {
			us[1].Record.SetPhase(0)
			us[1].Record.SetPhaseAngle(0)
		}, 4},
		{"group flags differ", "E2", func(us []*Unit) { us[1].Record.SetGrouped(false) }, 5},
		{"floating bits differ in L2", "E7", func(us []*Unit) { us[3].Record.SetFloating(true) }, 5},
		{"angles differ in L3", "E7", func(us []*Unit) { us[5].Record.SetPhaseAngle(120) }, 5},
		{"L1 floating", "E2", func(us []*Unit) {
			for _, u := range us {
				u.Record.SetFloating(true)
			}
		}, 5},
		{"L1 angle", "E2", func(us []*Unit) {
			for _, u := range us {
				u.Record.SetPhaseAngle(120)
			}
		}, 5},
		{"leader count too large", "E2", func(us []*Unit) { us[0].Record.SetRedundancy(3) }, 6},
		{"marker missing", "E3", func(us []*Unit) { us[2].Record.SetMarker(false) }, 7},
		{"assistant flag where it does not apply", "E1b", func(us []*Unit) { us[0].Record.SetAssistantResetFlag(true) }, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			us := built(t, tc.example)
			tc.break_(us)
			l, is := FromRecords(us)
			if is == nil {
				t.Fatal("records accepted")
			}
			if want := "condition " + string(rune('0'+tc.cond)) + ":"; !strings.HasPrefix(is.Detail, want) {
				t.Errorf("detail %q, want %s", is.Detail, want)
			}
			if is.Rule != RuleInconsistent || is.Severity != Error || !strings.Contains(is.Message, "does not form a valid system") {
				t.Errorf("issue %+v", is)
			}
			// Every unit unplaced, default choices.
			if len(l.Records(false)) != 0 || l.Setup != SinglePhase || l.ACInputs != 1 || l.L2 != Fixed {
				t.Errorf("layout after inconsistent read: %+v", l)
			}
			if got := l.Units(); !slices.Equal(got, us) {
				t.Errorf("units %v, want %v", got, us)
			}
		})
	}
}

func TestFromRecordsEmpty(t *testing.T) {
	l, is := FromRecords(nil)
	if is != nil || len(l.Units()) != 0 || l.Setup != SinglePhase {
		t.Errorf("FromRecords(nil) = %+v, %v", l, is)
	}
}

func TestFromRecordsInconsistentDual(t *testing.T) {
	d := dual(1)
	d.Record.SetPresenceMask(3)
	l, is := FromRecords([]*Unit{d})
	if is == nil {
		t.Fatal("accepted")
	}
	if got, want := l.Entries(Unconfigured), entries(d, d, true); !slices.Equal(got, want) {
		t.Errorf("unconfigured %v, want %v", got, want)
	}
}

// TestFromRecordsSetup: the setup follows the number of phases and the L2
// angle.
func TestFromRecordsSetup(t *testing.T) {
	for _, s := range []Setup{SinglePhase, Dual120, Dual180, Dual240, ThreePhase} {
		for _, opt := range []L2Option{Fixed, Floating, FloatingReturn} {
			if s == SinglePhase && opt != Fixed {
				continue
			}
			var ps []placement
			for p := range s.Phases() {
				ps = append(ps, placement{multi(uint32(p)), cell(p, 0)})
			}
			l := newLayout(t, s, 1, opt, ps...)
			apply(l.Records(true))
			got, is := FromRecords(l.Units())
			if is != nil {
				t.Fatalf("%s %s: %s", s, opt, is.Detail)
			}
			if got.Setup != s || got.L2 != opt {
				t.Errorf("%s %s read back as %s %s", s, opt, got.Setup, got.L2)
			}
		}
	}
}

// TestFromRecordsColumns: AC inputs come from the highest AC input used;
// an AC input that holds only a second input takes its group flag from
// bit 5 of the dual-input unit; a second input never adds a column.
func TestFromRecordsColumns(t *testing.T) {
	d, m := dual(1), multi(2)
	l := newLayout(t, SinglePhase, 3, Fixed, placement{d, cell(0, 0)}, placement{m, cell(0, 2)})
	l.Grouped[1] = false
	apply(l.Records(true))
	got, is := FromRecords([]*Unit{m, d})
	if is != nil {
		t.Fatal(is.Detail)
	}
	assertSameLayout(t, got, l)

	// A single dual-input unit whose second input was on AC input 2: read
	// back with one AC input, so the second input is unused and bit 5
	// differs from what the unit holds.
	single := dual(3)
	l = newLayout(t, SinglePhase, 2, Fixed, placement{single, cell(0, 0)})
	apply(l.Records(true))
	got, is = FromRecords([]*Unit{single})
	if is != nil {
		t.Fatal(is.Detail)
	}
	if got.ACInputs != 1 || got.SecondCell(single) != Unconfigured {
		t.Errorf("AC inputs %d, second input in %s", got.ACInputs, got.SecondCell(single))
	}
	if d := got.Differing(); !slices.Equal(d, []*Unit{single}) {
		t.Errorf("Differing() = %v", d)
	}
}

// TestFromRecordsOrder: in a cell the leader comes first, then ascending
// short address, within the class order.
func TestFromRecordsOrder(t *testing.T) {
	us := multis(3)
	l := parallelOf(t, us...)
	apply(l.Records(true))
	// Make the unit with address 6 the leader instead of address 0.
	us[0].Record.SetRedundancy(NoLeader)
	us[2].Record.SetRedundancy(3)
	got, is := FromRecords([]*Unit{us[1], us[0], us[2]})
	if is != nil {
		t.Fatal(is.Detail)
	}
	if e, want := got.Entries(cell(0, 0)), entries(us[2], us[0], us[1]); !slices.Equal(e, want) {
		t.Errorf("cell %v, want %v", e, want)
	}
	if d := got.Differing(); len(d) != 3 {
		t.Errorf("Differing() = %v, want all three (new addresses)", d)
	}
}
