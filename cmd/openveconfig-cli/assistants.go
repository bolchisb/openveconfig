package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// maxProbes bounds assistants probe --count (our choice).
const maxProbes = 256

var assistantsCmd = &command{
	name:    "assistants",
	args:    "probe",
	summary: "read-only probe of the assistant block read request (hardware check)",
	help: `assistants probe sends the Winmon command 0x5F, which community reports name
"VLL block read", to --address with the 2-byte argument --arg, then --arg+1
and so on, --count requests in all, and prints each reply as it is, in hex.
Apart from the usual link setup, nothing else is sent; the command is
reported to be a read. What the argument means and how the reply is laid out
are not known yet: this command is for the hardware check that finds out (#3,
#77). Run it with --verbose and keep the log.

"unknown command" means that the device or its firmware does not know the
request; "no reply" that nothing answered in time.

Reading and writing a unit's assistants is not available yet.`,
	timeout: 5 * time.Minute,
	device:  true,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.UintVar(&o.probeArg, "arg", 0, "first argument, 0-65535")
		fs.UintVar(&o.probeCount, "count", 1, fmt.Sprintf("number of requests, 1-%d, with consecutive arguments", maxProbes))
	},
	run: runAssistants,
}

func runAssistants(ctx context.Context, e *env) error {
	if len(e.args) != 1 || e.args[0] != "probe" {
		return e.usageError("want a subcommand: probe")
	}
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
