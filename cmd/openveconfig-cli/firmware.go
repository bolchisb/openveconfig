package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	fw "github.com/bolchisb/openveconfig/pkg/firmware"
	"github.com/bolchisb/openveconfig/pkg/serialport"
	"github.com/bolchisb/openveconfig/pkg/vff"
)

var firmwareCmd = &command{
	name:    "firmware",
	args:    "info|flash <file.vff>",
	summary: "check a .vff firmware file (info), or flash it to a device (flash)",
	help: `firmware info reads a .vff file, checks every record's check byte and every
part's checksum, and shows the version, the parts, their metadata and the
number of records. Nothing is flashed and no device is needed.

firmware flash backs up the connected device's settings to a .rvsc file,
checks the file against the device (firmware family and bootblock
requirements), then streams it to the device's bootloader, printing progress.
It needs --simulator or --port.

Flashing resets the device to its default settings, so the backup is written
first (BEFORE anything is checked or flashed), next to the firmware file or to
--backup <path>; apply it back later with "rvsc-apply". The backup is a
settings backup: the long ID, serial number and assistant area cannot be read
from a device over the MK2 protocol and are left out.

SAFETY: flashing can brick hardware and is NOT yet validated on real
hardware (#38). Against --simulator it runs fully. Against a real --port it
refuses before sending anything, unless you pass
--i-understand-this-can-brick-my-device; even then it stays behind that flag.`,
	timeout: 10 * time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		o.register(fs)
		fs.BoolVar(&o.fwGate, "i-understand-this-can-brick-my-device", false,
			"allow flashing a REAL device (off by default; #38 is not hardware-validated)")
		fs.StringVar(&o.fwBackup, "backup", "",
			"where to save the settings backup (.rvsc); default: next to the firmware file, timestamped")
	},
	run: runFirmware,
}

func runFirmware(ctx context.Context, e *env) error {
	if len(e.args) == 0 {
		return e.usageError("want a subcommand: info or flash")
	}
	switch e.args[0] {
	case "info":
		return runFirmwareInfo(ctx, e)
	case "flash":
		return runFirmwareFlash(ctx, e)
	default:
		return e.usageError("unknown subcommand %q: want info or flash", e.args[0])
	}
}

func runFirmwareInfo(ctx context.Context, e *env) error {
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

func runFirmwareFlash(ctx context.Context, e *env) error {
	if len(e.args) != 2 {
		return e.usageError("want firmware flash <file.vff>")
	}
	if err := e.opts.validate(); err != nil {
		return e.usageError("%v", err)
	}
	data, err := os.ReadFile(e.args[1])
	if err != nil {
		return err
	}
	f, err := vff.Parse(data)
	if err != nil {
		return fmt.Errorf("%s: %w", e.args[1], err)
	}
	// Safety gate FIRST: refuse a real device without the opt-in before
	// touching the port, so nothing destructive (or even a connection) is
	// attempted (#38).
	if err := fw.CheckHardwareGate(e.opts.simulator, e.opts.fwGate); err != nil {
		return err
	}

	w := e.stdout
	// Back up the device's settings first: flashing resets them to defaults.
	if err := e.backupSettings(ctx, e.args[1]); err != nil {
		return fmt.Errorf("settings backup: %w", err)
	}

	// Pre-flight: read the device's firmware number over the normal MK2
	// protocol, then check the file against it.
	target, err := e.readTarget(ctx)
	if err != nil {
		return fmt.Errorf("pre-flight: %w", err)
	}
	if err := fw.Preflight(f, target); err != nil {
		return err
	}
	fv, _ := f.Version()
	fmt.Fprintf(w, "pre-flight OK: device firmware %d, file firmware %d\n", target.FirmwareNumber, fv)

	// Reprogram over the bootloader transport (a different framing, so not
	// the MK2 client).
	t, err := e.openReprogram()
	if err != nil {
		return err
	}
	defer t.Close()
	rp := fw.NewReprogrammer(t, &fw.Options{Progress: func(p fw.Progress) {
		switch p.Phase {
		case fw.PhaseEntering:
			fmt.Fprintln(w, "entering bootloader")
		case fw.PhaseErasing:
			fmt.Fprintf(w, "erasing firmware (%d blocks)\n", p.Total)
		case fw.PhaseProgramming:
			if e.opts.verbose {
				fmt.Fprintf(w, "  block %d/%d\n", p.Block, p.Total)
			}
		case fw.PhaseDone:
			fmt.Fprintf(w, "programmed %d blocks\n", p.Total)
		}
	}})
	defer rp.Close()
	if err := rp.EnterBootloader(ctx); err != nil {
		return err
	}
	if err := rp.Program(ctx, f, target); err != nil {
		return err
	}
	fmt.Fprintln(w, "firmware flashed successfully")
	return nil
}

// backupSettings reads the connected device's settings and writes them to a
// .rvsc file, so the user can restore them after flashing resets the device.
// fwFile is the firmware file, used to place the default backup next to it.
func (e *env) backupSettings(ctx context.Context, fwFile string) error {
	path := e.opts.fwBackup
	if path == "" {
		base := strings.TrimSuffix(fwFile, filepath.Ext(fwFile))
		path = fmt.Sprintf("%s-backup-%s.rvsc", base, time.Now().Format("20060102-150405"))
	}
	c, err := e.connectDevice(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	file, err := fw.BackupSettings(ctx, c, byte(e.opts.address), nil)
	if err != nil {
		return err
	}
	data, err := file.MarshalBinary()
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "settings backed up to %s (apply it back with rvsc-apply)\n", path)
	return nil
}

// readTarget reads the connected device's firmware number for pre-flight.
func (e *env) readTarget(ctx context.Context) (fw.Target, error) {
	c, err := e.connectDevice(ctx)
	if err != nil {
		return fw.Target{}, err
	}
	defer c.Close()
	num, err := c.SoftwareVersion(ctx)
	if err != nil {
		return fw.Target{}, fmt.Errorf("read device firmware number: %w", err)
	}
	// The bootblock version is read over the bootloader on real hardware; the
	// MK2 protocol does not expose it, so it is left unknown (-1) and the
	// bootblock gate is skipped against the simulator.
	return fw.Target{FirmwareNumber: num, BootblockVersion: -1, BootblockRevision: -1}, nil
}

// reprogramTransport is the raw serial line the Reprogrammer writes to.
type reprogramTransport interface {
	io.ReadWriteCloser
}

// newReprogramSim returns the --simulator bootloader target. Tests replace
// it to simulate an interruption or to inspect the written blocks.
var newReprogramSim = func() reprogramTransport { return fw.NewBootSim(fw.BootConfig{}) }

// openReprogram opens the transport for the reprogram phase: the bootloader
// fake for --simulator, or the real serial port when the opt-in gate is
// already passed.
func (e *env) openReprogram() (reprogramTransport, error) {
	if e.opts.simulator {
		return newReprogramSim(), nil
	}
	return serialport.Open(e.opts.port)
}
