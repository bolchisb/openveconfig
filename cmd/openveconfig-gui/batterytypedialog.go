package main

// The battery type card of the Charger page and the dialog it opens: the
// types with the values they set on the loaded device, applying one,
// saving the current values as a type and removing a type of the user's
// own. The types themselves are in batterytypes.go.

import (
	"errors"
	"math"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// allTypes returns the built-in types followed by the user types.
func allTypes(u *ui) []batteryType {
	return append(builtinTypes(), loadUserTypes(u.app.Preferences())...)
}

// batteryTypeCard returns the card at the top of the Charger page: the
// battery type the current values match, which opens the battery type
// dialog. It follows every change of the model and is hidden while the
// loaded model has no parameter of a type.
func batteryTypeCard(u *ui) fyne.CanvasObject {
	name := widget.NewLabel("")
	name.TextStyle.Bold = true
	name.Truncation = fyne.TextTruncateEllipsis
	var update func(bool)
	open := func() { showBatteryTypes(u, func() { update(false) }) }
	change := widget.NewButtonWithIcon(tr("Change…"), theme.NavigateNextIcon(), open)
	change.IconPlacement = widget.ButtonIconTrailingText
	text := container.New(layout.NewCustomPaddedVBoxLayout(-8), captionText(tr("Battery type"), theme.ColorNamePlaceHolder), name)
	card := newChoiceCard(container.NewBorder(nil, nil, nil, container.NewCenter(change), text), open)
	update = func(bool) {
		d := u.settings.dev
		if d == nil || !supportsTypes(d) {
			card.Hide()
			return
		}
		types := allTypes(u)
		if i := matchType(d, types); i >= 0 {
			name.Importance = widget.MediumImportance
			name.SetText(types[i].name)
		} else {
			name.Importance = widget.LowImportance
			name.SetText(tr("No matching type (custom values)"))
		}
		card.Show()
	}
	u.settings.onChange(update)
	update(true)
	return card
}

// choiceCard is a card that does something when tapped: a battery type in
// the dialog's list, or the battery type card of the Charger page. A
// selected card is tinted in the accent colour.
type choiceCard struct {
	widget.BaseWidget
	content fyne.CanvasObject
	bg      *themeRect
	onTap   func()
}

func newChoiceCard(content fyne.CanvasObject, onTap func()) *choiceCard {
	c := &choiceCard{content: content, onTap: onTap, bg: newThemeRect(colorNameSurface, theme.ColorNameSeparator, -1, fyne.Size{})}
	c.ExtendBaseWidget(c)
	return c
}

func (c *choiceCard) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(c.bg, container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), c.content)))
}

// Tapped calls onTap.
func (c *choiceCard) Tapped(*fyne.PointEvent) {
	if c.onTap != nil {
		c.onTap()
	}
}

// Cursor is the pointer, as over a link.
func (c *choiceCard) Cursor() desktop.Cursor { return desktop.PointerCursor }

// setSelected shows whether the card is selected.
func (c *choiceCard) setSelected(on bool) {
	c.bg.fill, c.bg.stroke = colorNameSurface, theme.ColorNameSeparator
	if on {
		c.bg.fill, c.bg.stroke = theme.ColorNameSelection, theme.ColorNamePrimary
	}
	c.bg.Refresh()
}

// radioIcon is the radio button of a type in the list.
func radioIcon(on bool) fyne.Resource {
	if on {
		return theme.NewColoredResource(theme.RadioButtonCheckedIcon(), theme.ColorNamePrimary)
	}
	return theme.NewColoredResource(theme.RadioButtonIcon(), theme.ColorNamePlaceHolder)
}

// showBatteryTypes shows the battery types with the values they set on the
// loaded device, the one the current values match marked as current, and
// applies the one chosen to the model. closed is called when the dialog
// closes. Enter applies, Escape cancels.
func showBatteryTypes(u *ui, closed func()) {
	d := u.settings.dev
	if d == nil {
		return
	}
	body := container.NewStack()
	dlg := dialog.NewCustomWithoutButtons(tr("Battery type"), body, u.win)
	apply := widget.NewButtonWithIcon(tr("Apply"), theme.ConfirmIcon(), nil)
	apply.Importance = widget.HighImportance
	var types []batteryType
	var cards []*choiceCard
	var radios []*widget.Icon
	selected := -1
	choose := func(i int) {
		selected = i
		for j, c := range cards {
			c.setSelected(j == i)
			radios[j].SetResource(radioIcon(j == i))
		}
		if i >= 0 {
			apply.Enable()
		} else {
			apply.Disable()
		}
	}
	var scroll *container.Scroll
	shown := false
	// reveal scrolls the list so that the card of type i shows.
	reveal := func(i int) {
		if i < 0 || !shown {
			return
		}
		drv := fyne.CurrentApp().Driver()
		top := drv.AbsolutePositionForObject(cards[i]).Y - drv.AbsolutePositionForObject(scroll.Content).Y
		if top < scroll.Offset.Y || top+cards[i].Size().Height > scroll.Offset.Y+scroll.Size().Height {
			scroll.ScrollToOffset(fyne.NewPos(0, max(0, top-theme.Padding())))
		}
	}
	var rebuild func()
	rebuild = func() {
		types = allTypes(u)
		matched := matchType(d, types)
		k := voltageScale(d)
		cards, radios = nil, nil
		var builtin, own []fyne.CanvasObject
		for i, bt := range types {
			radio := widget.NewIcon(radioIcon(false))
			var remove *widget.Button
			if bt.user {
				remove = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { confirmRemoveType(u, bt.name, rebuild) })
				remove.Importance = widget.LowImportance
			}
			card := newChoiceCard(typeEntry(d, bt, k, i == matched, radio, remove), func() { choose(i) })
			cards, radios = append(cards, card), append(radios, radio)
			if bt.user {
				own = append(own, card)
			} else {
				builtin = append(builtin, card)
			}
		}
		if len(own) == 0 {
			own = append(own, mutedText(tr("You have no types of your own yet. When the current values match no type, you can save them as one.")))
		}
		save := widget.NewButtonWithIcon(tr("Save current values as a type…"), theme.ContentAddIcon(), func() { saveTypeDialog(u, d, rebuild) })
		saveRow := []fyne.CanvasObject{container.NewHBox(save)}
		status := summaryLine(theme.NewColoredResource(theme.InfoIcon(), theme.ColorNamePlaceHolder), tr("The current values match no type (custom values)."))
		if matched >= 0 {
			status = summaryLine(theme.NewSuccessThemedResource(theme.ConfirmIcon()),
				tr("The current values match “{{.Type}}”.", args{"Type": types[matched].name}))
			save.Disable()
			saveRow = append(saveRow, mutedText(tr("Only values that match no type can be saved as a new type.")))
		}
		note := mutedText(tr("Values are shown for the {{.Voltage}} battery of this device. Applying a type changes the settings here only; "+
			"send them to the device or save the file afterwards.", args{"Voltage": withUnit(formatNumber(12*k, 0), "V")}))
		list := []fyne.CanvasObject{typeSection(tr("Built-in types"))}
		list = append(list, builtin...)
		list = append(list, typeSection(tr("Your types")))
		list = append(list, own...)
		list = append(list, saveRow...)
		scroll = container.NewVScroll(container.New(layout.NewCustomPaddedLayout(0, 0, 0, 12), container.NewVBox(list...)))
		body.Objects = []fyne.CanvasObject{container.NewBorder(container.NewVBox(status, note), nil, nil, nil, scroll)}
		body.Refresh()
		choose(matched)
		reveal(matched)
	}
	rebuild()
	cancel := widget.NewButtonWithIcon(tr("Cancel"), theme.CancelIcon(), dlg.Hide)
	apply.OnTapped = func() {
		if selected < 0 {
			return
		}
		bt := types[selected]
		dlg.Hide()
		if u.settings.dev != d {
			return
		}
		applyBatteryType(u, d, bt)
	}
	dlg.SetButtons([]fyne.CanvasObject{cancel, apply})
	if closed != nil {
		dlg.SetOnClosed(closed)
	}
	dlg.Resize(fyne.NewSize(760, 660))
	showWithKeys(u, dlg, func() {
		if !apply.Disabled() {
			apply.OnTapped()
		}
	})
	shown = true
	reveal(selected)
}

// typeSection is the heading of a section of the type list.
func typeSection(title string) fyne.CanvasObject {
	t := widget.NewRichText(&widget.TextSegment{Text: title,
		Style: widget.RichTextStyle{ColorName: theme.ColorNamePrimary, TextStyle: fyne.TextStyle{Bold: true}}})
	return container.New(layout.NewCustomPaddedLayout(6, 0, 0, 0), t)
}

// typeEntry is the content of the card of bt in the list: its radio
// button, name, description and values on d (voltage scale k), whether it
// is the current one, and the remove button of a user type (nil for none).
func typeEntry(d *vebus.Device, bt batteryType, k float64, current bool, radio *widget.Icon, remove *widget.Button) fyne.CanvasObject {
	segs := []widget.RichTextSegment{&widget.TextSegment{Text: bt.name,
		Style: widget.RichTextStyle{ColorName: theme.ColorNameForeground, TextStyle: fyne.TextStyle{Bold: true}}}}
	if bt.desc != "" {
		segs = append(segs, &widget.TextSegment{Text: bt.desc,
			Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder, SizeName: theme.SizeNameCaptionText}})
	}
	if s := typeSummary(d, bt, k); s != "" {
		segs = append(segs, &widget.TextSegment{Text: s,
			Style: widget.RichTextStyle{ColorName: theme.ColorNameForeground, SizeName: theme.SizeNameCaptionText}})
	}
	text := widget.NewRichText(segs...)
	text.Wrapping = fyne.TextWrapWord
	tags := container.NewHBox()
	if on, ok := typeValue(d, bt, slotLithium, k); ok && on >= 0.5 {
		tags.Add(newPill(tr("Lithium"), theme.ColorNamePrimary, false))
	}
	if on, ok := typeValue(d, bt, slotBMS, k); ok && on >= 0.5 {
		tags.Add(newPill(tr("VE.Bus BMS"), theme.ColorNamePrimary, false))
	}
	if current {
		tags.Add(newPill(tr("current"), theme.ColorNameSuccess, true))
	}
	if remove != nil {
		tags.Add(remove)
	}
	return container.NewBorder(nil, nil, container.NewCenter(radio), container.NewCenter(tags), text)
}

// typeValue returns the display value bt sets slot i to on d (voltage scale
// k), and false when it leaves it unchanged or uses a default d does not
// have.
func typeValue(d *vebus.Device, bt batteryType, i int, k float64) (float64, bool) {
	s, sv := typeSlots[i], bt.values[i]
	switch sv.mode {
	case slotSet:
		return s.display(sv.v, k), true
	case slotDefault:
		v, err := defaultDisplay(d, s.id)
		return v, err == nil
	}
	return 0, false
}

// typeSummary returns the main values bt sets on d (voltage scale k):
// absorption and float voltage, absorption time and charge curve.
func typeSummary(d *vebus.Device, bt batteryType, k float64) string {
	var parts []string
	if v, ok := typeValue(d, bt, slotAbsorption, k); ok {
		parts = append(parts, tr("Absorption {{.Value}}", args{"Value": withUnit(formatNumber(v, 2), "V")}))
	}
	if v, ok := typeValue(d, bt, slotFloat, k); ok {
		parts = append(parts, tr("Float {{.Value}}", args{"Value": withUnit(formatNumber(v, 2), "V")}))
	}
	if v, ok := typeValue(d, bt, slotAbsorptionTime, k); ok {
		h := v / 60
		dec := 0
		if h != math.Trunc(h) {
			dec = 1
		}
		parts = append(parts, tr("{{.Value}} absorption", args{"Value": withUnit(formatNumber(h, dec), "h")}))
	}
	if v, ok := typeValue(d, bt, slotChargeCurve, k); ok {
		if l := choiceLabels()[10][uint16(math.Round(v))]; l != "" {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, " · ")
}

// applyBatteryType applies bt to d, the loaded model, and shows what
// changed.
func applyBatteryType(u *ui, d *vebus.Device, bt batteryType) {
	ids := []vebus.ID{vebus.FlagID(typeTractionFlag)}
	for _, s := range typeSlots {
		ids = append(ids, s.id)
	}
	before := map[vebus.ID]string{}
	for _, id := range ids {
		if d.Supported(id) {
			before[id] = entryValueText(d, id)
		}
	}
	res := bt.apply(d)
	u.settings.changed()
	showTypeApplied(u, d, bt, before, res)
}

// showTypeApplied shows the outcome of applying bt to d: what changed, the
// type the values match now, the values that were limited to the device's
// range, and the VE.Bus BMS notice. before holds the value texts before.
func showTypeApplied(u *ui, d *vebus.Device, bt batteryType, before map[vebus.ID]string, res applyResult) {
	ids := slices.Clone(res.changed)
	for _, s := range typeSlots {
		if _, ok := res.limited[s.id]; ok && !slices.Contains(ids, s.id) {
			ids = append(ids, s.id)
		}
	}
	var rows []changeRow
	for _, id := range ids {
		r := newRow(u, d, id, before[id], entryValueText(d, id))
		if d.ResetRequired(id) {
			r.marks = append(r.marks, resetMark())
		}
		if v, ok := res.limited[id]; ok {
			r.marks = append(r.marks, rowMark{tr("limited"), theme.ColorNameWarning})
			r.detail = limitText(d, id, v)
		}
		if slices.Contains(res.defaulted, id) {
			r.detail = tr("This type has no value for this setting, so the device default was set.")
		}
		rows = append(rows, r)
	}
	types := allTypes(u)
	now := tr("The settings now match no type.")
	if i := matchType(d, types); i >= 0 {
		now = tr("The settings now match “{{.Type}}”.", args{"Type": types[i].name})
	}
	head := container.NewVBox(
		summaryLine(theme.NewSuccessThemedResource(theme.ConfirmIcon()), tr("“{{.Type}}” was applied.", args{"Type": bt.name})),
		mutedText(trn("{{.Count}} setting changed. It is an unsent change, like any other edit.",
			"{{.Count}} settings changed. They are unsent changes, like any other edit.", len(res.changed))+" "+now))
	warning := theme.NewWarningThemedResource(theme.WarningIcon())
	if n := len(res.limited); n > 0 {
		head.Add(callout(warning, trn("{{.Count}} value of the type is outside this device's range and was set to the nearest limit.",
			"{{.Count}} values of the type are outside this device's range and were set to the nearest limit.", n)))
	}
	if res.noBMS {
		head.Add(callout(warning, tr("This device has no setting for a VE.Bus BMS: its firmware works with a VE.Bus BMS only through "+
			"the VE.Bus BMS assistant. The type was applied as a lithium battery without a BMS. Add the assistant with a tool that "+
			"supports assistants.")))
	}
	table := newChangeTable(tableColumns{old: tr("Before"), new: tr("New value"), notes: tr("Notes")}, rows,
		tr("Nothing changed: the settings already have the values of this type."))
	showTableDialog(u, tr("Battery type applied"), container.NewBorder(head, nil, nil, nil, table), tableDialogSize, tr("Close"), nil, nil)
	if len(res.errs) > 0 {
		showError(u, errors.Join(res.errs...))
	}
}

// limitText explains that the value v (display value) a type asked for id
// is outside d's range.
func limitText(d *vebus.Device, id vebus.ID, v float64) string {
	e, ok := d.Catalogue().Entry(id)
	if !ok {
		e = vebus.Entry{Kind: id.Kind, Num: id.Num}
	}
	n := newNumberView(d, e)
	lo, hi, _, _ := n.limits()
	return tr("The type asks for {{.Value}}; this device allows {{.Min}} to {{.Max}}.",
		args{"Value": n.text(n.shown(v)), "Min": n.text(lo), "Max": n.text(hi)})
}

// saveTypeDialog asks for a name and saves the current values of d as a
// user type under it, then calls after. A user type of the same name is
// replaced; a built-in name is refused.
func saveTypeDialog(u *ui, d *vebus.Device, after func()) {
	prefs := u.app.Preferences()
	var kept, missing []string
	for _, s := range typeSlots {
		if _, err := d.Display(s.id); err == nil {
			kept = append(kept, settingName(d, s.id))
		} else {
			missing = append(missing, settingName(d, s.id))
		}
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder(tr("Name of the battery type"))
	hint := captionText("", theme.ColorNamePlaceHolder)
	hint.Hide()
	content := container.NewVBox(entry, hint,
		mutedText(tr("The type keeps these values: {{.List}}.", args{"List": strings.Join(kept, ", ")})))
	if len(missing) > 0 {
		content.Add(mutedText(tr("This device does not have {{.List}}. For these the type uses the device default.",
			args{"List": strings.Join(missing, ", ")})))
	}
	save := widget.NewButtonWithIcon(tr("Save"), theme.DocumentSaveIcon(), nil)
	save.Importance = widget.HighImportance
	save.Disable()
	entry.OnChanged = func(text string) {
		name := strings.TrimSpace(text)
		msg, colour, ok := "", theme.ColorNamePlaceHolder, name != ""
		switch {
		case slices.ContainsFunc(builtinTypes(), func(bt batteryType) bool { return bt.name == name }):
			msg, colour, ok = tr("A built-in type has this name."), theme.ColorNameError, false
		case slices.ContainsFunc(loadUserTypes(prefs), func(bt batteryType) bool { return bt.name == name }):
			msg = tr("Your type with this name will be replaced.")
		}
		hint.Segments[0].(*widget.TextSegment).Style.ColorName = colour
		setCaptionText(hint, msg)
		content.Refresh()
		if ok {
			save.Enable()
		} else {
			save.Disable()
		}
	}
	dlg := dialog.NewCustomWithoutButtons(tr("Save as a battery type"), content, u.win)
	do := func() {
		if save.Disabled() {
			return
		}
		name := strings.TrimSpace(entry.Text)
		dlg.Hide()
		if u.settings.dev != d {
			return
		}
		types := slices.DeleteFunc(loadUserTypes(prefs), func(bt batteryType) bool { return bt.name == name })
		storeUserTypes(prefs, append(types, typeFromDevice(d, name)))
		after()
	}
	save.OnTapped = do
	entry.OnSubmitted = func(string) { do() }
	dlg.SetButtons([]fyne.CanvasObject{widget.NewButtonWithIcon(tr("Cancel"), theme.CancelIcon(), dlg.Hide), save})
	// Wrapped text knows its height only once it has a width.
	dlg.Resize(fyne.NewSize(560, 0))
	dlg.Resize(fyne.NewSize(560, dlg.MinSize().Height))
	showWithKeys(u, dlg, do)
	u.win.Canvas().Focus(entry)
}

// confirmRemoveType removes the user type named name after asking, then
// calls after.
func confirmRemoveType(u *ui, name string, after func()) {
	c := newConfirm(u, tr("Remove battery type"), tr("Remove your battery type “{{.Type}}”? This cannot be undone.", args{"Type": name}),
		tr("Remove"), func() {
			prefs := u.app.Preferences()
			storeUserTypes(prefs, slices.DeleteFunc(loadUserTypes(prefs), func(bt batteryType) bool { return bt.name == name }))
			after()
		})
	c.SetConfirmImportance(widget.DangerImportance)
	showWithKeys(u, c, c.Confirm)
}
