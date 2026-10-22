package assistant

import (
	"math"
	"strings"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// VEConfigVersion is the version number the original tool gives its
// sources as cVEConfigVersion (reverse-engineered, #8).
const VEConfigVersion = 9004248

// Device describes the unit that assistants are compiled for: what the
// composer's environment labels are computed from (reverse-engineered,
// #8; spec addendum §8.2, §8.3). A user interface fills it from a
// connected device or from a settings file.
type Device struct {
	// MultiVersion is the firmware version number (cMultiVersion, for
	// example 2627560). The sources derive family, model and revision
	// from it themselves.
	MultiVersion int64
	// PNominal is the nominal power, W (cPNominal).
	PNominal int64
	// IInvNominal is cIInvNominal when the device gives it; 0 computes it
	// from PNominal and the AC voltage.
	IInvNominal int64
	// DCCode is the device's DC value, 6, 12 or 24; hwDC is twice it.
	DCCode int64
	// ACVolts is the AC voltage; hwAC is the nearest of 120, 230, 276.
	ACVolts float64
	// WinmonIDOffset is device info item 3 (0xFF reads as 0).
	WinmonIDOffset int64
	// VersionExtra are the bytes after the version in the firmware-version
	// reply, when known (at least 3): they give cVLLVersion, cVLLSystem
	// and ConnectionBoardWithPIC. When nil those labels are left out.
	VersionExtra []byte
	// Settings and Flags are the unit's settings model.
	Settings []DeviceSetting
	Flags    []DeviceFlag
	// PackageVersion and DownloadedBy come from the package folder's
	// index (vap.ReadIndex); 0 leaves them out.
	PackageVersion, DownloadedBy int64
	// Extra are further labels whose derivation the spec does not give
	// (cDeviceInfo, cMultiSubVersion, cRatedPower, the bsi… layout); they
	// are passed as they are and win over computed ones.
	Extra map[string]asm.Value
}

// DeviceSetting is one setting of the unit, raw values as the device
// reports them (scale, offset, default, minimum, maximum as in its
// setting info) and its current raw value.
type DeviceSetting struct {
	ID                int
	Name              string // the name the sources use for it
	Scale, Offset     int64
	Default, Min, Max int64
	Value             int64
}

// DeviceFlag is a flag the unit supports, with its default bit and the
// bit it currently holds.
type DeviceFlag struct {
	Name    string // without the "ee" prefix
	Default bool
	Value   bool
}

// bit is a flag's value as the sources read it.
func bit(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Labels returns the environment labels for d (spec addendum §8.2, §8.3).
// Labels that the sources derive themselves (cMultiFamily, cMultiModel,
// cMultiRevision, cFirmwareSupportsVEBusBMS, cUseDCOvervoltageProtection,
// cRelativeSolarPower, the Prev_… labels; §8.1) are not made here.
func Labels(d Device) map[string]asm.Value {
	l := map[string]asm.Value{
		"cMultiVersion":         asm.IntVal(d.MultiVersion),
		"cPNominal":             asm.IntVal(d.PNominal),
		"hwDC":                  asm.IntVal(2 * d.DCCode),
		"hwAC":                  asm.IntVal(nearestAC(d.ACVolts)),
		"cWinmonIDOffset":       asm.IntVal(d.WinmonIDOffset),
		"GridCodesRequiringENS": asm.ListVal(asm.IntVal(3), asm.IntVal(4)),
		"cVEConfigVersion":      asm.IntVal(VEConfigVersion),
	}
	if d.WinmonIDOffset == 0xFF {
		l["cWinmonIDOffset"] = asm.IntVal(0)
	}
	inv := d.IInvNominal
	if inv == 0 {
		inv = (d.PNominal + 50) * 100 / nearestAC(d.ACVolts)
	}
	l["cIInvNominal"] = asm.IntVal(inv)
	if b := d.VersionExtra; len(b) >= 3 {
		vll := int64(b[0]) + int64(b[2])*256
		l["cVLLVersion"] = asm.IntVal(vll & 0xFFF)
		l["cVLLSystem"] = asm.IntVal(vll & 0xF000)
		l["ConnectionBoardWithPIC"] = asm.BoolVal(b[1]&1 != 0)
	}
	if d.PackageVersion != 0 {
		l["packageversion"] = asm.IntVal(d.PackageVersion)
	}
	if d.DownloadedBy != 0 {
		l["DownloadedBy"] = asm.IntVal(d.DownloadedBy)
	}
	for _, f := range d.Flags {
		if f.Name == "" || strings.Contains(f.Name, "@") {
			continue
		}
		l["ee"+f.Name+"_default"] = asm.IntVal(bit(f.Default))
		// A flag's current bit is read as <name>_VEC, the same suffix a
		// setting's value takes (the sources ask AddVECSettingToChange
		// for _AssistEnabled_VEC and the like).
		l[f.Name+"_VEC"] = asm.IntVal(bit(f.Value))
	}
	for _, s := range d.Settings {
		if s.Name == "" || strings.Contains(s.Name, "@") {
			continue
		}
		l[s.Name+"_VEC"] = asm.IntVal(s.Value)
		// Grid settings (128–191, table entries 247–310) get no metadata
		// labels, except GridSettingsInt0.
		grid := s.ID >= 128 && s.ID <= 191 && s.Name != "GridSettingsInt0"
		if s.Scale == 0 || grid {
			continue
		}
		l[s.Name+"_scale"] = asm.IntVal(s.Scale)
		l[s.Name+"_offset"] = asm.IntVal(s.Offset)
		l[s.Name+"_default"] = asm.IntVal(s.Default)
		l[s.Name+"_minimum"] = asm.IntVal(s.Min)
		l[s.Name+"_maximum"] = asm.IntVal(s.Max)
	}
	for k, v := range d.Extra {
		l[k] = v
	}
	return l
}

// nearestAC returns the nearest of 120, 230 and 276 V.
func nearestAC(v float64) int64 {
	best := int64(230)
	for _, c := range []int64{120, 230, 276} {
		if math.Abs(v-float64(c)) < math.Abs(v-float64(best)) {
			best = c
		}
	}
	return best
}
