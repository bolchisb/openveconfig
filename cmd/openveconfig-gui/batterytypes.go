package main

// Battery types: named sets of charger and battery monitor values that the
// Charger page applies in one step, and the matching that tells which type
// the current values are. Nine types are built in; the user can save the
// current values as a type of their own, kept in the app preferences on
// this computer only.
//
// What a type holds, how it is scaled, applied and matched, and the values
// of the built-in types are reverse-engineered (#6, battery type spec):
// interoperability facts restated here in our own tables, names and words.
// Where a public document gives the same values, it is cited next to the
// type.

import (
	"encoding/json"
	"math"
	"strconv"

	"fyne.io/fyne/v2"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// typeSlot is one parameter of a battery type. A type's values are in the
// units noted below, on a 12 V (6-cell) basis for the scaled ones.
type typeSlot struct {
	key    string   // its name in the stored user types
	id     vebus.ID // the setting or flag it sets
	scaled bool     // scaled with the battery voltage (voltageScale)
	factor float64  // the model's display value per unit of the type's value
}

// slotCount is the number of parameters of a type.
const slotCount = 13

// typeSlots are the parameters of a battery type, in order.
var typeSlots = [slotCount]typeSlot{
	{"absorption", vebus.SettingID(2), true, 1},                   // absorption voltage, V
	{"float", vebus.SettingID(3), true, 1},                        // float voltage, V
	{"absorptionTime", vebus.SettingID(9), false, 60},             // (maximum) absorption time, h
	{"repeatedAbsorption", vebus.SettingID(7), false, 60},         // repeated absorption time, h
	{"repeatedInterval", vebus.SettingID(8), false, 1440},         // repeated absorption interval, d
	{"storage", vebus.FlagID(11), false, 1},                       // reduced-float storage phase, 0/1
	{"lithium", vebus.FlagID(36), false, 1},                       // lithium battery, 0/1
	{"chargeCurve", vebus.SettingID(10), false, 1},                // 1 fixed, 2 adaptive, 3 adaptive with a gradual absorption
	{"socAfterBulk", vebus.SettingID(65), false, 1},               // state of charge at the end of bulk, %
	{"chargeEfficiency", vebus.SettingID(72), false, 0.01},        // charge efficiency, %
	{"temperatureCompensation", vebus.SettingID(87), true, 0.001}, // mV/°C, as the Charger page shows it
	{"lowTemperatureStop", vebus.SettingID(86), false, 1},         // block charging below, °C
	{"vebusBMS", vebus.FlagID(39), false, 1},                      // configured for a VE.Bus BMS, 0/1
}

// Indexes of slots the code refers to.
const (
	slotAbsorption     = 0
	slotFloat          = 1
	slotAbsorptionTime = 2
	slotLithium        = 6
	slotChargeCurve    = 7
	slotBMS            = 12
)

// typeTractionFlag is the tubular-plate traction curve. It is not a
// parameter of a type: applying any type turns it off, and matching ignores
// it.
const typeTractionFlag = 29

// slotMode says what a type does with a parameter.
type slotMode uint8

const (
	slotKeep    slotMode = iota // leaves it unchanged; always matches
	slotSet                     // sets the slot's value
	slotDefault                 // sets the device default; matches the device default
)

// slotValue is a type's value of one parameter.
type slotValue struct {
	mode slotMode
	v    float64 // with slotSet
}

// batteryType is a built-in or user-defined battery type.
type batteryType struct {
	name, desc string
	user       bool
	values     [slotCount]slotValue
}

// builtinTypes returns the built-in battery types, in the order the dialog
// lists them. Names and descriptions are our own; brand names only say
// which batteries a type is for.
func builtinTypes() []batteryType {
	keep := slotValue{}
	v := func(x float64) slotValue { return slotValue{mode: slotSet, v: x} }
	// lithium is a type with lithium charging: a fixed curve, no storage
	// phase, 1 h absorption repeated weekly, 95 % at the end of bulk. The
	// temperature compensation is left unchanged.
	lithium := func(name, desc string, absorption, float, efficiency float64, lowTemp slotValue, bms float64) batteryType {
		return batteryType{name: name, desc: desc, values: [slotCount]slotValue{
			v(absorption), v(float), v(1), v(1), v(7), v(0), v(1), v(1), v(95), v(efficiency), keep, lowTemp, v(bms)}}
	}
	// lead is a lead-acid type: 13.8 V float, adaptive curve with a gradual
	// absorption, 1 h absorption repeated weekly, storage phase on, 85 % at
	// the end of bulk, 80 % efficiency, 16.2 mV/°C compensation (2.7 mV/°C
	// per cell). They differ in absorption voltage and time only.
	lead := func(name, desc string, absorption, hours float64) batteryType {
		return batteryType{name: name, desc: desc, values: [slotCount]slotValue{
			v(absorption), v(13.8), v(hours), v(1), v(7), v(1), v(0), v(3), v(85), v(80), v(16.2), keep, v(0)}}
	}
	// stopOff is the lowest "block charging below" level, which turns the
	// device's own low-temperature charge stop off.
	stopOff := v(-20.5)
	return []batteryType{
		// Victron "Lithium Battery Smart" manual, section 4.8 "Charger
		// settings": 14.2 V absorption and 13.5 V float for a 12.8 V battery
		// (it advises 2 h absorption, not 1 h).
		// https://www.victronenergy.com/media/pg/Lithium_Battery_Smart/en/installation.html
		lithium(tr("LFP lithium, generic"),
			tr("Lithium iron phosphate (LiFePO4) batteries without a VE.Bus BMS."), 14.2, 13.5, 95, keep, 0),
		// As above. The VE.Bus BMS stops charging in the cold itself, so the
		// type turns the device's own stop off. On a device without the VE.Bus
		// BMS setting the BMS needs an assistant: "VE.Bus BMS" manual, §3.2,
		// https://www.victronenergy.com/upload/documents/Manual-VE-BUS-BMS-EN-NL-FR-DE-SE-.pdf
		lithium(tr("LFP lithium with a VE.Bus BMS"),
			tr("For example Victron Lithium Battery Smart batteries managed by a VE.Bus BMS."), 14.2, 13.5, 95, stopOff, 1),
		// MultiPlus 12 V 500-1200 VA 120 Vac manual, "5. Configuration", DIP
		// switch table: 14.1 V / 13.8 V, 8 h for long-life gel.
		// https://www.victronenergy.com/media/pg/MultiPlus_12V_500-1200VA_120Vac/en/configuration.html
		lead(tr("Gel, long-life (low voltage)"),
			tr("Long-life gel batteries charged at a lower voltage, for example MK gel or OPzV."), 14.1, 8),
		// Exide "Handbook for Gel-VRLA-Batteries Part 2", §6.1-§6.4: charging
		// up to 2.40 V per cell, so 14.2 V is within range; its float figure
		// is lower (2.27 V per cell). https://pdfcoffee.com/gel-handbook-part-2-pdf-free.html
		lead(tr("Gel, long-life (A400-class)"),
			tr("For example Exide/Sonnenschein A400 gel batteries."), 14.2, 8),
		// MultiPlus manual DIP switch table (above): 14.4 V / 13.8 V, 8 h.
		// Victron "Gel and AGM Batteries" datasheet, §13 and §14: the same
		// voltages (it gives a steeper temperature compensation).
		// https://www.victronenergy.com/upload/documents/Datasheet-GEL-and-AGM-Batteries-EN.pdf
		lead(tr("Gel/AGM deep-cycle, standard"),
			tr("For example Victron gel or AGM deep-cycle batteries and Exide/Sonnenschein A200."), 14.4, 8),
		// Reverse-engineered only; no public source found.
		lead(tr("Flooded flat-plate deep-cycle"),
			tr("Flooded flat-plate deep-cycle batteries (lead-antimony)."), 14.4, 5),
		// MultiPlus manual DIP switch table (above): 14.7 V / 13.8 V, 5 h;
		// Gel and AGM datasheet (above): 14.6-14.9 V for the fastest recharge.
		lead(tr("AGM fast recharge, tubular semi-float, spiral AGM"),
			tr("AGM deep-cycle batteries recharged fast, tubular-plate (OPzS) batteries in semi-float use, and spiral-cell AGM."), 14.7, 5),
		// COMMUNITY: a 48 V Quattro set to this type got 60 V absorption and
		// 85 % at the end of bulk.
		// https://communityarchive.victronenergy.com/questions/55796/how-to-change-state-of-charge-when-bulk-finished-v.html
		lead(tr("Flooded tubular-plate"),
			tr("Flooded tubular-plate (OPzS) batteries in cyclic use."), 15.0, 6),
		// Reverse-engineered only; Victron's Redflow ZCell page says the
		// battery's own BMS controls charging over CAN.
		// https://www.victronenergy.com/live/battery_compatibility:redflow_zcell
		lithium(tr("Zinc-bromine flow battery"),
			tr("For example Redflow ZCell."), 14.25, 14.25, 80, stopOff, 0),
	}
}

// batteryCells returns the number of cells of the battery d is built for:
// 6, 12 or 24 (a 12, 24 or 48 V battery). The device does not report it; it
// is read from the device default of the low DC voltage shutdown (setting
// 11) or, without it, of the float voltage (setting 3). Without either a 12 V
// battery is assumed.
func batteryCells(d *vebus.Device) int {
	for _, ref := range []struct {
		id   uint16
		at12 float64 // a typical default of a 12 V device
	}{{11, 9.3}, {3, 13.8}} {
		if info, ok := d.SettingInfo(ref.id); ok {
			return cellsFor(info.Value(info.Default), ref.at12)
		}
	}
	return 6
}

// cellsFor returns the battery class of a level v whose typical value on a
// 12 V battery is at12: 6, 12 or 24 cells. The bounds lie midway, on a
// ratio scale, between the classes, so they hold for any level of the
// usual ranges.
func cellsFor(v, at12 float64) int {
	switch r := v / at12; {
	case r < math.Sqrt2:
		return 6
	case r < 2*math.Sqrt2:
		return 12
	}
	return 24
}

// voltageScale returns the factor from the 12 V basis of a type to d's
// battery: cells / 6.
func voltageScale(d *vebus.Device) float64 { return float64(batteryCells(d)) / 6 }

// display returns the model's display value of the type's value v, on a
// battery of voltage scale k.
func (s typeSlot) display(v, k float64) float64 {
	x := v * s.factor
	if s.scaled {
		x *= k
	}
	return x
}

// typeValue is the inverse of display.
func (s typeSlot) typeValue(display, k float64) float64 {
	x := display / s.factor
	if s.scaled {
		x /= k
	}
	return x
}

// raw returns the raw value for the type's value v on d (voltage scale k),
// limited to the device's range, and whether it had to be limited.
func (s typeSlot) raw(d *vebus.Device, v, k float64) (uint16, bool) {
	if s.id.Kind == vebus.Flag {
		return uint16(boolIndex(v >= 0.5)), false
	}
	info, _ := d.SettingInfo(s.id.Num)
	x := math.Round(s.display(v, k)/info.Scale() - float64(info.Offset))
	switch {
	case x < float64(info.Min):
		return info.Min, true
	case x > float64(info.Max):
		return info.Max, true
	}
	return uint16(x), false
}

// defaultDisplay returns the display value of the device default of id.
func defaultDisplay(d *vebus.Device, id vebus.ID) (float64, error) {
	raw, err := d.Default(id)
	if err != nil || id.Kind == vebus.Flag {
		return float64(raw), err
	}
	info, _ := d.SettingInfo(id.Num)
	return info.Value(raw), nil
}

// applyResult is what applying a type changed.
type applyResult struct {
	// changed are the settings and flags whose value changed, in slot
	// order, the traction curve last.
	changed []vebus.ID
	// limited are the parameters whose value was outside the device's range
	// and was set to the nearest limit, with the value the type asked for
	// (display value).
	limited map[vebus.ID]float64
	// defaulted are the parameters the type has no value for, set to the
	// device default, that changed.
	defaulted []vebus.ID
	// noBMS is set when the type is for a VE.Bus BMS and the device has no
	// VE.Bus BMS setting: the type was applied as plain lithium.
	noBMS bool
	errs  []error
}

// apply sets the values of bt in d, the model: each parameter d supports
// and bt does not leave unchanged, limited to the device's range. The
// traction curve is turned off.
func (bt batteryType) apply(d *vebus.Device) applyResult {
	res := applyResult{limited: map[vebus.ID]float64{}}
	k := voltageScale(d)
	set := func(id vebus.ID, raw uint16) bool {
		before, _ := d.Raw(id)
		var err error
		if id.Kind == vebus.Flag {
			err = d.SetFlag(id.Num, raw == 1)
		} else {
			err = d.SetRaw(id, raw)
		}
		if err != nil {
			res.errs = append(res.errs, err)
		}
		after, _ := d.Raw(id)
		if after != before {
			res.changed = append(res.changed, id)
			return true
		}
		return false
	}
	for i, s := range typeSlots {
		sv := bt.values[i]
		if !d.Supported(s.id) {
			if i == slotBMS && sv.mode == slotSet && sv.v >= 0.5 {
				res.noBMS = true
			}
			continue
		}
		switch sv.mode {
		case slotDefault:
			raw, err := d.Default(s.id)
			if err != nil {
				res.errs = append(res.errs, err)
				continue
			}
			if set(s.id, raw) {
				res.defaulted = append(res.defaulted, s.id)
			}
		case slotSet:
			raw, limited := s.raw(d, sv.v, k)
			if limited {
				res.limited[s.id] = s.display(sv.v, k)
			}
			set(s.id, raw)
		}
	}
	if d.Supported(vebus.FlagID(typeTractionFlag)) {
		set(vebus.FlagID(typeTractionFlag), 0)
	}
	return res
}

// matches reports whether the current values of d are those of bt, on a
// battery of voltage scale k, and how many parameters were compared. A
// parameter is compared when bt does not leave it unchanged and d supports
// and has a value for it; it matches within half a step of the device.
func (bt batteryType) matches(d *vebus.Device, k float64) (bool, int) {
	n := 0
	for i, s := range typeSlots {
		sv := bt.values[i]
		if sv.mode == slotKeep || !d.Supported(s.id) {
			continue
		}
		cur, err := d.Display(s.id)
		if err != nil {
			continue
		}
		want := s.display(sv.v, k)
		if sv.mode == slotDefault {
			if want, err = defaultDisplay(d, s.id); err != nil {
				continue
			}
		}
		step, _ := d.Step(s.id)
		if math.Abs(cur-want) > step/2*(1+1e-9) {
			return false, n
		}
		n++
	}
	return true, n
}

// matchType returns the index of the type in types that the current values
// of d match, or -1: of the types that match, the one that compares the
// most parameters, the earlier one on a tie.
func matchType(d *vebus.Device, types []batteryType) int {
	best, bestN := -1, 0
	k := voltageScale(d)
	for i, bt := range types {
		if ok, n := bt.matches(d, k); ok && n > bestN {
			best, bestN = i, n
		}
	}
	return best
}

// typeFromDevice returns the current values of d as a user type named
// name. A parameter d does not support, or has no value for, is stored as
// "use the device default".
func typeFromDevice(d *vebus.Device, name string) batteryType {
	bt := batteryType{name: name, user: true}
	k := voltageScale(d)
	for i, s := range typeSlots {
		v, err := d.Display(s.id)
		if err != nil {
			bt.values[i] = slotValue{mode: slotDefault}
			continue
		}
		bt.values[i] = slotValue{mode: slotSet, v: s.typeValue(v, k)}
	}
	return bt
}

// supportsTypes reports whether d has any parameter of a battery type.
func supportsTypes(d *vebus.Device) bool {
	for _, s := range typeSlots {
		if d.Supported(s.id) {
			return true
		}
	}
	return false
}

// prefBatteryTypes is the preference that holds the user types as JSON.
const prefBatteryTypes = "charger.batteryTypes"

// prefLegacyPresets held the battery presets of earlier versions: name,
// absorption and float voltage, absorption time and charge curve, as
// display values of the device they were saved from. They are moved to
// prefBatteryTypes.
const prefLegacyPresets = "charger.presets"

// storedType is a user type as stored: values by slot key in the type's
// units, the keys of the slots that use the device default; slots in
// neither are left unchanged.
type storedType struct {
	Name     string             `json:"name"`
	Values   map[string]float64 `json:"values,omitempty"`
	Defaults []string           `json:"defaults,omitempty"`
}

// loadUserTypes returns the user types, after moving the presets of
// earlier versions into them. A damaged preference counts as none.
func loadUserTypes(p fyne.Preferences) []batteryType {
	migratePresets(p)
	var stored []storedType
	if s := p.String(prefBatteryTypes); s == "" || json.Unmarshal([]byte(s), &stored) != nil {
		return nil
	}
	types := make([]batteryType, 0, len(stored))
	for _, st := range stored {
		bt := batteryType{name: st.Name, user: true}
		for i, s := range typeSlots {
			if v, ok := st.Values[s.key]; ok {
				bt.values[i] = slotValue{mode: slotSet, v: v}
			}
		}
		for _, key := range st.Defaults {
			for i, s := range typeSlots {
				if s.key == key {
					bt.values[i] = slotValue{mode: slotDefault}
				}
			}
		}
		types = append(types, bt)
	}
	return types
}

// storeUserTypes saves types, the user types.
func storeUserTypes(p fyne.Preferences, types []batteryType) {
	stored := make([]storedType, 0, len(types))
	for _, bt := range types {
		st := storedType{Name: bt.name, Values: map[string]float64{}}
		for i, s := range typeSlots {
			switch bt.values[i].mode {
			case slotSet:
				st.Values[s.key] = bt.values[i].v
			case slotDefault:
				st.Defaults = append(st.Defaults, s.key)
			}
		}
		stored = append(stored, st)
	}
	data, _ := json.Marshal(stored)
	p.SetString(prefBatteryTypes, string(data))
}

// migratePresets moves the presets of earlier versions (prefLegacyPresets)
// into the user types and removes them. Their voltages are brought to the
// 12 V basis by the battery class of their own value; the parameters they
// lack are left unchanged.
func migratePresets(p fyne.Preferences) {
	s := p.String(prefLegacyPresets)
	if s == "" {
		return
	}
	var old []struct {
		Name   string             `json:"name"`
		Values map[string]float64 `json:"values"`
	}
	if json.Unmarshal([]byte(s), &old) == nil && len(old) > 0 {
		var stored []storedType
		if cur := p.String(prefBatteryTypes); cur != "" && json.Unmarshal([]byte(cur), &stored) != nil {
			stored = nil
		}
		for _, o := range old {
			st := storedType{Name: o.Name, Values: map[string]float64{}}
			// The absorption voltage, else the float voltage, tells the class.
			k := 1.0
			for _, id := range []string{"3", "2"} { // the last one found counts
				if v, ok := o.Values[id]; ok {
					k = float64(cellsFor(v, 13.8)) / 6
				}
			}
			for _, m := range []struct {
				id   uint16
				slot int
			}{{2, slotAbsorption}, {3, slotFloat}, {9, slotAbsorptionTime}, {10, slotChargeCurve}} {
				if v, ok := o.Values[strconv.Itoa(int(m.id))]; ok {
					slot := typeSlots[m.slot]
					st.Values[slot.key] = slot.typeValue(v, k)
				}
			}
			stored = append(stored, st)
		}
		data, _ := json.Marshal(stored)
		p.SetString(prefBatteryTypes, string(data))
	}
	p.RemoveValue(prefLegacyPresets)
}
