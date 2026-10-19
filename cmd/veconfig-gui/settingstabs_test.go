package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/internal/rvsctest"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// settingsSession returns a session on a simulator with one Synthetic12V230
// device at address 0.
func settingsSession(t *testing.T) *session {
	t.Helper()
	s := fakeSession(false)
	s.devices = []byte{0}
	t.Cleanup(func() { s.close() })
	return s
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// readModel reads the settings of the device at addr.
func readModel(t *testing.T, s *session, addr byte) *vebus.Device {
	t.Helper()
	d, rep, err := readSettings(ctxT(t), s, addr, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Failed) > 0 {
		t.Fatalf("failed reads: %v", rep.Failed)
	}
	return d
}

// rowOf returns the row of id on p.
func rowOf(t *testing.T, p *settingsPage, id vebus.ID) *settingRow {
	t.Helper()
	for _, r := range p.rows {
		if r.e.ID() == id {
			return r
		}
	}
	t.Fatalf("no row for %v", id)
	return nil
}

func rowIDs(p *settingsPage) []vebus.ID {
	var ids []vebus.ID
	for _, r := range p.rows {
		ids = append(ids, r.e.ID())
	}
	return ids
}

// findObjects returns the objects under o for which match is true.
func findObjects(o fyne.CanvasObject, match func(fyne.CanvasObject) bool) []fyne.CanvasObject {
	var found []fyne.CanvasObject
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if match(o) {
			found = append(found, o)
		}
		switch w := o.(type) {
		case *fyne.Container:
			for _, c := range w.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return found
}

// labelTexts returns the texts of the labels under o.
func labelTexts(o fyne.CanvasObject) string {
	var texts []string
	for _, l := range findObjects(o, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Label); return ok }) {
		texts = append(texts, l.(*widget.Label).Text)
	}
	return strings.Join(texts, "\n")
}

func TestTabsShowSupportedEntriesOnly(t *testing.T) {
	u := newTestUI(t)
	s := settingsSession(t)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	pages := map[vebus.Tab]*settingsPage{}
	for _, tab := range settingsTabSet {
		pages[tab] = newSettingsPage(u, tab)
	}
	if !pages[vebus.TabGeneral].empty() {
		t.Error("rows before a model is loaded")
	}

	d := readModel(t, s, 0)
	u.settings.load(d, nil)
	for _, tab := range settingsTabSet {
		var want []vebus.ID
		for _, e := range d.Entries() {
			if e.Tab == tab && !e.FlagWord {
				want = append(want, e.ID())
			}
		}
		got := rowIDs(pages[tab])
		cmp := func(a, b vebus.ID) int { return int(a.Kind)<<16 + int(a.Num) - int(b.Kind)<<16 - int(b.Num) }
		slices.SortFunc(got, cmp)
		slices.SortFunc(want, cmp)
		if !slices.Equal(got, want) {
			t.Errorf("tab %v shows %v, want the supported entries %v", tab, got, want)
		}
	}
	// The simulated device has charger and inverter settings but no
	// virtual switch or advanced settings, and not AC input 2 (setting 49).
	for _, id := range []vebus.ID{vebus.SettingID(2), vebus.SettingID(6), vebus.FlagID(5), vebus.SettingID(64)} {
		if !slices.ContainsFunc(settingsTabSet, func(tab vebus.Tab) bool { return slices.Contains(rowIDs(pages[tab]), id) }) {
			t.Errorf("%v is not shown", id)
		}
	}
	if slices.Contains(rowIDs(pages[vebus.TabGeneral]), vebus.SettingID(49)) {
		t.Error("unsupported setting 49 is shown")
	}
	for _, it := range u.tabs.Items {
		wantDisabled := it.Text == "Virtual switch" || it.Text == "Advanced"
		if it.Text != "Grid" && it.Disabled() != wantDisabled {
			t.Errorf("tab %q disabled = %v, want %v", it.Text, it.Disabled(), wantDisabled)
		}
	}
	if !pages[vebus.TabVirtualSwitch].empty() || !strings.Contains(labelTexts(pages[vebus.TabVirtualSwitch].content), "none of these settings") {
		t.Error("the empty virtual switch tab does not say so")
	}
}

// TestVirtualSwitchSubTabs loads a model with virtual switch settings
// and checks that each group with a supported entry gets a sub-tab.
func TestVirtualSwitchSubTabs(t *testing.T) {
	u := newTestUI(t)
	p := newSubPages(u, vebus.TabVirtualSwitch)
	d := vebus.NewDevice(nil)
	d.AddSetting(15, mk2.SettingInfo{Sc: 1, Max: 6}, 1)
	d.AddSetting(16, mk2.SettingInfo{Sc: -10, Max: 500}, 100)
	u.settings.load(d, nil)
	var titles []string
	for _, it := range p.subTabs.Items {
		titles = append(titles, it.Text)
	}
	if !slices.Equal(titles, []string{"Usage", "Switch on when"}) {
		t.Errorf("sub-tabs = %v, want Usage and Switch on when", titles)
	}
	// The usage choice offers the catalogue values within the device's
	// range.
	_, labels := choiceOptions(d, mustEntry(t, d, vebus.SettingID(15)))
	if len(labels) != 7 || labels[1] != "The relay" {
		t.Errorf("usage choices = %q", labels)
	}
}

func mustEntry(t *testing.T, d *vebus.Device, id vebus.ID) vebus.Entry {
	t.Helper()
	e, ok := d.Catalogue().Entry(id)
	if !ok {
		t.Fatalf("no catalogue entry %v", id)
	}
	return e
}

// TestEditSendGetRoundTrip edits a number through its value dialog and a
// flag through its check box, sends the changes to the simulator and reads
// them back.
func TestEditSendGetRoundTrip(t *testing.T) {
	u := newTestUI(t)
	s := settingsSession(t)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	charger := newSettingsPage(u, vebus.TabCharger)
	inverter := newSettingsPage(u, vebus.TabInverter)
	d := readModel(t, s, 0)
	u.settings.load(d, nil)

	absorption := rowOf(t, charger, vebus.SettingID(2))
	if got := absorption.control.(*widget.Button).Text; got != "14.40 V" {
		t.Errorf("absorption voltage shows %q, want 14.40 V", got)
	}
	absorption.edit()
	dlg := u.win.Canvas().Overlays().Top()
	entry := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Entry); return ok })[0].(*widget.Entry)
	ok := findButton(t, dlg, "OK")
	for _, tc := range []struct{ text, why string }{{"18", "above the maximum"}, {"14.405", "not a whole step"}, {"abc", "not a number"}} {
		entry.SetText(tc.text)
		if !ok.Disabled() {
			t.Errorf("value %q (%s) is accepted", tc.text, tc.why)
		}
	}
	for _, want := range []string{"8.00 V", "17.50 V", "0.01 V", "14.40 V"} { // min, max, step, default
		if !strings.Contains(labelTexts(dlg), want) {
			t.Errorf("value dialog does not show %q", want)
		}
	}
	entry.SetText("14.6")
	if ok.Disabled() {
		t.Fatal("valid value 14.6 is refused")
	}
	test.Tap(ok)

	assist := rowOf(t, inverter, vebus.FlagID(5))
	test.Tap(assist.control.(*widget.Check))

	for _, r := range []*settingRow{absorption, assist} {
		if !strings.Contains(r.marker.Text, "changed") || !r.name.TextStyle.Bold {
			t.Errorf("%v: marker %q, bold %v; want it marked as changed", r.e.ID(), r.marker.Text, r.name.TextStyle.Bold)
		}
	}
	if m := rowOf(t, charger, vebus.SettingID(3)).marker.Text; strings.Contains(m, "changed") {
		t.Errorf("unchanged float voltage is marked %q", m)
	}
	if got := d.ChangedIDs(); !slices.Equal(got, []vebus.ID{vebus.SettingID(0), vebus.SettingID(2)}) {
		t.Fatalf("changed = %v, want flag word 0 and setting 2", got)
	}

	rep, err := writeSettings(ctxT(t), s, d, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sent []vebus.ID
	for _, r := range rep.Results {
		sent = append(sent, r.ID)
		if r.Status != vebus.StatusVerified {
			t.Errorf("%v: %v", r.ID, r.Status)
		}
	}
	if !slices.Equal(sent, []vebus.ID{vebus.SettingID(0), vebus.SettingID(2)}) {
		t.Errorf("sent %v, want only the changed flag word 0 and setting 2", sent)
	}
	u.settings.changed()
	for _, r := range []*settingRow{absorption, assist} {
		if strings.Contains(r.marker.Text, "changed") || r.name.TextStyle.Bold {
			t.Errorf("%v still marked as changed after sending: %q", r.e.ID(), r.marker.Text)
		}
	}

	back := readModel(t, s, 0)
	if v, _ := back.Display(vebus.SettingID(2)); v != 14.6 {
		t.Errorf("device absorption voltage = %v, want 14.6", v)
	}
	if on, _ := back.Flag(5); !on {
		t.Error("device PowerAssist flag is off, want on")
	}
	if v, _ := back.Display(vebus.SettingID(3)); v != 13.8 {
		t.Errorf("unchanged float voltage = %v, want 13.8", v)
	}
}

func TestValidationBlocksSend(t *testing.T) {
	u := newTestUI(t)
	s := settingsSession(t)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	d := readModel(t, s, 0)
	u.settings.load(d, nil)
	if err := setNumber(d, mustEntry(t, d, vebus.SettingID(3)), "15"); err != nil { // float above absorption
		t.Fatal(err)
	}
	u.settings.changed()
	if st := modelStatus(u.settings); !strings.Contains(st, "1 value is not valid") {
		t.Errorf("status %q does not report the invalid value", st)
	}
	sendSettings(u)
	dlg := u.win.Canvas().Overlays().Top()
	if dlg == nil || !strings.Contains(labelTexts(dlg), "not valid") {
		t.Fatal("no validation message")
	}
	if len(findObjects(dlg, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == "Send" })) > 0 {
		t.Error("the send confirmation is shown despite the invalid value")
	}
	if v, _ := readModel(t, s, 0).Display(vebus.SettingID(3)); v != 13.8 {
		t.Errorf("device float voltage = %v, want 13.8 (nothing sent)", v)
	}
}

// testFile is a synthetic one-unit file with the setting info and defaults
// of the simulator's 12 V device.
func testFile() rvsctest.File {
	return rvsctest.FromProfile(simulator.Synthetic12V230(), 192, 0x0A0B0C0D, "ZZ0001TEST")
}

func TestSettingsFileLoadEditSave(t *testing.T) {
	u := newTestUI(t)
	charger := newSettingsPage(u, vebus.TabCharger)
	spec := testFile()
	sf, err := parseSettingsFile("test.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	d := u.settings.dev
	if d == nil || u.settings.file != sf {
		t.Fatal("file not loaded")
	}
	if err := setNumber(d, mustEntry(t, d, vebus.SettingID(2)), "14.6"); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()
	if m := rowOf(t, charger, vebus.SettingID(2)).marker.Text; !strings.Contains(m, "changed") {
		t.Errorf("edited value marked %q", m)
	}

	data, err := sf.save(d)
	if err != nil {
		t.Fatal(err)
	}
	spec.Units[0].Settings[2] = 1460
	if !bytes.Equal(data, spec.Bytes()) {
		t.Error("saved file differs from the original with only setting 2 changed")
	}
	loadSettingsFile(u, sf)
	if d := u.settings.dev; len(d.ChangedIDs()) != 0 {
		t.Errorf("after saving, changed = %v", d.ChangedIDs())
	}
	if m := rowOf(t, charger, vebus.SettingID(2)).marker.Text; strings.Contains(m, "changed") {
		t.Errorf("saved value still marked %q", m)
	}

	// The saved bytes load with the edit.
	sf2, err := parseSettingsFile("saved.rvsc", data)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := sf2.device()
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := d2.Display(vebus.SettingID(2)); v != 14.6 {
		t.Errorf("reloaded absorption voltage = %v, want 14.6", v)
	}
}

func TestSaveDeviceModelRefused(t *testing.T) {
	u := newTestUI(t)
	d := vebus.NewDevice(nil)
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Min: 800, Max: 1750}, 1440)
	u.settings.load(d, nil)
	saveSettingsFile(u)
	if dlg := u.win.Canvas().Overlays().Top(); dlg == nil || !strings.Contains(labelTexts(dlg), "Only an opened settings file") {
		t.Error("saving a model read from a device is not refused with a message")
	}
}

// TestSendFileToDevice applies an edited file to the simulator, after the
// checks the send dialog runs.
func TestSendFileToDevice(t *testing.T) {
	s := settingsSession(t)
	spec := testFile()
	sf, err := parseSettingsFile("test.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	fileDev, _ := sf.device()
	if err := setNumber(fileDev, mustEntry(t, fileDev, vebus.SettingID(2)), "14.6"); err != nil {
		t.Fatal(err)
	}
	var ids []uint16
	for id := range sf.unit.NumSettings() {
		if !vebus.GridCodeSetting(uint16(id)) {
			ids = append(ids, uint16(id))
		}
	}
	dev, _, err := readSettings(ctxT(t), s, 0, ids, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := vebus.MatchRVSC(sf.info, sf.unit, dev); !m.OK() {
		t.Fatalf("file does not match: %v", m)
	}
	diffs := vebus.DiffDevices(fileDev, dev)
	if len(diffs) != 1 || diffs[0].ID != vebus.SettingID(2) {
		t.Errorf("differences = %+v, want setting 2 only", diffs)
	}
	if err := copyFileValues(fileDev, dev); err != nil {
		t.Fatal(err)
	}
	rep, err := writeSettings(ctxT(t), s, dev, false, nil)
	if err != nil || len(rep.Results) != 1 || rep.Results[0].ID != vebus.SettingID(2) {
		t.Fatalf("write = %+v, %v; want setting 2 only", rep, err)
	}
	if v, _ := readModel(t, s, 0).Display(vebus.SettingID(2)); v != 14.6 {
		t.Errorf("device absorption voltage = %v, want 14.6", v)
	}

	// A file for other firmware does not match.
	spec.Units[0].Firmware++
	other, err := parseSettingsFile("other.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if vebus.MatchRVSC(other.info, other.unit, dev).OK() {
		t.Error("a file for other firmware matches the device")
	}
}

func TestNumberConversions(t *testing.T) {
	d := vebus.NewDevice(nil)
	d.AddSetting(5, mk2.SettingInfo{Sc: 1, Default: 230, Min: 210, Max: 245}, 230)
	d.AddSetting(50, mk2.SettingInfo{Sc: -10, Default: 8, Max: 100}, 8)
	d.AddSetting(8, mk2.SettingInfo{Sc: 60, Default: 168, Min: 1, Max: 720}, 168)
	d.AddSetting(11, mk2.SettingInfo{Sc: -100, Default: 930, Min: 800, Max: 1300}, 930)
	d.AddSetting(12, mk2.SettingInfo{Sc: -100, Default: 120, Max: 400}, 120)
	d.AddSetting(19, mk2.SettingInfo{Sc: -60, Offset: -1, Max: 60000}, 61)
	for _, tc := range []struct {
		id         uint16
		want       string
		set        string
		wantRaw    uint16
		wantShown  string
		lo, hi, st string
	}{
		{50, "184 W", "230", 10, "230 W", "0 W", "2,300 W", "23 W"},          // A at the output voltage
		{8, "7.00 d", "3", 72, "3.00 d", "0.04 d", "30.00 d", "0.04 d"},      // minutes as days
		{12, "10.50 V", "11", 170, "11.00 V", "9.30 V", "13.30 V", "0.01 V"}, // restart level: 11 + 12
		{19, "60 s", "90", 91, "90 s", "-1 s", "59,999 s", "1 s"},            // minutes as seconds
	} {
		e := mustEntry(t, d, vebus.SettingID(tc.id))
		if got := valueText(d, e); got != tc.want {
			t.Errorf("setting %d shows %q, want %q", tc.id, got, tc.want)
		}
		n := newNumberView(d, e)
		lo, hi, step, _ := n.limits()
		if got := []string{n.text(lo), n.text(hi), n.text(step)}; !slices.Equal(got, []string{tc.lo, tc.hi, tc.st}) {
			t.Errorf("setting %d range %q, want %q", tc.id, got, []string{tc.lo, tc.hi, tc.st})
		}
		if err := setNumber(d, e, tc.set); err != nil {
			t.Errorf("setting %d to %s: %v", tc.id, tc.set, err)
			continue
		}
		if raw, _ := d.Raw(vebus.SettingID(tc.id)); raw != tc.wantRaw {
			t.Errorf("setting %d raw = %d, want %d", tc.id, raw, tc.wantRaw)
		}
		if got := valueText(d, e); got != tc.wantShown {
			t.Errorf("setting %d after setting %s shows %q, want %q", tc.id, tc.set, got, tc.wantShown)
		}
	}
	if err := setNumber(d, mustEntry(t, d, vebus.SettingID(50)), "5000"); err == nil || !strings.Contains(err.Error(), "2,300 W") {
		t.Errorf("out-of-range power: %v, want a message with the range", err)
	}
}

func TestParseNumberLocale(t *testing.T) {
	t.Cleanup(func() { setLanguage("en") })
	setLanguage("de")
	if v, err := parseNumber("14,6"); err != nil || v != 14.6 {
		t.Errorf("de: parseNumber(14,6) = %v, %v", v, err)
	}
	if got := entryText(14.6, 2); got != "14,60" {
		t.Errorf("de: entryText = %q", got)
	}
	setLanguage("en")
	if v, err := parseNumber(" 14.6 "); err != nil || v != 14.6 {
		t.Errorf("en: parseNumber(14.6) = %v, %v", v, err)
	}
	if _, err := parseNumber("1,234"); err == nil {
		t.Error("en: digit grouping accepted")
	}
}

func TestBatteryPresets(t *testing.T) {
	u := newTestUI(t)
	prefs := u.app.Preferences()
	d := vebus.NewDevice(nil)
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Min: 800, Max: 1750}, 1440)
	d.AddSetting(3, mk2.SettingInfo{Sc: -100, Min: 800, Max: 1750}, 1380)
	d.AddSetting(1, mk2.SettingInfo{Sc: 1, Max: 0x7000}, 1<<13) // flag 29 (traction curve) on
	storePresets(prefs, []batteryPreset{presetFrom(d, "Mine")})

	_ = setNumber(d, mustEntry(t, d, vebus.SettingID(2)), "14.1")
	ps := loadPresets(prefs)
	if len(ps) != 1 || ps[0].Name != "Mine" {
		t.Fatalf("stored presets = %+v", ps)
	}
	if errs := ps[0].apply(d); len(errs) > 0 {
		t.Fatal(errs)
	}
	if v, _ := d.Display(vebus.SettingID(2)); v != 14.4 {
		t.Errorf("absorption after the preset = %v, want 14.4", v)
	}
	if on, _ := d.Flag(29); on {
		t.Error("the preset left the traction curve on")
	}
}

func TestFindEntries(t *testing.T) {
	d := vebus.NewDevice(nil)
	d.AddSetting(0, mk2.SettingInfo{Sc: 1, Max: 0x0024}, 0) // flags 2 and 5
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Min: 800, Max: 1750}, 1440)
	d.AddSetting(13, mk2.SettingInfo{Sc: 1, Max: 5}, 0) // system configuration: not on a tab
	ids := func(es []vebus.Entry) []vebus.ID {
		var out []vebus.ID
		for _, e := range es {
			out = append(out, e.ID())
		}
		return out
	}
	if got := ids(findEntries(d, "2")); !slices.Equal(got, []vebus.ID{vebus.SettingID(2), vebus.FlagID(2)}) {
		t.Errorf("find 2 = %v, want setting 2 and flag 2", got)
	}
	if got := ids(findEntries(d, "ABSORPTION")); !slices.Equal(got, []vebus.ID{vebus.SettingID(2)}) {
		t.Errorf("find absorption = %v", got)
	}
	if got := findEntries(d, "13"); len(got) != 0 {
		t.Errorf("find 13 = %v, want nothing (setting 13 is on no tab)", ids(got))
	}
}

func TestSetAllToDefault(t *testing.T) {
	d := vebus.NewDevice(nil)
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Default: 1440, Min: 800, Max: 1750}, 1500)
	d.AddSetting(13, mk2.SettingInfo{Sc: 1, Max: 5}, 2)
	if errs := applyDefaults(d); len(errs) > 0 {
		t.Fatal(errs)
	}
	if raw, _ := d.Raw(vebus.SettingID(2)); raw != 1440 {
		t.Errorf("setting 2 = %d, want its default 1440", raw)
	}
	if raw, _ := d.Raw(vebus.SettingID(13)); raw != 2 {
		t.Errorf("system configuration setting 13 = %d, want it left at 2", raw)
	}
}

// TestDemoSecondUnit reads the demo's second unit, as Get does with that
// unit chosen.
func TestDemoSecondUnit(t *testing.T) {
	s, err := connectDemo(t)
	if err != nil {
		t.Fatal(err)
	}
	u := newTestUI(t)
	general := newSettingsPage(u, vebus.TabGeneral)
	u.settings.load(readModel(t, s, 1), nil)
	if got := rowOf(t, general, vebus.FlagID(2)).control.(*widget.RadioGroup).Selected; got != "60 Hz" {
		t.Errorf("output frequency of unit 1 = %q, want 60 Hz", got)
	}
	if st := modelStatus(u.settings); !strings.Contains(st, "address 1") {
		t.Errorf("status %q does not name address 1", st)
	}
}
