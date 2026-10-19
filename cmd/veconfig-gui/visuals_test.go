package main

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// chartSetting is a setting of a test model: its scale, offset and raw
// value.
type chartSetting struct {
	sc, offset int16
	raw        uint16
}

// chartDevice returns a model with the given settings.
func chartDevice(settings map[uint16]chartSetting) *vebus.Device {
	d := vebus.NewDevice(nil)
	for id, s := range settings {
		d.AddSetting(id, mk2.SettingInfo{Sc: s.sc, Offset: s.offset, Min: 0, Max: 0xFFFF, Default: s.raw}, s.raw)
	}
	return d
}

// chargeSettings are the settings of the charge profile: 14.40 V
// absorption, 13.80 V float, 70 A, 8 h absorption, fixed algorithm.
func chargeSettings() map[uint16]chartSetting {
	return map[uint16]chartSetting{
		2: {sc: -100, raw: 1440}, 3: {sc: -100, raw: 1380}, 4: {sc: 1, raw: 70},
		9: {sc: 1, raw: 480}, 10: {sc: 1, raw: 1},
	}
}

// label returns the label of s whose value is value.
func label(t *testing.T, s plotScene, value string) plotLabel {
	t.Helper()
	for _, l := range s.labels {
		if l.value == value {
			return l
		}
	}
	t.Fatalf("no label %q in %v", value, labelValues(s))
	return plotLabel{}
}

func labelValues(s plotScene) []string {
	var v []string
	for _, l := range s.labels {
		v = append(v, l.value)
	}
	return v
}

// labelsNamed returns the labels of s with name name, left to right.
func labelsNamed(s plotScene, name string) []plotLabel {
	var ls []plotLabel
	for _, l := range s.labels {
		if l.name == name {
			ls = append(ls, l)
		}
	}
	slices.SortFunc(ls, func(a, b plotLabel) int {
		if a.x < b.x {
			return -1
		}
		return 1
	})
	return ls
}

func TestChargeChart(t *testing.T) {
	s := chargeChart(chartDevice(chargeSettings()))
	if s == nil {
		t.Fatal("no chart for a model with the charge settings")
	}
	if !strings.Contains(s.subtitle, "Fixed") || !strings.Contains(s.caption, "not a simulation") {
		t.Errorf("subtitle %q, caption %q", s.subtitle, s.caption)
	}
	abs, flt := label(t, s.plot, "14.40 V"), label(t, s.plot, "13.80 V")
	if abs.y <= flt.y || abs.y > 1 || flt.y < voltageBottom {
		t.Errorf("absorption at y %v, float at y %v; want absorption above float in the voltage panel", abs.y, flt.y)
	}
	if l := label(t, s.plot, "70 A"); l.y != currentTop {
		t.Errorf("charge current at y %v, want the top of the current panel %v", l.y, currentTop)
	}
	if l := label(t, s.plot, "Absorption"); l.name != "8 h" || l.x <= bulkEnd || l.x >= absorptionEnd {
		t.Errorf("absorption phase label %+v, want 8 h within the absorption band", l)
	}
	if slices.Contains(labelValues(s.plot), "Repeat") || len(s.notes) != 0 {
		t.Errorf("repeated absorption shown while off: %v, notes %v", labelValues(s.plot), s.notes)
	}
	// The voltage line ends at the float voltage and reaches absorption.
	var volts plotLine
	for _, l := range s.plot.lines {
		if l.colour == theme.ColorNamePrimary && l.pts[0].Y > voltageBottom-0.01 && len(l.pts) > 2 {
			volts = l
		}
	}
	if len(volts.pts) == 0 || volts.pts[len(volts.pts)-1].Y != flt.y || !slices.ContainsFunc(volts.pts, func(p fyne.Position) bool { return p.Y == abs.y }) {
		t.Errorf("voltage line %v does not reach absorption %v and end at float %v", volts.pts, abs.y, flt.y)
	}

	// Adaptive, with repeated absorption: 1 h every 7 d.
	set := chargeSettings()
	set[10] = chartSetting{sc: 1, raw: 2}
	set[7] = chartSetting{sc: 1, raw: 60}
	set[8] = chartSetting{sc: 1, raw: 7 * 1440}
	s = chargeChart(chartDevice(set))
	if l := label(t, s.plot, "Absorption"); l.name != "up to 8 h" {
		t.Errorf("adaptive absorption time %q, want up to 8 h", l.name)
	}
	if l := label(t, s.plot, "Repeat"); l.name != "1 h" || l.x <= repeatStart || l.x >= repeatEnd {
		t.Errorf("repeated absorption label %+v", l)
	}
	if len(s.notes) != 1 || s.notes[0] != "Repeated absorption: 1 h every 7 d." {
		t.Errorf("notes %q", s.notes)
	}
	if !strings.Contains(s.subtitle, "Adaptive") {
		t.Errorf("subtitle %q, want the adaptive algorithm", s.subtitle)
	}

	// Without the charge current there is no current panel.
	set = chargeSettings()
	delete(set, 4)
	s = chargeChart(chartDevice(set))
	// The voltage panel takes the whole height instead.
	if slices.Contains(labelValues(s.plot), "Charge current") || label(t, s.plot, "13.80 V").y >= flt.y {
		t.Errorf("without a charge current: labels %v", labelValues(s.plot))
	}

	// Without a voltage there is no chart.
	for _, id := range []uint16{2, 3} {
		set = chargeSettings()
		delete(set, id)
		if chargeChart(chartDevice(set)) != nil {
			t.Errorf("a chart without setting %d", id)
		}
	}
}

func TestACInputChart(t *testing.T) {
	set := map[uint16]chartSetting{
		1: {sc: 1, raw: 0}, 5: {sc: 1, raw: 230},
		44: {sc: 1, raw: 180}, 45: {sc: 1, raw: 7}, 46: {sc: 1, raw: 265}, 47: {sc: 1, raw: 5},
	}
	d := chartDevice(set)
	s := acInputChart(d)
	if s == nil {
		t.Fatal("no chart for a model with settings 44 to 47")
	}
	// The reconnect levels are stored relative to the disconnect levels.
	off, on := labelsNamed(s.plot, "Disconnect"), labelsNamed(s.plot, "Reconnect")
	if len(off) != 2 || len(on) != 2 || off[0].value != "180 V" || off[1].value != "265 V" ||
		on[0].value != "187 V" || on[1].value != "260 V" {
		t.Fatalf("disconnect %v, reconnect %v; want 180 V, 265 V and 187 V, 260 V", off, on)
	}
	nominal := label(t, s.plot, "230 V")
	xs := []float32{off[0].x, on[0].x, nominal.x, on[1].x, off[1].x}
	if !slices.IsSorted(xs) || xs[0] <= 0 || xs[4] >= 1 {
		t.Errorf("positions %v are not in order within the scale", xs)
	}
	if on[0].place != placeAbove || off[0].place != placeBelow {
		t.Error("reconnect levels belong above the bar, disconnect levels below")
	}
	var accepted *plotArea
	for i, a := range s.plot.areas {
		if a.colour == theme.ColorNameSuccess {
			accepted = &s.plot.areas[i]
		}
	}
	if accepted == nil || accepted.x1 != on[0].x || accepted.x2 != on[1].x {
		t.Errorf("accepted band %+v, want from %v to %v", accepted, on[0].x, on[1].x)
	}
	if len(s.notes) != 0 {
		t.Errorf("notes %q without wide input frequency", s.notes)
	}

	// Flag 27, wide input frequency, is bit 11 of setting 1.
	set[1] = chartSetting{sc: 1, raw: 1 << 11}
	if s := acInputChart(chartDevice(set)); len(s.notes) != 1 || !strings.Contains(s.notes[0], "frequency") {
		t.Errorf("notes %q with wide input frequency", s.notes)
	}

	delete(set, 46)
	if acInputChart(chartDevice(set)) != nil {
		t.Error("a chart without setting 46")
	}
}

func TestBatteryChart(t *testing.T) {
	set := map[uint16]chartSetting{
		11: {sc: -100, raw: 930}, 12: {sc: -100, raw: 120},
		63: {sc: -100, offset: -0x8000, raw: 0x8000 + 20},
		2:  {sc: -100, raw: 1440}, 3: {sc: -100, raw: 1380},
	}
	s := batteryChart(chartDevice(set))
	if s == nil {
		t.Fatal("no chart for a model with setting 11")
	}
	// Restart is 11 + 12; the pre-alarm is the restart level + 63.
	want := []struct{ value, name string }{
		{"9.30 V", "Shut down"}, {"10.50 V", "Restart"}, {"10.70 V", "Pre-alarm"}, {"13.80 V", "Float"}, {"14.40 V", "Absorption"},
	}
	var xs []float32
	for i, w := range want {
		l := label(t, s.plot, w.value)
		if l.name != w.name {
			t.Errorf("%s is named %q, want %q", w.value, l.name, w.name)
		}
		// Neighbours are on different sides of the bar.
		if above := i%2 == 1; (l.place == placeAbove) != above {
			t.Errorf("%s place %v", w.value, l.place)
		}
		xs = append(xs, l.x)
	}
	if !slices.IsSorted(xs) {
		t.Errorf("positions %v are not in voltage order", xs)
	}
	var low *plotArea
	for i, a := range s.plot.areas {
		if a.colour == theme.ColorNameWarning {
			low = &s.plot.areas[i]
		}
	}
	if low == nil || low.x1 != xs[0] || low.x2 != xs[2] {
		t.Errorf("low battery zone %+v, want from the shut-down to the pre-alarm level", low)
	}

	if batteryChart(chartDevice(map[uint16]chartSetting{12: {sc: -100, raw: 120}})) != nil {
		t.Error("a chart without setting 11")
	}
}

// TestPageVisual checks that the charts are offered for their pages, hide
// without the settings they need and follow the model.
func TestPageVisual(t *testing.T) {
	u := newTestUI(t)
	if pageVisual(u, vebus.TabGeneral) != nil {
		t.Error("a chart for the General page")
	}
	o := pageVisual(u, vebus.TabCharger)
	c := chartViews[u][vebus.TabCharger]
	if o == nil || c == nil || o.Visible() {
		t.Fatal("the charger chart is missing, or shown without a model")
	}
	if pageVisual(u, vebus.TabCharger) != o {
		t.Error("a second chart for the same page")
	}
	d := chartDevice(chargeSettings())
	u.settings.load(d, nil)
	if !o.Visible() || !slices.Contains(labelValues(c.plot.scene), "14.40 V") {
		t.Fatalf("after loading: visible %v, labels %v", o.Visible(), labelValues(c.plot.scene))
	}
	if err := d.SetDisplay(vebus.SettingID(2), 14.6); err != nil {
		t.Fatal(err)
	}
	u.settings.changed()
	if !slices.Contains(labelValues(c.plot.scene), "14.60 V") {
		t.Errorf("after an edit: labels %v", labelValues(c.plot.scene))
	}
	u.settings.load(chartDevice(map[uint16]chartSetting{3: {sc: -100, raw: 1380}}), nil)
	if o.Visible() {
		t.Error("the chart is shown for a model without an absorption voltage")
	}
	if g := pageVisual(u, vebus.TabGrid); g == nil || g.Visible() {
		t.Error("the grid chart is missing, or shown without its settings")
	}
	if i := pageVisual(u, vebus.TabInverter); i == nil || i.Visible() {
		t.Error("the inverter chart is missing, or shown without its settings")
	}
}

func TestSpreadLabels(t *testing.T) {
	ls := []*placedLabel{
		{l: plotLabel{row: 1}, x: 100, w: 40},
		{l: plotLabel{row: 1}, x: 110, w: 40},
		{l: plotLabel{row: 1}, x: 280, w: 40},
		{l: plotLabel{row: 2}, x: 105, w: 40}, // another row: may overlap
	}
	spreadLabels(ls, 300)
	if ls[1].x < ls[0].x+ls[0].w || ls[2].x < ls[1].x+ls[1].w {
		t.Errorf("labels still overlap: %v %v %v", ls[0].x, ls[1].x, ls[2].x)
	}
	if ls[2].x+ls[2].w > 300 || ls[0].x < 0 {
		t.Errorf("labels outside the plot: %v %v", ls[0].x, ls[2].x+ls[2].w)
	}
	if ls[3].x != 105 {
		t.Errorf("a label of another row moved to %v", ls[3].x)
	}
}
