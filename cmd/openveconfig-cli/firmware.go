package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/bolchisb/openveconfig/pkg/vff"
)

var firmwareCmd = &command{
	name:    "firmware",
	args:    "info <file.vff>",
	summary: "check a .vff firmware file and show its parts and metadata (offline)",
	help: `firmware info reads a .vff file, checks every record's check byte and every
part's checksum, and shows the version, the parts, their metadata and the
number of records. Nothing is flashed and no device is needed. The code and
EEPROM records are not read beyond their check bytes.

Metadata keys whose meaning is not known are shown as they are. An invalid
file gives a non-zero exit status.`,
	timeout: time.Minute,
	run:     runFirmware,
}

func runFirmware(ctx context.Context, e *env) error {
	if len(e.args) == 0 || e.args[0] != "info" {
		return e.usageError("want a subcommand: info")
	}
	if len(e.args) != 2 {
		return e.usageError("want firmware info <file.vff>")
	}
	data, err := os.ReadFile(e.args[1])
	if err != nil {
		return err
	}
	f, err := vff.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", e.args[1], err)
	}
	w := e.stdout
	if v, ok := f.Version(); ok {
		fmt.Fprintf(w, "version   %d\n", v)
	} else {
		fmt.Fprintln(w, "version   none in the file")
	}
	fmt.Fprintf(w, "format    %d\n", f.Parts[0].Format)
	fmt.Fprintf(w, "parts     %d in %d group(s), %d bytes, all checks passed\n", len(f.Parts), len(f.Groups()), len(data))
	for i, p := range f.Parts {
		fmt.Fprintf(w, "\npart %d: %s (offset %d, %d bytes, format %d, checksum %08X)\n", i+1, p.Name(), p.Offset, p.Length, p.Format, p.Checksum)
		fmt.Fprintf(w, "  records: %d ML, %d EEPROM, %d code; descriptor %d bytes\n", len(p.ML), len(p.EEPROM), len(p.Code), len(p.Descriptor))
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, it := range p.Meta {
			v := it.Value
			if len(v) > 48 {
				v = v[:48] + "…"
			}
			fmt.Fprintf(tw, "  %s\t%s\n", it.Key, v)
		}
		tw.Flush()
	}
	for _, warn := range f.Warnings {
		fmt.Fprintf(w, "warning: %s\n", strings.TrimSpace(warn))
	}
	return nil
}
