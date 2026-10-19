package vebus

import (
	"errors"
	"strings"
	"testing"
)

func TestRejectOutOfRange(t *testing.T) {
	d := testDevice()
	for _, v := range []float64{7.99, 17.51, -1, 1e9} {
		err := d.SetDisplay(SettingID(3), v)
		var is *Issue
		if !errors.As(err, &is) || is.Rule != "range" {
			t.Errorf("SetDisplay(3, %v) = %v, want a range issue", v, err)
			continue
		}
		if !strings.Contains(is.Message, "allowed range 8 to 17.5 V") || !strings.Contains(is.Message, "UBatFloat (setting 3)") {
			t.Errorf("message %q does not state the name and allowed range", is.Message)
		}
	}
	if raw, _ := d.Raw(SettingID(3)); raw != 1380 {
		t.Errorf("rejected value was stored: raw %d", raw)
	}
	if err := d.SetRaw(SettingID(3), 1751); err == nil || !strings.Contains(err.Error(), "8 to 17.5 V") {
		t.Errorf("SetRaw out of range: %v", err)
	}
	// ValidateDisplay checks without storing.
	if err := d.ValidateDisplay(SettingID(3), 20); err == nil {
		t.Error("ValidateDisplay accepted 20")
	}
	if err := d.ValidateDisplay(SettingID(3), 14); err != nil {
		t.Errorf("ValidateDisplay(14): %v", err)
	}
	if raw, _ := d.Raw(SettingID(3)); raw != 1380 {
		t.Error("ValidateDisplay stored a value")
	}
}

func TestRejectOffStep(t *testing.T) {
	d := testDevice()
	for _, tc := range []struct {
		id   ID
		v    float64
		want string
	}{
		{SettingID(3), 13.805, "allowed are 8 to 17.5 V in steps of 0.01 V"},
		{SettingID(8), 90, "allowed are 60 to 43200 min in steps of 60 min"}, // Sc 60
		{SettingID(50), 2.05, "in steps of 0.1 A"},                           // Sc -10
	} {
		err := d.SetDisplay(tc.id, tc.v)
		var is *Issue
		if !errors.As(err, &is) || is.Rule != "step" {
			t.Errorf("SetDisplay(%s, %v) = %v, want a step issue", tc.id, tc.v, err)
			continue
		}
		if !strings.Contains(is.Message, tc.want) {
			t.Errorf("message %q does not contain %q", is.Message, tc.want)
		}
	}
}

// findIssue returns the issue with rule key, if any.
func findIssue(issues []Issue, key string) *Issue {
	for i := range issues {
		if issues[i].Rule == key {
			return &issues[i]
		}
	}
	return nil
}

func TestValidateCleanDevice(t *testing.T) {
	d := testDevice()
	if issues := d.Validate(); len(issues) != 0 {
		t.Errorf("issues on a consistent device: %+v", issues)
	}
	if err := d.CheckWrite(false); err != nil {
		t.Error(err)
	}
}

// TestCrossRules covers each cross-setting rule: a broken rule is an Error
// when the user changed a value involved, a Warning when the device already
// held it, and warning-only rules are never Errors.
func TestCrossRules(t *testing.T) {
	for _, tc := range []struct {
		key      string
		source   string
		warnOnly bool
		break_   func(d *Device) error // as a user edit
		device   func(d *Device)       // as read from the device
	}{
		{
			key: "flag-inverse", source: "MK2 Protocol 3.14 §7.3.13.3",
			// A user edit through SetFlag keeps the pair consistent, so break
			// it through the word.
			break_: func(d *Device) error { return d.SetRaw(SettingID(0), 0x0088) },
			device: func(d *Device) { d.AddSetting(0, settingInfo(1, 0, 0, 0, 0x69FF), 0x0000) },
		},
		{
			key: "float-above-absorption", source: "reverse-engineered, #6",
			break_: func(d *Device) error { return d.SetDisplay(SettingID(3), 14.5) },
			device: func(d *Device) { d.AddSetting(3, settingInfo(-100, 0, 1380, 800, 1750), 1450) },
		},
		{
			key: "soc-restart-below-shutdown", source: "reverse-engineered, #6",
			break_: func(d *Device) error { return d.SetDisplay(SettingID(84), 5) },
			device: func(d *Device) { d.AddSetting(84, settingInfo(1, 0, 20, 0, 100), 5) },
		},
		{
			key: "ac-connect-levels", source: "reverse-engineered, #6", warnOnly: true,
			// Low connect 220 + 30 = 250, high connect 265 - 30 = 235.
			break_: func(d *Device) error {
				_ = d.SetDisplay(SettingID(44), 220)
				_ = d.SetDisplay(SettingID(45), 30)
				return d.SetDisplay(SettingID(47), 30)
			},
			device: func(d *Device) {
				d.AddSetting(44, settingInfo(1, 0, 180, 150, 220), 220)
				d.AddSetting(45, settingInfo(1, 0, 7, 0, 30), 30)
				d.AddSetting(47, settingInfo(1, 0, 5, 0, 30), 30)
			},
		},
		{
			key: "repeated-absorption", source: "reverse-engineered, #6", warnOnly: true,
			break_: func(d *Device) error { return d.SetDisplay(SettingID(8), 60) },
			device: func(d *Device) { d.AddSetting(8, settingInfo(60, 0, 168, 1, 720), 1) },
		},
		{
			key: "lithium-storage-mode", source: "reverse-engineered, #6",
			break_: func(d *Device) error { _ = d.SetFlag(11, true); return d.SetFlag(36, true) },
			device: func(d *Device) {
				d.AddSetting(0, settingInfo(1, 0, 0, 0, 0x69FF), 0x0880)
				d.AddSetting(60, settingInfo(1, 0, 0, 0, 0x0A11), 0x0010)
			},
		},
		{
			key: "lithium-traction-curve", source: "reverse-engineered, #6",
			break_: func(d *Device) error { _ = d.SetFlag(29, true); return d.SetFlag(36, true) },
			device: func(d *Device) {
				d.AddSetting(1, settingInfo(1, 0, 0, 0, 0x7000), 0x2000)
				d.AddSetting(60, settingInfo(1, 0, 0, 0, 0x0A11), 0x0010)
			},
		},
		{
			key: "lithium-charge-curve", source: "reverse-engineered, #6",
			break_: func(d *Device) error { _ = d.SetRaw(SettingID(10), 3); return d.SetFlag(36, true) },
			device: func(d *Device) {
				d.AddSetting(10, settingInfo(1, 0, 2, 1, 3), 3)
				d.AddSetting(60, settingInfo(1, 0, 0, 0, 0x0A11), 0x0010)
			},
		},
	} {
		t.Run(tc.key, func(t *testing.T) {
			d := testDevice()
			if err := tc.break_(d); err != nil {
				t.Fatal(err)
			}
			is := findIssue(d.Validate(), tc.key)
			if is == nil {
				t.Fatalf("no %s issue in %+v", tc.key, d.Validate())
			}
			want := Error
			if tc.warnOnly {
				want = Warning
			}
			if is.Severity != want || !strings.HasPrefix(is.Source, tc.source) || is.Message == "" {
				t.Errorf("issue = %+v, want severity %s, source %q", is, want, tc.source)
			}
			err := d.CheckWrite(false)
			if (err != nil) != (want == Error) {
				t.Errorf("CheckWrite = %v", err)
			}
			if err := d.CheckWrite(true); err != nil {
				t.Errorf("CheckWrite(override) = %v", err)
			}

			// The same state as read from the device is only a warning.
			d = testDevice()
			tc.device(d)
			is = findIssue(d.Validate(), tc.key)
			if is == nil || is.Severity != Warning {
				t.Errorf("device state: issue = %+v, want a warning", is)
			}
			if err := d.CheckWrite(false); err != nil {
				t.Errorf("CheckWrite on device state = %v", err)
			}
		})
	}
}

func TestCrossRuleSkippedWhenUnsupported(t *testing.T) {
	d := testDevice()
	_ = d.SetFlag(11, true)
	// Flag 36 lives in setting 60; without it the lithium rules do not apply.
	delete(d.settings, 60)
	if is := findIssue(d.Validate(), "lithium-storage-mode"); is != nil {
		t.Errorf("rule applied without flag 36: %+v", is)
	}
}

func TestFlagWordRules(t *testing.T) {
	d := testDevice()
	d.AddSetting(0, settingInfo(1, 0, 0x0080, 0, 0x6BFF), 0x0080) // bit 9 supported
	// Flag 9 is a "not promoted option" (§7.3.13.3).
	if err := d.SetFlag(9, true); err != nil {
		t.Fatal(err)
	}
	// Bit 11 of Flags2 (flag 43) is supported by this device but unnamed.
	if err := d.SetFlag(43, true); err != nil {
		t.Fatal(err)
	}
	issues := d.Validate()
	if is := findIssue(issues, "flag-not-promoted"); is == nil || is.Severity != Error || is.ID != FlagID(9) {
		t.Errorf("not-promoted issue = %+v", is)
	}
	if is := findIssue(issues, "flag-unknown"); is == nil || is.Severity != Error || is.ID != FlagID(43) {
		t.Errorf("unknown-flag issue = %+v", is)
	}
	var ve *ValidationError
	if err := d.CheckWrite(false); !errors.As(err, &ve) || len(ve.Issues) != 2 {
		t.Errorf("CheckWrite = %v", err)
	}
	// Unsupported bits read from a file or device are reported too.
	d = testDevice()
	d.settings[0].value = 0x1080
	if is := findIssue(d.Validate(), "flag-unsupported"); is == nil || is.Severity != Error {
		t.Errorf("unsupported-bit issue = %+v", is)
	}
}

func TestValidateRangeOfReadValues(t *testing.T) {
	d := testDevice()
	// The device reports a value outside its own limits: a warning only.
	d.AddSetting(2, settingInfo(-100, 0, 1440, 800, 1750), 1800)
	is := findIssue(d.Validate(), "range")
	if is == nil || is.Severity != Warning || !strings.Contains(is.Message, "allowed range 8 to 17.5 V") {
		t.Errorf("issue = %+v", is)
	}
	// The same value from a file (never read from the device) is an error.
	d.settings[2].hasRead = false
	if is := findIssue(d.Validate(), "range"); is == nil || is.Severity != Error {
		t.Errorf("issue = %+v", is)
	}
	if err := d.CheckWrite(false); err == nil {
		t.Error("CheckWrite passed with an out-of-range value")
	}
	if err := d.CheckWrite(true); err != nil {
		t.Errorf("CheckWrite(override) = %v", err)
	}
}

func TestValidateUnsupported(t *testing.T) {
	d := testDevice()
	for _, id := range []ID{SettingID(4), FlagID(12), RAMID(0)} {
		if err := d.ValidateRaw(id, 0); !errors.Is(err, ErrUnsupported) {
			t.Errorf("ValidateRaw(%s) = %v", id, err)
		}
	}
}
