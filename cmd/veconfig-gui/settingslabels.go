package main

// Labels and presentation of the catalogue entries on the settings tabs.
// The labels are this project's own wording. Facts (which flag is shown
// inverted, which flags are a choice of two, the units the values are shown
// in) are from this project's behavioural spec of the settings tabs
// (reverse-engineered, #6), restated here in our own words.

import (
	"errors"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// settingLabels returns the label of each entry the settings tabs know.
// Entries without one show their catalogue name.
func settingLabels() map[vebus.ID]string {
	s, f := vebus.SettingID, vebus.FlagID
	return map[vebus.ID]string{
		// General.
		s(6):  tr("Input current limit, AC input 1"),
		s(49): tr("Input current limit, AC input 2"),
		f(30): tr("Remote panel may override the AC input 1 limit"),
		f(15): tr("Remote panel may override the AC input 2 limit"),
		f(28): tr("Generator-friendly dynamic current limit"),
		f(38): tr("External current sensor fitted"),
		f(2):  tr("Output frequency"),
		s(64): tr("Battery bank capacity"),
		s(65): tr("State of charge after bulk"),
		s(72): tr("Charge efficiency"),
		s(74): tr("State of charge after absorption"),

		// Inverter.
		s(5):  tr("AC output voltage"),
		s(11): tr("Shut down at low DC voltage"),
		s(12): tr("Restart after a low DC voltage shutdown"),
		s(63): tr("Low DC voltage pre-alarm"),
		f(13): tr("Use the neutral-to-earth bonding relay"),
		f(5):  tr("Allow PowerAssist"),
		s(48): tr("Assist boost factor"),
		s(73): tr("Inverter current limit while assisting"),
		f(8):  tr("Low-load power saving on"),
		f(31): tr("Power saving method"),
		s(50): tr("Enter power saving below"),
		s(51): tr("Margin to leave power saving"),
		s(83): tr("Shut down at state of charge"),
		s(84): tr("Restart at state of charge"),

		// Charger.
		f(6):  tr("Charger on"),
		f(36): tr("Lithium battery"),
		f(39): tr("Battery managed by a VE.Bus BMS"),
		s(10): tr("Charge algorithm"),
		f(11): tr("Reduced-float storage phase"),
		f(29): tr("Traction battery charge curve"),
		s(2):  tr("Absorption voltage"),
		s(3):  tr("Float voltage"),
		s(4):  tr("Bulk charge current"),
		s(7):  tr("Repeated absorption length"),
		s(8):  tr("Repeated absorption interval"),
		s(9):  tr("Absorption time"),
		s(75): tr("Battery current that ends absorption"),
		f(4):  tr("Stop bulk charging after its safety time"),
		f(14): tr("Accept a distorted AC input"),
		f(40): tr("Ignore the battery voltage sense input"),
		s(86): tr("Block charging below temperature"),
		s(87): tr("Temperature compensation"),

		// Virtual switch: usage and options.
		s(15): tr("The virtual switch controls"),
		f(26): tr("Invert the virtual switch"),
		s(43): tr("Minimum on time"),
		f(34): tr("Shift the inverter frequency while on"),
		s(62): tr("Inverter period while shifted"),

		// Virtual switch: on conditions.
		s(16): tr("Load above"),
		s(19): tr("Load above, for"),
		s(17): tr("DC voltage above"),
		s(20): tr("DC voltage above, for"),
		s(18): tr("DC voltage below"),
		s(21): tr("DC voltage below, for"),
		s(22): tr("Not charging, for"),
		s(23): tr("Fan running, for"),
		f(16): tr("When the bulk safety time ends"),
		f(25): tr("On a general failure"),
		f(17): tr("Temperature alarm level"),
		s(24): tr("Temperature alarm, for"),
		f(18): tr("Low battery alarm level"),
		s(25): tr("Low battery alarm, for"),
		f(19): tr("Overload alarm level"),
		s(26): tr("Overload alarm, for"),
		f(20): tr("DC ripple alarm level"),
		s(27): tr("DC ripple alarm, for"),

		// Virtual switch: off conditions.
		s(28): tr("Load below"),
		s(31): tr("Load below, for"),
		s(29): tr("DC voltage above"),
		s(32): tr("DC voltage above, for"),
		s(30): tr("DC voltage below"),
		s(33): tr("DC voltage below, for"),
		s(34): tr("Charging, for"),
		s(35): tr("Fan stopped, for"),
		s(36): tr("Bulk finished, for"),
		s(37): tr("No on condition, for"),
		s(38): tr("No AC input, for"),
		f(21): tr("Temperature alarm level"),
		s(39): tr("Temperature alarm gone, for"),
		f(22): tr("Low battery alarm level"),
		s(40): tr("Low battery alarm gone, for"),
		f(23): tr("Overload alarm level"),
		s(41): tr("Overload alarm gone, for"),
		f(24): tr("DC ripple alarm level"),
		s(42): tr("DC ripple alarm gone, for"),

		// Virtual switch 2.
		s(52): tr("Start: load above"),
		s(53): tr("Start: load above, for"),
		s(54): tr("Start: DC voltage below"),
		s(55): tr("Start: DC voltage below, for"),
		s(70): tr("Start: state of charge below"),
		s(56): tr("Stop: load below"),
		s(57): tr("Stop: load below, for"),
		s(58): tr("Stop: DC voltage above or charge stage (coded value)"),
		s(59): tr("Stop: DC voltage or charge stage, for"),
		f(32): tr("Stop when AC input is available"),
		f(33): tr("Invert the second switch"),

		// Advanced.
		f(35): tr("Shift the inverter frequency with the DC voltage"),
		s(66): tr("Frequency shift: start above DC voltage"),
		s(67): tr("Frequency shift: start delay"),
		s(68): tr("Frequency shift: stop below DC voltage"),
		s(69): tr("Frequency shift: stop delay"),
		s(71): tr("Temperature compensation used by assistants"),
		f(41): tr("Give renewable energy priority"),
		s(88): tr("Sustain voltage"),

		// Grid.
		s(44): tr("Disconnect AC input below"),
		s(45): tr("Reconnect AC input above"),
		s(46): tr("Disconnect AC input above"),
		s(47): tr("Reconnect AC input below"),
		f(3):  tr("Turn off fast AC input voltage detection"),
		f(27): tr("Accept a wide AC input frequency range"),
	}
}

// settingHelp returns a one-line explanation, in this project's own words,
// of the entries whose label does not say enough.
func settingHelp() map[vebus.ID]string {
	s, f := vebus.SettingID, vebus.FlagID
	return map[vebus.ID]string{
		f(28): tr("Raises the load on the AC input slowly, for small generators."),
		s(64): tr("0 turns the battery monitor off."),
		s(65): tr("The battery monitor jumps to this value when bulk charging ends."),
		s(72): tr("Share of the charge that the battery monitor counts as stored."),
		s(74): tr("The battery monitor jumps to this value when absorption ends."),
		s(48): tr("Leave it at the default unless you know you need another value."),
		s(84): tr("0 turns the state-of-charge shutdown off."),
		s(51): tr("Added to the entry load, so that the inverter does not toggle."),
		s(9):  tr("With an adaptive algorithm, the longest absorption time."),
		s(7):  tr("A short absorption, repeated at the interval below while floating."),
		s(75): tr("Absorption also ends when the battery current falls below this."),
		f(14): tr("Lets the charger run on a distorted supply, such as a small generator."),
		f(40): tr("For fault finding only: the voltage measured on the sense wires is not used. Leave it off in normal use."),
		s(87): tr("How much the charge voltages drop per degree the battery warms up."),
		s(88): tr("The charger keeps the battery at least at this voltage."),
	}
}

// settingUnits are the units of settings the catalogue gives none, as the
// settings tabs show them.
var settingUnits = map[uint16]string{62: "ms", 70: "%", 74: "%", 83: "%", 84: "%", 86: "°C"}

// masterFlags are flags that switch the function of a group on: while the
// flag is off, the listed settings are not used and their rows are
// disabled. The flag is the header control of its group's card.
// Reverse-engineered (#6): the original tool enables these groups only
// while their flag is set.
var masterFlags = map[uint16][]vebus.ID{
	5:  {vebus.SettingID(48), vebus.SettingID(73)},                   // PowerAssist
	8:  {vebus.FlagID(31), vebus.SettingID(50), vebus.SettingID(51)}, // low-load power saving (shown inverted)
	41: {vebus.SettingID(88)},                                        // renewable energy priority
}

// masterValues are settings whose value 0 switches a function off, and the
// settings that function uses, disabled while it is off.
// Reverse-engineered (#6): a battery capacity of 0 turns the battery
// monitor off, a restart level of 0 the state-of-charge shutdown.
var masterValues = map[uint16][]vebus.ID{
	64: {vebus.SettingID(65), vebus.SettingID(72), vebus.SettingID(74)},
	84: {vebus.SettingID(83)},
	86: nil, // the low-temperature charge block: raw 0 is off
}

// groupOverrides moves entries to a group of the settings pages other than
// their catalogue group: the low-temperature charge block gets a card of
// its own, so that its switch does not sit on the temperature compensation.
var groupOverrides = map[vebus.ID]string{vebus.SettingID(86): "Low-temperature charge block"}

// entryGroup returns the group e is shown in on the settings pages.
func entryGroup(e vebus.Entry) string {
	if g, ok := groupOverrides[e.ID()]; ok {
		return g
	}
	return e.Group
}

// valueSwitches are the groups switched on by a master value (masterValues)
// rather than a flag. Their card header gets a switch that mirrors "value is
// not 0", like the original tool's check box that is no device value
// (reverse-engineered, #6). While the function is off, the master value
// itself is disabled too.
var valueSwitches = map[string]uint16{"Battery monitor": 64, "State of charge shutdown": 84, "Low-temperature charge block": 86}

// valueSwitchHint is shown under a value switch that was turned on before
// its master value n was entered.
func valueSwitchHint(n uint16) string {
	switch n {
	case 64:
		return tr("Enter the battery bank capacity to turn the battery monitor on.")
	case 86:
		return tr("Enter the temperature below which charging stops.")
	}
	return tr("Enter the restart level to turn the shutdown on.")
}

// cardPairs are groups shown as the two columns of one card: the key's
// group on the left, the value's on the right.
var cardPairs = map[string]string{"Charge voltages and current": "Absorption"}

// pairTitle returns the title of the card of the pair whose left group is
// left.
func pairTitle(left string) string {
	if left == "Charge voltages and current" {
		return tr("Charge voltages and times")
	}
	return groupLabel(left)
}

// The Charger page hides lithiumHidden while the lithium flag (36) is on, and
// shows lithiumOnly only then; with lithium the charge algorithm offers no
// gradual-absorption variant (3). Reverse-engineered (#6).
var (
	lithiumHidden = []vebus.ID{vebus.FlagID(11), vebus.FlagID(29), vebus.SettingID(87)}
	lithiumOnly   = []vebus.ID{vebus.SettingID(86), vebus.FlagID(39)}
)

// lithiumOn reports whether d's lithium flag is set.
func lithiumOn(d *vebus.Device) bool {
	on, err := d.Flag(36)
	return err == nil && on
}

// lithiumChanged makes the changes that switching the lithium flag implies
// (reverse-engineered, #6). On: storage mode and the traction curve are
// cleared, and the adaptive algorithm with gradual absorption becomes the
// fixed one. Off: the low-temperature charge block is turned off (raw 0)
// and the VE.Bus BMS flag is cleared.
func lithiumChanged(d *vebus.Device, on bool) error {
	var errs []error
	clear := func(n uint16) {
		if d.Supported(vebus.FlagID(n)) {
			errs = append(errs, d.SetFlag(n, false))
		}
	}
	if on {
		clear(11)
		clear(29)
		if raw, err := d.Raw(vebus.SettingID(10)); err == nil && raw == 3 {
			errs = append(errs, d.SetRaw(vebus.SettingID(10), 1))
		}
	} else {
		if d.Supported(vebus.SettingID(86)) {
			errs = append(errs, d.SetRaw(vebus.SettingID(86), 0))
		}
		clear(39)
	}
	return errors.Join(errs...)
}

// batteryMonitorOff makes the changes that turning the battery monitor off
// implies (reverse-engineered, #6): the state-of-charge shutdown is turned
// off, and bulk charging is no longer stopped after its safety time, as both
// need the monitor.
func batteryMonitorOff(d *vebus.Device) error {
	var errs []error
	if d.Supported(vebus.SettingID(84)) {
		errs = append(errs, d.SetRaw(vebus.SettingID(84), 0))
	}
	if d.Supported(vebus.FlagID(4)) {
		errs = append(errs, d.SetFlag(4, invertedFlags[4])) // its check box off
	}
	return errors.Join(errs...)
}

// groupLabel translates a catalogue group name. Groups without a
// translation show the catalogue text.
func groupLabel(group string) string {
	switch group {
	case "AC input current":
		return tr("Input current limits")
	case "System frequency":
		return tr("System frequency")
	case "Battery monitor":
		return tr("Battery monitor")
	case "Output":
		return tr("Output")
	case "DC input low":
		return tr("Low DC voltage")
	case "PowerAssist":
		return tr("PowerAssist")
	case "AES":
		return tr("Low-load power saving")
	case "State of charge shutdown":
		return tr("Low state of charge shutdown")
	case "Battery type":
		return tr("Battery type")
	case "Charge voltages and current":
		return tr("Charge voltages and current")
	case "Absorption":
		return tr("Absorption")
	case "Charger":
		return tr("Charger")
	case "Temperature":
		return tr("Temperature")
	case "Low-temperature charge block":
		return tr("Stop charging at low temperature")
	case "Usage":
		return tr("Usage")
	case "On conditions":
		return tr("Switch on when")
	case "Off conditions":
		return tr("Switch off when")
	case "Options":
		return tr("Options")
	case "Virtual switch 2":
		return tr("Second switch")
	case "Frequency shift":
		return tr("Frequency shift")
	case "Assistants":
		return tr("Assistants")
	case "Renewable energy priority":
		return tr("Renewable energy priority")
	case "AC input voltage limits":
		return tr("AC input voltage limits")
	case "UPS function":
		return tr("UPS function")
	case "AC input frequency":
		return tr("AC input frequency")
	case "Grid code":
		return tr("Grid code")
	}
	return group
}

// choiceLabels returns the labels of the values of enumerated settings.
// Values the catalogue names but this table does not show the catalogue
// text.
func choiceLabels() map[uint16]map[uint16]string {
	return map[uint16]map[uint16]string{
		10: {1: tr("Fixed"), 2: tr("Adaptive"), 3: tr("Adaptive, with a gradual absorption phase")},
		15: {
			0: tr("Nothing (not used)"),
			1: tr("The relay"),
			2: tr("Disconnecting the AC input"),
			3: tr("Second switch disconnects the AC input"),
			4: tr("Second switch runs a generator"),
			5: tr("The relay and disconnecting the AC input"),
			6: tr("Disconnecting the AC input; second switch runs a generator"),
		},
	}
}

// flagChoices returns the flags shown as a choice of two: the label for
// the flag off and for it on.
func flagChoices() map[uint16][2]string {
	alarm := [2]string{tr("Alarm"), tr("Pre-alarm")}
	return map[uint16][2]string{
		2:  {tr("50 Hz"), tr("60 Hz")},
		31: {tr("Low-power modified sine"), tr("Search mode (pulses)")},
		17: alarm, 18: alarm, 19: alarm, 20: alarm,
		21: alarm, 22: alarm, 23: alarm, 24: alarm,
	}
}

// invertedFlags are the flags whose label states the opposite of the flag
// (the flag disables what the label enables): ticked means the flag is off.
var invertedFlags = map[uint16]bool{4: true, 6: true, 8: true, 13: true}

// conversion is how a setting's value is shown when it differs from the
// model's display value (vebus.Device.Display).
type conversion int

const (
	convNone     conversion = iota
	convHours               // minutes shown as hours
	convDays                // minutes shown as days
	convSeconds             // minutes shown as seconds
	convWatts               // a current shown as a power at the AC output voltage (setting 5)
	convAbsolute            // a setting stored relative to another, shown as the level it stands for
	convPercent             // a ratio shown as a percentage
	convMilli               // V/°C shown as mV/°C
)

// settingConversions lists the settings shown in another unit or as an
// absolute level. The AC input reconnect levels are shown as the levels
// 44 + 45 and 46 - 47 (catalogue RelativeTo). The charge efficiency (72)
// is a ratio; the temperature
// compensation slope (87) is shown in mV/°C (reverse-engineered, #6), its
// display value is in V/°C.
var settingConversions = func() map[uint16]conversion {
	m := map[uint16]conversion{7: convHours, 9: convHours, 8: convDays, 12: convAbsolute, 63: convAbsolute,
		45: convAbsolute, 47: convAbsolute, 72: convPercent, 87: convMilli}
	for _, id := range []uint16{19, 20, 21, 22, 23, 24, 25, 26, 27, 31, 32, 33, 34, 35, 38, 39, 40, 41, 42, 53, 55, 67, 69} {
		m[id] = convSeconds
	}
	for _, id := range []uint16{16, 28, 50, 51, 52, 56} {
		m[id] = convWatts
	}
	return m
}()
