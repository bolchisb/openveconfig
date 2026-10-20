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
	"text/tabwriter"
	"time"

	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// rvscOptions are the flags of the rvsc-* commands.
type rvscOptions struct {
	unit          string // --unit
	out           string // rvsc-set -o
	raw, flag     bool   // rvsc-set
	force         bool   // rvsc-set
	allowGridCode bool   // rvsc-set, rvsc-apply
	single        bool   // rvsc-apply
}

const unitFlagHelp = "the unit to use, by serial number or long ID (decimal or 0x hex); required when the file has more than one"

// rvscFile is a parsed file with its setting info and units.
type rvscFile struct {
	name  string
	f     *rvsc.File
	info  *rvsc.SettingInfoTable
	units []*rvsc.Unit
}

func loadRVSC(name string) (*rvscFile, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	f, err := rvsc.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	info, err := f.SettingInfo()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	units, err := f.Units()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("%s: the file has no units", name)
	}
	return &rvscFile{name: name, f: f, info: info, units: units}, nil
}

// unit returns the unit chosen by sel (serial or long ID), or the only unit
// when sel is empty.
func (r *rvscFile) unit(sel string) (*rvsc.Unit, error) {
	if sel == "" {
		if len(r.units) == 1 {
			return r.units[0], nil
		}
		return nil, fmt.Errorf("%s has %d units; choose one with --unit: %s", r.name, len(r.units), r.unitList())
	}
	id, idErr := strconv.ParseUint(sel, 0, 32)
	var found []*rvsc.Unit
	for _, u := range r.units {
		if u.Serial == sel || idErr == nil && u.LongID == uint32(id) {
			found = append(found, u)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("%s has no unit %q; its units are %s", r.name, sel, r.unitList())
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("%s has %d units matching %q", r.name, len(found), sel)
}

func (r *rvscFile) unitList() string {
	l := make([]string, len(r.units))
	for i, u := range r.units {
		l[i] = vebus.UnitLabel(u)
	}
	return strings.Join(l, ", ")
}

func (r *rvscFile) device(u *rvsc.Unit) (*vebus.Device, error) {
	d, err := vebus.FromRVSC(r.info, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.name, err)
	}
	return d, nil
}

var rvscShowCmd = &command{
	name:    "rvsc-show",
	args:    "<file>",
	summary: "print the units and settings of a .rvsc/.rvms file",
	help: `Prints the file's units (serial number, long ID, firmware, form) and, for
each unit, every setting the file's setting info marks as supported, with
its name, display value and raw value, then the named flags. The format is
community-reported; see pkg/rvsc.`,
	timeout: time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.rvsc.unit, "unit", "", "show only this unit, by serial number or long ID")
	},
	run: runRVSCShow,
}

func runRVSCShow(ctx context.Context, e *env) error {
	if err := e.wantArgs(1); err != nil {
		return err
	}
	r, err := loadRVSC(e.args[0])
	if err != nil {
		return err
	}
	units := r.units
	if e.opts.rvsc.unit != "" {
		u, err := r.unit(e.opts.rvsc.unit)
		if err != nil {
			return err
		}
		units = []*rvsc.Unit{u}
	}
	w := e.stdout
	tool, err := r.f.ToolVersion()
	if err != nil {
		tool = "unknown (" + err.Error() + ")"
	}
	fmt.Fprintf(w, "file:     %s\n", r.name)
	fmt.Fprintf(w, "tool:     %s\n", tool)
	fmt.Fprintf(w, "info:     firmware %d, %d settings\n", r.info.FirmwareWord&0xFFFFFF, len(r.info.Settings))
	fmt.Fprintf(w, "units:    %d\n", len(r.units))
	for _, u := range units {
		d, err := r.device(u)
		if err != nil {
			return err
		}
		form := "device form"
		if u.UploadForm {
			form = "upload form"
		}
		fmt.Fprintf(w, "\nunit %s\n", vebus.UnitLabel(u))
		fmt.Fprintf(w, "  firmware %d, index %d, phase 0x%02X, %s, saved %s, %d settings, assistant area %d bytes\n",
			u.Firmware(), u.Index, u.Phase, form, u.SaveTime.Format(time.RFC3339), u.NumSettings(), len(u.AssistantArea()))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  ID\tNAME\tVALUE\tRAW")
		for _, id := range d.SettingIDs() {
			sid := vebus.SettingID(id)
			raw, _ := d.Raw(sid)
			name := d.Name(sid)
			if vebus.GridCodeSetting(id) {
				name += " [grid code]"
			}
			fmt.Fprintf(tw, "  %d\t%s\t%s\t%d\n", id, name, d.ValueText(sid), raw)
		}
		fmt.Fprintln(tw, "  FLAG\tNAME\tVALUE\t")
		for _, en := range d.Entries() {
			if en.Kind == vebus.Flag {
				fmt.Fprintf(tw, "  %d\t%s\t%s\t\n", en.Num, en.Name, d.ValueText(en.ID()))
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	return nil
}

var rvscDiffCmd = &command{
	name:    "rvsc-diff",
	args:    "<file> | <fileA> <fileB>",
	summary: "compare a .rvsc/.rvms file with a device or another file",
	help: `With one file, reads the device at --address (through --port or --simulator)
and checks whether the file matches it: firmware, setting info and the number
of units on the bus. The serial number and long ID cannot be read from a
device yet, so --unit chooses the file unit. Then lists the settings whose
values differ. Only the settings in the program's catalogue are read from the
device.

With two files, pairs their units by long ID and serial number (or takes the
only unit of each) and lists the settings and flags that differ.`,
	timeout: 10 * time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		o.register(fs)
		fs.StringVar(&o.rvsc.unit, "unit", "", unitFlagHelp)
	},
	run: runRVSCDiff,
}

func runRVSCDiff(ctx context.Context, e *env) error {
	switch len(e.args) {
	case 1:
		if err := e.opts.validate(); err != nil {
			return e.usageError("%v", err)
		}
		return e.diffDevice(ctx, e.args[0])
	case 2:
		return e.diffFiles(e.args[0], e.args[1])
	}
	return e.usageError("want 1 or 2 arguments (%s), got %d", e.cmd.args, len(e.args))
}

func (e *env) diffDevice(ctx context.Context, name string) error {
	r, err := loadRVSC(name)
	if err != nil {
		return err
	}
	u, err := r.unit(e.opts.rvsc.unit)
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
	dev, err := vebus.Load(ctx, c, byte(e.opts.address), nil)
	if err != nil {
		return err
	}
	m := vebus.MatchRVSC(r.info, u, dev)
	m.CheckUnitCount(len(r.units), len(addrs), false)
	w := e.stdout
	fmt.Fprintf(w, "file %s, unit %s; device at address %d\n\n", r.name, vebus.UnitLabel(u), e.opts.address)
	fmt.Fprint(w, m)
	if m.OK() {
		fmt.Fprint(w, "result: the file matches the device\n\n")
	} else {
		fmt.Fprint(w, "result: the file does NOT match the device; it must not be applied to it\n\n")
	}
	return printDiff(w, vebus.DiffDevices(file, dev), "FILE", "DEVICE", "file only (not read from the device)")
}

func (e *env) diffFiles(nameA, nameB string) error {
	a, err := loadRVSC(nameA)
	if err != nil {
		return err
	}
	b, err := loadRVSC(nameB)
	if err != nil {
		return err
	}
	w := e.stdout
	type pair struct{ ua, ub *rvsc.Unit }
	var pairs []pair
	if len(a.units) == 1 && len(b.units) == 1 {
		pairs = append(pairs, pair{a.units[0], b.units[0]})
	} else {
		matched := map[*rvsc.Unit]bool{}
		for _, ua := range a.units {
			p := pair{ua: ua}
			for _, ub := range b.units {
				if !matched[ub] && ub.LongID == ua.LongID && ub.Serial == ua.Serial {
					p.ub, matched[ub] = ub, true
					break
				}
			}
			pairs = append(pairs, p)
		}
		for _, ub := range b.units {
			if !matched[ub] {
				pairs = append(pairs, pair{ub: ub})
			}
		}
	}
	for i, p := range pairs {
		if i > 0 {
			fmt.Fprintln(w)
		}
		switch {
		case p.ub == nil:
			fmt.Fprintf(w, "unit %s: only in A (%s)\n", vebus.UnitLabel(p.ua), a.name)
			continue
		case p.ua == nil:
			fmt.Fprintf(w, "unit %s: only in B (%s)\n", vebus.UnitLabel(p.ub), b.name)
			continue
		}
		da, err := a.device(p.ua)
		if err != nil {
			return err
		}
		db, err := b.device(p.ub)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "unit A %s, unit B %s\n", vebus.UnitLabel(p.ua), vebus.UnitLabel(p.ub))
		if p.ua.Firmware() != p.ub.Firmware() {
			fmt.Fprintf(w, "firmware differs: A %d, B %d\n", p.ua.Firmware(), p.ub.Firmware())
		}
		if err := printDiff(w, vebus.DiffDevices(da, db), "A", "B", "only in A"); err != nil {
			return err
		}
	}
	return nil
}

// printDiff prints the differences as a table. Settings only on side A are
// counted on one line labelled onlyA, as for a file against a device they
// are the settings the device read skips.
func printDiff(w io.Writer, diffs []vebus.Difference, la, lb, onlyA string) error {
	var shown []vebus.Difference
	nOnlyA := 0
	for _, x := range diffs {
		if x.InA && !x.InB && x.ID.Kind == vebus.Setting {
			nOnlyA++
			continue
		}
		shown = append(shown, x)
	}
	if len(shown) == 0 {
		fmt.Fprintln(w, "no differences")
	} else {
		fmt.Fprintf(w, "%d differences\n", len(shown))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintf(tw, "  ID\tNAME\t%s\t%s\n", la, lb)
		for _, x := range shown {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", x.ID, x.Name, side(x.InA, x.A), side(x.InB, x.B))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if nOnlyA > 0 {
		fmt.Fprintf(w, "%d settings %s\n", nOnlyA, onlyA)
	}
	return nil
}

func side(in bool, v string) string {
	if !in {
		return "-"
	}
	return v
}

var rvscSetCmd = &command{
	name:    "rvsc-set",
	args:    "<file> <id> <value> -o <out>",
	summary: "change one setting in a .rvsc/.rvms file (offline) and save it as a new file",
	help: `Changes setting <id> of one unit and writes the result to -o. Only that
setting's bytes and the section checksum change; everything else, including
the assistant area and unknown parts, is kept byte for byte.

<value> is in display units, checked against the limits in the file's setting
info. With --raw it is the raw stored value (decimal or 0x hex). With --flag,
<id> is a flag number and <value> is 0 or 1. Pass a negative value after "--".

The model is validated before saving: errors (for example a float voltage
above the absorption voltage) refuse the save unless --force is given. A value
outside the file's limits is always refused.

Grid-code settings (81 and 128-191) are refused unless --allow-grid-code is
given: the grid code is protected by an installer password, its encoding is not
known, and a community report links a changed grid-code word to a system that
went offline (see pkg/vebus GridCodeSetting).

Whether a device accepts an edited file has not been verified.`,
	timeout: time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.rvsc.unit, "unit", "", unitFlagHelp)
		fs.StringVar(&o.rvsc.out, "o", "", "output file (required)")
		fs.BoolVar(&o.rvsc.raw, "raw", false, "<value> is the raw stored value, not display units")
		fs.BoolVar(&o.rvsc.flag, "flag", false, "<id> is a flag number and <value> 0 or 1")
		fs.BoolVar(&o.rvsc.force, "force", false, "save despite validation errors")
		fs.BoolVar(&o.rvsc.allowGridCode, "allow-grid-code", false, "allow changing grid-code settings (81, 128-191)")
	},
	run: runRVSCSet,
}

func runRVSCSet(ctx context.Context, e *env) error {
	if err := e.wantArgs(3); err != nil {
		return err
	}
	o := &e.opts.rvsc
	if o.out == "" {
		return e.usageError("no output file: pass -o <out>")
	}
	if o.flag && o.raw {
		return e.usageError("pass either --flag or --raw, not both")
	}
	n, err := e.parseID(e.args[1], 0xFFFF)
	if err != nil {
		return err
	}
	r, err := loadRVSC(e.args[0])
	if err != nil {
		return err
	}
	u, err := r.unit(o.unit)
	if err != nil {
		return err
	}
	d, err := r.device(u)
	if err != nil {
		return err
	}
	id := vebus.SettingID(uint16(n))
	if o.flag {
		id = vebus.FlagID(uint16(n))
	}
	if !d.Supported(id) {
		return fmt.Errorf("%s is not supported by unit %s (Sc 0 in the file's setting info)", id, vebus.UnitLabel(u))
	}
	old := d.ValueText(id)
	switch {
	case o.flag || o.raw:
		v, perr := strconv.ParseUint(e.args[2], 0, 16)
		if perr != nil {
			return e.usageError("invalid value %q: %v", e.args[2], perr)
		}
		err = d.SetRaw(id, uint16(v))
	default:
		v, perr := strconv.ParseFloat(e.args[2], 64)
		if perr != nil {
			return e.usageError("invalid value %q: %v", e.args[2], perr)
		}
		err = d.SetDisplay(id, v)
	}
	if err != nil {
		return err
	}
	for _, is := range d.Validate() {
		if is.Severity == vebus.Warning || o.force {
			fmt.Fprintf(e.stderr, "%s: %s\n", is.Severity, is.Message)
		}
	}
	changed, err := vebus.SaveRVSC(d, u, vebus.SaveOptions{Override: o.force, AllowGridCode: o.allowGridCode})
	var ge *vebus.GridCodeError
	if errors.As(err, &ge) {
		return fmt.Errorf("%v; pass --allow-grid-code only if you know the grid code you are setting", err)
	}
	if err != nil {
		return err
	}
	data, err := r.f.MarshalBinary()
	if err != nil {
		return err
	}
	if err := os.WriteFile(o.out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "unit %s: %s (%s): %s -> %s\n", vebus.UnitLabel(u), id, d.Name(id), old, d.ValueText(id))
	if len(changed) == 0 {
		fmt.Fprintln(e.stdout, "the value was already set; the file is unchanged")
	}
	fmt.Fprintf(e.stdout, "wrote %s\n", o.out)
	return nil
}
