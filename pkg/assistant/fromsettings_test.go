package assistant

import (
	"testing"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// records is a settings model for the tests: the info records of the
// MultiPlus-II 48/10000/140-100 the spec describes (§8.4), as its backup
// holds them.
func records(m map[int]mk2.SettingInfo) SettingRecords {
	return func(id int) (mk2.SettingInfo, bool) {
		si, ok := m[id]
		return si, ok && si.Sc != 0
	}
}

var multiPlusII = map[int]mk2.SettingInfo{
	setUBatFloat:       {Sc: -100, Default: 5520, Min: 4800, Max: 6400},
	setIBatBulk:        {Sc: 1, Default: 140, Max: 140},
	setUInvSetpoint:    {Sc: 1, Default: 230, Min: 210, Max: 245},
	setUBat2Low:        {Sc: -100, Default: 3720, Min: 3720, Max: 5200},
	setIInvHigh:        {Sc: -100, Default: 3630, Max: 17217},
	setBatteryCapacity: {Sc: 1, Max: 65330},
	setDeviceInfo:      {Sc: 0x0064, Offset: 0x042d, Default: 0x2710, Min: 0x1f40, Max: 0xffff},
}

func TestFromSettings(t *testing.T) {
	var d Device
	d.FromSettings(records(multiPlusII))

	if d.ACVolts != 230 {
		t.Errorf("ACVolts = %v, want 230", d.ACVolts)
	}
	if d.DCCode != 24 {
		t.Errorf("DCCode = %d, want 24 (hwDC 48)", d.DCCode)
	}
	if d.PNominal != 8300 {
		t.Errorf("PNominal = %d, want 8300", d.PNominal)
	}
	if d.WinmonIDOffset != 4 {
		t.Errorf("WinmonIDOffset = %d, want 4", d.WinmonIDOffset)
	}
	if got := d.Extra["cRatedPower"]; got.I != 10000 {
		t.Errorf("cRatedPower = %v, want 10000", got)
	}
	if got := d.Extra["cDeviceInfo"]; got.I != devIsMulti {
		t.Errorf("cDeviceInfo = %v, want %d (a Multi)", got, devIsMulti)
	}
	// The labels the composer then derives from them.
	l := Labels(d)
	for name, want := range map[string]int64{"hwAC": 230, "hwDC": 48, "cIInvNominal": 3630, "cPNominal": 8300} {
		if got := l[name]; got.I != want {
			t.Errorf("%s = %v, want %d", name, got, want)
		}
	}
}

func TestFromSettingsVariants(t *testing.T) {
	// An inverter has no bulk current and no second AC input.
	m := map[int]mk2.SettingInfo{
		setIBatBulk:     {Sc: 1, Max: 0},
		setUInvSetpoint: {Sc: 1, Default: 120},
		setUBat2Low:     {Sc: -100, Default: 1150},
		setIInvHigh:     {Sc: -100, Default: 1000},
		setDeviceInfo:   {Sc: 0x0064, Offset: 0x00ff, Default: 0xffff},
	}
	var d Device
	d.FromSettings(records(m))
	if d.DCCode != 6 {
		t.Errorf("DCCode = %d, want 6 (11.50 V)", d.DCCode)
	}
	if d.Extra["cDeviceInfo"].I != devIsInverter {
		t.Errorf("cDeviceInfo = %v, want %d", d.Extra["cDeviceInfo"], devIsInverter)
	}
	// 10.00 A × 120 V = 1200 W, less the 50 the original takes off.
	if d.PNominal != 1150 {
		t.Errorf("PNominal = %d, want 1150", d.PNominal)
	}
	// 0xFF means the unit gives no Winmon ID offset, and 0xFFFF no rated
	// power, so the nominal power stands in.
	if d.WinmonIDOffset != 0 {
		t.Errorf("WinmonIDOffset = %d, want 0", d.WinmonIDOffset)
	}
	if got := d.Extra["cRatedPower"]; got.I != 1150 {
		t.Errorf("cRatedPower = %v, want 1150", got)
	}

	// A second AC input makes it a Quattro.
	m[setIBatBulk] = mk2.SettingInfo{Sc: 1, Max: 140}
	m[setDeviceInfo] = mk2.SettingInfo{Sc: 0x6464, Offset: 0x042d, Default: 0x2710}
	d = Device{}
	d.FromSettings(records(m))
	if d.Extra["cDeviceInfo"].I != devIsQuattro {
		t.Errorf("cDeviceInfo = %v, want %d", d.Extra["cDeviceInfo"], devIsQuattro)
	}

	// Without any of the settings nothing is filled in.
	d = Device{}
	d.FromSettings(records(nil))
	if d.PNominal != 0 || d.WinmonIDOffset != 0 || d.Extra != nil {
		t.Errorf("empty model filled %+v", d)
	}
}
