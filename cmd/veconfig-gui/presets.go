package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// Battery presets are named sets of charger values the user saved. They
// are stored in the app preferences only, never on a device or in a file.
// No presets are built in: we have no public source for battery values to
// cite.

// prefBatteryPresets is the preference that holds the presets as JSON.
const prefBatteryPresets = "charger.presets"

// presetSettings are the settings a preset holds: absorption and float
// voltage, absorption time and charge algorithm.
var presetSettings = []uint16{2, 3, 9, 10}

// presetTractionFlag is the traction battery curve, which applying a
// preset turns off.
const presetTractionFlag = 29

// batteryPreset is one saved preset. Values are display values of the
// model (vebus.Device.Display), by setting ID.
type batteryPreset struct {
	Name   string             `json:"name"`
	Values map[string]float64 `json:"values"`
}

// loadPresets returns the saved presets; a damaged preference counts as
// none.
func loadPresets(p fyne.Preferences) []batteryPreset {
	var ps []batteryPreset
	if s := p.String(prefBatteryPresets); s != "" {
		if json.Unmarshal([]byte(s), &ps) != nil {
			return nil
		}
	}
	return ps
}

func storePresets(p fyne.Preferences, ps []batteryPreset) {
	data, _ := json.Marshal(ps)
	p.SetString(prefBatteryPresets, string(data))
}

// presetFrom returns the preset settings of d as a preset named name.
func presetFrom(d *vebus.Device, name string) batteryPreset {
	ps := batteryPreset{Name: name, Values: map[string]float64{}}
	for _, id := range presetSettings {
		if v, err := d.Display(vebus.SettingID(id)); err == nil {
			ps.Values[strconv.Itoa(int(id))] = v
		}
	}
	return ps
}

// apply sets the preset's values in d and turns the traction curve off. It
// returns the values that could not be set.
func (ps batteryPreset) apply(d *vebus.Device) []error {
	var errs []error
	for _, id := range presetSettings {
		v, ok := ps.Values[strconv.Itoa(int(id))]
		if !ok || !d.Supported(vebus.SettingID(id)) {
			continue
		}
		if err := d.SetDisplay(vebus.SettingID(id), v); err != nil {
			errs = append(errs, err)
		}
	}
	if d.Supported(vebus.FlagID(presetTractionFlag)) {
		if err := d.SetFlag(presetTractionFlag, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// presetsBar returns the button that opens the battery presets.
func presetsBar(u *ui) fyne.CanvasObject {
	b := widget.NewButton(tr("Battery presets"), func() { showPresets(u) })
	update := func(bool) {
		d := u.settings.dev
		if d != nil && slices.ContainsFunc(presetSettings, func(id uint16) bool { return d.Supported(vebus.SettingID(id)) }) {
			b.Enable()
		} else {
			b.Disable()
		}
	}
	u.settings.onChange(update)
	update(true)
	return container.NewHBox(b)
}

// showPresets lets the user apply, save and delete presets.
func showPresets(u *ui) {
	prefs := u.app.Preferences()
	d := u.settings.dev
	if d == nil {
		return
	}
	names := func() []string {
		var n []string
		for _, p := range loadPresets(prefs) {
			n = append(n, p.Name)
		}
		return n
	}
	list := widget.NewSelect(names(), nil)
	list.PlaceHolder = tr("Choose a preset")
	selected := func() (int, []batteryPreset) {
		ps := loadPresets(prefs)
		return slices.IndexFunc(ps, func(p batteryPreset) bool { return p.Name == list.Selected }), ps
	}
	apply := widget.NewButton(tr("Apply"), func() {
		i, ps := selected()
		if i < 0 || u.settings.dev != d {
			return
		}
		if errs := ps[i].apply(d); len(errs) > 0 {
			showError(u, errors.Join(errs...))
		}
		u.settings.changed()
	})
	remove := widget.NewButton(tr("Delete"), func() {
		if i, ps := selected(); i >= 0 {
			storePresets(prefs, slices.Delete(ps, i, i+1))
			list.ClearSelected()
			list.SetOptions(names())
		}
	})
	name := widget.NewEntry()
	name.SetPlaceHolder(tr("Name for the current values"))
	save := widget.NewButton(tr("Save current values"), func() {
		if name.Text == "" || u.settings.dev != d {
			return
		}
		ps := loadPresets(prefs)
		ps = slices.DeleteFunc(ps, func(p batteryPreset) bool { return p.Name == name.Text })
		storePresets(prefs, append(ps, presetFrom(d, name.Text)))
		list.SetOptions(names())
		list.SetSelected(name.Text)
	})
	help := widget.NewLabel(tr("A preset holds the absorption and float voltages, the absorption time and the charge algorithm. " +
		"Applying one also turns the traction battery curve off. Presets are kept on this computer only."))
	help.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(help,
		container.NewBorder(nil, nil, nil, container.NewHBox(apply, remove), list),
		container.NewBorder(nil, nil, nil, save, name))
	dlg := dialog.NewCustom(tr("Battery presets"), tr("Close"), content, u.win)
	dlg.Resize(fyne.NewSize(520, dlg.MinSize().Height))
	dlg.Show()
}
