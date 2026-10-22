package main

// The preview of an assistant's questions (#33, in part): a dialog that
// walks through the steps of an assistant from the user's package folder
// with the wizard of pkg/assistant/wizard, and shows the summary at the
// end; the same dialog walks through an assistant of the open file with
// the file's values (Start assistant), and the Summary dialog lists those
// values. They write nothing: what they would build is not yet the same as
// VEConfigure's code (#32). The step texts are the package's own, read at
// run time from the user's packages.

import (
	"context"
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// assistantLabels returns the environment the assistant sources read
// (assistant.Labels): the firmware, the named settings and flags of the
// loaded model, the unit's values worked out from that model
// (Device.FromSettings), and the package folder's index. With no model
// loaded the unit's values are left out rather than computed from zeros:
// an assistant that reads one then stops with an unknown label instead of
// being given a wrong number.
func assistantLabels(u *ui, dir string) map[string]asm.Value {
	var dev assistant.Device
	dev.PackageVersion, dev.DownloadedBy, _ = vap.ReadIndex(dir)
	d := u.settings.dev
	if d == nil {
		l := assistant.Labels(dev)
		for _, k := range []string{"cMultiVersion", "cPNominal", "cIInvNominal", "hwDC", "hwAC", "cWinmonIDOffset"} {
			delete(l, k)
		}
		return l
	}
	{
		dev.MultiVersion = int64(d.Firmware)
		if f := u.settings.file; f != nil {
			dev.VersionExtra = f.unit.VersionExtra
		}
		dev.FromSettings(func(id int) (mk2.SettingInfo, bool) {
			if id < 0 || id > math.MaxUint16 {
				return mk2.SettingInfo{}, false
			}
			info, ok := d.SettingInfo(uint16(id))
			return info, ok && info.Sc != 0
		})
		for n := 0; n < assistant.FlagCount; n++ {
			name, named := assistant.FlagName(n)
			if !named || !d.Supported(vebus.FlagID(uint16(n))) {
				continue
			}
			v, err := d.Flag(uint16(n))
			def, derr := d.Default(vebus.FlagID(uint16(n)))
			if err != nil || derr != nil {
				continue
			}
			dev.Flags = append(dev.Flags, assistant.DeviceFlag{Name: name, Default: def == 1, Value: v})
		}
		for _, id := range d.SettingIDs() {
			name, named := assistant.SettingName(int(id))
			info, ok := d.SettingInfo(id)
			raw, err := d.Raw(vebus.SettingID(id))
			if !named || !ok || err != nil {
				continue
			}
			dev.Settings = append(dev.Settings, assistant.DeviceSetting{ID: int(id), Name: name, Scale: int64(info.Sc),
				Offset: int64(info.Offset), Default: int64(info.Default), Min: int64(info.Min), Max: int64(info.Max), Value: int64(raw)})
		}
	}
	l := assistant.Labels(dev)
	return l
}

// previewAssistant opens the preview of a, from the package folder dir.
func previewAssistant(u *ui, dir string, a availableAssistant) {
	runWithProgress(u, tr("Opening the assistant"), func(context.Context, func(done, total int)) error {
		w := &assistantWizard{u: u, title: a.title, n: 1, dir: dir, id: a.info.ID}
		lang := "en"
		if p, err := vap.Load(a.info.Path); err == nil {
			lang = packageLanguage(p)
		}
		w.lang = lang
		c, err := assistant.Load(dir, lang, a.info.ID)
		var st *wizard.Step
		if err == nil {
			c.Labels = assistantLabels(u, dir)
			w.sess = c.Program().NewSession(nil)
			w.sess.Edit = a.info.Name
			st, err = w.sess.Start()
		}
		fyne.Do(func() { w.show(st, err) })
		return nil
	})
}

// assistantWizard is the preview dialog of one assistant.
type assistantWizard struct {
	u     *ui
	title string
	// dir, id and lang name the package being previewed, so its summary
	// can put it into the file; dir is "" for an assistant opened from
	// the file, which is in it already.
	dir  string
	id   uint16
	lang string
	sess *wizard.Session // nil when the assistant could not be loaded
	dlg  *dialog.CustomDialog
	body *fyne.Container
	// n is the number of the step shown, from 1.
	n                  int
	back, next, cancel *widget.Button
	// values returns the values entered at the step shown, or the message
	// of the first one that is not valid; nil at a step without input.
	values  func() ([]asm.Value, bool)
	done    bool // the summary is shown
	stopped bool // the assistant stopped at the step it was given
	// adds says that the summary's button puts the assistant into the
	// open file, rather than only closing the dialog.
	adds bool
	// readErr is why the file's values could not be read, for an
	// assistant of the file; its steps then offer their defaults.
	readErr error
}

// show opens the dialog at st, or with err.
func (w *assistantWizard) show(st *wizard.Step, err error) {
	w.body = container.NewStack()
	w.cancel = widget.NewButtonWithIcon(tr("Cancel"), theme.CancelIcon(), func() { w.dlg.Hide() })
	w.back = widget.NewButtonWithIcon(tr("Back"), theme.NavigateBackIcon(), w.goBack)
	w.next = widget.NewButtonWithIcon(tr("Next"), theme.NavigateNextIcon(), w.goNext)
	w.next.Importance = widget.HighImportance
	w.next.IconPlacement = widget.ButtonIconTrailingText
	scroll := container.NewVScroll(w.body)
	scroll.SetMinSize(fyne.NewSize(780, 620))
	d := dialog.NewCustomWithoutButtons(w.title, scroll, w.u.win)
	d.SetButtons([]fyne.CanvasObject{w.cancel, w.back, w.next})
	w.dlg = d
	w.stepTo(st, err)
	showWithKeys(w.u, d, func() {
		if !w.next.Disabled() {
			w.next.OnTapped()
		}
	})
}

// goNext answers the step shown, or, after the summary, puts the
// assistant into the file and closes the dialog; Cancel is the way out
// without it.
func (w *assistantWizard) goNext() {
	if w.done {
		if w.adds {
			w.addToFile()
			return
		}
		w.dlg.Hide()
		return
	}
	var vals []asm.Value
	if w.values != nil {
		v, ok := w.values()
		if !ok {
			return
		}
		vals = v
	}
	st, err := w.sess.Next(vals)
	if err == nil {
		w.n++
	}
	w.stepTo(st, err)
}

// goBack returns to the step answered last, or to the step whose answer
// made the assistant stop.
func (w *assistantWizard) goBack() {
	if w.stopped && w.sess.Current() != nil {
		w.stepTo(w.sess.Current(), nil)
		return
	}
	st, err := w.sess.Back()
	if err == nil {
		w.n--
	}
	w.stepTo(st, err)
}

// stepTo shows st, the summary when st is nil, or why the assistant
// stopped when err is not nil.
func (w *assistantWizard) stepTo(st *wizard.Step, err error) {
	w.values, w.done, w.stopped, w.adds = nil, false, err != nil, false
	w.next.SetText(tr("Next"))
	w.next.SetIcon(theme.NavigateNextIcon())
	w.next.IconPlacement = widget.ButtonIconTrailingText
	w.next.Enable()
	w.back.Disable()
	if w.sess != nil && (w.n > 1 || w.stopped && w.sess.Current() != nil) {
		w.back.Enable()
	}
	var view fyne.CanvasObject
	switch {
	case err != nil:
		view = w.stopView(err)
		w.next.Disable()
	case st == nil:
		// summaryView says whether the assistant can be put into the
		// file, which is what the dialog's own button then does.
		view = w.summaryView()
		w.done = true
		w.next.SetText(tr("Close"))
		w.next.SetIcon(theme.ConfirmIcon())
		if w.adds {
			w.next.SetText(tr("Add to the file"))
			w.next.SetIcon(theme.ContentAddIcon())
			w.next.IconPlacement = widget.ButtonIconLeadingText
		}
	default:
		view = w.stepView(st)
		if st.Directive == ".AER" {
			w.next.Disable()
		}
	}
	w.body.Objects = []fyne.CanvasObject{container.New(layout.NewCustomPaddedLayout(8, 8, 12, 12), view)}
	w.body.Refresh()
	// The dialog lays its buttons out in a grid of cells measured when
	// they were set, so a label that grew with the step would run over
	// its neighbour; setting the row again measures it afresh.
	if w.dlg != nil {
		w.dlg.SetButtons([]fyne.CanvasObject{w.cancel, w.back, w.next})
	}
}

// stepHead is the number of the step and the step's own name.
// title is the heading the package gives the step; "" when its section
// is missing, and then the step's own name stands in beside the number.
func (w *assistantWizard) stepHead(st *wizard.Step, title string) fyne.CanvasObject {
	n := strconv.Itoa(max(w.n, 1))
	if title == "" {
		return captionText(tr("Step {{.Number}} · {{.Name}}", args{"Number": n, "Name": st.Name}), theme.ColorNamePlaceHolder)
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(0),
		captionText(tr("Step {{.Number}}", args{"Number": n}), theme.ColorNamePlaceHolder), cardTitle(title))
}

// textValues are the values the step's text puts in its %N:s places:
// the labels that are neither the choice nor a field, such as the ends
// of a link. The places are numbered over those alone, so a step that
// asks for values must not offer them its numbers (#33).
func textValues(st *wizard.Step) []asm.Value {
	k := len(st.Formats)
	if st.Type != 'E' {
		k++
	}
	if k >= len(st.Values) {
		return nil
	}
	return st.Values[k:]
}

// wizardStepText is a package's step text, with the markup it carries
// worked out: the places filled with the step's values, bold where the
// package asks for it, and links as links.
func wizardStepText(text string, values []asm.Value) fyne.CanvasObject {
	if strings.TrimSpace(wizard.PlainText(text, values)) == "" {
		return mutedText(tr("The assistant package has no text for this step."))
	}
	return richAssistantText(text, values)
}

// wizardChoiceLabels returns what the buttons of st say: the first line of each
// of the package's choices (wizard.Choices), plain. It falls back to
// numbers when the package does not give one per button, or gives one
// that is empty or says the same as another, since the list tells them
// apart by their text.
func wizardChoiceLabels(st *wizard.Step, choices []string) []string {
	labels := make([]string, st.Buttons)
	for i := range labels {
		labels[i] = tr("Choice {{.Number}}", args{"Number": strconv.Itoa(i + 1)})
	}
	if len(choices) != st.Buttons {
		return labels
	}
	named := make([]string, st.Buttons)
	for i, c := range choices {
		first, _, _ := strings.Cut(c, "\n")
		named[i] = strings.TrimSpace(wizard.PlainText(first, nil))
		if named[i] == "" || slices.Contains(named[:i], named[i]) {
			return labels
		}
	}
	return named
}

// wizardChoiceNotes returns the lines a package writes under a choice to
// explain it, which the buttons themselves have no room for, each under
// the label of the choice it belongs to.
func wizardChoiceNotes(labels, choices []string) []fyne.CanvasObject {
	var out []fyne.CanvasObject
	for i, c := range choices {
		_, rest, found := strings.Cut(c, "\n")
		if !found || strings.TrimSpace(wizard.PlainText(rest, nil)) == "" {
			continue
		}
		out = append(out, mutedText(tr("{{.Choice}}: {{.Note}}",
			args{"Choice": labels[i], "Note": strings.TrimSpace(wizard.PlainText(rest, nil))})))
	}
	return out
}

// assistantMessage is the text an .AER or .AIP step carries in its
// values: the package's own words, markup and all.
func assistantMessage(values []asm.Value) string {
	var parts []string
	keep := func(s string) {
		if strings.TrimSpace(s) != "" {
			parts = append(parts, s)
		}
	}
	for _, v := range values {
		switch v.K {
		case asm.List:
			for _, e := range v.L {
				if e.K == asm.Str {
					keep(e.S)
				}
			}
		case asm.Str:
			keep(v.S)
		}
	}
	return strings.Join(parts, "\n")
}

// richAssistantText shows a package's text with its markup worked out.
func richAssistantText(text string, values []asm.Value) fyne.CanvasObject {
	spans := wizard.Spans(text, values)
	rt := widget.NewRichText()
	rt.Wrapping = fyne.TextWrapWord
	for _, s := range spans {
		if s.Link != "" {
			if u, err := url.Parse(s.Link); err == nil {
				rt.Segments = append(rt.Segments, &widget.HyperlinkSegment{Text: s.Text, URL: u})
				continue
			}
		}
		rt.Segments = append(rt.Segments, &widget.TextSegment{Text: s.Text,
			Style: widget.RichTextStyle{Inline: true, TextStyle: fyne.TextStyle{Bold: s.Bold}}})
	}
	rt.Refresh()
	return rt
}

// stepView shows a step: its text and its inputs, by type.
func (w *assistantWizard) stepView(st *wizard.Step) fyne.CanvasObject {
	// A step keeps what it asks for in its own text: a step with buttons
	// one dotted line per button, a step with values one dotted block
	// whose places name the fields. Both are taken out of the text, so
	// what is left of it reads as prose and nothing is said twice.
	vals := textValues(st)
	text, choices := st.Text, []string(nil)
	var entry []wizard.EntryLine
	if st.Type == 'E' {
		// Only a step that has fields writes places for them: in one
		// that has none the dotted line is prose like the rest, and the
		// text stays whole so its paragraphs keep their blank lines.
		if len(st.Formats) > 0 {
			text, entry = wizard.Entry(st.Text)
		}
	} else {
		text, choices = wizard.Choices(st.Text, st.Buttons)
	}
	title, text := wizard.Title(text, vals)
	box := container.NewVBox(w.stepHead(st, title))
	if title == "" || strings.TrimSpace(wizard.PlainText(text, vals)) != "" {
		box.Add(wizardStepText(text, vals))
	}
	if w.readErr != nil && w.n == 1 {
		box.Objects = append([]fyne.CanvasObject{callout(theme.InfoIcon(),
			tr("The file's values of this assistant cannot be read, so its steps offer their defaults."))}, box.Objects...)
	}
	switch st.Directive {
	case ".AER":
		// When the assistant says what is wrong, its own words stand:
		// what it asks for is often a setting of the unit, not an answer
		// to go back and change.
		if msg := assistantMessage(st.Values); msg != "" {
			box.Add(richAssistantText(msg, nil))
			box.Add(callout(theme.NewErrorThemedResource(theme.ErrorIcon()),
				tr("This is the assistant's own message, not a limitation of {{.App}}. Nothing was written.",
					args{"App": appName})))
			return box
		}
		box.Add(callout(theme.NewErrorThemedResource(theme.ErrorIcon()),
			tr("The assistant does not accept the answers given so far. Go back and change them.")))
		return box
	case ".AIP":
		if len(st.Values) > 0 {
			box.Add(mutedText(tr("Values shown: {{.Values}}", args{"Values": valueList(st.Values)})))
		}
		return box
	}
	var inputs []func() (asm.Value, bool)
	fields := map[int]*valueField{}
	for i := range st.Labels {
		val := asm.Value{}
		if i < len(st.Values) {
			val = st.Values[i]
		}
		if st.Type != 'E' && i == 0 {
			opts := wizardChoiceLabels(st, choices)
			radio := widget.NewRadioGroup(opts, nil)
			radio.Required = true
			if n, err := val.AsInt(); err == nil && n >= 0 && int(n) < len(opts) {
				radio.SetSelected(opts[n])
			}
			box.Add(radio)
			for _, note := range wizardChoiceNotes(opts, choices) {
				box.Add(note)
			}
			inputs = append(inputs, func() (asm.Value, bool) {
				return asm.IntVal(int64(max(slices.Index(opts, radio.Selected), 0))), true
			})
			continue
		}
		fi := i
		if st.Type != 'E' {
			fi = i - 1
		}
		// Only a label with a format descriptor is a field. The rest are
		// values the step's text puts in its places, such as the two ends
		// of a link: they are not asked for, and go back as they came so
		// that the step still gets one value per label.
		if fi >= len(st.Formats) {
			keep := val
			inputs = append(inputs, func() (asm.Value, bool) { return keep, true })
			continue
		}
		vf := newValueField(&st.Formats[fi], val)
		fields[fi] = vf
		inputs = append(inputs, vf.get)
	}
	for _, o := range entryBlock(entry, fields, vals) {
		box.Add(o)
	}
	if len(inputs) > 0 {
		w.values = func() ([]asm.Value, bool) {
			vals := make([]asm.Value, len(inputs))
			ok := true
			for i, get := range inputs {
				var good bool
				vals[i], good = get()
				ok = ok && good
			}
			return vals, ok
		}
	}
	return box
}

// valueField is one value a step asks for: its entry, the row it sits
// in, and whether the step's text found a place for it.
type valueField struct {
	f      *wizard.Format
	entry  *numberEntry
	row    *formRow
	label  string // what the step's text calls it
	unit   string // what the text writes after it
	placed bool
	get    func() (asm.Value, bool)
}

// newValueField makes the field of format f (nil: a plain whole number)
// holding val. Its row is made later, once every label of the step is
// known and they can share a column. The field says what is wrong as
// soon as it is left, as the entries of the settings pages do, and
// Escape puts the value it was given back.
func newValueField(f *wizard.Format, val asm.Value) *valueField {
	v := &valueField{f: f, entry: newNumberEntry()}
	if raw, err := val.AsInt(); err == nil {
		v.entry.SetText(strconv.FormatInt(raw, 10))
		if f != nil {
			v.entry.SetText(f.Show(raw))
		}
	}
	given := v.entry.Text
	v.entry.restore = func() { v.entry.SetText(given) }
	v.get = func() (asm.Value, bool) {
		setErr := func(text string) {
			if v.row != nil {
				v.row.setError(text)
			}
		}
		if f == nil {
			n, err := strconv.ParseInt(strings.TrimSpace(v.entry.Text), 10, 64)
			if err != nil {
				setErr(tr("Enter a whole number."))
				return asm.Value{}, false
			}
			setErr("")
			return asm.IntVal(n), true
		}
		n, err := f.Parse(v.entry.Text)
		if err != nil {
			setErr(tr("Enter a number from {{.Min}} to {{.Max}}.", args{"Min": f.Show(f.Min), "Max": f.Show(f.Max)}))
			return asm.Value{}, false
		}
		setErr("")
		return asm.IntVal(n), true
	}
	v.entry.commit = func(string) { v.get() }
	return v
}

// makeRow puts the field in a row of the form, with the range it accepts
// under the entry as the settings pages show it.
func (v *valueField) makeRow(cols *formColumns) *formRow {
	unit := v.unit
	if unit == "" {
		unit = formatUnit(v.f)
	}
	v.row = newFormRow(cols, v.label, "", v.entry, numberWidth, unit)
	if v.f != nil {
		v.row.setCaption(tr("{{.Min}} … {{.Max}}", args{"Min": v.f.Show(v.f.Min), "Max": v.f.Show(v.f.Max)}))
	}
	return v.row
}

// entryWords trims what the package writes around a place down to a label
// and a unit: the words before a value often end in "=" or ":" and the
// ones after it in the full stop of the sentence they were part of,
// neither of which reads well beside a field.
func entryWords(s string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "=:."))
}

// entryBlock lays out the entry block of a step: its prose as prose, and
// its fields as rows of one form, their labels in a column of their own
// width. Fields the text found no place for follow, numbered as they
// were before a package's places were read.
func entryBlock(lines []wizard.EntryLine, fields map[int]*valueField, values []asm.Value) []fyne.CanvasObject {
	plain := func(s string) string { return strings.TrimSpace(wizard.PlainText(s, values)) }
	// What each field is called, and the order the text puts them in.
	var order []*valueField
	for _, l := range lines {
		if l.Prose() {
			continue
		}
		for i, n := range l.Places {
			f := fields[n]
			if f == nil || f.placed {
				continue
			}
			f.placed = true
			f.unit = entryWords(plain(l.Parts[i+1]))
			if i == 0 {
				f.label = entryWords(plain(l.Parts[0]))
			}
			order = append(order, f)
		}
	}
	var left []int
	for n, f := range fields {
		if !f.placed {
			left = append(left, n)
		}
	}
	slices.Sort(left)
	for _, n := range left {
		f := fields[n]
		if len(fields) > 1 {
			f.label = tr("Value {{.Number}}", args{"Number": strconv.Itoa(n + 1)})
		}
		order = append(order, f)
	}
	if len(order) == 0 {
		return nil
	}
	cols := &formColumns{}
	labels := make([]string, len(order))
	for i, f := range order {
		labels[i] = f.label
	}
	cols.fit(labels)

	// The prose of the block heads the card the fields sit in, so that
	// what the step asks reads as one thing.
	var head []fyne.CanvasObject
	var prose []string
	for _, l := range lines {
		if l.Prose() {
			prose = append(prose, l.Parts[0])
		}
	}
	if plain(strings.Join(prose, "\n")) != "" {
		head = append(head, richAssistantText(strings.Join(prose, "\n"), values))
	}
	rows := make([]fyne.CanvasObject, 0, len(order))
	for _, f := range order {
		rows = append(rows, f.makeRow(cols).root)
	}
	return []fyne.CanvasObject{newFormCard(nil, append(head, rows...)...)}
}

// formatUnit returns the unit of a format: V for a battery voltage (type
// B), else none (the units of the other types are not known).
func formatUnit(f *wizard.Format) string {
	if f != nil && strings.Contains(f.Type, "B") {
		return "V"
	}
	return ""
}

// valueList writes values as the step shows them.
func valueList(vals []asm.Value) string {
	s := make([]string, len(vals))
	for i, v := range vals {
		s[i] = v.String()
	}
	return strings.Join(s, ", ")
}

// summaryView shows the steps the assistant lists in its summary, with
// their answers, and that nothing is written.
func (w *assistantWizard) summaryView() fyne.CanvasObject {
	run, err := w.sess.Summary()
	if err != nil {
		return w.stopView(err)
	}
	head := widget.NewLabel(tr("Summary"))
	head.TextStyle.Bold = true
	rows := container.New(layout.NewFormLayout())
	for _, st := range run.Steps {
		if !st.Listed {
			continue
		}
		name := widget.NewLabelWithStyle(answerName(st), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		v := widget.NewLabel(answerText(st))
		v.Wrapping = fyne.TextWrapWord
		rows.Add(name)
		rows.Add(v)
	}
	box := container.NewVBox(head)
	if len(rows.Objects) == 0 {
		box.Add(mutedText(tr("The assistant lists no answers in its summary.")))
	} else {
		box.Add(rows)
	}
	// The summary has no button of its own: putting the assistant into
	// the file is what the walk was for, so it is the dialog's own
	// button, and Cancel is the way out without it.
	switch {
	case w.dir == "": // an assistant of the file: editing it comes later
		box.Add(callout(theme.InfoIcon(), tr("Nothing was written. Saving the answers of an assistant already in the "+
			"file comes once {{.App}} can replace one in place.", args{"App": appName})))
		return box
	case w.u.settings.file == nil:
		box.Add(callout(theme.InfoIcon(), tr("Nothing was written. Open a settings file to add an assistant to it.")))
		return box
	}
	w.adds = true
	box.Add(callout(theme.InfoIcon(), tr("The assistant is put into the open file. The file itself is written "+
		"when you save it.")))
	return box
}

// addToFile builds the file's assistants with this one after them and
// puts the result in the open file; the assistants already there keep
// their answers. The file on disk changes only when the user saves it.
func (w *assistantWizard) addToFile() {
	sf := w.u.settings.file
	if sf == nil || w.dir == "" || w.sess == nil {
		return
	}
	answers, dir, lang, id := w.sess.Answers(), w.dir, w.lang, w.id
	labels := assistantLabels(w.u, dir)
	runWithProgress(w.u, tr("Adding the assistant"), func(context.Context, func(done, total int)) error {
		img, err := sf.unit.AssistantImage()
		if err != nil {
			return &uiError{err: err, msg: tr("The file's assistants cannot be read: {{.Err}}", args{"Err": err})}
		}
		out, err := assistant.Add(dir, lang, img, id, labels, answers)
		if err != nil {
			return addError(err)
		}
		fyne.Do(func() {
			sf.unit.SetAssistantImage(out.Bytes)
			w.u.settings.changed()
			w.dlg.Hide()
			showMessage(w.u, tr("Assistant added"),
				tr("{{.Name}} is in the open file, which uses {{.Used}} of {{.Total}} bytes of assistant memory. Save the file to write it.",
					args{"Name": w.title, "Used": out.Used, "Total": assistant.Budget}))
		})
		return nil
	})
}

// addError says why an assistant could not be added.
func addError(err error) error {
	switch {
	case errors.Is(err, assistant.ErrTooBig):
		return &uiError{err: err, msg: tr("The assistants do not fit in the unit's assistant memory. Nothing was changed.")}
	case errors.Is(err, assistant.ErrAlreadyThere):
		return &uiError{err: err, msg: tr("This assistant is already in the file. Nothing was changed.")}
	}
	return &uiError{err: err, msg: tr("The assistant cannot be built yet: {{.Err}}. Nothing was changed.", args{"Err": err})}
}

// showInstalledSummary shows the values of the file's assistant name, as
// its summary lists them, from run, the read-back of the file's area; err
// is why the read-back failed.
func showInstalledSummary(u *ui, title, name string, run *wizard.Run, err error) {
	var body fyne.CanvasObject
	if err != nil || run == nil {
		msg := tr("The file's values of this assistant cannot be read.")
		if err != nil {
			msg = tr("The file's values of this assistant cannot be read: {{.Err}}", args{"Err": err})
		}
		t := widget.NewLabel(msg)
		t.Wrapping = fyne.TextWrapWord
		body = container.NewVBox(container.NewBorder(nil, nil, widget.NewIcon(theme.InfoIcon()), nil, t),
			mutedText(tr("This is a known limitation of this version of {{.App}}: its assistant compiler is not complete yet.", args{"App": appName})))
	} else {
		rows := container.New(layout.NewFormLayout())
		for _, st := range run.Steps {
			if st.Source != name || !st.Listed {
				continue
			}
			v := widget.NewLabel(answerText(st))
			v.Wrapping = fyne.TextWrapWord
			rows.Add(widget.NewLabelWithStyle(answerName(st), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
			rows.Add(v)
		}
		body = rows
		if len(rows.Objects) == 0 {
			body = mutedText(tr("The assistant lists no answers in its summary."))
		}
	}
	head := widget.NewLabelWithStyle(tr("Summary"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	scroll := container.NewVScroll(container.NewVBox(head, body))
	scroll.SetMinSize(fyne.NewSize(720, 440))
	d := dialog.NewCustom(title, tr("Close"), scroll, u.win)
	d.Show()
}

// answerName is what the summary calls a step: the heading its package
// gives it, or the step's own name when the package has no text for it.
func answerName(st *wizard.Step) string {
	text := st.Text
	if st.Type != 'E' {
		text, _ = wizard.Choices(text, st.Buttons)
	} else if len(st.Formats) > 0 {
		text, _ = wizard.Entry(text)
	}
	if title, _ := wizard.Title(text, textValues(st)); title != "" {
		return title
	}
	return st.Name
}

// answerText writes the answer of a step: the choice it took, in the
// words of the button, then its values with the units their places give
// them. The labels that are neither are what the text puts in its own
// places, not answers, and are left out.
func answerText(st *wizard.Step) string {
	var entry []wizard.EntryLine
	if st.Type == 'E' && len(st.Formats) > 0 {
		_, entry = wizard.Entry(st.Text)
	}
	units := map[int]string{}
	for _, l := range entry {
		for k, n := range l.Places {
			units[n] = entryWords(wizard.PlainText(l.Parts[k+1], nil))
		}
	}
	var parts []string
	for i, v := range st.Values {
		fi := i
		if st.Type != 'E' {
			if i == 0 {
				_, choices := wizard.Choices(st.Text, st.Buttons)
				opts := wizardChoiceLabels(st, choices)
				n, err := v.AsInt()
				if err == nil && n >= 0 && int(n) < len(opts) {
					parts = append(parts, opts[n])
					continue
				}
				parts = append(parts, tr("Choice {{.Number}}", args{"Number": strconv.FormatInt(n+1, 10)}))
				continue
			}
			fi = i - 1
		}
		if fi >= len(st.Formats) {
			continue // a value of the step's text, not an answer
		}
		n, err := v.AsInt()
		if err != nil {
			parts = append(parts, v.String())
			continue
		}
		f := st.Formats[fi]
		s := f.Show(n)
		u := units[fi]
		if u == "" {
			u = formatUnit(&f)
		}
		if u != "" {
			s = tr("{{.Value}} {{.Unit}}", args{"Value": s, "Unit": u})
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// stopView says that the assistant cannot be configured yet, and why.
func (w *assistantWizard) stopView(err error) fyne.CanvasObject {
	why := tr("{{.App}} cannot work through every part of this assistant yet.", args{"App": appName})
	var ue *asm.UserError
	switch {
	case w.sess == nil:
		why = tr("The assistant cannot be opened from this folder.")
	case errors.Is(err, asm.ErrUnknown):
		why = tr("The assistant needs details of the unit that {{.App}} cannot give it yet.", args{"App": appName})
	case errors.Is(err, asm.ErrType):
		why = tr("The assistant makes a calculation that {{.App}} cannot do yet.", args{"App": appName})
	case errors.Is(err, asm.ErrSyntax):
		why = tr("The assistant uses a part of the assistant language that {{.App}} does not support yet.", args{"App": appName})
	case errors.As(err, &ue):
		why = tr("The assistant stopped with its own message: {{.Message}}", args{"Message": ue.Msg})
	}
	title := widget.NewLabel(tr("{{.Assistant}} cannot be configured yet", args{"Assistant": w.title}))
	title.TextStyle.Bold = true
	title.Wrapping = fyne.TextWrapWord
	detail := captionText(err.Error(), theme.ColorNamePlaceHolder)
	detail.Wrapping = fyne.TextWrapBreak
	return container.NewVBox(container.NewBorder(nil, nil, widget.NewIcon(theme.InfoIcon()), nil, title),
		callout(theme.InfoIcon(), why),
		mutedText(tr("This is a known limitation of this version of {{.App}}: its assistant compiler is not complete yet. "+
			"Nothing was changed or written.", args{"App": appName})),
		detail)
}
