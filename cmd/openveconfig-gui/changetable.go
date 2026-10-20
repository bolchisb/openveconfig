package main

// The table that the review, send and file dialogs show: each changed or
// written setting with its old and new value and what happens to it,
// grouped by the page it is on; and the dialog these tables are shown in.

import (
	"context"
	"errors"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/gridcode"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// tableDialogSize is the size of the dialogs that show a change table.
var tableDialogSize = fyne.NewSize(780, 580)

// rowState is the outcome of a setting in a send result.
type rowState int

const (
	stateNone      rowState = iota // no outcome: a review or a comparison
	stateVerified                  // written and read back
	stateUnchanged                 // the device already held the value
	stateRefused                   // refused: the device wants a password
	stateFailed                    // the write failed
	stateNotSent                   // not attempted
)

// rowMark is a short marker in the notes column of a row.
type rowMark struct {
	text   string
	colour fyne.ThemeColorName
}

// changeRow is one row of a change table: a setting or flag.
type changeRow struct {
	id       vebus.ID
	page     int    // the index of its page in ui.pages; len(ui.pages) for none
	group    string // the title of that page
	name     string // its label
	ref      string // its number, such as "setting 2"
	old, new string // the values, formatted as the pages show them
	state    rowState
	marks    []rowMark
	// detail is a sentence shown under the row, such as why a value is
	// invalid or a write failed; "" for none.
	detail      string
	detailError bool   // detail is an error, shown in the error colour
	revert      func() // sets the setting back; nil for no revert button
}

// tableColumns are the headings of the old value, the new value and the
// notes column.
type tableColumns struct{ old, new, notes string }

// newChangeTable returns rows in a table under a heading row, grouped by
// page in sidebar order, in a scroll area. empty is shown when there are no
// rows.
func newChangeTable(cols tableColumns, rows []changeRow, empty string) fyne.CanvasObject {
	if len(rows) == 0 {
		return container.NewCenter(mutedText(empty))
	}
	rows = slices.Clone(rows)
	slices.SortStableFunc(rows, func(a, b changeRow) int { return a.page - b.page })
	trail := float32(0)
	for _, r := range rows {
		if r.revert != nil {
			trail = revertButton(nil).MinSize().Width
			break
		}
	}
	lay := &tableLayout{trail: trail}
	head := container.New(lay, tableHeading(tr("Setting")), tableHeading(cols.old), tableHeading(cols.new), tableHeading(cols.notes))
	var items []fyne.CanvasObject
	for i, r := range rows {
		if i == 0 || r.group != rows[i-1].group {
			title := widget.NewRichText(&widget.TextSegment{Text: r.group,
				Style: widget.RichTextStyle{ColorName: theme.ColorNamePrimary, TextStyle: fyne.TextStyle{Bold: true}}})
			title.Truncation = fyne.TextTruncateEllipsis
			gap := float32(0)
			if i > 0 {
				gap = 8
			}
			items = append(items, container.New(layout.NewCustomPaddedLayout(gap, 0, 0, 0), title))
		} else {
			items = append(items, widget.NewSeparator())
		}
		items = append(items, tableRow(lay, r))
	}
	band := container.NewStack(newColourRect(colorNamePage, fyne.Size{}, theme.SizeNameInputRadius),
		container.New(layout.NewCustomPaddedLayout(2, 2, 8, 8), head))
	body := container.NewVScroll(container.New(layout.NewCustomPaddedLayout(0, 0, 8, 8), container.NewVBox(items...)))
	return container.NewBorder(band, nil, nil, nil, body)
}

// tableHeading is the text of a column heading.
func tableHeading(text string) fyne.CanvasObject {
	t := widget.NewRichText(&widget.TextSegment{Text: text,
		Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder, TextStyle: fyne.TextStyle{Bold: true}}})
	t.Truncation = fyne.TextTruncateEllipsis
	return t
}

// cellText returns one line of text in colour, cut off with an ellipsis
// when it does not fit.
func cellText(text string, colour fyne.ThemeColorName, bold bool) *widget.RichText {
	t := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: colour, TextStyle: fyne.TextStyle{Bold: bold}}})
	t.Truncation = fyne.TextTruncateEllipsis
	return t
}

// revertButton returns the button that sets a row's setting back.
func revertButton(f func()) *widget.Button {
	b := widget.NewButtonWithIcon("", theme.ContentUndoIcon(), f)
	b.Importance = widget.LowImportance
	return b
}

// tableRow returns the cells of r laid out by lay, with its detail under
// them.
func tableRow(lay *tableLayout, r changeRow) fyne.CanvasObject {
	name := widget.NewRichText(&widget.TextSegment{Text: r.name, Style: widget.RichTextStyleParagraph},
		&widget.TextSegment{Text: r.ref, Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder, SizeName: theme.SizeNameCaptionText}})
	name.Truncation = fyne.TextTruncateEllipsis
	notes := container.NewHBox()
	if icon, text, colour := stateView(r.state); icon != nil {
		notes.Add(widget.NewIcon(icon))
		notes.Add(widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: colour, TextStyle: fyne.TextStyle{Bold: true}}}))
	}
	for _, m := range r.marks {
		notes.Add(newPill(m.text, m.colour, false))
	}
	cells := []fyne.CanvasObject{name, cellText(r.old, theme.ColorNamePlaceHolder, false),
		cellText(r.new, theme.ColorNameForeground, r.state == stateNone), notes}
	if r.revert != nil {
		cells = append(cells, revertButton(r.revert))
	}
	row := container.New(lay, cells...)
	if r.detail == "" {
		return row
	}
	colour := theme.ColorNamePlaceHolder
	if r.detailError {
		colour = theme.ColorNameError
	}
	detail := widget.NewRichText(&widget.TextSegment{Text: r.detail, Style: widget.RichTextStyle{ColorName: colour}})
	detail.Wrapping = fyne.TextWrapWord
	return container.NewVBox(row, detail)
}

// stateView returns the icon, the text and the colour of an outcome; a nil
// icon for stateNone.
func stateView(s rowState) (fyne.Resource, string, fyne.ThemeColorName) {
	switch s {
	case stateVerified:
		return theme.NewSuccessThemedResource(theme.ConfirmIcon()), tr("Verified"), theme.ColorNameSuccess
	case stateUnchanged:
		return theme.NewColoredResource(theme.ConfirmIcon(), theme.ColorNamePlaceHolder), tr("Already set"), theme.ColorNamePlaceHolder
	case stateRefused:
		return theme.NewErrorThemedResource(theme.ErrorIcon()), tr("Refused"), theme.ColorNameError
	case stateFailed:
		return theme.NewErrorThemedResource(theme.ErrorIcon()), tr("Failed"), theme.ColorNameError
	case stateNotSent:
		return theme.NewColoredResource(theme.ContentRemoveIcon(), theme.ColorNamePlaceHolder), tr("Not sent"), theme.ColorNamePlaceHolder
	}
	return nil, "", ""
}

// columnShares are the parts of a row's width that the setting, the old
// value, the new value and the notes take.
var columnShares = [4]float32{0.34, 0.17, 0.17, 0.32}

// tableLayout lays out the cells of every row of a table, so that the
// columns line up: four cells that share the width, and an optional revert
// button in a column of width trail at the end.
type tableLayout struct{ trail float32 }

func (l *tableLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	pad := theme.Padding()
	w := size.Width
	if l.trail > 0 {
		w -= l.trail + pad
	}
	x := float32(0)
	for i, o := range objs {
		cw := w * columnShares[min(i, 3)]
		if i == 4 {
			cw = l.trail
			x += pad
		}
		h := min(o.MinSize().Height, size.Height)
		o.Move(fyne.NewPos(x, (size.Height-h)/2))
		o.Resize(fyne.NewSize(max(cw-pad, 0), h))
		x += cw
	}
}

func (l *tableLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var h float32
	for _, o := range objs {
		h = max(h, o.MinSize().Height)
	}
	return fyne.NewSize(400+l.trail, h)
}

// pageOf returns the index in ui.pages and the title of the page that
// shows tab; len(ui.pages) and "Other settings" for a tab no page shows.
func pageOf(u *ui, tab vebus.Tab) (int, string) {
	if tab != vebus.TabNone {
		for i, p := range u.pages {
			if p.tab == tab {
				return i, p.title
			}
		}
	}
	return len(u.pages), tr("Other settings")
}

// newRow returns the row of id of d, on its page, with old and new values
// and no outcome.
func newRow(u *ui, d *vebus.Device, id vebus.ID, old, new string) changeRow {
	tab := vebus.TabNone
	if e, ok := d.Catalogue().Entry(id); ok {
		tab = e.Tab
	} else if id.Kind == vebus.Setting && vebus.GridCodeSetting(id.Num) {
		tab = vebus.TabGrid
	}
	page, group := pageOf(u, tab)
	return changeRow{id: id, page: page, group: group, name: settingName(d, id), ref: idText(id), old: old, new: new}
}

// lastRead returns a model with the values of d as last read from the
// device or loaded from the file: what its unsent changes replace.
func lastRead(d *vebus.Device) *vebus.Device {
	// A dry run sends nothing and reports the last-read value of every
	// changed setting.
	rep, _ := vebus.Write(context.Background(), nil, d, vebus.WriteOptions{DryRun: true, Override: true})
	old := map[uint16]uint16{}
	for _, r := range rep.Results {
		if r.OldKnown {
			old[r.ID.Num] = r.Old
		}
	}
	b := vebus.NewDevice(d.Catalogue())
	b.Address, b.Firmware = d.Address, d.Firmware
	for _, id := range d.SettingIDs() {
		info, _ := d.SettingInfo(id)
		raw, _ := d.Raw(vebus.SettingID(id))
		if v, ok := old[id]; ok {
			raw = v
		}
		b.AddSetting(id, info, raw)
	}
	return b
}

// Markers of the notes column.
func resetMark() rowMark    { return rowMark{tr("needs reset"), colorNameChanged} }
func passwordMark() rowMark { return rowMark{tr("needs password"), theme.ColorNamePrimary} }
func invalidMark() rowMark  { return rowMark{tr("invalid"), theme.ColorNameError} }

// needsPassword reports whether d says a change of id needs a password, or
// id is the grid code and its value needs one on d's firmware.
func needsPassword(d *vebus.Device, id vebus.ID) bool {
	if a, ok := d.Access(id); ok && a.PasswordRequired() {
		return true
	}
	if id == vebus.SettingID(gridCodeSetting) {
		raw, err := d.Raw(id)
		return err == nil && !gridcode.SelectableWithoutPassword(gridcode.Code(raw), d.Firmware)
	}
	return false
}

// markIssues adds the issues to the rows of their settings, and a row of d
// for an issue whose setting has none.
func markIssues(u *ui, d *vebus.Device, rows []changeRow, issues []vebus.Issue) []changeRow {
	for _, is := range issues {
		i := slices.IndexFunc(rows, func(r changeRow) bool { return r.id == is.ID })
		if i < 0 {
			v := entryValueText(d, is.ID)
			rows = append(rows, newRow(u, d, is.ID, v, v))
			i = len(rows) - 1
		}
		r := &rows[i]
		if !slices.Contains(r.marks, invalidMark()) {
			r.marks = append(r.marks, invalidMark())
		}
		if r.detail != "" {
			r.detail += "\n"
		}
		r.detail += is.Message
		r.detailError = true
	}
	return rows
}

// changeRows returns the rows of the unsent changes of d, whose last-read
// values before holds: the entries of the settings pages and the grid page
// and the grid words, with their markers and validation errors.
func changeRows(u *ui, d, before *vebus.Device) []changeRow {
	var rows []changeRow
	add := func(id vebus.ID) changeRow {
		r := newRow(u, d, id, entryValueText(before, id), entryValueText(d, id))
		if d.ResetRequired(id) {
			r.marks = append(r.marks, resetMark())
		}
		if needsPassword(d, id) {
			r.marks = append(r.marks, passwordMark())
		}
		return r
	}
	for _, e := range d.Entries() {
		if e.FlagWord || !slices.Contains(settingsTabSet, e.Tab) && e.Tab != vebus.TabGrid || !d.Dirty(e.ID()) {
			continue
		}
		rows = append(rows, add(e.ID()))
	}
	for _, n := range changedGridWords(d) {
		id := vebus.SettingID(n)
		if slices.ContainsFunc(rows, func(r changeRow) bool { return r.id == id }) {
			continue
		}
		rows = append(rows, add(id))
	}
	// Grid changes are never sent to a device (gridWriteCheck).
	if u.settings.file == nil {
		for i := range rows {
			if rows[i].id.Kind == vebus.Setting && vebus.GridCodeSetting(rows[i].id.Num) {
				rows[i].marks = append(rows[i].marks, rowMark{tr("cannot be sent yet"), theme.ColorNameError})
			}
		}
	}
	return markIssues(u, d, rows, validationErrors(d))
}

// resultRows returns the rows of a send of d: one per result of rep, a
// flag word as its flags that changed. before holds the values of d before
// the send (lastRead).
func resultRows(u *ui, d, before *vebus.Device, rep *vebus.WriteReport) []changeRow {
	cat := d.Catalogue()
	var rows []changeRow
	for _, res := range rep.Results {
		ids := []vebus.ID{res.ID}
		if cat.IsFlagWord(res.ID.Num) {
			var flags []vebus.ID
			for _, e := range d.Entries() {
				if w, _, ok := cat.FlagLocation(e.Num); e.Kind == vebus.Flag && ok && w == res.ID.Num {
					if entryValueText(before, e.ID()) != entryValueText(d, e.ID()) {
						flags = append(flags, e.ID())
					}
				}
			}
			if len(flags) > 0 {
				ids = flags
			}
		}
		for _, id := range ids {
			r := newRow(u, d, id, entryValueText(before, id), entryValueText(d, id))
			r.state, r.detail, r.detailError = resultState(res)
			if res.ResetRequired && r.state == stateVerified && (len(ids) == 1 || d.ResetRequired(id)) {
				r.marks = append(r.marks, resetMark())
			}
			rows = append(rows, r)
		}
	}
	return rows
}

// resultState returns the outcome of a write and its explanation.
func resultState(r vebus.WriteResult) (rowState, string, bool) {
	switch r.Status {
	case vebus.StatusVerified:
		return stateVerified, "", false
	case vebus.StatusUnchanged:
		return stateUnchanged, "", false
	case vebus.StatusFailed:
		if errors.Is(r.Err, mk2.ErrAccessLevel) {
			return stateRefused, tr("The device accepts a new value for this setting only after its password has been entered, " +
				"which this program cannot do. The device keeps its old value."), true
		}
		return stateFailed, tr("The device did not take the value: {{.Err}}", args{"Err": r.Err}), true
	}
	return stateNotSent, tr("Not sent, because sending stopped before this setting. It is still marked as changed and can be sent again."), false
}

// showTableDialog shows a dialog of size (tableDialogSize for a table; a
// zero height for the height of content) with content, a button that
// closes it and, if not nil, primary. Enter taps
// primary while it is enabled, or closes a dialog without primary; Escape
// closes the dialog. primary does onPrimary after closing the dialog.
func showTableDialog(u *ui, title string, content fyne.CanvasObject, size fyne.Size, dismiss string, primary *widget.Button, onPrimary func()) {
	dlg := dialog.NewCustomWithoutButtons(title, content, u.win)
	buttons := []fyne.CanvasObject{widget.NewButtonWithIcon(dismiss, theme.CancelIcon(), dlg.Hide)}
	enter := dlg.Hide
	if primary != nil {
		primary.OnTapped = func() {
			dlg.Hide()
			onPrimary()
		}
		buttons = append(buttons, primary)
		enter = func() {
			if !primary.Disabled() {
				primary.OnTapped()
			}
		}
	}
	dlg.SetButtons(buttons)
	dlg.Resize(size)
	showWithKeys(u, dlg, enter)
}

// showWithKeys shows dlg with Escape closing it and Enter calling enter
// (nil: nothing), while no widget has the keyboard focus. The previous key
// handler of the window comes back when dlg closes.
func showWithKeys(u *ui, dlg dialog.Dialog, enter func()) {
	c := u.win.Canvas()
	prev := c.OnTypedKey()
	c.Unfocus()
	c.SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyEscape:
			dlg.Hide()
		case fyne.KeyReturn, fyne.KeyEnter:
			if enter != nil {
				enter()
			}
		default:
			if prev != nil {
				prev(ev)
			}
		}
	})
	dlg.SetOnClosed(func() { c.SetOnTypedKey(prev) })
	dlg.Show()
}

// summaryLine returns a line of bold text, with an icon in colour unless
// icon is nil.
func summaryLine(icon fyne.Resource, text string) fyne.CanvasObject {
	l := widget.NewLabel(text)
	l.TextStyle.Bold = true
	l.Wrapping = fyne.TextWrapWord
	if icon == nil {
		return l
	}
	return container.NewBorder(nil, nil, widget.NewIcon(icon), nil, l)
}

// callout returns text with an icon on a band: a note the user should act
// on.
func callout(icon fyne.Resource, text string) fyne.CanvasObject {
	t := widget.NewRichText(&widget.TextSegment{Text: text, Style: widget.RichTextStyle{ColorName: theme.ColorNameForeground}})
	t.Wrapping = fyne.TextWrapWord
	return container.NewStack(newColourRect(colorNamePage, fyne.Size{}, theme.SizeNameInputRadius),
		container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), container.NewBorder(nil, nil, widget.NewIcon(icon), nil, t)))
}
