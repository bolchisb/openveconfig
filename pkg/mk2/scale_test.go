package mk2

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// Appendix 2: RAM info replies "8E <Sc> 8F <Offset>" and the scales the
// trace derives from them.
var appendix2RAMInfo = []struct {
	id     uint16
	sc     int16
	offset int16
	scale  float64 // 0 for bit variables
	bit    int
}{
	{0, 32668, 0, 0.01, -1},
	{1, 32668, 0, 0.01, -1},
	{2, 32668, 0, 0.01, -1},
	{3, 32668, 0, 0.01, -1},
	{4, 32668, 0, 0.01, -1},
	{5, -32668, 0, 0.01, -1},
	{6, 32668, 0, 0.01, -1},
	{7, 30807, 256, 0.000510, -1},
	{8, 31791, 0, 0.001024, -1},
	{9, -32668, 0, 0.01, -1},
	{10, 4, -32768, 0, 3},
	{11, 1, -32768, 0, 0},
	{12, 6, -32768, 0, 5},
	{13, 32568, 0, 0.005, -1},
}

func TestRAMInfoScaleAppendix2(t *testing.T) {
	for _, tc := range appendix2RAMInfo {
		r := RAMInfo{Sc: tc.sc, Offset: tc.offset}
		if tc.bit >= 0 {
			if !r.IsBit() || r.Bit() != tc.bit {
				t.Errorf("id %d: IsBit %v Bit %d, want bit %d", tc.id, r.IsBit(), r.Bit(), tc.bit)
			}
			continue
		}
		if r.IsBit() {
			t.Errorf("id %d: unexpected bit variable", tc.id)
		}
		// The trace prints 6 decimals.
		if !near(r.Scale(), tc.scale, 0.0000005) {
			t.Errorf("id %d: Scale = %f, want %f", tc.id, r.Scale(), tc.scale)
		}
		if r.Signed() != (tc.sc < 0) {
			t.Errorf("id %d: Signed = %v", tc.id, r.Signed())
		}
	}
}

func TestRAMInfoValue(t *testing.T) {
	ubat := RAMInfo{Sc: 32668}
	if v := ubat.Value(0x0A51); !near(v, 26.41, 1e-9) {
		t.Errorf("UBat = %v", v)
	}
	ibat := RAMInfo{Sc: -32668}
	if v := ibat.Value(0xFF9C); !near(v, -1.0, 1e-9) { // -100 * 0.01
		t.Errorf("IBat = %v", v)
	}
	period := RAMInfo{Sc: 30807, Offset: 256}
	if v := period.Value(0x88); !near(10/v, 50.0, 0.05) {
		t.Errorf("inverter frequency = %v", 10/v)
	}
	small := RAMInfo{Sc: 5, Offset: -10}
	if v := small.Value(12); v != 10 { // 5 * (12 - 10)
		t.Errorf("small scale value = %v", v)
	}
	bit3 := RAMInfo{Sc: 4, Offset: BitOffset}
	if bit3.Value(0x0008) != 1 || bit3.Value(0xFFF7) != 0 {
		t.Errorf("bit 3 values wrong")
	}
	// |Sc| = 0x8000 must not overflow.
	if s := (RAMInfo{Sc: math.MinInt16}).Scale(); !math.IsInf(s, 1) {
		t.Errorf("Scale(-32768) = %v", s)
	}
}

func TestSettingInfoScale(t *testing.T) {
	tests := []struct {
		sc     int16
		offset int16
		raw    uint16
		scale  float64
		value  float64
	}{
		{1, 0, 50, 1, 50},            // Sc > 0: Scale = Sc
		{-100, 0, 2880, 0.01, 28.8},  // Sc < 0: Scale = 1/(-Sc)
		{-10, -100, 160, 0.1, 6.0},   // with offset
		{2, 0x7FFF, 1, 2, 2 * 32768}, // large offset
		{-32768, 0, 32768, 1.0 / 32768, 1},
	}
	for _, tc := range tests {
		s := SettingInfo{Sc: tc.sc, Offset: tc.offset}
		if s.Scale() != tc.scale {
			t.Errorf("Sc %d: Scale = %v, want %v", tc.sc, s.Scale(), tc.scale)
		}
		if v := s.Value(tc.raw); !near(v, tc.value, 1e-9) {
			t.Errorf("Sc %d: Value(%d) = %v, want %v", tc.sc, tc.raw, v, tc.value)
		}
		raw, err := s.Raw(tc.value)
		if err != nil || raw != tc.raw {
			t.Errorf("Sc %d: Raw(%v) = %d, %v; want %d", tc.sc, tc.value, raw, err, tc.raw)
		}
	}
	if (SettingInfo{}).Scale() != 0 {
		t.Error("Sc 0 must give Scale 0")
	}
	if _, err := (SettingInfo{}).Raw(1); err != ErrNotSupported {
		t.Errorf("Raw with Sc 0: %v", err)
	}
	if _, err := (SettingInfo{Sc: 1}).Raw(-1); err == nil {
		t.Error("Raw(-1) must fail")
	}
	if _, err := (SettingInfo{Sc: 1}).Raw(70000); err == nil {
		t.Error("Raw(70000) must fail")
	}
}
