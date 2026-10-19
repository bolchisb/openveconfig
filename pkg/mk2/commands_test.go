package mk2

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

// scripted returns a handler that answers each request (given without its
// checksum, as in the Appendix 2 dump) with the mapped reply, also given
// without checksum. Unknown requests fail the test.
func scripted(t *testing.T, script map[string]string) func(d *device, req Frame) {
	m := map[string][]byte{}
	for req, reply := range script {
		m[hex.EncodeToString(withCS(t, req))] = withCS(t, reply)
	}
	return func(d *device, req Frame) {
		b, _ := req.Encode()
		reply, ok := m[hex.EncodeToString(b)]
		if !ok {
			t.Errorf("unexpected request % X", b)
			return
		}
		d.send(reply)
	}
}

// TestAppendix2StartupTrace replays the annotated trace of Appendix 2: the
// requests the Client sends must match the dump byte for byte, and the
// values derived from the replies must match the ones the trace prints.
func TestAppendix2StartupTrace(t *testing.T) {
	script := map[string]string{
		"02 FF 'V":       "07 FF 'V 93 3E 11 00 00",
		"04 FF 'A 01 00": "04 FF 'A 01 00",
		"03 FF 'F 05":    "0C 41 10 09 00 00 00 3E 00 E8 03 F4 01",
		"03 FF 'F 00":    "0F 20 7F 9A 81 79 0C 51 0A 00 00 00 00 00 00 88",
		"03 FF 'F 01":    "0F 20 01 01 81 79 08 37 53 49 00 37 53 27 00 C3",
	}
	ramReplies := []string{
		"9C 7F 8F 00 00", "9C 7F 8F 00 00", "9C 7F 8F 00 00", "9C 7F 8F 00 00",
		"9C 7F 8F 00 00", "64 80 8F 00 00", "9C 7F 8F 00 00", "57 78 8F 00 01",
		"2F 7C 8F 00 00", "64 80 8F 00 00", "04 00 8F 00 80", "01 00 8F 00 80",
		"06 00 8F 00 80", "38 7F 8F 00 00",
	}
	for id, r := range ramReplies {
		script[fmt.Sprintf("05 FF 'W 36 %02X 00", id)] = "08 FF 'W 8E " + r
	}
	c, d := startDevice(t, nil, scripted(t, script))
	ctx := context.Background()

	v, err := c.Version(ctx)
	if err != nil || v.Version != 1130131 || v.Mode != 0 {
		t.Fatalf("Version = %+v, %v", v, err)
	}
	if err := c.SetAddress(ctx, 0); err != nil {
		t.Fatal(err)
	}
	ram := make([]RAMInfo, len(ramReplies))
	for id := range ram {
		if ram[id], err = c.RAMVarInfo(ctx, uint16(id)); err != nil {
			t.Fatalf("RAMVarInfo(%d): %v", id, err)
		}
		want := appendix2RAMInfo[id]
		if ram[id].Sc != want.sc || ram[id].Offset != want.offset {
			t.Errorf("RAMVarInfo(%d) = %+v, want sc=%d offset=%d", id, ram[id], want.sc, want.offset)
		}
	}

	// "raw current limit 500 [62 - 1000]", "active input: 0"
	m, err := c.MasterMultiLED(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.ActualInputLimit != 500 || m.MinInputLimit != 62 || m.MaxInputLimit != 1000 || m.ACInputConfig != 0 || m.HasSwitchRegister {
		t.Errorf("MasterMultiLED = %+v", m)
	}

	// "dc V: 26.4 V", "dc I: 0.0 A", "output frequency 50.0 Hz"
	dc, err := c.DCInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v := ram[4].Value(dc.Voltage); !near(v, 26.4, 0.05) {
		t.Errorf("dc V = %v", v)
	}
	if i := ram[5].Scale()*float64(dc.CurrentCharging) - ram[5].Scale()*float64(dc.CurrentInverting); i != 0 {
		t.Errorf("dc I = %v", i)
	}
	if f := 10 / ram[7].Value(uint16(dc.InverterPeriod)); !near(f, 50.0, 0.05) {
		t.Errorf("output frequency = %v", f)
	}

	// "input V: 213.0 V", "input I: 0.7 A", "inverter voltage 213.0 V",
	// "inverter current 0.4 A", "input period 50.1 Hz"
	ac, err := c.ACInfo(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ac.PhaseInfo != PhaseL1Of1 || ac.BFFactor != 1 || ac.InverterFactor != 1 {
		t.Errorf("ACInfo = %+v", ac)
	}
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"input V", ram[0].Value(ac.MainsVoltage), 213.0},
		{"input I", ram[1].Value(ac.MainsCurrent) * float64(ac.BFFactor), 0.7},
		{"inverter V", ram[2].Value(ac.InverterVoltage), 213.0},
		{"inverter I", ram[3].Value(ac.InverterCurrent) * float64(ac.InverterFactor), 0.4},
		{"input frequency", 10 / ram[8].Value(uint16(ac.MainsPeriod)), 50.1},
	}
	for _, ck := range checks {
		if !near(ck.got, ck.want, 0.05) {
			t.Errorf("%s = %v, want %v", ck.name, ck.got, ck.want)
		}
	}
	if n := len(d.requests()); n != 2+14+3 {
		t.Errorf("sent %d requests", n)
	}
}

func TestVersionModes(t *testing.T) {
	for _, tc := range []struct {
		wire string
		mode byte
	}{
		{"07 FF 'V 93 3E 11 00 'B", VersionModeNoAddress},
		{"07 FF 'V 93 3E 11 00 'W", VersionModeRS485},
		{"09 FF 'V 93 3E 11 00 02 AA BB", 2}, // extra bytes ignored (§3.2)
	} {
		f, err := Decode(withCS(t, tc.wire))
		if err != nil {
			t.Fatal(err)
		}
		v, err := ParseVersion(f)
		if err != nil || v.Mode != tc.mode || v.Version != 1130131 {
			t.Errorf("%s: %+v, %v", tc.wire, v, err)
		}
	}
	if _, err := ParseVersion(Frame{Command: 'V', Data: []byte{1, 2, 3}}); err == nil {
		t.Error("short V frame must fail")
	}
}

func TestLED(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		d.send(withCS(t, "05 FF 'L 11 01 00")) // old MK2 trailing 0x00
	})
	s, err := c.LED(context.Background())
	if err != nil || s.On != LEDMains|LEDInverter || s.Blink != LEDMains {
		t.Fatalf("LED = %+v, %v", s, err)
	}
	if !bytes.Equal(d.requests()[0], unhex(t, "02 FF 'L B3")) {
		t.Fatalf("request % X", d.requests()[0])
	}
}

func TestSetState(t *testing.T) {
	var replyLED bool
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		if replyLED {
			d.send(withCS(t, "84 FF 'S 01 00"))
			return
		}
		d.send(unhex(t, "02 FF 'S AC"))
	})
	ctx := context.Background()
	// Appendix 1: act as remote panel, switch on, 12 A of 16 A (variant 1).
	if err := c.SetState(ctx, State{Switch: SwitchOn, Pot: 0xC0, Scale: 0x10, Flags: StateFlagAutoSend}); err != nil {
		t.Fatal(err)
	}
	// Appendix 1: switch on, 31.5 A (variant 2).
	if err := c.SetState(ctx, State{Switch: SwitchOn, Limit: 315, Flags: StateFlagLimitDeciAmps}); err != nil {
		t.Fatal(err)
	}
	replyLED = true
	if err := c.SetState(ctx, State{Switch: SwitchOff, Flags: StateFlagAppendLED}); err != nil {
		t.Fatalf("LED-appended reply: %v", err)
	}
	reqs := d.requests()
	for i, want := range []string{"07 FF 'S 03 C0 10 01 01 D2", "07 FF 'S 03 3B 01 01 80 E7"} {
		if !bytes.Equal(reqs[i], unhex(t, want)) {
			t.Errorf("request %d = % X, want %s", i, reqs[i], want)
		}
	}
}

func TestSetStateSwitchesWinmonMode(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		switch req.Command {
		case 'S':
			d.send(unhex(t, "02 FF 'S AC"))
		case 'W':
			// Mode 2 software version: 0x82 with all four bytes.
			d.sendFrame(wReply(WReplySoftwareVersionPart0, 0x93, 0x3E, 0x11, 0x00, 0, 0))
		}
	})
	ctx := context.Background()
	if err := c.SetState(ctx, State{Switch: SwitchOn, Flags: StateFlagNoSend | StateFlagLimitDeciAmps, Limit: 0x8000, Ext: &StateExt{}}); err != nil {
		t.Fatal(err)
	}
	v, err := c.SoftwareVersion(ctx)
	if err != nil || v != 1130131 {
		t.Fatalf("SoftwareVersion = %d, %v", v, err)
	}
	reqs := d.requests()
	if want := withCS(t, "0A FF 'S 03 00 80 01 90 00 00 00"); !bytes.Equal(reqs[0], want) {
		t.Errorf("S = % X, want % X", reqs[0], want)
	}
	if len(reqs) != 2 {
		t.Errorf("mode 2 version must take one request, got %d", len(reqs)-1)
	}
}

func TestAddress(t *testing.T) {
	cur := byte(AddressNone)
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		action, addr := req.Data[0], req.Data[1]
		if action&1 != 0 {
			cur = addr
			if addr > MaxAddress {
				cur = AddressNone
			}
		}
		d.send(withCS(t, fmt.Sprintf("05 FF 'A %02X %02X 00", action, cur)))
	})
	ctx := context.Background()
	if a, err := c.Address(ctx); err != nil || a != AddressNone {
		t.Fatalf("Address = %#x, %v", a, err)
	}
	if err := c.SetAddress(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.requests()[1], unhex(t, "04 FF 'A 01 00 BB")) {
		t.Fatalf("request % X", d.requests()[1])
	}
	if err := c.SetAddress(ctx, 0x20); err == nil {
		t.Fatal("invalid address accepted")
	}
}

func TestDiscover(t *testing.T) {
	present := map[byte]bool{0: true, 1: true, 3: true}
	var cur byte
	var probed []byte
	c, _ := startDevice(t, &Options{Timeout: 20 * time.Millisecond}, func(d *device, req Frame) {
		switch req.Command {
		case 'A':
			cur = req.Data[1]
			probed = append(probed, cur)
			d.sendFrame(Frame{Command: 'A', Data: req.Data})
		case 'W':
			if present[cur] {
				d.sendFrame(wReply(WReplySoftwareVersionPart0, 1, 2))
			}
		}
	})
	found, err := c.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(found, []byte{0, 1, 3}) {
		t.Fatalf("found %v", found)
	}
	// Stops after 3 consecutive misses (4, 5, 6).
	if !bytes.Equal(probed, []byte{0, 1, 2, 3, 4, 5, 6}) {
		t.Fatalf("probed %v", probed)
	}
}

func TestReset(t *testing.T) {
	c, d := startDevice(t, nil, nil)
	if err := c.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitRequests(t, d, 1)
	// §3.6: 0x02 0xFF 'R' <checksum>.
	if !bytes.Equal(d.requests()[0], unhex(t, "02 FF 'R AD")) {
		t.Fatalf("request % X", d.requests()[0])
	}
}

func waitRequests(t *testing.T, d *device, n int) {
	t.Helper()
	for i := 0; i < 100 && len(d.requests()) < n; i++ {
		time.Sleep(time.Millisecond)
	}
	if len(d.requests()) < n {
		t.Fatalf("device saw %d requests, want %d", len(d.requests()), n)
	}
}

func TestACInfoPhases(t *testing.T) {
	c, _ := startDevice(t, nil, func(d *device, req Frame) {
		phase := req.Data[0]
		info := []byte{2, 3, 0, 9, PhaseL1Of1 + 1 - phase, 0x37, 0x53, 0x49, 0, 0x37, 0x53, 0x27, 0, 0xC3}
		if phase == 1 {
			info[4] = 0x0A // L1 of 3 phases
		}
		// A frame for another phase first: must be skipped.
		d.sendFrame(Frame{VEBus: true, Command: FrameInfo, Data: []byte{0, 0, 0, 0, PhaseDCFrame, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
		d.sendFrame(Frame{VEBus: true, Command: FrameInfo, Data: info})
	})
	for phase := 1; phase <= 4; phase++ {
		ac, err := c.ACInfo(context.Background(), phase)
		if err != nil {
			t.Fatalf("phase %d: %v", phase, err)
		}
		want := map[int]byte{1: 0x0A, 2: PhaseL2, 3: PhaseL3, 4: PhaseL4}[phase]
		if ac.PhaseInfo != want || ac.State != 9 || ac.BFFactor != 2 || ac.InverterFactor != 3 {
			t.Errorf("phase %d: %+v", phase, ac)
		}
	}
	if _, err := c.ACInfo(context.Background(), 5); err == nil {
		t.Error("phase 5 accepted")
	}
}

func TestNoReplyFrameCommands(t *testing.T) {
	c, d := startDevice(t, nil, nil)
	ctx := context.Background()
	if err := c.TakeSnapshot(ctx, 14, 15, 16); err != nil {
		t.Fatal(err)
	}
	if err := c.ResetDevice(ctx, 0x11223344); err != nil {
		t.Fatal(err)
	}
	if err := c.TakeSnapshot(ctx); err == nil {
		t.Error("empty snapshot accepted")
	}
	waitRequests(t, d, 2)
	reqs := d.requests()
	for i, want := range []string{"06 FF 'F 06 0E 0F 10", "07 FF 'F 08 22 11 44 33"} {
		if !bytes.Equal(reqs[i], withCS(t, want)) {
			t.Errorf("request %d = % X, want %s", i, reqs[i], want)
		}
	}
}

func TestSendBOL(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		// Echo the limits back in a BOL frame (0x3C).
		d.sendFrame(Frame{VEBus: true, Command: FrameBOL, Data: req.Data[1:]})
	})
	in := BOL{Flags: BOLUBatCharge | BOLIBatCharge | BOLUBatDischarge | BOLIBatDischarge,
		UBatCharge: 5520, IBatCharge: 0x012345, UBatDischarge: 4800, IBatDischarge: 2000}
	out, err := c.SendBOL(context.Background(), in)
	if err != nil || out != in {
		t.Fatalf("SendBOL = %+v, %v", out, err)
	}
	want := withCS(t, "0E FF 'F 09 0F 90 15 45 23 01 C0 12 D0 07 00")
	if !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X, want % X", d.requests()[0], want)
	}
	if b, err := ParseBOL([]byte{BOLIBatCharge, 1, 2}); err == nil {
		t.Errorf("short BOL parsed: %+v", b)
	}
	if b, err := ParseBOL([]byte{BOLInfoMissing}); err != nil || b.Flags != BOLInfoMissing {
		t.Errorf("BOL timeout reply: %+v, %v", b, err)
	}
}

func TestParseInfoRejects(t *testing.T) {
	short := Frame{VEBus: true, Command: FrameInfo, Data: make([]byte, 13)}
	if _, err := ParseDCInfo(short); err == nil {
		t.Error("short DC info accepted")
	}
	if _, err := ParseACInfo(short); err == nil {
		t.Error("short AC info accepted")
	}
	if _, err := ParseMasterMultiLED(Frame{VEBus: true, Command: FrameMasterMultiLED, Data: make([]byte, 10)}); err == nil {
		t.Error("short MasterMultiLED accepted")
	}
	m, err := ParseMasterMultiLED(Frame{VEBus: true, Command: FrameMasterMultiLED, Data: unhex(t, strings.Repeat("00 ", 11)+"30")})
	if err != nil || !m.HasSwitchRegister || m.SwitchRegister != 0x30 {
		t.Errorf("switch register: %+v, %v", m, err)
	}
}
