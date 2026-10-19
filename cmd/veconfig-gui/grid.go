package main

// The grid tab: the grid code (setting 81), the ordinary grid settings and
// the parameters of the selected grid code, packed into the grid words
// (settings 129-190, pkg/gridcode).
//
// The device keeps two checker words (settings 128 and 191) with the grid
// parameters; how they are computed is not known (#9). Grid parameters can
// therefore be edited here but neither sent nor saved (gridWriteCheck).
// The grid code itself is sent like any other setting; the device decides
// whether it needs a password. This program never enters or derives one
// (docs/CLEAN-ROOM.md, "Access control").

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/gridcode"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// gridCodeSetting is the setting that holds the grid code.
const gridCodeSetting = 81

// The grid words: the checker words and the parameter words between them.
const firstGridWord, lastGridWord = gridcode.CheckerWordA, gridcode.CheckerWordB

// deviceSettingIDs returns the settings Get reads: those of the catalogue
// and the grid words, which the catalogue does not list.
func deviceSettingIDs() []uint16 {
	var ids []uint16
	for _, e := range vebus.DefaultCatalogue().Entries() {
		if e.Kind == vebus.Setting {
			ids = append(ids, e.Num)
		}
	}
	for id := firstGridWord; id <= lastGridWord; id++ {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// changedGridWords returns the grid words (128-191) of d that were changed.
func changedGridWords(d *vebus.Device) []uint16 {
	var ids []uint16
	for id := firstGridWord; id <= lastGridWord; id++ {
		if sid := vebus.SettingID(id); d.Supported(sid) && d.Dirty(sid) {
			ids = append(ids, id)
		}
	}
	return ids
}

// gridWriteCheck refuses to send or save d when grid words were changed:
// valid checker words for them cannot be computed (gridcode.CheckWrite).
func gridWriteCheck(d *vebus.Device) error {
	ids := changedGridWords(d)
	err := gridcode.CheckWrite(ids)
	if err == nil {
		return nil
	}
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = strconv.Itoa(int(id))
	}
	return &uiError{err: err, msg: tr("Grid parameters were changed (settings {{.Settings}}). They cannot be sent or saved yet: "+
		"the device keeps two check words with them (settings 128 and 191) whose computation is not known. "+
		"Nothing was sent or saved. Undo the grid parameter changes on the Grid tab, then try again.",
		args{"Settings": strings.Join(list, ", ")})}
}

// writeAllButGrid writes every setting of d, as WriteOptions.All does,
// except the grid words and an unchanged grid code: rewriting them would
// need the checker words, and the grid code may need a password. It
// writes a copy of d and records the outcome in d.
func writeAllButGrid(ctx context.Context, c *mk2.Client, d *vebus.Device, progress func(done, total int)) (*vebus.WriteReport, error) {
	w := vebus.NewDevice(d.Catalogue())
	w.Address, w.Firmware = d.Address, d.Firmware
	for _, id := range d.SettingIDs() {
		sid := vebus.SettingID(id)
		if vebus.GridCodeSetting(id) && !(id == gridCodeSetting && d.Dirty(sid)) {
			continue
		}
		info, _ := d.SettingInfo(id)
		raw, _ := d.Raw(sid)
		w.AddSetting(id, info, raw)
	}
	rep, err := vebus.Write(ctx, c, w, vebus.WriteOptions{All: true, Progress: progress})
	for _, r := range rep.Results {
		if r.Status == vebus.StatusVerified || r.Status == vebus.StatusUnchanged {
			if v, rerr := w.Raw(r.ID); rerr == nil {
				d.SetRead(r.ID, v)
			}
		}
	}
	return rep, err
}

// gridTabView is the grid tab.
type gridTabView struct {
	u    *ui
	item *container.TabItem
	// Grid code.
	code       *widget.Select
	codeValues []uint16
	codeMarker *widget.Label
	codeNote   *widget.Label
	// ordinary shows the catalogue's grid settings.
	ordinary *settingsPage
	// Parameters of the selected grid code.
	subTabs    *container.AppTabs
	paramsItem *container.TabItem
	params     *fyne.Container
	rows       []*paramRow
	page       *gridcode.Page // the page the rows show
	pageCode   int            // the grid code the parameters were built for; -1 none
	undo       *widget.Button
	// loaded holds the grid words as loaded, to show what changed and to
	// undo the changes.
	loaded map[uint16]uint16
	// updating is set while controls are set from the model.
	updating bool
}

// paramRow is the control of one grid parameter.
type paramRow struct {
	p       gridcode.Param
	name    *widget.Label
	control fyne.CanvasObject
	marker  *widget.Label
	update  func()
}

// gridTab returns the grid tab.
func gridTab(u *ui) *container.TabItem { return newGridTab(u).item }

func newGridTab(u *ui) *gridTabView {
	g := &gridTabView{u: u, pageCode: -1}
	g.code = widget.NewSelect(nil, func(string) {
		if i := g.code.SelectedIndex(); !g.updating && i >= 0 && u.settings.dev != nil {
			if err := u.settings.dev.SetRaw(vebus.SettingID(gridCodeSetting), g.codeValues[i]); err != nil {
				showError(u, err)
			}
			u.settings.changed()
		}
	})
	g.codeMarker = widget.NewLabel("")
	g.codeNote = widget.NewLabel("")
	g.codeNote.Wrapping = fyne.TextWrapWord
	form := container.New(layout.NewFormLayout(), widget.NewLabel(tr("Grid code")),
		container.NewBorder(nil, nil, nil, g.codeMarker, g.code))
	codeCard := widget.NewCard("", tr("Grid code"), container.NewVBox(form, g.codeNote))

	g.ordinary = newSettingsPage(u, vebus.TabGrid, "AC input voltage limits", "UPS function", "AC input frequency")
	g.undo = widget.NewButton(tr("Undo grid parameter changes"), g.undoChanges)
	g.params = container.NewStack()
	g.paramsItem = container.NewTabItem(tr("Grid parameters"), g.params)
	g.subTabs = container.NewAppTabs(
		container.NewTabItem(tr("Grid code and AC input"), container.NewBorder(codeCard, nil, nil, nil, g.ordinary.content)),
		g.paramsItem)
	g.item = container.NewTabItem(tr("Grid"), g.subTabs)

	u.settings.onChange(func(loaded bool) {
		if loaded {
			g.load()
		}
		g.update(loaded)
	})
	u.settings.onFind(func(e vebus.Entry) bool {
		if e.Tab != vebus.TabGrid || u.tabs == nil {
			return false
		}
		u.tabs.Select(g.item)
		g.subTabs.Select(g.subTabs.Items[0])
		g.ordinary.show(e)
		return true
	})
	g.load()
	g.update(true)
	return g
}

// load records the grid words of a newly loaded model and shows or hides
// the parameters.
func (g *gridTabView) load() {
	g.loaded = map[uint16]uint16{}
	d := g.u.settings.dev
	if d != nil {
		for id := firstGridWord; id <= lastGridWord; id++ {
			if raw, err := d.Raw(vebus.SettingID(id)); err == nil {
				g.loaded[id] = raw
			}
		}
	}
	has := slices.Contains(g.subTabs.Items, g.paramsItem)
	switch {
	case len(g.loaded) == 0 && has:
		g.subTabs.Remove(g.paramsItem)
	case len(g.loaded) > 0 && !has:
		g.subTabs.Append(g.paramsItem)
	}
}

// update shows the model's grid code and parameters. The parameter
// controls are rebuilt when rebuild is set or the grid code changed.
func (g *gridTabView) update(rebuild bool) {
	g.updating = true
	defer func() { g.updating = false }()
	g.updateCode()
	code := -1
	if raw, err := g.rawCode(); err == nil {
		code = int(raw)
	}
	if rebuild || code != g.pageCode {
		g.buildParams(code)
	}
	for _, r := range g.rows {
		r.update()
		g.mark(r)
	}
	if len(g.changedParams()) > 0 {
		g.undo.Enable()
	} else {
		g.undo.Disable()
	}
}

// rawCode returns the grid code of the loaded model.
func (g *gridTabView) rawCode() (uint16, error) {
	d := g.u.settings.dev
	if d == nil {
		return 0, errors.New("no model")
	}
	return d.Raw(vebus.SettingID(gridCodeSetting))
}

// updateCode shows the grid code choice: the known codes the device
// accepts, each marked when it needs a password on this firmware.
func (g *gridTabView) updateCode() {
	d := g.u.settings.dev
	cur, err := g.rawCode()
	if err != nil {
		g.code.SetOptions(nil)
		g.code.Disable()
		g.codeValues = nil
		g.codeMarker.SetText("")
		if d == nil {
			g.codeNote.SetText(tr("No settings loaded. Get the settings from a device, or open a settings file."))
		} else {
			g.codeNote.SetText(tr("The loaded device does not report a grid code (setting 81)."))
		}
		return
	}
	info, _ := d.SettingInfo(gridCodeSetting)
	g.codeValues = nil
	var labels []string
	for _, ci := range gridcode.Codes() {
		if v := uint16(ci.Code); v >= info.Min && v <= info.Max {
			g.codeValues = append(g.codeValues, v)
		}
	}
	if !slices.Contains(g.codeValues, cur) {
		g.codeValues = append(g.codeValues, cur)
		slices.Sort(g.codeValues)
	}
	for _, v := range g.codeValues {
		labels = append(labels, gridCodeLabel(gridcode.Code(v), d.Firmware))
	}
	g.code.SetOptions(labels)
	g.code.SetSelectedIndex(slices.Index(g.codeValues, cur))
	g.code.Enable()

	id := vebus.SettingID(gridCodeSetting)
	var tags []string
	if d.Dirty(id) {
		tags = append(tags, tr("changed"))
	}
	if d.ResetRequired(id) {
		tags = append(tags, tr("needs reset"))
	}
	if a, ok := d.Access(id); ok && a.PasswordRequired() || !gridcode.SelectableWithoutPassword(gridcode.Code(cur), d.Firmware) {
		tags = append(tags, tr("needs password"))
	}
	g.codeMarker.SetText(strings.Join(tags, ", "))
	g.codeMarker.Importance = widget.MediumImportance
	if d.Dirty(id) {
		g.codeMarker.Importance = widget.WarningImportance
	}
	g.codeMarker.Refresh()
	g.codeNote.SetText(tr("Codes marked \"needs password\" are accepted by the device only after the installer password for grid codes " +
		"has been entered. This program cannot enter it: the device refuses such a code when the settings are sent, " +
		"and the send report says so."))
}

// gridCodeLabel names grid code c for the choice, with a note when it
// needs a password on the given firmware.
func gridCodeLabel(c gridcode.Code, firmware uint32) string {
	num := strconv.Itoa(int(c))
	var label string
	switch ci, ok := gridcode.Lookup(c); {
	case !ok:
		label = tr("{{.Number}}: unknown grid code", args{"Number": num})
	case ci.Region == "":
		label = tr("{{.Number}}: {{.Description}}", args{"Number": num, "Description": ci.Description})
	default:
		label = tr("{{.Number}}: {{.Region}}, {{.Standard}}", args{"Number": num, "Region": ci.Region, "Standard": ci.Standard})
	}
	if !gridcode.SelectableWithoutPassword(c, firmware) {
		label = tr("{{.Label}} (needs password)", args{"Label": label})
	}
	return label
}

// buildParams rebuilds the parameter controls for grid code code (-1: none
// known).
func (g *gridTabView) buildParams(code int) {
	g.pageCode, g.rows, g.page = code, nil, nil
	d := g.u.settings.dev
	var objs []fyne.CanvasObject
	note := func(text string) {
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapWord
		objs = append(objs, l)
	}
	if d != nil {
		note(tr("Grid parameters can be edited here to see how they are stored, but changed parameters cannot be sent " +
			"or saved yet: the device keeps two check words with them (settings 128 and 191) whose computation is not known."))
	}
	p, known := gridcode.PageFor(gridcode.Code(code))
	switch {
	case d == nil:
		note(tr("No settings loaded. Get the settings from a device, or open a settings file."))
	case code < 0:
		note(tr("The loaded device does not report a grid code (setting 81)."))
	case !known:
		note(tr("Which parameters grid code {{.Code}} uses is not known yet, so none are shown.", args{"Code": strconv.Itoa(code)}))
	case p == nil:
		note(tr("Grid code {{.Code}} has no parameters.", args{"Code": strconv.Itoa(code)}))
	default:
		g.page = p
		objs = append(objs, g.paramCards(d, p)...)
		if len(p.Params) == 0 {
			note(tr("This grid code has no parameters to set."))
		}
		if len(p.Unsupported) > 0 {
			note(tr("Not supported yet (how they are stored is not known): {{.Names}}", args{"Names": strings.Join(p.Unsupported, "; ")}))
		}
	}
	objs = append(objs, container.NewHBox(g.undo))
	g.params.Objects = []fyne.CanvasObject{container.NewVScroll(container.NewVBox(objs...))}
	g.params.Refresh()
}

// paramCards returns one card per group of page p, with a row per
// parameter whose word d holds.
func (g *gridTabView) paramCards(d *vebus.Device, p *gridcode.Page) []fyne.CanvasObject {
	var cards []fyne.CanvasObject
	var missing []string
	forms := map[string]*fyne.Container{}
	for _, prm := range p.Params {
		if _, ok := g.loaded[prm.Word]; !ok {
			if s := strconv.Itoa(int(prm.Word)); !slices.Contains(missing, s) {
				missing = append(missing, s)
			}
			continue
		}
		form, ok := forms[prm.Group]
		if !ok {
			form = container.New(layout.NewFormLayout())
			forms[prm.Group] = form
			title := prm.Group
			if title == "" {
				title = p.Name
			}
			cards = append(cards, widget.NewCard("", title, form))
		}
		r := g.newParamRow(prm)
		g.rows = append(g.rows, r)
		form.Add(r.name)
		form.Add(container.NewBorder(nil, nil, nil, r.marker, r.control))
	}
	if len(missing) > 0 {
		l := widget.NewLabel(tr("The device does not report settings {{.Settings}}, so the parameters stored in them are not shown.",
			args{"Settings": strings.Join(missing, ", ")}))
		l.Wrapping = fyne.TextWrapWord
		cards = append(cards, l)
	}
	return cards
}

// word returns the current value of grid word id.
func (g *gridTabView) word(id uint16) uint16 {
	raw, _ := g.u.settings.dev.Raw(vebus.SettingID(id))
	return raw
}

// setParam sets parameter p to display value v in the model.
func (g *gridTabView) setParam(p gridcode.Param, v float64) error {
	d := g.u.settings.dev
	w, err := p.Pack(g.word(p.Word), v)
	if err != nil {
		return paramRangeError(p)
	}
	return d.SetRaw(vebus.SettingID(p.Word), w)
}

// apply shows the outcome of an edit.
func (g *gridTabView) apply(err error) {
	if err != nil {
		showError(g.u, err)
	}
	g.u.settings.changed()
}

func (g *gridTabView) newParamRow(p gridcode.Param) *paramRow {
	r := &paramRow{p: p, name: widget.NewLabel(p.Name), marker: widget.NewLabel("")}
	switch p.Kind {
	case gridcode.Checkbox:
		check := widget.NewCheck("", func(on bool) {
			if !g.updating {
				g.apply(g.setParam(p, float64(boolIndex(on))))
			}
		})
		r.control = check
		r.update = func() { check.SetChecked(p.Unpack(g.word(p.Word)) == 1) }
	case gridcode.Choice:
		sel := widget.NewSelect(nil, nil)
		var values []int
		sel.OnChanged = func(string) {
			if i := sel.SelectedIndex(); !g.updating && i >= 0 {
				g.apply(g.setParam(p, float64(values[i])))
			}
		}
		r.control = sel
		r.update = func() {
			cur := p.Field(g.word(p.Word))
			values = values[:0]
			for v := range p.Choices {
				values = append(values, v)
			}
			if _, ok := p.Choices[cur]; !ok {
				values = append(values, cur)
			}
			slices.Sort(values)
			labels := make([]string, len(values))
			for i, v := range values {
				labels[i] = p.Choices[v]
				if labels[i] == "" {
					labels[i] = tr("Value {{.Value}}", args{"Value": strconv.Itoa(v)})
				}
			}
			sel.SetOptions(labels)
			sel.SetSelectedIndex(slices.Index(values, cur))
		}
	default:
		b := widget.NewButton("", func() { g.editParam(p) })
		b.Alignment = widget.ButtonAlignLeading
		r.control = b
		r.update = func() { b.SetText(paramText(p, p.Unpack(g.word(p.Word)))) }
	}
	return r
}

// mark shows whether r's parameter differs from the loaded value.
func (g *gridTabView) mark(r *paramRow) {
	changed := r.p.Field(g.word(r.p.Word)) != r.p.Field(g.loaded[r.p.Word])
	text := ""
	if changed {
		text = tr("changed")
	}
	r.marker.SetText(text)
	r.marker.Importance = widget.WarningImportance
	r.marker.Refresh()
	if r.name.TextStyle.Bold != changed {
		r.name.TextStyle.Bold = changed
		r.name.Refresh()
	}
}

// changedParams returns the grid words that differ from the loaded values.
func (g *gridTabView) changedParams() []uint16 {
	var ids []uint16
	if g.u.settings.dev == nil {
		return nil
	}
	for id, raw := range g.loaded {
		if g.word(id) != raw {
			ids = append(ids, id)
		}
	}
	return ids
}

// undoChanges sets the grid words back to their loaded values.
func (g *gridTabView) undoChanges() {
	d := g.u.settings.dev
	if d == nil {
		return
	}
	var errs []error
	for _, id := range g.changedParams() {
		if err := d.SetRaw(vebus.SettingID(id), g.loaded[id]); err != nil {
			errs = append(errs, err)
		}
	}
	g.apply(errors.Join(errs...))
}

// paramDecimals returns the number of decimals that shows one step of p.
func paramDecimals(p gridcode.Param) int {
	step := math.Abs(p.Value(p.Min+1) - p.Value(p.Min))
	if step <= 0 || step >= 1 || math.IsNaN(step) || math.IsInf(step, 0) {
		return 0
	}
	return min(4, int(math.Ceil(-math.Log10(step)-1e-9)))
}

// paramText formats a display value of p with its unit.
func paramText(p gridcode.Param, v float64) string {
	return withUnit(formatNumber(v, paramDecimals(p)), p.Unit)
}

// paramRangeError is the message for a value p cannot take.
func paramRangeError(p gridcode.Param) error {
	a, b := p.Value(p.Min), p.Value(p.Max)
	return errors.New(tr("Enter a value from {{.Min}} to {{.Max}}.",
		args{"Min": paramText(p, min(a, b)), "Max": paramText(p, max(a, b))}))
}

// editParam opens the value dialog of numeric parameter p.
func (g *gridTabView) editParam(p gridcode.Param) {
	u := g.u
	d := u.settings.dev
	if d == nil {
		return
	}
	check := func(s string) (float64, error) {
		v, err := parseNumber(s)
		if err != nil {
			return 0, errors.New(tr("Enter a number."))
		}
		if _, err := p.Pack(g.word(p.Word), v); err != nil {
			return 0, paramRangeError(p)
		}
		return v, nil
	}
	entry := widget.NewEntry()
	entry.SetText(entryText(p.Unpack(g.word(p.Word)), paramDecimals(p)))
	entry.Validator = func(s string) error { _, err := check(s); return err }
	entry.AlwaysShowValidationError = true
	info := func(text string) fyne.CanvasObject { return widget.NewLabel(text) }
	a, b := p.Value(p.Min), p.Value(p.Max)
	items := []*widget.FormItem{
		widget.NewFormItem(tr("Value"), entry),
		widget.NewFormItem(tr("Minimum"), info(paramText(p, min(a, b)))),
		widget.NewFormItem(tr("Maximum"), info(paramText(p, max(a, b)))),
	}
	if p.HasDefault {
		items = append(items, widget.NewFormItem(tr("Default"), info(paramText(p, p.Value(p.Default)))))
	}
	items = append(items, widget.NewFormItem(tr("Stored in"), info(tr("setting {{.Number}}, bits {{.Mask}}",
		args{"Number": strconv.Itoa(int(p.Word)), "Mask": "0x" + strings.ToUpper(strconv.FormatUint(uint64(p.Mask), 16))}))))
	dlg := dialog.NewForm(p.Name, tr("OK"), tr("Cancel"), items, func(ok bool) {
		if !ok || u.settings.dev != d {
			return
		}
		v, err := check(entry.Text)
		if err == nil {
			err = g.setParam(p, v)
		}
		g.apply(err)
	}, u.win)
	dlg.Resize(fyne.NewSize(420, dlg.MinSize().Height))
	dlg.Show()
	u.win.Canvas().Focus(entry)
}
