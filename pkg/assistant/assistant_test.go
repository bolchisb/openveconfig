package assistant

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

// The areas are synthetic: their bytes are invented, in the shapes the
// files show (#7).

func TestExpandCompactShapes(t *testing.T) {
	for name, tt := range map[string]struct {
		compact []byte
		lit     int // expected literal bytes before the fill
	}{
		// An empty literal, then 0xFF × 2816 (kylehart's "bare" area).
		"bare": {[]byte{0x00, 0x00, 0xFF, 0x00, 0x0B}, 0},
		// A short header, then 0xFF to the end (a device file).
		"header": {[]byte{0x06, 0x00, 0x11, 0x22, 0x00, 0x00, 0x33, 0x01, 0xFF, 0xFA, 0x0A}, 6},
	} {
		img, err := Expand(tt.compact)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(img) != ImageSize || !bytes.Equal(img[tt.lit:], bytes.Repeat([]byte{0xFF}, ImageSize-tt.lit)) {
			t.Errorf("%s: %d bytes, % X…", name, len(img), img[:min(len(img), 8)])
		}
		if got := Compact(img); !bytes.Equal(got, tt.compact) {
			t.Errorf("%s: compacted to % X, want % X", name, got, tt.compact)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	ramp := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*7 + 1)
		}
		return b
	}
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	same := func(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }
	for name, img := range map[string][]byte{
		"no runs":            ramp(100),
		"run at the start":   cat(same(0, 10), ramp(20)),
		"run at the end":     cat(ramp(20), same(0xFF, 30)),
		"five equal stay":    cat(ramp(3), same(9, 5), ramp(3)),
		"six equal fill":     cat(ramp(3), same(9, 6), ramp(3)),
		"two runs":           cat(same(1, 8), same(2, 8), ramp(4)),
		"long run":           same(0xFF, 70000),
		"long literal":       ramp(70000),
		"empty":              {},
		"device-sized image": cat(ramp(6), same(0xFF, ImageSize-6)),
	} {
		got, err := Expand(Compact(img))
		if err != nil || !bytes.Equal(got, img) {
			t.Errorf("%s: %v, %d bytes back of %d", name, err, len(got), len(img))
		}
	}
	// Six equal bytes are a fill run; five are not.
	if c := Compact(cat([]byte{1}, same(9, 6))); !bytes.Equal(c, []byte{1, 0, 1, 9, 6, 0}) {
		t.Errorf("six equal: % X", c)
	}
	if c := Compact(cat([]byte{1}, same(9, 5))); !bytes.Equal(c, []byte{6, 0, 1, 9, 9, 9, 9, 9, 0x00, 0, 0}) {
		t.Errorf("five equal: % X", c)
	}
}

func TestExpandMalformed(t *testing.T) {
	for name, b := range map[string][]byte{
		"stray byte":         {0x00},
		"literal too long":   {0x05, 0x00, 1, 2},
		"no fill count":      {0x00, 0x00, 0xFF, 0x00},
		"dangling literal n": {0x00, 0x00, 0xFF, 0x00, 0x0B, 0x02, 0x00},
	} {
		if _, err := Expand(b); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v, want ErrFormat", name, err)
		}
	}
}

func TestChecksum(t *testing.T) {
	// Invented code bytes, then free space, then the checksum.
	img := append([]byte{0x12, 0x34, 0x56, 0x78, 0x9A}, bytes.Repeat([]byte{0xFF}, 23)...)
	// Inverted: ED CB A9 87 65; shifted by 0, 8, 16, 24, 0.
	want := uint32(0xED) + 0xCB<<8 + 0xA9<<16 + 0x87<<24 + 0x65 - 1
	if got := Checksum(append(img, 0, 0, 0, 0)); got != want {
		t.Fatalf("checksum %08X, want %08X", got, want)
	}
	signed := binary.LittleEndian.AppendUint32(append([]byte(nil), img...), want)
	if !ChecksumOK(signed) {
		t.Error("a signed image is not OK")
	}
	signed[2] ^= 1
	if ChecksumOK(signed) || ChecksumOK([]byte{1, 2, 3}) {
		t.Error("a changed or short image is OK")
	}
}
