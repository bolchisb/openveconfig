package main

import (
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

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// settingsTabs returns the tabs of the device settings: General (with the
// battery monitor), Inverter, Charger, Virtual switch and Advanced. A tab
// with nothing the loaded device supports is disabled and says so.
func settingsTabs(u *ui) []*container.TabItem {
	type tab struct {
		title string
		tab   vebus.Tab
	}
	var items []*container.TabItem
	for _, t := range []tab{
		{tr("General"), vebus.TabGeneral},
		{tr("Inverter"), vebus.TabInverter},
		{tr("Charger"), vebus.TabCharger},
		{tr("Virtual switch"), vebus.TabVirtualSwitch},
		{tr("Advanced"), vebus.TabAdvanced},
	} {
		var content fyne.CanvasObject
		var p *settingsPage
		if t.tab == vebus.TabVirtualSwitch {
			p = newSubPages(u, t.tab)
		} else {
			p = newSettingsPage(u, t.tab)
		}
		content = p.content
		if t.tab == vebus.TabCharger {
			content = container.NewBorder(presetsBar(u), nil, nil, nil, content)
		}
		item := container.NewTabItem(t.title, content)
		u.settings.onChange(func(loaded bool) {
			if !loaded || u.tabs == nil {
				return
			}
			if p.empty() && u.settings.dev != nil {
				u.tabs.DisableItem(item)
			} else {
				u.tabs.EnableItem(item)
			}
		})
		u.settings.onFind(func(e vebus.Entry) bool {
			if e.Tab != t.tab || u.tabs == nil {
				return false
			}
			u.tabs.Select(item)
			p.show(e)
			return true
		})
		items = append(items, item)
	}
	return items
}

// settingsPage shows the entries of one tab that the loaded device
// supports, in groups, each bound to the model in ui.settings. It rebuilds
// when a model is loaded and shows the new values after every edit.
type settingsPage struct {
	u      *ui
	tab    vebus.Tab
	groups []string // the groups shown; all of the tab's when empty
	// content is what the page shows; its objects are replaced on load.
	content *fyne.Container
	rows    []*settingRow
	// sub are the pages of the sub-tabs of a tab shown as sub-tabs.
	sub     []*settingsPage
	subTabs *container.AppTabs
	// updating is set while the controls are set from the model, so their
	// change callbacks do not write back.
	updating bool
}

// settingsPageContent returns the content that shows the supported entries of tab
// in the given catalogue groups (all of the tab's groups when none are
// given), one card per group, each control bound to ui.settings. The grid
// tab uses it for its ordinary settings.
func settingsPageContent(u *ui, tab vebus.Tab, groups ...string) fyne.CanvasObject {
	return newSettingsPage(u, tab, groups...).content
}

func newSettingsPage(u *ui, tab vebus.Tab, groups ...string) *settingsPage {
	p := &settingsPage{u: u, tab: tab, groups: groups, content: container.NewStack()}
	p.build()
	u.settings.onChange(func(loaded bool) {
		if loaded {
			p.build()
		} else {
			p.update()
		}
	})
	return p
}

// newSubPages returns a page that shows each group of tab on a sub-tab of
// its own; a group with nothing supported has no sub-tab.
func newSubPages(u *ui, tab vebus.Tab) *settingsPage {
	p := &settingsPage{u: u, tab: tab, content: container.NewStack()}
	for _, g := range catalogueGroups(tab) {
		p.sub = append(p.sub, newSettingsPage(u, tab, g))
	}
	p.buildSubTabs()
	u.settings.onChange(func(loaded bool) {
		if loaded {
			p.buildSubTabs()
		}
	})
	return p
}

// buildSubTabs rebuilds the sub-tabs. Registered after the sub-pages, so
// they are built first.
func (p *settingsPage) buildSubTabs() {
	p.subTabs = nil
	var items []*container.TabItem
	for _, s := range p.sub {
		if !s.empty() {
			items = append(items, container.NewTabItem(groupLabel(s.groups[0]), s.content))
		}
	}
	if len(items) == 0 {
		p.content.Objects = []fyne.CanvasObject{p.note()}
	} else {
		p.subTabs = container.NewAppTabs(items...)
		p.content.Objects = []fyne.CanvasObject{p.subTabs}
	}
	p.content.Refresh()
}

// catalogueGroups returns the groups of tab in catalogue order.
func catalogueGroups(tab vebus.Tab) []string {
	var groups []string
	for _, e := range vebus.DefaultCatalogue().Entries() {
		if e.Tab == tab && !slices.Contains(groups, e.Group) {
			groups = append(groups, e.Group)
		}
	}
	return groups
}

// empty reports whether the page shows no entry.
func (p *settingsPage) empty() bool {
	if p.sub != nil {
		return p.subTabs == nil
	}
	return len(p.rows) == 0
}

// show selects the sub-tab that holds e, if any, and opens the value dialog
// of a number.
func (p *settingsPage) show(e vebus.Entry) {
	for _, s := range p.sub {
		if s.groups[0] == e.Group && p.subTabs != nil {
			for _, it := range p.subTabs.Items {
				if it.Content == s.content {
					p.subTabs.Select(it)
				}
			}
			s.show(e)
			return
		}
	}
	for _, r := range p.rows {
		if r.e.ID() == e.ID() && r.edit != nil {
			r.edit()
		}
	}
}

// note returns the text shown instead of the settings.
func (p *settingsPage) note() fyne.CanvasObject {
	text := tr("No settings loaded. Get the settings from a device, or open a settings file.")
	if p.u.settings.dev != nil {
		text = tr("The loaded device has none of these settings.")
	}
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// build rebuilds the page for the loaded model.
func (p *settingsPage) build() {
	p.rows = nil
	d := p.u.settings.dev
	var cards []fyne.CanvasObject
	if d != nil {
		labels := settingLabels()
		byGroup := map[string][]vebus.Entry{}
		var order []string
		for _, e := range d.Entries() {
			if e.Tab != p.tab || e.FlagWord || (len(p.groups) > 0 && !slices.Contains(p.groups, e.Group)) {
				continue
			}
			if _, ok := byGroup[e.Group]; !ok {
				order = append(order, e.Group)
			}
			byGroup[e.Group] = append(byGroup[e.Group], e)
		}
		for _, g := range order {
			form := container.New(layout.NewFormLayout())
			for _, e := range byGroup[g] {
				r := p.newRow(e, labels)
				p.rows = append(p.rows, r)
				form.Add(r.name)
				form.Add(container.NewBorder(nil, nil, nil, r.marker, r.control))
			}
			cards = append(cards, widget.NewCard("", groupLabel(g), form))
		}
	}
	if len(cards) == 0 {
		p.content.Objects = []fyne.CanvasObject{p.note()}
	} else {
		p.content.Objects = []fyne.CanvasObject{container.NewVScroll(container.NewVBox(cards...))}
	}
	p.update()
	p.content.Refresh()
}

// update shows the current values and marks of every row.
func (p *settingsPage) update() {
	p.updating = true
	defer func() { p.updating = false }()
	for _, r := range p.rows {
		r.update()
		p.mark(r)
	}
}

// mark shows whether r's value was changed and whether a change needs a
// device reset or a password.
func (p *settingsPage) mark(r *settingRow) {
	d := p.u.settings.dev
	id := r.e.ID()
	dirty := d.Dirty(id)
	var tags []string
	if dirty {
		tags = append(tags, tr("changed"))
	}
	if d.ResetRequired(id) {
		tags = append(tags, tr("needs reset"))
	}
	if a, ok := d.Access(id); ok && a.PasswordRequired() {
		tags = append(tags, tr("needs password"))
	}
	r.marker.SetText(strings.Join(tags, ", "))
	r.marker.Importance = widget.MediumImportance
	if dirty {
		r.marker.Importance = widget.WarningImportance
	}
	r.marker.Refresh()
	if r.name.TextStyle.Bold != dirty {
		r.name.TextStyle.Bold = dirty
		r.name.Refresh()
	}
}

// settingRow is the control of one entry.
type settingRow struct {
	e       vebus.Entry
	name    *widget.Label
	control fyne.CanvasObject
	marker  *widget.Label
	update  func() // shows the model's value
	edit    func() // opens the value dialog; nil when the control is edited in place
}

// newRow returns the row of entry e: a check box for a flag, a choice for a
// flag shown as a choice of two or an enumerated setting, else a button
// that shows the value and opens the value dialog.
func (p *settingsPage) newRow(e vebus.Entry, labels map[vebus.ID]string) *settingRow {
	u := p.u
	label := labels[e.ID()]
	if label == "" {
		label = e.Name
	}
	r := &settingRow{e: e, name: widget.NewLabel(label), marker: widget.NewLabel("")}
	id := e.ID()
	// set applies an edit made with a control and shows its result.
	set := func(err error) {
		if err != nil {
			showError(u, err)
		}
		u.settings.changed()
	}
	switch {
	case e.Kind == vebus.Flag:
		if c, ok := flagChoices()[e.Num]; ok {
			radio := widget.NewRadioGroup(c[:], nil)
			radio.Horizontal = true
			radio.Required = true
			radio.OnChanged = func(s string) {
				if !p.updating {
					set(u.settings.dev.SetFlag(e.Num, s == c[1]))
				}
			}
			r.control = radio
			r.update = func() {
				on, _ := u.settings.dev.Flag(e.Num)
				radio.SetSelected(c[boolIndex(on)])
			}
			break
		}
		check := widget.NewCheck("", nil)
		check.OnChanged = func(v bool) {
			if !p.updating {
				set(u.settings.dev.SetFlag(e.Num, v != invertedFlags[e.Num]))
			}
		}
		r.control = check
		r.update = func() {
			on, _ := u.settings.dev.Flag(e.Num)
			check.SetChecked(on != invertedFlags[e.Num])
		}
	case len(e.Choices) > 0:
		sel := widget.NewSelect(nil, nil)
		var values []uint16
		sel.OnChanged = func(string) {
			if i := sel.SelectedIndex(); !p.updating && i >= 0 {
				set(u.settings.dev.SetRaw(id, values[i]))
			}
		}
		r.control = sel
		r.update = func() {
			var opts []string
			values, opts = choiceOptions(u.settings.dev, e)
			sel.SetOptions(opts)
			raw, _ := u.settings.dev.Raw(id)
			sel.SetSelectedIndex(slices.Index(values, raw))
		}
	default:
		b := widget.NewButton("", nil)
		r.edit = func() { editNumber(u, e, label) }
		b.OnTapped = r.edit
		b.Alignment = widget.ButtonAlignLeading
		r.control = b
		r.update = func() { b.SetText(valueText(u.settings.dev, e)) }
	}
	return r
}

func boolIndex(b bool) int {
	if b {
		return 1
	}
	return 0
}

// choiceOptions returns the values an enumerated setting can take on d,
// and their labels: the catalogue's values within the device's limits,
// and the current value.
func choiceOptions(d *vebus.Device, e vebus.Entry) ([]uint16, []string) {
	info, _ := d.SettingInfo(e.Num)
	cur, _ := d.Raw(e.ID())
	var values []uint16
	for v := range e.Choices {
		if v >= info.Min && v <= info.Max {
			values = append(values, v)
		}
	}
	if !slices.Contains(values, cur) {
		values = append(values, cur)
	}
	slices.Sort(values)
	names := choiceLabels()[e.Num]
	labels := make([]string, len(values))
	for i, v := range values {
		switch {
		case names[v] != "":
			labels[i] = names[v]
		case e.Choices[v] != "":
			labels[i] = e.Choices[v]
		default:
			labels[i] = tr("Value {{.Value}}", args{"Value": strconv.Itoa(int(v))})
		}
	}
	return values, labels
}

// numberView converts between the display value of a numeric setting in
// the model and the value the tab shows (see conversion).
type numberView struct {
	d    *vebus.Device
	id   uint16
	conv conversion
	// shown = base + factor * display
	factor, base float64
	unit         string
}

func newNumberView(d *vebus.Device, e vebus.Entry) numberView {
	n := numberView{d: d, id: e.Num, conv: settingConversions[e.Num], factor: 1, unit: e.Unit}
	switch n.conv {
	case convHours:
		n.factor, n.unit = 1.0/60, "h"
	case convDays:
		n.factor, n.unit = 1.0/1440, "d"
	case convSeconds:
		n.factor, n.unit = 60, "s"
	case convWatts:
		if v, err := d.Display(vebus.SettingID(5)); err == nil && v > 0 {
			n.factor, n.unit = v, "W"
		} else {
			n.conv = convNone
		}
	case convAbsolute:
		if e.RelativeTo == nil {
			n.conv = convNone
			break
		}
		base, err := d.Absolute(e.RelativeTo.Base)
		if err != nil {
			n.conv = convNone
			break
		}
		n.base, n.factor = base, float64(e.RelativeTo.Sign)
	}
	return n
}

func (n numberView) shown(display float64) float64 { return n.base + n.factor*display }

func (n numberView) display(shown float64) float64 { return (shown - n.base) / n.factor }

// limits returns the range, step and default in shown units.
func (n numberView) limits() (lo, hi, step, def float64) {
	id := vebus.SettingID(n.id)
	dlo, dhi, _ := n.d.Limits(id)
	lo, hi = n.shown(dlo), n.shown(dhi)
	if lo > hi {
		lo, hi = hi, lo
	}
	s, _ := n.d.Step(id)
	info, _ := n.d.SettingInfo(n.id)
	return lo, hi, s * math.Abs(n.factor), n.shown(info.Value(info.Default))
}

// decimals returns the number of decimals that shows a whole step.
func (n numberView) decimals() int {
	_, _, step, _ := n.limits()
	if step <= 0 || step >= 1 {
		return 0
	}
	return min(3, int(math.Ceil(-math.Log10(step)-1e-9)))
}

// text formats a value in shown units, with its unit.
func (n numberView) text(v float64) string {
	return withUnit(formatNumber(v, n.decimals()), n.unit)
}

// withUnit appends the unit symbol to a formatted value. Unit symbols are
// not translated (docs/adr-0001-localization.md, rule 3).
func withUnit(v, unit string) string {
	a := args{"Value": v}
	switch unit {
	case "":
		return v
	case "V":
		return tr("{{.Value}} V", a)
	case "A":
		return tr("{{.Value}} A", a)
	case "W":
		return tr("{{.Value}} W", a)
	case "Ah":
		return tr("{{.Value}} Ah", a)
	case "%":
		return tr("{{.Value}} %", a)
	case "s":
		return tr("{{.Value}} s", a)
	case "min":
		return tr("{{.Value}} min", a)
	case "h":
		return tr("{{.Value}} h", a)
	case "d":
		return tr("{{.Value}} d", a)
	}
	return tr("{{.Value}} {{.Unit}}", args{"Value": v, "Unit": unit})
}

// valueText formats the current value of a numeric entry.
func valueText(d *vebus.Device, e vebus.Entry) string {
	if d.NotApplicable(e.ID()) {
		return tr("not applicable")
	}
	v, err := d.Display(e.ID())
	if err != nil {
		return tr("unknown")
	}
	n := newNumberView(d, e)
	return n.text(n.shown(v))
}

// errNotANumber is returned by parseNumber.
var errNotANumber = errors.New("not a number")

// parseNumber reads a number typed with the decimal separator of the UI
// language or a point, without digit grouping.
func parseNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if sep := decimalSeparator(); sep != "." {
		s = strings.Replace(s, sep, ".", 1)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, errNotANumber
	}
	return v, nil
}

// decimalSeparator returns the decimal separator of the UI language.
func decimalSeparator() string { return strings.Trim(formatNumber(0.5, 1), "05") }

// entryText formats v for editing: as parseNumber reads it.
func entryText(v float64, decimals int) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', decimals, 64), ".", decimalSeparator(), 1)
}

// setNumber parses text, a value in the shown units of numeric entry e, and
// sets it in d. A value shown in other units than the model's is rounded to
// the nearest value the device can store. The error is a message for the
// user.
func setNumber(d *vebus.Device, e vebus.Entry, text string) error {
	raw, err := numberRaw(d, e, text)
	if err != nil {
		return err
	}
	return d.SetRaw(e.ID(), raw)
}

// numberRaw checks text for setNumber and returns the raw value to set.
func numberRaw(d *vebus.Device, e vebus.Entry, text string) (uint16, error) {
	n := newNumberView(d, e)
	lo, hi, step, _ := n.limits()
	outOfRange := errors.New(tr("Enter a value from {{.Min}} to {{.Max}}, in steps of {{.Step}}.",
		args{"Min": n.text(lo), "Max": n.text(hi), "Step": n.text(step)}))
	v, err := parseNumber(text)
	if err != nil {
		return 0, errors.New(tr("Enter a number."))
	}
	info, _ := d.SettingInfo(e.Num)
	raw, err := info.Raw(n.display(v))
	if err != nil {
		return 0, outOfRange
	}
	if n.conv == convNone && d.ValidateDisplay(e.ID(), v) != nil {
		return 0, outOfRange
	}
	if d.ValidateRaw(e.ID(), raw) != nil {
		return 0, outOfRange
	}
	return raw, nil
}

// editNumber opens the value dialog of numeric entry e, labelled label: it
// shows the allowed range, the step and the device default, and accepts
// only a valid value.
func editNumber(u *ui, e vebus.Entry, label string) {
	d := u.settings.dev
	if d == nil {
		return
	}
	n := newNumberView(d, e)
	lo, hi, step, def := n.limits()
	entry := widget.NewEntry()
	if v, err := d.Display(e.ID()); err == nil {
		entry.SetText(entryText(n.shown(v), n.decimals()))
	}
	entry.Validator = func(s string) error {
		_, err := numberRaw(d, e, s)
		return err
	}
	// Report an invalid value while it is typed, which disables OK.
	entry.AlwaysShowValidationError = true
	info := func(v float64) fyne.CanvasObject { return widget.NewLabel(n.text(v)) }
	items := []*widget.FormItem{
		widget.NewFormItem(tr("Value"), entry),
		widget.NewFormItem(tr("Minimum"), info(lo)),
		widget.NewFormItem(tr("Maximum"), info(hi)),
		widget.NewFormItem(tr("Step"), info(step)),
		widget.NewFormItem(tr("Default"), info(def)),
	}
	dlg := dialog.NewForm(label, tr("OK"), tr("Cancel"), items, func(ok bool) {
		if !ok || u.settings.dev != d {
			return
		}
		if err := setNumber(d, e, entry.Text); err != nil {
			showError(u, err)
		}
		u.settings.changed()
	}, u.win)
	dlg.Resize(fyne.NewSize(420, dlg.MinSize().Height))
	dlg.Show()
	u.win.Canvas().Focus(entry)
}
