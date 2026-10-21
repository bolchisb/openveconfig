package rvsc

import (
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// TestBuildRoundTrip checks that a file Build makes parses back to the same
// tool version, setting info and unit values.
func TestBuildRoundTrip(t *testing.T) {
	info := &SettingInfoTable{
		FirmwareWord: 2699481,
		Settings: []mk2.SettingInfo{
			{Sc: 0}, // an unsupported setting
			{Sc: 1, Offset: 0, Default: 100, Min: 10, Max: 200},
			{Sc: -2, Offset: -5, Default: 1234, Min: 0, Max: 0xFFFF},
			{Sc: 10, Offset: 0, Default: 7, Min: 1, Max: 9},
		},
	}
	saved := time.Unix(1_700_000_000, 0).UTC()
	in := BuildUnit{
		LongID:       0x10000001,
		FirmwareWord: 2699481,
		Serial:       "HQ1234ABCDE",
		SaveTime:     saved,
		Settings:     []uint16{0, 150, 1234, 5},
	}
	f, err := Build("openveconfig", info, []BuildUnit{in})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	g, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse of built file: %v", err)
	}

	if v, err := g.ToolVersion(); err != nil || v != "openveconfig" {
		t.Errorf("ToolVersion = %q, %v", v, err)
	}
	gi, err := g.SettingInfo()
	if err != nil {
		t.Fatalf("SettingInfo: %v", err)
	}
	if gi.FirmwareWord != info.FirmwareWord {
		t.Errorf("FirmwareWord = %d, want %d", gi.FirmwareWord, info.FirmwareWord)
	}
	if len(gi.Settings) != len(info.Settings) {
		t.Fatalf("Settings len = %d, want %d", len(gi.Settings), len(info.Settings))
	}
	for i, s := range info.Settings {
		if gi.Settings[i] != s {
			t.Errorf("setting info %d = %+v, want %+v", i, gi.Settings[i], s)
		}
	}
	// Grid info was not written.
	if grid, err := g.GridInfo(); err != nil || grid != nil {
		t.Errorf("GridInfo = %v, %v; want nil, nil", grid, err)
	}

	units, err := g.Units()
	if err != nil {
		t.Fatalf("Units: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1", len(units))
	}
	u := units[0]
	if u.LongID != in.LongID {
		t.Errorf("LongID = %#x, want %#x", u.LongID, in.LongID)
	}
	if u.Firmware() != 2699481 {
		t.Errorf("Firmware = %d, want 2699481", u.Firmware())
	}
	if u.Serial != in.Serial {
		t.Errorf("Serial = %q, want %q", u.Serial, in.Serial)
	}
	if u.UploadForm {
		t.Errorf("UploadForm = true, want false (device form)")
	}
	if !u.SaveTime.Equal(saved) {
		t.Errorf("SaveTime = %v, want %v", u.SaveTime, saved)
	}
	if u.NumSettings() != len(in.Settings) {
		t.Fatalf("NumSettings = %d, want %d", u.NumSettings(), len(in.Settings))
	}
	for id, want := range in.Settings {
		if got, err := u.Setting(id); err != nil || got != want {
			t.Errorf("setting %d = %d, %v; want %d", id, got, err, want)
		}
	}
}

// TestBuildRejectsMismatch checks Build refuses a unit whose setting count
// differs from the setting-info table.
func TestBuildRejectsMismatch(t *testing.T) {
	info := &SettingInfoTable{Settings: make([]mk2.SettingInfo, 3)}
	_, err := Build("", info, []BuildUnit{{Settings: []uint16{1, 2}}})
	if err == nil {
		t.Fatal("Build accepted a unit with the wrong setting count")
	}
}
