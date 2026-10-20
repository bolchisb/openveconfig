package main

// The texts of the System Configurator: the names of the setup choices,
// the findings of the system check, the progress steps and what to do
// after a failure. The English texts restate those of pkg/vebus/system and
// pkg/vebus/system/bus, which are written for this table.

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// setups are the phase arrangements in the order the setup dialog offers
// them.
var setups = []system.Setup{system.SinglePhase, system.Dual120, system.Dual180, system.Dual240, system.ThreePhase}

func setupName(s system.Setup) string {
	switch s {
	case system.SinglePhase:
		return tr("Single phase")
	case system.Dual120:
		return tr("Two phases, 120°")
	case system.Dual180:
		return tr("Split phase, 180°")
	case system.Dual240:
		return tr("Two phases, 240°")
	case system.ThreePhase:
		return tr("Three phase")
	}
	return s.String()
}

var l2Options = []system.L2Option{system.Fixed, system.Floating, system.FloatingReturn}

func l2Name(o system.L2Option) string {
	switch o {
	case system.Fixed:
		return tr("Fixed")
	case system.Floating:
		return tr("Floating")
	case system.FloatingReturn:
		return tr("Floating, returns to the original phase")
	}
	return o.String()
}

// phaseName returns the name of phase index p, "L1" to "L3".
func phaseName(p int) string { return tr("L{{.Phase}}", args{"Phase": strconv.Itoa(p + 1)}) }

// cellName names a cell of the grid, or the unconfigured area.
func cellName(c system.Cell) string {
	if !c.Configured() {
		return tr("Unconfigured")
	}
	return tr("{{.Phase}} · AC input {{.Input}}", args{"Phase": phaseName(c.Phase), "Input": strconv.Itoa(c.AC + 1)})
}

// unitKindText names the kind of unit its response character gives.
func unitKindText(u *system.Unit) string {
	switch {
	case u.NoConfig():
		return tr("No system configuration")
	case u.Inverter():
		return tr("Inverter")
	case u.DualInput():
		return tr("Multi with two AC inputs")
	case u.MP2Family():
		return tr("Multi of the MultiPlus-II family")
	}
	return tr("Multi")
}

func unitLabels(us []*system.Unit) string {
	s := make([]string, len(us))
	for i, u := range us {
		s[i] = u.Label()
	}
	return strings.Join(s, ", ")
}

// distinctText returns the distinct values of f over us, comma-separated.
func distinctText(us []*system.Unit, f func(*system.Unit) string) string {
	var out []string
	for _, u := range us {
		if v := f(u); !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return strings.Join(out, ", ")
}

// issueText returns the message of a finding of the system check.
func issueText(is system.Issue) string {
	a := args{"Phase": strconv.Itoa(is.Phase), "Input": strconv.Itoa(is.ACInput), "Units": unitLabels(is.Units)}
	switch is.Rule {
	case system.RuleMixedKinds:
		return tr("Inverters and Multis cannot be combined in one VE.Bus system.")
	case system.RuleFirmware:
		a["Versions"] = distinctText(is.Units, func(u *system.Unit) string { return strconv.FormatUint(uint64(u.Firmware()), 10) })
		return tr("All units must run the same firmware version. Found: {{.Versions}}.", a)
	case system.RuleFamily:
		a["Families"] = distinctText(is.Units, func(u *system.Unit) string { return strconv.FormatUint(uint64(u.Family()), 10) })
		return tr("All units must be the same model type. Found the firmware families {{.Families}}.", a)
	case system.RuleInverterInputs:
		return tr("A system of inverters can have only one AC input.")
	case system.RuleMaxUnits:
		a["Max"] = formatNumber(system.MaxUnits, 0)
		return tr("A VE.Bus system can have at most {{.Max}} units.", a)
	case system.RuleMaxPerPhase:
		a["Count"], a["Max"] = formatNumber(float64(len(is.Units)), 0), formatNumber(system.MaxUnitsPerPhase, 0)
		return tr("Phase L{{.Phase}} has {{.Count}} units; at most {{.Max}} are allowed per phase.", a)
	case system.RuleLetterPerPhase:
		a["IDs"] = distinctText(is.Units, (*system.Unit).CompatibilityID)
		return tr("Units in phase L{{.Phase}} have different compatibility IDs ({{.IDs}}). Units in one phase need the same compatibility letter.", a)
	case system.RuleMP2Inputs:
		return tr("With this firmware, MultiPlus-II and MultiGrid systems support one AC input only, switched as a group. Update the firmware or change the layout.")
	case system.RuleQP2Inputs:
		return tr("With this firmware, QuattroPlus-II systems support at most two AC inputs, and every second input must be used.")
	case system.RuleGroupWithoutMulti:
		return tr("AC input {{.Input}} is switched as a group but has no Multi on it.", a)
	case system.RuleEmptyPhase:
		return tr("Phase L{{.Phase}} has no unit.", a)
	case system.RuleUnplaced:
		return tr("Place every unit on a phase and AC input before sending: {{.Units}}.", a)
	case system.RuleConfigProtocol:
		return tr("{{.Units}} needs a newer version of this program.", a)
	case system.RuleBusMismatch:
		return tr("The layout does not have exactly the units on the bus. Get the configuration again, or open a file made for these units.")
	case system.RuleSecondInputUnused:
		return tr("Do not connect AC input 2 of {{.Units}}: it is not configured.", a)
	case system.RuleFloatingThreePhase:
		return tr("A floating three-phase system may only power single-phase loads and may only be connected to three-phase sources.")
	case system.RuleOldFirmware:
		return tr("This old firmware may need a VE.Bus dongle for systems with more units; this program cannot check for it.")
	case system.RuleAssistants:
		return tr("Sending this configuration removes all assistants from {{.Units}}.", a)
	case system.RuleQuickFirmware:
		return tr("{{.Units}} has other firmware than the first unit. Only units of the same type and firmware version can be combined.", a)
	case system.RuleQuickLetter:
		a["Letter"] = distinctText(is.Units, (*system.Unit).LetterString)
		return tr("{{.Units}} has another compatibility letter ({{.Letter}}) than the first unit; units in one phase need the same letter.", a)
	case system.RuleQuickSplitModel:
		return tr("{{.Units}} is a 120/240 V split-phase model, which cannot be used in a system of several phases.", a)
	case system.RuleInconsistent:
		return tr("The configuration stored in the units does not form a valid system, so every unit is shown as unconfigured. Arrange the units and send a new configuration.")
	}
	return is.Message
}

// stepText describes a progress step of a system operation.
func stepText(e bus.Event) string {
	a := args{"Done": strconv.Itoa(e.Done), "Total": strconv.Itoa(e.Total), "Attempt": strconv.Itoa(e.Attempt)}
	if e.Unit != nil {
		a["Unit"] = e.Unit.Label()
	}
	switch e.Step {
	case bus.StepPrepare:
		return tr("Checking the interface…")
	case bus.StepIdentify:
		return tr("Looking for units ({{.Done}} found)…", a)
	case bus.StepRead, bus.StepSnapshot:
		return tr("Reading the configuration of {{.Unit}} ({{.Done}} of {{.Total}})…", a)
	case bus.StepWrite:
		return tr("Writing {{.Unit}} ({{.Done}} of {{.Total}}), attempt {{.Attempt}}…", a)
	case bus.StepRollback:
		return tr("Restoring the previous configuration of {{.Unit}} ({{.Done}} of {{.Total}})…", a)
	case bus.StepReset:
		return tr("Restarting the system…")
	case bus.StepWait:
		return tr("Waiting for the units to restart…")
	case bus.StepVerify:
		return tr("Checking the units after the restart…")
	}
	return e.String()
}

// statusText describes what happened to a unit in a write.
func statusText(s bus.Status) string {
	switch s {
	case bus.StatusPlanned:
		return tr("not written")
	case bus.StatusVerified:
		return tr("written and checked")
	case bus.StatusNotWritten:
		return tr("not written")
	case bus.StatusRestored:
		return tr("previous configuration restored")
	case bus.StatusRestoreFailed:
		return tr("could not be restored")
	}
	return s.String()
}

// failureText says what went wrong in a system operation, what state the
// units are in and what to do.
func failureText(err error) string {
	var e *bus.Error
	if !errors.As(err, &e) {
		return tr("Communication with the units failed: {{.Err}}. Nothing was written.", args{"Err": err})
	}
	a := args{"Units": unitLabels(e.Units), "Count": strconv.Itoa(e.Found)}
	switch e.Kind {
	case bus.KindLink:
		return tr("The interface does not answer in VE.Bus mode. Check the cable and that the interface is an MK2 or MK3, then connect again.")
	case bus.KindNoUnits:
		return tr("No unit answered. Check that the units are switched on and connected with straight RJ45 cables, that no GX device is connected to the VE.Bus, and that only a supported remote panel is connected.")
	case bus.KindNewerTool:
		return tr("{{.Units}} needs a newer version of this program. Nothing was written.", a)
	case bus.KindNoMute:
		return tr("A unit keeps answering the identification request. Switch the whole system off and on again, then try again. Nothing was written.")
	case bus.KindUnitCount:
		return tr("Exactly one unit must be switched on; found {{.Count}}. Switch every unit off, then switch on only the unit to reset. Nothing was written.", a)
	case bus.KindBlocked:
		var msgs []string
		for _, is := range e.Issues {
			msgs = append(msgs, issueText(is))
		}
		a["Issues"] = strings.Join(msgs, "\n")
		return tr("The system check found errors. Correct the layout and try again. Nothing was written.\n{{.Issues}}", a)
	case bus.KindAssistants:
		return tr("Sending this configuration removes all assistants from {{.Units}}. Confirm this separately to go on. Nothing was written.", a)
	case bus.KindSnapshot:
		return tr("{{.Units}} did not answer when its current configuration was read. Nothing was written. Get the configuration and try again.", a)
	case bus.KindRolledBack:
		if len(e.Units) == 0 {
			return tr("Sending was stopped. The previous configuration was restored on all units; nothing was applied. Get the configuration to check.")
		}
		return tr("Sending failed at {{.Units}}. The previous configuration was restored on all units; nothing was applied. Get the configuration to check.", a)
	case bus.KindRollbackFailed:
		return tr("The previous configuration could not be restored on every unit ({{.Units}}). Do not switch the system on for use. Switch every unit off and on again, get the configuration, and send a configuration again. If units do not answer, connect them one at a time.", a)
	case bus.KindNotBack:
		return tr("Not every unit came back with the new configuration after the restart. Wait a minute, then get the configuration. If the LEDs show an error code, look it up in the manual of the units.")
	}
	return err.Error()
}
