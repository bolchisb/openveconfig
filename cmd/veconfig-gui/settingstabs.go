package main

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// settingsPages returns the pages of the device settings: General (with
// the battery monitor), Inverter, Charger, Virtual switch and Advanced. A
// page with nothing the loaded device supports is greyed in the sidebar and
// says so when chosen.
func settingsPages(u *ui) []*page {
	type tab struct {
		title, desc string
		icon        fyne.Resource
		tab         vebus.Tab
	}
	var pages []*page
	for _, t := range []tab{
		{tr("General"), tr("Basic settings of the unit: AC output, AC input current limit and battery."), theme.SettingsIcon(), vebus.TabGeneral},
		{tr("Inverter"), tr("When the inverter starts and stops, and how it helps the AC input with the battery."), theme.LogoutIcon(), vebus.TabInverter},
		{tr("Charger"), tr("How the battery is charged: algorithm, voltages, current and times."), theme.DownloadIcon(), vebus.TabCharger},
		{tr("Virtual switch"), tr("Conditions that turn the virtual switch on and off, for example to drive the relay."), theme.RadioButtonCheckedIcon(), vebus.TabVirtualSwitch},
		{tr("Advanced"), tr("Settings that are rarely changed. Change them only if you know what they do."), theme.MoreHorizontalIcon(), vebus.TabAdvanced},
	} {
		var p *settingsPage
		if t.tab == vebus.TabVirtualSwitch {
			p = newSubPages(u, t.tab)
		} else {
			p = newSettingsPage(u, t.tab)
		}
		if t.tab == vebus.TabCharger {
			p.lead = []fyne.CanvasObject{batteryTypeCard(u)}
			p.build()
		}
		pg := newPage(t.title, t.desc, t.icon, t.tab, withVisual(p.content, pageVisual(u, t.tab)))
		pg.empty = p.empty
		p.scroll = pg.scroll
		for _, s := range p.sub {
			s.scroll = pg.scroll
		}
		u.settings.onFind(func(e vebus.Entry) bool {
			if e.Tab != t.tab {
				return false
			}
			u.showPage(pg)
			p.show(e)
			return true
		})
		pages = append(pages, pg)
	}
	return pages
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
	cols    *formColumns        // the label column of the rows
	scroll  *container.Scroll   // the scroll area of the page the rows are on; nil until set
	lead    []fyne.CanvasObject // shown above the cards
	// sub are the pages of the sub-tabs of a tab shown as sub-tabs.
	sub     []*settingsPage
	subTabs *container.AppTabs
	// updating is set while the controls are set from the model, so their
	// change callbacks do not write back.
	updating bool
	// switches are the card switches of the valueSwitches groups on the
	// page, by master setting.
	switches map[uint16]*valueSwitch
	// cardRows are the rows of each card, which is hidden when they all are.
	cardRows map[fyne.CanvasObject][]*settingRow
}

// valueSwitch is the card switch of a group switched on by a master value.
type valueSwitch struct {
	toggle *toggleSwitch
	head   *formRow
	// pending is set while the switch is on but no master value has been
	// entered yet.
	pending bool
	// last is the last master value that was not 0, restored when the
	// switch is turned on again.
	last uint16
}

// settingsPageContent returns the content that shows the supported entries of tab
// in the given catalogue groups (all of the tab's groups when none are
// given), one card per group, each control bound to ui.settings. The grid
// tab uses it for its ordinary settings.
func settingsPageContent(u *ui, tab vebus.Tab, groups ...string) fyne.CanvasObject {
	return newSettingsPage(u, tab, groups...).content
}

func newSettingsPage(u *ui, tab vebus.Tab, groups ...string) *settingsPage {
	p := &settingsPage{u: u, tab: tab, groups: groups, content: container.NewStack(), cols: &formColumns{}}
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

// show selects the sub-tab that holds e, if any, scrolls to e's row and
// focuses its control.
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
		if r.e.ID() != e.ID() {
			continue
		}
		if p.scroll != nil {
			drv := fyne.CurrentApp().Driver()
			y := drv.AbsolutePositionForObject(r.root).Y - drv.AbsolutePositionForObject(p.scroll.Content).Y
			p.scroll.ScrollToOffset(fyne.NewPos(0, max(0, y-2*theme.Padding())))
		}
		if r.focus != nil {
			p.u.win.Canvas().Focus(r.focus)
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
	p.rows, p.switches, p.cardRows = nil, map[uint16]*valueSwitch{}, map[fyne.CanvasObject][]*settingRow{}
	d := p.u.settings.dev
	var cards []fyne.CanvasObject
	if d != nil {
		labels := settingLabels()
		byGroup := map[string][]vebus.Entry{}
		var order, texts []string
		for _, e := range d.Entries() {
			if e.Tab != p.tab || e.FlagWord || (len(p.groups) > 0 && !slices.Contains(p.groups, e.Group)) {
				continue
			}
			g := entryGroup(e)
			if _, ok := byGroup[g]; !ok {
				order = append(order, g)
			}
			byGroup[g] = append(byGroup[g], e)
		}
		// Groups shown as the two columns of one card, each with its own
		// label column.
		paired := map[string]string{}
		for _, g := range order {
			if r, ok := cardPairs[g]; ok && len(byGroup[r]) > 0 {
				paired[g], paired[r] = r, ""
			}
		}
		for _, g := range order {
			if _, ok := paired[g]; ok {
				continue
			}
			for _, e := range byGroup[g] {
				if !isMasterFlag(e) {
					texts = append(texts, entryName(e, labels))
				}
			}
		}
		p.cols.fit(texts)
		for _, g := range order {
			if r, ok := paired[g]; ok {
				if r != "" {
					cards = append(cards, p.pairCard(g, r, byGroup, labels))
				}
				continue
			}
			head := cardTitle(groupLabel(g))
			if n, ok := valueSwitches[g]; ok && slices.ContainsFunc(byGroup[g], func(e vebus.Entry) bool { return e.ID() == vebus.SettingID(n) }) {
				head = p.newValueSwitch(g, n).head.root
			}
			var rows []fyne.CanvasObject
			var rowList []*settingRow
			for _, e := range byGroup[g] {
				title := ""
				if isMasterFlag(e) {
					title = groupLabel(g)
				}
				r := p.newRow(p.cols, e, entryName(e, labels), title)
				p.rows = append(p.rows, r)
				rowList = append(rowList, r)
				if r.header {
					head = r.root
				} else {
					rows = append(rows, r.root)
				}
			}
			c := newFormCard(head, rows...)
			p.cardRows[c] = rowList
			cards = append(cards, c)
		}
	}
	switch {
	case len(cards) == 0 && len(p.lead) == 0:
		p.content.Objects = []fyne.CanvasObject{p.note()}
	case len(cards) == 0:
		p.content.Objects = []fyne.CanvasObject{newForm(append(slices.Clone(p.lead), p.note())...)}
	default:
		p.content.Objects = []fyne.CanvasObject{newForm(append(slices.Clone(p.lead), cards...)...)}
	}
	p.update()
	p.content.Refresh()
}

// isMasterFlag reports whether e is a flag that switches its group on and
// off (masterFlags), shown as the header of its group's card.
func isMasterFlag(e vebus.Entry) bool {
	_, ok := masterFlags[e.Num]
	return e.Kind == vebus.Flag && ok
}

// entryName returns the label of e on the settings pages.
func entryName(e vebus.Entry, labels map[vebus.ID]string) string {
	if l := labels[e.ID()]; l != "" {
		return l
	}
	return e.Name
}

// update shows the current values and marks of every row, and disables the
// rows of functions that are switched off (masterFlags, masterValues).
func (p *settingsPage) update() {
	d := p.u.settings.dev
	if d == nil {
		return
	}
	p.updating = true
	defer func() { p.updating = false }()
	enabled := map[vebus.ID]bool{}
	for n, ids := range masterFlags {
		if on, err := d.Flag(n); err == nil {
			for _, id := range ids {
				enabled[id] = on != invertedFlags[n]
			}
		}
	}
	for n, ids := range masterValues {
		raw, err := d.Raw(vebus.SettingID(n))
		if err != nil {
			continue
		}
		on := raw != 0
		if s := p.switches[n]; s != nil {
			if on {
				s.pending, s.last = false, raw
			}
			on = on || s.pending
			s.toggle.SetOn(on)
			hint := ""
			if s.pending {
				hint = valueSwitchHint(n)
			}
			s.head.setCaption(hint)
			s.head.root.Refresh()
			enabled[vebus.SettingID(n)] = on
		}
		for _, id := range ids {
			enabled[id] = on
		}
	}
	_, lithiumErr := d.Flag(36)
	lithium := lithiumOn(d)
	for _, r := range p.rows {
		r.update()
		p.mark(r)
		on, ok := enabled[r.e.ID()]
		r.setEnabled(!ok || on)
		if lithiumErr == nil {
			switch id := r.e.ID(); {
			case slices.Contains(lithiumHidden, id):
				setShown(r.root, !lithium)
			case slices.Contains(lithiumOnly, id):
				setShown(r.root, lithium)
			}
		}
	}
	// A card whose rows are all hidden is hidden too.
	for c, rows := range p.cardRows {
		setShown(c, slices.ContainsFunc(rows, func(r *settingRow) bool { return r.header || r.root.Visible() }))
	}
}

// pairCard returns the card that shows the groups left and right as two
// columns, which share a narrower label column of their own.
func (p *settingsPage) pairCard(left, right string, byGroup map[string][]vebus.Entry, labels map[vebus.ID]string) fyne.CanvasObject {
	cols := &formColumns{control: pairControlWidth}
	var texts []string
	for _, e := range append(slices.Clone(byGroup[left]), byGroup[right]...) {
		texts = append(texts, entryName(e, labels))
	}
	cols.fitWithin(texts, pairLabelWidth)
	column := func(g string) fyne.CanvasObject {
		var rows []fyne.CanvasObject
		for _, e := range byGroup[g] {
			r := p.newRow(cols, e, entryName(e, labels), "")
			p.rows = append(p.rows, r)
			rows = append(rows, r.root)
		}
		return container.NewVBox(rows...)
	}
	return newFormCard(cardTitle(pairTitle(left)), newTwoColumns(column(left), column(right)))
}

// setShown shows or hides o.
func setShown(o fyne.CanvasObject, shown bool) {
	if o.Visible() != shown {
		if shown {
			o.Show()
		} else {
			o.Hide()
		}
	}
}

// newValueSwitch returns the card switch of group, which master setting n
// switches on.
func (p *settingsPage) newValueSwitch(group string, n uint16) *valueSwitch {
	s := &valueSwitch{}
	s.toggle = newToggleSwitch(func(on bool) {
		if !p.updating {
			p.switchValue(s, n, on)
		}
	})
	s.head = newFormHeader(p.cols, groupLabel(group), s.toggle)
	p.switches[n] = s
	return s
}

// switchValue turns the function of master setting n on or off. Off sets
// the value to 0; on restores the last value that was not 0, or, without
// one, enables the rows and focuses the value so that it can be entered.
func (p *settingsPage) switchValue(s *valueSwitch, n uint16, on bool) {
	u, d, id := p.u, p.u.settings.dev, vebus.SettingID(n)
	s.pending = false
	var err error
	switch {
	case !on:
		err = d.SetRaw(id, 0)
		if n == 64 {
			err = errors.Join(err, batteryMonitorOff(d))
		}
	case s.last != 0:
		err = d.SetRaw(id, s.last)
	default:
		s.pending = true
	}
	if err != nil {
		showError(u, err)
	}
	u.settings.changed()
	if s.pending {
		for _, r := range p.rows {
			if r.e.ID() == id && r.focus != nil {
				u.win.Canvas().Focus(r.focus)
			}
		}
	}
}

// mark shows whether r's value was changed and whether a change needs a
// device reset or a password.
func (p *settingsPage) mark(r *settingRow) {
	d := p.u.settings.dev
	id := r.e.ID()
	r.setChanged(d.Dirty(id))
	a, ok := d.Access(id)
	r.setMarkers(d.ResetRequired(id), ok && a.PasswordRequired())
}

// settingRow is the row of one entry.
type settingRow struct {
	*formRow
	e      vebus.Entry
	update func()         // shows the model's value
	focus  fyne.Focusable // what Find focuses; nil for none
	entry  *numberEntry   // the entry of a number; nil for other entries
}

// newRow returns the row of entry e, labelled label, of the loaded model:
// a check box for a flag, radio buttons for a flag shown as a choice of two
// or an enumerated setting with up to three values, a choice list for one
// with more, else an entry for a number. With a title, the row is the
// header of a card of that title.
func (p *settingsPage) newRow(cols *formColumns, e vebus.Entry, label, title string) *settingRow {
	u, d, id := p.u, p.u.settings.dev, e.ID()
	r := &settingRow{e: e}
	// set applies an edit made with a control and shows its result.
	set := func(err error) {
		if u.settings.dev != d {
			return
		}
		if err != nil {
			showError(u, err)
		}
		u.settings.changed()
	}
	var control fyne.CanvasObject
	var width float32
	switch {
	case e.Kind == vebus.Flag:
		if c, ok := flagChoices()[e.Num]; ok {
			radio := newRadio(c[:])
			radio.OnChanged = func(s string) {
				if !p.updating {
					set(d.SetFlag(e.Num, s == c[1]))
				}
			}
			control = radio
			r.update = func() {
				on, _ := d.Flag(e.Num)
				radio.SetSelected(c[boolIndex(on)])
			}
			break
		}
		changed := func(v bool) {
			if !p.updating {
				on := v != invertedFlags[e.Num]
				err := d.SetFlag(e.Num, on)
				if err == nil && e.Num == 36 {
					err = lithiumChanged(d, on)
				}
				set(err)
			}
		}
		if title != "" { // a card's master flag: a switch
			toggle := newToggleSwitch(changed)
			control, r.focus = toggle, toggle
			r.update = func() {
				on, _ := d.Flag(e.Num)
				toggle.SetOn(on != invertedFlags[e.Num])
			}
			break
		}
		check := widget.NewCheck("", changed)
		control, r.focus = check, check
		r.update = func() {
			on, _ := d.Flag(e.Num)
			check.SetChecked(on != invertedFlags[e.Num])
		}
	case len(e.Choices) > 0:
		values, opts := choiceOptions(d, e)
		if len(values) <= 3 {
			radio := newRadio(opts)
			radio.OnChanged = func(s string) {
				if i := slices.Index(radio.Options, s); !p.updating && i >= 0 {
					set(d.SetRaw(id, values[i]))
				}
			}
			control = radio
			r.update = func() {
				values, radio.Options = choiceOptions(d, e)
				raw, _ := d.Raw(id)
				radio.SetSelected(radio.Options[slices.Index(values, raw)])
				radio.Refresh()
			}
			break
		}
		sel := widget.NewSelect(opts, nil)
		sel.OnChanged = func(string) {
			if i := sel.SelectedIndex(); !p.updating && i >= 0 {
				set(d.SetRaw(id, values[i]))
			}
		}
		control, width, r.focus = sel, selectWidth(opts), sel
		r.update = func() {
			var opts []string
			values, opts = choiceOptions(d, e)
			sel.SetOptions(opts)
			raw, _ := d.Raw(id)
			sel.SetSelectedIndex(slices.Index(values, raw))
		}
	default:
		r.entry = newNumberEntry()
		control, width, r.focus = r.entry, numberWidth, r.entry
	}
	if title != "" {
		r.formRow = newFormHeader(cols, title, control)
	} else {
		r.formRow = newFormRow(cols, label, settingHelp()[id], control, width, "")
	}
	r.revert.OnTapped = func() { set(revertEntry(d, id)) }
	if r.entry != nil {
		show := r.bindNumber(r.entry, func() string { return numberEntryText(d, e) }, func(text string) error {
			if u.settings.dev != d {
				return nil
			}
			err := setNumber(d, e, text)
			if err == nil {
				u.settings.changed()
			}
			return err
		})
		r.update = func() {
			n := newNumberView(d, e)
			r.setUnit(n.unit)
			r.setCaption(n.rangeText())
			placeholder := ""
			if d.NotApplicable(id) {
				placeholder = tr("not applicable")
			}
			if r.entry.PlaceHolder != placeholder {
				r.entry.SetPlaceHolder(placeholder)
			}
			show()
		}
	}
	return r
}

// newRadio returns a required choice of opts, side by side when they fit
// the control column.
func newRadio(opts []string) *widget.RadioGroup {
	radio := widget.NewRadioGroup(opts, nil)
	radio.Required = true
	radio.Horizontal = true
	if radio.MinSize().Width > selectMaxWidth {
		radio.Horizontal = false
	}
	return radio
}

// selectWidth returns the width of a choice list of opts: the widest
// option, within controlWidth and selectMaxWidth.
func selectWidth(opts []string) float32 {
	th := theme.Current()
	w := float32(0)
	for _, o := range opts {
		w = max(w, fyne.MeasureText(o, th.Size(theme.SizeNameText), fyne.TextStyle{}).Width)
	}
	w += 3*th.Size(theme.SizeNameInnerPadding) + th.Size(theme.SizeNameInlineIcon)
	return min(max(w, controlWidth), selectMaxWidth)
}

// revertEntry sets id back to the value last read from the device or
// loaded from the file. vebus.Device keeps that value but has no accessor
// for it, so a changed flag is simply flipped back, and a setting is
// reverted with Device.Revert, after which the other changes are put back.
func revertEntry(d *vebus.Device, id vebus.ID) error {
	if !d.Dirty(id) {
		return nil
	}
	if id.Kind == vebus.Flag {
		on, err := d.Flag(id.Num)
		if err != nil {
			return err
		}
		return d.SetFlag(id.Num, !on)
	}
	keep := map[vebus.ID]uint16{}
	for _, c := range d.ChangedIDs() {
		if c != id {
			keep[c], _ = d.Raw(c)
		}
	}
	d.Revert()
	var errs []error
	for c, raw := range keep {
		if err := d.SetRaw(c, raw); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
		if v >= info.Min && v <= info.Max && !(e.Num == 10 && v == 3 && lithiumOn(d)) {
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
	if n.unit == "" {
		n.unit = settingUnits[e.Num]
	}
	switch n.conv {
	case convPercent:
		n.factor, n.unit = 100, "%"
	case convMilli:
		n.factor, n.unit = 1000, "mV/°C"
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

// numberEntryText is the text of numeric entry e's value in its entry: ""
// when it has none.
func numberEntryText(d *vebus.Device, e vebus.Entry) string {
	if d.NotApplicable(e.ID()) {
		return ""
	}
	v, err := d.Display(e.ID())
	if err != nil {
		return ""
	}
	n := newNumberView(d, e)
	return entryText(n.shown(v), n.decimals())
}

// rangeText is the caption under a numeric entry: its range, and the step
// when it is not one unit of the last decimal shown.
func (n numberView) rangeText() string {
	lo, hi, step, _ := n.limits()
	dec := n.decimals()
	a := args{"Min": formatNumber(lo, dec), "Max": n.text(hi)}
	if math.Abs(step*math.Pow10(dec)-1) > 1e-6 {
		a["Step"] = n.text(step)
		return tr("{{.Min}} – {{.Max}}, step {{.Step}}", a)
	}
	return tr("{{.Min}} – {{.Max}}", a)
}
