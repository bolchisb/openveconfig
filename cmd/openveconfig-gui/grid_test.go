package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/internal/rvsctest"
	"github.com/bolchisb/openveconfig/pkg/gridcode"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
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
	if !d.Dirty(vebus.SettingID(gridCodeSetting)) || !marked(g.codeRow) {
		t.Error("grid code not marked changed")
	}
	if p, _ := gridcode.PageFor(17); g.page != p || len(g.rows) == 0 {
		t.Errorf("page %v with %d rows, want the Europe page", g.page, len(g.rows))
	}
	if !strings.Contains(labelTexts(g.params), "Not supported yet") {
		t.Error("the page's unsupported parameters are not listed")
	}

	// A changed grid code is never sent to a device.
	if rep, err := writeSettings(ctxT(t), s, d, false, nil); rep != nil || !errors.Is(err, gridcode.ErrDeviceWrite) {
		t.Fatalf("write = %+v, %v; want refused", rep, err)
	}
	if v := deviceValue(t, s, gridCodeSetting); v != 0 {
		t.Errorf("device grid code = %d, want 0 (nothing sent)", v)
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
		if g.codeRow.passwd.Visible() {
			t.Errorf("firmware %d: code 0 marked as needing a password", tc.fw)
		}
		selectCode(t, g, 2)
		if !g.codeRow.passwd.Visible() {
			t.Errorf("firmware %d: code 2 not marked as needing a password", tc.fw)
		}
	}
}

func TestGridParamEditBlocksSend(t *testing.T) {
	u, s, g, d := gridSetup(t, 2699481)
	selectCode(t, g, 17)

	// A number in its entry: 3.1 s in the top 5 bits of 143.
	r := paramRowOf(t, g, "Over-frequency response: start delay")
	enter(r.formRow, "3.2")
	if w, _ := d.Raw(vebus.SettingID(143)); w != 0 || !strings.HasPrefix(errorText(r.formRow), "Enter a value from") {
		t.Errorf("3.2 s (above the maximum): word 143 = %#04x, error %q; want refused with the range", w, errorText(r.formRow))
	}
	enter(r.formRow, "3.1")
	if w, _ := d.Raw(vebus.SettingID(143)); w != 31<<11 || errorText(r.formRow) != "" {
		t.Errorf("word 143 = %#04x, error %q; want %#04x", w, errorText(r.formRow), 31<<11)
	}
	if !marked(r.formRow) || !d.Dirty(vebus.SettingID(143)) {
		t.Errorf("start delay marked %v, dirty %v", marked(r.formRow), d.Dirty(vebus.SettingID(143)))
	}
	if paramRowOf(t, g, "Over-frequency response: end delay").changed {
		t.Error("end delay, other bits of word 143, marked as changed")
	}

	// A check box (inverted) and a choice in the same word 148.
	inhibit := paramRowOf(t, g, "Use auxiliary input 1 as feed-in inhibit").control.(*widget.Check)
	if !inhibit.Checked {
		t.Error("inverted check box with its bit clear is not ticked")
	}
	test.Tap(inhibit)
	method := paramRowOf(t, g, "P(U) reduction method").control.(*widget.RadioGroup)
	method.SetSelected(method.Options[2])
	if w, _ := d.Raw(vebus.SettingID(148)); w != 0x4|2<<3 {
		t.Errorf("word 148 = %#04x, want %#04x", w, 0x4|2<<3)
	}
	if st := modelStatus(u.settings); !strings.Contains(st, "cannot be sent straight to a device") {
		t.Errorf("status %q does not report the grid changes", st)
	}
	if _, _, why := primaryAction(u, countChanges(d)); !strings.Contains(why, "Undo the grid code") {
		t.Errorf("changes bar reason %q, want the grid changes", why)
	}
	for _, r := range changeRows(u, d, lastRead(d)) {
		if vebus.GridCodeSetting(r.id.Num) && !hasMark(r, "cannot be sent yet") {
			t.Errorf("review row of %v is not marked as not sendable", r.id)
		}
	}

	// Sending is refused before anything is written, also the grid code.
	sendSettings(u)
	if dlg := u.win.Canvas().Overlays().Top(); dlg == nil || !strings.Contains(labelTexts(dlg), "cannot be sent straight to a device yet") {
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

	// Undo restores the words; the changed grid code still blocks sending
	// until it is reverted too.
	test.Tap(g.undo)
	if ids := changedGridWords(d); len(ids) != 0 {
		t.Errorf("after undo, changed grid words %v", ids)
	}
	if _, err := writeSettings(ctxT(t), s, d, false, nil); !errors.Is(err, gridcode.ErrDeviceWrite) {
		t.Errorf("send with the grid code changed: %v, want refused", err)
	}
	test.Tap(g.codeRow.revert)
	if err := d.SetRaw(vebus.SettingID(44), 181); err != nil {
		t.Fatal(err)
	}
	if rep, err := writeSettings(ctxT(t), s, d, false, nil); err != nil || len(rep.Results) != 1 {
		t.Errorf("send after undoing the grid changes = %+v, %v", rep, err)
	}
	if v := deviceValue(t, s, 44); v != 181 {
		t.Errorf("device setting 44 = %d, want 181", v)
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
	if rep, err = writeSettings(ctxT(t), s, d, true, nil); rep != nil || !errors.Is(err, gridcode.ErrDeviceWrite) {
		t.Errorf("send all with a changed grid code = %+v, %v; want refused", rep, err)
	}
	if v := deviceValue(t, s, gridCodeSetting); v != 0 {
		t.Errorf("device grid code = %d, want 0 (nothing sent)", v)
	}
}

// gridInfoTail returns invented bytes for the end of a file's setting-info
// section: undecoded bytes, n access bytes and a grid-info table (format
// 3) in which grid code c has the parameter-set version versions[c].
func gridInfoTail(n int, versions []byte) []byte {
	table := []byte{0, 0, 3, byte(len(versions)), 8}
	table = append(table, make([]byte, 2*len(versions))...)
	for c, v := range versions {
		binary.LittleEndian.PutUint16(table[5+2*c:], uint16(len(table)))
		table = append(table, v, 0, 0) // version, flags
		table = append(table, make([]byte, 16)...)
	}
	binary.LittleEndian.PutUint16(table, uint16(len(table)-5))
	tail := append([]byte{0x50, 0, 0, 0}, bytes.Repeat([]byte{0x81}, n)...)
	tail = binary.LittleEndian.AppendUint16(tail, uint16(len(table)))
	return append(tail, table...)
}

// gridFile returns a synthetic one-unit file of a gridProfile device with
// a grid-info table where code 17 has version 2 and every other code 0,
// and the checker words a and b.
func gridFile(a, b uint16) rvsctest.File {
	spec := rvsctest.FromProfile(gridProfile(2699481), 192, 0x0A0B0C0D, "ZZ0001TEST")
	versions := make([]byte, 33)
	versions[17] = 2
	spec.InfoTail = gridInfoTail(192, versions)
	spec.Units[0].Settings[128], spec.Units[0].Settings[191] = a, b
	return spec
}

// TestGridSaveFile saves grid changes into a file: the checker words are
// stamped from the file's pair, the new grid code and its version in the
// file's grid-info table, and every other byte stays as it was.
func TestGridSaveFile(t *testing.T) {
	u := newTestUI(t)
	g := newGridTab(u)
	spec := gridFile(0x0300, 0x0300)
	sf, err := parseSettingsFile("grid.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	if text := labelTexts(g.params); !strings.Contains(text, "fit the grid code.") {
		t.Errorf("check word status not shown as consistent: %q", text)
	}
	save := func(what string, want rvsctest.File) {
		t.Helper()
		data, err := sf.save(u.settings.dev)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if !bytes.Equal(data, want.Bytes()) {
			t.Errorf("%s: saved file is not the original with the expected changes", what)
		}
		loadSettingsFile(u, sf) // as saveSettingsFile does
	}

	// Grid code 0 to 17 (version 2): next generation 4.
	selectCode(t, g, 17)
	spec.Units[0].Settings[gridCodeSetting] = 17
	spec.Units[0].Settings[128], spec.Units[0].Settings[191] = 0x0411, 0x0413
	save("grid code changed", spec)
	if text := labelTexts(g.params); !strings.Contains(text, "fit the grid code.") {
		t.Errorf("after saving, check word status %q", text)
	}

	// A parameter word: generation 5, same code.
	if err := u.settings.dev.SetRaw(vebus.SettingID(150), 1); err != nil {
		t.Fatal(err)
	}
	spec.Units[0].Settings[150] = 1
	spec.Units[0].Settings[128], spec.Units[0].Settings[191] = 0x0511, 0x0513
	save("grid parameter changed", spec)

	// No grid change: the checker words stay.
	if err := u.settings.dev.SetRaw(vebus.SettingID(44), 181); err != nil {
		t.Fatal(err)
	}
	spec.Units[0].Settings[44] = 181
	save("other setting changed", spec)

	// The file's pair's generation is next: one more (an interrupted write).
	spec = gridFile(0x0300, 0x0400)
	if sf, err = parseSettingsFile("grid.rvsc", spec.Bytes()); err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	if text := labelTexts(g.params); !strings.Contains(text, "do not fit the grid code") {
		t.Errorf("inconsistent pair: check word status %q", text)
	}
	selectCode(t, g, 1)
	spec.Units[0].Settings[gridCodeSetting] = 1
	spec.Units[0].Settings[128], spec.Units[0].Settings[191] = 0x0501, 0x0501
	save("grid code changed after an interrupted write", spec)
}

// TestGridCheckStatusDevice: for a device the parameter-set version is
// not known, so only the rest of the check is made.
func TestGridCheckStatusDevice(t *testing.T) {
	_, _, g, _ := gridSetup(t, 2699481)
	if text := labelTexts(g.params); !strings.Contains(text, "fit the grid code. Their parameter-set version is not checked") {
		t.Errorf("check word status %q", text)
	}
	if text := labelTexts(g.params); !strings.Contains(text, "saved to a settings file") {
		t.Errorf("grid page notes %q", text)
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

// TestGridCodePicker chooses a grid code in the picker's dialog: the search
// narrows the list, and a tap on an entry sets the code and closes it.
func TestGridCodePicker(t *testing.T) {
	u, _, g, d := gridSetup(t, 2699481)
	if g.code.button.Text != "0 · None" {
		t.Errorf("button %q, want the code 0", g.code.button.Text)
	}
	g.code.button.OnTapped()
	dlg := u.win.Canvas().Overlays().Top()
	if dlg == nil {
		t.Fatal("no dialog")
	}
	entry := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Entry); return ok })[0].(*widget.Entry)
	list := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.List); return ok })[0].(*widget.List)
	entry.SetText("roman")
	if n := list.Length(); n != 1 {
		t.Fatalf("%d codes match \"roman\", want 1", n)
	}
	list.Select(0)
	if raw, _ := d.Raw(vebus.SettingID(gridCodeSetting)); raw != 14 {
		t.Errorf("grid code %d, want 14", raw)
	}
	if u.win.Canvas().Overlays().Top() != nil {
		t.Error("the dialog is still open")
	}
	if g.code.button.Text != "14 · Romania" {
		t.Errorf("button %q after the choice", g.code.button.Text)
	}
}
