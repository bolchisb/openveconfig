package system

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Severity is how serious an Issue is.
type Severity uint8

const (
	// Warning is shown to the user but does not block sending.
	Warning Severity = iota + 1
	// Error blocks sending. There is no override.
	Error
)

func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	case Error:
		return "error"
	}
	return fmt.Sprintf("Severity(%d)", uint8(s))
}

// Rule is the stable identifier of a check.
type Rule string

// Compatibility rules (Check, CheckBus). Sources: see the package doc.
const (
	RuleMixedKinds         Rule = "E1"  // inverters and Multis mixed
	RuleFirmware           Rule = "E2"  // different firmware numbers
	RuleFamily             Rule = "E3"  // different firmware families
	RuleInverterInputs     Rule = "E4"  // inverter-only system with more than one AC input
	RuleMaxUnits           Rule = "E5"  // more than MaxUnits units
	RuleMaxPerPhase        Rule = "E6"  // more than MaxUnitsPerPhase units in a phase
	RuleLetterPerPhase     Rule = "E7"  // different compatibility letters in a phase
	RuleMP2Inputs          Rule = "E8"  // MultiPlus-II/MultiGrid AC-input limits on old firmware
	RuleQP2Inputs          Rule = "E9"  // QuattroPlus-II AC-input limits on old firmware
	RuleGroupWithoutMulti  Rule = "E10" // grouped AC input without a Multi
	RuleEmptyPhase         Rule = "E11" // a phase of the setup has no unit
	RuleUnplaced           Rule = "E12" // a unit is in the Unconfigured area
	RuleConfigProtocol     Rule = "E13" // a unit's configuration protocol is too new
	RuleBusMismatch        Rule = "E14" // layout and bus have different units
	RuleSecondInputUnused  Rule = "W1"  // a dual-input unit's second input is unused
	RuleFloatingThreePhase Rule = "W2"  // floating three-phase system
	RuleOldFirmware        Rule = "W3"  // very old firmware: a dongle may be needed
	RuleAssistants         Rule = "W4"  // sending removes assistants
)

// Limits of rules E5 and E6, and the firmware limits of rules E8, E9 and W3
// (reverse-engineered (#10)).
const (
	MaxUnits         = 32
	MaxUnitsPerPhase = 9

	minRevisionMultiInput = 454 // E8, E9: below this, AC inputs are limited
	minRevisionNoDongle   = 121 // W3
	maxConfigProtocol     = 1   // E13
)

// Issue is one finding of a check.
type Issue struct {
	Rule     Rule
	Severity Severity
	// Units are the units concerned, in layout order; empty when the
	// finding is about the layout as a whole.
	Units []*Unit
	// Phase (1-3) or ACInput (1-4) name the phase or AC input the finding
	// is about; 0 when it is not about one.
	Phase   int
	ACInput int
	// Message is a complete sentence for the user, in English.
	Message string
	// Detail says which condition failed, for logs (consistency check
	// only).
	Detail string
}

func (i Issue) String() string { return fmt.Sprintf("%s %s: %s", i.Rule, i.Severity, i.Message) }

// HasErrors reports whether any issue blocks sending.
func HasErrors(issues []Issue) bool {
	return slices.ContainsFunc(issues, func(i Issue) bool { return i.Severity == Error })
}

func errorIssue(r Rule, units []*Unit, format string, a ...any) Issue {
	return Issue{Rule: r, Severity: Error, Units: units, Message: fmt.Sprintf(format, a...)}
}

func warning(r Rule, units []*Unit, format string, a ...any) Issue {
	return Issue{Rule: r, Severity: Warning, Units: units, Message: fmt.Sprintf(format, a...)}
}

// Check runs the compatibility rules E1-E13 and W1-W4 on the layout and
// returns the issues in rule order (errors E1-E13, then warnings). It is
// meant to run after every change. Rule E14 needs the bus: see CheckBus.
func (l *Layout) Check() []Issue {
	all := l.Units()
	var placed [maxPhases][]*Unit
	var unplaced []*Unit
	l.each(func(c Cell, list []Entry) {
		for _, e := range list {
			switch {
			case e.Second:
			case c.Configured():
				placed[c.Phase] = append(placed[c.Phase], e.Unit)
			default:
				unplaced = append(unplaced, e.Unit)
			}
		}
	})
	var out []Issue
	add := func(i Issue) { out = append(out, i) }

	// E1
	inverters := filter(all, (*Unit).Inverter)
	if len(inverters) > 0 && len(inverters) < len(all) {
		add(errorIssue(RuleMixedKinds, all, "Inverters and Multis cannot be combined in one VE.Bus system."))
	}
	// E2: the full 24-bit firmware number.
	if fw := distinct(all, (*Unit).Firmware); len(fw) > 1 {
		add(errorIssue(RuleFirmware, all, "All units must run the same firmware version. Found: %s.", join(fw, ", ")))
	}
	// E3: raw family digits; no exceptions.
	if fam := distinct(all, (*Unit).Family); len(fam) > 1 {
		add(errorIssue(RuleFamily, all, "All units must be the same model type (firmware family %s).", join(fam, " vs ")))
	}
	// E4
	if len(all) > 0 && len(inverters) == len(all) && l.ACInputs != 1 {
		add(errorIssue(RuleInverterInputs, all, "A system of inverters can have only one AC input."))
	}
	// E5
	if len(all) > MaxUnits {
		add(errorIssue(RuleMaxUnits, all, "A VE.Bus system can have at most %d units.", MaxUnits))
	}
	// E6
	for p, us := range placed {
		if len(us) > MaxUnitsPerPhase {
			i := errorIssue(RuleMaxPerPhase, us, "Phase L%d has %d units; at most %d are allowed per phase.", p+1, len(us), MaxUnitsPerPhase)
			i.Phase = p + 1
			add(i)
		}
	}
	// E7: raw letter indexes, so an unknown letter differs from a known one.
	for p, us := range placed {
		if len(distinct(us, func(u *Unit) byte { return u.Letter })) > 1 {
			i := errorIssue(RuleLetterPerPhase, us, "Units in phase L%d have different compatibility IDs (%s). Units in one phase need the same compatibility letter.",
				p+1, strings.Join(distinct(us, (*Unit).CompatibilityID), ", "))
			i.Phase = p + 1
			add(i)
		}
	}
	// E8, E9
	lowest, haveRev := lowestRevision(all)
	if haveRev {
		mp2 := filter(all, func(u *Unit) bool { return u.MP2Family() && !u.DualInput() })
		qp2 := filter(all, func(u *Unit) bool { return u.MP2Family() && u.DualInput() })
		out = append(out, l.inputLimits(mp2, qp2, lowest)...)
	}
	// E10: a second input counts as a Multi on its AC input (its owner is
	// a dual-input Multi).
	for a := range l.ACInputs {
		if !l.Grouped[a] {
			continue
		}
		multi := false
		for p := range l.Setup.Phases() {
			multi = multi || slices.ContainsFunc(l.cells[p][a], func(e Entry) bool { return !e.Unit.Inverter() })
		}
		if !multi {
			i := errorIssue(RuleGroupWithoutMulti, nil, "AC input %d is switched as a group but has no Multi on it.", a+1)
			i.ACInput = a + 1
			add(i)
		}
	}
	// E11
	for p := range l.Setup.Phases() {
		if len(placed[p]) == 0 {
			i := errorIssue(RuleEmptyPhase, nil, "Phase L%d has no unit.", p+1)
			i.Phase = p + 1
			add(i)
		}
	}
	// E12
	if len(unplaced) > 0 {
		add(errorIssue(RuleUnplaced, unplaced, "Place every unit on a phase and AC input before sending."))
	}
	// E13
	if newer := filter(all, func(u *Unit) bool { return u.ConfigProtocol > maxConfigProtocol }); len(newer) > 0 {
		add(errorIssue(RuleConfigProtocol, newer, "At least one unit needs a newer version of this tool."))
	}
	// W1: only for placed units; an unplaced one is already E12.
	for _, u := range all {
		if c, _, _ := l.CellOf(u); u.DualInput() && c.Configured() && !l.SecondCell(u).Configured() {
			add(warning(RuleSecondInputUnused, []*Unit{u}, "Do not connect AC input 2 of %s: it is not configured.", u.Label()))
		}
	}
	// W2
	if l.Setup == ThreePhase && l.L2 != Fixed {
		add(warning(RuleFloatingThreePhase, nil, "Floating three-phase systems may only power single-phase loads and may only be connected to three-phase sources."))
	}
	// W3
	if haveRev && lowest < minRevisionNoDongle {
		add(warning(RuleOldFirmware, nil, "This old firmware may need a VE.Bus dongle for systems with more units; this tool cannot check for it."))
	}
	// W4
	if us := AssistantsRemoved(all); len(us) > 0 {
		add(warning(RuleAssistants, us, "Sending this configuration removes all assistants from %s.", labels(us)))
	}
	return out
}

// inputLimits applies rules E8 and E9 when the lowest revision is below
// minRevisionMultiInput. mp2 are MultiPlus-II/MultiGrid units (response
// character ResponseMP2, single input), qp2 QuattroPlus-II units (that
// family with a second input; no response character reports this today,
// spec open question Q6).
func (l *Layout) inputLimits(mp2, qp2 []*Unit, lowest uint32) []Issue {
	if lowest >= minRevisionMultiInput {
		return nil
	}
	var out []Issue
	if len(mp2) > 0 && (l.ACInputs > 1 || !l.Grouped[0]) {
		out = append(out, errorIssue(RuleMP2Inputs, mp2, "With this firmware, MultiPlus-II/MultiGrid systems support one AC input only, switched as a group. Update the firmware or change the layout."))
	}
	if len(qp2) > 0 {
		unused := slices.ContainsFunc(l.unconf, func(e Entry) bool { return e.Second })
		if l.ACInputs > 2 || l.ACInputs == 2 && unused {
			out = append(out, errorIssue(RuleQP2Inputs, qp2, "With this firmware, QuattroPlus-II systems support at most two AC inputs and every second input must be used."))
		}
	}
	return out
}

// CheckBus is rule E14: the units of the layout must be exactly the units
// found on the bus (by long ID) in the same session. It returns nil when
// they are.
func (l *Layout) CheckBus(busLongIDs []uint32) *Issue {
	have := map[uint32]bool{}
	for _, u := range l.Units() {
		have[u.LongID] = true
	}
	bus := map[uint32]bool{}
	for _, id := range busLongIDs {
		bus[id] = true
	}
	if len(have) == len(bus) && len(have) == len(l.Units()) {
		same := true
		for id := range bus {
			same = same && have[id]
		}
		if same {
			return nil
		}
	}
	i := errorIssue(RuleBusMismatch, nil, "The layout does not match the units on the bus. Run Get configuration again.")
	return &i
}

// AssistantsRemoved returns the units on which sending would remove the
// assistants (rule W4, System Configurator form): the assistant-reset flag
// applies to them and their current record has it clear.
func AssistantsRemoved(units []*Unit) []*Unit {
	return filter(units, func(u *Unit) bool { return u.AssistantFlagApplies() && !u.Record.AssistantResetFlag() })
}

func lowestRevision(units []*Unit) (uint32, bool) {
	if len(units) == 0 {
		return 0, false
	}
	low := units[0].Revision()
	for _, u := range units[1:] {
		low = min(low, u.Revision())
	}
	return low, true
}

func filter(units []*Unit, f func(*Unit) bool) []*Unit {
	var out []*Unit
	for _, u := range units {
		if f(u) {
			out = append(out, u)
		}
	}
	return out
}

// distinct returns the distinct values of f over units, in order of first
// appearance.
func distinct[T comparable](units []*Unit, f func(*Unit) T) []T {
	var out []T
	for _, u := range units {
		if v := f(u); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func join(vs []uint32, sep string) string {
	s := make([]string, len(vs))
	for i, v := range vs {
		s[i] = strconv.FormatUint(uint64(v), 10)
	}
	return strings.Join(s, sep)
}

func labels(units []*Unit) string {
	s := make([]string, len(units))
	for i, u := range units {
		s[i] = u.Label()
	}
	return strings.Join(s, ", ")
}
