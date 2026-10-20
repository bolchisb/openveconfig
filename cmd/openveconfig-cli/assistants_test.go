package main

import (
	"testing"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

func TestAssistantsProbe(t *testing.T) {
	// The simulated devices do not know 0x5F.
	code, out, errOut := cli(t, args("assistants", "probe")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "Winmon 0x5F to device 0", "arg     0: unknown command (reason 0x00)")

	// A made-up device that answers arguments below 2.
	withSimulator(t, func(s *simulator.Simulator) {
		s.Handle('W', func(l *simulator.Line, req mk2.Frame) {
			if req.Data[0] != mk2.WCmdVLLBlockRead {
				return
			}
			if req.Data[1] >= 2 {
				l.Send(mk2.Frame{Command: 'W', Data: []byte{mk2.WReplyUnknownCommand, 1, 0}})
				return
			}
			l.Send(mk2.Frame{Command: 'W', Data: []byte{0xA0, req.Data[1], 0x11, 0x22}})
		})
	})
	code, out, errOut = cli(t, args("assistants", "probe", "--count", "3", "--address", "1")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "Winmon 0x5F to device 1",
		"arg     0: reply 0xA0, 4 bytes: A0 00 11 22",
		"arg     1: reply 0xA0, 4 bytes: A0 01 11 22",
		"arg     2: unknown command (reason 0x01)")
}

func TestAssistantsProbeUsage(t *testing.T) {
	for _, a := range [][]string{
		args("assistants"),
		args("assistants", "write"),
		args("assistants", "probe", "--count", "0"),
		args("assistants", "probe", "--count", "257"),
		args("assistants", "probe", "--arg", "65536"),
		args("assistants", "probe", "--arg", "65535", "--count", "2"),
	} {
		if code, _, _ := cli(t, a...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", a, code, exitUsage)
		}
	}
}
