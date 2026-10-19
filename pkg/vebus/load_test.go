package vebus

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
)

// simDevices are the devices of the simulator that "veconfig-cli
// --simulator" uses; testdata/dump-simulator-addr*.json were written by
// "veconfig-cli --simulator --baud 2400 [--address 1] dump" against it.
func simDevices() []simulator.Device {
	return []simulator.Device{
		{Address: 0, Profile: simulator.Synthetic12V230()},
		{Address: 1, Profile: simulator.Synthetic48V120()},
	}
}

func loadSim(t *testing.T, addr byte) *Device {
	t.Helper()
	sim := simulator.New(simulator.Config{Devices: simDevices(), VersionInterval: -1})
	c := mk2.NewClient(sim.Port(), nil)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, err := Load(ctx, c, addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLoadSimulator(t *testing.T) {
	d := loadSim(t, 0)
	p := simulator.Synthetic12V230()
	if d.Address != 0 || d.Firmware != p.Version {
		t.Errorf("address %d, firmware %d", d.Address, d.Firmware)
	}
	// Exactly the profile's settings are supported, with its info and values.
	var want []uint16
	for _, s := range p.Settings {
		want = append(want, s.ID)
		info, ok := d.SettingInfo(s.ID)
		if !ok || info.Sc != s.Sc || info.Offset != s.Offset || info.Default != s.Default || info.Min != s.Min || info.Max != s.Max {
			t.Errorf("setting %d: info %+v, %v", s.ID, info, ok)
		}
		if raw, _ := d.Raw(SettingID(s.ID)); raw != s.Default {
			t.Errorf("setting %d: raw %d, want %d", s.ID, raw, s.Default)
		}
	}
	if got := d.SettingIDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("SettingIDs = %v, want %v", got, want)
	}
	if len(d.RAMIDs()) != 16 {
		t.Errorf("RAMIDs = %v", d.RAMIDs())
	}
	// Unsupported settings are hidden; flags follow their word's Maximum.
	for id, want := range map[ID]bool{
		SettingID(7): false, SettingID(63): true, FlagID(7): true, FlagID(12): false,
		FlagID(29): true, FlagID(36): false, RAMID(15): false, RAMID(16): true,
	} {
		if d.Supported(id) != want {
			t.Errorf("Supported(%s) = %v", id, !want)
		}
	}
	for _, e := range d.Entries() {
		if e.ID() == SettingID(7) || e.ID() == FlagID(12) {
			t.Errorf("%s shown", e.ID())
		}
	}
	// Display values from the profile's synthetic numbers.
	for id, want := range map[ID]float64{
		SettingID(2): 14.4, SettingID(6): 16, SettingID(63): 0.2, RAMID(4): 13.52, RAMID(12): 1,
	} {
		if got := display(t, d, id); !near(got, want) {
			t.Errorf("Display(%s) = %v, want %v", id, got, want)
		}
	}
	if got, _ := d.Absolute(63); !near(got, 10.7) { // 9.3 + 1.2 + 0.2
		t.Errorf("Absolute(63) = %v", got)
	}
	if issues := d.Validate(); len(issues) != 0 {
		t.Errorf("issues: %+v", issues)
	}
	if len(d.ChangedIDs()) != 0 {
		t.Errorf("changes after load: %v", d.ChangedIDs())
	}
}

// TestLoadDump loads dumps written by the CLI and checks they give the same
// model as reading the simulator directly.
func TestLoadDump(t *testing.T) {
	for _, tc := range []struct {
		file string
		addr byte
	}{
		{"testdata/dump-simulator-addr0.json", 0},
		{"testdata/dump-simulator-addr1.json", 1},
	} {
		f, err := os.Open(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		got, err := LoadDump(f, nil)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		want := loadSim(t, tc.addr)
		if got.Address != want.Address || got.Firmware != want.Firmware {
			t.Errorf("%s: address/firmware %d/%d, want %d/%d", tc.file, got.Address, got.Firmware, want.Address, want.Firmware)
		}
		if !reflect.DeepEqual(got.settings, want.settings) {
			t.Errorf("%s: settings differ from the simulator", tc.file)
		}
		if !reflect.DeepEqual(got.ram, want.ram) {
			t.Errorf("%s: RAM variables differ from the simulator", tc.file)
		}
	}
}

func TestLoadDumpFields(t *testing.T) {
	const js = `{
	  "address": 2, "firmware": 2699481,
	  "settings": [
	    {"id": 0, "raw": 128, "info": {"sc": 1, "offset": 0, "default": 128, "min": 0, "max": 27135, "access_level": 129}},
	    {"id": 150, "raw": 65535, "info": {"sc": -100, "offset": 0, "default": 0, "min": 0, "max": 1000}}
	  ],
	  "ram": [{"id": 11, "raw": 1, "info": {"sc": 1, "offset": -32768}}],
	  "errors": [{"kind": "setting", "id": 13, "error": "timeout"}],
	  "future_field": true
	}`
	d, err := LoadDump(strings.NewReader(js), nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Address != 2 {
		t.Errorf("address %d", d.Address)
	}
	if a, ok := d.Access(FlagID(7)); !ok || !a.PasswordRequired() || !a.ResetRequired() {
		t.Errorf("Access = %#x, %v", a, ok)
	}
	if !d.NotApplicable(SettingID(150)) {
		t.Error("setting 150 applicable")
	}
	if v, _ := d.Display(RAMID(11)); v != 1 {
		t.Errorf("RAM 11 = %v", v)
	}
	for _, bad := range []string{`{`, `{"settings": []}`, `{"address": 32}`} {
		if _, err := LoadDump(strings.NewReader(bad), nil); err == nil {
			t.Errorf("LoadDump(%s) succeeded", bad)
		}
	}
}
