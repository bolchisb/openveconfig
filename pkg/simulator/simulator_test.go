package simulator

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

func TestFaultDropAndCount(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{}, nil)
	sim.InjectFault(FaultRule{Cmd: mk2.CmdLED, Fault: Drop, Count: 2})
	for i := range 2 {
		if _, err := c.LED(ctx); !errors.Is(err, mk2.ErrTimeout) {
			t.Fatalf("LED %d: %v", i, err)
		}
	}
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED after the faults ran out: %v", err)
	}

	// Count 0 lasts until ClearFaults; other commands are not affected.
	sim.InjectFault(FaultRule{Cmd: mk2.CmdLED, Fault: Drop})
	for range 2 {
		if _, err := c.LED(ctx); !errors.Is(err, mk2.ErrTimeout) {
			t.Fatalf("LED: %v", err)
		}
	}
	if _, err := c.Version(ctx); err != nil {
		t.Fatalf("Version: %v", err)
	}
	sim.ClearFaults()
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED after ClearFaults: %v", err)
	}

	// A retry hides a lost request.
	sim, c = start(t, Config{}, &mk2.Options{Retries: 1})
	sim.InjectFault(FaultRule{Cmd: mk2.CmdLED, Fault: Drop, Count: 1})
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED with retry: %v", err)
	}
}

func TestFaultNoReplyStillWrites(t *testing.T) {
	ctx := context.Background()
	for _, ignore := range []bool{false, true} {
		sim, c := start(t, Config{}, &mk2.Options{IgnoreMissingWriteAck: ignore})
		check(t, "SetAddress", c.SetAddress(ctx, 0))
		sim.InjectFault(FaultRule{WCmd: mk2.WCmdWriteData, Fault: NoReply, Count: 1})
		err := c.WriteSetting(ctx, 2, 1500)
		if ignore && err != nil || !ignore && !errors.Is(err, mk2.ErrTimeout) {
			t.Fatalf("IgnoreMissingWriteAck %v: WriteSetting: %v", ignore, err)
		}
		if v, err := c.ReadSetting(ctx, 2); err != nil || v != 1500 {
			t.Fatalf("ReadSetting = %d, %v", v, err)
		}
	}
}

func TestFaultCorruptRecovers(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{}, &mk2.Options{Timeout: 20 * time.Millisecond})
	sim.InjectFault(FaultRule{Cmd: mk2.CmdFrame, Fault: Corrupt, Count: 1})
	if _, err := c.DCInfo(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("DCInfo with a corrupted reply: %v", err)
	}
	// mk2.ScanFrames skips ahead to the next complete MK2 frame, or to two
	// back-to-back valid VE.Bus frames, instead of waiting for the long
	// <Length> a byte of the bad frame can fake. DC replies are VE.Bus
	// frames, so the first one after the corrupted reply has no partner
	// yet and that request may time out; the next one must succeed.
	_, err := c.DCInfo(ctx)
	if err != nil && !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("DCInfo 1 after the corrupted reply: %v", err)
	}
	if err != nil {
		if _, err := c.DCInfo(ctx); err != nil {
			t.Fatalf("DCInfo 2 after the corrupted reply: %v", err)
		}
	}
	// An MK2 frame resynchronises at once.
	sim.InjectFault(FaultRule{Cmd: mk2.CmdLED, Fault: Corrupt, Count: 1})
	if _, err := c.LED(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("LED with a corrupted reply: %v", err)
	}
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED after the corrupted reply: %v", err)
	}
}

func TestFaultUnsupported(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []mk2.WinmonMode{mk2.WinmonShort, mk2.WinmonLong} {
		t.Run(modeName(mode), func(t *testing.T) {
			sim, c := start(t, Config{}, nil)
			setMode(t, c, mode)
			check(t, "SetAddress", c.SetAddress(ctx, 0))
			sim.InjectFault(FaultRule{Fault: Unsupported})
			if _, err := c.LED(ctx); err != nil {
				t.Fatalf("LED is not a Winmon request: %v", err)
			}
			for name, f := range map[string]func() error{
				"ReadSetting": func() error { _, err := c.ReadSetting(ctx, 2); return err },
				"ReadRAMVar":  func() error { _, err := c.ReadRAMVar(ctx, 4); return err },
				"SettingInfo": func() error { _, err := c.SettingInfo(ctx, 2); return err },
				"RAMVarInfo":  func() error { _, err := c.RAMVarInfo(ctx, 4); return err },
			} {
				if err := f(); !errors.Is(err, mk2.ErrNotSupported) {
					t.Errorf("%s: %v", name, err)
				}
			}
			var uc *mk2.UnknownCommandError
			if _, err := c.SoftwareVersion(ctx); !errors.As(err, &uc) {
				t.Errorf("SoftwareVersion: %v", err)
			}
		})
	}
}

func TestAccessLevel(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{}, nil)
	setMode(t, c, mk2.WinmonLong)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	sim.InjectFault(FaultRule{WCmd: mk2.WCmdWriteViaID, Fault: AccessLevel, Count: 1})
	if err := c.WriteViaID(ctx, mk2.ViaIDSetting, 2, 1400); !errors.Is(err, mk2.ErrAccessLevel) {
		t.Fatalf("injected: %v", err)
	}
	check(t, "WriteViaID", c.WriteViaID(ctx, mk2.ViaIDSetting, 2, 1400))

	// Setting 13 needs access level 2.
	if err := c.WriteViaID(ctx, mk2.ViaIDSetting, 13, 1); !errors.Is(err, mk2.ErrAccessLevel) {
		t.Fatalf("level 0: %v", err)
	}
	_, c = start(t, Config{AccessLevel: 2}, nil)
	setMode(t, c, mk2.WinmonLong)
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	check(t, "WriteViaID at level 2", c.WriteViaID(ctx, mk2.ViaIDSetting, 13, 1))
}

func TestBaudRate(t *testing.T) {
	ctx := context.Background()
	sim, c := start(t, Config{}, nil)
	p := sim.Port()
	if err := p.SetBaudRate(0); err == nil {
		t.Fatal("SetBaudRate(0) accepted")
	}
	// LED, not Version: the power-up 'V' frame may still be on its way and
	// would answer a Version request.
	check(t, "SetBaudRate", p.SetBaudRate(115200))
	if _, err := c.LED(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("LED at mismatched speed: %v", err)
	}
	check(t, "SetBaudRate", p.SetBaudRate(DefaultBaudRate))
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED at 2400: %v", err)
	}

	// A handler added with Handle can switch the simulator's speed, as an
	// MK3 high-speed command would.
	sim.Handle('N', func(l *Line, req mk2.Frame) {
		l.Send(mk2.Frame{Command: 'N'}) // goes out at the old speed
		l.SetBaudRate(115200)
	})
	b, _ := mk2.Frame{Command: 'N'}.Encode()
	if _, err := p.Write(b); err != nil {
		t.Fatal(err)
	}
	if sim.BaudRate() != 115200 {
		t.Fatalf("BaudRate = %d", sim.BaudRate())
	}
	if _, err := c.LED(ctx); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("LED after the switch: %v", err)
	}
	check(t, "SetBaudRate", p.SetBaudRate(115200))
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED at 115200: %v", err)
	}
}

func TestIdleVersionFrames(t *testing.T) {
	for _, tc := range []struct {
		interval time.Duration
		check    func(n int) bool
	}{
		{5 * time.Millisecond, func(n int) bool { return n >= 2 }},
		{-1, func(n int) bool { return n == 1 }}, // power-up frame only
	} {
		var mu sync.Mutex
		n := 0
		_, c := start(t, Config{VersionInterval: tc.interval}, &mk2.Options{Unsolicited: func(f mk2.Frame) {
			if f.Command == mk2.CmdVersion {
				mu.Lock()
				n++
				mu.Unlock()
			}
		}})
		time.Sleep(50 * time.Millisecond)
		// Queued frames are handed to Unsolicited by the next request.
		if _, err := c.LED(context.Background()); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		if !tc.check(n) {
			t.Errorf("interval %v: %d 'V' frames", tc.interval, n)
		}
		mu.Unlock()
	}
}

// rawLine reads frames from a Port without an mk2.Client.
type rawLine struct {
	t  *testing.T
	p  *Port
	sc *bufio.Scanner
}

func newRawLine(t *testing.T, cfg Config) *rawLine {
	cfg.VersionInterval = -1
	s := New(cfg)
	t.Cleanup(func() { s.Port().Close() })
	sc := bufio.NewScanner(s.Port())
	sc.Split(mk2.ScanFrames)
	return &rawLine{t: t, p: s.Port(), sc: sc}
}

func (r *rawLine) next() mk2.Frame {
	r.t.Helper()
	if !r.sc.Scan() {
		r.t.Fatalf("scan: %v", r.sc.Err())
	}
	f, err := mk2.Decode(r.sc.Bytes())
	if err != nil {
		r.t.Fatal(err)
	}
	return f
}

func (r *rawLine) send(f mk2.Frame) {
	r.t.Helper()
	b, err := f.Encode()
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.p.Write(b); err != nil {
		r.t.Fatal(err)
	}
}

func TestRawFrames(t *testing.T) {
	r := newRawLine(t, Config{})
	if f := r.next(); f.Command != mk2.CmdVersion || f.Data[4] != 'B' {
		t.Fatalf("power-up frame = %+v", f)
	}

	// Stray bytes before a request are skipped (sync bytes, noise).
	if _, err := r.p.Write([]byte{0x55, 0x55, 0x00}); err != nil {
		t.Fatal(err)
	}
	r.send(mk2.Frame{Command: mk2.CmdAddress, Data: []byte{1, 0}})
	if f := r.next(); f.Command != mk2.CmdAddress || f.Data[1] != 0 {
		t.Fatalf("'A' reply = %+v", f)
	}

	// Appended LED status (§5 <Flags[1]>), on MK2 and VE.Bus frames.
	r.send(mk2.State{Switch: mk2.SwitchOn, Flags: mk2.StateFlagAppendLED | mk2.StateFlagNoSend}.Frame())
	want := Synthetic12V230().LED
	if f := r.next(); f.Command != mk2.CmdState || f.LED == nil || *f.LED != want {
		t.Fatalf("'S' reply = %+v", f)
	}
	r.send(mk2.Frame{Command: mk2.CmdFrame, Data: []byte{mk2.FTypeDC}})
	if f := r.next(); !f.VEBus || f.LED == nil || len(f.Data) != 14 {
		t.Fatalf("DC frame = %+v", f)
	}

	// Mode 1 setting info: five W frames in two MK2 frames (§7.3).
	r.send(mk2.WinmonRequest{Tag: 'W', Cmd: mk2.WCmdGetSettingInfo, Info: []byte{2, 0}}.Frame())
	if f := r.next(); len(f.Data) != 9 {
		t.Fatalf("first frame = % X", f.Data)
	}
	if f := r.next(); len(f.Data) != 6 || f.Data[0] != mk2.WReplySettingInfoMin {
		t.Fatalf("second frame = % X", f.Data)
	}

	// 'R' resets the address and sends a 'V' frame (§3.6).
	r.send(mk2.Frame{Command: mk2.CmdReset})
	if f := r.next(); f.Command != mk2.CmdVersion || f.Data[4] != 'B' || f.LED != nil {
		t.Fatalf("after reset = %+v", f)
	}
}

func TestPortClose(t *testing.T) {
	s := New(Config{VersionInterval: -1})
	p := s.Port()
	buf := make([]byte, 64)
	if _, err := p.Read(buf); err != nil { // the power-up 'V' frame
		t.Fatal(err)
	}
	done := make(chan error)
	go func() {
		_, err := p.Read(buf)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != io.EOF {
		t.Fatalf("blocked Read after Close: %v", err)
	}
	if _, err := p.Write([]byte{1}); err == nil {
		t.Fatal("Write after Close succeeded")
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestNewRejectsBadAddresses(t *testing.T) {
	for _, devs := range [][]Device{
		{{Address: 1}, {Address: 1}},
		{{Address: mk2.MaxAddress + 1}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%v) did not panic", devs)
				}
			}()
			New(Config{Devices: devs, VersionInterval: -1})
		}()
	}
}
