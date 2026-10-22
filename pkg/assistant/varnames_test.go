package assistant

import "testing"

func TestSettingName(t *testing.T) {
	if len(varNames) != 311 {
		t.Fatalf("%d names, want 311", len(varNames))
	}
	for id, want := range map[int]string{
		0:  "eestFlags#0",
		4:  "eestIBatBulk",
		6:  "eestIMainsLimit",
		64: "eestBatteryCapacity",
		81: "eecvGridCode",
	} {
		if got, ok := SettingName(id); !ok || got != want {
			t.Errorf("SettingName(%d) = %q %v, want %q", id, got, ok, want)
		}
	}
	// Out of range either way, and the names the original skips.
	for _, id := range []int{-1, 192, 1000} {
		if got, ok := SettingName(id); ok {
			t.Errorf("SettingName(%d) = %q, want none", id, got)
		}
	}
	// Setting 13 is named @eestObsoleteNrOfSlaves, one of the 90 the
	// original skips.
	if got, ok := SettingName(13); ok {
		t.Errorf("SettingName(13) = %q, want none", got)
	}
}
