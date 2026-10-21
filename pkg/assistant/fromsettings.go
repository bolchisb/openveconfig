package assistant

import (
	"math"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// The composer needs the unit's nominal power, its DC and AC voltage, its
// Winmon ID offset, its rated power and what kind of unit it is, and no
// reply carries any of them: the original works them out from the
// settings model, from the info records of a handful of settings and from
// setting 85, whose record it reads as ten bytes of "device info"
// (reverse-engineered, #32). They are therefore as available from a saved
// settings file as from a connected unit.
//
// The settings it reads, by number:
const (
	setUBatFloat       = 3  // float voltage, the fallback for the DC code
	setIBatBulk        = 4  // 0 on an inverter, which has no charger
	setUInvSetpoint    = 5  // inverter output voltage, which gives hwAC
	setUBat2Low        = 11 // low battery voltage, which gives the DC code
	setIInvHigh        = 16 // inverter current, which gives the nominal power
	setBatteryCapacity = 64 // battery capacity
	setDeviceInfo      = 85 // its info record is the device-info bytes
)

// Bits of cDeviceInfo, as the sources read it (IsMulti, IsCompact,
// IsQuattro, IsInverter, IsSolarModel, HasNTC). Only the three below are
// worked out here; compact, solar model and NTC stay 0, their derivation
// being unknown.
const (
	devIsMulti    = 1
	devIsQuattro  = 4
	devIsInverter = 8
)

// SettingRecords returns a setting's info record and whether the unit has
// it (a record with Sc of 0 counts as absent).
type SettingRecords func(id int) (mk2.SettingInfo, bool)

// FromSettings fills the fields of d that come from the settings model:
// ACVolts, DCCode, PNominal and WinmonIDOffset, and the cRatedPower and
// cDeviceInfo entries of Extra. It leaves a field alone when the settings
// it needs are not there, and never replaces an Extra the caller set.
func (d *Device) FromSettings(rec SettingRecords) {
	def := func(id int) (float64, bool) {
		si, ok := rec(id)
		if !ok {
			return 0, false
		}
		return si.Value(si.Default), true
	}

	if v, ok := def(setUInvSetpoint); ok {
		d.ACVolts = v
	}
	d.DCCode = dcCode(def)
	if w, ok := nominalPower(def, d.ACVolts); ok {
		d.PNominal = w
	}

	si, ok := rec(setDeviceInfo)
	if !ok {
		return
	}
	// Item 3 of the device info, the high byte of the record's offset,
	// reads as 0 when it is 0xFF.
	if w := int64(uint16(si.Offset) >> 8); w != 0xFF {
		d.WinmonIDOffset = w
	}
	if d.Extra == nil {
		d.Extra = map[string]asm.Value{}
	}
	// Items 4 and 5, the record's default, are the rated power; 0xFFFF
	// means the unit does not give one, and the nominal power stands in.
	if _, set := d.Extra["cRatedPower"]; !set {
		rated := int64(si.Default)
		if si.Default == 0xFFFF {
			rated = d.PNominal
		}
		d.Extra["cRatedPower"] = asm.IntVal(rated)
	}
	if _, set := d.Extra["cDeviceInfo"]; !set {
		d.Extra["cDeviceInfo"] = asm.IntVal(deviceInfo(rec, si))
	}
}

// dcCode is the unit's DC value, 6, 12 or 24 (hwDC is twice it): it steps
// at 12 V and 24 V of the low battery voltage, or of the float voltage
// when the unit has no low battery voltage, or of 12 V when it has
// neither.
func dcCode(def func(int) (float64, bool)) int64 {
	v, ok := def(setUBat2Low)
	if !ok || v < 0 {
		if v, ok = def(setUBatFloat); !ok || v < 0 {
			v = 12
		}
	}
	switch {
	case v > 24:
		return 24
	case v > 12:
		return 12
	}
	return 6
}

// nominalPower is cPNominal: setting 16 holds the inverter current, so
// the AC voltage turns it into watts; the original takes off the 50 W it
// added, falls back to the battery capacity's low byte when there is no
// current, and rounds to the nearest 10.
func nominalPower(def func(int) (float64, bool), acVolts float64) (int64, bool) {
	a, ok := def(setIInvHigh)
	if !ok {
		return 0, false
	}
	w := a * float64(nearestAC(acVolts))
	if w > 50 {
		w -= 50
	}
	if w == 0 {
		c, ok := def(setBatteryCapacity)
		if !ok {
			return 0, false
		}
		w = float64(byte(int64(c)))
	}
	return int64(math.Round(w/10) * 10), true
}

// deviceInfo is cDeviceInfo: a unit with no charger is an inverter, one
// with a second AC input (item 1 of the device info, the high byte of the
// record's scale) is a Quattro, and anything else is a Multi.
func deviceInfo(rec SettingRecords, info mk2.SettingInfo) int64 {
	if si, ok := rec(setIBatBulk); ok && si.Value(si.Max) == 0 {
		return devIsInverter
	}
	if uint16(info.Sc)>>8 != 0 {
		return devIsQuattro
	}
	return devIsMulti
}
