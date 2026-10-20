package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

var rvscApplyCmd = &command{
	name:    "rvsc-apply",
	args:    "<file>",
	summary: "write the settings of one .rvsc/.rvms unit to a device (dry run without --yes)",
	help: `WARNING: with --yes this changes the configuration of a live device. Wrong
settings can damage equipment.

Reads every setting the file has (its IDs 0 to the file's count) from the
device at --address, then checks that the file matches the device: the same
firmware, the same setting info (scale, offset, default, limits) for every
setting the device supports, and one file unit per device on the bus. The
serial number and long ID cannot be read from a device yet, so --unit chooses
the file unit. A mismatch is refused with an explanation. --single-unit
applies one unit of a multi-unit file to one device (the unit count is then
not required to match).

Then lists the differences between file and device and the writes planned.
Without --yes nothing is sent. With --yes the changed settings are written
and read back.

Only settings are applied. The file's assistant area (assistants, and other
parts whose encoding is not known, #7) is not written. Settings the file marks
"not applicable" are not written. Grid-code settings (81, 128-191) are refused
unless --allow-grid-code is given (see rvsc-set --help). Validation errors
refuse the write unless --force is given.`,
	timeout: 30 * time.Minute,
	device:  true,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.rvsc.unit, "unit", "", unitFlagHelp)
		fs.BoolVar(&o.yes, "yes", false, "write to the device; without it this is a dry run")
		fs.BoolVar(&o.rvsc.single, "single-unit", false, "apply one file unit to one device even if the unit counts differ")
		fs.BoolVar(&o.rvsc.force, "force", false, "write despite validation errors")
		fs.BoolVar(&o.rvsc.allowGridCode, "allow-grid-code", false, "allow changing grid-code settings (81, 128-191)")
	},
	run: runRVSCApply,
}

func runRVSCApply(ctx context.Context, e *env) error {
	if err := e.wantArgs(1); err != nil {
		return err
	}
	o := &e.opts.rvsc
	r, err := loadRVSC(e.args[0])
	if err != nil {
		return err
	}
	u, err := r.unit(o.unit)
	if err != nil {
		return err
	}
	file, err := r.device(u)
	if err != nil {
		return err
	}
	c, _, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	addrs, err := c.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover devices: %w", err)
	}
	ids := make([]uint16, u.NumSettings())
	for i := range ids {
		ids[i] = uint16(i)
	}
	fmt.Fprintf(e.stderr, "reading %d settings from device %d...\n", len(ids), e.opts.address)
	dev, _, err := vebus.ReadAll(ctx, c, byte(e.opts.address), nil, vebus.ReadOptions{Settings: ids, RAM: []uint16{}})
	if err != nil {
		return fmt.Errorf("read device %d: %w", e.opts.address, err)
	}

	w := e.stdout
	m := vebus.MatchRVSC(r.info, u, dev)
	m.CheckUnitCount(len(r.units), len(addrs), o.single)
	fmt.Fprintf(w, "file %s, unit %s; device at address %d\n\n", r.name, vebus.UnitLabel(u), e.opts.address)
	fmt.Fprint(w, m)
	if err := m.Err(); err != nil {
		return fmt.Errorf("refusing to apply the file: %w", err)
	}
	fmt.Fprintln(w)
	if err := printDiff(w, vebus.DiffDevices(file, dev), "FILE", "DEVICE", "in the file only (not supported by the device)"); err != nil {
		return err
	}

	// Copy the file's values into the device model.
	var grid []uint16
	skipped := 0
	for _, id := range dev.SettingIDs() {
		sid := vebus.SettingID(id)
		want, err := u.Setting(int(id))
		if err != nil {
			return err
		}
		if cur, _ := dev.Raw(sid); cur == want {
			continue
		}
		if file.NotApplicable(sid) {
			skipped++
			continue
		}
		if vebus.GridCodeSetting(id) && !o.allowGridCode {
			grid = append(grid, id)
			continue
		}
		if err := dev.SetRaw(sid, want); err != nil {
			return fmt.Errorf("refusing to apply the file: %w", err)
		}
	}
	if len(grid) > 0 {
		return fmt.Errorf("%v; pass --allow-grid-code only if you know the grid code you are setting", &vebus.GridCodeError{IDs: grid})
	}
	if skipped > 0 {
		fmt.Fprintf(w, "%d settings are \"not applicable\" in the file and are not written\n", skipped)
	}

	rep, werr := vebus.Write(ctx, c, dev, vebus.WriteOptions{DryRun: !e.opts.yes, Override: o.force})
	fmt.Fprintf(w, "\nwrites to device %d (firmware %d):\n", dev.Address, dev.Firmware)
	printWriteReport(w, dev, rep)
	var ve *vebus.ValidationError
	switch {
	case errors.As(werr, &ve):
		return fmt.Errorf("refusing to apply the file: %w (pass --force to write anyway)", werr)
	case werr != nil:
		return werr
	case len(rep.Results) == 0:
		fmt.Fprintln(w, "\nthe device already holds the file's settings; nothing to write")
	case !e.opts.yes:
		fmt.Fprintln(w, "\ndry run: nothing was written; pass --yes to write")
	default:
		fmt.Fprintln(w, "\nall writes verified")
	}
	if ids := rep.ResetRequired(); len(ids) > 0 {
		fmt.Fprintf(w, "a device reset is needed for these to take effect: %v\n", ids)
	}
	fmt.Fprintln(w, "the file's assistant area was not applied (its encoding is unknown, #7)")
	return nil
}
