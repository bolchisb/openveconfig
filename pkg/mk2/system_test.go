package mk2

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// The frame vectors below are synthetic: computed from the checksum rule and
// the frame layouts, not captured from the original tools.

const testLongID = 0x12345678

var testRecord = [SystemRecordLen]byte{0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0xF0, 0x00}

// systemVectors are system frames as the host sends them.
var systemVectors = []struct {
	name   string
	longID uint32
	sub    byte
	args   []byte
	wire   string
}{
	{"reset, broadcast", LongIDAll, SysReset, nil, "06 21 00 00 00 00 03 C4"},
	{"keep-alive, broadcast", LongIDAll, SysKeepAlive, []byte{0x03, 0x14}, "08 21 00 00 00 00 90 03 14 1E"},
	{"identify request, broadcast", LongIDAll, SysIdentify, nil, "06 21 00 00 00 00 00 C7"},
	{"identification on, broadcast", LongIDAll, SysIdentifyMode, []byte{0x01}, "07 21 00 00 00 00 01 01 C4"},
	{"identification off, broadcast", LongIDAll, SysIdentifyMode, []byte{0x00}, "07 21 00 00 00 00 01 00 C5"},
	{"identification off, one unit", testLongID, SysIdentifyMode, []byte{0x00}, "07 21 78 56 34 12 01 00 2B"},
	{"read record", testLongID, SysReadConfig, nil, "06 21 78 56 34 12 05 28"},
	{"write record", testLongID, SysWriteConfig, testRecord[:], "0F 21 78 56 34 12 04 01 01 01 00 00 00 00 F0 00 2D"},
	{"flash LEDs on", testLongID, SysFlashLEDs, []byte{0x01, 0x0F, 0xFF}, "09 21 78 56 34 12 02 01 0F FF 19"},
	{"flash LEDs off", testLongID, SysFlashLEDs, []byte{0x00}, "07 21 78 56 34 12 02 00 2A"},
	{"freeze", testLongID, SysFreeze, nil, "06 21 78 56 34 12 06 27"},
}

// Replies as a unit sends them (plain checksum).
const (
	identifyReplyWire = "0D 20 78 56 34 12 01 00 FD FB 27 00 00 00 9F"
	readReplyWire     = "10 20 78 56 34 12 02 00 01 01 01 00 00 00 00 F0 00 C7"
)

var testIdentity = Identity{LongID: testLongID, Flag: IdentifyFlag, ShortAddress: 0, VersionWord: 2620413, Letter: 0, ConfigProtocol: 0}

func TestSystemFrameVectors(t *testing.T) {
	for _, tc := range systemVectors {
		t.Run(tc.name, func(t *testing.T) {
			wire := unhex(t, tc.wire)
			p := SystemPayload(tc.longID, tc.sub, tc.args...)
			if got := RawChecksum(p); got != wire[len(wire)-1] {
				t.Errorf("RawChecksum = %02X, want %02X", got, wire[len(wire)-1])
			}
			enc, err := EncodeRaw(p)
			if err != nil || !bytes.Equal(enc, wire) {
				t.Fatalf("EncodeRaw = % X, %v; want % X", enc, err, wire)
			}
			dec, err := DecodeRaw(wire)
			if err != nil || !bytes.Equal(dec, p) {
				t.Fatalf("DecodeRaw = % X, %v", dec, err)
			}
			id, sub, args, err := ParseSystemPayload(dec)
			if err != nil || id != tc.longID || sub != tc.sub || !bytes.Equal(args, tc.args) {
				t.Fatalf("ParseSystemPayload = %08X %02X % X, %v", id, sub, args, err)
			}
			// The plain rule does not hold, so the frame cannot be taken
			// for a reply, and Decode drops an echo of it.
			if _, err := Decode(wire); !errors.Is(err, ErrChecksum) {
				t.Errorf("Decode = %v, want ErrChecksum", err)
			}
		})
	}
}

func TestRawChecksumShortPayloads(t *testing.T) {
	// Up to 4 payload bytes the rule is the MK2 rule (§3.2); from 5 the
	// nibble-swapped bytes 0, 2 and 4 count too.
	for _, tc := range []struct {
		payload string
		want    byte
	}{
		{"21", 0xDE},
		{"01 02 03 04", 0xF2},
		{"01 02 03 04 05", 0x5C},
	} {
		p := unhex(t, tc.payload)
		if got := RawChecksum(p); got != tc.want {
			t.Errorf("RawChecksum(% X) = %02X, want %02X", p, got, tc.want)
		}
		if len(p) <= 4 && Checksum(append([]byte{byte(len(p))}, p...)) != tc.want {
			t.Errorf("% X: raw and MK2 rules differ", p)
		}
	}
}

func TestRawFrameErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrShortFrame},
		{"zero length", []byte{0x00, 0x00, 0x00}, ErrShortFrame},
		{"too short for length", unhex(t, "06 21 00 00 00 00 C4"), ErrFrameSize},
		{"too long for length", unhex(t, "06 21 00 00 00 00 03 C4 00"), ErrFrameSize},
		{"bad checksum", unhex(t, "06 21 00 00 00 00 03 C5"), ErrChecksum},
		{"length over MaxBody", append([]byte{0x80}, make([]byte, 0x81)...), ErrTooLong},
		{"MK2 frame", unhex(t, "02 FF 'L B3"), errRawMarker},
	} {
		if _, err := DecodeRaw(tc.b); !errors.Is(err, tc.want) {
			t.Errorf("%s: DecodeRaw err = %v, want %v", tc.name, err, tc.want)
		}
	}
	for _, tc := range []struct {
		name string
		p    []byte
		want error
	}{
		{"empty", nil, ErrShortFrame},
		{"too long", make([]byte, MaxBody+1), ErrTooLong},
		{"marker", []byte{0xFF, 0x00}, errRawMarker},
	} {
		if _, err := EncodeRaw(tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: EncodeRaw err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if b, err := EncodeRaw(make([]byte, MaxBody)); err != nil || len(b) != MaxBody+2 {
		t.Errorf("max raw frame: len %d, %v", len(b), err)
	}
	if _, _, _, err := ParseSystemPayload(unhex(t, "22 00 00 00 00 03")); err == nil {
		t.Error("ParseSystemPayload accepted a payload without '!'")
	}
	if _, _, _, err := ParseSystemPayload(unhex(t, "21 00 00 00 00")); err == nil {
		t.Error("ParseSystemPayload accepted a payload without sub-command")
	}
}

func TestScanHostFrames(t *testing.T) {
	v := unhex(t, "02 FF 'V A9")
	reset := unhex(t, "06 21 00 00 00 00 03 C4")
	a := unhex(t, "04 FF 'A 01 00 BB")
	read := unhex(t, "06 21 78 56 34 12 05 28")
	var in []byte
	in = append(in, "UUUUU"...)
	in = append(in, v...)
	in = append(in, reset...)
	in = append(in, 0x00)                                   // line noise
	in = append(in, unhex(t, "06 21 00 00 00 00 03 C5")...) // corrupt raw frame
	in = append(in, a...)
	in = append(in, read...)
	got := scanAll(t, ScanHostFrames, in)
	want := [][]byte{v, reset, a, read}
	if len(got) != len(want) {
		t.Fatalf("got %d frames: % X", len(got), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("frame %d = % X, want % X", i, got[i], want[i])
		}
	}
	// ScanMK2Frames is unchanged: it skips raw frames.
	if got := scanAll(t, ScanMK2Frames, in); len(got) != 2 || !bytes.Equal(got[0], v) || !bytes.Equal(got[1], a) {
		t.Errorf("ScanMK2Frames = % X", got)
	}
}

func TestParseIdentify(t *testing.T) {
	f, err := Decode(unhex(t, identifyReplyWire))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseIdentify(f); err != nil || got != testIdentity {
		t.Fatalf("ParseIdentify(vector) = %+v, %v", got, err)
	}

	reply := func(flag byte, version uint32, tail ...byte) Frame {
		d := binary.LittleEndian.AppendUint32(nil, testLongID)
		d = append(d, flag, 0x05)
		d = binary.LittleEndian.AppendUint32(d, version)
		return Frame{VEBus: true, Command: FrameSystem, Data: append(d, tail...)}
	}
	const v553 = 0x01_000000 | 2620553 // sub-version 1, firmware ...553
	for _, tc := range []struct {
		name           string
		f              Frame
		want           error
		letter, config byte
	}{
		{"letter and config", reply(IdentifyFlag, 2620413, 3, 2), nil, 3, 2},
		{"no letter, no config", reply(IdentifyFlag, 2620413), nil, LetterUnknown, 0},
		{"letter only", reply(IdentifyFlag, 2620413, 7), nil, 7, 0},
		{"extra bytes ignored", reply(IdentifyFlag, 2620413, 3, 2, 9, 9), nil, 3, 2},
		{"firmware ...553 forces config 0", reply(IdentifyFlag, v553, 1, 2), nil, 1, 0},
		{"flag 0x11 parses", reply(IdentifyFlagAlt, 2620413, 0, 1), nil, 0, 1},
		{"short", Frame{VEBus: true, Command: FrameSystem, Data: reply(IdentifyFlag, 2620413).Data[:9]}, ErrShortReply, 0, 0},
		{"other flag", reply(0x02, 2620413, 0, 0), ErrUnexpectedReply, 0, 0},
		{"too short to tell", Frame{VEBus: true, Command: FrameSystem, Data: []byte{1, 2, 3, 4}}, ErrUnexpectedReply, 0, 0},
		{"MK2 frame", Frame{Command: FrameSystem, Data: reply(IdentifyFlag, 2620413).Data}, ErrUnexpectedReply, 0, 0},
		{"other VE.Bus type", Frame{VEBus: true, Command: FramePanel, Data: reply(IdentifyFlag, 2620413).Data}, ErrUnexpectedReply, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIdentify(tc.f)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if err != nil {
				return
			}
			if got.LongID != testLongID || got.ShortAddress != 5 || got.Flag != tc.f.Data[4] ||
				got.VersionWord != binary.LittleEndian.Uint32(tc.f.Data[6:]) || got.Letter != tc.letter || got.ConfigProtocol != tc.config {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestParseSystemConfig(t *testing.T) {
	f, err := Decode(unhex(t, readReplyWire))
	if err != nil {
		t.Fatal(err)
	}
	want := SystemConfig{LongID: testLongID, ResponseChar: 0x02, Record: testRecord}
	if got, err := ParseSystemConfig(f, testLongID); err != nil || got != want {
		t.Fatalf("ParseSystemConfig(vector) = %+v, %v", got, err)
	}
	if _, err := ParseSystemConfig(f, testLongID+1); !errors.Is(err, ErrUnexpectedReply) {
		t.Errorf("other unit: err = %v", err)
	}
	for c := 0; c < 0x100; c++ {
		r := SystemConfig{LongID: testLongID, ResponseChar: byte(c), Record: testRecord}
		_, err := ParseSystemConfig(r.Frame(), testLongID)
		ok := c >= 0x02 && c <= 0x04 || c >= 0x20 && c <= 0x2F
		if ok && err != nil || !ok && !errors.Is(err, ErrUnexpectedReply) {
			t.Errorf("response char 0x%02X: err = %v", c, err)
		}
	}
	for _, n := range []int{4, 14} {
		short := Frame{VEBus: true, Command: FrameSystem, Data: f.Data[:n]}
		if _, err := ParseSystemConfig(short, testLongID); !errors.Is(err, ErrShortReply) {
			t.Errorf("%d data bytes: err = %v", n, err)
		}
	}
}

func TestSystemRepliesRoundTrip(t *testing.T) {
	for _, id := range []Identity{testIdentity, {LongID: 0xFFFFFFFE, Flag: IdentifyFlagAlt, ShortAddress: 31, VersionWord: 0xAB123456, Letter: 25, ConfigProtocol: 1}} {
		b, err := id.Frame().Encode()
		if err != nil {
			t.Fatal(err)
		}
		f, err := Decode(b)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := ParseIdentify(f); err != nil || got != id {
			t.Errorf("identity round trip %+v -> %+v, %v", id, got, err)
		}
	}
	if b, _ := testIdentity.Frame().Encode(); !bytes.Equal(b, unhex(t, identifyReplyWire)) {
		t.Errorf("Identity.Frame = % X", b)
	}
	sc := SystemConfig{LongID: testLongID, ResponseChar: 0x02, Record: testRecord}
	b, _ := sc.Frame().Encode()
	if !bytes.Equal(b, unhex(t, readReplyWire)) {
		t.Errorf("SystemConfig.Frame = % X", b)
	}
}

func TestInfoFramesAreNotSystemReplies(t *testing.T) {
	// Info frames (§6.1) share frame type 0x20 with system replies; byte 5
	// tells them apart.
	for _, s := range []string{
		"0F 20 7F 9A 81 79 0C 51 0A 00 00 00 00 00 00 88", // DC
		"0F 20 01 01 81 79 08 37 53 49 00 37 53 27 00 C3", // AC L1
		"0F 20 01 01 81 79 05 37 53 49 00 37 53 27 00 C3", // AC L4
	} {
		f, err := Decode(withCS(t, s))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseIdentify(f); err == nil {
			t.Errorf("%s: ParseIdentify accepted an info frame", s)
		}
		// Even when bytes 1-4 equal the requested long ID.
		id := binary.LittleEndian.Uint32(f.Data)
		if _, err := ParseSystemConfig(f, id); err == nil {
			t.Errorf("%s: ParseSystemConfig accepted an info frame", s)
		}
		if fromUnit(f, id) {
			t.Errorf("%s: info frame counted as a reply", s)
		}
	}
	for _, s := range []string{identifyReplyWire, readReplyWire} {
		f, _ := Decode(unhex(t, s))
		if _, err := ParseDCInfo(f); err == nil {
			t.Errorf("%s: ParseDCInfo accepted a system reply", s)
		}
		if _, err := ParseACInfo(f); err == nil {
			t.Errorf("%s: ParseACInfo accepted a system reply", s)
		}
	}
}

func TestSystemLinkStateFrame(t *testing.T) {
	// 7 data bytes, no EEPROMFlags; Flags bit 4 keeps the switch value 4
	// (off) from being applied.
	b, err := systemLinkState.Frame().Encode()
	if err != nil || !bytes.Equal(b, unhex(t, "09 FF 53 04 00 FF 01 10 00 04 8D")) {
		t.Fatalf("S = % X, %v", b, err)
	}
	// Without OmitEEPROMFlags the frame keeps its 8 data bytes.
	s := systemLinkState
	s.Ext = &StateExt{FlagsExt0: FlagsExt0ForwardAllConfig, EEPROMFlags: EEPROMFlagListenOnly}
	if b, _ := s.Frame().Encode(); !bytes.Equal(b, withCS(t, "0A FF 53 04 00 FF 01 10 00 04 02")) {
		t.Fatalf("S with EEPROMFlags = % X", b)
	}
}

// bus is a scripted MK in VE.Bus mode on the far end of a net.Pipe. It
// answers 'V', 'A' and 'S' itself and passes system frames to handle.
type bus struct {
	t    *testing.T
	conn net.Conn

	mu    sync.Mutex
	reqs  [][]byte // request frames, in order
	times []time.Time
}

// startBus connects a Client to a bus whose 'V' frames carry mode. It
// shortens systemTiming for the test.
func startBus(t *testing.T, opts *Options, mode byte, handle func(b *bus, longID uint32, sub byte, args []byte)) (*Client, *bus) {
	t.Helper()
	saved := systemTiming
	systemTiming.listen = 20 * time.Millisecond
	systemTiming.spacing = 5 * time.Millisecond
	t.Cleanup(func() { systemTiming = saved })

	host, dev := net.Pipe()
	b := &bus{t: t, conn: dev}
	go func() {
		sc := bufio.NewScanner(dev)
		sc.Split(ScanHostFrames)
		for sc.Scan() {
			raw := bytes.Clone(sc.Bytes())
			b.mu.Lock()
			b.reqs = append(b.reqs, raw)
			b.times = append(b.times, time.Now())
			b.mu.Unlock()
			if p, err := DecodeRaw(raw); err == nil {
				id, sub, args, err := ParseSystemPayload(p)
				if err != nil {
					t.Errorf("bus: bad system frame % X", raw)
				} else if handle != nil {
					handle(b, id, sub, args)
				}
				continue
			}
			f, err := Decode(raw)
			if err != nil {
				t.Errorf("bus: bad request % X: %v", raw, err)
				continue
			}
			switch f.Command {
			case CmdVersion:
				b.sendFrame(Frame{Command: CmdVersion, Data: []byte{0x93, 0x3E, 0x11, 0x00, mode}})
			case CmdAddress:
				b.sendFrame(Frame{Command: CmdAddress, Data: f.Data[:2]})
			case CmdState:
				b.send(unhex(t, "02 FF 'S AC"))
			}
		}
		dev.Close()
	}()
	if opts == nil {
		opts = &Options{}
	}
	if opts.Timeout == 0 {
		opts.Timeout = 200 * time.Millisecond
	}
	if opts.RawTimeout == 0 {
		opts.RawTimeout = 100 * time.Millisecond
	}
	c := NewClient(host, opts)
	t.Cleanup(func() { c.Close() })
	return c, b
}

func (b *bus) send(frames ...[]byte) {
	for _, f := range frames {
		if _, err := b.conn.Write(f); err != nil {
			return // client closed
		}
	}
}

func (b *bus) sendFrame(f Frame) {
	w, err := f.Encode()
	if err != nil {
		b.t.Errorf("bus: %v", err)
		return
	}
	b.send(w)
}

func (b *bus) requests() [][]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([][]byte(nil), b.reqs...)
}

// waitRequests waits until the bus has seen n requests and returns them.
func (b *bus) waitRequests(n int) [][]byte {
	b.t.Helper()
	for deadline := time.Now().Add(time.Second); ; {
		r := b.requests()
		if len(r) >= n || time.Now().After(deadline) {
			if len(r) < n {
				b.t.Fatalf("bus saw %d requests, want %d", len(r), n)
			}
			return r
		}
		time.Sleep(time.Millisecond)
	}
}

// prepared returns a Client on a VE.Bus-mode bus after PrepareSystemLink,
// and the number of requests that took.
func prepared(t *testing.T, opts *Options, handle func(b *bus, longID uint32, sub byte, args []byte)) (*Client, *bus, int) {
	t.Helper()
	c, b := startBus(t, opts, VersionModeNoAddress, handle)
	if err := c.PrepareSystemLink(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, b, len(b.requests())
}

func TestPrepareSystemLink(t *testing.T) {
	c, b, n := prepared(t, nil, nil)
	reqs := b.requests()
	want := []string{"02 FF 'V A9", "04 FF 'A 01 00 BB", "09 FF 53 04 00 FF 01 10 00 04 8D"}
	if n != len(want) {
		t.Fatalf("requests = % X", reqs)
	}
	for i, w := range want {
		if !bytes.Equal(reqs[i], unhex(t, w)) {
			t.Errorf("request %d = % X, want %s", i, reqs[i], w)
		}
	}
	if m := c.WinmonMode(); m != WinmonLong {
		t.Errorf("WinmonMode = %d", m)
	}
}

func TestSystemLinkMode(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(c *Client) error{
		"SetIdentification": func(c *Client) error { return c.SetIdentification(ctx, testLongID, true) },
		"Identify":          func(c *Client) error { _, err := c.Identify(ctx); return err },
		"FlashLEDs":         func(c *Client) error { return c.FlashLEDs(ctx, testLongID, true) },
		"ReadSystemConfig":  func(c *Client) error { _, err := c.ReadSystemConfig(ctx, testLongID); return err },
		"WriteSystemConfig": func(c *Client) error { return c.WriteSystemConfig(ctx, testLongID, testRecord) },
		"ResetSystem":       func(c *Client) error { return c.ResetSystem(ctx) },
		"Freeze":            func(c *Client) error { return c.Freeze(ctx, testLongID) },
		"KeepAlive":         func(c *Client) error { return c.KeepAlive(ctx) },
	}
	check := func(t *testing.T, c *Client, b *bus, sent int) {
		t.Helper()
		for name, call := range calls {
			if err := call(c); !errors.Is(err, ErrLinkMode) {
				t.Errorf("%s: err = %v, want ErrLinkMode", name, err)
			}
		}
		time.Sleep(20 * time.Millisecond)
		if r := b.requests(); len(r) != sent {
			t.Errorf("sent % X, want %d frames", r, sent)
		}
	}

	t.Run("no V seen", func(t *testing.T) {
		c, b := startBus(t, nil, VersionModeNoAddress, nil)
		check(t, c, b, 0)
	})
	t.Run("9-bit mode", func(t *testing.T) {
		c, b := startBus(t, nil, VersionModeRS485, nil)
		if err := c.PrepareSystemLink(ctx); !errors.Is(err, ErrLinkMode) {
			t.Fatalf("PrepareSystemLink: %v", err)
		}
		check(t, c, b, 1) // only 'V'
	})
	t.Run("after Reset", func(t *testing.T) {
		c, b, n := prepared(t, nil, nil)
		if err := c.Reset(ctx); err != nil {
			t.Fatal(err)
		}
		check(t, c, b, n+1) // 'R'
	})
}

func TestSystemCommandFrames(t *testing.T) {
	ctx := context.Background()
	wire := func(name string) string {
		for _, v := range systemVectors {
			if v.name == name {
				return v.wire
			}
		}
		t.Fatalf("no vector %q", name)
		return ""
	}
	for _, tc := range []struct {
		call func(c *Client) error
		want string
	}{
		{func(c *Client) error { return c.ResetSystem(ctx) }, "reset, broadcast"},
		{func(c *Client) error { return c.KeepAlive(ctx) }, "keep-alive, broadcast"},
		{func(c *Client) error { return c.SetIdentification(ctx, LongIDAll, true) }, "identification on, broadcast"},
		{func(c *Client) error { return c.SetIdentification(ctx, LongIDAll, false) }, "identification off, broadcast"},
		{func(c *Client) error { return c.SetIdentification(ctx, testLongID, false) }, "identification off, one unit"},
		{func(c *Client) error { return c.WriteSystemConfig(ctx, testLongID, testRecord) }, "write record"},
		{func(c *Client) error { return c.FlashLEDs(ctx, testLongID, true) }, "flash LEDs on"},
		{func(c *Client) error { return c.FlashLEDs(ctx, testLongID, false) }, "flash LEDs off"},
		{func(c *Client) error { return c.Freeze(ctx, testLongID) }, "freeze"},
		{func(c *Client) error { _, _ = c.ReadSystemConfig(ctx, testLongID); return nil }, "read record"},
		{func(c *Client) error { _, _ = c.Identify(ctx); return nil }, "identify request, broadcast"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			c, b, n := prepared(t, nil, nil)
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			reqs := b.waitRequests(n + 1)
			if len(reqs) != n+1 || !bytes.Equal(reqs[n], unhex(t, wire(tc.want))) {
				t.Fatalf("sent % X, want %s", reqs[n:], wire(tc.want))
			}
		})
	}
}

func TestReadSystemConfig(t *testing.T) {
	c, _, _ := prepared(t, nil, func(b *bus, id uint32, sub byte, args []byte) {
		if sub != SysReadConfig {
			return
		}
		// An info frame, a reply from another unit and an MK2 frame come
		// first; they are not the reply.
		b.send(withCS(t, "0F 20 78 56 34 12 08 37 53 49 00 37 53 27 00 C3"))
		b.sendFrame(SystemConfig{LongID: id + 1, ResponseChar: 0x03}.Frame())
		b.send(withCS(t, "07 FF 'V 93 3E 11 00 00"))
		b.send(unhex(t, readReplyWire))
	})
	got, err := c.ReadSystemConfig(context.Background(), testLongID)
	if want := (SystemConfig{LongID: testLongID, ResponseChar: 0x02, Record: testRecord}); err != nil || got != want {
		t.Fatalf("ReadSystemConfig = %+v, %v", got, err)
	}
}

func TestSystemReplyErrors(t *testing.T) {
	good := unhex(t, readReplyWire)
	bad := bytes.Clone(good)
	bad[len(bad)-1] ^= 0x01
	short := withCS(t, "0A 20 78 56 34 12 02 00 01 01 01")
	odd := SystemConfig{LongID: testLongID, ResponseChar: 0x10, Record: testRecord}.Frame()
	for _, tc := range []struct {
		name  string
		reply func(b *bus)
		want  error
	}{
		{"no answer", func(*bus) {}, ErrTimeout},
		{"bad checksum", func(b *bus) { b.send(bad) }, ErrChecksum},
		{"truncated frame", func(b *bus) { b.send(good[:10]) }, ErrFrameSize},
		{"short reply", func(b *bus) { b.send(short) }, ErrShortReply},
		{"unexpected response char", func(b *bus) { b.sendFrame(odd) }, ErrUnexpectedReply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			c, _, _ := prepared(t, nil, func(b *bus, id uint32, sub byte, args []byte) {
				if n++; n == 1 {
					tc.reply(b)
				} else {
					b.send(good)
				}
			})
			ctx := context.Background()
			_, err := c.ReadSystemConfig(ctx, testLongID)
			if !errors.Is(err, ErrTimeout) || !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want ErrTimeout and %v", err, tc.want)
			}
			// The next request still succeeds.
			if got, err := c.ReadSystemConfig(ctx, testLongID); err != nil || got.Record != testRecord {
				t.Fatalf("next request: %+v, %v", got, err)
			}
		})
	}
}

func TestSystemRetries(t *testing.T) {
	n := 0
	c, b, sent := prepared(t, &Options{Retries: 1}, func(b *bus, id uint32, sub byte, args []byte) {
		if n++; n == 2 {
			b.send(unhex(t, readReplyWire))
		}
	})
	if _, err := c.ReadSystemConfig(context.Background(), testLongID); err != nil {
		t.Fatal(err)
	}
	if r := b.requests(); len(r) != sent+2 {
		t.Fatalf("requests after prepare = %d, want 2", len(r)-sent)
	}
}

func TestIdentify(t *testing.T) {
	c, b, n := prepared(t, nil, func(b *bus, id uint32, sub byte, args []byte) {
		if sub == SysIdentify && id == LongIDAll {
			b.send(unhex(t, identifyReplyWire))
		}
	})
	got, err := c.Identify(context.Background())
	if err != nil || got != testIdentity {
		t.Fatalf("Identify = %+v, %v", got, err)
	}
	if r := b.requests(); len(r) != n+1 || !bytes.Equal(r[n], unhex(t, "06 21 00 00 00 00 00 C7")) {
		t.Fatalf("requests % X", r[n:])
	}
}

func TestIdentifyListensFirst(t *testing.T) {
	c, b, n := prepared(t, nil, nil)
	systemTiming.listen = 300 * time.Millisecond
	// A unit answers on its own (for example after identification was
	// switched on); Identify takes that reply without asking.
	b.send(unhex(t, identifyReplyWire))
	got, err := c.Identify(context.Background())
	if err != nil || got != testIdentity {
		t.Fatalf("Identify = %+v, %v", got, err)
	}
	if r := b.requests(); len(r) != n {
		t.Fatalf("Identify sent % X", r[n:])
	}
}

func TestIdentifyRejects(t *testing.T) {
	alt := testIdentity
	alt.Flag = IdentifyFlagAlt
	short := Frame{VEBus: true, Command: FrameSystem, Data: testIdentity.Frame().Data[:9]}
	for _, tc := range []struct {
		name   string
		reply  Frame
		accept bool
		want   error
	}{
		{"flag 0x11", alt.Frame(), false, ErrUnexpectedReply},
		{"short", short, false, ErrShortReply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle := func(b *bus, id uint32, sub byte, args []byte) {
				if sub == SysIdentify {
					b.sendFrame(tc.reply)
				}
			}
			c, _, _ := prepared(t, nil, handle)
			if _, err := c.Identify(context.Background()); !errors.Is(err, ErrTimeout) || !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want ErrTimeout and %v", err, tc.want)
			}
		})
	}
	// Options.AcceptIdentify11 accepts flag 0x11.
	c, _, _ := prepared(t, &Options{AcceptIdentify11: true}, func(b *bus, id uint32, sub byte, args []byte) {
		if sub == SysIdentify {
			b.sendFrame(alt.Frame())
		}
	})
	if got, err := c.Identify(context.Background()); err != nil || got != alt {
		t.Fatalf("with AcceptIdentify11: %+v, %v", got, err)
	}
}

func TestSystemSpacing(t *testing.T) {
	c, b, n := prepared(t, nil, nil)
	systemTiming.spacing = 50 * time.Millisecond
	ctx := context.Background()
	if err := c.ResetSystem(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Version(ctx); err != nil {
		t.Fatal(err)
	}
	b.waitRequests(n + 2)
	b.mu.Lock()
	gap := b.times[n+1].Sub(b.times[n])
	b.mu.Unlock()
	if gap < 50*time.Millisecond {
		t.Fatalf("next frame %v after a no-reply system frame", gap)
	}
}

func TestStartKeepAlive(t *testing.T) {
	c, b, n := prepared(t, nil, nil)
	stop := c.StartKeepAlive(20 * time.Millisecond)
	reqs := b.waitRequests(n + 3)
	stop()
	for _, r := range reqs[n:] {
		if !bytes.Equal(r, unhex(t, "08 21 00 00 00 00 90 03 14 1E")) {
			t.Fatalf("keep-alive frame % X", r)
		}
	}
	after := len(b.requests())
	time.Sleep(60 * time.Millisecond)
	if len(b.requests()) != after {
		t.Fatal("keep-alive sent after stop")
	}
	// Other requests still get through while it runs.
	stop = c.StartKeepAlive(5 * time.Millisecond)
	defer stop()
	for range 5 {
		if _, err := c.Version(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
