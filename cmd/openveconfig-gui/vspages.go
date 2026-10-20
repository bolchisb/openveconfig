package main

// The virtual switch pages "A: Set VS ON", "B: Set VS OFF" and "VS options",
// laid out as in the original tool: one line per condition, such as "when
// load higher than [8349] W for [-1] seconds", and the LED alarms in a group
// of their own. The labels, the words between the controls and the alarm
// choices on these lines are the original tool's on-screen words, kept word for word because installers
// know them (user decision of 2026-10-20, see docs/CLEAN-ROOM.md); the notes
// are this project's own words. Which settings make up a line and when a
// line is in use are from this project's behavioural spec of the settings
// tabs (reverse-engineered, #6): a delay of -1 (raw 0) turns its condition
// off, and a condition's level is used only while its delay is not off; the
// inverter period time is used while flag 34 or 35 is on, its DC voltage
// conditions while flag 35 is on.

import (
	"errors"
	"math"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// vsLaidOut are the virtual switch groups whose page vsCards lays out.
var vsLaidOut = []string{"On conditions", "Off conditions", "Options"}

// unitSample is measured for the width of the unit after a level, so that
// the words after it line up; it is not shown.
const unitSample = "W"

// vsCards returns the cards of the virtual switch page of group for d and
// adds the rows of its entries to p.rows. A line is left out unless all its
// settings are supported, a card without lines too.
func (p *settingsPage) vsCards(d *vebus.Device, group string) []fyne.CanvasObject {
	b := &vsBuilder{p: p, d: d, bare: &formColumns{}, notes: map[fyne.CanvasObject]bool{}}
	// The original's words, as all labels of the lines below.
	sec, minutes := tr("seconds"), tr("minutes")
	var cards []fyne.CanvasObject
	switch group {
	case "On conditions":
		cards = []fyne.CanvasObject{
			b.card("",
				b.cond(tr("When load higher than"), 16, 19, sec, nil),
				b.cond(tr("When Udc lower than"), 18, 21, sec, nil),
				b.cond(tr("When Udc higher than"), 17, 20, sec, nil),
				b.cond(tr("When not charging for"), 0, 22, sec, nil),
				b.cond(tr("When fan on for"), 0, 23, sec, nil),
				b.check(16, tr("Set VS on when bulk protection is activated"), tr("(charger stopped after 10Hr bulk)"), nil),
				b.check(25, tr("Set VS on when general system failure occurs"), "", nil)),
			b.card(tr("When the following LED alarms are active"),
				b.alarm(17, 24), b.alarm(18, 25), b.alarm(19, 26), b.alarm(20, 27)),
		}
	case "Off conditions":
		cards = []fyne.CanvasObject{
			b.card("",
				b.cond(tr("When load lower than"), 28, 31, sec, nil),
				b.cond(tr("When Udc lower than"), 30, 33, sec, nil),
				b.cond(tr("When Udc higher than"), 29, 32, sec, nil),
				b.cond(tr("When charging for"), 0, 34, sec, nil),
				b.cond(tr("When fan off for"), 0, 35, sec, nil),
				b.cond(tr("When bulk charge finished for"), 0, 36, minutes, nil),
				b.cond(tr("When no VS ON condition for"), 0, 37, minutes, nil),
				b.cond(tr("When no AC input for"), 0, 38, sec, nil)),
			b.card(tr("When the following LED alarms are IN-active"),
				b.alarm(21, 39), b.alarm(22, 40), b.alarm(23, 41), b.alarm(24, 42)),
		}
	case "Options":
		period := func() bool { return flagOn(d, 34) || flagOn(d, 35) }
		shift := func() bool { return flagOn(d, 35) }
		higher := b.cond(tr("When Udc higher than"), 66, 67, sec, shift)
		lower := b.cond(tr("When Udc lower than"), 68, 69, sec, shift)
		var back, compensated fyne.CanvasObject
		if lower != nil {
			back = b.normalPeriod(shift)
		}
		if higher != nil || lower != nil {
			compensated = b.note(tr("The Udc levels of these conditions are temperature compensated."))
		}
		// In the dedicated usages the first switch is not used: its
		// minimum on time is not shown and the period time follows Udc
		// only (reverse-engineered, #6).
		firstUsed := func() bool {
			usage, err := d.Raw(vebus.SettingID(15))
			return err != nil || !dedicatedUsage(usage)
		}
		minimum := b.card("",
			b.value(tr("Do not switch off within"), 43, tr("minutes from switch on.")),
			b.note(tr("Switch-off conditions with a delay of 0 do not wait for this minimum time.")))
		if minimum != nil {
			b.p.refreshers = append(b.p.refreshers, func() {
				if firstUsed() {
					minimum.Show()
				} else {
					minimum.Hide()
				}
			})
		}
		cards = []fyne.CanvasObject{
			minimum,
			b.card("",
				b.check(34, tr("Change inverter period time when virtual switch is on"), "", firstUsed),
				b.check(35, tr("Change inverter period time on Udc"), "", nil),
				b.period(period), higher, back, lower, compensated),
		}
	}
	var shown []fyne.CanvasObject
	for _, c := range cards {
		if c != nil {
			shown = append(shown, c)
		}
	}
	if group == "Options" && len(shown) > 0 {
		invert := b.note(tr("Inverting the virtual switch is set on the Usage page."))
		b.p.refreshers = append(b.p.refreshers, func() {
			usage, err := d.Raw(vebus.SettingID(15))
			setShown(invert, err == nil && usageInvertible(usage))
		})
		shown = append([]fyne.CanvasObject{invert}, shown...)
	}
	b.fit()
	return shown
}

// flagOn reports whether flag n of d is supported and set.
func flagOn(d *vebus.Device, n uint16) bool {
	on, err := d.Flag(n)
	return err == nil && on
}

// vsBuilder builds the lines of a virtual switch page.
type vsBuilder struct {
	p *settingsPage
	d *vebus.Device
	// bare are the columns of the lines without a label: their controls
	// start where the labels do.
	bare *formColumns
	// labels and labelled are the labels and the lines with one, which
	// p.cols is fitted to.
	labels   []string
	labelled []*vsLine
	// notes are the notes made by note, which do not make a card.
	notes map[fyne.CanvasObject]bool
}

// vsLine is a line of a virtual switch page: a label and the controls of
// one or more entries, with words between them. It shows one changed dot,
// the reset and password markers and the errors of all its entries, and
// one revert button that reverts them all.
type vsLine struct {
	*formRow
	d       *vebus.Device
	rows    []*settingRow  // the entries on the line
	parts   []*formRow     // the rows that hold the errors of its number entries
	entries []*numberEntry // its number entries
	// on reports whether the line is in use; nil for always. While it is
	// not, all its controls are disabled.
	on func() bool
	// delay is the delay of a condition: while it is off (raw 0), level
	// and levelText are disabled. nil for a line without one.
	delay     *settingRow
	level     []fyne.Disableable // the controls used only with the delay on
	always    []fyne.Disableable // the controls used while the line is
	levelText []*widget.Label    // the texts muted with level
	lineText  []*widget.Label    // the texts muted with always
}

// supported reports whether d supports all ids.
func (b *vsBuilder) supported(ids ...vebus.ID) bool {
	for _, id := range ids {
		if !b.d.Supported(id) {
			return false
		}
	}
	return true
}

// set applies an edit made with a control and shows its result.
func (b *vsBuilder) set(err error) {
	u := b.p.u
	if u.settings.dev != b.d {
		return
	}
	if err != nil {
		showError(u, err)
	}
	u.settings.changed()
}

// add puts r on l and on the page.
func (b *vsBuilder) add(l *vsLine, r *settingRow) {
	r.line = l
	l.rows = append(l.rows, r)
	if r.entry != nil {
		l.parts = append(l.parts, r.formRow)
		l.entries = append(l.entries, r.entry)
	}
	b.p.rows = append(b.p.rows, r)
}

// number returns the row of numeric setting n on l: an entry bound to the
// model.
func (b *vsBuilder) number(l *vsLine, n uint16) *settingRow {
	u, d := b.p.u, b.d
	e, _ := d.Catalogue().Entry(vebus.SettingID(n))
	r := &settingRow{e: e, entry: newNumberEntry()}
	r.focus = r.entry
	r.formRow = newRowParts(b.p.cols, "", r.entry, "")
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
		placeholder := ""
		if d.NotApplicable(e.ID()) {
			placeholder = tr("not applicable")
		}
		if r.entry.PlaceHolder != placeholder {
			r.entry.SetPlaceHolder(placeholder)
		}
		show()
	}
	b.add(l, r)
	return r
}

// word returns a text of l between its controls, muted while the line is
// not in use.
func (b *vsBuilder) word(l *vsLine, text string) *widget.Label {
	w := widget.NewLabel(text)
	l.lineText = append(l.lineText, w)
	return w
}

// numberBox returns e at the width of a number.
func numberBox(e *numberEntry) fyne.CanvasObject { return container.New(fixedWidth(numberWidth), e) }

// cond returns the line of a condition: "label [level] unit for [delay]
// word", or "label [delay] word" without a level (0). While the delay is
// off the label and the level are disabled; while on reports false (nil:
// never), the whole line is.
func (b *vsBuilder) cond(label string, level, delay uint16, word string, on func() bool) fyne.CanvasObject {
	ids := []vebus.ID{vebus.SettingID(delay)}
	if level != 0 {
		ids = append(ids, vebus.SettingID(level))
	}
	if !b.supported(ids...) {
		return nil
	}
	l := &vsLine{d: b.d, on: on}
	var parts []fyne.CanvasObject
	if level != 0 {
		r := b.number(l, level)
		unit := widget.NewLabel(newNumberView(b.d, r.e).unit)
		th := theme.Current()
		w := fyne.MeasureText(unitSample, th.Size(theme.SizeNameText), fyne.TextStyle{}).Width + 2*th.Size(theme.SizeNameInnerPadding)
		parts = append(parts, numberBox(r.entry), container.New(fixedWidth(w), unit), b.word(l, tr("for")))
		l.level = append(l.level, r.entry)
		l.levelText = append(l.levelText, unit)
	}
	l.delay = b.number(l, delay)
	l.always = append(l.always, l.delay.entry)
	parts = append(parts, numberBox(l.delay.entry), b.word(l, word))
	return b.finish(l, label, b.p.cols, parts...)
}

// value returns the line "label [setting n] word".
func (b *vsBuilder) value(label string, n uint16, word string) fyne.CanvasObject {
	if !b.supported(vebus.SettingID(n)) {
		return nil
	}
	l := &vsLine{d: b.d}
	r := b.number(l, n)
	l.always = append(l.always, r.entry)
	return b.finish(l, label, b.p.cols, numberBox(r.entry), b.word(l, word))
}

// check returns the line of flag n: a check box labelled text, with more
// on a second line under the text when it is not "". While on reports
// false (nil: never) the check box is disabled.
func (b *vsBuilder) check(n uint16, text, more string, on func() bool) fyne.CanvasObject {
	if !b.supported(vebus.FlagID(n)) {
		return nil
	}
	d := b.d
	l := &vsLine{d: d, on: on}
	check := widget.NewCheck(text, func(on bool) {
		if !b.p.updating {
			b.set(d.SetFlag(n, on))
		}
	})
	e, _ := d.Catalogue().Entry(vebus.FlagID(n))
	r := &settingRow{e: e, focus: check, formRow: newRowParts(b.bare, "", check, "")}
	r.update = func() { check.SetChecked(flagOn(d, n)) }
	b.add(l, r)
	l.always = append(l.always, check)
	var main fyne.CanvasObject = check
	if more != "" {
		// Under the check box's text: the icon and its border are left
		// of it, the label's inner padding is inside the label.
		th := theme.Current()
		indent := th.Size(theme.SizeNameInlineIcon) + 2*th.Size(theme.SizeNameInputBorder)
		gap := -2 * th.Size(theme.SizeNameInnerPadding)
		main = container.New(layout.NewCustomPaddedVBoxLayout(gap), check,
			container.New(layout.NewCustomPaddedLayout(0, 0, indent, 0), widget.NewLabel(more)))
	}
	return b.finish(l, "", b.bare, main)
}

// alarm returns the line of an LED alarm: "[alarm or pre-alarm, flag] for
// [delay] seconds". While the delay is off, the choice is disabled.
func (b *vsBuilder) alarm(flag, delay uint16) fyne.CanvasObject {
	if !b.supported(vebus.FlagID(flag), vebus.SettingID(delay)) {
		return nil
	}
	d := b.d
	l := &vsLine{d: d}
	opts := flagChoices()[flag]
	sel := widget.NewSelect(opts[:], nil)
	sel.OnChanged = func(string) {
		if i := sel.SelectedIndex(); !b.p.updating && i >= 0 {
			b.set(d.SetFlag(flag, i == 1))
		}
	}
	e, _ := d.Catalogue().Entry(vebus.FlagID(flag))
	r := &settingRow{e: e, focus: sel, formRow: newRowParts(b.bare, "", sel, "")}
	r.update = func() { sel.SetSelectedIndex(boolIndex(flagOn(d, flag))) }
	b.add(l, r)
	l.delay = b.number(l, delay)
	l.level = append(l.level, sel)
	l.always = append(l.always, l.delay.entry)
	var all []string
	for n := uint16(17); n <= 20; n++ {
		c := flagChoices()[n]
		all = append(all, c[:]...)
	}
	return b.finish(l, "", b.bare, container.New(fixedWidth(selectWidth(all)), sel),
		b.word(l, tr("for")), numberBox(l.delay.entry), b.word(l, tr("seconds")))
}

// period returns the line of the inverter period time (setting 62), in ms
// and as the frequency it makes, which can be entered too; in use while on
// reports true.
func (b *vsBuilder) period(on func() bool) fyne.CanvasObject {
	if !b.supported(vebus.SettingID(62)) {
		return nil
	}
	u, d := b.p.u, b.d
	l := &vsLine{d: d, on: on}
	r := b.number(l, 62)
	hz := newNumberEntry()
	part := newRowParts(b.p.cols, "", hz, "")
	show := part.bindNumber(hz, func() string { return periodHzText(d) }, func(text string) error {
		if u.settings.dev != d {
			return nil
		}
		err := setPeriodHz(d, r.e, text)
		if err == nil {
			u.settings.changed()
		}
		return err
	})
	b.p.refreshers = append(b.p.refreshers, show) // before the line's, which shows its error
	l.parts = append(l.parts, part)
	l.entries = append(l.entries, hz)
	l.always = append(l.always, r.entry, hz)
	return b.finish(l, tr("Make period time"), b.p.cols,
		numberBox(r.entry), b.word(l, tr("ms (=")), numberBox(hz), b.word(l, tr("Hz)")))
}

// periodHzText is the frequency of the inverter period time (setting 62,
// in ms) as the text of its entry; "" without one.
func periodHzText(d *vebus.Device) string {
	v, err := d.Display(vebus.SettingID(62))
	if err != nil || v <= 0 {
		return ""
	}
	return entryText(1000/v, 2)
}

// setPeriodHz sets the inverter period time e (setting 62, in ms) to the
// period of the frequency text, in Hz, rounded to the nearest value the
// device can store. The error is a message for the user.
func setPeriodHz(d *vebus.Device, e vebus.Entry, text string) error {
	lo, hi, _, _ := newNumberView(d, e).limits()
	if lo <= 0 { // no frequency for a period of 0
		return errors.New(tr("Enter the period time in ms."))
	}
	// The frequencies of the longest and the shortest period, as shown.
	minHz, maxHz := math.Round(1e5/hi)/100, math.Round(1e5/lo)/100
	hz, err := parseNumber(text)
	if err != nil {
		return errors.New(tr("Enter a number."))
	}
	if hz < minHz || hz > maxHz {
		return errors.New(tr("Enter a value from {{.Min}} to {{.Max}}.",
			args{"Min": withUnit(formatNumber(minHz, 2), "Hz"), "Max": withUnit(formatNumber(maxHz, 2), "Hz")}))
	}
	info, _ := d.SettingInfo(e.Num)
	raw, err := info.Raw(1000 / hz)
	if err != nil {
		return err
	}
	// A frequency shown as in range may round to just outside it.
	return d.SetRaw(e.ID(), min(max(raw, info.Min), info.Max))
}

// normalPeriod returns the text that says to which period the inverter
// returns: the normal one of the system frequency (flag 2). It is muted
// while on reports false.
func (b *vsBuilder) normalPeriod(on func() bool) fyne.CanvasObject {
	t := widget.NewLabel("")
	t.Wrapping = fyne.TextWrapWord
	b.p.refreshers = append(b.p.refreshers, func() {
		text := tr("Back to the normal period for the system frequency")
		if sixty, err := b.d.Flag(2); err == nil {
			f := 50.0
			if sixty {
				f = 60
			}
			text = tr("Make period time {{.Period}} ms (={{.Frequency}} Hz) again",
				args{"Period": formatNumber(1000/f, 3), "Frequency": formatNumber(f, 0)})
		}
		if t.Text != text {
			t.SetText(text)
		}
		setMuted(t, !on())
	})
	return container.New(layout.NewCustomPaddedLayout(0, 0, dotSize+theme.Padding(), 0), t)
}

// note returns a muted note in the label column.
func (b *vsBuilder) note(text string) fyne.CanvasObject {
	t := captionText(text, theme.ColorNamePlaceHolder)
	t.Wrapping = fyne.TextWrapWord
	o := container.New(layout.NewCustomPaddedLayout(0, 0, dotSize+theme.Padding(), 0), t)
	b.notes[o] = true
	return o
}

// card returns a card of items, titled title unless it is ""; nil when
// items has no line.
func (b *vsBuilder) card(title string, items ...fyne.CanvasObject) fyne.CanvasObject {
	var objs []fyne.CanvasObject
	lines := 0
	for _, o := range items {
		if o == nil {
			continue
		}
		if !b.notes[o] {
			lines++
		}
		objs = append(objs, o)
	}
	if lines == 0 {
		return nil
	}
	if title != "" {
		return newFormCard(cardTitle(title), objs...)
	}
	return newFormCard(objs[0], objs[1:]...)
}

// finish lays l out: label (none when "") in the label column of cols,
// then parts; and makes its revert button revert all its entries.
func (b *vsBuilder) finish(l *vsLine, label string, cols *formColumns, parts ...fyne.CanvasObject) fyne.CanvasObject {
	d := b.d
	l.formRow = newFormRow(cols, label, "", container.NewHBox(parts...), 0, "")
	if label == "" {
		l.label.Hide()
	} else {
		l.levelText = append(l.levelText, l.label)
		b.labels = append(b.labels, label)
		b.labelled = append(b.labelled, l)
	}
	for _, r := range l.rows {
		r.root = l.root
	}
	for _, f := range l.parts {
		f.root = l.root
	}
	// A refused text, or Escape, changes the errors of the line.
	for _, e := range l.entries {
		commit, restore := e.commit, e.restore
		e.commit = func(text string) { commit(text); l.refresh() }
		e.restore = func() { restore(); l.refresh() }
	}
	l.revert.OnTapped = func() {
		var errs []error
		for _, r := range l.rows {
			errs = append(errs, revertEntry(d, r.e.ID()))
		}
		for _, e := range l.entries {
			e.restore()
		}
		b.set(errors.Join(errs...))
	}
	b.p.refreshers = append(b.p.refreshers, l.update)
	return l.root
}

// fit sets the label column of the page to its labels, and the control
// column to the widest line, so that the markers and revert buttons line up.
func (b *vsBuilder) fit() {
	c := b.p.cols
	c.fit(b.labels)
	c.control = 0
	for _, l := range b.labelled {
		c.control = max(c.control, l.main.MinSize().Width)
	}
	b.bare.control = c.label + c.controlWidth()
}

// update enables the controls of l as its use allows and shows its marks.
func (l *vsLine) update() {
	lineOn := l.on == nil || l.on()
	levelOn := lineOn
	if l.delay != nil {
		raw, err := l.d.Raw(l.delay.e.ID())
		levelOn = lineOn && err == nil && raw != 0
	}
	for _, c := range l.level {
		setDisabled(c, !levelOn)
	}
	for _, c := range l.always {
		setDisabled(c, !lineOn)
	}
	for _, t := range l.levelText {
		setMuted(t, !levelOn)
	}
	for _, t := range l.lineText {
		setMuted(t, !lineOn)
	}
	l.refresh()
}

// refresh shows whether an entry of l was changed, its markers and the
// errors of its entries.
func (l *vsLine) refresh() {
	var changed, reset, password bool
	for _, r := range l.rows {
		id := r.e.ID()
		changed = changed || l.d.Dirty(id)
		reset = reset || l.d.ResetRequired(id)
		a, ok := l.d.Access(id)
		password = password || ok && a.PasswordRequired()
	}
	l.setChanged(changed)
	l.setMarkers(reset, password)
	var msgs []string
	for _, f := range l.parts {
		if f.invalid {
			msgs = append(msgs, f.errText.String())
		}
	}
	l.setError(strings.Join(msgs, " "))
}

// setDisabled disables or enables c.
func setDisabled(c fyne.Disableable, off bool) {
	if c.Disabled() != off {
		if off {
			c.Disable()
		} else {
			c.Enable()
		}
	}
}

// setMuted shows t muted or not.
func setMuted(t *widget.Label, muted bool) {
	imp := widget.MediumImportance
	if muted {
		imp = widget.LowImportance
	}
	if t.Importance != imp {
		t.Importance = imp
		t.Refresh()
	}
}
