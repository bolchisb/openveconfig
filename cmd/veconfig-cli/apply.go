package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// applyOptions are the flags of apply.
type applyOptions struct {
	addrs           addrList
	all             bool
	force           bool
	continueOnError bool
}

// addrList is a repeatable --address flag that also takes a comma list.
type addrList []byte

func (a *addrList) String() string {
	s := make([]string, len(*a))
	for i, x := range *a {
		s[i] = strconv.Itoa(int(x))
	}
	return strings.Join(s, ",")
}

func (a *addrList) Set(v string) error {
	for _, f := range strings.Split(v, ",") {
		n, err := strconv.ParseUint(strings.TrimSpace(f), 0, 8)
		if err != nil || n > mk2.MaxAddress {
			return fmt.Errorf("invalid address %q: want 0-%d", f, mk2.MaxAddress)
		}
		for _, x := range *a {
			if x == byte(n) {
				return fmt.Errorf("address %d given twice", n)
			}
		}
		*a = append(*a, byte(n))
	}
	return nil
}

var applyCmd = &command{
	name:    "apply",
	args:    "<file.json>",
	summary: "write the settings of a dump file to devices (dry run without --yes)",
	help: `WARNING: with --yes this changes the configuration of live devices. Wrong
settings can damage equipment.

<file.json> is the output of "dump". Its "address" is not used: apply reads
each device given with --address (default 0), applies the raw setting values
of the file to that model and shows what would change. Only with --yes is
anything written, and then only the settings that differ from the device
(--all: every setting). Each write is read back.

Checks before anything is written, for every device: the file's scale and
offset of each setting must match the device's, every value must lie within
the device's limits, and the model must pass validation (cross-setting
rules, flags that must not be changed). --force skips the validation only.
Settings the device does not support, and values the file marks as not
applicable, are left out. RAM variables in the file are ignored.

A write that gets no answer is repeated once. "Access level required" is
reported and not repeated. The first failure ends the run for that device
and the remaining settings are skipped, unless --continue-on-error. Settings
that need a device reset to take effect are listed; apply does not reset.`,
	timeout: 15 * time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		o.registerLink(fs)
		fs.Var(&o.apply.addrs, "address", "device address, 0-31; repeat or comma-separate for several units (default 0)")
		fs.BoolVar(&o.yes, "yes", false, "write to the device(s); without it apply only shows what it would write")
		fs.BoolVar(&o.apply.all, "all", false, "write every setting, not only those that differ from the device")
		fs.BoolVar(&o.apply.force, "force", false, "write even if validation fails (device limits are still enforced)")
		fs.BoolVar(&o.apply.continueOnError, "continue-on-error", false, "go on with the next setting or device after a failed write")
	},
	run: runApply,
}

func runApply(ctx context.Context, e *env) error {
	if err := e.wantArgs(1); err != nil {
		return err
	}
	if err := e.opts.validate(); err != nil {
		return e.usageError("%v", err)
	}
	ao := &e.opts.apply
	addrs := []byte(ao.addrs)
	if len(addrs) == 0 {
		addrs = []byte{0}
	}
	f, err := os.Open(e.args[0])
	if err != nil {
		return err
	}
	want, err := vebus.LoadDump(f, nil)
	f.Close()
	if err != nil {
		return err
	}

	c, _, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	w := e.stdout

	// Read and check every device before writing to any.
	var devs []*vebus.Device
	ok := true
	for _, a := range addrs {
		d, _, err := vebus.ReadAll(ctx, c, a, nil, vebus.ReadOptions{Settings: settingsToRead(want), RAM: []uint16{}})
		if err != nil {
			return fmt.Errorf("device %d: read: %w", a, err)
		}
		fmt.Fprintf(w, "device %d (firmware %d):\n", a, d.Firmware)
		if !applyFile(w, d, want) {
			ok = false
		}
		rep, err := vebus.Write(ctx, nil, d, vebus.WriteOptions{All: ao.all, DryRun: true, Override: ao.force})
		printWriteReport(w, d, rep)
		var ve *vebus.ValidationError
		if errors.As(err, &ve) {
			for _, is := range ve.Issues {
				fmt.Fprintf(w, "  invalid: %s\n", is.Message)
			}
			ok = false
		} else if err != nil {
			return err
		}
		devs = append(devs, d)
	}
	if !ok {
		return errors.New("nothing written: fix the problems above (--force skips validation only)")
	}
	if !e.opts.yes {
		fmt.Fprintln(w, "dry run: nothing written; pass --yes to write")
		return nil
	}

	failed := false
	for _, d := range devs {
		fmt.Fprintf(w, "writing device %d:\n", d.Address)
		rep, err := vebus.Write(ctx, c, d, vebus.WriteOptions{All: ao.all, Override: ao.force, ContinueOnError: ao.continueOnError})
		printWriteReport(w, d, rep)
		if ids := rep.ResetRequired(); len(ids) > 0 {
			fmt.Fprintf(w, "  device %d needs a reset for these changes to take effect:", d.Address)
			for _, id := range ids {
				fmt.Fprintf(w, " %s;", applyLabel(d, id))
			}
			fmt.Fprintln(w)
		}
		if err != nil {
			failed = true
			if !ao.continueOnError || ctx.Err() != nil {
				break
			}
		}
	}
	if failed {
		return errors.New("some settings were not written (see above)")
	}
	return nil
}

// settingsToRead returns the catalogue's settings and those in the file.
func settingsToRead(want *vebus.Device) []uint16 {
	seen := map[uint16]bool{}
	var ids []uint16
	add := func(n uint16) {
		if !seen[n] {
			seen[n] = true
			ids = append(ids, n)
		}
	}
	for _, e := range want.Catalogue().Entries() {
		if e.Kind == vebus.Setting {
			add(e.Num)
		}
	}
	for _, n := range want.SettingIDs() {
		add(n)
	}
	return ids
}

// applyFile sets the file's raw setting values in d and prints what it left
// out. It reports false when a value cannot be applied.
func applyFile(w io.Writer, d, want *vebus.Device) bool {
	ok := true
	for _, n := range want.SettingIDs() {
		id := vebus.SettingID(n)
		if !d.Supported(id) {
			fmt.Fprintf(w, "  note: %s is not supported by the device; left out\n", applyLabel(want, id))
			continue
		}
		if want.NotApplicable(id) {
			continue
		}
		wi, _ := want.SettingInfo(n)
		di, _ := d.SettingInfo(n)
		if wi.Sc != di.Sc || wi.Offset != di.Offset {
			fmt.Fprintf(w, "  error: %s: the file's scale and offset (Sc %d, offset %d) differ from the device's (Sc %d, offset %d)\n",
				applyLabel(d, id), wi.Sc, wi.Offset, di.Sc, di.Offset)
			ok = false
			continue
		}
		raw, _ := want.Raw(id)
		if d.Catalogue().IsFlagWord(n) {
			// Only the flags the device supports (§7.3.13.3).
			cur, _ := d.Raw(id)
			raw = cur&^di.Max | raw&di.Max
		}
		if err := d.SetRaw(id, raw); err != nil {
			fmt.Fprintf(w, "  error: %v\n", err)
			ok = false
		}
	}
	return ok
}

// printWriteReport prints one line per setting of a write report.
func printWriteReport(w io.Writer, d *vebus.Device, rep *vebus.WriteReport) {
	if len(rep.Results) == 0 {
		fmt.Fprintln(w, "  no changes")
		return
	}
	for _, r := range rep.Results {
		old := "?"
		if r.OldKnown {
			old = applyValue(d, r.ID, r.Old)
		}
		line := fmt.Sprintf("  %-9s %s: %s -> %s", r.Status, applyLabel(d, r.ID), old, applyValue(d, r.ID, r.New))
		if r.ResetRequired {
			line += " [reset required]"
		}
		if r.Err != nil {
			line += ": " + r.Err.Error()
		}
		fmt.Fprintln(w, line)
		if r.OldKnown {
			printApplyFlags(w, d, r.ID.Num, r.Old, r.New)
		}
	}
}

// printApplyFlags lists the flags that differ between two values of flag word
// word.
func printApplyFlags(w io.Writer, d *vebus.Device, word, old, new uint16) {
	cat := d.Catalogue()
	for _, e := range cat.Entries() {
		if e.Kind != vebus.Flag {
			continue
		}
		fw, bit, ok := cat.FlagLocation(e.Num)
		if !ok || fw != word || (old^new)>>bit&1 == 0 {
			continue
		}
		fmt.Fprintf(w, "            %s: %s -> %s\n", applyLabel(d, e.ID()), applyOnOff(old>>bit&1), applyOnOff(new>>bit&1))
	}
}

func applyOnOff(b uint16) string {
	if b != 0 {
		return "on"
	}
	return "off"
}

// applyLabel names a variable with its ID.
func applyLabel(d *vebus.Device, id vebus.ID) string {
	if name := d.Name(id); name != id.String() {
		return fmt.Sprintf("%s (%s)", id, name)
	}
	return id.String()
}

// applyValue formats a raw setting value with its display value and unit.
func applyValue(d *vebus.Device, id vebus.ID, raw uint16) string {
	info, _ := d.SettingInfo(id.Num)
	if d.Catalogue().IsFlagWord(id.Num) {
		return fmt.Sprintf("0x%04X", raw)
	}
	if raw == vebus.NotApplicable && info.Max != vebus.NotApplicable {
		return "n/a"
	}
	unit := ""
	if e, ok := d.Catalogue().Entry(id); ok && e.Unit != "" {
		unit = " " + e.Unit
	}
	return fmt.Sprintf("%s%s (raw %d)", num(tidy(info.Value(raw))), unit, raw)
}
