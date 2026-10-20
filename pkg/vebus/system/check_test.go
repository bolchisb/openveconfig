package system

import (
	"slices"
	"strings"
	"testing"
)

// parallelOf places units in L1 AC1 of a single-phase, one-input layout.
func parallelOf(t *testing.T, us ...*Unit) *Layout {
	return newLayout(t, SinglePhase, 1, Fixed, inCell(cell(0, 0), us...)...)
}

func withFW(u *Unit, fw uint32) *Unit {
	u.VersionWord = fw
	return u
}

func TestCheckRules(t *testing.T) {
	mp2 := func(id, fw uint32) *Unit {
		u := withFW(multi(id), fw)
		u.ResponseChar = ResponseMP2
		return u
	}
	for _, tc := range []struct {
		name   string
		layout func(t *testing.T) *Layout
		rule   Rule
		fires  bool
		msg    string // part of the message, when it fires
	}{
		{"E1 inverter and Multi", func(t *testing.T) *Layout { return parallelOf(t, multi(1), inverter(2)) },
			RuleMixedKinds, true, "Inverters and Multis cannot be combined"},
		{"E1 inverters only", func(t *testing.T) *Layout { return parallelOf(t, inverter(1), inverter(2)) },
			RuleMixedKinds, false, ""},
		{"E1 Multis and a dual-input unit", func(t *testing.T) *Layout { return parallelOf(t, multi(1), dual(2)) },
			RuleMixedKinds, false, ""},

		{"E2 different revisions", func(t *testing.T) *Layout { return parallelOf(t, multi(1), withFW(multi(2), 2623457)) },
			RuleFirmware, true, "Found: 2623456, 2623457."},
		{"E2 high byte differs only", func(t *testing.T) *Layout { return parallelOf(t, multi(1), withFW(multi(2), 0xC7000000|fw)) },
			RuleFirmware, false, ""},
		{"E3 different families", func(t *testing.T) *Layout { return parallelOf(t, multi(1), withFW(multi(2), 2624456)) },
			RuleFamily, true, "firmware family 2623 vs 2624"},
		{"E3 same family", func(t *testing.T) *Layout { return parallelOf(t, multi(1), withFW(multi(2), 2623457)) },
			RuleFamily, false, ""},
		// Raw digits: 2000251 is shown as 2700 but is not family 2700.
		{"E3 compares raw family", func(t *testing.T) *Layout {
			return parallelOf(t, withFW(multi(1), 2000251), withFW(multi(2), 2700251))
		}, RuleFamily, true, "2000 vs 2700"},

		{"E4 inverters on two AC inputs", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{inverter(1), cell(0, 0)}, placement{inverter(2), cell(0, 1)})
		}, RuleInverterInputs, true, "only one AC input"},
		{"E4 inverters on one AC input", func(t *testing.T) *Layout { return parallelOf(t, inverter(1), inverter(2)) },
			RuleInverterInputs, false, ""},
		{"E4 Multis on two AC inputs", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{multi(1), cell(0, 0)}, placement{multi(2), cell(0, 1)})
		}, RuleInverterInputs, false, ""},

		{"E5 33 units", func(t *testing.T) *Layout { return threePhaseOf(t, 33) }, RuleMaxUnits, true, "at most 32 units"},
		{"E5 32 units", func(t *testing.T) *Layout { return threePhaseOf(t, 32) }, RuleMaxUnits, false, ""},
		{"E5 counts unplaced units", func(t *testing.T) *Layout {
			l := threePhaseOf(t, 27)
			for _, u := range multis(6) {
				u.LongID += 0x1000
				if err := l.Add(u); err != nil {
					t.Fatal(err)
				}
			}
			return l
		}, RuleMaxUnits, true, ""},

		{"E6 ten in a phase", func(t *testing.T) *Layout { return parallelOf(t, multis(10)...) },
			RuleMaxPerPhase, true, "Phase L1 has 10 units; at most 9"},
		{"E6 nine in a phase", func(t *testing.T) *Layout { return parallelOf(t, multis(9)...) },
			RuleMaxPerPhase, false, ""},

		{"E7 letters differ in a phase", func(t *testing.T) *Layout {
			u := multi(2)
			u.Letter = 0
			return parallelOf(t, multi(1), u)
		}, RuleLetterPerPhase, true, "phase L1 have different compatibility IDs (2623_B, 2623_A)"},
		{"E7 unknown letter differs from a known one", func(t *testing.T) *Layout {
			u := multi(2)
			u.Letter = LetterUnknown
			return parallelOf(t, multi(1), u)
		}, RuleLetterPerPhase, true, ""},
		{"E7 letters differ between phases", func(t *testing.T) *Layout {
			u := multi(2)
			u.Letter = 0
			return newLayout(t, Dual120, 1, Fixed, placement{multi(1), cell(0, 0)}, placement{u, cell(1, 0)})
		}, RuleLetterPerPhase, false, ""},

		{"E8 MultiPlus-II old firmware, two AC inputs", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{mp2(1, 2623453), cell(0, 0)}, placement{mp2(2, 2623453), cell(0, 1)})
		}, RuleMP2Inputs, true, "one AC input only"},
		{"E8 MultiPlus-II old firmware, input not grouped", func(t *testing.T) *Layout {
			l := parallelOf(t, mp2(1, 2623453))
			l.Grouped[0] = false
			return l
		}, RuleMP2Inputs, true, ""},
		{"E8 MultiPlus-II old firmware, one grouped input", func(t *testing.T) *Layout { return parallelOf(t, mp2(1, 2623453)) },
			RuleMP2Inputs, false, ""},
		{"E8 MultiPlus-II firmware 454, two AC inputs", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{mp2(1, 2623454), cell(0, 0)}, placement{mp2(2, 2623454), cell(0, 1)})
		}, RuleMP2Inputs, false, ""},
		{"E8 other Multis, old firmware, two AC inputs", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{withFW(multi(1), 2623453), cell(0, 0)}, placement{withFW(multi(2), 2623453), cell(0, 1)})
		}, RuleMP2Inputs, false, ""},

		{"E10 grouped AC input without unit", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{multi(1), cell(0, 0)})
		}, RuleGroupWithoutMulti, true, "AC input 2 is switched as a group but has no Multi"},
		{"E10 grouped AC input with only inverters", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{multi(1), cell(0, 0)}, placement{inverter(2), cell(0, 1)})
		}, RuleGroupWithoutMulti, true, ""},
		{"E10 AC input not grouped", func(t *testing.T) *Layout {
			l := newLayout(t, SinglePhase, 2, Fixed, placement{multi(1), cell(0, 0)})
			l.Grouped[1] = false
			return l
		}, RuleGroupWithoutMulti, false, ""},
		{"E10 second input counts as a Multi", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 2, Fixed, placement{dual(1), cell(0, 0)})
		}, RuleGroupWithoutMulti, false, ""},

		{"E11 L2 empty", func(t *testing.T) *Layout { return newLayout(t, Dual180, 1, Fixed, placement{multi(1), cell(0, 0)}) },
			RuleEmptyPhase, true, "Phase L2 has no unit."},
		{"E11 every phase used", func(t *testing.T) *Layout {
			return newLayout(t, Dual180, 1, Fixed, placement{multi(1), cell(0, 0)}, placement{multi(2), cell(1, 0)})
		}, RuleEmptyPhase, false, ""},

		{"E12 unit unplaced", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 1, Fixed, placement{multi(1), cell(0, 0)}, placement{multi(2), Unconfigured})
		}, RuleUnplaced, true, "Place every unit"},
		{"E12 unused second input is not a unit", func(t *testing.T) *Layout { return parallelOf(t, dual(1)) },
			RuleUnplaced, false, ""},

		{"E13 config protocol 2", func(t *testing.T) *Layout {
			u := multi(2)
			u.ConfigProtocol = 2
			return parallelOf(t, multi(1), u)
		}, RuleConfigProtocol, true, "newer version of this tool"},
		{"E13 config protocol 0 and 1", func(t *testing.T) *Layout {
			u := multi(2)
			u.ConfigProtocol = 0
			return parallelOf(t, multi(1), u)
		}, RuleConfigProtocol, false, ""},

		{"W1 second input unused", func(t *testing.T) *Layout {
			d := dual(1)
			d.Name = "north"
			return parallelOf(t, d)
		}, RuleSecondInputUnused, true, "Do not connect AC input 2 of north"},
		{"W1 second input used", func(t *testing.T) *Layout { return newLayout(t, SinglePhase, 2, Fixed, placement{dual(1), cell(0, 0)}) },
			RuleSecondInputUnused, false, ""},
		{"W1 not for an unplaced unit", func(t *testing.T) *Layout {
			return newLayout(t, SinglePhase, 1, Fixed, placement{multi(1), cell(0, 0)}, placement{dual(2), Unconfigured})
		}, RuleSecondInputUnused, false, ""},

		{"W2 floating three phase", func(t *testing.T) *Layout {
			l := threePhaseOf(t, 3)
			l.L2 = Floating
			return l
		}, RuleFloatingThreePhase, true, "Floating three-phase systems"},
		{"W2 fixed three phase", func(t *testing.T) *Layout { return threePhaseOf(t, 3) }, RuleFloatingThreePhase, false, ""},
		{"W2 floating split phase", func(t *testing.T) *Layout {
			return newLayout(t, Dual180, 1, FloatingReturn, placement{multi(1), cell(0, 0)}, placement{multi(2), cell(1, 0)})
		}, RuleFloatingThreePhase, false, ""},

		{"W3 revision 120", func(t *testing.T) *Layout { return parallelOf(t, withFW(multi(1), 2623120)) },
			RuleOldFirmware, true, "VE.Bus dongle"},
		{"W3 revision 121", func(t *testing.T) *Layout { return parallelOf(t, withFW(multi(1), 2623121)) },
			RuleOldFirmware, false, ""},

		{"W4 assistants installed", func(t *testing.T) *Layout {
			u := multi(1)
			u.Name = "north"
			return parallelOf(t, u)
		}, RuleAssistants, true, "removes all assistants from north."},
		{"W4 flag already set", func(t *testing.T) *Layout {
			u := multi(1)
			u.Record.SetAssistantResetFlag(true)
			return parallelOf(t, u)
		}, RuleAssistants, false, ""},
		{"W4 flag does not apply", func(t *testing.T) *Layout { return parallelOf(t, withFW(multi(1), 2623204)) },
			RuleAssistants, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := tc.layout(t).Check()
			i := slices.IndexFunc(issues, func(is Issue) bool { return is.Rule == tc.rule })
			if fired := i >= 0; fired != tc.fires {
				t.Fatalf("rule %s fired = %v, want %v; issues %v", tc.rule, fired, tc.fires, issues)
			}
			if i < 0 {
				return
			}
			is := issues[i]
			wantSev := Error
			if strings.HasPrefix(string(tc.rule), "W") {
				wantSev = Warning
			}
			if is.Severity != wantSev {
				t.Errorf("severity %s, want %s", is.Severity, wantSev)
			}
			if !strings.Contains(is.Message, tc.msg) {
				t.Errorf("message %q does not contain %q", is.Message, tc.msg)
			}
		})
	}
}

// threePhaseOf places n Multis round-robin over L1-L3 of a three-phase,
// one-input layout.
func threePhaseOf(t *testing.T, n int) *Layout {
	var ps []placement
	for i, u := range multis(n) {
		ps = append(ps, placement{u, cell(i%3, 0)})
	}
	return newLayout(t, ThreePhase, 1, Fixed, ps...)
}

// TestCheckClean: every worked example except E9 (W1) passes without
// issues once the records it builds are stored (no W4).
func TestCheckClean(t *testing.T) {
	for _, ex := range workedExamples() {
		t.Run(ex.name, func(t *testing.T) {
			l, _ := ex.build(t)
			apply(l.Records(true))
			issues := l.Check()
			if strings.HasPrefix(ex.name, "E9 ") {
				if !slices.Equal(rules(issues), []Rule{RuleSecondInputUnused}) {
					t.Errorf("issues %v, want only W1", issues)
				}
				return
			}
			if strings.HasPrefix(ex.name, "E8 ") {
				// 12 units in one phase.
				if !slices.Equal(rules(issues), []Rule{RuleMaxPerPhase}) {
					t.Errorf("issues %v, want only E6", issues)
				}
				return
			}
			if len(issues) != 0 {
				t.Errorf("issues %v", issues)
			}
		})
	}
}

func TestCheckIssueFields(t *testing.T) {
	us := multis(10)
	issues := newLayout(t, Dual120, 2, Fixed, inCell(cell(1, 1), us...)...).Check()
	var e6, e10, e11 *Issue
	for i := range issues {
		switch issues[i].Rule {
		case RuleMaxPerPhase:
			e6 = &issues[i]
		case RuleGroupWithoutMulti:
			e10 = &issues[i]
		case RuleEmptyPhase:
			e11 = &issues[i]
		}
	}
	if e6 == nil || e6.Phase != 2 || !slices.Equal(e6.Units, us) {
		t.Errorf("E6 = %+v", e6)
	}
	if e10 == nil || e10.ACInput != 1 {
		t.Errorf("E10 = %+v", e10)
	}
	if e11 == nil || e11.Phase != 1 {
		t.Errorf("E11 = %+v", e11)
	}
	if !HasErrors(issues) {
		t.Error("HasErrors = false")
	}
	if HasErrors([]Issue{{Rule: RuleOldFirmware, Severity: Warning}}) {
		t.Error("HasErrors(warning) = true")
	}
	if got := e11.String(); got != "E11 error: Phase L1 has no unit." {
		t.Errorf("String() = %s", got)
	}
}

// TestE9 checks rule E9 directly: no response character makes a unit both
// MultiPlus-II family and dual-input (spec open question Q6), so Check
// cannot reach it.
func TestE9(t *testing.T) {
	for rc := range 256 {
		u := &Unit{ResponseChar: byte(rc)}
		if u.MP2Family() && u.DualInput() {
			t.Errorf("response char %02X is both MultiPlus-II family and dual-input", rc)
		}
	}
	qp2 := []*Unit{dual(1)}
	for _, tc := range []struct {
		name     string
		acInputs int
		place    Cell // of the dual unit
		lowest   uint32
		fires    bool
	}{
		{"three AC inputs", 3, cell(0, 0), 453, true},
		{"two AC inputs, second input unused", 2, cell(0, 1), 453, true},
		{"two AC inputs, second input used", 2, cell(0, 0), 453, false},
		{"one AC input, second input unused", 1, cell(0, 0), 453, false},
		{"three AC inputs, firmware 454", 3, cell(0, 0), 454, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLayout(t, SinglePhase, tc.acInputs, Fixed, placement{qp2[0], tc.place})
			issues := l.inputLimits(nil, qp2, tc.lowest)
			if fired := slices.ContainsFunc(issues, func(i Issue) bool { return i.Rule == RuleQP2Inputs }); fired != tc.fires {
				t.Errorf("E9 fired = %v, want %v", fired, tc.fires)
			}
		})
	}
}

func TestCheckBus(t *testing.T) {
	us := multis(3)
	l := parallelOf(t, us...)
	for _, tc := range []struct {
		name  string
		bus   []uint32
		fires bool
	}{
		{"same units in another order", []uint32{us[2].LongID, us[0].LongID, us[1].LongID}, false},
		{"one more on the bus", []uint32{us[0].LongID, us[1].LongID, us[2].LongID, 0x999}, true},
		{"one missing on the bus", []uint32{us[0].LongID, us[1].LongID}, true},
		{"another unit", []uint32{us[0].LongID, us[1].LongID, 0x999}, true},
		{"empty bus", nil, true},
	} {
		is := l.CheckBus(tc.bus)
		if (is != nil) != tc.fires {
			t.Errorf("%s: issue %v", tc.name, is)
			continue
		}
		if is != nil && (is.Rule != RuleBusMismatch || is.Severity != Error || !strings.Contains(is.Message, "Run Get configuration again")) {
			t.Errorf("%s: issue %+v", tc.name, is)
		}
	}
	if is := NewLayout().CheckBus(nil); is != nil {
		t.Errorf("empty layout and bus: %v", is)
	}
}
