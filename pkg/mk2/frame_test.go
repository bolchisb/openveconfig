package mk2

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// unhex parses space-separated hex bytes; 'c' stands for the ASCII byte of
// the next character, so "04 FF 'A 01" is 04 FF 41 01.
func unhex(t testing.TB, s string) []byte {
	t.Helper()
	var b []byte
	for _, f := range strings.Fields(s) {
		if f[0] == '\'' {
			b = append(b, f[1])
			continue
		}
		v, err := hex.DecodeString(f)
		if err != nil {
			t.Fatalf("bad hex %q: %v", f, err)
		}
		b = append(b, v...)
	}
	return b
}

// withCS appends the checksum to a frame given without one, as in the
// Appendix 2 trace ("checksums are not shown in the dump").
func withCS(t testing.TB, s string) []byte {
	b := unhex(t, s)
	return append(b, Checksum(b))
}

// Appendix 1 frames, whose checksums are given in the spec.
var appendix1 = []struct {
	name    string
	wire    string
	cmd     byte
	data    string
	vebus   bool
	hasLED  bool
	ledOn   byte
	ledBlnk byte
}{
	{name: "A set address 0", wire: "04 FF 'A 01 00 BB", cmd: 'A', data: "01 00"},
	{name: "A reply, old MK2 trailing 0", wire: "05 FF 'A 01 00 00 BA", cmd: 'A', data: "01 00 00"},
	{name: "W software version part 0", wire: "05 FF 'W 05 00 00 A0", cmd: 'W', data: "05 00 00"},
	{name: "W software version part 1", wire: "05 FF 'W 06 00 00 9F", cmd: 'W', data: "06 00 00"},
	{name: "W RAM info UBat", wire: "05 FF 'W 36 04 00 6B", cmd: 'W', data: "36 04 00"},
	{name: "W read RAM charge state", wire: "05 FF 'W 30 0D 00 68", cmd: 'W', data: "30 0D 00"},
	{name: "F DC info", wire: "03 FF 'F 00 B8", cmd: 'F', data: "00"},
	{name: "L", wire: "02 FF 'L B3", cmd: 'L', data: ""},
	{name: "S variant 1", wire: "07 FF 'S 03 C0 10 01 01 D2", cmd: 'S', data: "03 C0 10 01 01"},
	{name: "S reply", wire: "02 FF 'S AC", cmd: 'S', data: ""},
	{name: "S variant 2", wire: "07 FF 'S 03 3B 01 01 80 E7", cmd: 'S', data: "03 3B 01 01 80"},
}

func TestAppendix1Frames(t *testing.T) {
	for _, tc := range appendix1 {
		t.Run(tc.name, func(t *testing.T) {
			wire := unhex(t, tc.wire)
			if got := Checksum(wire[:len(wire)-1]); got != wire[len(wire)-1] {
				t.Fatalf("Checksum = %02X, spec says %02X", got, wire[len(wire)-1])
			}
			f, err := Decode(wire)
			if err != nil {
				t.Fatal(err)
			}
			if f.VEBus || f.Command != tc.cmd || !bytes.Equal(f.Data, unhex(t, tc.data)) || f.LED != nil {
				t.Fatalf("Decode = %+v", f)
			}
			enc, err := f.Encode()
			if err != nil || !bytes.Equal(enc, wire) {
				t.Fatalf("Encode = % X, %v; want % X", enc, err, wire)
			}
		})
	}
}

func TestLEDAppended(t *testing.T) {
	// Appendix 1: with <Flags[1]> set, 'S' is answered by 84 FF 'S' XX XX.
	wire := withCS(t, "84 FF 'S 1F 02")
	f, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if f.Command != 'S' || len(f.Data) != 0 || f.LED == nil || *f.LED != (LEDStatus{On: 0x1F, Blink: 0x02}) {
		t.Fatalf("Decode = %+v led %+v", f, f.LED)
	}
	enc, _ := f.Encode()
	if !bytes.Equal(enc, wire) {
		t.Fatalf("Encode = % X, want % X", enc, wire)
	}
}

func TestVEBusFrame(t *testing.T) {
	// Appendix 2: reply to 'F' 5, a VE.Bus frame (no 0xFF).
	wire := withCS(t, "0C 41 10 09 00 00 00 3E 00 E8 03 F4 01")
	f, err := Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !f.VEBus || f.Command != FrameMasterMultiLED || len(f.Data) != 11 {
		t.Fatalf("Decode = %+v", f)
	}
	enc, _ := f.Encode()
	if !bytes.Equal(enc, wire) {
		t.Fatalf("Encode = % X, want % X", enc, wire)
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrShortFrame},
		{"two bytes", []byte{0x01, 0xFF}, ErrShortFrame},
		{"zero length", []byte{0x00, 0x00, 0x00}, ErrShortFrame},
		{"too short for length", unhex(t, "05 FF 'W 05 A0"), ErrFrameSize},
		{"too long for length", unhex(t, "02 FF 'L B3 00"), ErrFrameSize},
		{"bad checksum", unhex(t, "02 FF 'L B4"), ErrChecksum},
		{"marker without command", withCS(t, "01 FF"), ErrShortFrame},
		{"LED flag without LED bytes", withCS(t, "83 FF 'S 01"), ErrShortFrame},
	}
	for _, tc := range tests {
		if _, err := Decode(tc.b); !errors.Is(err, tc.want) {
			t.Errorf("%s: Decode err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestEncodeTooLong(t *testing.T) {
	if _, err := (Frame{Command: 'W', Data: make([]byte, 126)}).Encode(); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v", err)
	}
	if b, err := (Frame{Command: 'W', Data: make([]byte, 126), VEBus: true}).Encode(); err != nil || len(b) != 129 {
		t.Fatalf("max VE.Bus frame: len %d, %v", len(b), err)
	}
}

func scanAll(t *testing.T, split func([]byte, bool) (int, []byte, error), in []byte) [][]byte {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(in))
	sc.Split(split)
	var out [][]byte
	for sc.Scan() {
		out = append(out, bytes.Clone(sc.Bytes()))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestScanFramesResync(t *testing.T) {
	v := withCS(t, "07 FF 'V 93 3E 11 00 00")
	dc := withCS(t, "0F 20 7F 9A 81 79 0C 51 0A 00 00 00 00 00 00 88")
	l := unhex(t, "02 FF 'L B3")
	var in []byte
	in = append(in, 0x00, 0x13, 0xFF) // line noise
	in = append(in, v...)
	in = append(in, dc...)
	in = append(in, 0x02, 0xFF, 'L', 0x00) // corrupt frame
	in = append(in, l...)
	in = append(in, 0x05, 0xFF) // truncated at EOF
	got := scanAll(t, ScanFrames, in)
	want := [][]byte{v, dc, l}
	if len(got) != len(want) {
		t.Fatalf("got %d frames: % X", len(got), got)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("frame %d = % X, want % X", i, got[i], want[i])
		}
	}
}

func TestScanMK2FramesSkipsSync(t *testing.T) {
	a := unhex(t, "04 FF 'A 01 00 BB")
	in := append([]byte("UUUUU"), a...)
	in = append(in, withCS(t, "03 20 01 02")...) // VE.Bus frame: not accepted
	got := scanAll(t, ScanMK2Frames, in)
	if len(got) != 1 || !bytes.Equal(got[0], a) {
		t.Fatalf("got % X", got)
	}
}

func FuzzDecode(f *testing.F) {
	for _, tc := range appendix1 {
		f.Add(unhex(f, tc.wire))
	}
	f.Add(withCS(f, "84 FF 'S 1F 02"))
	f.Add(withCS(f, "0F 20 01 01 81 79 08 37 53 49 00 37 53 27 00 C3"))
	f.Add([]byte("UUUUU\x02\xffL\xb3"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if fr, err := Decode(b); err == nil {
			// A decoded frame re-encodes to the same bytes.
			enc, err := fr.Encode()
			if err != nil || !bytes.Equal(enc, b) {
				t.Fatalf("round trip: % X -> %+v -> % X, %v", b, fr, enc, err)
			}
		}
		// The stream splitters never panic, and every token they return
		// is a complete frame with a valid checksum.
		for _, split := range []func([]byte, bool) (int, []byte, error){ScanFrames, ScanMK2Frames} {
			sc := bufio.NewScanner(bytes.NewReader(b))
			sc.Split(split)
			for sc.Scan() {
				tok := sc.Bytes()
				if len(tok) < 3 || len(tok) != int(tok[0]&^LengthLEDFlag)+2 {
					t.Fatalf("bad token % X", tok)
				}
				var sum byte
				for _, c := range tok {
					sum += c
				}
				if sum != 0 {
					t.Fatalf("token with bad checksum % X", tok)
				}
			}
		}
	})
}
