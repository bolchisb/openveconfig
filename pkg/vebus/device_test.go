package vebus

import (
	"errors"
	"math"
	"testing"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// All values in these tests are synthetic.

func settingInfo(sc, offset int16, def, min, max uint16) mk2.SettingInfo {
	return mk2.SettingInfo{Sc: sc, Offset: offset, Default: def, Min: min, Max: max}
}

// testDevice is a synthetic 12 V device with the settings the tests use.
func testDevice() *Device {
	d := NewDevice(nil)
	d.Firmware = 2699481
	d.AddSetting(0, settingInfo(1, 0, 0x0080, 0, 0x69FF), 0x0080) // flags 0-8, 11, 13, 14 supported; 7 set
	d.AddSetting(1, settingInfo(1, 0, 0, 0, 0x7000), 0)
	d.AddSetting(2, settingInfo(-100, 0, 1440, 800, 1750), 1440)
	d.AddSetting(3, settingInfo(-100, 0, 1380, 800, 1750), 1380)
	d.AddSetting(7, settingInfo(60, 0, 1, 0, 24), 1)
	d.AddSetting(8, settingInfo(60, 0, 168, 1, 720), 168)
	d.AddSetting(10, settingInfo(1, 0, 2, 1, 3), 2)
	d.AddSetting(11, settingInfo(-100, 0, 930, 800, 1300), 930)
	d.AddSetting(12, settingInfo(-100, 0, 120, 0, 400), 120)
	d.AddSetting(44, settingInfo(1, 0, 180, 150, 220), 180)
	d.AddSetting(45, settingInfo(1, 0, 7, 0, 30), 7)
	d.AddSetting(46, settingInfo(1, 0, 265, 240, 280), 265)
	d.AddSetting(47, settingInfo(1, 0, 5, 0, 30), 5)
	d.AddSetting(50, mk2.SettingInfo{Sc: -10, Max: 100, HasAccessLevel: true, AccessLevel: 0x80}, 20)
	d.AddSetting(60, settingInfo(1, 0, 0, 0, 0x0A11), 0) // flags 32, 36, 41 supported (bits 0, 4, 9) and bit 11
	d.AddSetting(63, settingInfo(-100, -0x8000, 0x8000+20, 0x8000-200, 0x8000+200), 0x8000+20)
	d.AddSetting(83, settingInfo(1, 0, 10, 0, 100), 10)
	d.AddSetting(84, settingInfo(1, 0, 20, 0, 100), 20)
	d.AddSetting(150, settingInfo(-100, 0, 0, 0, 1000), NotApplicable) // not in the catalogue
	d.AddSetting(7000, settingInfo(0, 0, 0, 0, 0), 0)                  // Sc 0: unsupported
	d.AddRAM(4, mk2.RAMInfo{Sc: 32668}, 1352)
	d.AddRAM(5, mk2.RAMInfo{Sc: -32668}, uint16(0xFFEA)) // -22 -> -0.22 A
	d.AddRAM(7, mk2.RAMInfo{Sc: 30807, Offset: 256}, 136)
	d.AddRAM(12, mk2.RAMInfo{Sc: 6, Offset: mk2.BitOffset}, 0x0021) // bit 5, plus bit 0 set
	return d
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func display(t *testing.T, d *Device, id ID) float64 {
	t.Helper()
	v, err := d.Display(id)
	if err != nil {
		t.Fatalf("Display(%s): %v", id, err)
	}
	return v
}

func TestDisplayScaling(t *testing.T) {
	d := testDevice()
	for _, tc := range []struct {
		id   ID
		want float64
	}{
		{SettingID(2), 14.4},             // Sc -100: scale 1/100 (§7.3.8)
		{SettingID(8), 168 * 60},         // Sc 60: scale 60
		{SettingID(50), 2},               // Sc -10
		{SettingID(63), 0.2},             // Offset -0x8000 cancels the 0x8000 bias (§7.3.13.2)
		{RAMID(4), 13.52},                // RAM Sc 32668: 1/(0x8000-32668) = 1/100 (§7.3.9)
		{RAMID(5), -0.22},                // RAM Sc < 0: signed
		{RAMID(7), (136 + 256) / 1961.0}, // RAM offset; Sc 30807: 1/1961
		{RAMID(12), 1},                   // single-bit RAM variable, bit Sc-1 = 5
		{FlagID(7), 1}, {FlagID(3), 0},   // flags
	} {
		if got := display(t, d, tc.id); !near(got, tc.want) {
			t.Errorf("Display(%s) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestSettingNegativeScaleEdges(t *testing.T) {
	d := NewDevice(nil)
	// Sc = -32768: the smallest scale, 1/32768.
	d.AddSetting(2, settingInfo(math.MinInt16, 0, 0, 0, 0xFFFE), 32768)
	if got := display(t, d, SettingID(2)); got != 1 {
		t.Errorf("Display = %v, want 1", got)
	}
	if err := d.SetDisplay(SettingID(2), 0.5); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.Raw(SettingID(2)); raw != 16384 {
		t.Errorf("raw = %d, want 16384", raw)
	}
	// Positive Sc with an offset: display = 10 * (raw - 5).
	d.AddSetting(3, settingInfo(10, -5, 5, 0, 100), 5)
	if err := d.SetDisplay(SettingID(3), -50); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.Raw(SettingID(3)); raw != 0 {
		t.Errorf("raw = %d, want 0", raw)
	}
	if err := d.SetDisplay(SettingID(3), 15); err == nil {
		t.Error("15 is off-step for scale 10 but was accepted")
	}
}

func TestSetDisplayRoundTrip(t *testing.T) {
	d := testDevice()
	for _, tc := range []struct {
		id  ID
		v   float64
		raw uint16
	}{
		{SettingID(3), 13.8, 1380}, // 13.8/0.01 is 1379.9999... in float
		{SettingID(3), 8, 800},     // minimum
		{SettingID(3), 17.5, 1750}, // maximum
		{SettingID(63), -1.5, 0x8000 - 150},
		{SettingID(8), 60, 1},
		{RAMID(5), -44.2, uint16(0x10000 - 4420)},
	} {
		if err := d.SetDisplay(tc.id, tc.v); err != nil {
			t.Errorf("SetDisplay(%s, %v): %v", tc.id, tc.v, err)
			continue
		}
		if raw, _ := d.Raw(tc.id); raw != tc.raw {
			t.Errorf("SetDisplay(%s, %v): raw %d, want %d", tc.id, tc.v, raw, tc.raw)
		}
		if got := display(t, d, tc.id); !near(got, tc.v) {
			t.Errorf("Display(%s) = %v after SetDisplay(%v)", tc.id, got, tc.v)
		}
	}
}

func TestRAMBitVariable(t *testing.T) {
	d := testDevice()
	id := RAMID(12)
	if err := d.SetDisplay(id, 0); err != nil {
		t.Fatal(err)
	}
	// Only bit 5 changes; bit 0 is kept.
	if raw, _ := d.Raw(id); raw != 0x0001 {
		t.Errorf("raw = %#04x, want 0x0001", raw)
	}
	if err := d.SetDisplay(id, 1); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.Raw(id); raw != 0x0021 {
		t.Errorf("raw = %#04x, want 0x0021", raw)
	}
	if err := d.SetDisplay(id, 2); err == nil {
		t.Error("2 accepted for a bit variable")
	}
	if s, _ := d.Step(id); s != 1 {
		t.Errorf("Step = %v", s)
	}
	// Signed RAM range: -327.68 to 327.67 A for Sc 1/100.
	if err := d.SetDisplay(RAMID(5), -400); err == nil {
		t.Error("-400 accepted for a signed 1/100 variable")
	}
	// Unsigned RAM rejects negatives.
	if err := d.SetDisplay(RAMID(4), -1); err == nil {
		t.Error("-1 accepted for an unsigned variable")
	}
}

func TestNotApplicable(t *testing.T) {
	d := testDevice()
	if !d.NotApplicable(SettingID(150)) {
		t.Error("0xFFFF not applicable")
	}
	if _, err := d.Display(SettingID(150)); !errors.Is(err, ErrNotApplicable) {
		t.Errorf("Display err = %v, want ErrNotApplicable", err)
	}
	// 0xFFFF is an ordinary value when the device allows it ...
	d.AddSetting(151, settingInfo(1, 0, 0, 0, 0xFFFF), 0xFFFF)
	if d.NotApplicable(SettingID(151)) {
		t.Error("0xFFFF within Max is not applicable")
	}
	if v, err := d.Display(SettingID(151)); err != nil || v != 0xFFFF {
		t.Errorf("Display = %v, %v", v, err)
	}
	// ... and in a flag word.
	d.AddSetting(61, settingInfo(1, 0, 0, 0, 0xFFFF), 0xFFFF)
	if d.NotApplicable(SettingID(61)) {
		t.Error("flag word 0xFFFF is not applicable")
	}
	if v, _ := d.Flag(48); !v {
		t.Error("flag 48 not set")
	}
	// A not-applicable value is not range-checked.
	for _, is := range d.Validate() {
		if is.ID == SettingID(150) {
			t.Errorf("issue for a not-applicable value: %s", is.Message)
		}
	}
}

func TestSupportedAndEntries(t *testing.T) {
	d := testDevice()
	for id, want := range map[ID]bool{
		SettingID(2): true, SettingID(4): false, SettingID(7000): false,
		FlagID(3): true, FlagID(11): true, FlagID(12): false, // 0x69FF: bit 12 clear (§7.3.13.3)
		FlagID(16): false, FlagID(28): true, // Flags1 Max 0x7000
		FlagID(36): true, FlagID(33): false, FlagID(64): false, // no setting 82
		RAMID(4): true, RAMID(0): false,
	} {
		if got := d.Supported(id); got != want {
			t.Errorf("Supported(%s) = %v, want %v", id, got, want)
		}
	}
	shown := map[ID]bool{}
	for _, e := range d.Entries() {
		shown[e.ID()] = true
		if !d.Supported(e.ID()) {
			t.Errorf("unsupported %s shown", e.ID())
		}
	}
	for _, id := range []ID{SettingID(4), FlagID(12), RAMID(0), SettingID(150)} {
		if shown[id] {
			t.Errorf("%s shown", id)
		}
	}
	if !shown[FlagID(11)] || !shown[SettingID(63)] {
		t.Error("supported entries hidden")
	}
	if _, err := d.Display(SettingID(4)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Display(unsupported) err = %v", err)
	}
	if err := d.SetFlag(12, true); !errors.Is(err, ErrUnsupported) {
		t.Errorf("SetFlag(unsupported) err = %v", err)
	}
	if ids := d.SettingIDs(); len(ids) != 19 || ids[len(ids)-1] != 150 {
		t.Errorf("SettingIDs = %v", ids)
	}
}

func TestFlagsReadModifyWrite(t *testing.T) {
	d := testDevice()
	// Default of a flag is its bit of the word's Default.
	if def, _ := d.Default(FlagID(7)); def != 1 {
		t.Errorf("Default(flag 7) = %d", def)
	}
	if err := d.SetFlag(14, true); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.Raw(SettingID(0)); raw != 0x4080 {
		t.Errorf("word = %#04x, want 0x4080", raw)
	}
	if !d.Dirty(FlagID(14)) || d.Dirty(FlagID(7)) || !d.Dirty(SettingID(0)) {
		t.Error("dirty state wrong after SetFlag(14)")
	}
	// Flags 3 and 7 are kept inverse (§7.3.13.3).
	if err := d.SetFlag(3, true); err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Flag(7); v {
		t.Error("flag 7 not cleared with flag 3 set")
	}
	if err := d.SetRaw(FlagID(7), 1); err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Flag(3); v {
		t.Error("flag 3 not cleared with flag 7 set")
	}
	// The write merges only the changed bit (14) into what the device holds now.
	got, err := d.MergeFlagWord(0, 0x0180)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0x4180 {
		t.Errorf("MergeFlagWord = %#04x, want 0x4180", got)
	}
	if _, err := d.MergeFlagWord(2, 0); err == nil {
		t.Error("MergeFlagWord on a non-flag word")
	}
	// A whole flag word may not change unsupported bits.
	if err := d.SetRaw(SettingID(0), 0x1080); err == nil {
		t.Error("unsupported bit 12 accepted")
	}
	if err := d.SetRaw(FlagID(14), 2); err == nil {
		t.Error("flag value 2 accepted")
	}
}

func TestRelativeSettings(t *testing.T) {
	d := testDevice()
	// Restart = 11 + 12 = 9.3 + 1.2; pre-alarm = restart + 63 (§7.3.13.2).
	for id, want := range map[uint16]float64{11: 9.3, 12: 10.5, 63: 10.7, 45: 187, 47: 260} {
		got, err := d.Absolute(id)
		if err != nil || !near(got, want) {
			t.Errorf("Absolute(%d) = %v, %v; want %v", id, got, err, want)
		}
	}
	// Moving the shutdown level moves the levels stored relative to it.
	if err := d.SetDisplay(SettingID(11), 10); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.Absolute(63); !near(got, 11.4) {
		t.Errorf("Absolute(63) = %v, want 11.4", got)
	}
	// SetAbsolute stores the difference.
	if err := d.SetAbsolute(12, 12.5); err != nil {
		t.Fatal(err)
	}
	if raw, _ := d.Raw(SettingID(12)); raw != 250 {
		t.Errorf("raw 12 = %d, want 250", raw)
	}
	if err := d.SetAbsolute(47, 250); err != nil { // 265 - 15
		t.Fatal(err)
	}
	if raw, _ := d.Raw(SettingID(47)); raw != 15 {
		t.Errorf("raw 47 = %d, want 15", raw)
	}
	// Below the shutdown level means a negative hysteresis: out of range.
	if err := d.SetAbsolute(12, 9); err == nil {
		t.Error("restart below shutdown accepted")
	}
	if got, _ := d.Absolute(2); got != 14.4 {
		t.Errorf("Absolute of a plain setting = %v", got)
	}
}

func TestDirtyTracking(t *testing.T) {
	d := testDevice()
	if len(d.ChangedIDs()) != 0 {
		t.Fatalf("fresh model has changes: %v", d.ChangedIDs())
	}
	_ = d.SetDisplay(SettingID(2), 14.6)
	_ = d.SetFlag(36, true)
	_ = d.SetDisplay(RAMID(12), 0)
	want := []ID{SettingID(2), SettingID(60), RAMID(12)}
	got := d.ChangedIDs()
	if len(got) != len(want) {
		t.Fatalf("ChangedIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ChangedIDs = %v, want %v", got, want)
		}
	}
	// Setting the read value back is not a change.
	_ = d.SetDisplay(SettingID(2), 14.4)
	if d.Dirty(SettingID(2)) {
		t.Error("setting 2 dirty at its read value")
	}
	// A confirmed write.
	if err := d.SetRead(SettingID(60), 0x0010); err != nil {
		t.Fatal(err)
	}
	if d.Dirty(FlagID(36)) {
		t.Error("flag 36 dirty after SetRead")
	}
	d.Revert()
	if len(d.ChangedIDs()) != 0 {
		t.Errorf("changes after Revert: %v", d.ChangedIDs())
	}
	if err := d.SetRead(FlagID(3), 1); err == nil {
		t.Error("SetRead on a flag")
	}
}

func TestAccessAndReset(t *testing.T) {
	d := testDevice()
	a, ok := d.Access(SettingID(50))
	if !ok || !a.ResetRequired() || a.PasswordRequired() {
		t.Errorf("Access(50) = %#x, %v", a, ok)
	}
	if _, ok := d.Access(SettingID(2)); ok {
		t.Error("mode 1 setting has an access byte")
	}
	if !Access(0x01).PasswordRequired() {
		t.Error("bit 0 is not password")
	}
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Max: 1750, HasAccessLevel: true, AccessLevel: 0x80}, 1440)
	if !d.ResetRequired(SettingID(2)) {
		t.Error("access bit 7 not reset-required")
	}
	if !d.ResetRequired(SettingID(51)) { // §7.3.13.2 footnote
		t.Error("setting 51 not reset-required")
	}
	if d.ResetRequired(SettingID(64)) {
		t.Error("setting 64 reset-required on revision 481")
	}
	d.Firmware = 2699305 // revision 305
	for _, id := range []ID{SettingID(64), SettingID(3), FlagID(36)} {
		if !d.ResetRequired(id) {
			t.Errorf("%s not reset-required on revision 305", id)
		}
	}
	if d.ResetRequired(SettingID(81)) {
		t.Error("setting 81 reset-required on revision 305")
	}
}
