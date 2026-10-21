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
	dlg   dialog.Dialog
	body  *fyne.Container
	// n is the number of the step shown, from 1.
	n                  int
	back, next, cancel *widget.Button
	// values returns the values entered at the step shown, or the message
	// of the first one that is not valid; nil at a step without input.
	values  func() ([]asm.Value, bool)
	done    bool // the summary is shown
	stopped bool // the assistant stopped at the step it was given
	// apply is the label of the summary's button that would put the
	// assistant into the file; "" for Add to the file.
	apply string
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
	scroll.SetMinSize(fyne.NewSize(520, 320))
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

// goNext answers the step shown, or closes the dialog after the summary.
func (w *assistantWizard) goNext() {
	if w.done {
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
	w.values, w.done, w.stopped = nil, false, err != nil
	w.next.SetText(tr("Next"))
	w.next.SetIcon(theme.NavigateNextIcon())
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
		view = w.summaryView()
		w.done = true
		w.next.SetText(tr("Close"))
		w.next.SetIcon(theme.ConfirmIcon())
	default:
		view = w.stepView(st)
		if st.Directive == ".AER" {
			w.next.Disable()
		}
	}
	w.body.Objects = []fyne.CanvasObject{view}
	w.body.Refresh()
}

// stepHead is the number of the step and the step's own name.
func (w *assistantWizard) stepHead(st *wizard.Step) fyne.CanvasObject {
	return captionText(tr("Step {{.Number}} · {{.Name}}", args{"Number": strconv.Itoa(max(w.n, 1)), "Name": st.Name}), theme.ColorNamePlaceHolder)
}

// wizardStepText is the package's text of st.
func wizardStepText(st *wizard.Step) fyne.CanvasObject {
	if strings.TrimSpace(st.Text) == "" {
		return mutedText(tr("The assistant package has no text for this step."))
	}
	t := widget.NewLabel(strings.TrimSpace(st.Text))
	t.Wrapping = fyne.TextWrapWord
	return t
}

// stepView shows a step: its text and its inputs, by type.
func (w *assistantWizard) stepView(st *wizard.Step) fyne.CanvasObject {
	box := container.NewVBox(w.stepHead(st), wizardStepText(st))
	if w.readErr != nil && w.n == 1 {
		box.Objects = append([]fyne.CanvasObject{callout(theme.InfoIcon(),
			tr("The file's values of this assistant cannot be read, so its steps offer their defaults."))}, box.Objects...)
	}
	switch st.Directive {
	case ".AER":
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
	for i := range st.Labels {
		val := asm.Value{}
		if i < len(st.Values) {
			val = st.Values[i]
		}
		if st.Type != 'E' && i == 0 {
			opts := make([]string, st.Buttons)
			for b := range opts {
				opts[b] = tr("Choice {{.Number}}", args{"Number": strconv.Itoa(b + 1)})
			}
			radio := widget.NewRadioGroup(opts, nil)
			radio.Required = true
			if n, err := val.AsInt(); err == nil && n >= 0 && int(n) < len(opts) {
				radio.SetSelected(opts[n])
			}
			box.Add(radio)
			inputs = append(inputs, func() (asm.Value, bool) {
				return asm.IntVal(int64(max(slices.Index(opts, radio.Selected), 0))), true
			})
			continue
		}
		fi := i
		if st.Type != 'E' {
			fi = i - 1
		}
		var f *wizard.Format
		if fi < len(st.Formats) {
			f = &st.Formats[fi]
		}
		row, get := valueInput(f, val, i, len(st.Labels))
		box.Add(row)
		inputs = append(inputs, get)
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

// valueInput is the entry of value i of n, with the range and unit of
// format f (nil: a plain whole number), its default val, and the message
// under it when the text is not valid. get returns the raw value.
func valueInput(f *wizard.Format, val asm.Value, i, n int) (row fyne.CanvasObject, get func() (asm.Value, bool)) {
	e := widget.NewEntry()
	raw, err := val.AsInt()
	if err == nil {
		e.SetText(strconv.FormatInt(raw, 10))
		if f != nil {
			e.SetText(f.Show(raw))
		}
	}
	// The message keeps its line while empty, so that the dialog does not
	// move when it shows.
	msg := captionText("", theme.ColorNameError)
	setMsg := func(text string) {
		msg.Segments[0].(*widget.TextSegment).Text = text
		msg.Refresh()
	}
	right := container.NewHBox()
	if unit := formatUnit(f); unit != "" {
		right.Add(widget.NewLabel(unit))
	}
	if f != nil {
		right.Add(captionText(tr("{{.Min}} … {{.Max}}", args{"Min": f.Show(f.Min), "Max": f.Show(f.Max)}), theme.ColorNamePlaceHolder))
	}
	entry := container.New(fixedWidth(140), e)
	line := container.NewHBox(entry, container.NewCenter(right))
	if n > 1 {
		line.Objects = append([]fyne.CanvasObject{container.New(fixedWidth(90),
			widget.NewLabel(tr("Value {{.Number}}", args{"Number": strconv.Itoa(i + 1)})))}, line.Objects...)
	}
	get = func() (asm.Value, bool) {
		if f == nil {
			v, err := strconv.ParseInt(strings.TrimSpace(e.Text), 10, 64)
			if err != nil {
				setMsg(tr("Enter a whole number."))
				return asm.Value{}, false
			}
			setMsg("")
			return asm.IntVal(v), true
		}
		v, err := f.Parse(e.Text)
		if err != nil {
			setMsg(tr("Enter a number from {{.Min}} to {{.Max}}.", args{"Min": f.Show(f.Min), "Max": f.Show(f.Max)}))
			return asm.Value{}, false
		}
		setMsg("")
		return asm.IntVal(v), true
	}
	return container.New(layout.NewCustomPaddedVBoxLayout(0), line, msg), get
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
		name := widget.NewLabelWithStyle(st.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
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
	label := w.apply
	if label == "" {
		label = tr("Add to the file")
	}
	add := widget.NewButtonWithIcon(label, theme.ContentAddIcon(), nil)
	add.Importance = widget.HighImportance
	switch {
	case w.dir == "": // an assistant of the file: editing it comes later
		add.Disable()
		box.Add(container.NewHBox(add))
		box.Add(callout(theme.InfoIcon(), tr("Nothing was written. Saving the answers of an assistant already in the "+
			"file comes once {{.App}} can replace one in place.", args{"App": appName})))
		return box
	case w.u.settings.file == nil:
		add.Disable()
		box.Add(container.NewHBox(add))
		box.Add(callout(theme.InfoIcon(), tr("Nothing was written. Open a settings file to add an assistant to it.")))
		return box
	}
	add.OnTapped = func() { w.addToFile() }
	box.Add(container.NewHBox(add))
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
			rows.Add(widget.NewLabelWithStyle(st.Name, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
			rows.Add(v)
		}
		body = rows
		if len(rows.Objects) == 0 {
			body = mutedText(tr("The assistant lists no answers in its summary."))
		}
	}
	head := widget.NewLabelWithStyle(tr("Summary"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	scroll := container.NewVScroll(container.NewVBox(head, body))
	scroll.SetMinSize(fyne.NewSize(520, 280))
	d := dialog.NewCustom(title, tr("Close"), scroll, u.win)
	d.Show()
}

// answerText writes the answer of a step: the choice, then the values in
// the step's formats.
func answerText(st *wizard.Step) string {
	var parts []string
	for i, v := range st.Values {
		n, err := v.AsInt()
		switch {
		case err != nil:
			parts = append(parts, v.String())
		case st.Type != 'E' && i == 0:
			parts = append(parts, tr("Choice {{.Number}}", args{"Number": strconv.FormatInt(n+1, 10)}))
		default:
			fi := i
			if st.Type != 'E' {
				fi = i - 1
			}
			if fi < len(st.Formats) {
				f := st.Formats[fi]
				s := f.Show(n)
				if u := formatUnit(&f); u != "" {
					s = tr("{{.Value}} {{.Unit}}", args{"Value": s, "Unit": u})
				}
				parts = append(parts, s)
			} else {
				parts = append(parts, strconv.FormatInt(n, 10))
			}
		}
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
