package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
	"github.com/bolchisb/openveconfig/pkg/vms"
)

// systemOptions are the flags of system.
type systemOptions struct {
	json             bool
	removeAssistants bool
	noKeepAlive      bool
}

var systemCmd = &command{
	name:    "system",
	args:    "show | write <file.vms> | reset | standalone",
	summary: "find, configure or reset the units of a VE.Bus system (writes need --yes)",
	help: `WARNING: write, reset and standalone change live devices. Every unit of the
system switches off for a moment, so loads lose power.

Before you start, switch every unit on (for standalone: only the one unit),
connect only supported remote panels and disconnect GX devices from the
VE.Bus.

  system show               find the units and print each one with the
                            check of their records; --json for JSON
  system write <file.vms>   write the layout of a System Configurator file
                            to the units. Without --yes it is a dry run: it
                            prints the checks, which units would change and
                            whether assistants would be removed. With --yes
                            it writes and verifies every unit, restarts the
                            system and checks the units again.
  system reset              restart every unit (the system reset); needs --yes
  system standalone         make the one unit that is switched on a unit on
                            its own; refused unless exactly one unit answers;
                            needs --yes

A write is refused before anything is sent when the layout does not pass the
system check or does not have exactly the units on the bus. When a unit
cannot be written, the previous configuration is written back to every unit
that was written and the system is not restarted. A write that removes
assistants also needs --remove-assistants. Units do not store names; the
file's names are used in the output.`,
	timeout:   10 * time.Minute,
	systemBus: true,
	setFlags: func(fs *flag.FlagSet, o *options) {
		o.registerLink(fs)
		fs.Lookup("simulator").Usage = "use the built-in simulator (a VE.Bus system of two units in parallel) instead of a serial port"
		fs.BoolVar(&o.yes, "yes", false, "write: write and restart the units; reset, standalone: confirm")
		fs.BoolVar(&o.system.removeAssistants, "remove-assistants", false,
			"write, standalone: confirm that the units the dry run lists lose their assistants")
		fs.BoolVar(&o.system.json, "json", false, "show: print the units and the check as JSON")
		fs.BoolVar(&o.system.noKeepAlive, "no-keepalive", false, "do not send the keep-alive frame while connected")
	},
	run: runSystem,
}

// newSystemSimulator returns the simulator used by "system --simulator".
// Tests replace it.
var newSystemSimulator = func() *simulator.Simulator {
	return simulator.New(simulator.Config{HighSpeed: true, Devices: simulator.SystemParallel2()})
}

// sessionOptions are the options of the system session. Tests shorten the
// waits.
var sessionOptions bus.Options

// nothingDone is the state of the units after a failure before any write.
const nothingDone = "Nothing was written and the units were not reset."

func runSystem(ctx context.Context, e *env) error {
	if len(e.args) == 0 {
		return e.usageError("want a subcommand: show, write, reset or standalone")
	}
	sub, so := e.args[0], &e.opts.system
	nargs := 0
	switch sub {
	case "write":
		nargs = 1
	case "show", "reset", "standalone":
	default:
		return e.usageError("unknown subcommand %q: want show, write, reset or standalone", sub)
	}
	switch {
	case len(e.args)-1 != nargs:
		return e.usageError("system %s wants %d argument(s), got %d", sub, nargs, len(e.args)-1)
	case so.json && sub != "show":
		return e.usageError("--json is for system show only")
	case e.opts.yes && sub == "show":
		return e.usageError("system show writes nothing; --yes is not used")
	case so.removeAssistants && sub != "write" && sub != "standalone":
		return e.usageError("--remove-assistants is for system write and standalone only")
	case !e.opts.yes && (sub == "reset" || sub == "standalone"):
		return e.usageError("refusing system %s without --yes: it changes live devices. Nothing was sent", sub)
	}
	if err := e.opts.validate(); err != nil {
		return e.usageError("%v", err)
	}
	var file *vms.File
	if sub == "write" {
		data, err := os.ReadFile(e.args[1])
		if err != nil {
			return err
		}
		if file, err = vms.Parse(data); err != nil {
			return fmt.Errorf("%s: %w", e.args[1], err)
		}
	}

	c, _, err := e.connect(ctx)
	if err != nil {
		return systemError(err, nothingDone)
	}
	defer c.Close()
	opts := sessionOptions
	opts.NoKeepAlive = so.noKeepAlive
	s, err := bus.Open(ctx, c, opts)
	if err != nil {
		return systemError(err, nothingDone)
	}
	defer s.Close()
	progress := func(ev bus.Event) { fmt.Fprintf(e.stderr, "  %s\n", ev) }
	w := e.stdout
	confirm := bus.WriteOptions{Confirm: e.opts.yes, RemoveAssistants: so.removeAssistants}

	if sub == "standalone" {
		rep, err := s.ResetStandalone(ctx, confirm, progress)
		return finishWrite(w, rep, err, "The unit is now standalone.")
	}
	sr, err := s.Scan(ctx, progress)
	if err != nil {
		return systemError(err, nothingDone)
	}
	switch sub {
	case "show":
		if so.json {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(scanJSON(s, sr))
		}
		printScan(w, s, sr)
		return nil
	case "reset":
		rep, err := s.Reset(ctx, confirm, progress)
		return finishWrite(w, rep, err, "Every unit came back with its configuration.")
	}
	l, err := file.Layout(s.Units())
	if err != nil {
		return systemError(fmt.Errorf("%s: %w", e.args[1], err), nothingDone)
	}
	rep, err := s.Write(ctx, l, confirm, progress)
	return finishWrite(w, rep, err, "Every unit holds and uses the new configuration.")
}

// finishWrite prints the report of a writing operation and returns its
// error, with what state the units are in.
func finishWrite(w io.Writer, rep *bus.WriteReport, err error, done string) error {
	if rep != nil {
		printSystemReport(w, rep)
	}
	switch {
	case err != nil:
		return systemError(err, nothingDone)
	case rep.DryRun:
		fmt.Fprintln(w, "dry run: nothing written; pass --yes to write")
		if len(rep.AssistantsRemoved) > 0 {
			fmt.Fprintln(w, "the write removes assistants: pass --remove-assistants as well to confirm")
		}
	default:
		fmt.Fprintln(w, done)
	}
	return nil
}

// systemError adds to err what the user should do and what state the units
// are in: the guidance of a *bus.Error, otherwise state.
func systemError(err error, state string) error {
	var be *bus.Error
	if !errors.As(err, &be) {
		return fmt.Errorf("%w\n%s", err, state)
	}
	g := strings.ReplaceAll(be.Guidance(), "Get configuration", "'openveconfig-cli system show'")
	if be.Kind == bus.KindAssistants {
		g += " Pass --remove-assistants to confirm."
	}
	return fmt.Errorf("%w\n%s", err, g)
}

// unitKind names the kind of unit its response character gives.
func unitKind(u *system.Unit) string {
	switch {
	case u.NoConfig():
		return "unknown (no system configuration)"
	case u.Inverter():
		return "inverter"
	case u.DualInput():
		return "Multi, two AC inputs"
	case u.MP2Family():
		return "Multi (MultiPlus-II family)"
	}
	return "Multi"
}

func role(r system.Record) string {
	if r.Leader() {
		return fmt.Sprintf("leader of %d", r.Redundancy())
	}
	return "follower"
}

func printScan(w io.Writer, s *bus.Session, sr *bus.ScanReport) {
	fmt.Fprintf(w, "%d unit(s) found:\n", len(sr.Units))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ADDRESS\tLONG ID\tFIRMWARE\tCOMPAT ID\tKIND\tPHASE\tAC INPUT\tROLE\tRECORD")
	for _, u := range sr.Units {
		addr, _ := s.ActiveAddress(u.LongID)
		fmt.Fprintf(tw, "  %d\t%08X\t%d\t%s\t%s\tL%d\t%d\t%s\t%s\n", addr, u.LongID, u.Firmware(), u.CompatibilityID(),
			unitKind(u), u.Record.Phase()+1, u.Record.ACInput()+1, role(u.Record), u.Record.Hex())
	}
	tw.Flush()
	for _, u := range sr.Lost {
		fmt.Fprintf(w, "lost: %s was identified but did not answer\n", u.Label())
	}
	if sr.Inconsistent != nil {
		fmt.Fprintf(w, "records do not agree: %s (%s)\n", sr.Inconsistent.Message, sr.Inconsistent.Detail)
	} else {
		fmt.Fprintf(w, "records agree: %s\n", describeLayout(sr.Layout))
	}
	printIssues(w, "system check", sr.Issues)
	for _, u := range sr.Differing {
		fmt.Fprintf(w, "note: the record of %s differs from what the layout gives it\n", u.Label())
	}
}

// describeLayout returns the setup choices of l in one line.
func describeLayout(l *system.Layout) string {
	s := fmt.Sprintf("%s, %d AC input(s)", l.Setup, l.ACInputs)
	if l.Setup.Phases() > 1 {
		s += ", L2 " + l.L2.String()
	}
	var g []string
	for i := range l.ACInputs {
		if l.Grouped[i] {
			g = append(g, fmt.Sprint(i+1))
		}
	}
	if len(g) > 0 {
		s += ", switched as a group: AC input " + strings.Join(g, ", ")
	}
	return s
}

func printIssues(w io.Writer, what string, issues []system.Issue) {
	if len(issues) == 0 {
		fmt.Fprintf(w, "%s: no issues\n", what)
		return
	}
	fmt.Fprintf(w, "%s:\n", what)
	for _, is := range issues {
		fmt.Fprintf(w, "  %s\n", is)
	}
}

func printSystemReport(w io.Writer, rep *bus.WriteReport) {
	if len(rep.Units) > 0 {
		printIssues(w, "checks", rep.Issues)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  UNIT\tCELL\tCURRENT RECORD\tNEW RECORD\tCHANGE\tSTATUS")
		for _, r := range rep.Units {
			change := "changes"
			if r.Old == r.New {
				change = "unchanged"
			}
			status := r.Status.String()
			if r.Writes > 0 {
				status += fmt.Sprintf(" (%d write(s))", r.Writes)
			}
			if r.Status == bus.StatusRestoreFailed {
				if r.HoldsKnown {
					status += ", holds " + r.Holds.Hex()
				} else {
					status += ", holds unknown"
				}
			}
			if r.Err != nil {
				status += ": " + r.Err.Error()
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", r.Unit.Label(), r.Cell, r.Old.Hex(), r.New.Hex(), change, status)
		}
		tw.Flush()
	}
	for _, u := range rep.AssistantsRemoved {
		fmt.Fprintf(w, "assistants: writing removes the assistants of %s\n", u.Label())
	}
	if rep.DryRun {
		fmt.Fprintln(w, "steps:")
		for _, op := range rep.Ops {
			fmt.Fprintf(w, "  %s\n", op)
		}
		return
	}
	if rep.Reset {
		fmt.Fprintln(w, "the system was reset")
	}
	for _, u := range rep.NotBack {
		fmt.Fprintf(w, "not back: %s is missing or does not use the record written\n", u.Label())
	}
}

// The JSON of system show.
type (
	systemScan struct {
		Units         []systemUnit  `json:"units"`
		Lost          []string      `json:"lost,omitempty"`
		Consistent    bool          `json:"consistent"`
		Inconsistency *systemIssue  `json:"inconsistency,omitempty"`
		Layout        string        `json:"layout,omitempty"`
		Issues        []systemIssue `json:"issues"`
		Differing     []string      `json:"differing,omitempty"`
	}
	systemUnit struct {
		ShortAddress    byte   `json:"short_address"`
		LongID          string `json:"long_id"`
		Firmware        uint32 `json:"firmware"`
		CompatibilityID string `json:"compatibility_id"`
		Kind            string `json:"kind"`
		Phase           int    `json:"phase"`
		ACInput         int    `json:"ac_input"`
		Leader          bool   `json:"leader"`
		Record          string `json:"record"`
	}
	systemIssue struct {
		Rule     string   `json:"rule"`
		Severity string   `json:"severity"`
		Message  string   `json:"message"`
		Detail   string   `json:"detail,omitempty"`
		Units    []string `json:"units,omitempty"`
	}
)

func scanJSON(s *bus.Session, sr *bus.ScanReport) systemScan {
	out := systemScan{Units: []systemUnit{}, Consistent: sr.Inconsistent == nil, Issues: []systemIssue{}}
	for _, u := range sr.Units {
		addr, _ := s.ActiveAddress(u.LongID)
		out.Units = append(out.Units, systemUnit{
			ShortAddress: addr, LongID: fmt.Sprintf("%08X", u.LongID), Firmware: u.Firmware(),
			CompatibilityID: u.CompatibilityID(), Kind: unitKind(u), Phase: u.Record.Phase() + 1,
			ACInput: u.Record.ACInput() + 1, Leader: u.Record.Leader(), Record: u.Record.Hex(),
		})
	}
	out.Lost = labelList(sr.Lost)
	if sr.Inconsistent != nil {
		is := issueJSON(*sr.Inconsistent)
		out.Inconsistency = &is
	} else {
		out.Layout = describeLayout(sr.Layout)
	}
	for _, is := range sr.Issues {
		out.Issues = append(out.Issues, issueJSON(is))
	}
	out.Differing = labelList(sr.Differing)
	return out
}

func issueJSON(is system.Issue) systemIssue {
	return systemIssue{Rule: string(is.Rule), Severity: is.Severity.String(), Message: is.Message,
		Detail: is.Detail, Units: labelList(is.Units)}
}

func labelList(us []*system.Unit) []string {
	var s []string
	for _, u := range us {
		s = append(s, u.Label())
	}
	return s
}
