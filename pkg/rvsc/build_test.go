package rvsc

import (
	"bytes"
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

// TestBuildDeviceProvidedFields checks the #74 superset: when the caller
// supplies the device-provided parts (setting-info byte8, access region and
// grid table; per-unit version-extra bytes, response char and system record)
// Build carries them and they parse back. All data is synthetic.
func TestBuildDeviceProvidedFields(t *testing.T) {
	grid := []byte{0xfb, 0x06, 0x03, 0x21, 0x08, 0x11, 0x22} // synthetic grid table
	access := make([]byte, 276)                              // synthetic access region
	for i := range access {
		access[i] = 0xee
	}
	info := &SettingInfoTable{
		FirmwareWord: 2699481,
		HeaderByte8:  0x2f,
		Settings: []mk2.SettingInfo{
			{Sc: 1, Offset: 0, Default: 100, Min: 10, Max: 200},
			{Sc: 2, Offset: 0, Default: 7, Min: 1, Max: 9},
		},
		AccessRegion: access,
		GridTable:    grid,
	}
	in := BuildUnit{
		LongID:       0x10000001,
		FirmwareWord: 2699481,
		Serial:       "HQ1234ABCDE",
		SaveTime:     time.Unix(1_700_000_000, 0).UTC(),
		Settings:     []uint16{150, 5},
		VersionExtra: []byte{0x08, 0x1d, 0x00, 0x00, 0x42, 0x10},
		ResponseChar: 0x2f,
		SystemRecord: [SystemRecordLen]byte{1, 1, 1, 0, 0, 0, 0x04, 0xf0, 0x02},
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
		t.Fatalf("Parse: %v", err)
	}

	gi, err := g.SettingInfo()
	if err != nil {
		t.Fatalf("SettingInfo: %v", err)
	}
	if gi.HeaderByte8 != 0x2f {
		t.Errorf("HeaderByte8 = %#x, want 0x2f", gi.HeaderByte8)
	}
	if got, err := g.GridInfo(); err != nil || !bytes.Equal(got, grid) {
		t.Errorf("GridInfo = % x, %v; want % x", got, err, grid)
	}

	units, err := g.Units()
	if err != nil {
		t.Fatalf("Units: %v", err)
	}
	u := units[0]
	if !bytes.Equal(u.VersionExtra, in.VersionExtra) {
		t.Errorf("VersionExtra = % x, want % x", u.VersionExtra, in.VersionExtra)
	}
	if u.ResponseChar != in.ResponseChar {
		t.Errorf("ResponseChar = %#x, want %#x", u.ResponseChar, in.ResponseChar)
	}
	if rec := u.SystemRecord(); rec != in.SystemRecord {
		t.Errorf("SystemRecord = % x, want % x", rec, in.SystemRecord)
	}
	// Bytes 6, 7, 8 of the system record are the phase, assistant flag and index.
	if u.Phase != 0x04 || u.AssistantFlag != 0xf0 || u.Index != 0x02 {
		t.Errorf("phase/flag/index = %#x/%#x/%#x, want 0x04/0xf0/0x02", u.Phase, u.AssistantFlag, u.Index)
	}
	// Setting values still survive the superset.
	for id, want := range in.Settings {
		if got, err := u.Setting(id); err != nil || got != want {
			t.Errorf("setting %d = %d, %v; want %d", id, got, err, want)
		}
	}
}

// TestBuildFromScratchDefaults checks a from-scratch unit (no device download:
// no device-provided fields supplied). Build must produce a structurally valid
// file: the default byte8, no grid table, an empty-but-present assistant area
// and zeroed device-provided fields.
func TestBuildFromScratchDefaults(t *testing.T) {
	info := &SettingInfoTable{
		FirmwareWord: 1234567,
		Settings:     []mk2.SettingInfo{{Sc: 1, Default: 1}, {Sc: 2, Default: 2}},
	}
	in := BuildUnit{
		Serial:   "FROMSCRATCH",
		SaveTime: time.Unix(1_700_000_000, 0).UTC(),
		Settings: []uint16{11, 22},
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
		t.Fatalf("Parse: %v", err)
	}

	gi, err := g.SettingInfo()
	if err != nil {
		t.Fatalf("SettingInfo: %v", err)
	}
	if gi.HeaderByte8 != infoHeaderByte8 {
		t.Errorf("HeaderByte8 = %#x, want default %#x", gi.HeaderByte8, infoHeaderByte8)
	}
	if grid, err := g.GridInfo(); err != nil || grid != nil {
		t.Errorf("GridInfo = % x, %v; want nil, nil", grid, err)
	}
	units, err := g.Units()
	if err != nil {
		t.Fatalf("Units: %v", err)
	}
	u := units[0]
	// The assistant area is present but empty: the compact form expands to no
	// bytes, and it is not a zero-length area.
	if len(u.AssistantArea()) == 0 {
		t.Error("assistant area is zero-length, want the minimal compact empty area")
	}
	if img, err := u.AssistantImage(); err != nil || len(img) != 0 {
		t.Errorf("AssistantImage = %d bytes, %v; want empty", len(img), err)
	}
	// Device-provided fields default to zero but the header is well-formed.
	if rec := u.SystemRecord(); rec != ([SystemRecordLen]byte{}) {
		t.Errorf("SystemRecord = % x, want zero", rec)
	}
	if u.ResponseChar != 0 || !allZero(u.VersionExtra) {
		t.Errorf("response char %#x / version extra % x, want zero", u.ResponseChar, u.VersionExtra)
	}
	if u.IdentifyFlag != 1 {
		t.Errorf("IdentifyFlag = %d, want 1", u.IdentifyFlag)
	}
}

// TestBuildRejectsBadVersionExtra checks Build refuses version-extra bytes of
// the wrong length (the fixed header offsets assume six).
func TestBuildRejectsBadVersionExtra(t *testing.T) {
	info := &SettingInfoTable{Settings: []mk2.SettingInfo{{Sc: 1}}}
	_, err := Build("", info, []BuildUnit{{Settings: []uint16{1}, VersionExtra: []byte{1, 2, 3}}})
	if err == nil {
		t.Fatal("Build accepted version-extra bytes of the wrong length")
	}
}
