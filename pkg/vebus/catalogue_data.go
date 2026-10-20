package vebus

// The built-in catalogue. To add a variable, add a row here; nothing else
// needs to change.
//
// Every name comes from one of:
//   - Victron's public document "Interfacing with VE.Bus products - MK2
//     Protocol 3.14", §7.3.13 (settings 0-65, flags 0-34, RAM 0-19). Names
//     are written as the document prints them, with "(time)" added where
//     it gives a level and a time setting the same name.
//   - The MIT-licensed community findings of xcellsior/ve-bus-programming,
//     https://github.com/xcellsior/ve-bus-programming, FINDINGS.md §12.2
//     (settings 66-88), §12.3 (flags 35-41) and §12.4 (RAM 20-37). These are
//     community-reported, not confirmed by Victron. See THIRD_PARTY_NOTICES.md.
//   - Own neutral naming, marked "own" in Source.
//
// Tabs follow the public VEConfigure manual,
// https://www.victronenergy.com/media/pg/VEConfigure_Manual/en/ : chapters
// 7 General (system frequency, shore current, dynamic current limiter,
// external current sensor, battery monitor), 8 Grid (grid code, input
// frequency, input voltage limits, UPS function), 9 Inverter (output
// voltage, ground relay, DC input low levels, AES, PowerAssist, boost
// factor, SoC shutdown), 10 Charger (charger on/off, weak AC, stop after
// excessive bulk, lithium, storage mode, traction curve, charge curve,
// voltages, current, absorption times, temperature compensation) and 13
// Virtual Switch. Entries those chapters do not mention are placed by own
// judgment from their names (Advanced for assistant-related settings, None
// for system configuration and flags not to be changed by hand). Flag 40
// (VSense off, for diagnostics) is on the Charger tab, where the original
// tool shows it (reverse-engineered, #6). Group labels are this project's
// own.

const (
	srcRAM        = "MK2 Protocol 3.14 §7.3.13.1"
	srcSettings   = "MK2 Protocol 3.14 §7.3.13.2"
	srcFlags      = "MK2 Protocol 3.14 §7.3.13.3"
	srcVSSettings = "MK2 Protocol 3.14 §7.3.13.4"
	srcVSFlags    = "MK2 Protocol 3.14 §7.3.13.5"
	srcCommSet    = "community: xcellsior/ve-bus-programming FINDINGS.md §12.2"
	srcCommFlags  = "community: xcellsior/ve-bus-programming FINDINGS.md §12.3"
	srcCommRAM    = "community: xcellsior/ve-bus-programming FINDINGS.md §12.4"
	srcOwnRAM     = "own name; MK2 Protocol 3.14 §7.3.13.1 (17-19 are the unfiltered 14-16; 15 is named after community FINDINGS.md §12.4)"
	srcOwnFlag7   = "own name; MK2 Protocol 3.14 §7.3.13.3 (no name given)"
	srcOwnSet65   = "own name; MK2 Protocol 3.14 §7.3.13.2"
)

// defaultFlagWords are the settings that hold flags 0-79, 16 per word.
// Settings 0, 1, 60 and 61 hold Flags0-3 (§7.3.13.2, §7.3.13.3). Setting 82
// holds 16 more "permanent" flag bits, numbered 64-79 here: the name
// "Permanent flags 0" is community-reported (FINDINGS.md §12.2); that it is
// a word of flag bits is reverse-engineered (#6), INFERRED. The catalogue
// names none of its bits.
var defaultFlagWords = []uint16{0, 1, 60, 61, 82}

// chargeCurveAdaptiveBatterySafe is the raw value of setting 10 that a
// lithium battery does not allow (reverse-engineered (#6), OBSERVED).
const chargeCurveAdaptiveBatterySafe = 3

// resetByRevision lists variables that need a device reset after a change
// on firmware revisions (firmware version mod 1000) in [minRev, maxRev].
// Reverse-engineered (#6), OBSERVED: the original tool marks them so.
var resetByRevision = []struct {
	minRev, maxRev uint32
	ids            []ID
}{
	{0, 308, []ID{SettingID(50), SettingID(51), SettingID(64)}},
	{300, 308, []ID{FlagID(36), SettingID(3), SettingID(11)}},
	{0, 199, []ID{SettingID(65)}},
	{308, 308, []ID{SettingID(81), FlagID(37)}},
}

func setting(n uint16, name, unit string, tab Tab, group, src string) Entry {
	return Entry{Kind: Setting, Num: n, Name: name, Unit: unit, Tab: tab, Group: group, Source: src}
}

func flag(n uint16, name string, tab Tab, group, src string) Entry {
	return Entry{Kind: Flag, Num: n, Name: name, Tab: tab, Group: group, Source: src}
}

func ram(n uint16, name, unit, src string) Entry {
	return Entry{Kind: RAM, Num: n, Name: name, Unit: unit, Tab: TabMonitor, Group: "Readings", Source: src}
}

func (e Entry) word() Entry { e.FlagWord = true; return e }

func (e Entry) relativeTo(base uint16, sign int) Entry {
	e.RelativeTo = &Relative{Base: base, Sign: sign}
	return e
}

func (e Entry) choices(c map[uint16]string) Entry { e.Choices = c; return e }

func (e Entry) needsReset() Entry { e.ResetRequired = true; return e }

func (e Entry) inverseOf(n uint16) Entry { e.InverseOf = &n; return e }

func (e Entry) notPromoted() Entry { e.NotPromoted = true; return e }

var defaultEntries = []Entry{
	// Settings, §7.3.13.2.
	setting(0, "Flags0", "", TabNone, "Flag words", srcSettings).word(),
	setting(1, "Flags1", "", TabNone, "Flag words", srcSettings).word(),
	setting(2, "UBatAbsorption", "V", TabCharger, "Charge voltages and current", srcSettings),
	setting(3, "UBatFloat", "V", TabCharger, "Charge voltages and current", srcSettings),
	setting(4, "IBatBulk", "A", TabCharger, "Charge voltages and current", srcSettings),
	setting(5, "UInvSetpoint", "V", TabInverter, "Output", srcSettings),
	setting(6, "IMainsLimit (AC1)", "A", TabGeneral, "AC input current", srcSettings),
	setting(7, "Repeated Absorption Time", "min", TabCharger, "Absorption", srcSettings),
	setting(8, "Repeated Absorption Interval", "min", TabCharger, "Absorption", srcSettings),
	setting(9, "(Maximum) Absorption duration", "min", TabCharger, "Absorption", srcSettings),
	// Values reverse-engineered (#6), OBSERVED; own wording.
	setting(10, "Charge characteristic", "", TabCharger, "Battery type", srcSettings).
		choices(map[uint16]string{1: "Fixed", 2: "Adaptive", chargeCurveAdaptiveBatterySafe: "Adaptive + BatterySafe"}),
	setting(11, "UBatLowLimit for Inverter", "V", TabInverter, "DC input low", srcSettings),
	// The restart level is setting 11 + setting 12 (§7.3.13.2, ID 63; the
	// VEConfigure manual §9 item 5 says the same).
	setting(12, "UBatLow hysteresis for Inverter", "V", TabInverter, "DC input low", srcSettings).relativeTo(11, +1),
	setting(13, "Number of slaves connected", "", TabNone, "System configuration", srcSettings),
	setting(14, "Special three phase setting", "", TabNone, "System configuration", srcSettings).
		choices(map[uint16]string{0: "3 phase", 1: "Split phase 180", 2: "2 leg 3 phase 120"}),

	// Virtual switch settings, §7.3.13.4. Values 0-2 are from the document;
	// 3-6 are reverse-engineered (#6), INFERRED (own wording).
	setting(15, "vsUsage", "", TabVirtualSwitch, "Usage", srcVSSettings).
		choices(map[uint16]string{
			0: "Not used", 1: "Use VS to control Relay", 2: "Use VS to ignore AC input",
			3: "VS2 ignores AC input", 4: "VS2 controls a generator",
			5: "VS controls relay and ignores AC input", 6: "VS ignores AC input, VS2 controls a generator",
		}),
	setting(16, "vsonIInvHigh", "A", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(17, "vsonUBatHigh", "V", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(18, "vsonUBatLow", "V", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(19, "vstonIInvHigh", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(20, "vstonUBatHigh", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(21, "vstonUBatLow", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(22, "vstonNotCharging", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(23, "vstonFanOn", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(24, "vstonTemperatureAlarm", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(25, "vstonLowBatteryAlarm", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(26, "vstonOverloadAlarm", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(27, "vstonUBatRippleAlarm", "min", TabVirtualSwitch, "On conditions", srcVSSettings),
	setting(28, "vsoffIInvLow", "A", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(29, "vsoffUBatHigh", "V", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(30, "vsoffUBatLow", "V", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(31, "vstoffIInvLow", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(32, "vstoffUBatHigh", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(33, "vstoffUBatLow", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(34, "vstoffCharging", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(35, "vstoffFanOff", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(36, "vstoffChargeBulkFinished", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(37, "vstoffNoVSOnCondition", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(38, "vstoffNoACInput", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(39, "vstoffTemperatureAlarm", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(40, "vstoffLowBatteryAlarm", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(41, "vstoffOverloadAlarm", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(42, "vstoffUBatRippleAlarm", "min", TabVirtualSwitch, "Off conditions", srcVSSettings),
	setting(43, "vsMinimumOnTime", "min", TabVirtualSwitch, "Options", srcVSSettings),

	setting(44, "Lowest acceptable UMains", "V", TabGrid, "AC input voltage limits", srcSettings),
	// A hysteresis above 44: the connect level is 44 + 45.
	setting(45, "Hysteresis for parameter 44", "V", TabGrid, "AC input voltage limits", srcSettings).relativeTo(44, +1),
	setting(46, "Highest acceptable UMains", "V", TabGrid, "AC input voltage limits", srcSettings),
	// A hysteresis below 46: the connect level is 46 - 47.
	setting(47, "Hysteresis for parameter 46", "V", TabGrid, "AC input voltage limits", srcSettings).relativeTo(46, -1),
	setting(48, "Assist current boost factor", "", TabInverter, "PowerAssist", srcSettings),
	setting(49, "IMainsLimit (AC2)", "A", TabGeneral, "AC input current", srcSettings),
	setting(50, "Low current limit for switching to AES", "A", TabInverter, "AES", srcSettings).needsReset(),
	// "leave AES when current > Settings 50+51".
	setting(51, "Hysteresis on AES current limit", "A", TabInverter, "AES", srcSettings).needsReset().relativeTo(50, +1),

	setting(52, "vs2onILoadHigh", "A", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(53, "vs2onILoadHigh (time)", "min", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(54, "vs2onUBatLow", "V", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(55, "vs2onUBatLow (time)", "min", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(56, "vs2offILoadLow", "A", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(57, "vs2offILoadLow (time)", "min", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(58, "vs2offUBatHigh", "V", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(59, "vs2offUBatHigh (time)", "min", TabVirtualSwitch, "Virtual switch 2", srcVSSettings),
	setting(60, "Flags2", "", TabNone, "Flag words", srcSettings).word(),
	setting(61, "Flags3", "", TabNone, "Flag words", srcSettings).word(),
	setting(62, "vsInverterPeriodTime", "", TabVirtualSwitch, "Options", srcVSSettings),
	// "Must be added to (UBatLowLimit + UBatLowHysteresis)": relative to the
	// restart level, which is itself 11 + 12. The 0x8000 that the document
	// says is added is expected to be cancelled by the device-reported
	// Offset (not confirmed on hardware).
	setting(63, "UBat low pre-alarm offset", "V", TabInverter, "DC input low", srcSettings).relativeTo(12, +1),
	setting(64, "Battery capacity", "Ah", TabGeneral, "Battery monitor", srcSettings),
	setting(65, "Charge percentage at end of bulk", "%", TabGeneral, "Battery monitor", srcOwnSet65),

	// Settings 66-88, community names (FINDINGS.md §12.2). 76-80 ("Vebus
	// config. Ignore") and 85 ("Info ID 0") are left out: they are not user
	// settings. The frequency shift (66-69, flag 35) is on the virtual switch
	// options page, as in the original tool (reverse-engineered, #6).
	setting(66, "Frequency shift Ubat start", "V", TabVirtualSwitch, "Options", srcCommSet),
	setting(67, "Frequency shift start delay", "", TabVirtualSwitch, "Options", srcCommSet),
	setting(68, "Frequency shift Ubat stop", "V", TabVirtualSwitch, "Options", srcCommSet),
	setting(69, "Frequency shift stop delay", "", TabVirtualSwitch, "Options", srcCommSet),
	setting(70, "VS2 on SOC level", "", TabVirtualSwitch, "Virtual switch 2", srcCommSet),
	// Not on the original tool's Advanced tab: it is for assistants to use.
	setting(71, "Assistant temp compensation", "", TabNone, "Assistants", srcCommSet),
	setting(72, "Charge efficiency", "", TabGeneral, "Battery monitor", srcCommSet),
	setting(73, "Inverter current limit during assist", "A", TabInverter, "PowerAssist", srcCommSet),
	setting(74, "SOC reset value when switching from absorption to float", "", TabGeneral, "Battery monitor", srcCommSet),
	setting(75, "Battery current for stop absorption", "A", TabCharger, "Absorption", srcCommSet),
	setting(81, "Grid code", "", TabGrid, "Grid code", srcCommSet),
	setting(82, "Permanent flags 0", "", TabNone, "Flag words", srcCommSet).word(),
	setting(83, "SOC low shutdown", "", TabInverter, "State of charge shutdown", srcCommSet),
	setting(84, "SOC low restart", "", TabInverter, "State of charge shutdown", srcCommSet),
	setting(86, "Stop charging at temperature", "", TabCharger, "Temperature", srcCommSet),
	setting(87, "Temperature compensation slope", "", TabCharger, "Temperature", srcCommSet),
	setting(88, "DoNotChargeVoltage", "V", TabAdvanced, "Renewable energy priority", srcCommSet),

	// Flags, §7.3.13.3.
	flag(0, "MultiPhaseSystem", TabNone, "System configuration", srcFlags),
	flag(1, "MultiPhaseLeader", TabNone, "System configuration", srcFlags),
	flag(2, "60Hz", TabGeneral, "System frequency", srcFlags),
	flag(3, "Disable Wave Check (fast input voltage detection)", TabGrid, "UPS function", srcFlags),
	flag(4, "DoNotStopAfter10HrBulk", TabCharger, "Charger", srcFlags),
	flag(5, "AssistEnabled", TabInverter, "PowerAssist", srcFlags),
	flag(6, "DisableCharge", TabCharger, "Charger", srcFlags),
	// "IMPORTANT: Must have inverted value of flags[3]." Kept in step with
	// flag 3 by SetFlag, so it has no tab of its own.
	flag(7, "Inverse of Disable Wave Check", TabNone, "Do not change", srcOwnFlag7).inverseOf(3),
	flag(8, "DisableAES", TabInverter, "AES", srcFlags),
	flag(9, "Not promoted option", TabNone, "Do not change", srcFlags).notPromoted(),
	flag(10, "Not promoted option", TabNone, "Do not change", srcFlags).notPromoted(),
	flag(11, "EnableReducedFloat", TabCharger, "Battery type", srcFlags),
	flag(12, "Not promoted option", TabNone, "Do not change", srcFlags).notPromoted(),
	flag(13, "Disable ground relay", TabInverter, "Output", srcFlags),
	flag(14, "Weak AC input", TabCharger, "Charger", srcFlags),
	flag(15, "Remote overrules AC2", TabGeneral, "AC input current", srcFlags),

	// Virtual switch flags, §7.3.13.5.
	flag(16, "vsonBulkProtection", TabVirtualSwitch, "On conditions", srcVSFlags),
	flag(17, "vsonTemperaturePreAlarm", TabVirtualSwitch, "On conditions", srcVSFlags),
	flag(18, "vsonLowBatteryPreAlarm", TabVirtualSwitch, "On conditions", srcVSFlags),
	flag(19, "vsonOverloadPreAlarm", TabVirtualSwitch, "On conditions", srcVSFlags),
	flag(20, "vsonUBatRipplePreAlarm", TabVirtualSwitch, "On conditions", srcVSFlags),
	flag(21, "vsoffTemperaturePreAlarm", TabVirtualSwitch, "Off conditions", srcVSFlags),
	flag(22, "vsoffLowBatteryPreAlarm", TabVirtualSwitch, "Off conditions", srcVSFlags),
	flag(23, "vsoffOverloadPreAlarm", TabVirtualSwitch, "Off conditions", srcVSFlags),
	flag(24, "vsoffUBatRipplePreAlarm", TabVirtualSwitch, "Off conditions", srcVSFlags),
	flag(25, "vsonWhenGeneralFailure", TabVirtualSwitch, "On conditions", srcVSFlags),
	flag(26, "vsInvert", TabVirtualSwitch, "Options", srcVSFlags),

	flag(27, "Accept wide input frequency", TabGrid, "AC input frequency", srcFlags),
	flag(28, "Dynamic current limiter", TabGeneral, "AC input current", srcFlags),
	flag(29, "Use tubular plate traction battery curve", TabCharger, "Battery type", srcFlags),
	flag(30, "Remote overrules AC1", TabGeneral, "AC input current", srcFlags),
	flag(31, "Use Low Power Shutdown in AES instead of modified sinewave", TabInverter, "AES", srcFlags),
	flag(32, "vs2offWhenAC1Available", TabVirtualSwitch, "Virtual switch 2", srcVSFlags),
	flag(33, "vs2Invert", TabVirtualSwitch, "Virtual switch 2", srcVSFlags),
	flag(34, "vsSetInverterPeriodTime", TabVirtualSwitch, "Options", srcVSFlags),

	// Flags 35-41, community names (FINDINGS.md §12.3). The document calls
	// 35-63 unused.
	flag(35, "Frequency changes by Ubat", TabVirtualSwitch, "Options", srcCommFlags),
	flag(36, "Lithium battery", TabCharger, "Battery type", srcCommFlags),
	flag(37, "Allow enable feed-in", TabGrid, "Grid code", srcCommFlags),
	flag(38, "External mains current sensor connected", TabGeneral, "AC input current", srcCommFlags),
	flag(39, "Configured for VE.Bus BMS", TabCharger, "Battery type", srcCommFlags),
	flag(40, "Disable Ubat VSense (for diagnostic purposes)", TabCharger, "Charger", srcCommFlags),
	flag(41, "Prefer renewable energy (Solar & Wind priority)", TabAdvanced, "Renewable energy priority", srcCommFlags),

	// RAM variables, §7.3.13.1.
	ram(0, "UMainsRMS", "V", srcRAM),
	ram(1, "IMainsRMS", "A", srcRAM),
	ram(2, "UInverterRMS", "V", srcRAM),
	ram(3, "IInverterRMS", "A", srcRAM),
	ram(4, "UBat", "V", srcRAM),
	ram(5, "IBat", "A", srcRAM),
	ram(6, "UBatRMS", "V", srcRAM),
	ram(7, "Inverter Period Time", "", srcRAM),
	ram(8, "Mains Period Time", "", srcRAM),
	ram(9, "Signed AC Load Current", "A", srcRAM),
	ram(10, "Virtual switch position", "", srcRAM),
	ram(11, "Ignore AC input state", "", srcRAM),
	ram(12, "Multi functional relay state", "", srcRAM),
	ram(13, "Charge state", "", srcRAM),
	ram(14, "Inverter Power (filtered)", "W", srcRAM),
	ram(15, "Input Power (filtered)", "W", srcOwnRAM),
	ram(16, "Output power (filtered)", "W", srcRAM),
	ram(17, "Inverter Power (unfiltered)", "W", srcOwnRAM),
	ram(18, "Input Power (unfiltered)", "W", srcOwnRAM),
	ram(19, "Output power (unfiltered)", "W", srcOwnRAM),

	// RAM variables 20-37, community names (FINDINGS.md §12.4). 20, 22, 25
	// and 27-31 are left out ("To be defined", a duplicate name, "Reserved").
	ram(21, "Battery temperature", "", srcCommRAM),
	ram(23, "Command RAM flags", "", srcCommRAM),
	ram(24, "AC Output Current split phase L2", "A", srcCommRAM),
	ram(26, "Info RAM flags", "", srcCommRAM),
	ram(32, "AC Input Voltage split phase L2", "V", srcCommRAM),
	ram(33, "AC Input Current split phase L2", "A", srcCommRAM),
	ram(34, "Instantaneous Input Power split phase L2", "W", srcCommRAM),
	ram(35, "Input Power split phase L2", "W", srcCommRAM),
	ram(36, "Instantaneous Output Power split phase L2", "W", srcCommRAM),
	ram(37, "Output Power split phase L2", "W", srcCommRAM),
}
