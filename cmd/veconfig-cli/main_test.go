package main

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
)

// cli runs the command line and returns its exit code, stdout and stderr.
func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// sim are the flags for a fast simulator session: 2400 baud skips the
// high-speed attempt.
var sim = []string{"--simulator", "--baud", "2400"}

func args(a ...string) []string {
	return append(append([]string{}, a[:1]...), append(sim, a[1:]...)...)
}

// withSimulator makes --simulator use a Simulator set up by setup.
func withSimulator(t *testing.T, setup func(*simulator.Simulator)) {
	t.Helper()
	orig := newSimulator
	t.Cleanup(func() { newSimulator = orig })
	newSimulator = func() *simulator.Simulator {
		s := orig()
		setup(s)
		return s
	}
}

func wantContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output does not contain %q:\n%s", w, got)
		}
	}
}

func TestPortsSimulatorProbe(t *testing.T) {
	code, out, errOut := cli(t, "ports", "--simulator", "--probe")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "PROBE", "simulator", "interface answers, version 1130131")
}

func TestVersion(t *testing.T) {
	// Flags before the command work too.
	code, out, errOut := cli(t, append(sim, "version")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "link:      2400 baud", "interface: version 1130131", "devices:   2 found",
		"address 0: firmware 2699481", "address 1: firmware 2799482")
}

func TestReadSetting(t *testing.T) {
	code, out, errOut := cli(t, args("read-setting", "2")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "setting 2 (device 0)", "raw      1440", "value    14.4", "scale    0.01 (Sc -100)",
		"default  14.4 (raw 1440)", "min      8 (raw 800)", "max      17.5 (raw 1750)")

	// A signed setting stored with 0x8000 added (§7.3.13.2), as hex ID.
	code, out, errOut = cli(t, args("read-setting", "0x3f")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "setting 63", "value    0.2", "offset   -32768", "min      -2 (raw 32568)")

	code, _, errOut = cli(t, args("read-setting", "7")...)
	if code != exitError || !strings.Contains(errOut, "not supported") {
		t.Errorf("unsupported setting: exit %d, %s", code, errOut)
	}
}

func TestReadRAM(t *testing.T) {
	code, out, errOut := cli(t, args("read-ram", "--address", "1", "5")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "RAM variable 5 (device 1)", "raw      61116", "value    -44.2", "signed   true")

	code, out, errOut = cli(t, args("read-ram", "--address", "1", "10")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "value    1", "bit      3")

	if code, _, _ := cli(t, args("read-ram", "256")...); code != exitUsage {
		t.Errorf("RAM ID 256: exit %d, want %d", code, exitUsage)
	}
}

func TestWriteSetting(t *testing.T) {
	// Flags after the arguments work too.
	code, out, errOut := cli(t, args("write-setting", "2", "14.5", "--yes")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "14.4 -> 14.5 (raw 1440 -> 1450), read back OK")

	code, out, errOut = cli(t, args("write-setting", "--yes", "--raw", "--address", "1", "4", "0x20")...)
	if code != exitOK {
		t.Fatalf("raw: exit %d: %s", code, errOut)
	}
	wantContains(t, out, "setting 4 (device 1): 35 -> 32 (raw 35 -> 32)")

	// A negative display value after "--".
	code, out, errOut = cli(t, args("write-setting", "--yes", "63", "--", "-1.5")...)
	if code != exitOK {
		t.Fatalf("negative: exit %d: %s", code, errOut)
	}
	wantContains(t, out, "0.2 -> -1.5 (raw 32788 -> 32618)")
}

func TestWriteSettingWithoutYes(t *testing.T) {
	withSimulator(t, func(*simulator.Simulator) { t.Error("connected to the device without --yes") })
	code, out, errOut := cli(t, args("write-setting", "2", "14.5")...)
	if code != exitUsage || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	wantContains(t, errOut, "refusing to write setting 2 without --yes")
}

func TestWriteSettingOutOfRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"above max", []string{"2", "17.6"}, "17.6 (raw 1760) is outside the device limits 8 to 17.5 (raw 800 to 1750)"},
		{"below min", []string{"2", "7.99"}, "outside the device limits"},
		{"raw above max", []string{"--raw", "2", "1751"}, "(raw 1751) is outside the device limits"},
		{"not 16 bits", []string{"2", "1000"}, "1000 is out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := append(args("write-setting", "--yes", "--verbose"), tc.args...)
			code, _, errOut := cli(t, a...)
			if code != exitError {
				t.Fatalf("exit %d, want %d: %s", code, exitError, errOut)
			}
			wantContains(t, errOut, tc.want)
			// No CommandWriteSetting (0x33) or CommandWriteData (0x34)
			// went out.
			if strings.Contains(errOut, "FF 57 33") || strings.Contains(errOut, "FF 57 34") {
				t.Errorf("a write was sent:\n%s", errOut)
			}
		})
	}
}

func TestWriteSettingReadBackMismatch(t *testing.T) {
	// Lose CommandWriteData: the write never happens and its missing
	// acknowledgement is ignored, so only the read-back can catch it.
	withSimulator(t, func(s *simulator.Simulator) {
		s.InjectFault(simulator.FaultRule{Cmd: mk2.CmdWinmon, WCmd: mk2.WCmdWriteData, Fault: simulator.Drop})
	})
	code, _, errOut := cli(t, args("write-setting", "--yes", "2", "14.5")...)
	if code != exitError {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, errOut, "wrote raw 1450 but read back raw 1440")
}

func TestDump(t *testing.T) {
	code, out, errOut := cli(t, args("dump", "--address", "1")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var d dumpOutput
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if d.LinkBaud != 2400 || d.InterfaceVersion != 1130131 || d.Address != 1 || d.Firmware != 2799482 {
		t.Errorf("header = %+v", d)
	}
	p := simulator.Synthetic48V120()
	if len(d.Settings) != len(p.Settings) || len(d.RAM) != len(p.RAMVars) || len(d.Errors) != 0 {
		t.Fatalf("got %d settings, %d RAM vars, errors %v; want %d, %d, none",
			len(d.Settings), len(d.RAM), d.Errors, len(p.Settings), len(p.RAMVars))
	}
	for i, s := range d.Settings {
		if want := p.Settings[i]; s.ID != want.ID || s.Raw != want.Default || s.Info.Min != want.Min || s.Info.Max != want.Max {
			t.Errorf("setting %+v, want %+v", s, want)
		}
	}
	s := d.Settings[2]
	if s.ID != 2 || s.Value != 57.6 || s.Info.Scale != 0.01 || s.Info.MinValue != 32 || s.Info.MaxValue != 70 {
		t.Errorf("setting 2 = %+v", s)
	}
	r := d.RAM[5]
	if r.ID != 5 || r.Value != -44.2 || !r.Info.Signed || r.Info.Bit != nil {
		t.Errorf("RAM 5 = %+v", r)
	}
	if r := d.RAM[10]; r.ID != 10 || r.Info.Bit == nil || *r.Info.Bit != 3 || r.Value != 1 {
		t.Errorf("RAM 10 = %+v", r)
	}
}

func TestDumpRecordsPerIDErrors(t *testing.T) {
	// RAM variable 0 does not answer once: it is listed under "errors" and
	// the dump goes on.
	withSimulator(t, func(s *simulator.Simulator) {
		s.InjectFault(simulator.FaultRule{Cmd: mk2.CmdWinmon, WCmd: mk2.WCmdReadRAMVar, Fault: simulator.NoReply, Count: 1})
	})
	code, out, errOut := cli(t, args("dump", "--max-setting", "3", "--max-ram", "1")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var d dumpOutput
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(d.Settings) != 4 || len(d.RAM) != 1 || len(d.Errors) != 1 || d.Errors[0].Kind != "ram" || d.Errors[0].ID != 0 ||
		!strings.Contains(d.Errors[0].Error, "timeout") {
		t.Errorf("settings %d, RAM %d, errors %+v", len(d.Settings), len(d.RAM), d.Errors)
	}
}

func TestVerboseFrameLog(t *testing.T) {
	code, out, errOut := cli(t, args("read-setting", "--verbose", "2")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "raw      1440")
	for _, re := range []string{
		`(?m)^\d\d:\d\d:\d\d\.\d{3} TX 02 FF 56 A9$`,             // 'V' request
		`(?m)^\d\d:\d\d:\d\d\.\d{3} RX 07 FF 56( [0-9A-F]{2})+$`, // 'V' reply
		`(?m)^\d\d:\d\d:\d\d\.\d{3} TX 05 FF 57 31 02 00 72$`,    // read setting 2
		`(?m)^\d\d:\d\d:\d\d\.\d{3} RX 05 FF 57 86 A0 05 7A$`,    // 1440
		`(?m)^\d\d:\d\d:\d\d\.\d{3} -- baud 2400$`,
	} {
		if !regexp.MustCompile(re).MatchString(errOut) {
			t.Errorf("frame log does not match %s:\n%s", re, errOut)
		}
	}
}

// chunks is a transport whose reads return the given pieces in turn.
type chunks [][]byte

func (c *chunks) Read(b []byte) (int, error) {
	if len(*c) == 0 {
		return 0, io.EOF
	}
	n := copy(b, (*c)[0])
	*c = (*c)[1:]
	return n, nil
}
func (c *chunks) Write(b []byte) (int, error) { return len(b), nil }
func (c *chunks) Close() error                { return nil }
func (c *chunks) SetBaudRate(int) error       { return nil }

func TestFrameLogSplitsFrames(t *testing.T) {
	// A frame arriving in pieces, with a noise byte in front, is logged
	// as one line; a partial frame left at Close is logged as it is.
	var buf bytes.Buffer
	l := newFrameLog(&chunks{{0x00, 0x07, 0xFF}, {0x56, 0x93, 0x3E, 0x11, 0x00, 0x42}, {0x80, 0x02}}, &buf)
	b := make([]byte, 16)
	for {
		if _, err := l.Read(b); err != nil {
			break
		}
	}
	l.Close()
	want := regexp.MustCompile(`^\S+ RX \?\? 00\n\S+ RX 07 FF 56 93 3E 11 00 42 80\n\S+ RX \?\? 02\n$`)
	if !want.MatchString(buf.String()) {
		t.Errorf("log:\n%s", buf.String())
	}
}

func TestBaud115200(t *testing.T) {
	// An interface that ignores 'N' stays at 2400: forced 115200 fails.
	withSimulator(t, func(s *simulator.Simulator) {
		s.Handle(mk2.CmdBaudRate, func(*simulator.Line, mk2.Frame) {})
	})
	code, _, errOut := cli(t, "read-setting", "--simulator", "--baud", "115200", "2")
	if code != exitError {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, errOut, "did not switch to 115200 baud")

	// One that switches on 'N' works at 115200.
	withSimulator(t, func(s *simulator.Simulator) {
		s.Handle(mk2.CmdBaudRate, func(l *simulator.Line, _ mk2.Frame) { l.SetBaudRate(mk2.HighSpeedBaudRate) })
	})
	code, out, errOut := cli(t, "version", "--simulator", "--baud", "115200")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "link:      115200 baud")
}

func TestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{nil, exitUsage, "Commands:"},
		{[]string{"bogus"}, exitUsage, `unknown command "bogus"`},
		{[]string{"--simulator"}, exitUsage, "no command given"},
		{[]string{"read-setting", "--help"}, exitOK, "Usage: veconfig-cli read-setting [flags] <id>"},
		{[]string{"read-setting", "2"}, exitUsage, "pass --port NAME"},
		{[]string{"read-setting", "--simulator", "--baud", "9600", "2"}, exitUsage, "--baud must be auto, 2400 or 115200"},
		{[]string{"read-setting", "--simulator", "--address", "32", "2"}, exitUsage, "--address must be 0-31"},
		{[]string{"read-setting", "--simulator"}, exitUsage, "want 1 argument(s)"},
		{[]string{"read-setting", "--simulator", "x"}, exitUsage, `invalid ID "x"`},
		{[]string{"write-setting", "--simulator", "--yes", "2", "abc"}, exitUsage, `invalid value "abc"`},
		{[]string{"dump", "--simulator", "--max-ram", "256"}, exitUsage, "--max-ram must be at most 255"},
		{[]string{"ports", "--nope"}, exitUsage, "flag provided but not defined"},
	} {
		code, out, errOut := cli(t, tc.args...)
		if code != tc.code || !strings.Contains(out+errOut, tc.want) {
			t.Errorf("%q: exit %d, want %d and %q in:\n%s%s", tc.args, code, tc.code, tc.want, out, errOut)
		}
	}
}
