package simulator

import "github.com/bolchisb/openveconfig/pkg/mk2"

// Setting describes one simulated setting (§7.3.8, §7.3.13.2). Its value
// starts at Default.
type Setting struct {
	ID   uint16
	Name string
	// Sc, Offset, Default, Min and Max are returned by
	// CommandGetSettingInfo, raw as on the wire.
	Sc, Offset        int16
	Default, Min, Max uint16
	// AccessLevel is returned in Winmon mode 2. CommandWriteViaID is
	// refused when it is above Config.AccessLevel.
	AccessLevel byte
}

// RAMVar describes one simulated RAM variable (§7.3.9, §7.3.13.1).
type RAMVar struct {
	ID         uint16
	Name       string
	Sc, Offset int16
	// Value is the raw starting value.
	Value uint16
}

// Profile describes one simulated VE.Bus device. Use a constructor such as
// Synthetic12V230 or build one; every value in it is reported verbatim.
type Profile struct {
	Name string
	// Version is the firmware version (§7.3.1).
	Version uint32
	// State is the starting device state (§7.3.2).
	State mk2.DeviceState
	// LED is the LED status ('L', §4) when this device is the master.
	LED mk2.LEDStatus
	// MinInputLimit, MaxInputLimit and InputLimit are the input current
	// limits in 0.1 A of the MasterMultiLED frame (§6.2), used when this
	// device is the master. InputLimit is the starting actual limit.
	MinInputLimit, MaxInputLimit, InputLimit uint16
	// ACInputConfig is the AC input configuration byte of the
	// MasterMultiLED frame (§6.2), reported verbatim; bit 2 means a remote
	// panel may override the input current limit.
	ACInputConfig byte
	Settings      []Setting
	// RAMVars also feed the info frames (§6.1): IDs 0-3 the AC frame, 4,
	// 5 and 7 the DC frame, 8 the AC mains period.
	RAMVars []RAMVar
}

// ramDefs are the RAM variables of the synthetic profiles. IDs and names
// are from §7.3.13.1. Sc and Offset of IDs 0-13 are the example values of
// Appendix 2; those of 14 and 16 are made up. 15 and 17-19 are left out.
var ramDefs = []RAMVar{
	{ID: 0, Name: "UMainsRMS", Sc: 32668},
	{ID: 1, Name: "IMainsRMS", Sc: 32668},
	{ID: 2, Name: "UInverterRMS", Sc: 32668},
	{ID: 3, Name: "IInverterRMS", Sc: 32668},
	{ID: 4, Name: "UBat", Sc: 32668},
	{ID: 5, Name: "IBat", Sc: -32668},
	{ID: 6, Name: "UBatRMS", Sc: 32668},
	{ID: 7, Name: "InverterPeriodTime", Sc: 30807, Offset: 256},
	{ID: 8, Name: "MainsPeriodTime", Sc: 31791},
	{ID: 9, Name: "SignedACLoadCurrent", Sc: -32668},
	{ID: 10, Name: "VirtualSwitchPosition", Sc: 4, Offset: mk2.BitOffset},
	{ID: 11, Name: "IgnoreACInputState", Sc: 1, Offset: mk2.BitOffset},
	{ID: 12, Name: "MultiFunctionalRelayState", Sc: 6, Offset: mk2.BitOffset},
	{ID: 13, Name: "ChargeState", Sc: 32568},
	{ID: 14, Name: "InverterPowerFiltered", Sc: -1},
	{ID: 16, Name: "OutputPowerFiltered", Sc: -1},
}

// ramVars returns ramDefs with the given raw values; missing IDs are 0.
func ramVars(values map[uint16]int32) []RAMVar {
	vs := make([]RAMVar, len(ramDefs))
	for i, r := range ramDefs {
		r.Value = uint16(values[r.ID]) // negative values wrap to int16 form
		vs[i] = r
	}
	return vs
}

// Synthetic12V230 is a made-up 12 V / 230 V 50 Hz inverter/charger on
// mains, charging in absorption. All values are invented; setting IDs and
// names are from §7.3.13.2. It does not describe any real product.
func Synthetic12V230() Profile {
	return Profile{
		Name:          "Synthetic 12 V / 230 V inverter/charger",
		Version:       2699481, // invented, xxyyCCC format of §2
		State:         mk2.DeviceState{State: 9, SubState: 2},
		LED:           mk2.LEDStatus{On: mk2.LEDMains | mk2.LEDAbsorption},
		MinInputLimit: 20, MaxInputLimit: 500, InputLimit: 160,
		ACInputConfig: 1 << 2, // invented: a remote panel may override the limit
		Settings: []Setting{
			{ID: 0, Name: "Flags0", Sc: 1, Default: 0x0080, Max: 0x69FF},
			{ID: 1, Name: "Flags1", Sc: 1, Default: 0x0000, Max: 0x7000},
			{ID: 2, Name: "UBatAbsorption", Sc: -100, Default: 1440, Min: 800, Max: 1750},
			{ID: 3, Name: "UBatFloat", Sc: -100, Default: 1380, Min: 800, Max: 1750},
			{ID: 4, Name: "IBatBulk", Sc: 1, Default: 70, Min: 0, Max: 70},
			{ID: 5, Name: "UInvSetpoint", Sc: 1, Default: 230, Min: 210, Max: 245},
			{ID: 6, Name: "IMainsLimitAC1", Sc: -10, Default: 160, Min: 20, Max: 500},
			{ID: 8, Name: "RepeatedAbsorptionInterval", Sc: 60, Default: 168, Min: 1, Max: 720},
			{ID: 11, Name: "UBatLowLimit", Sc: -100, Default: 930, Min: 800, Max: 1300},
			{ID: 12, Name: "UBatLowHysteresis", Sc: -100, Default: 120, Min: 0, Max: 400},
			{ID: 13, Name: "NumberOfSlaves", Sc: 1, Default: 0, Min: 0, Max: 5, AccessLevel: 2},
			// §7.3.13.2: signed value stored with 0x8000 added.
			{ID: 63, Name: "UBatLowPreAlarmOffset", Sc: -100, Offset: -0x8000, Default: 0x8000 + 20, Min: 0x8000 - 200, Max: 0x8000 + 200},
			{ID: 64, Name: "BatteryCapacity", Sc: 1, Default: 200, Min: 1, Max: 3000},
			{ID: 65, Name: "ChargePercentAtAbsorption", Sc: 1, Default: 85, Min: 0, Max: 100},
		},
		RAMVars: ramVars(map[uint16]int32{
			0: 23050, 1: 412, 2: 23050, 3: 95, // 230.50 V, 4.12 A
			4: 1352, 5: 2150, 6: 4, // 13.52 V, +21.50 A (charging)
			7: 136, 8: 195, // about 50 Hz
			9: 95, 12: 1 << 5, // relay on (bit 5)
			13: 16200, 14: 290, 16: 210, // 81 %, W
		}),
	}
}

// GridSettings returns simulated settings for the grid code (setting 81) and
// the grid words (settings 128-191), so a device accepts the grid send of
// #52 (gridcode.SendPlan). All start at 0 with the full 16-bit range; the
// grid code (81) allows codes 0-32. passwordRequired lists IDs (81 or
// 128-191) that are given an access level above 0, to model a device that
// refuses them for want of the installer password: a write to one is refused
// with "access level required" when Config.AccessPasswords is set and the
// host has not been granted access (see Config.AccessPasswords). All values
// are invented; the IDs are from gridcode and §7.3.13.2.
func GridSettings(passwordRequired ...uint16) []Setting {
	locked := func(id uint16) byte {
		for _, p := range passwordRequired {
			if p == id {
				return 0xFF
			}
		}
		return 0
	}
	ss := []Setting{{ID: 81, Name: "GridCode", Sc: 1, Default: 0, Min: 0, Max: 32, AccessLevel: locked(81)}}
	for id := uint16(128); id <= 191; id++ {
		ss = append(ss, Setting{ID: id, Name: "GridWord", Sc: 1, Default: 0, Min: 0, Max: 0xFFFF, AccessLevel: locked(id)})
	}
	return ss
}

// Synthetic48V120 is a made-up 48 V / 120 V 60 Hz inverter/charger
// inverting from its battery with no mains. All values are invented;
// setting IDs and names are from §7.3.13.2. It does not describe any real
// product.
func Synthetic48V120() Profile {
	return Profile{
		Name:          "Synthetic 48 V / 120 V inverter/charger",
		Version:       2799482, // invented, xxyyCCC format of §2
		State:         mk2.DeviceState{State: 4},
		LED:           mk2.LEDStatus{On: mk2.LEDInverter},
		MinInputLimit: 40, MaxInputLimit: 500, InputLimit: 300,
		Settings: []Setting{
			{ID: 0, Name: "Flags0", Sc: 1, Default: 0x00A4, Max: 0x69FF}, // 60 Hz, assist
			{ID: 1, Name: "Flags1", Sc: 1, Default: 0x0000, Max: 0x7000},
			{ID: 2, Name: "UBatAbsorption", Sc: -100, Default: 5760, Min: 3200, Max: 7000},
			{ID: 3, Name: "UBatFloat", Sc: -100, Default: 5520, Min: 3200, Max: 7000},
			{ID: 4, Name: "IBatBulk", Sc: 1, Default: 35, Min: 0, Max: 35},
			{ID: 5, Name: "UInvSetpoint", Sc: 1, Default: 120, Min: 100, Max: 130},
			{ID: 6, Name: "IMainsLimitAC1", Sc: -10, Default: 300, Min: 40, Max: 500},
			{ID: 8, Name: "RepeatedAbsorptionInterval", Sc: 60, Default: 168, Min: 1, Max: 720},
			{ID: 11, Name: "UBatLowLimit", Sc: -100, Default: 3720, Min: 3200, Max: 5200},
			{ID: 12, Name: "UBatLowHysteresis", Sc: -100, Default: 480, Min: 0, Max: 1600},
			{ID: 13, Name: "NumberOfSlaves", Sc: 1, Default: 0, Min: 0, Max: 5, AccessLevel: 2},
			{ID: 63, Name: "UBatLowPreAlarmOffset", Sc: -100, Offset: -0x8000, Default: 0x8000 + 80, Min: 0x8000 - 800, Max: 0x8000 + 800},
			{ID: 64, Name: "BatteryCapacity", Sc: 1, Default: 400, Min: 1, Max: 3000},
			{ID: 65, Name: "ChargePercentAtAbsorption", Sc: 1, Default: 85, Min: 0, Max: 100},
		},
		RAMVars: ramVars(map[uint16]int32{
			2: 12010, 3: 1830, // no mains; 120.10 V, 18.30 A out
			4: 5168, 5: -4420, 6: 21, // 51.68 V, -44.20 A (discharging)
			7: 71,               // about 60 Hz
			9: 1830, 10: 1 << 3, // virtual switch on (bit 3)
			13: 12600, 14: -2196, 16: 2196, // 63 %, W
		}),
	}
}
