package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/assistant/transfer"
	"github.com/bolchisb/openveconfig/pkg/mk2"
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

By default only settings are applied and the device keeps its assistants.
--assistants=write also writes the file's assistant area, as "assistants
write" does (see its help), after the settings: the unit that answers at
--address must have the file unit's long ID, and the checks of the area run
before anything is sent. With --yes every unit then restarts. Writing
assistants to real devices is off until the hardware checks pass (#3).
Other parts of the file whose encoding is not known (#7) are not written.
Settings the file marks
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
		fs.StringVar(&o.rvsc.assistants, "assistants", "keep",
			"keep: leave the device's assistants as they are; write: also write the file's assistant area (every unit restarts)")
	},
	run: runRVSCApply,
}

func runRVSCApply(ctx context.Context, e *env) error {
	if err := e.wantArgs(1); err != nil {
		return err
	}
	o := &e.opts.rvsc
	if o.assistants != "keep" && o.assistants != "write" {
		return e.usageError("--assistants must be keep or write, not %q", o.assistants)
	}
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
	var want *transfer.Area // the file's area, with --assistants=write
	if o.assistants == "write" {
		if err := assistantsAvailable(e.blockCodec(), true); err != nil {
			return err
		}
		img, err := u.AssistantImage()
		if err != nil {
			return fmt.Errorf("%s: the assistant area of unit %s: %w", r.name, vebus.UnitLabel(u), err)
		}
		want = &transfer.Area{LongID: u.LongID, Firmware: u.Firmware(), Image: img}
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
	var au transfer.Unit // the unit whose area is written
	if want != nil {
		if au, err = e.planAssistants(ctx, c, want); err != nil {
			return fmt.Errorf("refusing to apply the file: %w", err)
		}
		if au.LongID == 0 {
			want = nil // the device already holds the file's area
		}
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
	switch {
	case o.assistants == "keep":
		fmt.Fprintln(w, "assistants: kept as the device has them; the file's assistant area was not written (--assistants=write writes it)")
	case want == nil:
	case !e.opts.yes:
		fmt.Fprintln(w, "assistants: dry run; with --yes the file's assistant area is written after the settings, then every unit restarts")
	default:
		fmt.Fprintln(w, "\nassistants:")
		arep, err := transfer.Write(ctx, c, e.blockCodec(), au, want, e.transferOptions())
		if err := finishAssistants(w, arep, err); err != nil {
			return err
		}
	}
	if ids := rep.ResetRequired(); len(ids) > 0 && (want == nil || !e.opts.yes) {
		fmt.Fprintf(w, "a device reset is needed for these to take effect: %v\n", ids)
	}
	return nil
}

// planAssistants checks, before anything is written, that the file's area
// want can be written to the unit at --address: the unit with want's long ID
// must answer there, and the dry run of the write must pass. It returns the
// unit, or the zero Unit when the unit already holds the area.
func (e *env) planAssistants(ctx context.Context, c *mk2.Client, want *transfer.Area) (transfer.Unit, error) {
	units, err := e.findUnits(ctx, c)
	if err != nil {
		return transfer.Unit{}, err
	}
	u, err := unitByID(units, want.LongID)
	if err != nil {
		return transfer.Unit{}, err
	}
	if u.Address != byte(e.opts.address) {
		return transfer.Unit{}, fmt.Errorf("the file's unit %s answers at address %d, not at --address %d", u.label, u.Address, e.opts.address)
	}
	w := e.stdout
	fmt.Fprintf(w, "\nassistants: the file's area: %s\n", describeArea(want.Image))
	opts := e.transferOptions()
	opts.Confirm = false
	rep, err := transfer.Write(ctx, c, e.blockCodec(), u.Unit, want, opts)
	if err != nil {
		return transfer.Unit{}, finishAssistants(w, rep, err)
	}
	fmt.Fprintf(w, "assistants: the device's area: %s\n", describeArea(rep.Snapshot.Image))
	if rep.Unchanged {
		fmt.Fprintln(w, "assistants: the device already holds the file's area; it is not written")
		return transfer.Unit{}, nil
	}
	fmt.Fprintf(w, "assistants: the checks passed; %d blocks to write\n", rep.Blocks)
	return u.Unit, nil
}
