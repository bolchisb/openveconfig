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
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
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
	if m.file == nil && (c.gridWords > 0 || c.gridCode) {
		text += " " + tr("The grid code or grid parameters were changed; these changes cannot be sent straight to a device (see the Grid page).")
	}
	return text
}

// changeCounts counts the unsent changes of a model.
type changeCounts struct {
	changed   int  // changed entries on the settings pages and the grid page
	reset     int  // of which need a device reset
	password  int  // of which need a password
	invalid   int  // values that block sending (validationErrors)
	gridWords int  // changed grid parameter words (changedGridWords)
	gridCode  bool // the grid code was changed
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
	c.gridCode = d.Dirty(vebus.SettingID(gridCodeSetting))
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

// changeSummary sums up the changes counted in c in one line.
func changeSummary(c changeCounts) string {
	parts := []string{trn("{{.Count}} change", "{{.Count}} changes", c.total())}
	if c.reset > 0 {
		parts = append(parts, trn("{{.Count}} needs a device reset", "{{.Count}} need a device reset", c.reset))
	}
	if c.password > 0 {
		parts = append(parts, trn("{{.Count}} needs a password", "{{.Count}} need a password", c.password))
	}
	if c.invalid > 0 {
		parts = append(parts, trn("{{.Count}} invalid value", "{{.Count}} invalid values", c.invalid))
	}
	return strings.Join(parts, " · ")
}

// reviewChanges shows the unsent changes of the loaded model in a table,
// each with a button that reverts it, and offers the action of the changes
// bar: send them to the device or save the file.
func reviewChanges(u *ui) {
	m := u.settings
	d := m.dev
	if d == nil {
		return
	}
	before := lastRead(d)
	cols := tableColumns{old: tr("On device"), new: tr("New value"), notes: tr("Notes")}
	intro := tr("Changes to the settings read from the device at address {{.Address}}. Nothing has been sent yet.", args{"Address": d.Address})
	primary := widget.NewButtonWithIcon("", theme.UploadIcon(), nil)
	if m.file != nil {
		cols.old = tr("In file")
		intro = tr("Changes to {{.File}}. Nothing has been saved yet.", args{"File": m.file.name})
		primary.SetIcon(theme.DocumentSaveIcon())
	}
	primary.Importance = widget.HighImportance
	table := container.NewStack()
	summary := widget.NewLabel("")
	summary.TextStyle.Bold = true
	reason := mutedText("")
	var do func()
	var refresh func()
	revertAll := widget.NewButtonWithIcon(tr("Revert all"), theme.ContentUndoIcon(), func() {
		confirmDiscard(u, d, func() { refresh() })
	})
	revert := func(id vebus.ID) func() {
		return func() {
			raw, err := before.Raw(id)
			if err == nil {
				err = d.SetRaw(id, raw)
			}
			if err != nil {
				showError(u, err)
			}
			m.changed()
			refresh()
		}
	}
	refresh = func() {
		rows := changeRows(u, d, before)
		for i := range rows {
			if d.Dirty(rows[i].id) {
				rows[i].revert = revert(rows[i].id)
			}
		}
		table.Objects = []fyne.CanvasObject{newChangeTable(cols, rows, tr("There are no unsent changes."))}
		table.Refresh()
		c := countChanges(d)
		summary.SetText(changeSummary(c))
		label, act, why := primaryAction(u, c)
		primary.SetText(label)
		do = act
		if why == "" && c.total() > 0 {
			primary.Enable()
		} else {
			primary.Disable()
		}
		setMutedText(reason, why)
		if len(d.ChangedIDs()) > 0 {
			revertAll.Enable()
		} else {
			revertAll.Disable()
		}
	}
	refresh()
	footer := container.NewBorder(nil, nil, nil, container.NewCenter(revertAll), container.NewVBox(summary, reason))
	content := container.NewBorder(mutedText(intro), footer, nil, nil, table)
	showTableDialog(u, tr("Review changes"), content, tableDialogSize, tr("Close"), primary, func() { do() })
}

// discardChanges sets the loaded model back to the values last read or
// loaded, after asking.
func discardChanges(u *ui) {
	if d := u.settings.dev; d != nil {
		confirmDiscard(u, d, nil)
	}
}

// confirmDiscard asks whether the changes of d, the loaded model, may be
// discarded, discards them on yes and then calls after, if not nil.
func confirmDiscard(u *ui, d *vebus.Device, after func()) {
	c := newConfirm(u, tr("Discard changes"), tr("Discard every change that was not sent or saved, and go back to the values "+
		"last read from the device or loaded from the file?"), tr("Discard changes"), func() {
		if u.settings.dev == d {
			d.Revert()
			u.settings.changed()
		}
		if after != nil {
			after()
		}
	})
	c.SetConfirmImportance(widget.DangerImportance)
	showWithKeys(u, c, c.Confirm)
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
		n := countChanges(d).total()
		if n == 0 {
			n = len(d.ChangedIDs())
		}
		c := newConfirm(u, tr("Get settings"), trn("You have {{.Count}} change that was not sent or saved. Reading the settings from the device discards it. Continue?",
			"You have {{.Count}} changes that were not sent or saved. Reading the settings from the device discards them. Continue?", n),
			tr("Discard and read"), read)
		c.SetConfirmImportance(widget.DangerImportance)
		showWithKeys(u, c, c.Confirm)
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
// grid code and the grid words: writeAllButGrid) to the device at
// d.Address through s. It writes nothing when the grid code or grid
// parameters were changed (gridWriteCheck). d must not be touched by
// others until it returns.
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
	if err := gridWriteCheck(d); err != nil {
		showMessage(u, tr("Send settings"), err.Error())
		return
	}
	issues := validationErrors(d)
	changedOnly, all := tr("Changed settings only"), tr("All settings")
	explain := mutedText("")
	what := widget.NewRadioGroup([]string{changedOnly, all}, nil)
	what.Required = true
	what.Horizontal = true
	options := container.NewVBox(what, explain)
	var target *widget.Select
	if m.file != nil {
		if len(s.devices) == 1 {
			options.Add(widget.NewLabel(tr("The file's settings are sent to the unit at address {{.Address}}.", args{"Address": s.devices[0]})))
		} else {
			opts := make([]string, len(s.devices))
			for i, a := range s.devices {
				opts[i] = tr("Unit at address {{.Address}}", args{"Address": a})
			}
			target = widget.NewSelect(opts, nil)
			target.SetSelectedIndex(slices.Index(s.devices, s.target))
			options.Add(container.NewBorder(nil, nil, widget.NewLabel(tr("Send the file's settings to:")), nil, target))
		}
		options.Add(mutedText(tr("The device is read first. You see how its settings differ from the file before anything is sent.")))
	} else {
		options.Add(widget.NewLabel(tr("The settings are sent to the device at address {{.Address}}, which they were read from.", args{"Address": d.Address})))
	}
	// A model read from a device shows its changes; a file, which is
	// compared with the device later, only its invalid values.
	var rows []changeRow
	if m.file == nil {
		rows = changeRows(u, d, lastRead(d))
	} else {
		rows = markIssues(u, d, nil, issues)
	}
	summary := widget.NewLabel("")
	summary.TextStyle.Bold = true
	reason := mutedText("")
	primary := widget.NewButtonWithIcon(tr("Send"), theme.UploadIcon(), nil)
	primary.Importance = widget.HighImportance
	what.OnChanged = func(sel string) {
		if sel == all {
			explain.Segments[0].(*widget.TextSegment).Text = tr("Every setting is written to the device again, also the ones you did not change. " +
				"The grid code and the grid parameters are left as the device has them.")
		} else if m.file != nil {
			explain.Segments[0].(*widget.TextSegment).Text = tr("Only the settings that differ between the file and the device are written.")
		} else {
			explain.Segments[0].(*widget.TextSegment).Text = tr("Only the settings you changed are written to the device.")
		}
		explain.Refresh()
		why := ""
		switch {
		case len(issues) > 0:
			why = trn("{{.Count}} value is not valid; correct it before sending.", "{{.Count}} values are not valid; correct them before sending.", len(issues))
		case m.file == nil && sel != all && len(d.ChangedIDs()) == 0:
			why = tr("No setting was changed, so there is nothing to send.")
		}
		if why == "" {
			primary.Enable()
		} else {
			primary.Disable()
		}
		setMutedText(reason, why)
	}
	what.SetSelected(changedOnly)
	var center fyne.CanvasObject = layout.NewSpacer()
	if m.file == nil || len(rows) > 0 {
		center = newChangeTable(tableColumns{old: tr("On device"), new: tr("New value"), notes: tr("Notes")}, rows, tr("There are no unsent changes."))
		if m.file == nil {
			summary.SetText(changeSummary(countChanges(d)))
		}
	}
	content := container.NewBorder(options, container.NewVBox(summary, reason), nil, nil, center)
	size := tableDialogSize
	if m.file != nil && len(rows) == 0 {
		size = fyne.NewSize(620, 340)
	}
	showTableDialog(u, tr("Send settings"), content, size, tr("Cancel"), primary, func() {
		if u.sess != s || m.dev != d {
			return
		}
		sendAll := what.Selected == all
		switch {
		case m.file == nil:
			sendToDevice(u, s, d, sendAll)
		case target == nil:
			sendFile(u, s, d, m.file, s.devices[0], sendAll)
		case target.SelectedIndex() >= 0:
			sendFile(u, s, d, m.file, s.devices[target.SelectedIndex()], sendAll)
		}
	})
}

// sendToDevice writes the model d to the device it was read from and shows
// the outcome.
func sendToDevice(u *ui, s *session, d *vebus.Device, all bool) {
	if !all && len(d.ChangedIDs()) == 0 {
		showMessage(u, tr("Send settings"), tr("No setting was changed, so there is nothing to send."))
		return
	}
	before := lastRead(d)
	runWithProgress(u, tr("Sending the settings"), func(ctx context.Context, progress func(done, total int)) error {
		rep, err := writeSettings(ctx, s, d, all, progress)
		if rep == nil {
			return err
		}
		fyne.Do(func() {
			u.settings.changed()
			showSendResult(u, d, before, rep, err)
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

// confirmFileSend shows whether the file fits the device model t and how
// they differ and, if the user agrees, writes the file's values.
func confirmFileSend(u *ui, s *session, fileDev, t *vebus.Device, match *vebus.Match, all bool) {
	if !match.OK() {
		list := matchList(match)
		for _, n := range match.NotCompared {
			list.Add(mutedText(tr("Not compared: {{.What}}", args{"What": n})))
		}
		content := container.NewBorder(summaryLine(theme.NewErrorThemedResource(theme.ErrorIcon()),
			tr("The file does not fit this device, so nothing was sent.")), nil, nil, nil, container.NewVScroll(list))
		showTableDialog(u, tr("Send settings"), content, fyne.NewSize(tableDialogSize.Width, 420), tr("Close"), nil, nil)
		return
	}
	diffs := vebus.DiffDevices(fileDev, t)
	// The device's values, before the file's are copied into t.
	rows := make([]changeRow, 0, len(diffs))
	for _, df := range diffs {
		r := newRow(u, fileDev, df.ID, sideText(t, df.ID, df.InB), sideText(fileDev, df.ID, df.InA))
		if t.ResetRequired(df.ID) {
			r.marks = append(r.marks, resetMark())
		}
		if needsPassword(t, df.ID) {
			r.marks = append(r.marks, passwordMark())
		}
		rows = append(rows, r)
	}
	if err := copyFileValues(fileDev, t); err != nil {
		showError(u, err)
		return
	}
	issues := validationErrors(t)
	if len(diffs) == 0 && !all && len(issues) == 0 {
		showMessage(u, tr("Send settings"), tr("The device already holds the file's settings; there is nothing to send."))
		return
	}
	rows = markIssues(u, t, rows, issues)
	head := container.NewVBox(summaryLine(theme.NewSuccessThemedResource(theme.ConfirmIcon()),
		tr("The file fits the device at address {{.Address}}.", args{"Address": t.Address})),
		widget.NewLabel(trn("{{.Count}} setting differs between the file and the device:", "{{.Count}} settings differ between the file and the device:", len(diffs))))
	if all {
		head.Add(mutedText(tr("All settings are written, also the ones that do not differ.")))
	}
	primary := widget.NewButtonWithIcon(tr("Send"), theme.UploadIcon(), nil)
	primary.Importance = widget.HighImportance
	reason := mutedText("")
	setMutedText(reason, "")
	if len(issues) > 0 {
		primary.Disable()
		setMutedText(reason, trn("{{.Count}} value is not valid; correct it before sending.", "{{.Count}} values are not valid; correct them before sending.", len(issues)))
	}
	table := newChangeTable(tableColumns{old: tr("On device"), new: tr("From file"), notes: tr("Notes")}, rows, tr("No setting differs."))
	content := container.NewBorder(head, reason, nil, nil, table)
	showTableDialog(u, tr("Send settings"), content, tableDialogSize, tr("Cancel"), primary, func() {
		if u.sess != s {
			return
		}
		before := lastRead(t)
		runWithProgress(u, tr("Sending the settings"), func(ctx context.Context, progress func(done, total int)) error {
			rep, err := writeSettings(ctx, s, t, all, progress)
			if rep == nil {
				return err
			}
			fyne.Do(func() { showSendResult(u, t, before, rep, err) })
			return nil
		})
	})
}

// matchList lists the comparisons of a file with a device, each marked as
// passed or failed.
func matchList(m *vebus.Match) *fyne.Container {
	names := map[string]string{"firmware": tr("Firmware version"), "settings count": tr("Number of settings"),
		"setting info": tr("Setting limits"), "unit count": tr("Number of units")}
	list := container.NewVBox()
	for _, c := range m.Checks {
		what := names[c.What]
		if what == "" {
			what = c.What
		}
		var icon fyne.Resource = theme.NewSuccessThemedResource(theme.ConfirmIcon())
		colour := theme.ColorNameForeground
		if !c.OK {
			icon, colour = theme.NewErrorThemedResource(theme.ErrorIcon()), theme.ColorNameError
		}
		text := what
		if c.Detail != "" {
			text = tr("{{.Check}}: {{.Detail}}", args{"Check": what, "Detail": c.Detail})
		}
		t := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: colour}})
		t.Wrapping = fyne.TextWrapWord
		list.Add(container.NewBorder(nil, nil, widget.NewIcon(icon), nil, t))
	}
	return list
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
	return tr("{{.Name}} ({{.ID}})", args{"Name": settingName(d, id), "ID": idText(id)})
}

// settingName returns the label of id as the tabs show it.
func settingName(d *vebus.Device, id vebus.ID) string {
	if name := settingLabels()[id]; name != "" {
		return name
	}
	return d.Name(id)
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

// showIssues shows the validation errors of d that block saving it, each
// on the row of its setting.
func showIssues(u *ui, d *vebus.Device, issues []vebus.Issue) {
	rows := markIssues(u, d, nil, issues)
	msg := trn("{{.Count}} value is not valid. Correct it, then try again:", "{{.Count}} values are not valid. Correct them, then try again:", len(issues))
	table := newChangeTable(tableColumns{old: tr("In file"), new: tr("New value"), notes: tr("Notes")}, rows, "")
	showTableDialog(u, tr("Invalid settings"), container.NewBorder(summaryLine(theme.NewErrorThemedResource(theme.ErrorIcon()), msg),
		nil, nil, nil, table), tableDialogSize, tr("Close"), nil, nil)
}

// showSendResult shows the outcome of each setting of a send of d, whose
// values before the send before holds (lastRead), with what to do next.
func showSendResult(u *ui, d, before *vebus.Device, rep *vebus.WriteReport, err error) {
	var sent, refused, failed, skipped int
	for _, r := range rep.Results {
		switch r.Status {
		case vebus.StatusVerified, vebus.StatusUnchanged:
			sent++
		case vebus.StatusFailed:
			if errors.Is(r.Err, mk2.ErrAccessLevel) {
				refused++
			} else {
				failed++
			}
		default:
			skipped++
		}
	}
	icon := fyne.Resource(theme.NewSuccessThemedResource(theme.ConfirmIcon()))
	var summary string
	switch {
	case len(rep.Results) == 0:
		summary = tr("Nothing needed to be sent.")
	case sent == len(rep.Results) && err == nil:
		summary = trn("{{.Count}} setting sent.", "{{.Count}} settings sent.", sent)
	case err != nil && refused+failed == 0:
		icon = theme.NewErrorThemedResource(theme.ErrorIcon())
		summary = tr("Sending stopped: {{.Err}}", args{"Err": err})
	default:
		icon = theme.NewErrorThemedResource(theme.ErrorIcon())
		var parts []string
		if sent > 0 {
			parts = append(parts, trn("{{.Count}} sent", "{{.Count}} sent", sent))
		}
		if refused > 0 {
			parts = append(parts, trn("{{.Count}} refused", "{{.Count}} refused", refused))
		}
		if failed > 0 {
			parts = append(parts, trn("{{.Count}} failed", "{{.Count}} failed", failed))
		}
		if skipped > 0 {
			parts = append(parts, trn("{{.Count}} not sent", "{{.Count}} not sent", skipped))
		}
		summary = strings.Join(parts, " · ")
	}
	head := container.NewVBox(summaryLine(icon, summary))
	if n := len(rep.ResetRequired()); n > 0 {
		head.Add(callout(theme.NewWarningThemedResource(theme.WarningIcon()),
			trn("{{.Count}} change takes effect only after the device is reset.", "{{.Count}} changes take effect only after the device is reset.", n)+" "+
				tr("Until then the device keeps working with the old values. Restart it when its output may be interrupted, "+
					"for example by switching it off and on again.")))
	}
	table := newChangeTable(tableColumns{old: tr("Before"), new: tr("New value"), notes: tr("Result")}, resultRows(u, d, before, rep), tr("Nothing needed to be sent."))
	showTableDialog(u, tr("Send result"), container.NewBorder(head, nil, nil, nil, table), tableDialogSize, tr("Close"), nil, nil)
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
		showIssues(u, m.dev, issues)
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
