package system

import (
	"fmt"
	"slices"
)

// Configuration response characters: the byte a unit sends with its record
// (reverse-engineered (#10)). Units accept 0x02-0x04 and 0x20-0x2F; values
// not listed here are single-input Multis.
const (
	ResponseDualInput = 0x03 // dual-AC-input unit (Quattro class)
	ResponseInverter  = 0x04 // inverter: no AC input
	ResponseNoConfig  = 0x23 // '#': the unit has no system configuration
	ResponseMP2       = 0x2F // '/': single-input Multi of the MultiPlus-II / MultiGrid family
)

// LetterUnknown is the compatibility letter index of a unit that did not
// report one.
const LetterUnknown = 0xFF

// Unit is one VE.Bus unit of a system: its identity as the unit reports it,
// its current record and a PC-side name. A Layout refers to units by
// pointer.
type Unit struct {
	LongID uint32
	// VersionWord: low 24 bits the seven-digit firmware number, high 8
	// bits a sub-version byte.
	VersionWord uint32
	// Letter is the compatibility letter index: 0 = 'A' … 25 = 'Z';
	// LetterUnknown (or any larger value) when unknown.
	Letter byte
	// ConfigProtocol is the configuration-protocol version the unit
	// speaks. This package supports 0 and 1 (rule E13).
	ConfigProtocol byte
	// IdentifyFlag is byte 5 of the unit's identify reply (normally 1).
	// It is kept for .rvms files and not used here.
	IdentifyFlag byte
	// ResponseChar is the configuration response character (Response…).
	ResponseChar byte
	// ExternalBF is read from the unit (see ExternalBFSystem).
	ExternalBF bool
	// Record is the record the unit holds (or, for a file, the stored one).
	Record Record
	// Name is a PC-side name; it never reaches a device.
	Name string
}

// Firmware returns the seven-digit firmware number: the low 24 bits of the
// version word.
func (u *Unit) Firmware() uint32 { return u.VersionWord & 0xFFFFFF }

// SubVersion returns the high byte of the version word.
func (u *Unit) SubVersion() byte { return byte(u.VersionWord >> 24) }

// Family returns the first four digits of the firmware number (the model
// type; "VE.Bus firmware versions explained").
func (u *Unit) Family() uint32 { return u.Firmware() / 1000 }

// Revision returns the last three digits of the firmware number (the
// firmware version).
func (u *Unit) Revision() uint32 { return u.Firmware() % 1000 }

// DualInput reports a unit with two AC inputs. Only ResponseDualInput
// counts (reverse-engineered (#10)).
func (u *Unit) DualInput() bool { return u.ResponseChar == ResponseDualInput }

// Inverter reports an inverter (no AC input). Only ResponseInverter counts;
// every other unit is a Multi (reverse-engineered (#10)).
func (u *Unit) Inverter() bool { return u.ResponseChar == ResponseInverter }

// NoConfig reports a unit that has no system configuration.
func (u *Unit) NoConfig() bool { return u.ResponseChar == ResponseNoConfig }

// MP2Family reports a single-input Multi of the MultiPlus-II / MultiGrid
// family, which rule E8 limits on old firmware.
func (u *Unit) MP2Family() bool { return u.ResponseChar == ResponseMP2 }

// ExternalBFSystem reports whether master marking treats the unit as
// "external BF" (rule of Layout.Records): ExternalBF is set or the
// version word's high byte is 0xC7. Reverse-engineered (#10); why 0xC7
// means it, and what BF stands for, is not known.
func (u *Unit) ExternalBFSystem() bool { return u.ExternalBF || u.SubVersion() == 0xC7 }

// AssistantFlagApplies reports whether record byte 7 bit 4 (the
// assistant-reset flag) applies to the unit: its revision is 0 or at least
// 205. Reverse-engineered (#10).
func (u *Unit) AssistantFlagApplies() bool {
	r := u.Revision()
	return r == 0 || r >= 205
}

// splitPhaseFamilies are the firmware families of the 120/240 V
// split-phase models, which Quick Configure refuses in multi-phase layouts
// when their revision is below 210. Reverse-engineered (#10); our own table.
var splitPhaseFamilies = []uint32{2060, 2062, 2071, 2072}

// SplitPhaseModel reports a 120/240 V split-phase model (see
// splitPhaseFamilies).
func (u *Unit) SplitPhaseModel() bool {
	return slices.Contains(splitPhaseFamilies, u.Family()) && u.Revision() < 210
}

// LetterString returns the compatibility letter, or "?" when unknown.
func (u *Unit) LetterString() string { return letterString(u.Letter) }

func letterString(l byte) string {
	if l > 25 {
		return "?"
	}
	return string(rune('A' + l))
}

// CompatibilityID returns the compatibility ID shown to users: four family
// digits, an underscore and the letter, for example "2623_B". For display
// only, 700 is added to a family below 2100 when the revision is above 250
// (reverse-engineered (#10)); the rules compare the raw family.
func (u *Unit) CompatibilityID() string {
	f := u.Family()
	if u.Revision() > 250 && f < 2100 {
		f += 700
	}
	return fmt.Sprintf("%04d_%s", f, u.LetterString())
}

// Label returns Name, or the long ID as eight hex digits when Name is
// empty. Messages name units with it.
func (u *Unit) Label() string {
	if u.Name != "" {
		return u.Name
	}
	return fmt.Sprintf("%08X", u.LongID)
}
