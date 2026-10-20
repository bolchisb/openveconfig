package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/vms"
)

var rvscSystemCmd = &command{
	name:    "rvsc-system",
	args:    "<backup.rvms> <layout.vms> -o <out>",
	summary: "put the system layout of a .vms file into a .rvsc/.rvms backup (offline) and save it as a new file",
	help: `<backup.rvms> is a file downloaded from the units of the system (a GX
device's VE.Bus backup). Each unit's system record becomes the record the
layout of <layout.vms> gives it; units are matched by long ID. Only those
nine bytes per unit and the section checksums change; settings, assistants
and unknown parts are kept byte for byte.

The file is refused when the layout has errors, or when its units are not
exactly the file's units with the same firmware.

Settings 76-80 are inferred to mirror the system record; they are not
changed until that is checked on hardware. Whether a GX applies the record
when it restores the file has not been verified either.`,
	timeout: time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.rvsc.out, "o", "", "output file (required; not the backup itself)")
	},
	run: runRVSCSystem,
}

func runRVSCSystem(ctx context.Context, e *env) error {
	if err := e.wantArgs(2); err != nil {
		return err
	}
	out := e.opts.rvsc.out
	if out == "" {
		return e.usageError("no output file: pass -o <out>")
	}
	if a, b := filepath.Clean(out), filepath.Clean(e.args[0]); a == b {
		return e.usageError("-o must not be the backup itself")
	}
	data, err := os.ReadFile(e.args[0])
	if err != nil {
		return err
	}
	f, err := rvsc.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", e.args[0], err)
	}
	units, err := f.SystemUnits()
	if err != nil {
		return fmt.Errorf("%s: %w", e.args[0], err)
	}
	vdata, err := os.ReadFile(e.args[1])
	if err != nil {
		return err
	}
	vf, err := vms.Parse(vdata)
	if err != nil {
		return fmt.Errorf("%s: %w", e.args[1], err)
	}
	l, err := vf.Layout(units)
	if err != nil {
		return fmt.Errorf("%s: %w", e.args[1], err)
	}
	if err := f.SetLayout(l); err != nil {
		return fmt.Errorf("%w; nothing was written", err)
	}
	b, err := f.MarshalBinary()
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	for _, r := range l.Records(false) {
		fmt.Fprintf(e.stdout, "unit %s (%08X): %s: %s -> %s\n", r.Unit.Label(), r.Unit.LongID, r.Cell, r.Unit.Record.Hex(), r.Record.Hex())
	}
	fmt.Fprintln(e.stderr, "note: settings 76-80 were not changed (whether they mirror the system record is not verified)")
	fmt.Fprintf(e.stdout, "wrote %s\n", out)
	return nil
}
