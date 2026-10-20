package main

// The Quick Configure wizard: set up one of the fixed VE.Bus systems by
// switching the units on one at a time, look at the system an existing
// set of units forms, reset a unit to standalone, or read a VE.Bus error
// code from the LEDs. The layouts and checks are in pkg/vebus/system
// (QuickLayout, CheckQuickUnit, RecognizeQuick), the bus operations in
// pkg/vebus/system/bus.

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/vebus/system"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

// quickWizards holds the open wizard of each ui. Used on the UI goroutine
// only.
var quickWizards = map[*ui]*quickWizard{}

// showQuickConfigure opens the Quick Configure wizard, or brings the open
// one to the front.
func showQuickConfigure(u *ui) {
	if w := quickWizards[u]; w != nil {
		w.win.RequestFocus()
		return
	}
	w := newQuickWizard(u)
	quickWizards[u] = w
	w.win.SetOnClosed(func() {
		w.r.close()
		delete(quickWizards, u)
	})
	u.onSession(func(s *session) {
		if quickWizards[u] == w {
			w.sessionChanged(s)
		}
	})
	w.win.Show()
}

// quickAction is what the wizard does.
type quickAction int

const (
	actSetUp quickAction = iota
	actChange
	actStandalone
	actLEDs
)

// quickSetUps are the layouts the wizard sets up, in the order offered.
var quickSetUps = []system.QuickLayout{system.QuickParallel2, system.QuickParallel3, system.QuickSplit180,
	system.QuickSplit180Auto, system.QuickSplit120, system.QuickSplit240, system.QuickThreePhase}

func quickName(q system.QuickLayout) string {
	switch q {
	case system.QuickParallel2:
		return tr("Two units in parallel")
	case system.QuickParallel3:
		return tr("Three units in parallel")
	case system.QuickSplit120:
		return tr("Two phases, 120°")
	case system.QuickSplit180:
		return tr("Split phase, 180°")
	case system.QuickSplit240:
		return tr("Two phases, 240°")
	case system.QuickSplit180Auto:
		return tr("Split phase, 180°, L2 floating (automatic detection)")
	case system.QuickThreePhase:
		return tr("Three phase")
	case system.QuickStandalone:
		return tr("One unit on its own")
	}
	return q.String()
}

// roleText names role i (from 0, the switch-on order) of layout q.
func roleText(q system.QuickLayout, i int) string {
	n := strconv.Itoa(i + 1)
	switch {
	case q == system.QuickStandalone:
		return tr("the unit on its own")
	case !q.Parallel():
		return tr("the unit on {{.Phase}}", args{"Phase": phaseName(i)})
	case i == 0:
		return tr("unit 1, which leads the system")
	}
	return tr("unit {{.N}} in parallel", args{"N": n})
}

// quickWizard is the wizard window. Its fields are used on the UI
// goroutine only.
type quickWizard struct {
	u   *ui
	win fyne.Window
	r   *busRunner // on the session the wizard uses; replaced when it changes

	action quickAction
	layout system.QuickLayout
	order  []*system.Unit // units found so far, in switch-on order
	// needsBus reports that the current step is after the port step.
	needsBus bool
	current  func() // shows the current step again
	history  []func()

	title *widget.Label
	body  *fyne.Container
	back  *widget.Button
	next  *widget.Button
}

func newQuickWizard(u *ui) *quickWizard {
	w := &quickWizard{u: u, win: u.app.NewWindow(tr("Quick Configure"))}
	w.r = newBusRunner(w.win, u.sess)
	w.title = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	w.title.SizeName = theme.SizeNameSubHeadingText
	w.body = container.NewVBox()
	w.back = widget.NewButtonWithIcon(tr("Back"), theme.NavigateBackIcon(), w.goBack)
	w.next = widget.NewButtonWithIcon(tr("Next"), theme.NavigateNextIcon(), nil)
	w.next.IconPlacement = widget.ButtonIconTrailingText
	w.next.Importance = widget.HighImportance
	buttons := container.NewBorder(nil, nil, w.back, w.next)
	w.win.SetContent(container.NewStack(newColourRect(colorNamePage, fyne.Size{}, ""),
		container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16),
			container.NewBorder(w.title, buttons, nil, nil, container.NewVScroll(w.body)))))
	w.win.Resize(fyne.NewSize(640, 520))
	w.start()
	return w
}

// show makes step the current one: title, content and the Next action
// (nil: no Next button).
func (w *quickWizard) show(title string, content []fyne.CanvasObject, nextText string, next func()) {
	w.title.SetText(title)
	w.body.Objects = content
	w.body.Refresh()
	w.next.SetText(nextText)
	w.next.OnTapped = next
	w.next.Hidden = next == nil
	w.next.Enable()
	w.back.Hidden = len(w.history) == 0
	w.win.Content().Refresh()
}

// goTo shows step, keeping the current one for Back.
func (w *quickWizard) goTo(step func()) {
	if w.current != nil {
		w.history = append(w.history, w.current)
	}
	w.current = step
	step()
}

func (w *quickWizard) goBack() {
	if len(w.history) == 0 {
		return
	}
	w.current = w.history[len(w.history)-1]
	w.history = w.history[:len(w.history)-1]
	w.current()
}

// start shows the first step and forgets the others.
func (w *quickWizard) start() {
	w.current, w.history, w.needsBus, w.order = nil, nil, false, nil
	w.goTo(w.stepAction)
}

// restartFrom shows step and forgets the steps after the port step, so
// Back does not repeat bus operations.
func (w *quickWizard) restartFrom(step func()) {
	w.current = nil
	w.history = nil
	w.goTo(step)
}

// sessionChanged makes the wizard use session s. A step after the port
// step goes back to it.
func (w *quickWizard) sessionChanged(s *session) {
	w.r.close()
	w.r = newBusRunner(w.win, s)
	if w.needsBus {
		w.needsBus = false
		w.restartFrom(w.stepPort)
	} else if w.current != nil {
		w.current()
	}
}

func paragraph(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func (w *quickWizard) stepAction() {
	w.needsBus = false
	actions := []string{tr("Set up a VE.Bus system"), tr("Look at an existing system and change the settings of its units"),
		tr("Reset a unit to standalone"), tr("Read a VE.Bus error code from the LEDs")}
	choice := widget.NewRadioGroup(actions, nil)
	choice.SetSelected(actions[w.action])
	w.show(tr("What do you want to do?"), []fyne.CanvasObject{
		paragraph(tr("Quick Configure sets up the common VE.Bus systems without arranging the units yourself. For other layouts use the System Configurator.")),
		choice,
	}, tr("Next"), func() {
		w.action = quickAction(slices.Index(actions, choice.Selected))
		switch w.action {
		case actSetUp:
			w.goTo(w.stepLayout)
		case actLEDs:
			w.goTo(w.stepLEDs)
		default:
			w.goTo(w.stepConnect)
		}
	})
}

func (w *quickWizard) stepLayout() {
	names := make([]string, len(quickSetUps))
	for i, q := range quickSetUps {
		names[i] = quickName(q)
	}
	choice := widget.NewRadioGroup(names, nil)
	if i := slices.Index(quickSetUps, w.layout); i >= 0 {
		choice.SetSelected(names[i])
	} else {
		choice.SetSelected(names[0])
	}
	w.show(tr("Which system?"), []fyne.CanvasObject{choice}, tr("Next"), func() {
		w.layout = quickSetUps[slices.Index(names, choice.Selected)]
		w.goTo(w.stepConnect)
	})
}

func (w *quickWizard) stepConnect() {
	text := tr("Connect the units to each other with straight RJ45 cables (no crossover cables), and connect the interface to one of them. Disconnect GX devices from the VE.Bus and connect only supported remote panels.")
	if w.action == actStandalone {
		text = tr("Connect the interface to the unit to reset with a straight RJ45 cable. Disconnect GX devices from the VE.Bus.")
	}
	w.show(tr("Connect the interface"), []fyne.CanvasObject{paragraph(text)}, tr("Next"), func() { w.goTo(w.stepPort) })
}

func (w *quickWizard) stepPort() {
	w.needsBus = false
	s := w.r.sess
	var content []fyne.CanvasObject
	if s == nil {
		connect := widget.NewButton(tr("Connect…"), func() { w.u.conn.show() })
		content = []fyne.CanvasObject{paragraph(tr("Connect to the interface. The wizard goes on when the connection is open.")),
			container.NewHBox(connect)}
	} else {
		content = []fyne.CanvasObject{paragraph(describe(s))}
	}
	w.show(tr("Choose the port"), content, tr("Next"), func() { w.goTo(w.stepSwitchOn) })
	if s == nil {
		w.next.Disable()
	}
}

func (w *quickWizard) stepSwitchOn() {
	w.needsBus = true
	switch w.action {
	case actSetUp:
		w.order = nil
		w.show(tr("Switch every unit off"), []fyne.CanvasObject{paragraph(trn(
			"Switch every unit of the system off and keep the cables connected. Then you switch on the {{.Count}} unit one at a time; the order gives each unit its place in the system.",
			"Switch every unit of the system off and keep the cables connected. Then you switch on the {{.Count}} units one at a time; the order gives each unit its place in the system.",
			w.layout.Units()))}, tr("Next"), func() { w.goTo(func() { w.stepUnit(1) }) })
	case actChange:
		w.show(tr("Switch the units on"), []fyne.CanvasObject{paragraph(tr("Switch every unit of the system on and wait until they have started. Nothing is written to the units."))},
			tr("Get configuration"), w.recognize)
	case actStandalone:
		w.show(tr("Switch on only one unit"), []fyne.CanvasObject{paragraph(tr("Switch every unit off, then switch on only the unit to reset and wait until it has started."))},
			tr("Next"), w.checkStandalone)
	}
}

// stepUnit asks for unit i (from 1) to be switched on.
func (w *quickWizard) stepUnit(i int) {
	n := w.layout.Units()
	w.show(tr("Switch on unit {{.N}} of {{.Total}}", args{"N": strconv.Itoa(i), "Total": strconv.Itoa(n)}), []fyne.CanvasObject{
		paragraph(tr("Switch on the unit that is to be {{.Role}}, wait until it has started, then press Find unit.",
			args{"Role": roleText(w.layout, i-1)})),
	}, tr("Find unit"), w.findUnit)
}

// failure shows what went wrong, with what to do, and starts again from
// switching every unit off. Nothing is written in the steps before the
// write, and a failed write restores the previous records.
func (w *quickWizard) failure(msg string) {
	w.show(tr("Stopped"), []fyne.CanvasObject{
		widget.NewIcon(theme.NewColoredResource(theme.ErrorIcon(), theme.ColorNameError)), paragraph(msg),
	}, tr("Start again"), func() { w.restartFrom(w.stepSwitchOn) })
	w.back.Hide()
}

// findUnit scans and takes the one unit that is new as the next role.
func (w *quickWizard) findUnit() {
	var rep *bus.ScanReport
	w.r.run(tr("Find unit"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		rep, err = bs.Scan(ctx, p)
		return err
	}, func(err error) {
		if err != nil {
			if !isCancel(err) {
				w.failure(failureText(err))
			}
			return
		}
		var added []*system.Unit
		for _, un := range rep.Units {
			if !slices.Contains(w.order, un) {
				added = append(added, un)
			}
		}
		lost := slices.ContainsFunc(w.order, func(un *system.Unit) bool { return !slices.Contains(rep.Units, un) })
		if len(added) != 1 || lost {
			w.failure(tr("Exactly one more unit should have answered, but {{.Found}} units answered in all ({{.Want}} expected). Switch every unit off and start again; switch on one unit at a time and wait until it has started.",
				args{"Found": strconv.Itoa(len(rep.Units)), "Want": strconv.Itoa(len(w.order) + 1)}))
			return
		}
		un := added[0]
		first := un
		if len(w.order) > 0 {
			first = w.order[0]
		}
		if issues := system.CheckQuickUnit(w.layout, first, un); len(issues) > 0 {
			msg := ""
			for _, is := range issues {
				msg += issueText(is) + "\n"
			}
			w.failure(msg + tr("Nothing was written. Switch every unit off, remove the unit that does not match, and start again."))
			return
		}
		w.order = append(w.order, un)
		if len(w.order) < w.layout.Units() {
			w.goTo(func() { w.stepUnit(len(w.order) + 1) })
			return
		}
		w.prepareWrite()
	})
}

// isCancel reports that the user cancelled an operation before anything
// was written.
func isCancel(err error) bool {
	var be *bus.Error
	return !errors.As(err, &be) && errors.Is(err, context.Canceled)
}

// prepareWrite reads every unit's record (the dry run) and asks for
// confirmation.
func (w *quickWizard) prepareWrite() {
	var dry *bus.WriteReport
	q, order := w.layout, w.order
	w.r.run(tr("Send configuration"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		dry, err = bs.WriteQuick(ctx, q, order, bus.WriteOptions{}, p)
		return err
	}, func(err error) {
		if err != nil {
			if !isCancel(err) {
				w.failure(failureText(err))
			}
			return
		}
		w.confirm(dry, func(remove bool) {
			w.r.run(tr("Send configuration"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
				var err error
				dry, err = bs.WriteQuick(ctx, q, order, bus.WriteOptions{Confirm: true, RemoveAssistants: remove}, p)
				return err
			}, func(err error) {
				w.finishWrite(dry, err, tr("The system is set up: every unit restarted with its new configuration."))
			})
		})
	})
}

// confirm shows what a write does, with the assistant warning, and calls
// send with whether the assistants may be removed.
func (w *quickWizard) confirm(dry *bus.WriteReport, send func(removeAssistants bool)) {
	content := []fyne.CanvasObject{paragraph(trn(
		"Sending writes the configuration to {{.Count}} unit and restarts the system. All units switch off for a moment, so loads lose power.",
		"Sending writes the configuration to {{.Count}} units and restarts the system. All units switch off for a moment, so loads lose power.",
		len(dry.Units)))}
	for i, r := range dry.Units {
		role := roleText(w.layout, i)
		if w.action == actStandalone {
			role = roleText(system.QuickStandalone, 0)
		}
		content = append(content, widget.NewLabel(tr("{{.Unit}}: {{.Role}}", args{"Unit": r.Unit.Label(), "Role": role})))
	}
	var remove *widget.Check
	if len(dry.AssistantsRemoved) > 0 {
		warn := paragraph(tr("Sending this configuration removes all assistants from {{.Units}}.", args{"Units": unitLabels(dry.AssistantsRemoved)}))
		warn.Importance = widget.DangerImportance
		remove = widget.NewCheck(tr("Remove the assistants"), nil)
		content = append(content, warn, remove)
	}
	w.goTo(func() {
		w.show(tr("Send configuration"), content, tr("Send"), func() {
			if remove != nil && !remove.Checked {
				w.next.Disable()
				return
			}
			send(remove != nil)
		})
		if remove != nil {
			w.next.Disable()
			remove.OnChanged = func(on bool) {
				if on {
					w.next.Enable()
				} else {
					w.next.Disable()
				}
			}
		}
	})
}

// finishWrite shows the outcome of a write.
func (w *quickWizard) finishWrite(rep *bus.WriteReport, err error, done string) {
	if err != nil {
		if details := writeDetails(rep); details != "" {
			w.failure(failureText(err) + "\n\n" + details)
		} else {
			w.failure(failureText(err))
		}
		return
	}
	w.restartFrom(func() {
		w.needsBus = false
		w.show(tr("Done"), []fyne.CanvasObject{paragraph(done)}, tr("Start again"), w.start)
	})
}

// recognize reads every unit's record and shows the layout they form.
func (w *quickWizard) recognize() {
	var rep *bus.ScanReport
	var active map[uint32]byte
	w.r.run(tr("Get configuration"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		rep, err = bs.Scan(ctx, p)
		active = activeAddresses(bs)
		return err
	}, func(err error) {
		if err != nil {
			if !isCancel(err) {
				w.failure(failureText(err))
			}
			return
		}
		q, ordered, ok := system.RecognizeQuick(rep.Units)
		w.goTo(func() { w.stepRecognized(q, ordered, ok, rep.Units, active) })
	})
}

func (w *quickWizard) stepRecognized(q system.QuickLayout, ordered []*system.Unit, ok bool, units []*system.Unit, active map[uint32]byte) {
	if !ok {
		open := widget.NewButton(tr("Open the System Configurator"), func() { showSystemConfigurator(w.u) })
		w.show(tr("Unknown layout"), []fyne.CanvasObject{
			paragraph(trn("The {{.Count}} unit found does not form one of the systems Quick Configure sets up. Use the System Configurator to see and change its layout.",
				"The {{.Count}} units found do not form one of the systems Quick Configure sets up. Use the System Configurator to see and change their layout.", len(units))),
			container.NewHBox(open),
		}, "", nil)
		return
	}
	content := []fyne.CanvasObject{paragraph(tr("The units form this system: {{.Layout}}. Open a unit to change its settings in the main window.",
		args{"Layout": quickName(q)}))}
	for i, un := range ordered {
		addr, found := active[un.LongID]
		b := widget.NewButtonWithIcon(tr("Open settings"), theme.SettingsIcon(), func() {
			w.u.unit = addr
			w.u.win.RequestFocus()
			getSettings(w.u)
		})
		if !found {
			b.Disable()
		}
		content = append(content, container.NewBorder(nil, nil, nil, b,
			widget.NewLabel(tr("{{.Unit}}: {{.Role}}", args{"Unit": un.Label(), "Role": roleText(q, i)}))))
	}
	w.show(tr("Existing system"), content, "", nil)
}

// checkStandalone finds the one unit that is switched on and asks for
// confirmation before resetting it.
func (w *quickWizard) checkStandalone() {
	var dry *bus.WriteReport
	w.r.run(tr("Reset a unit to standalone"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
		var err error
		dry, err = bs.ResetStandalone(ctx, bus.WriteOptions{}, p)
		return err
	}, func(err error) {
		if err != nil {
			if !isCancel(err) {
				w.failure(failureText(err))
			}
			return
		}
		w.confirm(dry, func(remove bool) {
			w.r.run(tr("Reset a unit to standalone"), func(ctx context.Context, bs *bus.Session, p bus.Progress) error {
				var err error
				dry, err = bs.ResetStandalone(ctx, bus.WriteOptions{Confirm: true, RemoveAssistants: remove}, p)
				return err
			}, func(err error) {
				w.finishWrite(dry, err, tr("The unit is now a unit on its own and restarted with that configuration."))
			})
		})
	})
}

// stepLEDs reads a VE.Bus error code from the bulk, absorption and float
// LEDs. It needs no connection.
func (w *quickWizard) stepLEDs() {
	states := []string{tr("Off"), tr("Flashing"), tr("On")}
	result := paragraph("")
	var bulk, absorption, float *widget.Select
	update := func(string) {
		n := ledCode(ledState(bulk.SelectedIndex()), ledState(absorption.SelectedIndex()), ledState(float.SelectedIndex()))
		text := errorCodeText(n)
		if n > 0 {
			text = tr("VE.Bus error {{.Code}}: {{.Text}}", args{"Code": strconv.Itoa(n), "Text": text})
		}
		result.SetText(text)
	}
	newLED := func() *widget.Select {
		s := widget.NewSelect(states, update)
		return s
	}
	bulk, absorption, float = newLED(), newLED(), newLED()
	for _, s := range []*widget.Select{bulk, absorption, float} {
		s.SetSelectedIndex(0)
	}
	update("")
	form := widget.NewForm(
		widget.NewFormItem(tr("Bulk"), bulk),
		widget.NewFormItem(tr("Absorption"), absorption),
		widget.NewFormItem(tr("Float"), float))
	content := []fyne.CanvasObject{
		paragraph(tr("A unit shows a VE.Bus error code while it is stopped by the error: the inverter-on LED flashes, and the bulk, absorption and float LEDs that flash do so in turn with it. Choose what these three LEDs do.")),
		form, result, widget.NewLabelWithStyle(tr("Sources"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	}
	for _, s := range ledSources {
		if link, err := url.Parse(s); err == nil {
			content = append(content, widget.NewHyperlink(s, link))
		}
	}
	w.show(tr("VE.Bus error code"), content, "", nil)
}
