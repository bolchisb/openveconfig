package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/transfer"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// maxProbes bounds assistants probe --count (our choice).
const maxProbes = 256

var assistantsCmd = &command{
	name:    "assistants",
	args:    "read [<backup.rvsc>] | write <backup.rvsc> | clear | probe",
	summary: "back up, restore or clear a unit's assistants (writes need --yes); probe the block read",
	help: `WARNING: write and clear change live devices. After writing, every unit of
the system restarts, so loads lose power for a moment.

  assistants read [<backup.rvsc>]  read the unit's assistant area and show its
                                   size, checksum, bytes used and free, the
                                   assistant IDs it stores and the assistants
                                   running. -o without a file saves the area as
                                   a raw image; with a .rvsc/.rvms backup, -o
                                   gets a copy of the backup whose unit with the
                                   same long ID has the area read.
  assistants write <backup.rvsc>   write the area of the file's unit (--unit)
                                   to the unit with the same long ID. Without
                                   --yes it is a dry run.
  assistants clear                 write the empty area: every byte 0xFF, the
                                   "bare" shape of a unit without assistants
                                   (community-reported). Without --yes it is a
                                   dry run.
  assistants probe                 read-only probe of the block read request,
                                   for the hardware check (see below).

read, write and clear find the units as "system show" does; --unit chooses
one by long ID (0x hex or decimal) when more than one answers. A write is
refused before anything is written unless the area's checksum is right, it
was read from the same unit with the same firmware, it has the size of the
unit's area, and the unit's system record says that the unit is configured
for assistants. The unit's current area is read first; after writing, the
area is read back, the units are restarted and the running assistants are
checked. On a failure the previous area is written back. Assistants are
neither enabled nor disabled: how that is done is not known yet (#77).

Writing assistants to real devices is off until the hardware checks pass
(#3), and how a real device sends its assistant area is not specified yet
(#77): on a serial port, read, write and clear stop before anything is sent.
With --simulator they work fully, on a synthetic unit with made-up
assistants at address 0.

probe sends the Winmon command 0x5F, which community reports name "VLL block
read", to --address with the 2-byte argument --arg, then --arg+1 and so on,
--count requests in all, and prints each reply as it is, in hex. Apart from
the usual link setup, nothing else is sent; the command is reported to be a
read. What the argument means and how the reply is laid out are not known
yet: this command is for the hardware check that finds out (#3, #77). Run it
with --verbose and keep the log. "unknown command" means that the device or
its firmware does not know the request; "no reply" that nothing answered in
time.`,
	timeout: 15 * time.Minute,
	device:  true,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.UintVar(&o.probeArg, "arg", 0, "probe: first argument, 0-65535")
		fs.UintVar(&o.probeCount, "count", 1, fmt.Sprintf("probe: number of requests, 1-%d, with consecutive arguments", maxProbes))
		fs.StringVar(&o.rvsc.unit, "unit", "", "read, clear: the unit by long ID; write: the file's unit, by serial number or long ID")
		fs.StringVar(&o.rvsc.out, "o", "", "read: output file (a raw image, or with a backup a copy of it; not the backup itself)")
		fs.BoolVar(&o.yes, "yes", false, "write, clear: write and restart the units; without it this is a dry run")
	},
	run: runAssistants,
}

func runAssistants(ctx context.Context, e *env) error {
	if len(e.args) == 0 {
		return e.usageError("want a subcommand: read, write, clear or probe")
	}
	sub, o := e.args[0], e.opts
	nargs := map[string][2]int{"read": {0, 1}, "write": {1, 1}, "clear": {0, 0}, "probe": {0, 0}}
	n, ok := nargs[sub]
	switch {
	case !ok:
		return e.usageError("unknown subcommand %q: want read, write, clear or probe", sub)
	case len(e.args)-1 < n[0] || len(e.args)-1 > n[1]:
		return e.usageError("assistants %s wants %d to %d argument(s), got %d", sub, n[0], n[1], len(e.args)-1)
	case o.yes && sub != "write" && sub != "clear":
		return e.usageError("assistants %s writes nothing to the device; --yes is not used", sub)
	case o.rvsc.out != "" && sub != "read":
		return e.usageError("-o is for assistants read only")
	case sub == "read" && len(e.args) == 2 && o.rvsc.out == "":
		return e.usageError("no output file: pass -o <out> for the copy of the backup")
	case sub == "read" && len(e.args) == 2 && filepath.Clean(o.rvsc.out) == filepath.Clean(e.args[1]):
		return e.usageError("-o must not be the backup itself")
	}
	if sub == "probe" {
		return runProbe(ctx, e)
	}
	return runTransfer(ctx, e, sub)
}

func runProbe(ctx context.Context, e *env) error {
	o := e.opts
	switch {
	case o.probeArg > 0xFFFF:
		return e.usageError("--arg must be 0-65535, not %d", o.probeArg)
	case o.probeCount < 1 || o.probeCount > maxProbes:
		return e.usageError("--count must be 1-%d, not %d", maxProbes, o.probeCount)
	case o.probeArg+o.probeCount-1 > 0xFFFF:
		return e.usageError("--arg + --count goes past 65535")
	}
	c, err := e.connectDevice(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	fmt.Fprintf(e.stdout, "Winmon 0x%02X to device %d\n", mk2.WCmdVLLBlockRead, o.address)
	for arg := o.probeArg; arg < o.probeArg+o.probeCount; arg++ {
		p, err := c.ProbeVLLBlockRead(ctx, uint16(arg))
		var uc *mk2.UnknownCommandError
		switch {
		case err == nil:
			fmt.Fprintf(e.stdout, "  arg %5d: reply 0x%02X, %d bytes: % X\n", arg, p[0], len(p), p)
		case errors.As(err, &uc):
			fmt.Fprintf(e.stdout, "  arg %5d: unknown command (reason 0x%02X)\n", arg, uc.Reason)
		case errors.Is(err, mk2.ErrTimeout):
			fmt.Fprintf(e.stdout, "  arg %5d: no reply\n", arg)
		default:
			return fmt.Errorf("arg %d: %w", arg, err)
		}
	}
	return nil
}

// runTransfer runs assistants read, write and clear.
func runTransfer(ctx context.Context, e *env, sub string) error {
	codec := e.blockCodec()
	if err := assistantsAvailable(codec, sub != "read"); err != nil {
		return err
	}
	var r *rvscFile
	var fu *rvsc.Unit
	if len(e.args) == 2 {
		var err error
		if r, err = loadRVSC(e.args[1]); err != nil {
			return err
		}
		if sub == "write" {
			if fu, err = r.unit(e.opts.rvsc.unit); err != nil {
				return err
			}
		}
	}
	c, _, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	units, err := e.findUnits(ctx, c)
	if err != nil {
		return err
	}
	var u foundUnit
	if fu != nil {
		u, err = unitByID(units, fu.LongID)
	} else {
		u, err = pickUnit(units, e.opts.rvsc.unit)
	}
	if err != nil {
		return err
	}
	w := e.stdout
	fmt.Fprintf(w, "unit %s at address %d\n", u.label, u.Address)

	var want *transfer.Area
	switch sub {
	case "read":
		a, err := transfer.Read(ctx, c, codec, u.Unit)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "area:    %s\n", describeArea(a.Image))
		fmt.Fprintf(w, "running: %s\n", runningText(a.Running))
		return e.saveArea(r, a)
	case "write":
		img, err := fu.AssistantImage()
		if err != nil {
			return fmt.Errorf("%s: the assistant area of unit %s: %w", r.name, vebus.UnitLabel(fu), err)
		}
		fmt.Fprintf(w, "file %s, unit %s: %s\n", r.name, vebus.UnitLabel(fu), describeArea(img))
		want = &transfer.Area{LongID: fu.LongID, Firmware: fu.Firmware(), Image: img}
	case "clear":
		a, err := transfer.Read(ctx, c, codec, u.Unit)
		if err != nil {
			return err
		}
		// The erased area: kylehart's "bare" shape (community-reported,
		// kylehart/mk2vsc FORMAT.md §5), whose checksum is its last 4
		// bytes of 0xFF (assistant.Checksum). Our unit's empty image has
		// 6 header bytes first (observed, #77); they are not written.
		want = &transfer.Area{LongID: u.LongID, Firmware: a.Firmware, Image: bytes.Repeat([]byte{0xFF}, len(a.Image))}
		fmt.Fprintf(w, "new area: %s\n", describeArea(want.Image))
	}
	rep, err := transfer.Write(ctx, c, codec, u.Unit, want, e.transferOptions())
	return finishAssistants(w, rep, err)
}

// blockCodec returns the assistant block codec of the connection: the
// simulator's with --simulator, otherwise the one for real devices.
func (e *env) blockCodec() mk2.BlockCodec {
	if e.opts.simulator {
		return simulator.BlockCodec{}
	}
	return mk2.DeviceBlockCodec{}
}

// assistantsAvailable returns why assistants cannot be read (write false)
// or written through codec yet, or nil. It is checked before anything is
// sent.
func assistantsAvailable(codec mk2.BlockCodec, write bool) error {
	if write && codec.Hardware() && !transfer.WriteOnHardware {
		return fmt.Errorf("%w. Nothing was sent", transfer.ErrHardwareGate)
	}
	if codec.BlockSize() <= 0 {
		return fmt.Errorf("reading assistants from real devices is not available yet: %w", mk2.ErrBlockTransferUnspecified)
	}
	return nil
}

// transferOptions are the options of the assistant writes. Tests shorten
// the waits.
var transferOptions transfer.Options

// transferOptions returns the options of a write: confirmed with --yes,
// with the progress on stderr.
func (e *env) transferOptions() transfer.Options {
	opts := transferOptions
	opts.Confirm = e.opts.yes
	opts.Progress = func(ev transfer.Event) { fmt.Fprintf(e.stderr, "  %s\n", ev) }
	return opts
}

// foundUnit is a unit found by findUnits.
type foundUnit struct {
	transfer.Unit
	label string
}

// findUnits finds the units on the bus as "system show" does.
func (e *env) findUnits(ctx context.Context, c *mk2.Client) ([]foundUnit, error) {
	opts := sessionOptions
	opts.NoKeepAlive = true
	s, err := bus.Open(ctx, c, opts)
	if err != nil {
		return nil, systemError(err, nothingDone)
	}
	defer s.Close()
	if _, err := s.Scan(ctx, func(ev bus.Event) { fmt.Fprintf(e.stderr, "  %s\n", ev) }); err != nil {
		return nil, systemError(err, nothingDone)
	}
	var units []foundUnit
	for _, u := range s.Units() {
		if a, ok := s.ActiveAddress(u.LongID); ok {
			units = append(units, foundUnit{transfer.Unit{Address: a, LongID: u.LongID}, fmt.Sprintf("%08X", u.LongID)})
		}
	}
	return units, nil
}

// pickUnit returns the unit sel (a long ID), or the only unit when sel is
// empty.
func pickUnit(units []foundUnit, sel string) (foundUnit, error) {
	if sel == "" {
		if len(units) == 1 {
			return units[0], nil
		}
		return foundUnit{}, fmt.Errorf("%d units answered; choose one with --unit: %s", len(units), unitLabels(units))
	}
	id, err := strconv.ParseUint(sel, 0, 32)
	if err != nil {
		return foundUnit{}, fmt.Errorf("--unit %q is not a long ID", sel)
	}
	return unitByID(units, uint32(id))
}

// unitByID returns the unit with the long ID id.
func unitByID(units []foundUnit, id uint32) (foundUnit, error) {
	for _, u := range units {
		if u.LongID == id {
			return u, nil
		}
	}
	return foundUnit{}, fmt.Errorf("no unit with long ID %08X answered; the units on the bus are %s. Nothing was written", id, unitLabels(units))
}

func unitLabels(units []foundUnit) string {
	l := make([]string, len(units))
	for i, u := range units {
		l[i] = u.label
	}
	return strings.Join(l, ", ")
}

// saveArea saves the area a read with -o: as a raw image, or, with the
// backup r, into a copy of r whose unit with a's long ID gets the area.
func (e *env) saveArea(r *rvscFile, a *transfer.Area) error {
	out := e.opts.rvsc.out
	if out == "" {
		return nil
	}
	data := a.Image
	if r != nil {
		fu, err := r.unit(fmt.Sprintf("%#x", a.LongID))
		switch {
		case err != nil:
			return fmt.Errorf("%w; nothing was saved", err)
		case fu.Firmware() != a.Firmware:
			return fmt.Errorf("unit %s of %s has firmware %d, the unit %d: its area would not be accepted back; nothing was saved",
				vebus.UnitLabel(fu), r.name, fu.Firmware(), a.Firmware)
		case !a.ChecksumOK:
			return errors.New("the area's checksum is wrong (an interrupted write leaves it so); nothing was saved")
		}
		fu.SetAssistantImage(a.Image)
		if data, err = r.f.MarshalBinary(); err != nil {
			return err
		}
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	if r != nil {
		fmt.Fprintf(e.stdout, "wrote %s: %s with the area read for unit %08X\n", out, r.name, a.LongID)
	} else {
		fmt.Fprintf(e.stdout, "wrote %s: the raw image, %d bytes\n", out, len(data))
	}
	return nil
}

// describeArea describes an assistant image in one line.
func describeArea(img []byte) string {
	s := fmt.Sprintf("%d bytes", len(img))
	if !assistant.ChecksumOK(img) {
		return s + ", checksum WRONG (an interrupted write leaves it so)"
	}
	l, err := assistant.ParseLayout(img)
	switch {
	case err != nil:
		return s + ", checksum ok; layout not known for this size"
	case len(l.IDs) == 0:
		return fmt.Sprintf("%s, checksum ok, no assistants: %d bytes used, %d free", s, l.Used, l.Free)
	}
	ids := make([]string, len(l.IDs))
	for i, id := range l.IDs {
		ids[i] = strconv.Itoa(int(id))
	}
	return fmt.Sprintf("%s, checksum ok, %d bytes used, %d free; assistant IDs stored: %s", s, l.Used, l.Free, strings.Join(ids, ", "))
}

// runningText lists the IDs of running assistants. ID 5 is ESS (public
// document: Victron, "ESS mode 2 and 3" §3.6.2).
func runningText(ids []uint16) string {
	if len(ids) == 0 {
		return "none"
	}
	l := make([]string, len(ids))
	for i, id := range ids {
		l[i] = strconv.Itoa(int(id))
		if id == 5 {
			l[i] += " (ESS)"
		}
	}
	return strings.Join(l, ", ")
}

// finishAssistants prints the report of an assistant write and returns its
// error with what the user should do.
func finishAssistants(w io.Writer, rep *transfer.Report, err error) error {
	if rep != nil {
		if rep.Snapshot != nil {
			fmt.Fprintf(w, "unit's area before: %s\n", describeArea(rep.Snapshot.Image))
		}
		switch {
		case rep.Unchanged:
			fmt.Fprintln(w, "the unit already holds this area; nothing to write")
		case rep.DryRun:
			if err == nil {
				fmt.Fprintf(w, "dry run: nothing written; with --yes %d blocks are written and read back, then every unit restarts\n", rep.Blocks)
			}
		default:
			fmt.Fprintf(w, "%d block write(s) sent\n", rep.Writes)
			if rep.RolledBack {
				fmt.Fprintln(w, "writing failed; the previous area was written back")
			}
			if rep.Reset {
				fmt.Fprintln(w, "the units were restarted")
			}
			if rep.RunningKnown {
				fmt.Fprintf(w, "running: %s\n", runningText(rep.Running))
			}
		}
	}
	if err != nil {
		var te *transfer.Error
		if errors.As(err, &te) {
			return fmt.Errorf("%w\n%s", err, te.Guidance())
		}
		return err
	}
	if !rep.DryRun && !rep.Unchanged {
		fmt.Fprintln(w, "the unit holds the new area, read back and verified")
	}
	return nil
}
