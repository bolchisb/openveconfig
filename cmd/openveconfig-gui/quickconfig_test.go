package main

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus/system"
)

// newWizard opens the wizard of a new UI on session s and chooses action.
func newWizard(t *testing.T, s *session, action quickAction) (*ui, *quickWizard) {
	t.Helper()
	u := newTestUI(t)
	u.setSession(s)
	w := newQuickWizard(u)
	t.Cleanup(w.win.Close)
	w.action = action
	return u, w
}

// press taps Next and waits for the operation it starts.
func press(t *testing.T, w *quickWizard) {
	t.Helper()
	if w.next.Hidden || w.next.Disabled() {
		t.Fatalf("%q: Next is not available", w.title.Text)
	}
	test.Tap(w.next)
	w.r.ops.Wait()
}

// bodyText returns the text of the labels of the current step.
func bodyText(w *quickWizard) string {
	var b strings.Builder
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Label:
			b.WriteString(x.Text + "\n")
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		}
	}
	walk(w.body)
	return b.String()
}

// offBus returns n new units, all switched off.
func offBus(n int) []simulator.Device {
	ds := simulator.SystemFactory(n)
	for _, d := range ds {
		d.System.Off = true
	}
	return ds
}

// setUpUntilUnits walks the wizard to the first switch-on step.
func setUpUntilUnits(t *testing.T, w *quickWizard, q system.QuickLayout) {
	t.Helper()
	w.goTo(w.stepLayout)
	w.layout = q
	w.goTo(w.stepConnect)
	press(t, w) // to the port step
	press(t, w) // to switching every unit off
	press(t, w) // to unit 1
	if !strings.Contains(w.title.Text, "Switch on unit 1") {
		t.Fatalf("step %q", w.title.Text)
	}
}

func TestQuickSetUpOneAtATime(t *testing.T) {
	s, sim := systemTestSession(t, offBus(3))
	_, w := newWizard(t, s, actSetUp)
	setUpUntilUnits(t, w, system.QuickThreePhase)
	ids := []uint32{0x10000003, 0x10000001, 0x10000002} // switch-on order
	for i, id := range ids {
		sim.SetPowered(id, true)
		press(t, w)
		if len(w.order) != i+1 || w.order[i].LongID != id {
			t.Fatalf("after unit %d: order %v, step %q: %s", i+1, w.order, w.title.Text, bodyText(w))
		}
	}
	if w.title.Text != "Send configuration" || !w.next.Disabled() {
		t.Fatalf("step %q, Send disabled %v; want the confirmation, waiting for the assistant tick", w.title.Text, w.next.Disabled())
	}
	for _, o := range w.body.Objects {
		if c, ok := o.(*widget.Check); ok {
			c.SetChecked(true)
		}
	}
	press(t, w)
	if w.title.Text != "Done" {
		t.Fatalf("step %q: %s", w.title.Text, bodyText(w))
	}
	// Unit i in switch-on order is on phase i.
	for i, id := range ids {
		un, _ := sim.Unit(id)
		if r := system.Record(un.ActiveRecord); r.Phase() != i || r.Phases() != 3 {
			t.Errorf("unit %08X uses %s, want phase %d of 3", id, r.Hex(), i)
		}
	}
}

func TestQuickSetUpMismatch(t *testing.T) {
	devs := offBus(2)
	devs[1].System.VersionWord = 2699475
	devs[1].Profile.Version = 2699475
	s, sim := systemTestSession(t, devs)
	_, w := newWizard(t, s, actSetUp)
	setUpUntilUnits(t, w, system.QuickParallel2)
	sim.SetPowered(0x10000001, true)
	press(t, w)
	sim.SetPowered(0x10000002, true)
	press(t, w)
	if w.title.Text != "Stopped" || !strings.Contains(bodyText(w), "other firmware than the first unit") ||
		!strings.Contains(bodyText(w), "Nothing was written") {
		t.Fatalf("step %q: %s", w.title.Text, bodyText(w))
	}
	for _, un := range sim.Units() {
		if un.Writes != 0 {
			t.Errorf("unit %08X got %d writes", un.LongID, un.Writes)
		}
	}
	// Start again goes back to switching every unit off.
	press(t, w)
	if w.title.Text != "Switch every unit off" || w.order != nil {
		t.Errorf("after Start again: step %q, order %v", w.title.Text, w.order)
	}

	// Two units switched on at once.
	press(t, w)
	press(t, w)
	if w.title.Text != "Stopped" || !strings.Contains(bodyText(w), "Exactly one more unit") {
		t.Errorf("two at once: step %q: %s", w.title.Text, bodyText(w))
	}
}

func TestQuickSetUpWriteFailure(t *testing.T) {
	s, sim := systemTestSession(t, offBus(2))
	sim.SetBehaviour(0x10000002, simulator.Behaviour{StaleReads: 3})
	_, w := newWizard(t, s, actSetUp)
	setUpUntilUnits(t, w, system.QuickParallel2)
	before := records(sim)
	for _, id := range []uint32{0x10000001, 0x10000002} {
		sim.SetPowered(id, true)
		press(t, w)
	}
	for _, o := range w.body.Objects {
		if c, ok := o.(*widget.Check); ok {
			c.SetChecked(true)
		}
	}
	press(t, w)
	if w.title.Text != "Stopped" || !strings.Contains(bodyText(w), "The previous configuration was restored on all units") {
		t.Fatalf("step %q: %s", w.title.Text, bodyText(w))
	}
	if got := records(sim); got[0] != before[0] || got[1] != before[1] {
		t.Errorf("records %v, want %v", got, before)
	}
}

func TestQuickChangeSettings(t *testing.T) {
	s, _ := systemTestSession(t, simulator.SystemThreePhase())
	u, w := newWizard(t, s, actChange)
	w.goTo(w.stepConnect)
	press(t, w)
	press(t, w)
	press(t, w) // Get configuration
	if w.title.Text != "Existing system" || !strings.Contains(bodyText(w), "Three phase") ||
		!strings.Contains(bodyText(w), "10000002: the unit on L2") {
		t.Fatalf("step %q: %s", w.title.Text, bodyText(w))
	}
	// Open the unit on L2 (address 1).
	test.Tap(findButtons(w.body, "Open settings")[1])
	waitFor(t, "settings", func() bool {
		var ok bool
		fyne.DoAndWait(func() { ok = u.settings.dev != nil })
		return ok
	})
	if u.unit != 1 || u.settings.dev.Address != 1 {
		t.Errorf("unit %d, settings of %d; want address 1", u.unit, u.settings.dev.Address)
	}

	// An unknown layout points to the System Configurator.
	s2, _ := systemTestSession(t, simulator.SystemFactory(2))
	_, w2 := newWizard(t, s2, actChange)
	w2.goTo(w2.stepSwitchOn)
	press(t, w2)
	if w2.title.Text != "Unknown layout" || len(findButtons(w2.body, "Open the System Configurator")) != 1 {
		t.Errorf("step %q: %s", w2.title.Text, bodyText(w2))
	}
}

func TestQuickStandalone(t *testing.T) {
	s, sim := systemTestSession(t, simulator.SystemParallel2())
	_, w := newWizard(t, s, actStandalone)
	w.goTo(w.stepSwitchOn)
	press(t, w)
	if w.title.Text != "Stopped" || !strings.Contains(bodyText(w), "found 2") {
		t.Fatalf("two units on: step %q: %s", w.title.Text, bodyText(w))
	}
	sim.SetPowered(0x10000001, false)
	press(t, w) // Start again
	press(t, w)
	if w.title.Text != "Send configuration" {
		t.Fatalf("step %q: %s", w.title.Text, bodyText(w))
	}
	press(t, w)
	if w.title.Text != "Done" {
		t.Fatalf("step %q: %s", w.title.Text, bodyText(w))
	}
	un, _ := sim.Unit(0x10000002)
	if r := system.Record(un.ActiveRecord); r != system.Standalone(&system.Unit{VersionWord: 2699481}, true) || un.Address != 0 {
		t.Errorf("unit uses %s at address %d", r.Hex(), un.Address)
	}
}

func TestQuickLEDCodes(t *testing.T) {
	for _, tt := range []struct {
		bulk, abs, float ledState
		want             int
	}{
		{ledOff, ledOff, ledFlashing, 1}, {ledOff, ledFlashing, ledOff, 3}, {ledFlashing, ledOff, ledOn, 11},
		{ledOn, ledOn, ledFlashing, 25}, {ledOn, ledOn, ledOn, 26},
	} {
		if got := ledCode(tt.bulk, tt.abs, tt.float); got != tt.want {
			t.Errorf("ledCode(%d, %d, %d) = %d, want %d", tt.bulk, tt.abs, tt.float, got, tt.want)
		}
	}
	_, w := newWizard(t, nil, actLEDs)
	w.goTo(w.stepLEDs)
	form := w.body.Objects[1].(*widget.Form)
	form.Items[0].Widget.(*widget.Select).SetSelectedIndex(int(ledOn))
	form.Items[1].Widget.(*widget.Select).SetSelectedIndex(int(ledOn))
	form.Items[2].Widget.(*widget.Select).SetSelectedIndex(int(ledFlashing))
	if !strings.Contains(bodyText(w), "VE.Bus error 25: The units are not compatible") {
		t.Errorf("LED page: %s", bodyText(w))
	}
	// The content cites its public sources.
	var links []string
	for _, o := range w.body.Objects {
		if h, ok := o.(*widget.Hyperlink); ok {
			links = append(links, h.URL.String())
		}
	}
	if len(links) != len(ledSources) || !strings.Contains(links[0], "victronenergy.com") {
		t.Errorf("sources %v", links)
	}
}

// findButtons returns the buttons labelled text under o.
func findButtons(o fyne.CanvasObject, text string) []*widget.Button {
	var out []*widget.Button
	switch x := o.(type) {
	case *widget.Button:
		if x.Text == text {
			out = append(out, x)
		}
	case *fyne.Container:
		for _, c := range x.Objects {
			out = append(out, findButtons(c, text)...)
		}
	}
	return out
}

// records returns every simulated unit's stored record.
func records(s *simulator.Simulator) []system.Record {
	var rs []system.Record
	for _, u := range s.Units() {
		rs = append(rs, system.Record(u.Record))
	}
	return rs
}
