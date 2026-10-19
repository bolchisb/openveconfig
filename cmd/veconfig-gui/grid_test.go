package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/internal/rvsctest"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/gridcode"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// gridProfile is the simulator's 12 V device with firmware version fw plus
// invented AC input limits (settings 44-47), a grid code (setting 81) and
// grid words 128-191, all 0.
func gridProfile(fw uint32) simulator.Profile {
	p := simulator.Synthetic12V230()
	p.Version = fw
	p.Settings = append(p.Settings,
		simulator.Setting{ID: 44, Name: "UMainsLow", Sc: 1, Default: 180, Min: 150, Max: 230},
		simulator.Setting{ID: 45, Name: "UMainsLowHysteresis", Sc: 1, Default: 7, Min: 0, Max: 30},
		simulator.Setting{ID: 46, Name: "UMainsHigh", Sc: 1, Default: 265, Min: 230, Max: 280},
		simulator.Setting{ID: 47, Name: "UMainsHighHysteresis", Sc: 1, Default: 5, Min: 0, Max: 30},
		simulator.Setting{ID: 81, Name: "GridCode", Sc: 1, Default: 0, Min: 0, Max: 32},
	)
	for id := uint16(128); id <= 191; id++ {
		p.Settings = append(p.Settings, simulator.Setting{ID: id, Name: "GridWord", Sc: 1, Max: 0xFFFF})
	}
	return p
}

// gridSetup returns a UI with a grid tab and the model of a gridProfile
// device read through Get's setting list.
func gridSetup(t *testing.T, fw uint32) (*ui, *session, *gridTabView, *vebus.Device) {
	t.Helper()
	u := newTestUI(t)
	s, _ := simSession(t, simulator.Device{Address: 0, Profile: gridProfile(fw)})
	u.sess = s // no listeners: the monitor poller would race with the test driver
	g := newGridTab(u)
	d, rep, err := readSettings(ctxT(t), s, 0, deviceSettingIDs(), nil)
	if err != nil || len(rep.Failed) > 0 {
		t.Fatalf("read: %v %v", err, rep.Failed)
	}
	u.settings.load(d, nil)
	return u, s, g, d
}

// deviceValue reads setting id from the device at address 0.
func deviceValue(t *testing.T, s *session, id uint16) uint16 {
	t.Helper()
	d, _, err := readSettings(ctxT(t), s, 0, []uint16{id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := d.Raw(vebus.SettingID(id))
	return v
}

// selectCode chooses grid code c in g's choice.
func selectCode(t *testing.T, g *gridTabView, c uint16) {
	t.Helper()
	i := slices.Index(g.codeValues, c)
	if i < 0 {
		t.Fatalf("grid code %d not offered: %v", c, g.codeValues)
	}
	g.code.SetSelectedIndex(i)
}

// paramRowOf returns the row of the parameter named name.
func paramRowOf(t *testing.T, g *gridTabView, name string) *paramRow {
	t.Helper()
	for _, r := range g.rows {
		if r.p.Name == name {
			return r
		}
	}
	t.Fatalf("no row for %q", name)
	return nil
}

func TestGridCodeSelectAndSend(t *testing.T) {
	_, s, g, d := gridSetup(t, 2699481)
	if !d.Supported(vebus.SettingID(150)) || !d.Supported(vebus.SettingID(gridCodeSetting)) {
		t.Fatal("Get did not read the grid code and grid words")
	}
	if len(g.codeValues) != 33 {
		t.Errorf("%d codes offered, want 33", len(g.codeValues))
	}
	selectCode(t, g, 17)
	if !d.Dirty(vebus.SettingID(gridCodeSetting)) || !strings.Contains(g.codeMarker.Text, "changed") {
		t.Errorf("grid code not marked changed: %q", g.codeMarker.Text)
	}
	if p, _ := gridcode.PageFor(17); g.page != p || len(g.rows) == 0 {
		t.Errorf("page %v with %d rows, want the Europe page", g.page, len(g.rows))
	}
	if !strings.Contains(labelTexts(g.params), "Not supported yet") {
		t.Error("the page's unsupported parameters are not listed")
	}

	rep, err := writeSettings(ctxT(t), s, d, false, nil)
	if err != nil || len(rep.Results) != 1 || rep.Results[0].ID != vebus.SettingID(gridCodeSetting) || rep.Results[0].Status != vebus.StatusVerified {
		t.Fatalf("write = %+v, %v; want setting 81 verified", rep, err)
	}
	if v := deviceValue(t, s, gridCodeSetting); v != 17 {
		t.Errorf("device grid code = %d, want 17", v)
	}

	// A code whose page is not known says so.
	selectCode(t, g, 4)
	if g.rows != nil || !strings.Contains(labelTexts(g.params), "not known yet") {
		t.Errorf("code 4: %d rows, text %q", len(g.rows), labelTexts(g.params))
	}
}

func TestGridOrdinaryEntries(t *testing.T) {
	_, _, g, _ := gridSetup(t, 2699481)
	ids := rowIDs(g.ordinary)
	for _, id := range []vebus.ID{vebus.SettingID(44), vebus.SettingID(45), vebus.SettingID(46), vebus.SettingID(47), vebus.FlagID(3)} {
		if !slices.Contains(ids, id) {
			t.Errorf("%v is not shown; rows %v", id, ids)
		}
	}
	if slices.Contains(ids, vebus.SettingID(gridCodeSetting)) {
		t.Error("the grid code is shown as an ordinary entry")
	}
}

func TestGridPasswordFlags(t *testing.T) {
	for _, tc := range []struct {
		fw   uint32
		free []uint16
	}{
		{2699100, []uint16{0}},        // old revision: only code 0
		{2699415, []uint16{0, 1}},     // code 1 from 415
		{2699554, []uint16{0, 1, 21}}, // code 21 from 554
	} {
		_, _, g, _ := gridSetup(t, tc.fw)
		for i, v := range g.codeValues {
			marked := strings.Contains(g.code.Options[i], "needs password")
			if marked == slices.Contains(tc.free, v) {
				t.Errorf("firmware %d, code %d: label %q", tc.fw, v, g.code.Options[i])
			}
		}
		if strings.Contains(g.codeMarker.Text, "needs password") {
			t.Errorf("firmware %d: code 0 marked %q", tc.fw, g.codeMarker.Text)
		}
		selectCode(t, g, 2)
		if !strings.Contains(g.codeMarker.Text, "needs password") {
			t.Errorf("firmware %d: code 2 marked %q, want needs password", tc.fw, g.codeMarker.Text)
		}
	}
}

func TestGridParamEditBlocksSend(t *testing.T) {
	u, s, g, d := gridSetup(t, 2699481)
	selectCode(t, g, 17)

	// A number through its value dialog: 3.1 s in the top 5 bits of 143.
	r := paramRowOf(t, g, "Over-frequency response: start delay")
	g.editParam(r.p)
	dlg := u.win.Canvas().Overlays().Top()
	entry := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Entry); return ok })[0].(*widget.Entry)
	ok := findButton(t, dlg, "OK")
	entry.SetText("3.2")
	if !ok.Disabled() {
		t.Error("3.2 s (above the maximum) is accepted")
	}
	entry.SetText("3.1")
	test.Tap(ok)
	if w, _ := d.Raw(vebus.SettingID(143)); w != 31<<11 {
		t.Errorf("word 143 = %#04x, want %#04x", w, 31<<11)
	}
	if !strings.Contains(r.marker.Text, "changed") || !d.Dirty(vebus.SettingID(143)) {
		t.Errorf("start delay marker %q, dirty %v", r.marker.Text, d.Dirty(vebus.SettingID(143)))
	}
	if m := paramRowOf(t, g, "Over-frequency response: end delay").marker.Text; m != "" {
		t.Errorf("end delay, other bits of word 143, marked %q", m)
	}

	// A check box (inverted) and a choice in the same word 148.
	inhibit := paramRowOf(t, g, "Use auxiliary input 1 as feed-in inhibit").control.(*widget.Check)
	if !inhibit.Checked {
		t.Error("inverted check box with its bit clear is not ticked")
	}
	test.Tap(inhibit)
	method := paramRowOf(t, g, "P(U) reduction method").control.(*widget.Select)
	method.SetSelectedIndex(2)
	if w, _ := d.Raw(vebus.SettingID(148)); w != 0x4|2<<3 {
		t.Errorf("word 148 = %#04x, want %#04x", w, 0x4|2<<3)
	}
	if st := modelStatus(u.settings); !strings.Contains(st, "2 grid parameter words were changed") {
		t.Errorf("status %q does not report the 2 changed grid words (143, 148)", st)
	}

	// Sending is refused before anything is written, also the grid code.
	sendSettings(u)
	if dlg := u.win.Canvas().Overlays().Top(); dlg == nil || !strings.Contains(labelTexts(dlg), "cannot be sent or saved yet") {
		t.Fatal("no message that grid parameters cannot be sent")
	}
	for _, all := range []bool{false, true} {
		if rep, err := writeSettings(ctxT(t), s, d, all, nil); rep != nil || err == nil {
			t.Errorf("writeSettings(all=%v) = %+v, %v; want refused", all, rep, err)
		}
	}
	for _, id := range []uint16{143, 148, gridCodeSetting} {
		if v := deviceValue(t, s, id); v != 0 {
			t.Errorf("device setting %d = %d, want 0 (nothing sent)", id, v)
		}
	}

	// Undo restores the words; then the grid code can be sent.
	test.Tap(g.undo)
	if ids := changedGridWords(d); len(ids) != 0 {
		t.Errorf("after undo, changed grid words %v", ids)
	}
	if _, err := writeSettings(ctxT(t), s, d, false, nil); err != nil {
		t.Errorf("send after undo: %v", err)
	}
}

func TestGridSendAllSkipsGridWords(t *testing.T) {
	_, s, g, d := gridSetup(t, 2699481)
	grid := func(rep *vebus.WriteReport) (ids []uint16) {
		for _, r := range rep.Results {
			if vebus.GridCodeSetting(r.ID.Num) {
				ids = append(ids, r.ID.Num)
			}
		}
		return ids
	}
	rep, err := writeSettings(ctxT(t), s, d, true, nil)
	if err != nil || len(rep.Results) == 0 {
		t.Fatalf("send all = %+v, %v", rep, err)
	}
	if ids := grid(rep); len(ids) != 0 {
		t.Errorf("send all wrote grid settings %v", ids)
	}
	selectCode(t, g, 2)
	if rep, err = writeSettings(ctxT(t), s, d, true, nil); err != nil || !slices.Equal(grid(rep), []uint16{gridCodeSetting}) {
		t.Errorf("send all with a changed grid code wrote grid settings %v, %v; want 81 only", grid(rep), err)
	}
	if d.Dirty(vebus.SettingID(gridCodeSetting)) || len(d.ChangedIDs()) != 0 {
		t.Errorf("after send all, changed = %v", d.ChangedIDs())
	}
	if v := deviceValue(t, s, gridCodeSetting); v != 2 {
		t.Errorf("device grid code = %d, want 2", v)
	}
}

func TestGridSaveFile(t *testing.T) {
	u := newTestUI(t)
	newGridTab(u)
	spec := rvsctest.FromProfile(gridProfile(2699481), 192, 0x0A0B0C0D, "ZZ0001TEST")
	sf, err := parseSettingsFile("grid.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	d := u.settings.dev
	if err := d.SetRaw(vebus.SettingID(gridCodeSetting), 17); err != nil {
		t.Fatal(err)
	}
	data, err := sf.save(d)
	if err != nil {
		t.Fatalf("saving a changed grid code: %v", err)
	}
	spec.Units[0].Settings[gridCodeSetting] = 17
	if !bytes.Equal(data, spec.Bytes()) {
		t.Error("saved file differs from the original with only the grid code changed")
	}
	if err := d.SetRaw(vebus.SettingID(150), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.save(d); err == nil || !strings.Contains(err.Error(), "cannot be sent or saved yet") {
		t.Errorf("saving a changed grid parameter = %v, want refused", err)
	}
}

// TestGridDemoProfile: the demo devices have no grid code or grid words,
// so the parameters are not shown.
func TestGridDemoProfile(t *testing.T) {
	u := newTestUI(t)
	s := settingsSession(t)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	g := newGridTab(u)
	d, rep, err := readSettings(ctxT(t), s, 0, deviceSettingIDs(), nil)
	if err != nil || len(rep.Failed) > 0 {
		t.Fatalf("read: %v %v", err, rep.Failed)
	}
	u.settings.load(d, nil)
	if slices.Contains(g.subTabs.Items, g.paramsItem) {
		t.Error("grid parameters shown for a device without grid words")
	}
	if !strings.Contains(g.codeNote.Text, "does not report a grid code") || !g.code.Disabled() {
		t.Errorf("grid code note %q, disabled %v", g.codeNote.Text, g.code.Disabled())
	}
	if !slices.Contains(rowIDs(g.ordinary), vebus.FlagID(3)) {
		t.Error("UPS function flag 3 is not shown")
	}
}
