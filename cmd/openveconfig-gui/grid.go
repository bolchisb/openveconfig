package main

// The grid tab: the grid code (setting 81), the ordinary grid settings and
// the parameters of the selected grid code, packed into the grid words
// (settings 129-190, pkg/gridcode).
//
// The device keeps two checker words (settings 128 and 191) with the grid
// parameters (gridcode.Stamp). A settings file with a changed grid code or
// changed grid parameters is saved with newly stamped checker words
// (settingsFile.save). Sending them straight to a device would need a
// fixed write order that has not been verified on hardware, so grid
// changes are never sent to a device (gridWriteCheck). Whether a grid code
// needs a password is up to the device; this program never enters or
// derives one (docs/CLEAN-ROOM.md, "Access control").

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/gridcode"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
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

// changedGridSettings returns the grid code, if it was changed, and the
// changed grid words of d.
func changedGridSettings(d *vebus.Device) []uint16 {
	ids := changedGridWords(d)
	if d.Dirty(vebus.SettingID(gridCodeSetting)) {
		ids = append([]uint16{gridCodeSetting}, ids...)
	}
	return ids
}

// gridWriteCheck refuses to send d to a device when its grid code or grid
// words were changed (gridcode.CheckDeviceWrite): the checker words would
// have to be written in a fixed order that has not been verified on
// hardware.
func gridWriteCheck(d *vebus.Device) error {
	ids := changedGridSettings(d)
	err := gridcode.CheckDeviceWrite(ids)
	if err == nil {
		return nil
	}
	list := make([]string, len(ids))
	for i, id := range ids {
		list[i] = strconv.Itoa(int(id))
	}
	return &uiError{err: err, msg: tr("The grid code or grid parameters were changed (settings {{.Settings}}). They cannot be sent straight to a device yet: "+
		"the device keeps two check words with them that must be updated in a fixed order, which has not been verified on hardware. "+
		"Nothing was sent. To apply them, save them in a settings file and apply that file through a GX device (VRM Remote VEConfigure), "+
		"or undo the grid changes on the Grid page to send the other settings.",
		args{"Settings": strings.Join(list, ", ")})}
}

// writeAllButGrid writes every setting of d, as WriteOptions.All does,
// except the grid code and the grid words, which are never sent to a
// device (gridWriteCheck). It writes a copy of d and records the outcome
// in d.
func writeAllButGrid(ctx context.Context, c *mk2.Client, d *vebus.Device, progress func(done, total int)) (*vebus.WriteReport, error) {
	w := vebus.NewDevice(d.Catalogue())
	w.Address, w.Firmware = d.Address, d.Firmware
	for _, id := range d.SettingIDs() {
		sid := vebus.SettingID(id)
		if vebus.GridCodeSetting(id) {
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

// gridTabView is the grid page.
type gridTabView struct {
	u  *ui
	pg *page // the page in the sidebar
	// Grid code.
	code       *codePicker
	codeValues []uint16
	codeRow    *formRow
	codeNote   *widget.Label
	// ordinary shows the catalogue's grid settings.
	ordinary *settingsPage
	// Parameters of the selected grid code.
	subTabs    *container.AppTabs
	paramsItem *container.TabItem
	params     *fyne.Container
	paramCols  *formColumns
	rows       []*paramRow
	page       *gridcode.Page // the page the rows show
	pageCode   int            // the grid code the parameters were built for; -1 none
	undo       *widget.Button
	// loaded holds the grid words as loaded, to show what changed and to
	// undo the changes.
	loaded map[uint16]uint16
	// checkStatus describes the checker words as loaded (checkWordStatus).
	checkStatus string
	// updating is set while controls are set from the model.
	updating bool
}

// paramRow is the row of one grid parameter.
type paramRow struct {
	*formRow
	p      gridcode.Param
	update func()
}

// gridPage returns the grid page.
func gridPage(u *ui) *page { return newGridTab(u).pg }

func newGridTab(u *ui) *gridTabView {
	g := &gridTabView{u: u, pageCode: -1, paramCols: &formColumns{}}
	g.code = newCodePicker(u.win, tr("Country / grid code standard"), func(i int) {
		d := u.settings.dev
		if g.updating || i < 0 || d == nil {
			return
		}
		id := vebus.SettingID(gridCodeSetting)
		old, _ := d.Raw(id)
		if err := d.SetRaw(id, g.codeValues[i]); err != nil {
			showError(u, err)
		}
		u.settings.changed()
		// A code that needs a password asks for one, as in the original,
		// which also checks it here; this program only hands it to the
		// device when the settings are sent (accesspassword.go). Without
		// one the choice is undone.
		s := u.sess
		if !passwordsEnabled(s) || len(s.passwords) > 0 || gridcode.SelectableWithoutPassword(gridcode.Code(g.codeValues[i]), d.Firmware) {
			return
		}
		askAccessPasswords(u, s, func(ok bool) {
			if !ok && u.settings.dev == d {
				if err := d.SetRaw(id, old); err != nil {
					showError(u, err)
				}
				u.settings.changed()
			}
		})
	})
	g.codeNote = widget.NewLabel("")
	g.codeNote.Wrapping = fyne.TextWrapWord
	// The grid code row shares the label column of the settings under it.
	g.ordinary = newSettingsPage(u, vebus.TabGrid, "AC input voltage limits", "UPS function", "AC input frequency")
	g.codeRow = newFormRow(g.ordinary.cols, tr("Country / grid code standard"), "", g.code.button, controlWidth, "")
	g.code.caption = g.codeRow.setCaption
	g.codeRow.revert.OnTapped = func() {
		if d := u.settings.dev; d != nil {
			g.apply(revertEntry(d, vebus.SettingID(gridCodeSetting)))
		}
	}
	g.ordinary.lead = []fyne.CanvasObject{newFormCard(cardTitle(tr("Grid code")), g.codeRow.root, g.codeNote)}
	g.ordinary.leadLabels = []string{tr("Country / grid code standard")}
	g.ordinary.build()

	g.undo = widget.NewButton(tr("Undo grid parameter changes"), g.undoChanges)
	g.params = container.NewStack()
	g.paramsItem = container.NewTabItem(tr("Grid parameters"), g.params)
	g.subTabs = container.NewAppTabs(
		container.NewTabItem(tr("Grid code selection"), g.ordinary.content),
		g.paramsItem)
	g.pg = newPage(tr("Grid"), tr("The grid code, and the limits within which the AC input is accepted."),
		theme.LoginIcon(), vebus.TabGrid, withVisual(g.subTabs, pageVisual(u, vebus.TabGrid)))
	g.ordinary.scroll = g.pg.scroll

	u.settings.onChange(func(loaded bool) {
		if loaded {
			g.load()
		}
		g.update(loaded)
	})
	u.settings.onFind(func(e vebus.Entry) bool {
		if e.Tab != vebus.TabGrid {
			return false
		}
		u.showPage(g.pg)
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
	g.checkStatus = checkWordStatus(d, g.u.settings.file)
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
	relayout := false
	for _, r := range g.rows {
		r.update()
		g.mark(r)
		if r.p.NeedsMask != 0 {
			r.setEnabled(g.word(r.p.NeedsWord)&r.p.NeedsMask != 0)
		}
		if show := r.p.Shown(g.word(r.p.ShowWord)); show != r.root.Visible() {
			if show {
				r.root.Show()
			} else {
				r.root.Hide()
			}
			relayout = true
		}
	}
	if relayout {
		g.params.Refresh()
	}
	// Shown only while there is something to undo.
	if len(g.changedParams()) > 0 {
		g.undo.Enable()
		g.undo.Show()
	} else {
		g.undo.Disable()
		g.undo.Hide()
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
		g.code.SetItems(nil)
		g.code.Disable()
		g.codeValues = nil
		g.codeRow.setChanged(false)
		g.codeRow.setMarkers(false, false)
		if d == nil {
			g.codeNote.SetText(tr("No settings loaded. Get the settings from a device, or open a settings file."))
		} else {
			g.codeNote.SetText(tr("The loaded device does not report a grid code (setting 81)."))
		}
		return
	}
	info, _ := d.SettingInfo(gridCodeSetting)
	g.codeValues = nil
	var items []codeItem
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
		items = append(items, gridCodeItem(gridcode.Code(v), d.Firmware))
	}
	g.code.SetItems(items)
	g.code.SetSelectedIndex(slices.Index(g.codeValues, cur))
	g.code.Enable()

	id := vebus.SettingID(gridCodeSetting)
	a, ok := d.Access(id)
	g.codeRow.setChanged(d.Dirty(id))
	g.codeRow.setMarkers(d.ResetRequired(id), ok && a.PasswordRequired() || !gridcode.SelectableWithoutPassword(gridcode.Code(cur), d.Firmware))
	g.codeNote.SetText(tr("Codes marked \"needs password\" are accepted by the device only after the installer password for grid codes " +
		"has been entered. This program cannot enter it. A changed grid code can be saved to a settings file, " +
		"but not sent straight to a device."))
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
		label = tr("{{.Number}} – {{.Description}}", args{"Number": num, "Description": gridCodeText(ci.Description)})
	default:
		label = tr("{{.Number}} – {{.Region}}: {{.Standard}}", args{"Number": num, "Region": gridCodeText(ci.Region), "Standard": ci.Standard})
	}
	if !gridcode.SelectableWithoutPassword(c, firmware) {
		label = tr("{{.Label}} (needs password)", args{"Label": label})
	}
	return label
}

// gridCodeItem returns grid code c as an item of the picker: its number and
// region, its standard, and whether it needs a password on firmware.
func gridCodeItem(c gridcode.Code, firmware uint32) codeItem {
	num := strconv.Itoa(int(c))
	it := codeItem{label: gridCodeLabel(c, firmware), locked: !gridcode.SelectableWithoutPassword(c, firmware)}
	switch ci, ok := gridcode.Lookup(c); {
	case !ok:
		it.title = tr("{{.Number}}: unknown grid code", args{"Number": num})
	case ci.Region == "":
		// "None: (…)", "Other: …": the word before the colon names it.
		name, rest, _ := strings.Cut(gridCodeText(ci.Description), ": ")
		it.title = tr("{{.Number}} · {{.Region}}", args{"Number": num, "Region": name})
		it.subtitle = rest
	default:
		it.title = tr("{{.Number}} · {{.Region}}", args{"Number": num, "Region": gridCodeText(ci.Region)})
		it.subtitle = ci.Standard
	}
	return it
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
		note(tr("Changes to the grid parameters are saved to a settings file; they cannot be sent straight to a device yet."))
		if g.checkStatus != "" {
			note(g.checkStatus)
		}
	}
	p, known := gridPageFor(code, g.u.settings.file)
	// The tab is named after the page, as in the original.
	title := tr("Grid parameters")
	if p != nil {
		title = gridText(p, p.Name)
	}
	if g.paramsItem.Text != title {
		g.paramsItem.Text = title
		g.subTabs.Refresh()
	}
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
			var names []string
			for _, n := range p.Unsupported {
				names = append(names, gridText(p, n))
			}
			note(tr("Not supported yet (how they are stored is not known): {{.Names}}", args{"Names": strings.Join(names, "; ")}))
		}
	}
	objs = append(objs, container.NewHBox(g.undo))
	g.params.Objects = []fyne.CanvasObject{newForm(objs...)}
	g.params.Refresh()
}

// gridPageFor returns the parameter page of grid code code with its
// reactive-power group, whose layout follows the parameter-set version of
// file (not known for a device: file nil).
func gridPageFor(code int, file *settingsFile) (*gridcode.Page, bool) {
	p, known := gridcode.PageFor(gridcode.Code(code))
	v := -1
	if file != nil {
		if table, err := file.f.GridInfo(); err == nil {
			if pv, err := gridcode.ParamSetVersion(table, gridcode.Code(code)); err == nil {
				v = int(pv)
			}
		}
	}
	return p.WithReactivePower(gridcode.Code(code), v), known
}

// checkWordStatus describes whether the checker words of d, a model just
// loaded from file (nil: read from a device), fit its grid code, in the
// form the original checks when it shows settings (gridcode.Pair.CheckShown).
// The parameter-set version comes from the file's grid-info table (0
// without one); it is not read from a device, so for a device only the
// rest is checked. It returns "" when d has no checker words.
func checkWordStatus(d *vebus.Device, file *settingsFile) string {
	if d == nil {
		return ""
	}
	a, errA := d.Raw(vebus.SettingID(firstGridWord))
	b, errB := d.Raw(vebus.SettingID(lastGridWord))
	code, errC := d.Raw(vebus.SettingID(gridCodeSetting))
	if errA != nil || errB != nil || errC != nil {
		return ""
	}
	var v uint8
	if file != nil {
		table, err := file.f.GridInfo()
		if err == nil {
			v, err = gridcode.ParamSetVersion(table, gridcode.Code(code))
		}
		if err != nil {
			return tr("The check words (settings 128 and 191) cannot be checked: {{.Err}}", args{"Err": err})
		}
	}
	header, version := gridcode.Pair{A: a, B: b}.CheckShown(gridcode.Code(code), v)
	switch {
	case !header || file != nil && !version:
		return tr("The check words (settings 128 and 191) do not fit the grid code: the device may refuse the grid parameters.")
	}
	return "" // they fit: nothing to say, as in the original
}

// paramCards returns one card per group of page p, with a row per
// parameter whose word d holds.
func (g *gridTabView) paramCards(d *vebus.Device, p *gridcode.Page) []fyne.CanvasObject {
	var missing, groups, names []string
	rows := map[string][]fyne.CanvasObject{}
	var params []gridcode.Param
	for _, prm := range p.Params {
		lost := false
		for _, w := range []uint16{prm.Word, prm.High} {
			if _, ok := g.loaded[w]; w != 0 && !ok {
				lost = true
				if s := strconv.Itoa(int(w)); !slices.Contains(missing, s) {
					missing = append(missing, s)
				}
			}
		}
		if lost {
			continue
		}
		params = append(params, prm)
		names = append(names, gridText(p, prm.Name))
	}
	g.paramCols.fit(names)
	for _, prm := range params {
		if !slices.Contains(groups, prm.Group) {
			groups = append(groups, prm.Group)
		}
		r := g.newParamRow(prm)
		g.rows = append(g.rows, r)
		rows[prm.Group] = append(rows[prm.Group], r.root)
	}
	var cards []fyne.CanvasObject
	for _, grp := range groups {
		// The page's own parameters have no title: the tab names the page.
		var head fyne.CanvasObject
		if grp != "" {
			head = cardTitle(gridText(p, grp))
		}
		cards = append(cards, newFormCard(head, rows[grp]...))
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

// field returns p's field in the model; loadedField as loaded.
func (g *gridTabView) field(p gridcode.Param) int {
	var high uint16
	if p.High != 0 {
		high = g.word(p.High)
	}
	return p.FieldWords(g.word(p.Word), high)
}

func (g *gridTabView) loadedField(p gridcode.Param) int {
	return p.FieldWords(g.loaded[p.Word], g.loaded[p.High])
}

// setParam sets parameter p to display value v in the model.
func (g *gridTabView) setParam(p gridcode.Param, v float64) error {
	d := g.u.settings.dev
	var high uint16
	if p.High != 0 {
		high = g.word(p.High)
	}
	w, h, err := p.PackWords(g.word(p.Word), high, v)
	if err != nil {
		return paramRangeError(p)
	}
	if p.High != 0 {
		if err := d.SetRaw(vebus.SettingID(p.High), h); err != nil {
			return err
		}
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
	r := &paramRow{p: p}
	var control fyne.CanvasObject
	var width float32
	var entry *numberEntry
	switch p.Kind {
	case gridcode.Checkbox:
		check := widget.NewCheck("", func(on bool) {
			if !g.updating {
				g.apply(g.setParam(p, float64(boolIndex(on))))
			}
		})
		control = check
		r.update = func() { check.SetChecked(p.Value(g.field(p)) == 1) }
	case gridcode.Choice:
		var values []int
		// options returns the labels of the choices and of the current value.
		options := func() []string {
			cur := g.field(p)
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
				labels[i] = gridText(g.page, p.Choices[v])
				if labels[i] == "" {
					labels[i] = tr("Value {{.Value}}", args{"Value": strconv.Itoa(v)})
				}
			}
			return labels
		}
		opts := options()
		if len(opts) <= 3 {
			radio := newRadio(opts)
			radio.OnChanged = func(s string) {
				if i := slices.Index(radio.Options, s); !g.updating && i >= 0 {
					g.apply(g.setParam(p, float64(values[i])))
				}
			}
			control = radio
			r.update = func() {
				radio.Options = options()
				radio.SetSelected(radio.Options[slices.Index(values, g.field(p))])
				radio.Refresh()
			}
			break
		}
		sel := widget.NewSelect(opts, nil)
		sel.OnChanged = func(string) {
			if i := sel.SelectedIndex(); !g.updating && i >= 0 {
				g.apply(g.setParam(p, float64(values[i])))
			}
		}
		control, width = sel, selectWidth(opts)
		r.update = func() {
			sel.SetOptions(options())
			sel.SetSelectedIndex(slices.Index(values, g.field(p)))
		}
	default:
		entry = newNumberEntry()
		control, width = entry, numberWidth
	}
	r.formRow = newFormRow(g.paramCols, gridText(g.page, p.Name), "", control, width, gridText(nil, p.Unit))
	r.revert.OnTapped = func() {
		if d := g.u.settings.dev; d != nil {
			var err error
			if p.High != 0 {
				err = d.SetRaw(vebus.SettingID(p.High), g.loaded[p.High])
			}
			g.apply(errors.Join(err, d.SetRaw(vebus.SettingID(p.Word), g.word(p.Word)&^p.Mask|g.loaded[p.Word]&p.Mask)))
		}
	}
	if entry != nil {
		d := g.u.settings.dev
		show := r.bindNumber(entry, func() string { return entryText(p.Value(g.field(p)), paramDecimals(p)) }, func(text string) error {
			if g.u.settings.dev != d {
				return nil
			}
			v, err := parseNumber(text)
			if err != nil {
				return errors.New(tr("Enter a number."))
			}
			if _, _, err := p.PackWords(g.word(p.Word), 0, v); err != nil {
				return paramRangeError(p)
			}
			g.apply(g.setParam(p, v))
			return nil
		})
		a, b := p.Value(p.Min), p.Value(p.Max)
		r.setCaption(tr("{{.Min}} – {{.Max}}", args{"Min": formatNumber(min(a, b), paramDecimals(p)), "Max": paramText(p, max(a, b))}))
		r.update = show
	}
	return r
}

// mark shows whether r's parameter differs from the loaded value.
func (g *gridTabView) mark(r *paramRow) {
	r.setChanged(g.field(r.p) != g.loadedField(r.p))
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

// paramDecimals returns the number of decimals that shows one step of p,
// or the number the original shows (Param.Decimals).
func paramDecimals(p gridcode.Param) int {
	if p.Decimals > 0 {
		return p.Decimals
	}
	step := math.Abs(p.Value(p.Min+1) - p.Value(p.Min))
	if step <= 0 || step >= 1 || math.IsNaN(step) || math.IsInf(step, 0) {
		return 0
	}
	return min(4, int(math.Ceil(-math.Log10(step)-1e-9)))
}

// paramText formats a display value of p with its unit.
func paramText(p gridcode.Param, v float64) string {
	return withUnit(formatNumber(v, paramDecimals(p)), gridText(nil, p.Unit))
}

// paramRangeError is the message for a value p cannot take.
func paramRangeError(p gridcode.Param) error {
	a, b := p.Value(p.Min), p.Value(p.Max)
	return errors.New(tr("Enter a value from {{.Min}} to {{.Max}}.",
		args{"Min": paramText(p, min(a, b)), "Max": paramText(p, max(a, b))}))
}
