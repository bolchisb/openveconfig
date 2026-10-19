package main

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// settingsTabSet are the tabs whose settings "Set all to default" changes:
// the tabs of settingstabs.go. System configuration and the grid tab are
// left alone.
var settingsTabSet = []vebus.Tab{vebus.TabGeneral, vebus.TabInverter, vebus.TabCharger, vebus.TabVirtualSwitch, vebus.TabAdvanced}

// settingsBar is the row of settings actions above the tabs.
type settingsBar struct {
	u        *ui
	get      *widget.Button
	send     *widget.Button
	defaults *widget.Button
	find     *widget.Button
	// units chooses the device that Get reads when several answered.
	units  *widget.Select
	addrs  []byte // parallel to units.Options
	status *widget.Label
}

// newSettingsBar returns the settings actions and the state of the model.
func newSettingsBar(u *ui) fyne.CanvasObject {
	b := &settingsBar{u: u}
	b.get = widget.NewButton(tr("Get settings"), b.getSettings)
	b.send = widget.NewButton(tr("Send settings"), func() { sendSettings(u) })
	b.defaults = widget.NewButton(tr("Set all to default"), func() { setAllToDefault(u) })
	b.find = widget.NewButton(tr("Find setting"), func() { findSetting(u) })
	b.units = widget.NewSelect(nil, nil)
	b.status = widget.NewLabel("")
	b.status.Wrapping = fyne.TextWrapWord
	u.onSession(func(*session) { b.update() })
	u.settings.onChange(func(bool) { b.update() })
	b.update()
	row := container.NewHBox(b.get, b.units, b.send, b.defaults, b.find)
	return container.NewVBox(row, b.status)
}

// update shows the session's units and the state of the model.
func (b *settingsBar) update() {
	s, m := b.u.sess, b.u.settings
	b.addrs = nil
	if s != nil {
		b.addrs = s.devices
	}
	if len(b.addrs) > 1 {
		opts := make([]string, len(b.addrs))
		for i, a := range b.addrs {
			opts[i] = tr("Unit at address {{.Address}}", args{"Address": a})
		}
		if !slices.Equal(opts, b.units.Options) {
			b.units.SetOptions(opts)
			b.units.SetSelectedIndex(slices.Index(b.addrs, s.target))
		}
		b.units.Show()
	} else {
		b.units.Hide()
	}
	enable := func(w fyne.Disableable, on bool) {
		if on {
			w.Enable()
		} else {
			w.Disable()
		}
	}
	enable(b.get, s != nil)
	enable(b.send, s != nil && m.dev != nil)
	enable(b.defaults, m.dev != nil)
	enable(b.find, m.dev != nil)
	b.status.SetText(modelStatus(m))
}

// modelStatus describes the loaded model and its unsent changes.
func modelStatus(m *settingsModel) string {
	d := m.dev
	if d == nil {
		return tr("No settings loaded. Get the settings from a device, or open a settings file.")
	}
	fw := strconv.FormatUint(uint64(d.Firmware), 10)
	var text string
	if m.file != nil {
		text = tr("Settings file {{.File}}, unit {{.Unit}}, firmware {{.Firmware}}.",
			args{"File": m.file.name, "Unit": vebus.UnitLabel(m.file.unit), "Firmware": fw})
	} else {
		text = tr("Settings of the device at address {{.Address}}, firmware {{.Firmware}}.",
			args{"Address": d.Address, "Firmware": fw})
	}
	var changed, reset, password int
	for _, e := range d.Entries() {
		if e.FlagWord || !slices.Contains(settingsTabSet, e.Tab) && e.Tab != vebus.TabGrid || !d.Dirty(e.ID()) {
			continue
		}
		changed++
		if d.ResetRequired(e.ID()) {
			reset++
		}
		if a, ok := d.Access(e.ID()); ok && a.PasswordRequired() {
			password++
		}
	}
	if changed > 0 {
		text += " " + trn("{{.Count}} setting changed and not sent or saved yet.", "{{.Count}} settings changed and not sent or saved yet.", changed)
	}
	if reset > 0 {
		text += " " + trn("{{.Count}} of the changes needs a device reset to take effect.", "{{.Count}} of the changes need a device reset to take effect.", reset)
	}
	if password > 0 {
		text += " " + trn("{{.Count}} of the changes needs a password.", "{{.Count}} of the changes need a password.", password)
	}
	if n := len(validationErrors(d)); n > 0 {
		text += " " + trn("{{.Count}} value is not valid; correct it before sending.", "{{.Count}} values are not valid; correct them before sending.", n)
	}
	if n := len(changedGridWords(d)); n > 0 {
		text += " " + trn("{{.Count}} grid parameter word was changed; it cannot be sent or saved yet (see the Grid tab).",
			"{{.Count}} grid parameter words were changed; they cannot be sent or saved yet (see the Grid tab).", n)
	}
	return text
}

// validationErrors returns the issues that block sending d.
func validationErrors(d *vebus.Device) []vebus.Issue {
	var ve *vebus.ValidationError
	if errors.As(d.CheckWrite(false), &ve) {
		return ve.Issues
	}
	return nil
}

// selectedUnit returns the device address chosen for Get.
func (b *settingsBar) selectedUnit() byte {
	if i := b.units.SelectedIndex(); len(b.addrs) > 1 && i >= 0 {
		return b.addrs[i]
	}
	return b.u.sess.target
}

// getSettings reads the settings of the chosen unit, after asking whether
// unsent changes may be discarded.
func (b *settingsBar) getSettings() {
	u := b.u
	s := u.sess
	if s == nil {
		return
	}
	addr := b.selectedUnit()
	read := func() {
		runWithProgress(u, tr("Reading the settings"), func(ctx context.Context, progress func(done, total int)) error {
			d, rep, err := readSettings(ctx, s, addr, deviceSettingIDs(), progress)
			if err != nil {
				return err
			}
			fyne.Do(func() {
				u.settings.load(d, nil)
				if len(rep.Failed) > 0 {
					showMessage(u, tr("Get settings"), trn("{{.Count}} setting could not be read and is not shown.",
						"{{.Count}} settings could not be read and are not shown.", len(rep.Failed)))
				}
			})
			return nil
		})
	}
	if d := u.settings.dev; d != nil && len(d.ChangedIDs()) > 0 {
		confirm(u, tr("Get settings"), tr("Reading the settings discards the changes you have not sent or saved. Continue?"), tr("Get settings"), read)
		return
	}
	read()
}

// confirm asks a yes/no question and calls yes on yes.
func confirm(u *ui, title, msg, yesText string, yes func()) {
	d := dialog.NewConfirm(title, msg, func(ok bool) {
		if ok {
			yes()
		}
	}, u.win)
	d.SetConfirmText(yesText)
	d.SetDismissText(tr("Cancel"))
	d.Show()
}

// readSettings reads, through s, the settings ids of the device at addr
// (nil: every setting in the catalogue), without RAM variables. A setting
// that cannot be read is listed in the report.
func readSettings(ctx context.Context, s *session, addr byte, ids []uint16, progress func(done, total int)) (*vebus.Device, *vebus.ReadReport, error) {
	var d *vebus.Device
	var rep *vebus.ReadReport
	err := s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		var err error
		d, rep, err = vebus.ReadAll(ctx, c, addr, nil, vebus.ReadOptions{
			Settings: ids, RAM: []uint16{}, ContinueOnError: true, Progress: progress,
		})
		return err
	})
	if err != nil {
		return nil, nil, s.deviceFailure(ctx, err)
	}
	return d, rep, nil
}

// writeSettings writes the changed settings of d (all with all, except the
// grid words and an unchanged grid code: writeAllButGrid) to the device at
// d.Address through s. It writes nothing when grid parameters were changed
// (gridWriteCheck). d must not be touched by others until it returns.
func writeSettings(ctx context.Context, s *session, d *vebus.Device, all bool, progress func(done, total int)) (*vebus.WriteReport, error) {
	if err := gridWriteCheck(d); err != nil {
		return nil, err
	}
	var rep *vebus.WriteReport
	var werr error
	err := s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		if all {
			rep, werr = writeAllButGrid(ctx, c, d, progress)
		} else {
			rep, werr = vebus.Write(ctx, c, d, vebus.WriteOptions{Progress: progress})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, werr
}

// sendSettings asks what to send and where, then sends: a model read from
// a device goes back to that device; a file's unit goes to the chosen
// device after checking that the file fits it.
func sendSettings(u *ui) {
	m, s := u.settings, u.sess
	d := m.dev
	if d == nil || s == nil {
		showMessage(u, tr("Send settings"), tr("Connect to a device and load settings first."))
		return
	}
	if issues := validationErrors(d); len(issues) > 0 {
		showIssues(u, issues)
		return
	}
	if err := gridWriteCheck(d); err != nil {
		showMessage(u, tr("Send settings"), err.Error())
		return
	}
	changedOnly, all := tr("Changed settings only"), tr("All settings")
	what := widget.NewRadioGroup([]string{changedOnly, all}, nil)
	what.Required = true
	what.SetSelected(changedOnly)
	content := container.NewVBox(what)
	var target *widget.Select
	if m.file != nil {
		opts := make([]string, len(s.devices))
		for i, a := range s.devices {
			opts[i] = tr("Unit at address {{.Address}}", args{"Address": a})
		}
		target = widget.NewSelect(opts, nil)
		target.SetSelectedIndex(slices.Index(s.devices, s.target))
		content.Add(widget.NewLabel(tr("Send the file's settings to:")))
		content.Add(target)
	} else {
		content.Add(widget.NewLabel(tr("The settings are sent to the device at address {{.Address}}, which they were read from.", args{"Address": d.Address})))
	}
	dlg := dialog.NewCustomConfirm(tr("Send settings"), tr("Send"), tr("Cancel"), content, func(ok bool) {
		if !ok || u.sess != s || m.dev != d {
			return
		}
		sendAll := what.Selected == all
		if m.file == nil {
			sendToDevice(u, s, d, sendAll)
			return
		}
		if i := target.SelectedIndex(); i >= 0 {
			sendFile(u, s, d, m.file, s.devices[i], sendAll)
		}
	}, u.win)
	dlg.Show()
}

// sendToDevice writes the model d to the device it was read from.
func sendToDevice(u *ui, s *session, d *vebus.Device, all bool) {
	if !all && len(d.ChangedIDs()) == 0 {
		showMessage(u, tr("Send settings"), tr("No setting was changed, so there is nothing to send."))
		return
	}
	runWithProgress(u, tr("Sending the settings"), func(ctx context.Context, progress func(done, total int)) error {
		rep, err := writeSettings(ctx, s, d, all, progress)
		if rep == nil {
			return err
		}
		fyne.Do(func() {
			u.settings.changed()
			showWriteReport(u, d, rep, err)
		})
		return nil
	})
}

// sendFile writes the values of fileDev, the model of a file's unit, to the
// device at addr: it reads the device, refuses unless the file fits it
// (vebus.MatchRVSC), shows the differences, and on confirmation writes
// them. Grid-code settings are neither read nor written.
func sendFile(u *ui, s *session, fileDev *vebus.Device, file *settingsFile, addr byte, all bool) {
	var ids []uint16
	for id := range file.unit.NumSettings() {
		if !vebus.GridCodeSetting(uint16(id)) {
			ids = append(ids, uint16(id))
		}
	}
	runWithProgress(u, tr("Reading the target device"), func(ctx context.Context, progress func(done, total int)) error {
		t, _, err := readSettings(ctx, s, addr, ids, progress)
		if err != nil {
			return err
		}
		match := vebus.MatchRVSC(file.info, file.unit, t)
		// The user chose one unit of the file and one device, so the
		// unit count need not match (vebus.Match.CheckUnitCount).
		match.CheckUnitCount(len(file.units), len(s.devices), true)
		fyne.Do(func() { confirmFileSend(u, s, fileDev, t, match, all) })
		return nil
	})
}

// confirmFileSend shows the differences between the file and the device
// model t and, if the user agrees, writes them.
func confirmFileSend(u *ui, s *session, fileDev, t *vebus.Device, match *vebus.Match, all bool) {
	if !match.OK() {
		showMessage(u, tr("Send settings"), tr("The file does not fit this device, so nothing was sent:\n{{.Report}}", args{"Report": match.String()}))
		return
	}
	diffs := vebus.DiffDevices(fileDev, t)
	if err := copyFileValues(fileDev, t); err != nil {
		showError(u, err)
		return
	}
	if issues := validationErrors(t); len(issues) > 0 {
		showIssues(u, issues)
		return
	}
	if len(diffs) == 0 && !all {
		showMessage(u, tr("Send settings"), tr("The device already holds the file's settings; there is nothing to send."))
		return
	}
	var lines []string
	for _, df := range diffs {
		lines = append(lines, tr("{{.Name}}: device {{.Device}}, file {{.File}}", args{
			"Name": entryLabel(fileDev, df.ID), "Device": sideText(t, df.ID, df.InB), "File": sideText(fileDev, df.ID, df.InA),
		}))
	}
	msg := trn("{{.Count}} setting differs between the file and the device:", "{{.Count}} settings differ between the file and the device:", len(diffs))
	content := container.NewBorder(widget.NewLabel(msg), nil, nil, nil, scrollText(strings.Join(lines, "\n")))
	dlg := dialog.NewCustomConfirm(tr("Send settings"), tr("Send"), tr("Cancel"), content, func(ok bool) {
		if !ok || u.sess != s {
			return
		}
		runWithProgress(u, tr("Sending the settings"), func(ctx context.Context, progress func(done, total int)) error {
			rep, err := writeSettings(ctx, s, t, all, progress)
			if rep == nil {
				return err
			}
			fyne.Do(func() { showWriteReport(u, t, rep, err) })
			return nil
		})
	}, u.win)
	dlg.Resize(fyne.NewSize(640, 420))
	dlg.Show()
}

// copyFileValues sets in t, a model read from a device, the current values
// of fileDev that differ, except settings fileDev holds as not applicable.
func copyFileValues(fileDev, t *vebus.Device) error {
	for _, id := range t.SettingIDs() {
		sid := vebus.SettingID(id)
		want, err := fileDev.Raw(sid)
		if err != nil || fileDev.NotApplicable(sid) {
			continue
		}
		if cur, _ := t.Raw(sid); cur == want {
			continue
		}
		if err := t.SetRaw(sid, want); err != nil {
			return &uiError{err: err, msg: tr("The file's value of {{.Name}} cannot be set on this device: {{.Err}}",
				args{"Name": entryLabel(fileDev, sid), "Err": err})}
		}
	}
	return nil
}

// sideText is the value of id in d for a comparison, or a note that d
// does not have it.
func sideText(d *vebus.Device, id vebus.ID, in bool) string {
	if !in {
		return tr("(not present)")
	}
	return entryValueText(d, id)
}

// entryLabel returns the label of id as the tabs show it, with its ID.
func entryLabel(d *vebus.Device, id vebus.ID) string {
	name := settingLabels()[id]
	if name == "" {
		name = d.Name(id)
	}
	return tr("{{.Name}} ({{.ID}})", args{"Name": name, "ID": idText(id)})
}

// idText names a setting or flag number for the user.
func idText(id vebus.ID) string {
	n := strconv.Itoa(int(id.Num))
	if id.Kind == vebus.Flag {
		return tr("flag {{.Number}}", args{"Number": n})
	}
	return tr("setting {{.Number}}", args{"Number": n})
}

// entryValueText formats the current value of id as the tabs show it.
func entryValueText(d *vebus.Device, id vebus.ID) string {
	e, ok := d.Catalogue().Entry(id)
	if !ok {
		e = vebus.Entry{Kind: id.Kind, Num: id.Num}
	}
	raw, err := d.Raw(id)
	switch {
	case err != nil:
		return tr("unknown")
	case id.Kind == vebus.Flag:
		on := raw == 1
		if c, ok := flagChoices()[id.Num]; ok {
			return c[boolIndex(on)]
		}
		if on != invertedFlags[id.Num] {
			return tr("on")
		}
		return tr("off")
	case e.FlagWord:
		return "0x" + strings.ToUpper(strconv.FormatUint(uint64(raw), 16))
	case len(e.Choices) > 0:
		values, labels := choiceOptions(d, e)
		return labels[slices.Index(values, raw)]
	}
	return valueText(d, e)
}

// scrollText returns read-only text in a scroll area.
func scrollText(text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	s := container.NewVScroll(l)
	s.SetMinSize(fyne.NewSize(560, 240))
	return s
}

// showIssues shows the validation errors that block sending or saving.
func showIssues(u *ui, issues []vebus.Issue) {
	lines := make([]string, len(issues))
	for i, is := range issues {
		lines[i] = is.Message
	}
	msg := trn("{{.Count}} value is not valid. Correct it, then try again:", "{{.Count}} values are not valid. Correct them, then try again:", len(issues))
	d := dialog.NewCustom(tr("Invalid settings"), tr("OK"),
		container.NewBorder(widget.NewLabel(msg), nil, nil, nil, scrollText(strings.Join(lines, "\n"))), u.win)
	d.Show()
}

// showWriteReport shows the outcome of each setting of a send. d is the
// model that was written.
func showWriteReport(u *ui, d *vebus.Device, rep *vebus.WriteReport, err error) {
	var lines []string
	failed, password := 0, false
	for _, r := range rep.Results {
		var status string
		switch r.Status {
		case vebus.StatusVerified:
			status = tr("written and verified")
		case vebus.StatusUnchanged:
			status = tr("already held this value")
		case vebus.StatusSkipped:
			status = tr("not sent")
		case vebus.StatusPlanned:
			status = tr("planned")
		case vebus.StatusFailed:
			failed++
			status = tr("failed: {{.Err}}", args{"Err": r.Err})
			if errors.Is(r.Err, mk2.ErrAccessLevel) {
				password = true
				status = tr("refused: the device requires a password for this setting")
			}
		}
		if r.ResetRequired && r.Status == vebus.StatusVerified {
			status = tr("{{.Status}}; takes effect after a device reset", args{"Status": status})
		}
		lines = append(lines, tr("{{.Name}}: {{.Status}}", args{"Name": entryLabel(d, r.ID), "Status": status}))
	}
	var summary string
	switch {
	case len(rep.Results) == 0:
		summary = tr("Nothing needed to be sent.")
	case failed == 0 && err == nil:
		summary = trn("{{.Count}} setting sent.", "{{.Count}} settings sent.", len(rep.Results))
	case err != nil && failed == 0:
		summary = tr("Sending stopped: {{.Err}}", args{"Err": err})
	default:
		summary = trn("{{.Count}} setting could not be sent.", "{{.Count}} settings could not be sent.", failed)
	}
	if password {
		summary += " " + tr("Some settings need a password, which this program cannot enter.")
	}
	if n := len(rep.ResetRequired()); n > 0 {
		summary += " " + trn("{{.Count}} change takes effect only after the device is reset.", "{{.Count}} changes take effect only after the device is reset.", n)
	}
	dlg := dialog.NewCustom(tr("Send report"), tr("OK"),
		container.NewBorder(widget.NewLabel(summary), nil, nil, nil, scrollText(strings.Join(lines, "\n"))), u.win)
	dlg.Show()
}

// setAllToDefault sets the settings of the settings tabs to the device
// defaults, in the model only.
func setAllToDefault(u *ui) {
	d := u.settings.dev
	if d == nil {
		return
	}
	confirm(u, tr("Set all to default"), tr("Set every setting on the General, Inverter, Charger, Virtual switch and Advanced tabs "+
		"to the device's default? Nothing is sent until you send the settings."), tr("Set all to default"), func() {
		if u.settings.dev != d {
			return
		}
		if errs := applyDefaults(d); len(errs) > 0 {
			showError(u, errors.Join(errs...))
		}
		u.settings.changed()
	})
}

// applyDefaults sets the entries of the settings tabs to their device
// defaults and returns the ones that could not be set.
func applyDefaults(d *vebus.Device) []error {
	var errs []error
	for _, e := range d.Entries() {
		if !slices.Contains(settingsTabSet, e.Tab) || e.FlagWord {
			continue
		}
		def, err := d.Default(e.ID())
		if err == nil {
			err = d.SetRaw(e.ID(), def)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// findEntries returns the entries of d on a tab that match query: a
// setting or flag number, or a part of the label or catalogue name.
func findEntries(d *vebus.Device, query string) []vebus.Entry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	num, numErr := strconv.Atoi(query)
	labels := settingLabels()
	var out []vebus.Entry
	for _, e := range d.Entries() {
		if e.Tab == vebus.TabNone || e.Tab == vebus.TabMonitor || e.FlagWord {
			continue
		}
		if numErr == nil && int(e.Num) == num ||
			strings.Contains(strings.ToLower(labels[e.ID()]), query) || strings.Contains(strings.ToLower(e.Name), query) {
			out = append(out, e)
		}
	}
	return out
}

// findSetting asks for a setting number or name and shows the chosen
// setting on its tab.
func findSetting(u *ui) {
	d := u.settings.dev
	if d == nil {
		return
	}
	var found []vebus.Entry
	var dlg dialog.Dialog
	list := widget.NewList(
		func() int { return len(found) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(tr("{{.Name}}: {{.Value}}", args{"Name": entryLabel(d, found[i].ID()), "Value": entryValueText(d, found[i].ID())}))
		})
	list.OnSelected = func(i widget.ListItemID) {
		e := found[i]
		dlg.Hide()
		for _, nav := range u.settings.navigators {
			if nav(e) {
				return
			}
		}
	}
	query := widget.NewEntry()
	query.SetPlaceHolder(tr("Setting number or part of its name"))
	query.OnChanged = func(s string) {
		found = findEntries(d, s)
		list.UnselectAll()
		list.Refresh()
	}
	content := container.NewBorder(query, nil, nil, nil, list)
	dlg = dialog.NewCustom(tr("Find setting"), tr("Close"), content, u.win)
	dlg.Resize(fyne.NewSize(560, 420))
	dlg.Show()
	u.win.Canvas().Focus(query)
}

// rvscFilter limits the file dialogs to settings files.
var rvscFilter = storage.NewExtensionFileFilter([]string{".rvsc", ".rvms"})

// openSettingsFile asks for a .rvsc/.rvms file and loads it for offline
// editing; for a file with several units, it asks which one.
func openSettingsFile(u *ui) {
	if d := u.settings.dev; d != nil && len(d.ChangedIDs()) > 0 {
		confirm(u, tr("Open settings file"), tr("Opening a file discards the changes you have not sent or saved. Continue?"),
			tr("Open settings file"), func() { chooseSettingsFile(u) })
		return
	}
	chooseSettingsFile(u)
}

func chooseSettingsFile(u *ui) {
	dlg := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil {
			showError(u, err)
			return
		}
		if r == nil {
			return
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			showError(u, &uiError{err: err, msg: tr("Cannot read {{.File}}: {{.Err}}", args{"File": r.URI().Name(), "Err": err})})
			return
		}
		sf, err := parseSettingsFile(r.URI().Name(), data)
		if err != nil {
			showError(u, err)
			return
		}
		if len(sf.units) == 1 {
			loadSettingsFile(u, sf)
			return
		}
		opts := make([]string, len(sf.units))
		for i, x := range sf.units {
			opts[i] = vebus.UnitLabel(x)
		}
		sel := widget.NewSelect(opts, nil)
		sel.SetSelectedIndex(0)
		pick := dialog.NewCustomConfirm(tr("Choose a unit"), tr("Open"), tr("Cancel"),
			container.NewVBox(widget.NewLabel(tr("The file holds several units. Which one do you want to edit?")), sel), func(ok bool) {
				if ok {
					sf.unit = sf.units[sel.SelectedIndex()]
					loadSettingsFile(u, sf)
				}
			}, u.win)
		pick.Show()
	}, u.win)
	dlg.SetFilter(rvscFilter)
	dlg.Show()
}

// loadSettingsFile makes the selected unit of sf the model.
func loadSettingsFile(u *ui, sf *settingsFile) {
	d, err := sf.device()
	if err != nil {
		showError(u, err)
		return
	}
	u.settings.load(d, sf)
}

// saveSettingsFile saves the edited unit into a copy of the open file.
// New files cannot be made yet, so a model read from a device cannot be
// saved.
func saveSettingsFile(u *ui) {
	m := u.settings
	if m.file == nil {
		showMessage(u, tr("Save settings file"), tr("Only an opened settings file can be saved. "+
			"Creating a new file from a device is not supported yet."))
		return
	}
	if issues := validationErrors(m.dev); len(issues) > 0 {
		showIssues(u, issues)
		return
	}
	d, sf := m.dev, m.file
	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil {
			showError(u, err)
			return
		}
		if w == nil || m.dev != d {
			return
		}
		data, err := sf.save(d)
		if err == nil {
			_, err = w.Write(data)
		}
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			showError(u, err)
			return
		}
		sf.name = w.URI().Name()
		loadSettingsFile(u, sf)
	}, u.win)
	dlg.SetFilter(rvscFilter)
	dlg.SetFileName(sf.name)
	dlg.Show()
}
