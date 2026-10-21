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
	"github.com/bolchisb/openveconfig/pkg/mk2"
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
	if p, _ := gridcode.PageFor(17, -1); g.page == nil || g.page.Name != p.Name || len(g.rows) == 0 {
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

	// A code whose page is not known says so: on a device, an AS/NZS
	// 4777.2:2015 code, whose page follows the parameter-set version.
	selectCode(t, g, 6)
	if g.rows != nil || !strings.Contains(labelTexts(g.params), "not known yet") {
		t.Errorf("code 6: %d rows, text %q", len(g.rows), labelTexts(g.params))
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
	if text := labelTexts(g.params); strings.Contains(text, "check words") {
		t.Errorf("consistent check words get a note: %q", text)
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
	if text := labelTexts(g.params); strings.Contains(text, "check words") {
		t.Errorf("after saving, check word note %q", text)
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

// defaultsTailTable returns a made-up grid-info table (format 3) for codes
// 0-17: code 17 (version 1) uses words 128, 129, 150 and 191, with the
// defaults 0x1234 for 129 and 0x0042 for 150; every other code has an
// empty entry.
func defaultsTailTable() []byte {
	const codes = 18
	b := []byte{0, 0, 3, codes, 8}
	empty := len(b) + 2*codes
	for c := range codes {
		off := empty
		if c == 17 {
			off = empty + 3 + 16
		}
		b = binary.LittleEndian.AppendUint16(b, uint16(off))
	}
	b = append(b, make([]byte, 3+16)...) // empty entry: version, u16, two bitmaps
	e := append([]byte{1, 0, 0}, make([]byte, 16)...)
	for _, w := range []int{128, 129, 150, 191} {
		e[3+8+(w-128)/8] |= 1 << ((w - 128) % 8)
	}
	e = binary.LittleEndian.AppendUint16(e, 0x1234) // word 129
	e = binary.LittleEndian.AppendUint16(e, 0x0042) // word 150
	b = append(append(b, e...), 0)                  // no masks
	binary.LittleEndian.PutUint16(b, uint16(len(b)-5))
	return b
}

// TestGridCodeDefaults: choosing a grid code in a file loads the defaults
// its grid-info table gives the code, as the original does, and 0xFFFF in
// the words without one; the checker words are left to be stamped. The
// same code chosen again, or a model without a known table, changes no
// word.
func TestGridCodeDefaults(t *testing.T) {
	u := newTestUI(t)
	g := newGridTab(u)
	spec := gridFile(0x0300, 0x0300)
	table := defaultsTailTable()
	tail := append([]byte{0x50, 0, 0, 0}, bytes.Repeat([]byte{0x81}, 192)...)
	tail = binary.LittleEndian.AppendUint16(tail, uint16(len(table)))
	spec.InfoTail = append(tail, table...)
	sf, err := parseSettingsFile("grid.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	d := u.settings.dev
	if !u.settings.grid.Known {
		t.Fatal("the file's grid-info table is not known")
	}
	word := func(w uint16) uint16 {
		v, _ := d.Raw(vebus.SettingID(w))
		return v
	}

	selectCode(t, g, 17)
	for w := uint16(128); w <= 191; w++ {
		want := uint16(0xFFFF)
		switch w {
		case 128, 191:
			want = 0x0300 // stamped when saved
		case 129:
			want = 0x1234
		case 150:
			want = 0x0042
		}
		if got := word(w); got != want {
			t.Errorf("code 17: word %d = %#04x, want %#04x", w, got, want)
		}
	}
	if !paramRowOf(t, g, "Over-voltage trip on 10-minute running average").changed {
		t.Error("a word changed by the defaults is not marked as changed")
	}

	// The same code again: nothing is loaded.
	if err := d.SetRaw(vebus.SettingID(150), 7); err != nil {
		t.Fatal(err)
	}
	g.code.onChanged(g.code.SelectedIndex())
	if word(150) != 7 {
		t.Errorf("code 17 chosen again: word 150 = %#04x, want it kept", word(150))
	}

	// A code with an empty entry: every word 0xFFFF.
	selectCode(t, g, 3)
	if word(129) != 0xFFFF || word(150) != 0xFFFF {
		t.Errorf("code 3: words 129, 150 = %#04x, %#04x; want 0xFFFF", word(129), word(150))
	}

	// No known table: the words stay.
	if before, err := loadCodeDefaults(d, gridcode.Model{}, 17); before != nil || err != nil || word(129) != 0xFFFF {
		t.Errorf("no table: %v, %v, word 129 = %#04x", before, err, word(129))
	}
}

// TestGridCheckStatusDevice: for a device the parameter-set version is
// not known, so only the rest of the check is made.
func TestGridCheckStatusDevice(t *testing.T) {
	_, _, g, _ := gridSetup(t, 2699481)
	if text := labelTexts(g.params); strings.Contains(text, "check words") {
		t.Errorf("consistent check words get a note: %q", text)
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
	givePassword(t, u, "OK") // Romania needs the password
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

// TestGridRomaniaPage checks the Romania page against VEConfigure's, with
// the grid words of a real file: the tab named after the page, the
// over-voltage value from two words, the trip delays, the P(U) voltages
// greyed while the curve is not used, the reactive-power rows of the
// regulation, and no technical captions.
func TestGridRomaniaPage(t *testing.T) {
	u, _, g, d := gridSetup(t, 2699481)
	for id, v := range map[uint16]uint16{gridCodeSetting: 14, 129: 59728, 130: 77, 131: 25300, 132: 25760,
		133: 64650, 134: 3, 135: 3, 147: 42993, 148: 0, 149: 4165, 151: 32768} {
		if err := d.SetRaw(vebus.SettingID(id), v); err != nil {
			t.Fatal(err)
		}
	}
	u.settings.load(d, nil)
	if want := "Romania grid code settings (TOR-D4 derivative)"; g.paramsItem.Text != want {
		t.Errorf("tab %q, want %q", g.paramsItem.Text, want)
	}
	shown := func() []string {
		var names []string
		for _, r := range g.rows {
			if r.root.Visible() {
				names = append(names, r.label.Text)
			}
		}
		return names
	}
	// Reactive power regulation 0: a fixed cos phi.
	if want := []string{"High frequency reconnect value", "Trip delay U<", "Trip delay U>>", "Over voltage U> (10 min. running mean)",
		"Reconnection time", "P(U) curve", "UKnick", "UGrenz", "Reactive power regulation", "Filter time reactive power (1T)", "Cos phi is"}; !slices.Equal(shown(), want) {
		t.Errorf("rows %q, want %q", shown(), want)
	}
	// The values VEConfigure shows for this file.
	for name, want := range map[string]string{
		"Under-voltage trip delay":              "0.0",
		"Fast over-voltage trip delay":          "0.0",
		"Reactive-power response time constant": "3.3",
		"Constant cos phi (fixed mode)":         "1.00",
	} {
		if got := paramRowOf(t, g, name).control.(*numberEntry).Text; got != want {
			t.Errorf("%s shows %q, want %q", name, got, want)
		}
	}
	// Regulation 3: the Q(U) points.
	if err := d.SetRaw(vebus.SettingID(148), 3); err != nil {
		t.Fatal(err)
	}
	g.update(false)
	if want := []string{"Q at point 1", "Reactive power curve point 1", "Q at point 2", "Reactive power curve point 2",
		"Q at point 3", "Reactive power curve point 3", "Q at point 4", "Reactive power curve point 4"}; !slices.Equal(shown()[10:], want) {
		t.Errorf("rows %q with Q(U), want %q after the first 10", shown(), want)
	}
	if err := d.SetRaw(vebus.SettingID(148), 0); err != nil {
		t.Fatal(err)
	}
	g.update(false)
	// Choosing a fixed Q sets it to 0, as the original does.
	if err := d.SetRaw(vebus.SettingID(151), 40000); err != nil {
		t.Fatal(err)
	}
	paramRowOf(t, g, "Reactive-power control mode").control.(*widget.Select).SetSelectedIndex(2)
	if w, _ := d.Raw(vebus.SettingID(151)); w != 32768 {
		t.Errorf("fixed Q stored as %d after choosing it, want 32768 (0 %%)", w)
	}
	if err := d.SetRaw(vebus.SettingID(148), 0); err != nil {
		t.Fatal(err)
	}
	g.update(false)
	ov := paramRowOf(t, g, "Over-voltage trip on 10-minute running average")
	if got := ov.control.(*numberEntry).Text; got != "255.3" {
		t.Errorf("over voltage shows %q, want 255.3", got)
	}
	knee := paramRowOf(t, g, "P(U) curve: knee voltage")
	if !knee.control.(*numberEntry).Disabled() {
		t.Error("U Knick is enabled while the P(U) curve is not used")
	}
	if strings.Contains(labelTexts(g.params), "bits 0x") {
		t.Error("technical captions are shown")
	}
	// Editing the over-voltage writes both words.
	ov.control.(*numberEntry).commit("260")
	if lo, _ := d.Raw(vebus.SettingID(129)); lo != uint16(5200000&0xFFFF) {
		t.Errorf("setting 129 = %d", lo)
	}
	if hi, _ := d.Raw(vebus.SettingID(130)); hi != uint16(5200000>>16) {
		t.Errorf("setting 130 = %d", hi)
	}
}

// TestGridPolandPage checks the Poland page: the start frequency in the
// high byte of its word with three decimals, written to both bytes, and
// the LFSM-O box that switches it off (0xFFFF) and greys it and the droop.
func TestGridPolandPage(t *testing.T) {
	u, _, g, d := gridSetup(t, 2699481)
	for id, v := range map[uint16]uint16{gridCodeSetting: 32, 142: 0x0E0E, 144: 60, 148: 0} {
		if err := d.SetRaw(vebus.SettingID(id), v); err != nil {
			t.Fatal(err)
		}
	}
	u.settings.load(d, nil)
	if want := "Polish grid code settings (EU 2016/631)"; g.paramsItem.Text != want {
		t.Errorf("tab %q, want %q", g.paramsItem.Text, want)
	}
	start := paramRowOf(t, g, "Over-frequency power reduction: start frequency")
	droop := paramRowOf(t, g, "Over-frequency power reduction: droop")
	box := paramRowOf(t, g, "Limited frequency-sensitive mode (over-frequency) enabled").control.(*widget.Check)
	if got := start.control.(*numberEntry).Text; got != "50.350" || !box.Checked {
		t.Errorf("start frequency %q, LFSM-O ticked %t; want 50.350, ticked", got, box.Checked)
	}
	start.control.(*numberEntry).commit("50.2")
	if w, _ := d.Raw(vebus.SettingID(142)); w != 0x0808 {
		t.Errorf("setting 142 = %#04x, want 0x0808", w)
	}
	box.SetChecked(false)
	if w, _ := d.Raw(vebus.SettingID(142)); w != 0xFFFF || !start.control.(*numberEntry).Disabled() || !droop.control.(*numberEntry).Disabled() {
		t.Errorf("LFSM-O off: setting 142 = %#04x, start frequency and droop greyed %t, %t", w,
			start.control.(*numberEntry).Disabled(), droop.control.(*numberEntry).Disabled())
	}
	box.SetChecked(true)
	if w, _ := d.Raw(vebus.SettingID(142)); w != 0x0808 || start.control.(*numberEntry).Disabled() {
		t.Errorf("LFSM-O on: setting 142 = %#04x, start frequency greyed %t", w, start.control.(*numberEntry).Disabled())
	}
}

// madeUpGridTable returns a made-up grid-info table, as a device sends it:
// format 3, grid codes 0 to 9 with version 0, except France (9) at version 2.
func madeUpGridTable() []byte {
	const codes = 10
	b := []byte{0, 0, 3, codes, 8}
	at := len(b) + 2*codes
	entry := func(v byte) []byte {
		e := append([]byte{v, 0, 0}, make([]byte, 16)...)
		e[3+8], e[3+15] = 0x01, 0x80 // words 128 and 191 in the second bitmap
		return append(e, 0)
	}
	var entries []byte
	for c := range codes {
		v := byte(0)
		if c == 9 {
			v = 2
		}
		b = append(b, byte(at+len(entries)), byte((at+len(entries))>>8))
		entries = append(entries, entry(v)...)
	}
	b = append(b, entries...)
	binary.LittleEndian.PutUint16(b, uint16(len(b)))
	var sum uint16
	for i, x := range b {
		sum += uint16(x) << (8 * (i % 2))
	}
	return binary.LittleEndian.AppendUint16(b, sum)
}

// TestGridDeviceTable checks that the grid-info table read from a device
// sets the page as from a file: France at version 2 gets the compact
// reactive-power layout and the VFR 2019 caption, and its entry of the
// grid code list the VFR 2019 standard.
func TestGridDeviceTable(t *testing.T) {
	for _, table := range [][]byte{madeUpGridTable(), nil} {
		sim := simulator.New(simulator.Config{VersionInterval: -1, GridInfo: table,
			Devices: []simulator.Device{{Address: 0, Profile: gridProfile(2699481)}}})
		s := &session{client: mk2.NewClient(sim.Port(), nil), lock: make(chan struct{}, 1), devices: []byte{0}}
		t.Cleanup(func() { s.close() })
		m := readGridInfo(ctxT(t), s, 0)
		// The read table may end with the padding of its last part.
		if !m.Known || len(m.Table) < len(table) || !bytes.Equal(m.Table[:len(table)], table) {
			t.Fatalf("table %x read as %+v", table, m)
		}
		p, _ := gridPageFor(9, m)
		compact := slices.ContainsFunc(p.Params, func(q gridcode.Param) bool { return q.Name == "Lowest cos phi allowed while following a Q set-point" })
		if want := table != nil; compact != want || (p.Title == "VFR 2019 grid code settings") != want {
			t.Errorf("table %t: compact layout %t, title %q", want, compact, p.Title)
		}
		want := "UTE C15-712-1:2013-07"
		if table != nil {
			want = "VDE V 0126-1-1 VFR 2019"
		}
		if it := gridCodeItem(9, m, 2699481); it.subtitle != want || !strings.HasSuffix(it.label, want+" (needs password)") {
			t.Errorf("table %t: list entry %q, %q; want the standard %q", table != nil, it.subtitle, it.label, want)
		}
	}
}

// TestGridNSProtection chooses the Germany codes for a file of a device
// with and without built-in interface protection: the question offers the
// other code, and Cancel keeps the code as it was.
func TestGridNSProtection(t *testing.T) {
	u := newTestUI(t)
	g := newGridTab(u)
	sf, err := parseSettingsFile("grid.rvsc", gridFile(0x0300, 0x0300).Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	code := func() uint16 {
		v, _ := u.settings.dev.Raw(vebus.SettingID(gridCodeSetting))
		return v
	}
	answer := func(button string) {
		t.Helper()
		dlg := u.win.Canvas().Overlays().Top()
		if dlg == nil {
			t.Fatal("no question")
		}
		bs := findObjects(dlg, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == button })
		if len(bs) != 1 {
			t.Fatalf("no %q button in %q", button, labelTexts(dlg))
		}
		test.Tap(bs[0].(*widget.Button))
	}
	sf.unit.VersionExtra = []byte{0x08, 0x04}
	for _, tc := range []struct {
		button string
		want   uint16
	}{{"Cancel", 0}, {"Yes", 5}, {"No", 4}} {
		selectCode(t, g, 4)
		answer(tc.button)
		if tc.want != 0 {
			givePassword(t, u, "OK") // 4 and 5 need the password
		}
		if c := code(); c != tc.want {
			t.Errorf("code 4 with internal protection, %s: code %d, want %d", tc.button, c, tc.want)
		}
	}
	selectCode(t, g, 5) // fits: no question but the password's
	givePassword(t, u, "OK")
	if c := code(); c != 5 || u.win.Canvas().Overlays().Top() != nil {
		t.Errorf("code 5 with internal protection: code %d", c)
	}

	sf.unit.VersionExtra = nil
	selectCode(t, g, 4)
	givePassword(t, u, "OK")
	for _, tc := range []struct {
		button string
		want   uint16
	}{{"Cancel", 4}, {"OK", 4}} {
		selectCode(t, g, 5)
		answer(tc.button)
		if c := code(); c != tc.want {
			t.Errorf("code 5 without internal protection, %s: code %d, want %d", tc.button, c, tc.want)
		}
	}
}

// givePassword answers the access password dialog on top of u's window
// with a made-up password and button.
func givePassword(t *testing.T, u *ui, button string) {
	t.Helper()
	dlg := u.win.Canvas().Overlays().Top()
	if dlg == nil || !strings.Contains(labelTexts(dlg), "Enter access password") {
		t.Fatal("no access password dialog")
	}
	entry := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Entry); return ok })[0].(*widget.Entry)
	entry.SetText("MADE-UP")
	test.Tap(findButton(t, dlg, button))
}

// TestGridCodeAsksPassword chooses a code that needs a password, as in the
// original: for a settings file it is asked at every new choice and
// Cancel undoes the choice; with a device it is asked once per
// connection; choosing a code that needs none, or the same code again,
// asks nothing.
func TestGridCodeAsksPassword(t *testing.T) {
	u := newTestUI(t)
	g := newGridTab(u)
	sf, err := parseSettingsFile("grid.rvsc", gridFile(0x0300, 0x0300).Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	d := u.settings.dev
	code := func() uint16 {
		v, _ := d.Raw(vebus.SettingID(gridCodeSetting))
		return v
	}
	selectCode(t, g, 14)
	givePassword(t, u, "Cancel")
	if c := code(); c != 0 {
		t.Errorf("file, Cancel: code %d, want 0", c)
	}
	selectCode(t, g, 14)
	givePassword(t, u, "OK")
	if c := code(); c != 14 || u.win.Canvas().Overlays().Top() != nil {
		t.Errorf("file, OK: code %d", c)
	}
	selectCode(t, g, 14) // the same code: nothing asked
	selectCode(t, g, 0)  // needs no password
	if u.win.Canvas().Overlays().Top() != nil {
		t.Error("a password is asked for the same code or for code 0")
	}
	selectCode(t, g, 14) // a file keeps no password: asked again
	givePassword(t, u, "OK")

	s := fakeSession(false)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	selectCode(t, g, 17)
	givePassword(t, u, "OK")
	if len(s.passwords) != 1 {
		t.Errorf("device: %d passwords kept, want 1", len(s.passwords))
	}
	selectCode(t, g, 14) // already given for this connection
	if c := code(); c != 14 || u.win.Canvas().Overlays().Top() != nil {
		t.Errorf("device, second code: code %d, or asked again", c)
	}
}
