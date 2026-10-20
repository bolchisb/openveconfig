package bus

import (
	"fmt"
	"strings"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus/system"
)

// Kind says what went wrong in an operation, and so what the user should
// do (Error.Guidance).
type Kind int

const (
	// KindLink: the interface does not answer in VE.Bus mode
	// (PrepareSystemLink failed). Nothing was sent to the units.
	KindLink Kind = iota + 1
	// KindNoUnits: no unit answered the identification request.
	KindNoUnits
	// KindNewerTool: a unit speaks a newer configuration protocol (rule
	// E13). The scan stopped; nothing was written.
	KindNewerTool
	// KindNoMute: a unit kept answering the identification request (or
	// more than system.MaxUnits answered). The scan stopped.
	KindNoMute
	// KindUnitCount: a standalone reset found other than exactly one unit.
	// Nothing was written.
	KindUnitCount
	// KindBlocked: the system check or the bus check has an error
	// (Error.Issues). Nothing was sent.
	KindBlocked
	// KindAssistants: writing would remove the assistants of Error.Units
	// and WriteOptions.RemoveAssistants was not set. Nothing was written.
	KindAssistants
	// KindSnapshot: a unit's record could not be read just before writing.
	// Nothing was written.
	KindSnapshot
	// KindRolledBack: writing failed at Error.Units (or was cancelled; then
	// Error.Units is empty and Err is the context's error). The previous
	// record was written back and verified on every unit that had been
	// written. The system was not reset, so nothing was applied.
	KindRolledBack
	// KindRollbackFailed: as KindRolledBack, but at least one unit could
	// not be restored (WriteReport says which units hold what). The system
	// was not reset.
	KindRollbackFailed
	// KindNotBack: the system was reset, but not every unit came back with
	// the record that was written (Error.Units), or the check after the
	// reset could not finish (Err).
	KindNotBack
)

func (k Kind) String() string {
	switch k {
	case KindLink:
		return "link"
	case KindNoUnits:
		return "no units"
	case KindNewerTool:
		return "newer tool needed"
	case KindNoMute:
		return "unit does not mute"
	case KindUnitCount:
		return "unit count"
	case KindBlocked:
		return "blocked by system check"
	case KindAssistants:
		return "assistant removal not confirmed"
	case KindSnapshot:
		return "snapshot failed"
	case KindRolledBack:
		return "rolled back"
	case KindRollbackFailed:
		return "rollback failed"
	case KindNotBack:
		return "not back after reset"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error is a failure of a Session operation that the user has to act on.
type Error struct {
	Kind Kind
	// Units are the units concerned: the unit that failed, or the units
	// that do not answer, hold the wrong record or would lose assistants.
	Units []*system.Unit
	// Issues are the blocking issues (KindBlocked).
	Issues []system.Issue
	// Found is the number of units found (KindUnitCount).
	Found int
	// Err is the cause, if any: an mk2 error, a *MismatchError or the
	// context's error.
	Err error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("system: " + e.Kind.String())
	if len(e.Units) > 0 {
		b.WriteString(" (" + labels(e.Units) + ")")
	}
	if e.Kind == KindUnitCount {
		fmt.Fprintf(&b, ": found %d units", e.Found)
	}
	for _, is := range e.Issues {
		b.WriteString("; " + is.String())
	}
	if e.Err != nil {
		b.WriteString(": " + e.Err.Error())
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// Guidance returns what the user should do, as complete English sentences
// in our own words (the recovery table of the project's spec, #53).
func (e *Error) Guidance() string {
	switch e.Kind {
	case KindLink:
		return "The interface does not answer in VE.Bus mode. Check the cable and that the interface is an MK2 or MK3, then connect again."
	case KindNoUnits:
		return "No unit answered. Check that the units are switched on and connected with straight RJ45 cables, that no GX device is connected to the VE.Bus, and that only a supported remote panel is connected."
	case KindNewerTool:
		return "At least one unit needs a newer version of this tool. Nothing was written."
	case KindNoMute:
		return "A unit keeps answering the identification request. Switch the whole system off and on again, then try again. Nothing was written."
	case KindUnitCount:
		return fmt.Sprintf("Exactly one unit must be switched on; found %d. Switch every unit off, then switch on only the unit to reset. Nothing was written.", e.Found)
	case KindBlocked:
		return "The system check found errors. Correct the layout and try again. Nothing was written."
	case KindAssistants:
		return fmt.Sprintf("Sending this configuration removes all assistants from %s. Confirm this separately to go on. Nothing was written.", labels(e.Units))
	case KindSnapshot:
		return "A unit did not answer when its current configuration was read. Nothing was written. Run Get configuration and try again."
	case KindRolledBack:
		if len(e.Units) == 0 {
			return "Sending was stopped. The previous configuration was restored on all units; nothing was applied. Run Get configuration to check."
		}
		return fmt.Sprintf("Sending failed at unit %s. The previous configuration was restored on all units; nothing was applied. Run Get configuration to check.", labels(e.Units))
	case KindRollbackFailed:
		return "The previous configuration could not be restored on every unit. Do not switch the system on for use. Switch every unit off and on again, run Get configuration, and send a configuration again. If units do not answer, connect them one at a time."
	case KindNotBack:
		return "Not every unit came back with the new configuration after the reset. Wait a minute, then run Get configuration. If the LEDs show an error code, open the LED-code help."
	}
	return ""
}

// MismatchError is a record read back after a write that is not the record
// written.
type MismatchError struct {
	Wrote, Read system.Record
}

func (e *MismatchError) Error() string {
	return fmt.Sprintf("wrote %s but read back %s", e.Wrote.Hex(), e.Read.Hex())
}

func labels(units []*system.Unit) string {
	s := make([]string, len(units))
	for i, u := range units {
		s[i] = u.Label()
	}
	return strings.Join(s, ", ")
}
