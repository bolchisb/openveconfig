package firmware

import (
	"context"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// TestBackupSettings reads a simulator device, builds a .rvsc backup, and
// checks that the file parses back and matches the device: same firmware,
// same setting info and the same raw value for every setting the device
// supports. MatchRVSC accepting the file is what a later restore relies on.
func TestBackupSettings(t *testing.T) {
	profile := simulator.Synthetic48V120()
	sim := simulator.New(simulator.Config{
		Devices:         []simulator.Device{{Address: 0, Profile: profile}},
		VersionInterval: -1,
	})
	c := mk2.NewClient(sim.Port(), nil)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Read the device once as the source of truth.
	want, _, err := vebus.ReadAll(ctx, c, 0, nil, vebus.ReadOptions{RAM: []uint16{}})
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	file, err := BackupSettings(ctx, c, 0, nil)
	if err != nil {
		t.Fatalf("BackupSettings: %v", err)
	}
	data, err := file.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}

	// Parse the bytes back, as loading the saved file would.
	g, err := rvsc.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	gi, err := g.SettingInfo()
	if err != nil {
		t.Fatalf("SettingInfo: %v", err)
	}
	units, err := g.Units()
	if err != nil {
		t.Fatalf("Units: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1", len(units))
	}
	u := units[0]
	if u.Firmware() != want.Firmware {
		t.Errorf("file firmware %d, device %d", u.Firmware(), want.Firmware)
	}

	// The file must match the device it was read from.
	m := vebus.MatchRVSC(gi, u, want)
	if err := m.Err(); err != nil {
		t.Fatalf("MatchRVSC: %v\n%s", err, m)
	}

	// Every supported setting's raw value survives the round trip.
	for _, id := range want.SettingIDs() {
		raw, _ := want.Raw(vebus.SettingID(id))
		got, err := u.Setting(int(id))
		if err != nil || got != raw {
			t.Errorf("setting %d: file %d, %v; device %d", id, got, err, raw)
		}
	}

	// FromRVSC rebuilds the same supported settings from the file.
	fileDev, err := vebus.FromRVSC(gi, u, nil)
	if err != nil {
		t.Fatalf("FromRVSC: %v", err)
	}
	if got, w := len(fileDev.SettingIDs()), len(want.SettingIDs()); got != w {
		t.Errorf("file model has %d settings, device %d", got, w)
	}
}

// TestBackupNoSettings reports an error rather than writing an empty file when
// the device has no settings.
func TestBackupNoSettings(t *testing.T) {
	if _, err := backupFile(vebus.NewDevice(nil)); err == nil {
		t.Fatal("backupFile accepted a device with no settings")
	}
}
