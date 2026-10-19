package mk2

import (
	"fmt"
	"math"
)

// SettingInfo is the reply to CommandGetSettingInfo (§7.3.8).
type SettingInfo struct {
	Sc      int16
	Offset  int16
	Default uint16
	Min     uint16
	Max     uint16
	// AccessLevel is only sent in Winmon mode 2; HasAccessLevel reports
	// whether it was.
	AccessLevel    byte
	HasAccessLevel bool
}

// Scale returns the scaling factor of a setting (§7.3.8): Sc if Sc > 0,
// else 1/(-Sc). It returns 0 for Sc == 0, which marks an unsupported setting.
func (s SettingInfo) Scale() float64 {
	switch {
	case s.Sc > 0:
		return float64(s.Sc)
	case s.Sc < 0:
		return 1 / -float64(s.Sc)
	}
	return 0
}

// Value converts a raw setting value (CommandReadSetting, or Default, Min and
// Max) to its display value: Scale * (x + Offset) (§7.3.8).
func (s SettingInfo) Value(raw uint16) float64 {
	return s.Scale() * (float64(raw) + float64(s.Offset))
}

// Raw is the inverse of Value, rounded to the nearest integer. It fails when
// the result does not fit the unsigned 16-bit setting value. It does not
// check Min and Max.
func (s SettingInfo) Raw(display float64) (uint16, error) {
	sc := s.Scale()
	if sc == 0 {
		return 0, ErrNotSupported
	}
	x := math.Round(display/sc - float64(s.Offset))
	if x < 0 || x > math.MaxUint16 || math.IsNaN(x) {
		return 0, fmt.Errorf("mk2: value %v out of range for setting", display)
	}
	return uint16(x), nil
}

// BitOffset is the RAM variable Offset that marks a single-bit variable
// (§7.3.9).
const BitOffset = -0x8000

// RAMInfo is the reply to CommandGetRAMVarInfo (§7.3.9).
type RAMInfo struct {
	Sc     int16
	Offset int16
}

// Signed reports whether the RAM variable's 16-bit value is signed (Sc < 0).
func (r RAMInfo) Signed() bool { return r.Sc < 0 }

// IsBit reports whether the RAM variable is a single bit (Offset 0x8000).
func (r RAMInfo) IsBit() bool { return r.Offset == BitOffset }

// Bit returns the bit number of a single-bit variable: Sc - 1 (§7.3.9).
func (r RAMInfo) Bit() int { return int(r.Sc) - 1 }

// Scale returns the scaling factor of a RAM variable (§7.3.9):
// Scale = |Sc|, and if that is >= 0x4000, Scale = 1/(0x8000 - |Sc|).
func (r RAMInfo) Scale() float64 {
	s := int32(r.Sc)
	if s < 0 {
		s = -s
	}
	if s >= 0x4000 {
		return 1 / float64(0x8000-s)
	}
	return float64(s)
}

// Value converts a raw RAM value (CommandReadRAMVar) to its display value:
// Scale * (x + Offset), with x signed when Sc < 0 (§7.3.9). For single-bit
// variables it returns the selected bit, 0 or 1.
func (r RAMInfo) Value(raw uint16) float64 {
	if r.IsBit() {
		b := r.Bit()
		if b < 0 || b > 15 {
			return 0
		}
		return float64(raw >> uint(b) & 1)
	}
	x := float64(raw)
	if r.Signed() {
		x = float64(int16(raw))
	}
	return r.Scale() * (x + float64(r.Offset))
}
