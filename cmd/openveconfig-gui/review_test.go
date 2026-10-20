package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// reviewProfile is the simulator's 12 V device with the AES current limit
// (setting 50), which needs a device reset after a change, and a battery
// capacity (setting 64) that the device changes only after its password
// has been entered.
func reviewProfile() simulator.Profile {
	p := simulator.Synthetic12V230()
	for i := range p.Settings {
		if p.Settings[i].ID == 64 {
			p.Settings[i].AccessLevel = 1
		}
	}
	p.Settings = append(p.Settings, simulator.Setting{ID: 50, Name: "AESLowCurrent", Sc: -10, Default: 8, Max: 100})
	return p
}

// reviewSetup returns a UI with the model of a reviewProfile device loaded.
func reviewSetup(t *testing.T) (*ui, *session, *vebus.Device) {
	t.Helper()
	u := newTestUI(t)
	s, _ := simSession(t, simulator.Device{Address: 0, Profile: reviewProfile()})
	u.sess = s // no listeners: the monitor poller would race with the test driver
	// Winmon mode 2: the device reports the access level of each setting
	// and refuses a write that needs a higher one.
	err := s.Do(ctxT(t), func(ctx context.Context, c *mk2.Client) error {
		return c.SetState(ctx, mk2.State{Switch: mk2.SwitchOn, Flags: mk2.StateFlagNoSend, Ext: &mk2.StateExt{}})
	})
	if err != nil {
		t.Fatal(err)
	}
	d := readModel(t, s, 0)
	u.settings.load(d, nil)
	return u, s, d
}

// set sets numeric setting id of d to text, as its value dialog does.
func set(t *testing.T, d *vebus.Device, id uint16, text string) {
	t.Helper()
	if err := setNumber(d, mustEntry(t, d, vebus.SettingID(id)), text); err != nil {
		t.Fatalf("setting %d to %s: %v", id, text, err)
	}
}

// dialogTexts returns the texts of the labels, rich texts and pills under o.
func dialogTexts(o fyne.CanvasObject) string {
	var texts []string
	for _, x := range findObjects(o, func(fyne.CanvasObject) bool { return true }) {
		switch w := x.(type) {
		case *widget.Label:
			texts = append(texts, w.Text)
		case *widget.RichText:
			texts = append(texts, w.String())
		case *pill:
			texts = append(texts, w.text)
		}
	}
	return strings.Join(texts, "\n")
}

// rowByID returns the row of id.
func rowByID(t *testing.T, rows []changeRow, id vebus.ID) changeRow {
	t.Helper()
	i := slices.IndexFunc(rows, func(r changeRow) bool { return r.id == id })
	if i < 0 {
		t.Fatalf("no row for %v", id)
	}
	return rows[i]
}

func hasMark(r changeRow, text string) bool {
	return slices.ContainsFunc(r.marks, func(m rowMark) bool { return m.text == text })
}

// pressKey sends a key to the window as the driver does when no widget
// has the focus.
func pressKey(u *ui, key fyne.KeyName) {
	if f := u.win.Canvas().OnTypedKey(); f != nil {
		f(&fyne.KeyEvent{Name: key})
	}
}

// TestReviewChanges checks the table of three changes, one of which needs a
// reset and one of which is invalid, the summary, the blocked primary
// action, reverting a row and the keyboard.
func TestReviewChanges(t *testing.T) {
	u, _, d := reviewSetup(t)
	set(t, d, 2, "14.6") // absorption
	set(t, d, 3, "15")   // float above absorption: invalid
	set(t, d, 50, "230") // AES: needs a reset
	u.settings.changed()

	rows := changeRows(u, d, lastRead(d))
	var ids []vebus.ID
	for _, r := range rows {
		ids = append(ids, r.id)
	}
	if len(rows) != 3 {
		t.Fatalf("rows %v, want settings 2, 3 and 50", ids)
	}
	abs := rowByID(t, rows, vebus.SettingID(2))
	if abs.group != "Charger" || abs.old != "14.40 V" || abs.new != "14.60 V" || abs.name != "Absorption voltage" || abs.ref != "setting 2" ||
		len(abs.marks) != 0 || abs.detail != "" {
		t.Errorf("absorption row %+v", abs)
	}
	if r := rowByID(t, rows, vebus.SettingID(3)); !hasMark(r, "invalid") || !r.detailError || r.detail == "" {
		t.Errorf("invalid float row %+v, want the invalid mark and the message", r)
	}
	if r := rowByID(t, rows, vebus.SettingID(50)); r.group != "Inverter" || !hasMark(r, "needs reset") || hasMark(r, "invalid") {
		t.Errorf("AES row %+v, want the reset mark on the Inverter page", r)
	}

	reviewChanges(u)
	dlg := u.win.Canvas().Overlays().Top()
	text := dialogTexts(dlg)
	for _, want := range []string{"3 changes · 1 needs a device reset · 1 invalid value", "Charger", "Inverter", "Absorption voltage",
		"14.40 V", "14.60 V", "needs reset", "invalid", "Correct the invalid values to send."} {
		if !strings.Contains(text, want) {
			t.Errorf("review dialog does not show %q", want)
		}
	}
	send := findButton(t, dlg, "Send settings")
	if !send.Disabled() {
		t.Error("Send to device is enabled with an invalid value")
	}
	pressKey(u, fyne.KeyReturn) // the primary is disabled: nothing happens
	if u.win.Canvas().Overlays().Top() != dlg {
		t.Error("Enter acted on a disabled primary action")
	}

	// The rows are in sidebar order, so the first revert button is that
	// of setting 50 on the Inverter page.
	reverts := findObjects(dlg, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == "" })
	if len(reverts) != 3 {
		t.Fatalf("%d revert buttons, want 3", len(reverts))
	}
	test.Tap(reverts[0].(*widget.Button))
	if d.Dirty(vebus.SettingID(50)) || !d.Dirty(vebus.SettingID(2)) {
		t.Errorf("after reverting the first row: 50 dirty %v, 2 dirty %v; want only 50 reverted",
			d.Dirty(vebus.SettingID(50)), d.Dirty(vebus.SettingID(2)))
	}
	if text := dialogTexts(dlg); !strings.Contains(text, "2 changes · 1 invalid value") || strings.Contains(text, "needs reset") {
		t.Errorf("after reverting: dialog shows %q", text)
	}

	// Fixing the invalid value enables the primary action; Escape closes.
	set(t, d, 3, "13.8")
	pressKey(u, fyne.KeyEscape)
	if top := u.win.Canvas().Overlays().Top(); top != nil {
		t.Error("Escape did not close the review dialog")
	}
	if u.win.Canvas().OnTypedKey() != nil {
		t.Error("the key handler of the dialog was left on the window")
	}
}

// TestReviewFileRevertAll reviews the changes of a settings file and
// reverts them all.
func TestReviewFileRevertAll(t *testing.T) {
	u := newTestUI(t)
	sf, err := parseSettingsFile("test.rvsc", testFile().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	d := u.settings.dev
	set(t, d, 2, "14.6")
	if err := d.SetFlag(5, true); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()
	reviewChanges(u)
	dlg := u.win.Canvas().Overlays().Top()
	if text := dialogTexts(dlg); !strings.Contains(text, "In file") || !strings.Contains(text, "test.rvsc") {
		t.Errorf("file review dialog shows %q", text)
	}
	if findButton(t, dlg, "Save settings").Disabled() {
		t.Error("Save file is disabled")
	}
	test.Tap(findButton(t, dlg, "Revert all"))
	test.Tap(findButton(t, u.win.Canvas().Overlays().Top(), "Discard changes"))
	if len(d.ChangedIDs()) != 0 {
		t.Errorf("after Revert all: changed %v", d.ChangedIDs())
	}
	if text := dialogTexts(dlg); !strings.Contains(text, "There are no unsent changes.") {
		t.Errorf("after Revert all the dialog shows %q", text)
	}
}

// TestPasswordMark checks that a change the device marks as needing a
// password is marked so.
func TestPasswordMark(t *testing.T) {
	u := newTestUI(t)
	d := vebus.NewDevice(nil)
	d.AddSetting(64, mk2.SettingInfo{Sc: 1, Default: 200, Min: 1, Max: 3000, AccessLevel: 1, HasAccessLevel: true}, 200)
	u.settings.load(d, nil)
	set(t, d, 64, "300")
	if r := rowByID(t, changeRows(u, d, lastRead(d)), vebus.SettingID(64)); !hasMark(r, "needs password") || r.old != "200 Ah" || r.new != "300 Ah" {
		t.Errorf("battery capacity row %+v, want 200 Ah → 300 Ah marked as needing a password", r)
	}
}

// TestSendResult sends changes of which the device refuses one for want of
// a password, and checks the status of each row and the notes.
func TestSendResult(t *testing.T) {
	u, s, d := reviewSetup(t)
	set(t, d, 2, "14.6") // verified
	set(t, d, 50, "230") // verified, needs a reset
	set(t, d, 64, "300") // refused: needs a password
	set(t, d, 65, "90")  // not sent: after the refusal
	// Flag 5 is in flag word 0, which is written first.
	if err := d.SetFlag(5, true); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()

	sendSettings(u)
	dlg := u.win.Canvas().Overlays().Top()
	if text := dialogTexts(dlg); !strings.Contains(text, "5 changes · 1 needs a device reset") || !strings.Contains(text, "Battery capacity") {
		t.Errorf("send dialog shows %q", text)
	}
	if findButton(t, dlg, "Send").Disabled() {
		t.Fatal("Send is disabled")
	}
	all := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.RadioGroup); return ok })[0].(*widget.RadioGroup)
	all.SetSelected("All settings")
	if !strings.Contains(dialogTexts(dlg), "grid parameters are left as the device has them") {
		t.Error("choosing All settings does not explain how the grid words are handled")
	}
	pressKey(u, fyne.KeyEscape)

	before := lastRead(d)
	rep, err := writeSettings(ctxT(t), s, d, false, nil)
	if err == nil {
		t.Fatal("write succeeded, want the refusal of setting 64")
	}
	rows := resultRows(u, d, before, rep)
	for _, want := range []struct {
		id    vebus.ID
		state rowState
		reset bool
	}{
		{vebus.FlagID(5), stateVerified, false},
		{vebus.SettingID(2), stateVerified, false},
		{vebus.SettingID(50), stateVerified, true},
		{vebus.SettingID(64), stateRefused, false},
		{vebus.SettingID(65), stateNotSent, false},
	} {
		r := rowByID(t, rows, want.id)
		if r.state != want.state || hasMark(r, "needs reset") != want.reset {
			t.Errorf("%v: state %d, marks %v; want state %d, reset mark %v", want.id, r.state, r.marks, want.state, want.reset)
		}
	}
	if len(rows) != 5 {
		t.Errorf("%d rows, want 5 (flag word 0 shown as flag 5)", len(rows))
	}
	if r := rowByID(t, rows, vebus.SettingID(64)); !strings.Contains(r.detail, "password") || r.old != "200 Ah" || r.new != "300 Ah" {
		t.Errorf("refused row %+v", r)
	}

	showSendResult(u, d, before, rep, err, nil, nil)
	text := dialogTexts(u.win.Canvas().Overlays().Top())
	for _, want := range []string{"3 sent · 1 refused · 1 not sent", "Verified", "Refused", "Not sent", "password has been entered",
		"1 change takes effect only after the device is reset.", "sending stopped before this setting"} {
		if !strings.Contains(text, want) {
			t.Errorf("result dialog does not show %q", want)
		}
	}
	if v := deviceValue(t, s, 64); v != 200 {
		t.Errorf("device battery capacity = %d, want 200 (refused)", v)
	}
	if !d.Dirty(vebus.SettingID(64)) || !d.Dirty(vebus.SettingID(65)) || d.Dirty(vebus.SettingID(2)) {
		t.Error("after the send, the refused and the unsent settings should be the only changes left")
	}
}

// TestFileApplyDialog checks the comparison of a file with a device: the
// match, the differences in the table, a blocked send for an invalid value
// and the refusal of a file for other firmware.
func TestFileApplyDialog(t *testing.T) {
	u := newTestUI(t)
	s := settingsSession(t)
	u.sess = s // no listeners: the monitor poller would race with the test driver
	spec := testFile()
	sf, err := parseSettingsFile("test.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	read := func() *vebus.Device {
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
		return dev
	}
	fileDev, _ := sf.device()
	set(t, fileDev, 2, "14.6")
	dev := read()
	confirmFileSend(u, s, fileDev, dev, vebus.MatchRVSC(sf.info, sf.unit, dev), false, nil)
	dlg := u.win.Canvas().Overlays().Top()
	text := dialogTexts(dlg)
	for _, want := range []string{"The file fits the device at address 0.", "1 setting differs", "On device", "From file",
		"Absorption voltage", "14.40 V", "14.60 V"} {
		if !strings.Contains(text, want) {
			t.Errorf("file dialog does not show %q", want)
		}
	}
	if findButton(t, dlg, "Send").Disabled() {
		t.Error("Send is disabled for a fitting file")
	}
	pressKey(u, fyne.KeyEscape)

	// An invalid value in the file blocks sending.
	set(t, fileDev, 3, "15")
	dev = read()
	confirmFileSend(u, s, fileDev, dev, vebus.MatchRVSC(sf.info, sf.unit, dev), false, nil)
	dlg = u.win.Canvas().Overlays().Top()
	if !findButton(t, dlg, "Send").Disabled() || !strings.Contains(dialogTexts(dlg), "not valid") {
		t.Error("an invalid value in the file does not block sending")
	}
	pressKey(u, fyne.KeyEscape)

	// A file for other firmware is refused with the reason.
	spec.Units[0].Firmware++
	other, err := parseSettingsFile("other.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	otherDev, _ := other.device()
	confirmFileSend(u, s, otherDev, dev, vebus.MatchRVSC(other.info, other.unit, dev), false, nil)
	dlg = u.win.Canvas().Overlays().Top()
	if !strings.Contains(dialogTexts(dlg), "does not fit") || !strings.Contains(dialogTexts(dlg), "Firmware version") {
		t.Errorf("mismatch dialog shows %q", dialogTexts(dlg))
	}
	if len(findObjects(dlg, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == "Send" })) > 0 {
		t.Error("a file for other firmware can be sent")
	}
	if v := deviceValue(t, s, 2); v != 1440 {
		t.Errorf("device absorption = %d, want 1440 (nothing sent)", v)
	}
}

// TestGetSettingsConfirmCount checks that Get asks with the number of
// unsent changes.
func TestGetSettingsConfirmCount(t *testing.T) {
	u, _, d := reviewSetup(t)
	set(t, d, 2, "14.6")
	set(t, d, 50, "230")
	u.settings.changed()
	getSettings(u)
	dlg := u.win.Canvas().Overlays().Top()
	if !strings.Contains(dialogTexts(dlg), "You have 2 changes that were not sent or saved.") {
		t.Errorf("Get confirmation shows %q", dialogTexts(dlg))
	}
	findButton(t, dlg, "Discard and read")
	pressKey(u, fyne.KeyEscape)
	if u.win.Canvas().Overlays().Top() != nil || !d.Dirty(vebus.SettingID(2)) {
		t.Error("Escape did not cancel the Get confirmation")
	}
}
