package main

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// vsDevice returns a synthetic device whose virtual switch drives the
// relay, with a condition (load 16 for 19), an LED alarm (flag 17 for 24),
// the minimum on time and the inverter period settings.
func vsDevice() *vebus.Device {
	d := vebus.NewDevice(nil)
	d.AddSetting(1, mk2.SettingInfo{Sc: 1, Max: 0x03FF}, 0)  // flags 16-25
	d.AddSetting(60, mk2.SettingInfo{Sc: 1, Max: 0x000C}, 0) // flags 34 and 35
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Min: 800, Max: 1750}, 1440)
	d.AddSetting(5, mk2.SettingInfo{Sc: 1, Default: 230, Min: 210, Max: 245}, 230)
	d.AddSetting(15, mk2.SettingInfo{Sc: 1, Max: 6}, 1)
	d.AddSetting(16, mk2.SettingInfo{Sc: -100, Max: 17217}, 1000)
	d.AddSetting(19, mk2.SettingInfo{Sc: -60, Offset: -1, Max: 255}, 0)
	d.AddSetting(24, mk2.SettingInfo{Sc: -60, Offset: -1, Max: 255}, 0)
	d.AddSetting(43, mk2.SettingInfo{Sc: 1, Max: 1200}, 0)
	d.AddSetting(62, mk2.SettingInfo{Sc: -2500, Default: 50000, Min: 38461, Max: 55555}, 50000)
	d.AddSetting(66, mk2.SettingInfo{Sc: -100, Max: 7000}, 5772)
	d.AddSetting(67, mk2.SettingInfo{Sc: -60, Offset: -1, Min: 1, Max: 255}, 3)
	return d
}

// vsSub returns the sub-page of the virtual switch page p for group.
func vsSub(t *testing.T, p *settingsPage, group string) *settingsPage {
	t.Helper()
	i := slices.IndexFunc(p.sub, func(s *settingsPage) bool { return s.groups[0] == group })
	if i < 0 {
		t.Fatalf("no sub-page %q", group)
	}
	return p.sub[i]
}

func isDisabled(o fyne.CanvasObject) bool { return o.(fyne.Disableable).Disabled() }

func vsRaw(t *testing.T, d *vebus.Device, n uint16) uint16 {
	t.Helper()
	v, err := d.Raw(vebus.SettingID(n))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestVSConditionLine checks a condition line: both settings on one line,
// the level disabled while the delay is off and enabled once one is
// entered, one revert for both, and an error under the line.
func TestVSConditionLine(t *testing.T) {
	u := newTestUI(t)
	p := newSubPages(u, vebus.TabVirtualSwitch)
	d := vsDevice()
	u.settings.load(d, nil)
	on := vsSub(t, p, "On conditions")
	level, delay := rowOf(t, on, vebus.SettingID(16)), rowOf(t, on, vebus.SettingID(19))
	l := level.line
	if l == nil || delay.line != l {
		t.Fatal("load level and its delay are not on one line")
	}
	if l.label.Text != "When load higher than" || level.entry.Text != "2300" || delay.entry.Text != "-1" {
		t.Errorf("line %q [%s] for [%s], want when load higher than [2300] for [-1]", l.label.Text, level.entry.Text, delay.entry.Text)
	}
	if !isDisabled(level.entry) || isDisabled(delay.entry) || l.label.Importance != widget.LowImportance {
		t.Error("with the delay off, the level is not disabled, or the delay is")
	}

	enter(delay.formRow, "5")
	enter(level.formRow, "4600")
	if vsRaw(t, d, 19) != 6 || vsRaw(t, d, 16) != 2000 {
		t.Errorf("raw delay %d, level %d; want 6 and 2000", vsRaw(t, d, 19), vsRaw(t, d, 16))
	}
	if isDisabled(level.entry) || l.label.Importance == widget.LowImportance || !marked(l.formRow) {
		t.Error("after entering a delay: the level is still disabled, or the line is not marked as changed")
	}

	enter(level.formRow, "99999")
	if msg := errorText(l.formRow); !strings.Contains(msg, "Enter a value from") {
		t.Errorf("refused level: error %q under the line", msg)
	}
	level.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if errorText(l.formRow) != "" || level.entry.Text != "4600" {
		t.Errorf("after Escape: error %q, text %q", errorText(l.formRow), level.entry.Text)
	}

	test.Tap(l.revert)
	if vsRaw(t, d, 19) != 0 || vsRaw(t, d, 16) != 1000 || marked(l.formRow) || !isDisabled(level.entry) {
		t.Errorf("after revert: raw delay %d, level %d, marked %v; want both read values, unmarked, level disabled",
			vsRaw(t, d, 19), vsRaw(t, d, 16), marked(l.formRow))
	}
}

// TestVSAlarmLine checks that an LED alarm's choice sets its flag and is
// disabled while its delay is off.
func TestVSAlarmLine(t *testing.T) {
	u := newTestUI(t)
	p := newSubPages(u, vebus.TabVirtualSwitch)
	d := vsDevice()
	u.settings.load(d, nil)
	on := vsSub(t, p, "On conditions")
	sel := rowOf(t, on, vebus.FlagID(17)).focus.(*widget.Select)
	delay := rowOf(t, on, vebus.SettingID(24))
	if !slices.Equal(sel.Options, []string{"Temperature alarm", "Temperature pre-alarm"}) || sel.Selected != "Temperature alarm" {
		t.Errorf("choice %q, selected %q", sel.Options, sel.Selected)
	}
	if !sel.Disabled() {
		t.Error("the alarm choice is enabled while its delay is off")
	}
	enter(delay.formRow, "1")
	if sel.Disabled() {
		t.Fatal("the alarm choice is disabled with a delay")
	}
	sel.SetSelectedIndex(1)
	if on, _ := d.Flag(17); !on {
		t.Error("pre-alarm did not set flag 17")
	}
	sel.SetSelectedIndex(0)
	if on, _ := d.Flag(17); on {
		t.Error("alarm did not clear flag 17")
	}
}

// TestVSOptions checks the period time in ms and Hz and which flags enable
// the period-time lines.
func TestVSOptions(t *testing.T) {
	u := newTestUI(t)
	p := newSubPages(u, vebus.TabVirtualSwitch)
	d := vsDevice()
	u.settings.load(d, nil)
	opt := vsSub(t, p, "Options")
	period := rowOf(t, opt, vebus.SettingID(62))
	hz := period.line.entries[1]
	shiftUp, shiftDelay := rowOf(t, opt, vebus.SettingID(66)), rowOf(t, opt, vebus.SettingID(67))
	minimum := rowOf(t, opt, vebus.SettingID(43))
	if period.entry.Text != "20.0000" || hz.Text != "50.00" {
		t.Errorf("period %q ms = %q Hz, want 20.0000 and 50.00", period.entry.Text, hz.Text)
	}
	check := func(what string, wantPeriod, wantShift bool) {
		t.Helper()
		if !isDisabled(period.entry) != wantPeriod || !hz.Disabled() != wantPeriod {
			t.Errorf("%s: period enabled %v, want %v", what, !isDisabled(period.entry), wantPeriod)
		}
		if !isDisabled(shiftUp.entry) != wantShift || !isDisabled(shiftDelay.entry) != wantShift {
			t.Errorf("%s: DC voltage condition enabled %v, want %v", what, !isDisabled(shiftUp.entry), wantShift)
		}
		if isDisabled(minimum.entry) {
			t.Errorf("%s: minimum on time disabled", what)
		}
	}
	check("both flags off", false, false)
	test.Tap(rowOf(t, opt, vebus.FlagID(34)).focus.(*widget.Check))
	check("flag 34 on", true, false)

	hz.SetText("60")
	hz.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if vsRaw(t, d, 62) != 41667 || period.entry.Text != "16.6668" || hz.Text != "60.00" {
		t.Errorf("60 Hz: raw %d, %q ms, %q Hz; want 41667, 16.6668, 60.00", vsRaw(t, d, 62), period.entry.Text, hz.Text)
	}
	enter(period.formRow, "20")
	if vsRaw(t, d, 62) != 50000 || hz.Text != "50.00" {
		t.Errorf("20 ms: raw %d, %q Hz; want 50000, 50.00", vsRaw(t, d, 62), hz.Text)
	}
	hz.SetText("70")
	hz.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if msg := errorText(period.line.formRow); !strings.Contains(msg, "45.00 Hz") || !strings.Contains(msg, "65.00 Hz") || vsRaw(t, d, 62) != 50000 {
		t.Errorf("70 Hz: error %q, raw %d; want refused with the range in Hz", msg, vsRaw(t, d, 62))
	}
	hz.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})

	test.Tap(rowOf(t, opt, vebus.FlagID(34)).focus.(*widget.Check))
	test.Tap(rowOf(t, opt, vebus.FlagID(35)).focus.(*widget.Check))
	check("flag 35 on", true, true)
}

// TestVSFind checks that Find shows a virtual switch setting's sub-page
// and focuses its control.
func TestVSFind(t *testing.T) {
	u := newTestUI(t)
	d := vsDevice()
	if err := d.SetRaw(vebus.SettingID(19), 31); err != nil { // 30 s
		t.Fatal(err)
	}
	u.settings.load(d, nil)
	find := func(id vebus.ID) (fyne.Focusable, string) {
		t.Helper()
		e := mustEntry(t, d, id)
		for _, nav := range u.settings.navigators {
			if nav(e) {
				break
			}
		}
		tabs := findObjects(u.current.view, func(o fyne.CanvasObject) bool { _, ok := o.(*container.AppTabs); return ok })
		if u.current.title != "Virtual switch" || len(tabs) == 0 {
			t.Fatalf("Find %v shows page %q", id, u.current.title)
		}
		return u.win.Canvas().Focused(), tabs[0].(*container.AppTabs).Selected().Text
	}
	if f, tab := find(vebus.SettingID(19)); tab != "A: Set VS ON" || f == nil || f.(*numberEntry).Text != "30" {
		t.Errorf("Find setting 19: sub-tab %q, focused %T; want the delay entry on A: Set VS ON", tab, f)
	}
	if f, tab := find(vebus.SettingID(62)); tab != "VS options" || f == nil || f.(*numberEntry).Text != "20.0000" {
		t.Errorf("Find setting 62: sub-tab %q, focused %T; want the period entry on VS options", tab, f)
	}
	// An alarm whose delay is off: its choice is disabled, so Find focuses
	// the delay (the only -1 on the page), which turns it on.
	if f, _ := find(vebus.FlagID(17)); f == nil || f.(*numberEntry).Text != "-1" {
		t.Errorf("Find flag 17 with its delay off focuses %T, want the delay entry", f)
	}
	if err := d.SetRaw(vebus.SettingID(24), 2); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()
	if f, _ := find(vebus.FlagID(17)); f == nil {
		t.Error("Find flag 17 focuses nothing")
	} else if _, ok := f.(*widget.Select); !ok {
		t.Errorf("Find flag 17 focuses %T, want its choice", f)
	}
}
