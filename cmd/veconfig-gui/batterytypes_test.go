package main

import (
	"math"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// typeDevice returns a synthetic device for a battery of cells cells (6, 12
// or 24) with every parameter of a battery type, the traction curve on and
// the storage phase, lithium and VE.Bus BMS flags off. All values are
// invented.
func typeDevice(cells int) *vebus.Device {
	k := uint16(cells / 6)
	d := vebus.NewDevice(nil)
	d.AddSetting(0, mk2.SettingInfo{Sc: 1, Max: 0x69FF}, 0x0080) // flag 11 supported, off
	d.AddSetting(1, mk2.SettingInfo{Sc: 1, Max: 0x7000}, 1<<13)  // flag 29 on
	d.AddSetting(60, mk2.SettingInfo{Sc: 1, Max: 0x0090}, 0)     // flags 36 and 39 supported, off
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Default: 1440 * k, Min: 800 * k, Max: 1750 * k}, 1440*k)
	d.AddSetting(3, mk2.SettingInfo{Sc: -100, Default: 1380 * k, Min: 800 * k, Max: 1750 * k}, 1380*k)
	d.AddSetting(7, mk2.SettingInfo{Sc: 1, Default: 60, Max: 1440}, 60)           // minutes
	d.AddSetting(8, mk2.SettingInfo{Sc: 60, Default: 168, Min: 1, Max: 720}, 168) // hours
	d.AddSetting(9, mk2.SettingInfo{Sc: 1, Default: 480, Max: 1440}, 480)         // minutes
	d.AddSetting(10, mk2.SettingInfo{Sc: 1, Default: 3, Min: 1, Max: 3}, 3)
	d.AddSetting(11, mk2.SettingInfo{Sc: -100, Default: 930 * k, Min: 800 * k, Max: 1300 * k}, 930*k)
	d.AddSetting(65, mk2.SettingInfo{Sc: 1, Default: 85, Max: 100}, 85)
	d.AddSetting(72, mk2.SettingInfo{Sc: -100, Default: 80, Min: 50, Max: 100}, 80)
	d.AddSetting(86, mk2.SettingInfo{Sc: -2, Offset: -60, Default: 70, Max: 160}, 70) // -30 to +50 °C; +5 °C
	d.AddSetting(87, mk2.SettingInfo{Sc: -10000, Default: 162 * k, Max: 1000}, 162*k)
	return d
}

// builtinIndex returns the index of the built-in type named name.
func builtinIndex(t *testing.T, name string) int {
	t.Helper()
	i := slices.IndexFunc(builtinTypes(), func(bt batteryType) bool { return bt.name == name })
	if i < 0 {
		t.Fatalf("no built-in type %q", name)
	}
	return i
}

func builtin(t *testing.T, name string) batteryType {
	t.Helper()
	return builtinTypes()[builtinIndex(t, name)]
}

func displayOf(t *testing.T, d *vebus.Device, id vebus.ID) float64 {
	t.Helper()
	v, err := d.Display(id)
	if err != nil {
		t.Fatalf("%v: %v", id, err)
	}
	return v
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestBuiltinTypes(t *testing.T) {
	types := builtinTypes()
	if len(types) != 9 {
		t.Fatalf("%d built-in types, want 9", len(types))
	}
	for _, bt := range types {
		if bt.name == "" || bt.desc == "" || bt.user {
			t.Errorf("built-in type %+v: want a name, a description and not user", bt)
		}
		for i, sv := range bt.values {
			if sv.mode == slotDefault {
				t.Errorf("%s: slot %s uses the device default; built-in types never do", bt.name, typeSlots[i].key)
			}
		}
	}
}

func TestBatteryCells(t *testing.T) {
	for _, cells := range []int{6, 12, 24} {
		d := typeDevice(cells)
		if got := batteryCells(d); got != cells {
			t.Errorf("device for %d cells: batteryCells = %d", cells, got)
		}
		if got := voltageScale(d); got != float64(cells)/6 {
			t.Errorf("device for %d cells: voltageScale = %v", cells, got)
		}
	}
	// Without setting 11, the float voltage's default tells the class.
	d := vebus.NewDevice(nil)
	d.AddSetting(3, mk2.SettingInfo{Sc: -100, Default: 2760, Min: 1600, Max: 3500}, 2760)
	if got := batteryCells(d); got != 12 {
		t.Errorf("float default 27.6 V: batteryCells = %d, want 12", got)
	}
	if got := batteryCells(vebus.NewDevice(nil)); got != 6 {
		t.Errorf("no voltage settings: batteryCells = %d, want 6", got)
	}
}

// TestApplyTypeScaled applies a lead-acid type to a 12, 24 and 48 V device:
// the voltages and the temperature compensation are scaled, the other
// values are not, exactly the differing parameters and the traction curve
// change.
func TestApplyTypeScaled(t *testing.T) {
	s, f := vebus.SettingID, vebus.FlagID
	for _, cells := range []int{6, 12, 24} {
		k := float64(cells) / 6
		d := typeDevice(cells)
		res := builtin(t, "AGM fast recharge, tubular semi-float, spiral AGM").apply(d)
		if len(res.errs) > 0 || len(res.limited) > 0 || res.noBMS {
			t.Fatalf("%d cells: %+v", cells, res)
		}
		if want := []vebus.ID{s(2), s(9), f(11), f(29)}; !slices.Equal(res.changed, want) {
			t.Errorf("%d cells: changed %v, want %v", cells, res.changed, want)
		}
		for id, want := range map[vebus.ID]float64{s(2): 14.7 * k, s(3): 13.8 * k, s(9): 300, s(7): 60, s(8): 10080,
			f(11): 1, f(36): 0, s(10): 3, s(65): 85, s(72): 0.8, s(87): 0.0162 * k, s(86): 5, f(39): 0, f(29): 0} {
			if got := displayOf(t, d, id); !near(got, want) {
				t.Errorf("%d cells: %v = %v, want %v", cells, id, got, want)
			}
			if d.Dirty(id) != slices.Contains(res.changed, id) {
				t.Errorf("%d cells: %v dirty %v, want it marked as changed only if it changed", cells, id, d.Dirty(id))
			}
		}
	}

	// A lithium type on the 48 V device.
	d := typeDevice(24)
	res := builtin(t, "LFP lithium, generic").apply(d)
	if want := []vebus.ID{s(2), s(3), s(9), f(36), s(10), s(65), s(72), f(29)}; !slices.Equal(res.changed, want) {
		t.Errorf("LFP on 48 V: changed %v, want %v", res.changed, want)
	}
	for id, want := range map[vebus.ID]float64{s(2): 56.8, s(3): 54, s(9): 60, s(10): 1, s(65): 95, s(72): 0.95, s(87): 0.0648, s(86): 5} {
		if got := displayOf(t, d, id); !near(got, want) {
			t.Errorf("LFP on 48 V: %v = %v, want %v", id, got, want)
		}
	}
}

// TestApplyTypeSimulator applies a type to the model read from the
// simulator's 12 V device, which lacks most parameters: those are skipped.
func TestApplyTypeSimulator(t *testing.T) {
	d := readModel(t, settingsSession(t), 0)
	if err := d.SetFlag(typeTractionFlag, true); err != nil {
		t.Fatal(err)
	}
	res := builtin(t, "AGM fast recharge, tubular semi-float, spiral AGM").apply(d)
	if len(res.errs) > 0 || len(res.limited) > 0 || res.noBMS {
		t.Fatalf("result %+v", res)
	}
	if want := []vebus.ID{vebus.SettingID(2), vebus.FlagID(11), vebus.FlagID(29)}; !slices.Equal(res.changed, want) {
		t.Errorf("changed %v, want %v", res.changed, want)
	}
	if v := displayOf(t, d, vebus.SettingID(2)); !near(v, 14.7) {
		t.Errorf("absorption = %v, want 14.7", v)
	}
	if i := matchType(d, builtinTypes()); i != builtinIndex(t, "AGM fast recharge, tubular semi-float, spiral AGM") {
		t.Errorf("after applying, the match is %d", i)
	}
	// The BMS type on a device without the VE.Bus BMS flag: plain lithium,
	// with the notice.
	if res := builtin(t, "LFP lithium with a VE.Bus BMS").apply(d); !res.noBMS || len(res.errs) > 0 {
		t.Errorf("BMS type on the simulator: %+v, want the VE.Bus BMS notice", res)
	}
}

func TestApplyTypeBMS(t *testing.T) {
	d := typeDevice(6)
	res := builtin(t, "LFP lithium with a VE.Bus BMS").apply(d)
	if res.noBMS || len(res.errs) > 0 {
		t.Fatalf("result %+v", res)
	}
	for id, want := range map[vebus.ID]float64{vebus.FlagID(39): 1, vebus.FlagID(36): 1, vebus.SettingID(86): -20.5} {
		if got := displayOf(t, d, id); !near(got, want) {
			t.Errorf("%v = %v, want %v", id, got, want)
		}
	}
}

// TestApplyTypeLimited applies a type whose absorption voltage is above the
// device's maximum: it is limited, and the result dialog says so.
func TestApplyTypeLimited(t *testing.T) {
	u := newTestUI(t)
	d := typeDevice(6)
	d.AddSetting(2, mk2.SettingInfo{Sc: -100, Default: 1440, Min: 800, Max: 1450}, 1440)
	u.settings.load(d, nil)
	bt := builtin(t, "AGM fast recharge, tubular semi-float, spiral AGM")
	applyBatteryType(u, d, bt)
	if raw, _ := d.Raw(vebus.SettingID(2)); raw != 1450 {
		t.Errorf("absorption raw %d, want the maximum 1450", raw)
	}
	text := dialogTexts(u.win.Canvas().Overlays().Top())
	for _, want := range []string{"“AGM fast recharge, tubular semi-float, spiral AGM” was applied.",
		"1 value of the type is outside this device's range and was set to the nearest limit.",
		"limited", "The type asks for 14.70 V; this device allows 8.00 V to 14.50 V.", "14.40 V", "14.50 V",
		"Traction battery charge curve", "The settings now match no type."} {
		if !strings.Contains(text, want) {
			t.Errorf("result dialog does not show %q:\n%s", want, text)
		}
	}
}

// TestMatchType checks the matching: every built-in type matches after it
// is applied; a value one step off does not match; leave-unchanged slots are
// not compared; the type comparing the most slots wins, the earlier one on
// a tie; a device-default slot compares with the device default.
func TestMatchType(t *testing.T) {
	types := builtinTypes()
	d := typeDevice(24)
	if i := matchType(d, types); i != -1 {
		t.Errorf("fresh device matches %q", types[i].name)
	}
	for i, bt := range types {
		bt.apply(d)
		if got := matchType(d, types); got != i {
			t.Errorf("after applying %q the match is %d, want %d", bt.name, got, i)
		}
	}

	gel := builtinIndex(t, "Gel/AGM deep-cycle, standard")
	d = typeDevice(6)
	types[gel].apply(d)
	if got := matchType(d, types); got != gel {
		t.Fatalf("match %d, want %d", got, gel)
	}
	_ = d.SetRaw(vebus.SettingID(2), 1441) // one step off: more than half a step
	if got := matchType(d, types); got != -1 {
		t.Errorf("absorption one step off matches %q", types[got].name)
	}
	_ = d.SetRaw(vebus.SettingID(2), 1440)

	abs := func(name string, v float64, float slotValue) batteryType {
		bt := batteryType{name: name, user: true}
		bt.values[slotAbsorption] = slotValue{mode: slotSet, v: v}
		bt.values[slotFloat] = float
		return bt
	}
	keep := slotValue{}
	if got := matchType(d, []batteryType{abs("a", 14.404, keep)}); got != 0 {
		t.Error("a value within half a step does not match")
	}
	if got := matchType(d, []batteryType{abs("a", 14.406, keep)}); got != -1 {
		t.Error("a value more than half a step off matches")
	}
	float := slotValue{mode: slotSet, v: 13.8}
	if got := matchType(d, []batteryType{abs("few", 14.4, keep), abs("more", 14.4, float)}); got != 1 {
		t.Errorf("match %d, want the type comparing more slots", got)
	}
	if got := matchType(d, []batteryType{abs("first", 14.4, float), abs("second", 14.4, float)}); got != 0 {
		t.Errorf("match %d on a tie, want the earlier type", got)
	}
	def := abs("default", 0, keep)
	def.values[slotAbsorption] = slotValue{mode: slotDefault}
	if got := matchType(d, []batteryType{def}); got != 0 {
		t.Error("a device-default slot does not match the device default")
	}
	_ = d.SetRaw(vebus.SettingID(2), 1460)
	if got := matchType(d, []batteryType{def}); got != -1 {
		t.Error("a device-default slot matches another value")
	}
}

func TestUserTypesMigration(t *testing.T) {
	prefs := test.NewTempApp(t).Preferences()
	prefs.SetString(prefLegacyPresets, `[{"name":"Old 24","values":{"2":28.8,"3":27.6,"9":480,"10":3}},{"name":"Old 12","values":{"2":14.1}}]`)
	for range 2 { // a second load finds them migrated
		types := loadUserTypes(prefs)
		if len(types) != 2 || types[0].name != "Old 24" || types[1].name != "Old 12" || !types[0].user {
			t.Fatalf("migrated types %+v", types)
		}
		want := map[int]float64{slotAbsorption: 14.4, slotFloat: 13.8, slotAbsorptionTime: 8, slotChargeCurve: 3}
		for i, sv := range types[0].values {
			if w, ok := want[i]; ok != (sv.mode == slotSet) || ok && !near(sv.v, w) {
				t.Errorf("Old 24 slot %s = %+v, want %v (set: %v)", typeSlots[i].key, sv, w, ok)
			}
		}
		for i, sv := range types[1].values {
			if (i == slotAbsorption) != (sv.mode == slotSet) || i == slotAbsorption && !near(sv.v, 14.1) {
				t.Errorf("Old 12 slot %s = %+v", typeSlots[i].key, sv)
			}
		}
	}
	if prefs.String(prefLegacyPresets) != "" {
		t.Error("the old presets were left in the preferences")
	}
}

// TestUserTypeStore saves the values of the simulator's device as a type:
// the parameters it lacks use the device default; the type survives a
// store and load and matches the device.
func TestUserTypeStore(t *testing.T) {
	prefs := test.NewTempApp(t).Preferences()
	d := readModel(t, settingsSession(t), 0)
	bt := typeFromDevice(d, "Mine")
	for i, s := range typeSlots {
		if want := map[bool]slotMode{true: slotSet, false: slotDefault}[d.Supported(s.id)]; bt.values[i].mode != want {
			t.Errorf("slot %s: mode %d, want %d", s.key, bt.values[i].mode, want)
		}
	}
	storeUserTypes(prefs, []batteryType{bt})
	got := loadUserTypes(prefs)
	if len(got) != 1 || got[0] != bt {
		t.Fatalf("loaded %+v, want %+v", got, bt)
	}
	if i := matchType(d, append(builtinTypes(), got...)); i != len(builtinTypes()) {
		t.Errorf("match %d, want the saved type", i)
	}
}

// choiceCards returns the battery type cards under o.
func choiceCards(o fyne.CanvasObject) []*choiceCard {
	var cards []*choiceCard
	for _, c := range findObjects(o, func(o fyne.CanvasObject) bool { _, ok := o.(*choiceCard); return ok }) {
		cards = append(cards, c.(*choiceCard))
	}
	return cards
}

// TestBatteryTypeDialog drives the card of the Charger page and the dialog:
// choose and apply a type with Enter, save the current values as a type,
// remove it, close with Escape.
func TestBatteryTypeDialog(t *testing.T) {
	u := newTestUI(t)
	d := typeDevice(6)
	u.settings.load(d, nil)
	card := batteryTypeCard(u)
	if text := dialogTexts(card); !strings.Contains(text, "No matching type (custom values)") {
		t.Errorf("card shows %q", text)
	}

	test.Tap(card.(*choiceCard))
	dlg := u.win.Canvas().Overlays().Top()
	text := dialogTexts(dlg)
	for _, want := range []string{"The current values match no type (custom values).", "Built-in types", "Your types",
		"LFP lithium, generic", "Absorption 14.20 V · Float 13.50 V · 1 h absorption · Fixed", "Lithium", "VE.Bus BMS",
		"Values are shown for the 12 V battery of this device.", "You have no types of your own yet."} {
		if !strings.Contains(text, want) {
			t.Errorf("dialog does not show %q", want)
		}
	}
	if !findButton(t, dlg, "Apply").Disabled() {
		t.Error("Apply is enabled with no type chosen")
	}
	gel := builtinIndex(t, "Gel/AGM deep-cycle, standard")
	test.Tap(choiceCards(dlg)[gel])
	pressKey(u, fyne.KeyReturn)
	if v := displayOf(t, d, vebus.FlagID(11)); v != 1 || !d.Dirty(vebus.FlagID(11)) {
		t.Error("Enter did not apply the chosen type")
	}
	result := u.win.Canvas().Overlays().Top()
	if text := dialogTexts(result); !strings.Contains(text, "The settings now match “Gel/AGM deep-cycle, standard”.") {
		t.Errorf("result dialog shows %q", text)
	}
	pressKey(u, fyne.KeyEscape)
	if text := dialogTexts(card); !strings.Contains(text, "Gel/AGM deep-cycle, standard") {
		t.Errorf("card shows %q after applying", text)
	}

	// The current values match a type: they cannot be saved as a new one.
	test.Tap(card.(*choiceCard))
	dlg = u.win.Canvas().Overlays().Top()
	if !findButton(t, dlg, "Save current values as a type…").Disabled() || !strings.Contains(dialogTexts(dlg), "current") {
		t.Error("Save is enabled while a type matches, or the match is not marked current")
	}
	pressKey(u, fyne.KeyEscape)

	// Custom values: save them as a type, which then matches.
	set(t, d, 2, "14.5")
	u.settings.changed()
	if text := dialogTexts(card); !strings.Contains(text, "No matching type") {
		t.Errorf("card shows %q after an edit", text)
	}
	test.Tap(card.(*choiceCard))
	dlg = u.win.Canvas().Overlays().Top()
	test.Tap(findButton(t, dlg, "Save current values as a type…"))
	named := u.win.Canvas().Overlays().Top()
	entry := findObjects(named, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Entry); return ok })[0].(*widget.Entry)
	entry.SetText("Gel/AGM deep-cycle, standard")
	if !findButton(t, named, "Save").Disabled() {
		t.Error("a built-in type's name is accepted")
	}
	entry.SetText("My gel")
	pressKey(u, fyne.KeyReturn)
	if u.win.Canvas().Overlays().Top() != dlg {
		t.Fatal("the name dialog did not close")
	}
	if types := loadUserTypes(u.app.Preferences()); len(types) != 1 || types[0].name != "My gel" {
		t.Fatalf("user types %+v", types)
	}
	if text := dialogTexts(dlg); !strings.Contains(text, "The current values match “My gel”.") {
		t.Errorf("after saving the dialog shows %q", text)
	}

	// Remove it, confirming with Enter.
	remove := findObjects(dlg, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == "" })
	if len(remove) != 1 {
		t.Fatalf("%d remove buttons, want 1", len(remove))
	}
	test.Tap(remove[0].(*widget.Button))
	pressKey(u, fyne.KeyReturn)
	if types := loadUserTypes(u.app.Preferences()); len(types) != 0 {
		t.Errorf("after removing: user types %+v", types)
	}
	if text := dialogTexts(dlg); !strings.Contains(text, "You have no types of your own yet.") {
		t.Errorf("after removing the dialog shows %q", text)
	}
	pressKey(u, fyne.KeyEscape)
	if u.win.Canvas().Overlays().Top() != nil || u.win.Canvas().OnTypedKey() != nil {
		t.Error("Escape did not close the dialog, or left its key handler")
	}
	if text := dialogTexts(card); !strings.Contains(text, "No matching type") {
		t.Errorf("card shows %q after removing the matching type", text)
	}
}
