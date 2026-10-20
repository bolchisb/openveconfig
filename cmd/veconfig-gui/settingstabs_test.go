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
	for _, p := range u.pages {
		wantOff := p.title == "Virtual switch" || p.title == "Advanced"
		if p.title != "Grid" && p.unavailable(u) != wantOff {
			t.Errorf("page %q greyed = %v, want %v", p.title, p.unavailable(u), wantOff)
		}
	}
	if !pages[vebus.TabVirtualSwitch].empty() || !strings.Contains(labelTexts(pages[vebus.TabVirtualSwitch].content), "none of these settings") {
		t.Error("the empty virtual switch tab does not say so")
	}
}

// TestVirtualSwitchSubTabs checks that the virtual switch sub-pages follow
// the usage, that the usages are a list whose texts follow the inversion,
// and that the inversion is only offered where it applies.
func TestVirtualSwitchSubTabs(t *testing.T) {
	u := newTestUI(t)
	p := newSubPages(u, vebus.TabVirtualSwitch)
	d := vebus.NewDevice(nil)
	d.AddSetting(1, mk2.SettingInfo{Sc: 1, Max: 1 << 10}, 0) // flag 26 (invert)
	d.AddSetting(15, mk2.SettingInfo{Sc: 1, Max: 6}, 0)
	d.AddSetting(16, mk2.SettingInfo{Sc: -10, Max: 500}, 100)
	d.AddSetting(52, mk2.SettingInfo{Sc: -10, Max: 500}, 100)
	u.settings.load(d, nil)
	titles := func() (s []string) {
		for _, it := range p.subTabs.Items {
			s = append(s, it.Text)
		}
		return s
	}
	usage := func(v uint16) {
		t.Helper()
		if err := d.SetRaw(vebus.SettingID(15), v); err != nil {
			t.Fatal(err)
		}
		u.settings.changed()
	}
	usagePage := p.sub[slices.IndexFunc(p.sub, func(s *settingsPage) bool { return s.groups[0] == "Usage" })]
	invert := rowOf(t, usagePage, vebus.FlagID(26))

	for _, tc := range []struct {
		usage      uint16
		tabs       []string
		invertible bool
	}{
		{0, []string{"Usage"}, false},
		{1, []string{"Usage", "Switch on when"}, true},
		{3, []string{"Usage", "Second switch"}, false},
		{5, []string{"Usage", "Switch on when", "Second switch"}, true},
	} {
		usage(tc.usage)
		if !slices.Equal(titles(), tc.tabs) {
			t.Errorf("usage %d: sub-tabs %v, want %v", tc.usage, titles(), tc.tabs)
		}
		if got := !invert.control.(fyne.Disableable).Disabled(); got != tc.invertible {
			t.Errorf("usage %d: inversion offered %v, want %v", tc.usage, got, tc.invertible)
		}
	}

	// The usages are a list of all seven; their texts follow the inversion.
	row := rowOf(t, usagePage, vebus.SettingID(15))
	radio := row.control.(*widget.RadioGroup)
	if len(radio.Options) != 7 || !strings.Contains(radio.Options[1], "open while the switch is on") {
		t.Errorf("usages %q", radio.Options)
	}
	if err := d.SetFlag(26, true); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()
	if !strings.Contains(radio.Options[1], "closed while the switch is on") {
		t.Errorf("inverted usages %q", radio.Options)
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

// enter types text into a number's entry and presses Enter.
func enter(r *formRow, text string) {
	e := r.control.(*numberEntry)
	e.SetText(text)
	e.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
}

// errorText returns the error shown under r's control, or "".
func errorText(r *formRow) string {
	if !r.errText.Visible() {
		return ""
	}
	return r.errText.String()
}

// marked reports whether r shows that its value was changed: the dot, the
// bold label and the revert button.
func marked(r *formRow) bool {
	return r.dot.Visible() && r.revert.Visible() && (r.header || r.label.TextStyle.Bold)
}

// TestEditSendGetRoundTrip edits a number in its entry and a flag through
// its check box, sends the changes to the simulator and reads them back.
func TestEditSendGetRoundTrip(t *testing.T) {
	u := newTestUI(t)
	s := settingsSession(t)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	charger := newSettingsPage(u, vebus.TabCharger)
	inverter := newSettingsPage(u, vebus.TabInverter)
	d := readModel(t, s, 0)
	u.settings.load(d, nil)

	absorption := rowOf(t, charger, vebus.SettingID(2))
	if got := absorption.entry.Text; got != "14.40" || absorption.unit.Text != "V" {
		t.Errorf("absorption voltage shows %q %q, want 14.40 V", got, absorption.unit.Text)
	}
	if got := absorption.caption.String(); got != "8.00 – 17.50 V" {
		t.Errorf("absorption voltage range %q, want 8.00 – 17.50 V", got)
	}
	for _, tc := range []struct{ text, why, msg string }{
		{"18", "above the maximum", "Enter a value from 8.00 V to 17.50 V, in steps of 0.01 V."},
		{"14.405", "not a whole step", "Enter a value from 8.00 V to 17.50 V, in steps of 0.01 V."},
		{"abc", "not a number", "Enter a number."},
	} {
		enter(absorption.formRow, tc.text)
		if d.Dirty(vebus.SettingID(2)) || errorText(absorption.formRow) != tc.msg || absorption.entry.Text != tc.text {
			t.Errorf("value %q (%s): dirty %v, error %q, text %q; want refused with %q, the text kept",
				tc.text, tc.why, d.Dirty(vebus.SettingID(2)), errorText(absorption.formRow), absorption.entry.Text, tc.msg)
		}
	}
	enter(absorption.formRow, "14.6")
	if errorText(absorption.formRow) != "" || absorption.entry.Text != "14.60" {
		t.Fatalf("valid value 14.6: error %q, text %q", errorText(absorption.formRow), absorption.entry.Text)
	}

	assist := rowOf(t, inverter, vebus.FlagID(5))
	if !assist.header {
		t.Error("PowerAssist is not the header of its card")
	}
	test.Tap(assist.control.(*toggleSwitch))

	for _, r := range []*settingRow{absorption, assist} {
		if !marked(r.formRow) {
			t.Errorf("%v is not marked as changed", r.e.ID())
		}
	}
	if marked(rowOf(t, charger, vebus.SettingID(3)).formRow) {
		t.Error("unchanged float voltage is marked as changed")
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
		if r.dot.Visible() || r.revert.Visible() || r.label.TextStyle.Bold && !r.header {
			t.Errorf("%v still marked as changed after sending", r.e.ID())
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
	if dlg == nil || !strings.Contains(dialogTexts(dlg), "not valid") {
		t.Fatal("no validation message")
	}
	if !findButton(t, dlg, "Send").Disabled() {
		t.Error("sending is not blocked despite the invalid value")
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
	if !marked(rowOf(t, charger, vebus.SettingID(2)).formRow) {
		t.Error("edited value is not marked as changed")
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
	if rowOf(t, charger, vebus.SettingID(2)).changed {
		t.Error("saved value still marked as changed")
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

// TestNumberEntryCommit checks that a number's entry commits on Enter and
// when it loses focus, and that Escape restores the value.
func TestNumberEntryCommit(t *testing.T) {
	u := newTestUI(t)
	charger := newSettingsPage(u, vebus.TabCharger)
	d := vebus.NewDevice(nil)
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Default: 1440, Min: 800, Max: 1750}, 1440)
	d.AddSetting(3, mk2.SettingInfo{Sc: -100, Default: 1380, Min: 800, Max: 1750}, 1380)
	u.settings.load(d, nil)
	w := test.NewTempWindow(t, charger.content)
	absorption, float := rowOf(t, charger, vebus.SettingID(2)), rowOf(t, charger, vebus.SettingID(3))

	// Enter.
	enter(absorption.formRow, "14.5")
	if v, _ := d.Display(vebus.SettingID(2)); v != 14.5 || !marked(absorption.formRow) {
		t.Errorf("after Enter: absorption %v, marked %v; want 14.5, marked", v, marked(absorption.formRow))
	}

	// Focus loss.
	w.Canvas().Focus(float.entry)
	float.entry.SetText("13.5")
	w.Canvas().Unfocus()
	if v, _ := d.Display(vebus.SettingID(3)); v != 13.5 || float.entry.Text != "13.50" {
		t.Errorf("after focus loss: float %v, text %q; want 13.5, 13.50", v, float.entry.Text)
	}

	// Escape restores the value, also after a refused text.
	w.Canvas().Focus(float.entry)
	float.entry.SetText("13.9")
	float.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if v, _ := d.Display(vebus.SettingID(3)); v != 13.5 || float.entry.Text != "13.50" {
		t.Errorf("after Escape: float %v, text %q; want 13.5, 13.50", v, float.entry.Text)
	}
	enter(float.formRow, "99")
	if errorText(float.formRow) == "" || !float.revert.Visible() {
		t.Error("a refused value shows no error or no revert button")
	}
	float.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if errorText(float.formRow) != "" || float.entry.Text != "13.50" {
		t.Errorf("after Escape: error %q, text %q", errorText(float.formRow), float.entry.Text)
	}
	w.Canvas().Unfocus()
	if v, _ := d.Display(vebus.SettingID(3)); v != 13.5 {
		t.Errorf("float %v after Escape and focus loss, want 13.5", v)
	}
}

// TestRevertOneSetting reverts one changed setting and one changed flag,
// and leaves the other changes.
func TestRevertOneSetting(t *testing.T) {
	u := newTestUI(t)
	charger := newSettingsPage(u, vebus.TabCharger)
	inverter := newSettingsPage(u, vebus.TabInverter)
	d := vebus.NewDevice(nil)
	d.AddSetting(0, mk2.SettingInfo{Sc: 1, Max: 0x0060}, 0) // flags 5 and 6
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Default: 1440, Min: 800, Max: 1750}, 1440)
	d.AddSetting(3, mk2.SettingInfo{Sc: -100, Default: 1380, Min: 800, Max: 1750}, 1380)
	u.settings.load(d, nil)
	absorption, float := rowOf(t, charger, vebus.SettingID(2)), rowOf(t, charger, vebus.SettingID(3))
	assist, charge := rowOf(t, inverter, vebus.FlagID(5)), rowOf(t, charger, vebus.FlagID(6))
	enter(absorption.formRow, "14.6")
	enter(float.formRow, "13.6")
	test.Tap(assist.control.(*toggleSwitch))
	test.Tap(charge.control.(*widget.Check))

	test.Tap(absorption.revert)
	test.Tap(assist.revert)
	if v, _ := d.Display(vebus.SettingID(2)); v != 14.4 || absorption.entry.Text != "14.40" || marked(absorption.formRow) {
		t.Errorf("reverted absorption = %v (%q), marked %v; want 14.40, unmarked", v, absorption.entry.Text, marked(absorption.formRow))
	}
	if on, _ := d.Flag(5); on || marked(assist.formRow) {
		t.Errorf("reverted PowerAssist flag = %v, marked %v; want off, unmarked", on, marked(assist.formRow))
	}
	if v, _ := d.Display(vebus.SettingID(3)); v != 13.6 || !marked(float.formRow) {
		t.Errorf("float = %v, marked %v; want 13.6 kept, marked", v, marked(float.formRow))
	}
	if on, _ := d.Flag(6); !on || !marked(charge.formRow) {
		t.Errorf("flag 6 = %v, marked %v; want the change kept", on, marked(charge.formRow))
	}
}

// TestMasterToggles checks that the rows of a function that is switched
// off are disabled, not hidden.
func TestMasterToggles(t *testing.T) {
	u := newTestUI(t)
	inverter := newSettingsPage(u, vebus.TabInverter)
	general := newSettingsPage(u, vebus.TabGeneral)
	d := vebus.NewDevice(nil)
	d.AddSetting(0, mk2.SettingInfo{Sc: 1, Max: 0x0120}, 0x0100) // flag 5 off, flag 8 on (power saving off)
	d.AddSetting(1, mk2.SettingInfo{Sc: 1, Max: 0x8000}, 0)      // flag 31
	d.AddSetting(48, mk2.SettingInfo{Sc: -10, Default: 20, Max: 50}, 20)
	d.AddSetting(73, mk2.SettingInfo{Sc: -100, Max: 20000}, 10000)
	d.AddSetting(50, mk2.SettingInfo{Sc: -10, Max: 100}, 8)
	d.AddSetting(51, mk2.SettingInfo{Sc: -10, Max: 100}, 1)
	d.AddSetting(64, mk2.SettingInfo{Sc: 1, Max: 3000}, 0)
	d.AddSetting(65, mk2.SettingInfo{Sc: -2, Max: 200}, 190)
	d.AddSetting(72, mk2.SettingInfo{Sc: -256, Offset: 1, Max: 255}, 242)
	d.AddSetting(83, mk2.SettingInfo{Sc: 1, Max: 100}, 0)
	d.AddSetting(84, mk2.SettingInfo{Sc: 1, Max: 100}, 0)
	u.settings.load(d, nil)

	disabled := func(p *settingsPage, id vebus.ID) bool {
		return rowOf(t, p, id).control.(fyne.Disableable).Disabled()
	}
	check := func(what string, want bool, rows map[*settingsPage][]vebus.ID) {
		t.Helper()
		for p, ids := range rows {
			for _, id := range ids {
				if got := disabled(p, id); got != want {
					t.Errorf("%s: %v disabled = %v, want %v", what, id, got, want)
				}
			}
		}
	}
	check("all off", true, map[*settingsPage][]vebus.ID{
		inverter: {vebus.SettingID(48), vebus.SettingID(73), vebus.FlagID(31), vebus.SettingID(50), vebus.SettingID(51), vebus.SettingID(83), vebus.SettingID(84)},
		general:  {vebus.SettingID(64), vebus.SettingID(65), vebus.SettingID(72)},
	})
	check("masters", false, map[*settingsPage][]vebus.ID{
		inverter: {vebus.FlagID(5), vebus.FlagID(8)},
	})
	for _, id := range []vebus.ID{vebus.FlagID(5), vebus.FlagID(8)} {
		if r := rowOf(t, inverter, id); !r.header {
			t.Errorf("%v is not the header of its card", id)
		}
	}
	for p, n := range map[*settingsPage]uint16{general: 64, inverter: 84} {
		if s := p.switches[n]; s == nil || s.toggle.On {
			t.Errorf("setting %d: card switch missing or on while the value is 0", n)
		}
	}

	test.Tap(rowOf(t, inverter, vebus.FlagID(5)).control.(*toggleSwitch))
	test.Tap(rowOf(t, inverter, vebus.FlagID(8)).control.(*toggleSwitch))
	test.Tap(general.switches[64].toggle)
	test.Tap(inverter.switches[84].toggle)
	enter(rowOf(t, general, vebus.SettingID(64)).formRow, "200")
	enter(rowOf(t, inverter, vebus.SettingID(84)).formRow, "50")
	check("all on", false, map[*settingsPage][]vebus.ID{
		inverter: {vebus.SettingID(48), vebus.SettingID(73), vebus.FlagID(31), vebus.SettingID(50), vebus.SettingID(51), vebus.SettingID(83), vebus.SettingID(84)},
		general:  {vebus.SettingID(64), vebus.SettingID(65), vebus.SettingID(72)},
	})
	if on, _ := d.Flag(8); on {
		t.Error("ticking power saving left its inverted flag 8 set")
	}
}

// TestBatteryMonitorSwitch checks the battery monitor's card switch: on
// without a capacity waits for one, off sets the capacity to 0 and turns off
// what needs the monitor, and on again restores the capacity.
func TestBatteryMonitorSwitch(t *testing.T) {
	u := newTestUI(t)
	general := newSettingsPage(u, vebus.TabGeneral)
	d := vebus.NewDevice(nil)
	d.AddSetting(0, mk2.SettingInfo{Sc: 1, Max: 0x0010}, 0) // flag 4 off: bulk stops after its safety time
	d.AddSetting(64, mk2.SettingInfo{Sc: 1, Max: 3000}, 0)
	d.AddSetting(65, mk2.SettingInfo{Sc: -2, Max: 200}, 190)
	d.AddSetting(83, mk2.SettingInfo{Sc: 1, Max: 100}, 0)
	d.AddSetting(84, mk2.SettingInfo{Sc: 1, Max: 100}, 0)
	u.settings.load(d, nil)
	s := general.switches[64]
	capacity := rowOf(t, general, vebus.SettingID(64))

	test.Tap(s.toggle)
	if !s.toggle.On || !s.pending || capacity.control.(fyne.Disableable).Disabled() {
		t.Fatalf("on without a capacity: switch %v, pending %v, capacity disabled %v",
			s.toggle.On, s.pending, capacity.control.(fyne.Disableable).Disabled())
	}
	if !strings.Contains(s.head.caption.String(), "capacity") {
		t.Errorf("no hint to enter the capacity: %q", s.head.caption.String())
	}
	if raw, _ := d.Raw(vebus.SettingID(64)); raw != 0 {
		t.Errorf("turning on wrote capacity %d before one was entered", raw)
	}

	enter(capacity.formRow, "200")
	if s.pending || !s.toggle.On || s.head.caption.Visible() {
		t.Errorf("after entering a capacity: pending %v, switch %v, hint shown %v", s.pending, s.toggle.On, s.head.caption.Visible())
	}
	if err := d.SetRaw(vebus.SettingID(84), 50); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()

	test.Tap(s.toggle)
	for id, want := range map[uint16]uint16{64: 0, 84: 0} {
		if raw, _ := d.Raw(vebus.SettingID(id)); raw != want {
			t.Errorf("monitor off: setting %d = %d, want %d", id, raw, want)
		}
	}
	if on, _ := d.Flag(4); !on {
		t.Error("monitor off left bulk stopping after its safety time on")
	}
	if !capacity.control.(fyne.Disableable).Disabled() || s.toggle.On {
		t.Errorf("monitor off: switch %v, capacity disabled %v", s.toggle.On, capacity.control.(fyne.Disableable).Disabled())
	}

	test.Tap(s.toggle)
	if raw, _ := d.Raw(vebus.SettingID(64)); raw != 200 || s.pending {
		t.Errorf("on again: capacity %d (want 200), pending %v", raw, s.pending)
	}
}

// TestLithiumRules checks what the lithium flag hides and shows on the
// Charger page, the charge algorithms it allows, and what switching it on
// and off changes.
func TestLithiumRules(t *testing.T) {
	u := newTestUI(t)
	charger := newSettingsPage(u, vebus.TabCharger)
	d := vebus.NewDevice(nil)
	d.AddSetting(0, mk2.SettingInfo{Sc: 1, Max: 1 << 11}, 1<<11)            // flag 11 (storage) on
	d.AddSetting(1, mk2.SettingInfo{Sc: 1, Max: 1 << 13}, 0)                // flag 29
	d.AddSetting(60, mk2.SettingInfo{Sc: 1, Max: 1<<4 | 1<<7}, 0)           // flags 36 (lithium), 39 (BMS)
	d.AddSetting(10, mk2.SettingInfo{Sc: 1, Default: 3, Min: 1, Max: 3}, 3) // adaptive with gradual absorption
	d.AddSetting(86, mk2.SettingInfo{Sc: -2, Offset: -41, Max: 137}, 20)
	d.AddSetting(87, mk2.SettingInfo{Sc: -100, Max: 12000}, 3240)
	u.settings.load(d, nil)

	shown := func(ids ...vebus.ID) (s []bool) {
		for _, id := range ids {
			s = append(s, rowOf(t, charger, id).root.Visible())
		}
		return s
	}
	algorithms := func() []string { return rowOf(t, charger, vebus.SettingID(10)).control.(*widget.RadioGroup).Options }
	hidden, only := []vebus.ID{vebus.FlagID(11), vebus.FlagID(29), vebus.SettingID(87)}, []vebus.ID{vebus.SettingID(86), vebus.FlagID(39)}

	if !slices.Equal(shown(hidden...), []bool{true, true, true}) || !slices.Equal(shown(only...), []bool{false, false}) || len(algorithms()) != 3 {
		t.Errorf("lithium off: shown %v / %v, algorithms %v", shown(hidden...), shown(only...), algorithms())
	}

	test.Tap(rowOf(t, charger, vebus.FlagID(36)).control.(*widget.Check))
	if !slices.Equal(shown(hidden...), []bool{false, false, false}) || !slices.Equal(shown(only...), []bool{true, true}) || len(algorithms()) != 2 {
		t.Errorf("lithium on: shown %v / %v, algorithms %v", shown(hidden...), shown(only...), algorithms())
	}
	if on, _ := d.Flag(11); on {
		t.Error("lithium on left storage mode on")
	}
	if raw, _ := d.Raw(vebus.SettingID(10)); raw != 1 {
		t.Errorf("lithium on: algorithm %d, want 1 (fixed)", raw)
	}
	// The low-temperature block has a switch of its own: on while its raw
	// value is not 0.
	if s := charger.switches[86]; s == nil || !s.toggle.On {
		t.Error("low-temperature block: no switch, or off while set")
	}

	if err := d.SetFlag(39, true); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()
	test.Tap(rowOf(t, charger, vebus.FlagID(36)).control.(*widget.Check))
	if raw, _ := d.Raw(vebus.SettingID(86)); raw != 0 || charger.switches[86].toggle.On {
		t.Errorf("lithium off: low-temperature block raw %d (want 0, off), switch on %v", raw, charger.switches[86].toggle.On)
	}
	if on, _ := d.Flag(39); on {
		t.Error("lithium off left the VE.Bus BMS flag on")
	}
}

// TestToggleSwitch checks that the switch flips on tap and on Space, and
// not while disabled.
func TestToggleSwitch(t *testing.T) {
	test.NewTempApp(t)
	var got []bool
	s := newToggleSwitch(func(on bool) { got = append(got, on) })
	test.Tap(s)
	s.TypedRune(' ')
	s.Disable()
	test.Tap(s)
	s.TypedRune(' ')
	if !slices.Equal(got, []bool{true, false}) || s.On {
		t.Errorf("changes %v, on %v; want [true false], off", got, s.On)
	}
}

// TestEfficiencyInPercent checks that the charge efficiency, a ratio, is
// shown and entered as a percentage, and that state-of-charge values have
// their unit.
func TestEfficiencyInPercent(t *testing.T) {
	u := newTestUI(t)
	general := newSettingsPage(u, vebus.TabGeneral)
	d := vebus.NewDevice(nil)
	d.AddSetting(64, mk2.SettingInfo{Sc: 1, Max: 3000}, 200)
	d.AddSetting(72, mk2.SettingInfo{Sc: -256, Offset: 1, Max: 255}, 242)
	d.AddSetting(74, mk2.SettingInfo{Sc: -2, Max: 200}, 200)
	u.settings.load(d, nil)
	eff := rowOf(t, general, vebus.SettingID(72))
	if eff.entry.Text != "94.9" || eff.unit.Text != "%" || valueText(d, eff.e) != "94.9 %" {
		t.Errorf("efficiency shows %q %q (%q), want 94.9 %%", eff.entry.Text, eff.unit.Text, valueText(d, eff.e))
	}
	enter(eff.formRow, "97.3")
	if raw, _ := d.Raw(vebus.SettingID(72)); raw != 248 || eff.entry.Text != "97.3" {
		t.Errorf("efficiency 97.3 %%: raw %d, text %q; want 248, 97.3", raw, eff.entry.Text)
	}
	if soc := rowOf(t, general, vebus.SettingID(74)); soc.unit.Text != "%" || soc.entry.Text != "100.0" {
		t.Errorf("state of charge after absorption shows %q %q, want 100.0 %%", soc.entry.Text, soc.unit.Text)
	}
}

// TestFindFocusesEntry checks that Find shows a number's page and focuses
// its entry.
func TestFindFocusesEntry(t *testing.T) {
	u := newTestUI(t)
	sf, err := parseSettingsFile("test.rvsc", testFile().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	e := mustEntry(t, u.settings.dev, vebus.SettingID(3))
	for _, nav := range u.settings.navigators {
		if nav(e) {
			break
		}
	}
	f, ok := u.win.Canvas().Focused().(*numberEntry)
	if u.current.title != "Charger" || !ok || f.Text != "13.80" {
		t.Errorf("after Find: page %q, focused %T; want the float voltage entry on Charger", u.current.title, u.win.Canvas().Focused())
	}
}
