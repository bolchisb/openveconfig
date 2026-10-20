// Command openveconfig-cli is a command-line tool for VE.Bus diagnostics and
// configuration over an MK2/MK3 interface.
//
// Usage:
//
//	openveconfig-cli <command> [flags] [arguments]
//
// Commands:
//
//	ports          list serial ports and flag likely MK2/MK3 interfaces
//	version        show interface and device firmware versions
//	read-setting   read one setting with its scale, offset, default and limits
//	read-ram       read one RAM variable with its scale and offset
//	write-setting  write one setting (needs --yes), then read it back
//	dump           print all readable settings and RAM variables as JSON
//	rvsc-show      print the units and settings of a .rvsc/.rvms file
//	rvsc-diff      compare a .rvsc/.rvms file with a device or another file
//	rvsc-set       change one setting in a .rvsc/.rvms file (offline)
//	rvsc-apply     write one .rvsc/.rvms unit to a device (dry run without --yes)
//	apply          write the settings of a dump file to devices (dry run without --yes)
//	system show        find the units of a VE.Bus system and check their records
//	system write       write a .vms layout to the units (dry run without --yes)
//	system reset       restart every unit of the system (needs --yes)
//	system standalone  make the one unit switched on standalone (needs --yes)
//
// Run "openveconfig-cli <command> --help" for the flags of a command. Every
// command that talks to a device takes --simulator to use the built-in
// simulator instead of a serial port.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/serialport"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Exit codes.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// command is one subcommand.
type command struct {
	name     string
	args     string // positional arguments, for the usage line
	summary  string
	help     string // extra text for --help
	timeout  time.Duration
	setFlags func(fs *flag.FlagSet, o *options) // command-specific flags; may be nil
	run      func(ctx context.Context, env *env) error
	// device reports whether the command talks to a device, and so takes
	// the connection flags.
	device bool
	// systemBus makes --simulator a VE.Bus system (newSystemSimulator).
	systemBus bool
}

// env is what a command gets to work with.
type env struct {
	cmd    *command
	args   []string // positional arguments
	opts   *options
	stdout io.Writer
	stderr io.Writer
}

// options holds the parsed flags. The first group is shared by the device
// commands; the rest belong to single commands.
type options struct {
	port      string
	address   uint
	baud      string
	simulator bool
	verbose   bool

	probe      bool // ports
	yes, raw   bool // write-setting
	maxSetting uint // dump
	maxRAM     uint // dump
	rvsc       rvscOptions
	apply      applyOptions
	system     systemOptions
}

// errUsage means a usage message has already been printed.
var errUsage = errors.New("usage error")

var commands = []*command{portsCmd, versionCmd, readSettingCmd, readRAMCmd, writeSettingCmd, dumpCmd,
	rvscShowCmd, rvscDiffCmd, rvscSetCmd, rvscApplyCmd, applyCmd, systemCmd}

// run is main without the process: it returns the exit code.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		usage(stdout)
		return exitOK
	}
	// Flags may come before the command ("--simulator dump"): the first
	// argument that names a command is the command.
	i := slices.IndexFunc(args, func(a string) bool { return lookup(a) != nil })
	if i < 0 {
		if strings.HasPrefix(args[0], "-") {
			fmt.Fprint(stderr, "openveconfig-cli: no command given\n\n")
		} else {
			fmt.Fprintf(stderr, "openveconfig-cli: unknown command %q\n\n", args[0])
		}
		usage(stderr)
		return exitUsage
	}
	cmd := lookup(args[i])
	args = append(args[:i:i], args[i+1:]...)

	e := &env{cmd: cmd, opts: &options{}, stdout: stdout, stderr: stderr}
	fs := flag.NewFlagSet(cmd.name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { commandUsage(fs, cmd) }
	if cmd.device {
		e.opts.register(fs)
	}
	if cmd.setFlags != nil {
		cmd.setFlags(fs, e.opts)
	}
	pos, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitUsage // the flag package printed the error and usage
	}
	e.args = pos
	if cmd.device {
		if err := e.opts.validate(); err != nil {
			e.usageError("%v", err)
			return exitUsage
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, cmd.timeout)
	defer cancel()
	switch err := cmd.run(ctx, e); {
	case err == nil:
		return exitOK
	case errors.Is(err, errUsage):
		return exitUsage
	default:
		fmt.Fprintf(stderr, "openveconfig-cli %s: %v\n", cmd.name, err)
		return exitError
	}
}

// lookup returns the command called name, or nil.
func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

func usage(w io.Writer) {
	fmt.Fprint(w, "openveconfig-cli: VE.Bus diagnostics and configuration over an MK2/MK3 interface.\n\n")
	fmt.Fprint(w, "Usage:\n  openveconfig-cli <command> [flags] [arguments]\n\nCommands:\n")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-14s %s\n", c.name, c.summary)
	}
	fmt.Fprint(w, "\nRun 'openveconfig-cli <command> --help' for the flags of a command.\n")
}

func commandUsage(fs *flag.FlagSet, c *command) {
	w := fs.Output()
	fmt.Fprintf(w, "Usage: openveconfig-cli %s [flags] %s\n\n%s\n", c.name, c.args, c.summary)
	if c.help != "" {
		fmt.Fprintf(w, "\n%s\n", strings.TrimSpace(c.help))
	}
	fmt.Fprint(w, "\nFlags:\n")
	fs.PrintDefaults()
}

// usageError prints a usage problem and returns errUsage.
func (e *env) usageError(format string, a ...any) error {
	fmt.Fprintf(e.stderr, "openveconfig-cli %s: %s\n", e.cmd.name, fmt.Sprintf(format, a...))
	fmt.Fprintf(e.stderr, "Run 'openveconfig-cli %s --help' for usage.\n", e.cmd.name)
	return errUsage
}

// wantArgs checks the number of positional arguments.
func (e *env) wantArgs(n int) error {
	if len(e.args) != n {
		return e.usageError("want %d argument(s) (%s), got %d", n, e.cmd.args, len(e.args))
	}
	return nil
}

// parseInterspersed parses fs like fs.Parse but also accepts flags after
// positional arguments, so "write-setting 2 14.5 --yes" works. Everything
// after "--" is positional, which is how a negative value is passed.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if i := len(args) - len(rest) - 1; i >= 0 && args[i] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func (o *options) register(fs *flag.FlagSet) {
	fs.UintVar(&o.address, "address", 0, "VE.Bus device address, 0-31")
	o.registerLink(fs)
}

// registerLink registers the connection flags other than --address.
func (o *options) registerLink(fs *flag.FlagSet) {
	fs.StringVar(&o.port, "port", "", "serial port of the interface, e.g. COM3, /dev/ttyUSB0 or /dev/cu.usbserial-XXXX")
	fs.StringVar(&o.baud, "baud", "auto", "line speed: auto (try 115200, fall back to 2400), 2400, or 115200\n"+
		"(115200 tries the switch and fails if the interface stays at 2400)")
	o.registerSimulator(fs)
	o.registerVerbose(fs)
}

func (o *options) registerSimulator(fs *flag.FlagSet) {
	fs.BoolVar(&o.simulator, "simulator", false,
		"use the built-in simulator (two synthetic devices, addresses 0 and 1) instead of a serial port")
}

func (o *options) registerVerbose(fs *flag.FlagSet) {
	fs.BoolVar(&o.verbose, "verbose", false,
		"log every frame sent and received to stderr, in hex with direction and timestamp")
}

func (o *options) validate() error {
	switch o.baud {
	case "auto", "2400", "115200":
	default:
		return fmt.Errorf("--baud must be auto, 2400 or 115200, not %q", o.baud)
	}
	if o.address > mk2.MaxAddress {
		return fmt.Errorf("--address must be 0-%d, not %d", mk2.MaxAddress, o.address)
	}
	if o.port == "" && !o.simulator {
		return errors.New("no interface: pass --port NAME (see 'openveconfig-cli ports') or --simulator")
	}
	if o.port != "" && o.simulator {
		return errors.New("pass either --port or --simulator, not both")
	}
	return nil
}

// transport is the serial line under an mk2.Client.
type transport interface {
	io.ReadWriteCloser
	mk2.BaudRateSetter
}

// newSimulator returns the simulator used by --simulator. Tests replace it.
var newSimulator = func() *simulator.Simulator {
	return simulator.New(simulator.Config{HighSpeed: true, Devices: []simulator.Device{
		{Address: 0, Profile: simulator.Synthetic12V230()},
		{Address: 1, Profile: simulator.Synthetic48V120()},
	}})
}

// open opens the serial port or simulator chosen by the flags, wrapped in a
// frame log with --verbose. name is the serial port; it is ignored with
// --simulator.
func (e *env) open(name string) (transport, error) {
	var t transport
	if e.opts.simulator && e.cmd.systemBus {
		t = newSystemSimulator().Port()
	} else if e.opts.simulator {
		t = newSimulator().Port()
	} else {
		p, err := serialport.Open(name)
		if err != nil {
			return nil, err
		}
		t = p
	}
	if e.opts.verbose {
		t = newFrameLog(t, e.stderr)
	}
	return t, nil
}

// clientOptions are the options of the mk2.Client. The write commands read
// the value back, so a missing acknowledgement (reported for some settings)
// need not fail the write. Tests shorten the timeouts.
var clientOptions = mk2.Options{IgnoreMissingWriteAck: true}

// connect opens the interface, sets up the line speed chosen by --baud and
// selects --address. The caller must close the client, which also closes
// the port.
func (e *env) connect(ctx context.Context) (*mk2.Client, int, error) {
	t, err := e.open(e.opts.port)
	if err != nil {
		return nil, 0, err
	}
	opts := clientOptions
	c := mk2.NewClient(t, &opts)
	mode := mk2.HighSpeedAuto
	if e.opts.baud == "2400" {
		mode = mk2.HighSpeedOff
	}
	baud, err := c.NegotiateBaud(ctx, t, mode)
	if err == nil && e.opts.baud == "115200" && baud != mk2.HighSpeedBaudRate {
		err = fmt.Errorf("the interface did not switch to %d baud (the link works at %d; use --baud auto or 2400)",
			mk2.HighSpeedBaudRate, baud)
	}
	if err != nil {
		c.Close()
		return nil, 0, fmt.Errorf("set up link: %w", err)
	}
	if e.opts.verbose {
		fmt.Fprintf(e.stderr, "link at %d baud\n", baud)
	}
	return c, baud, nil
}

// connectDevice is connect followed by selecting --address.
func (e *env) connectDevice(ctx context.Context) (*mk2.Client, error) {
	c, _, err := e.connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.SetAddress(ctx, byte(e.opts.address)); err != nil {
		c.Close()
		return nil, fmt.Errorf("select address %d: %w", e.opts.address, err)
	}
	return c, nil
}
