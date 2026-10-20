package gridcode

import (
	"errors"
	"strings"
	"testing"
)

func TestStamp(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  Pair
		g    Code
		v    uint8
		want Pair
	}{
		// Code changes on complete pairs: the generation goes up by one.
		{"0 to 14", Pair{0x0301, 0x0301}, 14, 0, Pair{0x040e, 0x040e}},
		{"14 to 0", Pair{0x0400, 0x0400}, 14, 0, Pair{0x050e, 0x050e}},
		{"14 to 1", Pair{0x050e, 0x050e}, 1, 0, Pair{0x0601, 0x0601}},
		{"1 to 0", Pair{0x0601, 0x0601}, 0, 0, Pair{0x0700, 0x0700}},
		{"same code", Pair{0x0700, 0x0700}, 0, 0, Pair{0x0800, 0x0800}},
		{"never set", Pair{0xFFFF, 0xFFFF}, 0, 0, Pair{0x0000, 0x0000}},
		{"generation wraps", Pair{0xFF0e, 0xFF0e}, 14, 0, Pair{0x000e, 0x000e}},
		// The next generation is B's (an interrupted earlier write): skip it.
		{"collision", Pair{0x0301, 0x0401}, 1, 0, Pair{0x0501, 0x0501}},
		{"collision wraps", Pair{0xFE00, 0xFF00}, 0, 0, Pair{0x0000, 0x0000}},
		{"B behind A", Pair{0x0501, 0x0301}, 1, 0, Pair{0x0601, 0x0601}},
		// B's low byte is (g + v) mod 256.
		{"version 2", Pair{0x0400, 0x0400}, 2, 2, Pair{0x0502, 0x0504}},
		{"version 1", Pair{0x0502, 0x0504}, 6, 1, Pair{0x0606, 0x0607}},
		{"version wraps", Pair{0x0000, 0x0000}, 0xFE, 3, Pair{0x01FE, 0x0101}},
	} {
		got, err := Stamp(tc.old, tc.g, tc.v)
		if err != nil || got != tc.want {
			t.Errorf("%s: Stamp(%04x/%04x, %d, %d) = %04x/%04x, %v; want %04x/%04x",
				tc.name, tc.old.A, tc.old.B, tc.g, tc.v, got.A, got.B, err, tc.want.A, tc.want.B)
		}
	}
	if _, err := Stamp(Pair{}, 0x100, 0); err == nil {
		t.Error("Stamp with grid code 256 succeeded")
	}
}

// TestStampChain stamps repeatedly: every result passes both checks and
// the generation goes up by one each time, wrapping after 0xFF.
func TestStampChain(t *testing.T) {
	p := Pair{0xFFFF, 0xFFFF}
	for i := range 300 {
		g, v := Code(i%33), uint8(i%3)
		next, err := Stamp(p, g, v)
		if err != nil {
			t.Fatal(err)
		}
		if hi(next.A) != (hi(p.A)+1)&0xFF {
			t.Fatalf("step %d: generation %02x after %02x", i, hi(next.A), hi(p.A))
		}
		if !next.Consistent(g, v) {
			t.Fatalf("step %d: %04x/%04x not consistent for code %d, version %d", i, next.A, next.B, g, v)
		}
		if h, ver := next.CheckShown(g, v); !h || !ver {
			t.Fatalf("step %d: CheckShown(%04x/%04x, %d, %d) = %v, %v", i, next.A, next.B, g, v, h, ver)
		}
		p = next
	}
}

func TestConsistent(t *testing.T) {
	for _, tc := range []struct {
		p    Pair
		g    Code
		v    uint8
		want bool
	}{
		{Pair{0x1234, 0x5678}, 0, 0, true}, // grid code 0 always passes
		{Pair{0x040e, 0x040e}, 14, 0, true},
		{Pair{0x040e, 0x040e}, 1, 0, false},  // other code
		{Pair{0x040e, 0x050e}, 14, 0, false}, // generations differ
		{Pair{0x040e, 0x040e}, 14, 1, false}, // other version
		{Pair{0x0502, 0x0504}, 2, 2, true},
		{Pair{0x0502, 0x0504}, 2, 0, false},
		{Pair{0x01FE, 0x0101}, 0xFE, 3, true},
		{Pair{0x0100, 0x0100}, 0x100, 0, false}, // code beyond a byte
	} {
		if got := tc.p.Consistent(tc.g, tc.v); got != tc.want {
			t.Errorf("%04x/%04x.Consistent(%d, %d) = %v", tc.p.A, tc.p.B, tc.g, tc.v, got)
		}
	}
}

func TestCheckShown(t *testing.T) {
	for _, tc := range []struct {
		p               Pair
		g               Code
		v               uint8
		header, version bool
	}{
		{Pair{0xFFFF, 0xFFFF}, 0, 0, true, true},   // never set
		{Pair{0xFFFF, 0x0000}, 0, 0, true, false},  // never set, B differs
		{Pair{0xFFFF, 0xFFFF}, 1, 0, false, true},  // 0xFFFF only for code 0
		{Pair{0x0700, 0x0700}, 0, 0, true, true},   // ordinary code 0
		{Pair{0x0301, 0x0601}, 1, 0, false, true},  // generations differ
		{Pair{0x0301, 0x0301}, 14, 0, false, true}, // other code
		{Pair{0x0502, 0x0504}, 2, 2, true, true},
		{Pair{0x0502, 0x0504}, 2, 0, true, false}, // version differs
		{Pair{0x01FE, 0x0101}, 0xFE, 3, true, true},
	} {
		h, v := tc.p.CheckShown(tc.g, tc.v)
		if h != tc.header || v != tc.version {
			t.Errorf("%04x/%04x.CheckShown(%d, %d) = %v, %v; want %v, %v", tc.p.A, tc.p.B, tc.g, tc.v, h, v, tc.header, tc.version)
		}
	}
}

// gridTable builds a synthetic grid-info table of the given format with
// one entry per version in versions; codes[i] is the entry of grid code i.
func gridTable(format byte, versions []byte, codes []int) []byte {
	b := []byte{0, 0, format, byte(len(codes)), 8}
	at := len(b) + 2*len(codes)
	b = append(b, make([]byte, 2*len(codes))...)
	var entries []int
	for _, v := range versions {
		entries = append(entries, at)
		e := append([]byte{v, 0x00, 0x80}, make([]byte, 16)...) // version, flags, two bitmaps
		b = append(b, e...)
		at += len(e)
	}
	for i, c := range codes {
		b[5+2*i], b[6+2*i] = byte(entries[c]), byte(entries[c]>>8)
	}
	b[0], b[1] = byte(len(b)-5), byte((len(b)-5)>>8)
	return b
}

func TestParamSetVersion(t *testing.T) {
	table := gridTable(3, []byte{0, 1, 2}, []int{0, 1, 2, 1, 0})
	for g, want := range []uint8{0, 1, 2, 1, 0} {
		if v, err := ParamSetVersion(table, Code(g)); err != nil || v != want {
			t.Errorf("code %d: %d, %v; want %d", g, v, err, want)
		}
	}
	for _, tc := range []struct {
		name  string
		table []byte
		g     Code
	}{
		{"no table", nil, 2},
		{"code outside the table", table, 5},
		{"format 0 has no version", gridTable(0, []byte{2}, []int{0, 0, 0}), 2},
	} {
		if v, err := ParamSetVersion(tc.table, tc.g); err != nil || v != 0 {
			t.Errorf("%s: %d, %v; want 0", tc.name, v, err)
		}
	}
	bad := gridTable(3, []byte{2}, []int{0, 0})
	bad[7] = byte(len(bad)) // code 1 points past the end
	for _, tc := range []struct {
		name  string
		table []byte
		g     Code
	}{
		{"short header", []byte{0, 0, 3}, 0},
		{"offsets cut off", table[:8], 2},
		{"entry outside", bad, 1},
	} {
		if _, err := ParamSetVersion(tc.table, tc.g); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
}

func TestCheckDeviceWrite(t *testing.T) {
	for _, ids := range [][]uint16{nil, {0, 44, 80, 82, 127, 192}} {
		if err := CheckDeviceWrite(ids); err != nil {
			t.Errorf("CheckDeviceWrite(%v) = %v", ids, err)
		}
	}
	for _, ids := range [][]uint16{{81}, {128}, {44, 150}, {190}, {191}} {
		if err := CheckDeviceWrite(ids); !errors.Is(err, ErrDeviceWrite) {
			t.Errorf("CheckDeviceWrite(%v) = %v, want ErrDeviceWrite", ids, err)
		}
	}
	if err := CheckDeviceWrite([]uint16{44, 81, 150}); err == nil || !strings.Contains(err.Error(), "settings 81, 150") {
		t.Errorf("error %v does not name settings 81 and 150", err)
	}
}
