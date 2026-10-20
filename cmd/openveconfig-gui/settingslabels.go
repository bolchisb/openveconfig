package main

// Labels and presentation of the catalogue entries on the settings tabs.
// The labels are the original tool's words: the text on screen, or the name
// it shows when the value is edited (see docs/CLEAN-ROOM.md); the help lines
// are this project's own. Facts (which flag is shown inverted, which flags
// are a choice of two, the units the values are shown in) are from this
// project's behavioural spec of the settings tabs (reverse-engineered, #6),
// restated here in our own words.

import (
	"errors"
	"maps"
	"slices"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// settingLabels returns the label of each entry the settings tabs know.
// Entries without one show their catalogue name.
func settingLabels() map[vebus.ID]string {
	s, f := vebus.SettingID, vebus.FlagID
	return map[vebus.ID]string{
		// General.
		s(6):  tr("AC1 input current limit"),
		s(49): tr("AC2 input current limit"),
		f(30): tr("Overruled by remote"),
		f(15): tr("Overruled by remote"),
		f(28): tr("Dynamic current limiter"),
		f(38): tr("External current sensor connected (see manual)"),
		f(2):  tr("System frequency"),
		s(64): tr("Battery capacity"),
		s(65): tr("State of charge when Bulk finished"),
		s(72): tr("Charge efficiency"),
		s(74): tr("State of charge after absorption"),
		s(13): tr("Number of slaves"),
		f(0):  tr("Enable 2-3 phase"),
		f(1):  tr("Leader"),
		s(14): tr("Multi phase type"),
		s(89): tr("Current sensor rating"),

		// Inverter.
		s(5):  tr("Inverter output voltage"),
		s(11): tr("DC input low shut-down"),
		s(12): tr("DC input low restart"),
		s(63): tr("DC input low pre-alarm"),
		f(13): tr("Ground relay"),
		f(64): tr("Do not restart after short-circuit (VDE 2510-2 safety)"),
		f(5):  tr("PowerAssist"),
		s(48): tr("Assist current boost factor"),
		s(73): tr("Maximum inverter current"),
		f(8):  tr("Enable AES"),
		f(31): tr("AES type"),
		s(50): tr("Start AES when load lower than"),
		s(51): tr("Stop AES when load higher than start level"),
		s(83): tr("SOC low shut-down"),
		s(84): tr("SOC low restart"),

		// Charger.
		f(6):  tr("Enable charger"),
		f(36): tr("Lithium batteries"),
		f(39): tr("Configured for VE.Bus BMS"),
		s(10): tr("Charge curve"),
		f(11): tr("Storage mode"),
		f(29): tr("Use equalization (tubular plate traction battery curve)"),
		s(2):  tr("Absorption voltage"),
		s(3):  tr("Float voltage"),
		s(4):  tr("Charge current"),
		s(7):  tr("Repeated absorption time"),
		s(8):  tr("Repeated absorption interval"),
		s(9):  tr("Absorption time"),
		s(75): tr("Battery current that ends absorption"),
		f(4):  tr("Stop after excessive bulk"),
		f(14): tr("Weak AC input"),
		f(40): tr("Disable VSense (for diagnostic purposes)"),
		s(86): tr("Stop charger below"),
		s(87): tr("Temperature compensation"),

		// Virtual switch: usage and options.
		s(15): tr("Specify virtual switch usage"),
		f(26): tr("Invert virtual switch usage"),
		s(43): tr("Minimum VS on time"),
		f(34): tr("Change inverter period time when virtual switch is on"),
		s(62): tr("Inverter period time"),
		f(35): tr("Change inverter period time on Udc"),
		s(66): tr("High DC level for frequency change start"),
		s(67): tr("\"High DC level\" delay for frequency change start"),
		s(68): tr("Low DC level for normal frequency"),
		s(69): tr("\"Low DC level\" delay for normal frequency"),

		// Virtual switch: on conditions.
		s(16): tr("High power limit for VS on"),
		s(19): tr("\"High power limit\" VS on delay"),
		s(17): tr("High DC level for VS on"),
		s(20): tr("\"High DC level\" VS on delay"),
		s(18): tr("Low DC level for VS on"),
		s(21): tr("\"Low DC level\" VS on delay"),
		s(22): tr("\"Not charging\" VS on delay"),
		s(23): tr("\"Fan on\" VS on delay"),
		f(16): tr("Set VS on when bulk protection is activated"),
		f(25): tr("Set VS on when general system failure occurs"),
		f(17): tr("Temperature alarm level"),
		s(24): tr("\"Temperature alarm\" VS on delay"),
		f(18): tr("Low battery alarm level"),
		s(25): tr("\"Low-battery alarm\" VS on delay"),
		f(19): tr("Overload alarm level"),
		s(26): tr("\"Overload alarm\" VS on delay"),
		f(20): tr("DC ripple alarm level"),
		s(27): tr("\"Udc ripple alarm\" VS on delay"),

		// Virtual switch: off conditions.
		s(28): tr("Low power limit for VS off"),
		s(31): tr("\"Low power limit\" VS off delay"),
		s(29): tr("High DC level for VS off"),
		s(32): tr("\"High DC level\" VS off delay"),
		s(30): tr("Low DC level for VS off"),
		s(33): tr("\"Low DC level\" VS off delay"),
		s(34): tr("\"Charging\" VS off delay"),
		s(35): tr("\"Fan off\" VS off delay"),
		s(36): tr("\"Bulk finished\" VS off delay"),
		s(37): tr("\"No VS on\" VS off delay"),
		s(38): tr("\"No AC input\" VS off delay"),
		f(21): tr("Temperature alarm level"),
		s(39): tr("\"Temperature alarm\" VS off delay"),
		f(22): tr("Low battery alarm level"),
		s(40): tr("\"Low-battery alarm\" VS off delay"),
		f(23): tr("Overload alarm level"),
		s(41): tr("\"Overload alarm\" VS off delay"),
		f(24): tr("DC ripple alarm level"),
		s(42): tr("\"Udc ripple alarm\" VS off delay"),

		// Virtual switch 2: see vs2Labels, which words them after the
		// usage. These are the generator texts, for where no device is known.
		s(52): tr("Start generator when load higher than"),
		s(53): tr("\"High power limit\" delay for start generator"),
		s(54): tr("Low DC level for start generator"),
		s(55): tr("\"Low DC level\" delay for start generator"),
		s(70): tr("Low SOC level for start generator"),
		s(56): tr("Once started due to load condition, stop generator when load lower than"),
		s(57): tr("\"Low power limit\" delay for stop generator"),
		s(58): tr("Once started due to a battery condition, stop generator when"),
		s(59): tr("\"High DC level\" delay for stop generator"),
		f(32): tr("Stop generator when AC2 available"),
		f(33): tr("Invert driving of auxiliary relay"),

		// Advanced.
		s(71): tr("Temperature compensation used by assistants"),
		f(41): tr("Limit internal charger to prioritize other energy sources"),
		s(88): tr("Sustain voltage"),

		// Grid.
		s(81): tr("Country / grid code standard"),
		s(44): tr("AC low disconnect"),
		s(45): tr("AC low connect"),
		s(46): tr("AC high disconnect"),
		s(47): tr("AC high connect"),
		f(3):  tr("UPS function"),
		f(27): tr("Accept wide input frequency range (45-65 Hz)"),
	}
}

// vs2Generator reports whether the second virtual switch of d controls a
// generator (usages 4 and 6) rather than whether the AC input is ignored
// (3 and 5); the original words its page after that.
func vs2Generator(d *vebus.Device) bool {
	usage, err := d.Raw(vebus.SettingID(15))
	return err != nil || usage != 3 && usage != 5
}

// vs2Title returns the title of the second virtual switch's page on d.
func vs2Title(d *vebus.Device) string {
	if vs2Generator(d) {
		return tr("Generator control")
	}
	return tr("Ignore AC input")
}

// vs2Labels returns the labels of the second virtual switch's settings on
// d, worded as the original after the usage and, for the delay of the stop
// condition (59), after that condition (58: below 256, 0 is bulk finished
// and 1 absorption finished; otherwise a voltage).
func vs2Labels(d *vebus.Device) map[vebus.ID]string {
	s := vebus.SettingID
	cond, _ := d.Raw(s(58))
	if vs2Generator(d) {
		l := map[vebus.ID]string{
			s(59): tr("\"High DC level\" delay for stop generator"),
		}
		switch cond {
		case 0:
			l[s(59)] = tr("\"Bulk finished\" delay for stop generator")
		case 1:
			l[s(59)] = tr("\"Absorption finished\" delay for stop generator")
		}
		return l
	}
	l := map[vebus.ID]string{
		s(52): tr("Do not ignore AC input when load higher than"),
		s(53): tr("\"High power limit\" delay for not-ignore AC"),
		s(54): tr("Low DC level for not-ignore AC"),
		s(55): tr("\"Low DC level\" delay for not-ignore AC"),
		s(70): tr("Low SOC level for not-ignore AC"),
		s(56): tr("When accepting AC due to load, ignore AC when load lower than"),
		s(57): tr("\"Low power limit\" delay for ignore AC"),
		s(58): tr("When accepting AC due to a battery condition, ignore AC when"),
		s(59): tr("\"High DC level\" delay for ignore AC"),
	}
	switch cond {
	case 0:
		l[s(59)] = tr("\"Bulk finished\" delay for ignore AC")
	case 1:
		l[s(59)] = tr("\"Absorption finished\" delay for ignore AC")
	}
	return l
}

// ledDelayLabels returns the labels of the delays of the virtual switch's
// LED alarm conditions on d. As in the original, they name the alarm or
// pre-alarm that the condition's flag (17-24) selects.
func ledDelayLabels(d *vebus.Device) map[vebus.ID]string {
	pre := map[uint16]string{
		24: tr("\"Temperature pre-alarm\" VS on delay"),
		25: tr("\"Low-battery pre-alarm\" VS on delay"),
		26: tr("\"Overload pre-alarm\" VS on delay"),
		27: tr("\"Udc ripple pre-alarm\" VS on delay"),
		39: tr("\"Temperature pre-alarm\" VS off delay"),
		40: tr("\"Low-battery pre-alarm\" VS off delay"),
		41: tr("\"Overload pre-alarm\" VS off delay"),
		42: tr("\"Udc ripple pre-alarm\" VS off delay"),
	}
	l := map[vebus.ID]string{}
	for n, text := range pre {
		flag := n - 7 // 24-27: flags 17-20
		if n >= 39 {
			flag = n - 18 // 39-42: flags 21-24
		}
		if on, err := d.Flag(flag); err == nil && on {
			l[vebus.SettingID(n)] = text
		}
	}
	return l
}

// deviceLabels returns settingLabels with the labels that follow the
// values of d (vs2Labels, ledDelayLabels).
func deviceLabels(d *vebus.Device) map[vebus.ID]string {
	labels := settingLabels()
	maps.Copy(labels, vs2Labels(d))
	maps.Copy(labels, ledDelayLabels(d))
	return labels
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
		s(51): tr("Added to the start level, so that the inverter does not toggle."),
		s(9):  tr("With an adaptive charge curve, the longest absorption time."),
		s(7):  tr("A short absorption, repeated at the interval below while floating."),
		s(75): tr("Absorption also ends when the battery current falls below this."),
		f(14): tr("Lets the charger run on a distorted supply, such as a small generator."),
		f(40): tr("For fault finding only: the voltage measured on the sense wires is not used. Leave it off in normal use."),
		s(87): tr("How much the charge voltages drop per degree the battery warms up."),
		s(88): tr("The charger keeps the battery at least at this voltage."),
		f(33): tr("(i.e. generator starts when relay NOT driven)"), // the original's words
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
var groupOverrides = map[vebus.ID]string{
	vebus.SettingID(86): "Low-temperature charge block",
	vebus.FlagID(26):    "Usage", // the inversion goes with the usage, as in the original
}

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
		return tr("Enter the battery capacity to turn the battery monitor on.")
	case 86:
		return tr("Enter the temperature below which charging stops.")
	}
	return tr("Enter the restart level to turn the shutdown on.")
}

// groupOrder lists groups in the order the original tool shows them, where
// the catalogue's order (settings before flags) differs; entryOrder does
// the same for the entries of a group (reverse-engineered, #6).
var (
	groupOrder = []string{"AC input current", "System frequency", "Parallel systems", "Multi phase", "Battery monitor", "External current sensor"}
	entryOrder = map[string][]vebus.ID{"Multi phase": {vebus.FlagID(0), vebus.FlagID(1), vebus.SettingID(14)}}
)

// sortLikeOriginal puts groups, and the entries of each group, in the order
// of groupOrder and entryOrder. Those not listed keep their order, after
// the listed ones.
func sortLikeOriginal(groups []string, entries map[string][]vebus.Entry) {
	rank := func(list []string, s string) int {
		if i := slices.Index(list, s); i >= 0 {
			return i
		}
		return len(list)
	}
	slices.SortStableFunc(groups, func(a, b string) int { return rank(groupOrder, a) - rank(groupOrder, b) })
	for g, ids := range entryOrder {
		slices.SortStableFunc(entries[g], func(a, b vebus.Entry) int {
			ia, ib := slices.Index(ids, a.ID()), slices.Index(ids, b.ID())
			if ia < 0 {
				ia = len(ids)
			}
			if ib < 0 {
				ib = len(ids)
			}
			return ia - ib
		})
	}
}

// cardPairs are groups shown as the two columns of one card: the key's
// group on the left, the value's on the right.
var cardPairs = map[string]string{"Charge voltages and current": "Absorption"}

// cardSplits are groups shown as one card with two columns: the entries
// listed first on the left, the others on the right.
var cardSplits = map[string][]vebus.ID{
	"AC input voltage limits": {vebus.SettingID(44), vebus.SettingID(45)}, // low levels left, high right
}

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
		return tr("Shore limit")
	case "System frequency":
		return tr("System frequency")
	case "Parallel systems":
		return tr("Parallel systems")
	case "Multi phase":
		return tr("Multi phase type")
	case "External current sensor":
		return tr("External current sensor")
	case "Battery monitor":
		return tr("Battery monitor")
	case "Output":
		return tr("Output")
	case "DC input low":
		return tr("Low DC voltage")
	case "PowerAssist":
		return tr("PowerAssist")
	case "AES":
		return tr("Enable AES")
	case "State of charge shutdown":
		return tr("Shut-down on SOC")
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
		return tr("Stop charger below")
	case "Usage":
		return tr("Usage")
	case "On conditions":
		return tr("A: Set VS ON") // the original's words, as the next two
	case "Off conditions":
		return tr("B: Set VS OFF")
	case "Options":
		return tr("VS options")
	case "Virtual switch 2":
		return tr("Second switch")
	case "Renewable energy priority":
		return tr("Limit internal charger to prioritize other energy sources")
	case "AC input voltage limits":
		return tr("Transfer switch")
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
		// "Adaptive + BatterySafe" is the name Victron's public manuals use.
		10: {1: tr("Fixed"), 2: tr("Adaptive"), 3: tr("Adaptive + BatterySafe")},
		14: {0: tr("3 phase"), 1: tr("Split phase 180°"), 2: tr("Two leg 3 phase 120°")},
	}
}

// usageLabels returns the labels of the virtual switch usages (setting 15)
// while the usage is inverted (flag 26) or not. They are the texts the
// original tool shows, which states what the switch does in each case
// (reverse-engineered, #6; see docs/CLEAN-ROOM.md for the labels).
func usageLabels(inverted bool) map[uint16]string {
	if inverted {
		return map[uint16]string{
			0: tr("Do not use VS"),
			1: tr("Drive multifunctional (aux.) relay: VS on=open; VS off=close"),
			2: tr("Ignore AC input: VS on=ignore; VS off=do not ignore"),
			3: tr("Dedicated ignore AC input"),
			4: tr("Dedicated generator control"),
			5: tr("Drive aux. relay (VS on=open) + dedicated ignore AC input"),
			6: tr("Ignore AC input (VS on=ignore) + dedicated generator control"),
		}
	}
	return map[uint16]string{
		0: tr("Do not use VS"),
		1: tr("Drive multifunctional (aux.) relay: VS on=close; VS off=open"),
		2: tr("Ignore AC input: VS on=do not ignore; VS off=ignore"),
		3: tr("Dedicated ignore AC input"),
		4: tr("Dedicated generator control"),
		5: tr("Drive aux. relay (VS on=close) + dedicated ignore AC input"),
		6: tr("Ignore AC input (VS on=do not ignore) + dedicated generator control"),
	}
}

// usageInvertible reports whether a virtual switch usage can be inverted:
// those in which the first switch drives the relay or the AC input.
func usageInvertible(usage uint16) bool { return usage == 1 || usage == 2 || usage == 5 || usage == 6 }

// vsGroupShown reports whether the virtual switch sub-page of group is shown
// for usage on d (reverse-engineered, #6; seen on the original's screen for
// usages 3 and 4): the usage page always; the on and off pages when the
// first switch drives something (1, 2, 5, 6); the options page then too,
// and for the dedicated usages 3 and 4 only when d can change the inverter
// period time on Udc (flag 35); never for 0; the second switch's page for
// usages 3 to 6.
func vsGroupShown(group string, usage uint16, d *vebus.Device) bool {
	switch group {
	case "Usage":
		return true
	case "Virtual switch 2":
		return usage >= 3
	case "Options":
		if dedicatedUsage(usage) {
			return d.Supported(vebus.FlagID(35))
		}
	}
	return usageInvertible(usage)
}

// dedicatedUsage reports the usages in which only the second switch is
// used (dedicated ignore AC input, dedicated generator control).
func dedicatedUsage(usage uint16) bool { return usage == 3 || usage == 4 }

// flagChoices returns the flags shown as a choice of two: the label for
// the flag off and for it on. The LED alarm choices of the virtual switch
// are the original tool's words (see usageLabels).
func flagChoices() map[uint16][2]string {
	temperature := [2]string{tr("Temperature alarm"), tr("Temperature pre-alarm")}
	lowBattery := [2]string{tr("Low-battery alarm"), tr("Low-battery pre-alarm")}
	overload := [2]string{tr("Overload alarm"), tr("Overload pre-alarm")}
	ripple := [2]string{tr("Udc ripple alarm"), tr("Udc ripple pre-alarm")}
	return map[uint16][2]string{
		2:  {tr("50 Hz"), tr("60 Hz")},
		31: {tr("Modified sine wave"), tr("Search mode")},
		17: temperature, 18: lowBattery, 19: overload, 20: ripple,
		21: temperature, 22: lowBattery, 23: overload, 24: ripple,
	}
}

// invertedFlags are the flags whose label states the opposite of the flag
// (the flag disables what the label enables): ticked means the flag is off.
var invertedFlags = map[uint16]bool{3: true, 4: true, 6: true, 8: true, 13: true}

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
