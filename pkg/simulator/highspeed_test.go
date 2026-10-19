package simulator

import (
	"context"
	"testing"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

func TestNegotiateBaud(t *testing.T) {
	for _, tc := range []struct {
		name      string
		highSpeed bool // an MK3 that accepts 'N'
		stuck     bool // left at 115200 by an earlier session
		mode      mk2.HighSpeed
		want      int
	}{
		{"MK3", true, false, mk2.HighSpeedAuto, mk2.HighSpeedBaudRate},
		{"MK2 falls back", false, false, mk2.HighSpeedAuto, mk2.BaudRate},
		{"MK3 HighSpeedOff", true, false, mk2.HighSpeedOff, mk2.BaudRate},
		{"MK3 stuck HighSpeedOff", true, true, mk2.HighSpeedOff, mk2.BaudRate},
		{"MK3 stuck", true, true, mk2.HighSpeedAuto, mk2.HighSpeedBaudRate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // each run waits through the real switch delays
			ctx := context.Background()
			sim, c := start(t, Config{HighSpeed: tc.highSpeed}, nil)
			if tc.stuck {
				// An earlier session switched the MK3 and the new host
				// opened the port at 2400.
				b, _ := mk2.Frame{Command: mk2.CmdBaudRate}.Encode()
				if _, err := sim.Port().Write(b); err != nil {
					t.Fatal(err)
				}
				if sim.BaudRate() != mk2.HighSpeedBaudRate {
					t.Fatalf("simulator at %d after 'N'", sim.BaudRate())
				}
			}
			baud, err := c.NegotiateBaud(ctx, sim.Port(), tc.mode)
			if err != nil || baud != tc.want {
				t.Fatalf("NegotiateBaud = %d, %v; want %d, nil", baud, err, tc.want)
			}
			if sim.BaudRate() != tc.want {
				t.Fatalf("simulator at %d, want %d", sim.BaudRate(), tc.want)
			}
			check(t, "SetAddress", c.SetAddress(ctx, 0))
			if v, err := c.ReadSetting(ctx, 2); err != nil || v == 0 {
				t.Fatalf("ReadSetting = %d, %v", v, err)
			}
			if _, err := c.ReadRAMVar(ctx, 4); err != nil {
				t.Fatalf("ReadRAMVar: %v", err)
			}
			if _, err := c.LED(ctx); err != nil {
				t.Fatalf("LED: %v", err)
			}
		})
	}
}

func TestResetRestoresBaudRate(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{HighSpeed: true}, nil)
	p := sim.Port()
	b, _ := mk2.Frame{Command: mk2.CmdBaudRate}.Encode()
	if _, err := p.Write(b); err != nil {
		t.Fatal(err)
	}
	check(t, "SetBaudRate", p.SetBaudRate(mk2.HighSpeedBaudRate))
	check(t, "Reset", c.Reset(ctx))
	if sim.BaudRate() != DefaultBaudRate {
		t.Fatalf("simulator at %d after 'R'", sim.BaudRate())
	}
}
