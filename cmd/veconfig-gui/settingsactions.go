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

// settingsTabSet are the tabs whose settings "Reset all to defaults"
// changes: the pages of settingstabs.go. System configuration and the grid
// tab are left alone.
var settingsTabSet = []vebus.Tab{vebus.TabGeneral, vebus.TabInverter, vebus.TabCharger, vebus.TabVirtualSwitch, vebus.TabAdvanced}

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
	c := countChanges(d)
	if n := c.changed; n > 0 {
		text += " " + trn("{{.Count}} setting changed and not sent or saved yet.", "{{.Count}} settings changed and not sent or saved yet.", n)
	}
	if n := c.reset; n > 0 {
		text += " " + trn("{{.Count}} of the changes needs a device reset to take effect.", "{{.Count}} of the changes need a device reset to take effect.", n)
	}
	if n := c.password; n > 0 {
		text += " " + trn("{{.Count}} of the changes needs a password.", "{{.Count}} of the changes need a password.", n)
	}
	if n := c.invalid; n > 0 {
		text += " " + trn("{{.Count}} value is not valid; correct it before sending.", "{{.Count}} values are not valid; correct them before sending.", n)
	}
	if n := c.gridWords; n > 0 {
		text += " " + trn("{{.Count}} grid parameter word was changed; it cannot be sent or saved yet (see the Grid page).",
			"{{.Count}} grid parameter words were changed; they cannot be sent or saved yet (see the Grid page).", n)
	}
	return text
}

// changeCounts counts the unsent changes of a model.
type changeCounts struct {
	changed   int // changed entries on the settings pages and the grid page
	reset     int // of which need a device reset
	password  int // of which need a password
	invalid   int // values that block sending (validationErrors)
	gridWords int // changed grid parameter words (changedGridWords)
}

// total is the number of unsent changes, grid words included.
func (c changeCounts) total() int { return c.changed + c.gridWords }

// countChanges counts the unsent changes of d.
func countChanges(d *vebus.Device) changeCounts {
	var c changeCounts
	for _, e := range d.Entries() {
		if e.FlagWord || !slices.Contains(settingsTabSet, e.Tab) && e.Tab != vebus.TabGrid || !d.Dirty(e.ID()) {
			continue
		}
		c.changed++
		if d.ResetRequired(e.ID()) {
			c.reset++
		}
		if a, ok := d.Access(e.ID()); ok && a.PasswordRequired() {
			c.password++
		}
	}
	c.invalid = len(validationErrors(d))
	c.gridWords = len(changedGridWords(d))
	return c
}

// tabChanges returns the number of unsent changes of d on tab, the grid
// parameter words included for the grid tab.
func tabChanges(d *vebus.Device, tab vebus.Tab) int {
	n := 0
	for _, e := range d.Entries() {
		if e.Tab == tab && !e.FlagWord && d.Dirty(e.ID()) {
			n++
		}
	}
	if tab == vebus.TabGrid {
		n += len(changedGridWords(d))
	}
	return n
}

// reviewChanges lists the unsent changes of the loaded model with their
// new values.
func reviewChanges(u *ui) {
	d := u.settings.dev
	if d == nil {
		return
	}
	var lines []string
	for _, e := range d.Entries() {
		if e.FlagWord || !slices.Contains(settingsTabSet, e.Tab) && e.Tab != vebus.TabGrid || !d.Dirty(e.ID()) {
			continue
		}
		lines = append(lines, tr("{{.Name}}: {{.Value}}", args{"Name": entryLabel(d, e.ID()), "Value": entryValueText(d, e.ID())}))
	}
	for _, id := range changedGridWords(d) {
		sid := vebus.SettingID(id)
		lines = append(lines, tr("{{.Name}}: {{.Value}}", args{"Name": entryLabel(d, sid), "Value": entryValueText(d, sid)}))
	}
	msg := widget.NewLabel(modelStatus(u.settings))
	msg.Wrapping = fyne.TextWrapWord
	dlg := dialog.NewCustom(tr("Unsaved changes"), tr("Close"),
		container.NewBorder(msg, nil, nil, nil, scrollText(strings.Join(lines, "\n"))), u.win)
	dlg.Resize(fyne.NewSize(640, 420))
	dlg.Show()
}

// discardChanges sets the loaded model back to the values last read or
// loaded, after asking.
func discardChanges(u *ui) {
	d := u.settings.dev
	if d == nil {
		return
	}
	c := newConfirm(u, tr("Discard changes"), tr("Discard every change that was not sent or saved, and go back to the values "+
		"last read from the device or loaded from the file?"), tr("Discard changes"), func() {
		if u.settings.dev == d {
			d.Revert()
			u.settings.changed()
		}
	})
	c.SetConfirmImportance(widget.DangerImportance)
	c.Show()
}

// validationErrors returns the issues that block sending d.
func validationErrors(d *vebus.Device) []vebus.Issue {
	var ve *vebus.ValidationError
	if errors.As(d.CheckWrite(false), &ve) {
		return ve.Issues
	}
	return nil
}

// getSettings reads the settings of the unit chosen on the Overview page
// (ui.unit), after asking whether unsent changes may be discarded.
func getSettings(u *ui) {
	s := u.sess
	if s == nil {
		showMessage(u, tr("Get settings"), tr("Connect to a device first."))
		return
	}
	addr := u.unit
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
	newConfirm(u, title, msg, yesText, yes).Show()
}

// newConfirm returns the dialog of confirm, not yet shown.
func newConfirm(u *ui, title, msg, yesText string, yes func()) *dialog.ConfirmDialog {
	d := dialog.NewConfirm(title, msg, func(ok bool) {
		if ok {
			yes()
		}
	}, u.win)
	d.SetConfirmText(yesText)
	d.SetDismissText(tr("Cancel"))
	return d
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

// setAllToDefault sets the settings of the settings pages to the device
// defaults, in the model only, after asking.
func setAllToDefault(u *ui) {
	d := u.settings.dev
	if d == nil {
		return
	}
	c := newConfirm(u, tr("Reset all to defaults"), tr("Set every setting on the General, Inverter, Charger, Virtual switch and Advanced pages "+
		"to the device's default? Your values on these pages are replaced. Nothing is sent until you send the settings."),
		tr("Reset all to defaults"), func() {
			if u.settings.dev != d {
				return
			}
			if errs := applyDefaults(d); len(errs) > 0 {
				showError(u, errors.Join(errs...))
			}
			u.settings.changed()
		})
	c.SetConfirmImportance(widget.DangerImportance)
	c.Show()
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
// setting on its page.
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
		if r != nil {
			readSettingsFile(u, r)
		}
	}, u.win)
	dlg.SetFilter(rvscFilter)
	dlg.Show()
}

// openRecentFile opens the settings file at uri, as openSettingsFile does
// once a file is chosen.
func openRecentFile(u *ui, uri fyne.URI) {
	open := func() {
		r, err := storage.Reader(uri)
		if err != nil {
			showError(u, &uiError{err: err, msg: tr("Cannot read {{.File}}: {{.Err}}", args{"File": uri.Name(), "Err": err})})
			return
		}
		readSettingsFile(u, r)
	}
	if d := u.settings.dev; d != nil && len(d.ChangedIDs()) > 0 {
		confirm(u, tr("Open settings file"), tr("Opening a file discards the changes you have not sent or saved. Continue?"),
			tr("Open settings file"), open)
		return
	}
	open()
}

// readSettingsFile reads and closes r, a .rvsc/.rvms file, and loads it;
// for a file with several units, it asks which one.
func readSettingsFile(u *ui, r fyne.URIReadCloser) {
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
	addRecent(u, r.URI())
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
		addRecent(u, w.URI())
		loadSettingsFile(u, sf)
	}, u.win)
	dlg.SetFilter(rvscFilter)
	dlg.SetFileName(sf.name)
	dlg.Show()
}
